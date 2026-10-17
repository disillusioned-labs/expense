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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	"github.com/disillusioned-labs/platform/s3"

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
	storage  s3.Store
	bucket   string
	ocr      ocr.Submitter
	identity contract.IdentityClient
	log      *slog.Logger
}

func NewDocumentService(repo repository.Store, authorizer *authz.Authorizer, storage s3.Store, bucket string, submitter ocr.Submitter, identity contract.IdentityClient, log *slog.Logger) DocumentService {
	return &documentService{repo: repo, authz: authorizer, storage: storage, bucket: bucket, ocr: submitter, identity: identity, log: log}
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
	url, err := s.storage.PresignGet(ctx, s.bucket, path, duration, filename)
	if err != nil {
		s.log.WarnContext(ctx, "presign failed;", "error", err, "path", path)
		return ""
	}
	return url
}

// The sqlc rows carry the same fields as repository.Document but in query
// order, so each query gets a small adapter instead of one struct conversion.
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
	if err := s.authz.Can(ctx, actor, t.ProjectID, constant.ActionDocumentUpload); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "check upload permission failed")
		s.log.ErrorContext(ctx, "check upload permission failed", "error", err)
		return Document{}, err
	}

	return s.upload(ctx, actor, t.ProjectID, &transactionID, in)
}

// loadProject resolves and authorizes the upload target. Archive state is
// checked after idempotency lookup so a completed retry can return its result.
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
	if in.IdempotencyKey == uuid.Nil {
		return Document{}, service.ErrIdempotencyKeyRequired
	}

	hasher := sha256.New()
	size, err := io.Copy(hasher, in.Content)
	if err != nil {
		return Document{}, fmt.Errorf("hash upload: %w", err)
	}
	if size != in.Size {
		return Document{}, fmt.Errorf("upload size changed while reading: got %d bytes, expected %d", size, in.Size)
	}
	if _, err := in.Content.Seek(0, io.SeekStart); err != nil {
		return Document{}, fmt.Errorf("rewind upload: %w", err)
	}
	hash := hex.EncodeToString(hasher.Sum(nil))
	lookupParams := repository.GetDocumentUploadByKeyParams{
		ProjectID: projectID, UploadedBy: actor.UserID, IdempotencyKey: &in.IdempotencyKey,
	}
	existing, err := s.repo.GetDocumentUploadByKey(ctx, lookupParams)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Document{}, err
	}
	if err == nil {
		if !sameUploadRequest(existing, transactionID, in, hash) || existing.DeletedAt.Valid {
			return Document{}, service.ErrIdempotencyKeyReused
		}
		if existing.UploadComplete {
			return s.documentFromUploadKeyRow(ctx, existing), nil
		}
	}
	project, err := s.repo.GetProject(ctx, repository.GetProjectParams{ID: projectID, OrganizationID: actor.OrgID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Document{}, service.ErrNotFound
		}
		return Document{}, err
	}
	if project.ArchivedAt.Valid {
		return Document{}, service.ErrProjectArchived
	}
	if transactionID != nil {
		transaction, err := s.repo.GetTransaction(ctx, repository.GetTransactionParams{ID: *transactionID, OrganizationID: actor.OrgID})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Document{}, service.ErrNotFound
			}
			return Document{}, err
		}
		if transaction.ProjectID != projectID {
			return Document{}, service.ErrNotFound
		}
		if transaction.Status != constant.StatusDraft {
			return Document{}, service.ErrTransactionLocked
		}
	}
	_, uploadedByName, uploadedByEmail, uploadedByRole := s.snapshot(ctx, actor)
	docID := uuid.Must(uuid.NewV7())
	key := fmt.Sprintf("%s/%s/%s%s", actor.OrgID, projectID, docID, extensionForMime(in.MimeType))
	var upload repository.GetDocumentUploadByKeyRow
	err = s.repo.ExecTx(ctx, func(q repository.Querier) error {
		lockedProject, lockErr := q.LockProjectForDocumentUpload(ctx, repository.LockProjectForDocumentUploadParams{
			ID: projectID, OrganizationID: actor.OrgID,
		})
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return service.ErrNotFound
		}
		if lockErr != nil {
			return lockErr
		}
		if lockedProject.ArchivedAt.Valid {
			return service.ErrProjectArchived
		}
		_, reserveErr := q.ReserveDocumentUpload(ctx, repository.ReserveDocumentUploadParams{
			ID: docID, ProjectID: projectID, TransactionID: transactionID, StoragePath: key,
			FileName: in.FileName, FileSize: in.Size, MimeType: in.MimeType,
			FileHash: pgutil.String(hash), UploadedBy: actor.UserID, UploadedByName: uploadedByName,
			UploadedByEmail: uploadedByEmail, UploadedByRole: uploadedByRole,
			IdempotencyKey: &in.IdempotencyKey,
		})
		if reserveErr != nil && !errors.Is(reserveErr, pgx.ErrNoRows) {
			return reserveErr
		}
		var lookupErr error
		upload, lookupErr = q.GetDocumentUploadByKey(ctx, lookupParams)
		if lookupErr != nil {
			return lookupErr
		}
		if !sameUploadRequest(upload, transactionID, in, hash) || upload.DeletedAt.Valid {
			return service.ErrIdempotencyKeyReused
		}
		return nil
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "reserve document upload failed")
		s.log.ErrorContext(ctx, "reserve document upload failed", "error", err)
		return Document{}, err
	}
	if upload.UploadComplete {
		return s.documentFromUploadKeyRow(ctx, upload), nil
	}
	project, err = s.repo.GetProject(ctx, repository.GetProjectParams{ID: projectID, OrganizationID: actor.OrgID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Document{}, service.ErrNotFound
		}
		return Document{}, err
	}
	if project.ArchivedAt.Valid {
		return Document{}, service.ErrProjectArchived
	}
	if transactionID != nil {
		transaction, err := s.repo.GetTransaction(ctx, repository.GetTransactionParams{ID: *transactionID, OrganizationID: actor.OrgID})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Document{}, service.ErrNotFound
			}
			return Document{}, err
		}
		if transaction.ProjectID != projectID {
			return Document{}, service.ErrNotFound
		}
		if transaction.Status != constant.StatusDraft {
			return Document{}, service.ErrTransactionLocked
		}
	}

	if !upload.UploadComplete {
		// Retries for one key reuse this path; the request fingerprint prevents
		// different bytes from replacing a previously reserved object.
		if _, err := s.storage.Put(ctx, s.bucket, upload.StoragePath, in.Size, in.MimeType, in.Content); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "save file failed")
			s.log.ErrorContext(ctx, "save file failed", "error", err)
			return Document{}, err
		}
	}

	createdEvent := DocumentCreatedEvent{
		OrganizationID: actor.OrgID, ProjectID: projectID, TransactionID: upload.TransactionID,
		FileName: upload.FileName, FileSize: upload.FileSize, ActorID: actor.UserID,
	}
	var completed repository.LockDocumentUploadRow
	err = s.repo.ExecTx(ctx, func(q repository.Querier) error {
		lockedProject, lockErr := q.LockProjectForDocumentUpload(ctx, repository.LockProjectForDocumentUploadParams{
			ID: projectID, OrganizationID: actor.OrgID,
		})
		if lockErr != nil {
			return lockErr
		}
		if lockedProject.ArchivedAt.Valid {
			return service.ErrProjectArchived
		}
		if transactionID != nil {
			locked, lockErr := q.LockTransaction(ctx, *transactionID)
			if lockErr != nil {
				return lockErr
			}
			if locked.OrganizationID != actor.OrgID || locked.ProjectID != projectID || locked.DeletedAt.Valid {
				return service.ErrNotFound
			}
			if locked.Status != constant.StatusDraft {
				return service.ErrTransactionLocked
			}
		}
		var documentLockErr error
		completed, documentLockErr = q.LockDocumentUpload(ctx, upload.ID)
		if documentLockErr != nil {
			return documentLockErr
		}
		if completed.UploadComplete {
			return nil
		}
		rows, completeErr := q.CompleteDocumentUpload(ctx, completed.ID)
		if completeErr != nil {
			return completeErr
		}
		if rows == 0 {
			return service.ErrConflict
		}
		createdEvent.DocumentID = completed.ID
		return service.Emit(ctx, q, "document", completed.ID,
			EventDocumentCreated, constant.TopicAudit, createdEvent)
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "complete document upload failed")
		s.log.ErrorContext(ctx, "complete document upload failed", "error", err)
		return Document{}, err
	}

	// The gateway sees the same idempotency key on a retry after a lost HTTP response.
	s.submitOCR(ctx, completed.ID, completed.StoragePath, completed.FileSize, completed.MimeType)
	doc := uploadRowToDoc(completed)
	doc.FileURL = s.fileURL(ctx, completed.StoragePath, completed.FileName)
	return doc, nil
}

func sameUploadRequest(upload repository.GetDocumentUploadByKeyRow, transactionID *uuid.UUID, in UploadInput, hash string) bool {
	if upload.FileName != in.FileName || upload.FileSize != in.Size || upload.MimeType != in.MimeType ||
		!upload.FileHash.Valid || upload.FileHash.String != hash {
		return false
	}
	if upload.TransactionID == nil || transactionID == nil {
		return upload.TransactionID == nil && transactionID == nil
	}
	return *upload.TransactionID == *transactionID
}

func (s *documentService) submitOCR(ctx context.Context, documentID uuid.UUID, path string, size int64, mimeType string) {
	ocrDocID, submitErr := s.ocr.SubmitDocument(ctx, ocr.SubmitInput{
		IdempotencyKey: documentID.String(), ExternalRef: documentID.String(), DocType: "receipt",
		Bucket: s.bucket, StoragePath: path, SizeBytes: size, DeclaredMime: mimeType,
	})
	if submitErr != nil {
		s.log.ErrorContext(ctx, "ocr submit failed; document stays pending", "error", submitErr, "document_id", documentID)
		return
	}
	if ocrDocID == "" {
		return
	}
	ocrID, err := uuid.Parse(ocrDocID)
	if err != nil {
		s.log.WarnContext(ctx, "ocr submit returned an invalid document id", "document_id", documentID)
		return
	}
	if _, err := s.repo.SetOcrDocumentID(ctx, repository.SetOcrDocumentIDParams{ID: documentID, OcrDocumentID: &ocrID}); err != nil {
		s.log.WarnContext(ctx, "saving ocr document id failed; sweep will not cover this document",
			"error", err, "document_id", documentID)
	}
}

func uploadRowToDoc(d repository.LockDocumentUploadRow) Document {
	return toDoc(repository.Document{
		ID: d.ID, TransactionID: d.TransactionID, StoragePath: d.StoragePath,
		FileName: d.FileName, MimeType: d.MimeType, FileSize: d.FileSize,
		OcrStatus: d.OcrStatus, CreatedAt: d.CreatedAt,
	})
}

func uploadKeyRowToDoc(d repository.GetDocumentUploadByKeyRow) Document {
	return toDoc(repository.Document{
		ID: d.ID, TransactionID: d.TransactionID, StoragePath: d.StoragePath,
		FileName: d.FileName, MimeType: d.MimeType, FileSize: d.FileSize,
		OcrStatus: d.OcrStatus, CreatedAt: d.CreatedAt,
	})
}

func (s *documentService) documentFromUploadKeyRow(ctx context.Context, upload repository.GetDocumentUploadByKeyRow) Document {
	s.submitOCR(ctx, upload.ID, upload.StoragePath, upload.FileSize, upload.MimeType)
	doc := uploadKeyRowToDoc(upload)
	doc.FileURL = s.fileURL(ctx, upload.StoragePath, upload.FileName)
	return doc
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

	if err := s.repo.ExecTx(ctx, func(q repository.Querier) error {
		return service.Emit(ctx, q, "document", d.ID,
			EventDocumentAccessed, constant.TopicAudit, DocumentAccessedEvent{
				OrganizationID: actor.OrgID,
				DocumentID:     d.ID,
				ActorID:        actor.UserID,
			})
	}); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "emit document accessed failed")
		s.log.ErrorContext(ctx, "emit document accessed failed", "error", err)
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

	if err := s.repo.ExecTx(ctx, func(q repository.Querier) error {
		lockedTransactionID := uuid.Nil
		lockTransaction := func(transactionID uuid.UUID) error {
			transaction, err := q.LockTransaction(ctx, transactionID)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return service.ErrNotFound
				}
				return err
			}
			if transaction.OrganizationID != actor.OrgID || transaction.DeletedAt.Valid {
				return service.ErrNotFound
			}
			if transaction.Status != constant.StatusDraft {
				return service.ErrTransactionLocked
			}
			lockedTransactionID = transaction.ID
			return nil
		}
		if d.TransactionID != nil {
			if err := lockTransaction(*d.TransactionID); err != nil {
				return err
			}
		}

		locked, err := q.GetDocumentForProcessing(ctx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return service.ErrNotFound
			}
			return err
		}
		if locked.OrganizationID != actor.OrgID {
			return service.ErrNotFound
		}
		if locked.TransactionID != nil {
			if lockedTransactionID != uuid.Nil && lockedTransactionID != *locked.TransactionID {
				return service.ErrConflict
			}
			if lockedTransactionID == uuid.Nil {
				if err := lockTransaction(*locked.TransactionID); err != nil {
					return err
				}
			}
		}

		rows, err := q.SoftDeleteDocument(ctx, id)
		if err != nil {
			return err
		}
		if rows == 0 {
			return service.ErrNotFound
		}
		return service.Emit(ctx, q, "document", id,
			EventDocumentDeleted, constant.TopicAudit, DocumentDeletedEvent{
				OrganizationID: actor.OrgID,
				DocumentID:     id,
				FileName:       locked.FileName,
				ActorID:        actor.UserID,
			})
	}); err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return service.ErrNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "soft delete failed")
		s.log.ErrorContext(ctx, "soft delete failed", "error", err)
		return err
	}
	return nil
}

func extensionForMime(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "application/pdf":
		return ".pdf"
	default:
		return ""
	}
}
