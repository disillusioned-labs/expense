package document

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/disillusioned-labs/platform/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	"github.com/disillusioned-labs/expense/internal/authz"
	"github.com/disillusioned-labs/expense/internal/constant"
	"github.com/disillusioned-labs/expense/internal/contract"
	"github.com/disillusioned-labs/expense/internal/ocr"
	"github.com/disillusioned-labs/expense/internal/repository"
	"github.com/disillusioned-labs/expense/internal/service"
	"github.com/disillusioned-labs/platform/pgutil"
)

var tracer = otel.Tracer("service/document")

type DocumentService interface {
	UploadToProject(ctx context.Context, actor authz.Actor, projectID uuid.UUID, in UploadInput) (Document, error)
	AttachToTransaction(ctx context.Context, actor authz.Actor, transactionID uuid.UUID, in UploadInput) (Document, error)
	Get(ctx context.Context, actor authz.Actor, id uuid.UUID) (Document, error)
	ListByProject(ctx context.Context, actor authz.Actor, projectID uuid.UUID) ([]Document, error)
	ListByTransaction(ctx context.Context, actor authz.Actor, transactionID uuid.UUID) ([]Document, error)
	Delete(ctx context.Context, actor authz.Actor, id uuid.UUID) error
}

type documentService struct {
	repo     repository.Store
	authz    *authz.Authorizer
	storage  *s3.Client
	ocr      ocr.Submitter
	identity contract.IdentityClient
	log      *slog.Logger
}

func NewDocumentService(repo repository.Store, authorizer *authz.Authorizer, storage *s3.Client, submitter ocr.Submitter, identity contract.IdentityClient, log *slog.Logger) DocumentService {
	return &documentService{repo: repo, authz: authorizer, storage: storage, ocr: submitter, identity: identity, log: log}
}

func toDoc(d repository.Document) Document {
	out := Document{
		ID:        d.ID,
		FileName:  d.FileName,
		MimeType:  d.MimeType,
		FileSize:  d.FileSize,
		OcrStatus: d.OcrStatus,
		CreatedAt: d.CreatedAt,
	}
	if d.TransactionID != nil {
		out.TransactionID = *d.TransactionID
	}
	// FileURL is set by the service via store.URL (presigned on the s3
	// engine) - toDoc stays a pure row mapping.
	return out
}

func (s *documentService) fileURL(ctx context.Context, path, filename string) string {
	duration := time.Duration(constant.DocumentPresignTTL) * time.Minute
	url, err := s.storage.PresignGet(ctx, constant.DocumentBucket, path, duration, filename)
	if err != nil {
		s.log.WarnContext(ctx, "presign failed;", "error", err, "path", path)
		return ""
	}
	return url
}

// The sqlc rows carry the same fields as repository.Document but in query
// order, so each query gets a small adapter instead of one struct conversion.
func createdRowToDoc(d repository.CreateDocumentRow) Document {
	return toDoc(repository.Document{
		ID: d.ID, TransactionID: d.TransactionID, StoragePath: d.StoragePath,
		FileName: d.FileName, MimeType: d.MimeType, FileSize: d.FileSize,
		OcrStatus: d.OcrStatus, CreatedAt: d.CreatedAt,
	})
}

func getRowToDoc(d repository.GetDocumentRow) Document {
	return toDoc(repository.Document{
		ID: d.ID, TransactionID: d.TransactionID, StoragePath: d.StoragePath,
		FileName: d.FileName, MimeType: d.MimeType, FileSize: d.FileSize,
		OcrStatus: d.OcrStatus, CreatedAt: d.CreatedAt,
	})
}

func listRowToDoc(d repository.ListDocumentsByProjectRow) Document {
	return toDoc(repository.Document{
		ID: d.ID, TransactionID: d.TransactionID, StoragePath: d.StoragePath,
		FileName: d.FileName, MimeType: d.MimeType, FileSize: d.FileSize,
		OcrStatus: d.OcrStatus, CreatedAt: d.CreatedAt,
	})
}

func listTxRowToDoc(d repository.ListDocumentsByTransactionRow) Document {
	return toDoc(repository.Document{
		ID: d.ID, TransactionID: d.TransactionID, StoragePath: d.StoragePath,
		FileName: d.FileName, MimeType: d.MimeType, FileSize: d.FileSize,
		OcrStatus: d.OcrStatus, CreatedAt: d.CreatedAt,
	})
}

func (s *documentService) snapshot(ctx context.Context, actor authz.Actor) (uuid.UUID, string, string, string) {
	users, err := s.identity.GetUsersInfo(ctx, []contract.UserOrgPair{
		{UserID: actor.UserID, OrganizationID: actor.OrgID},
	})
	if err != nil || len(users) == 0 {
		return actor.UserID, "unknown", "unknown", actor.Role
	}
	u := users[0]
	return actor.UserID, u.Name, u.Email, u.Role
}

func (s *documentService) UploadToProject(ctx context.Context, actor authz.Actor, projectID uuid.UUID, in UploadInput) (Document, error) {
	ctx, span := tracer.Start(ctx, "DocumentService.UploadToProject")
	defer span.End()

	project, err := s.loadProject(ctx, actor, projectID)
	if err != nil {
		return Document{}, err
	}
	return s.upload(ctx, actor, project.ID, nil, in)
}

func (s *documentService) AttachToTransaction(ctx context.Context, actor authz.Actor, transactionID uuid.UUID, in UploadInput) (Document, error) {
	ctx, span := tracer.Start(ctx, "DocumentService.AttachToTransaction")
	defer span.End()

	if err := s.authz.CheckOrgWrite(ctx, actor); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "check org write failed")
		s.log.ErrorContext(ctx, "check org write failed", "error", err)
		return Document{}, err
	}

	t, err := s.repo.GetTransaction(ctx, repository.GetTransactionParams{ID: transactionID, OrganizationID: actor.OrgID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Document{}, service.ErrNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "get transaction failed")
		s.log.ErrorContext(ctx, "get transaction failed", "error", err)
		return Document{}, err
	}
	if t.Status != constant.StatusDraft {
		return Document{}, service.ErrTransactionLocked
	}

	return s.upload(ctx, actor, t.ProjectID, &transactionID, in)
}

// loadProject resolves and authorizes the upload target: the project must
// exist in the actor's organization, not be archived, and grant document.upload.
func (s *documentService) loadProject(ctx context.Context, actor authz.Actor, projectID uuid.UUID) (repository.Project, error) {
	ctx, span := tracer.Start(ctx, "DocumentService.loadProject")
	defer span.End()

	project, err := s.repo.GetProject(ctx, repository.GetProjectParams{ID: projectID, OrganizationID: actor.OrgID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.Project{}, service.ErrNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "get project failed")
		s.log.ErrorContext(ctx, "get project failed", "error", err)
		return repository.Project{}, err
	}
	if project.ArchivedAt.Valid {
		return repository.Project{}, service.ErrProjectArchived
	}
	if err := s.authz.CheckOrgWrite(ctx, actor); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "check org write failed")
		s.log.ErrorContext(ctx, "check org write failed", "error", err)
		return repository.Project{}, err
	}
	if err := s.authz.Can(ctx, actor, project.ID, constant.ActionDocumentUpload); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "check permission failed")
		s.log.ErrorContext(ctx, "check permission failed", "error", err)
		return repository.Project{}, err
	}
	return project, nil
}

func (s *documentService) upload(ctx context.Context, actor authz.Actor, projectID uuid.UUID, transactionID *uuid.UUID, in UploadInput) (Document, error) {
	_, span := tracer.Start(ctx, "DocumentService.upload")
	defer span.End()

	fileName, mimeType, size, content := in.FileName, in.MimeType, in.Size, in.Content
	docID := uuid.Must(uuid.NewV7())
	key := fmt.Sprintf("%s/%s/%s%s", actor.OrgID, projectID, docID, fileExt(fileName))

	hasher := sha256.New()
	path, err := s.storage.Put(ctx, constant.DocumentBucket, key, size, mimeType, io.TeeReader(content, hasher))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "save file failed")
		s.log.ErrorContext(ctx, "save file failed", "error", err)
		return Document{}, err
	}

	hash := hex.EncodeToString(hasher.Sum(nil))
	uploadedBy, uploadedByName, uploadedByEmail, uploadedByRole := s.snapshot(ctx, actor)

	var created repository.CreateDocumentRow
	if transactionID == nil {
		created, err = s.repo.CreateDocument(ctx, repository.CreateDocumentParams{
			ProjectID:       projectID,
			TransactionID:   nil,
			StoragePath:     path,
			FileName:        fileName,
			FileSize:        size,
			MimeType:        mimeType,
			FileHash:        pgutil.String(hash),
			UploadedBy:      uploadedBy,
			UploadedByName:  uploadedByName,
			UploadedByEmail: uploadedByEmail,
			UploadedByRole:  uploadedByRole,
		})
	} else {
		// The document binds to a draft that must stay draft across the
		// check-then-insert, so both run inside one locked transaction.
		err = s.repo.ExecTx(ctx, func(q repository.Querier) error {
			locked, lockErr := q.LockTransaction(ctx, *transactionID)
			if lockErr != nil {
				return lockErr
			}
			if locked.OrganizationID != actor.OrgID || locked.DeletedAt.Valid {
				return service.ErrNotFound
			}
			if locked.Status != constant.StatusDraft {
				return service.ErrTransactionLocked
			}
			created, lockErr = q.CreateDocument(ctx, repository.CreateDocumentParams{
				ProjectID:       projectID,
				TransactionID:   transactionID,
				StoragePath:     path,
				FileName:        fileName,
				FileSize:        size,
				MimeType:        mimeType,
				FileHash:        pgutil.String(hash),
				UploadedBy:      uploadedBy,
				UploadedByName:  uploadedByName,
				UploadedByEmail: uploadedByEmail,
				UploadedByRole:  uploadedByRole,
			})
			return lockErr
		})
	}
	if err != nil {
		errDelete := s.storage.Delete(ctx, constant.DocumentBucket, path)
		if errDelete != nil {
			s.log.WarnContext(ctx, "delete failed;", "error", err, "path", path)
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "create document failed")
		s.log.ErrorContext(ctx, "create document failed", "error", err)
		return Document{}, err
	}

	ocrDocID, submitErr := s.ocr.SubmitDocument(ctx, ocr.SubmitInput{
		IdempotencyKey: created.ID.String(),
		ExternalRef:    created.ID.String(),
		DocType:        "receipt",
		Bucket:         actor.OrgID.String(),
		StoragePath:    path,
		SizeBytes:      size,
		DeclaredMime:   mimeType,
	})
	if submitErr != nil {
		s.log.ErrorContext(ctx, "ocr submit failed; document stays pending", "error", submitErr, "document_id", created.ID)
	} else if ocrDocID != "" {
		// Persist the ocr job id so the reconciliation sweep can ask the
		// gateway about this document if its routed event never arrives. The
		// document exists already; a failed save only degrades the sweep.
		ocrID := uuid.MustParse(ocrDocID)
		if _, saveErr := s.repo.SetOcrDocumentID(ctx, repository.SetOcrDocumentIDParams{
			ID:            created.ID,
			OcrDocumentID: &ocrID,
		}); saveErr != nil {
			s.log.WarnContext(ctx, "saving ocr document id failed; sweep will not cover this document",
				"error", saveErr, "document_id", created.ID)
		}
	}
	doc := createdRowToDoc(created)
	doc.FileURL = s.fileURL(ctx, created.StoragePath, created.FileName)
	return doc, nil
}

func (s *documentService) Get(ctx context.Context, actor authz.Actor, id uuid.UUID) (Document, error) {
	ctx, span := tracer.Start(ctx, "DocumentService.Get")
	defer span.End()

	d, err := s.getAuthorized(ctx, actor, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "get failed")
		s.log.ErrorContext(ctx, "get failed", "error", err)
		return Document{}, err
	}

	doc := getRowToDoc(d)
	doc.FileURL = s.fileURL(ctx, d.StoragePath, d.FileName)
	return doc, nil
}

func (s *documentService) ListByProject(ctx context.Context, actor authz.Actor, projectID uuid.UUID) ([]Document, error) {
	ctx, span := tracer.Start(ctx, "DocumentService.ListByProject")
	defer span.End()

	if _, err := s.loadProjectForView(ctx, actor, projectID); err != nil {
		return nil, err
	}

	docs, err := s.repo.ListDocumentsByProject(ctx, projectID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "list documents by project failed")
		s.log.ErrorContext(ctx, "list documents by project failed", "error", err)
		return nil, err
	}

	out := make([]Document, 0, len(docs))
	for _, d := range docs {
		doc := listRowToDoc(d)
		doc.FileURL = s.fileURL(ctx, d.StoragePath, d.FileName)
		out = append(out, doc)
	}
	return out, nil
}

func (s *documentService) ListByTransaction(ctx context.Context, actor authz.Actor, transactionID uuid.UUID) ([]Document, error) {
	ctx, span := tracer.Start(ctx, "DocumentService.ListByTransaction")
	defer span.End()

	t, err := s.repo.GetTransaction(ctx, repository.GetTransactionParams{ID: transactionID, OrganizationID: actor.OrgID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, service.ErrNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "get transaction failed")
		s.log.ErrorContext(ctx, "get transaction failed", "error", err)
		return nil, err
	}

	if err := s.authz.Can(ctx, actor, t.ProjectID, constant.ActionTransactionView); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "check permission failed")
		s.log.ErrorContext(ctx, "check permission failed", "error", err)
		return nil, err
	}

	docs, err := s.repo.ListDocumentsByTransaction(ctx, &transactionID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "list documents by transaction failed")
		s.log.ErrorContext(ctx, "list documents by transaction failed", "error", err)
		return nil, err
	}

	out := make([]Document, 0, len(docs))
	for _, d := range docs {
		doc := listTxRowToDoc(d)
		doc.FileURL = s.fileURL(ctx, d.StoragePath, d.FileName)
		out = append(out, doc)
	}
	return out, nil
}

// loadProjectForView resolves the project for a read-only path: it must exist
// in the actor's organization and grant transaction.view.
func (s *documentService) loadProjectForView(ctx context.Context, actor authz.Actor, projectID uuid.UUID) (repository.Project, error) {
	ctx, span := tracer.Start(ctx, "DocumentService.loadProjectForView")
	defer span.End()

	project, err := s.repo.GetProject(ctx, repository.GetProjectParams{ID: projectID, OrganizationID: actor.OrgID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.Project{}, service.ErrNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "get project failed")
		s.log.ErrorContext(ctx, "get project failed", "error", err)
		return repository.Project{}, err
	}
	if err := s.authz.Can(ctx, actor, project.ID, constant.ActionTransactionView); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "check permission failed")
		s.log.ErrorContext(ctx, "check permission failed", "error", err)
		return repository.Project{}, err
	}
	return project, nil
}

func (s *documentService) getAuthorized(ctx context.Context, actor authz.Actor, id uuid.UUID) (repository.GetDocumentRow, error) {
	ctx, span := tracer.Start(ctx, "DocumentService.getAuthorized")
	defer span.End()

	// GetDocument scopes by organization through the document's own project,
	// so it works for project-level documents with no transaction yet.
	d, err := s.repo.GetDocument(ctx, repository.GetDocumentParams{ID: id, OrganizationID: actor.OrgID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.GetDocumentRow{}, service.ErrNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "get document failed")
		s.log.ErrorContext(ctx, "get document failed", "error", err)
		return repository.GetDocumentRow{}, err
	}

	if err := s.authz.Can(ctx, actor, d.ProjectID, constant.ActionTransactionView); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "check permission failed")
		s.log.ErrorContext(ctx, "check permission failed", "error", err)
		return repository.GetDocumentRow{}, err
	}

	return d, nil
}

func (s *documentService) Delete(ctx context.Context, actor authz.Actor, id uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "DocumentService.Delete")
	defer span.End()

	d, err := s.getAuthorized(ctx, actor, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "delete failed")
		s.log.ErrorContext(ctx, "delete failed", "error", err)
		return err
	}

	if d.UploadedBy != actor.UserID {
		if err := s.authz.CheckAdmin(ctx, actor); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "check admin failed")
			s.log.ErrorContext(ctx, "check admin failed", "error", err)
			return err
		}
	}

	rows, err := s.repo.SoftDeleteDocument(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "soft delete failed")
		s.log.ErrorContext(ctx, "soft delete failed", "error", err)
		return err
	}

	if rows == 0 {
		return service.ErrNotFound
	}
	return nil
}

func fileExt(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			if ext := name[i:]; len(ext) <= 10 {
				return ext
			}
			return ""
		}
	}
	return ""
}
