package transaction

import (
	"context"
	"errors"
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
	"github.com/disillusioned-labs/expense/internal/repository"
	"github.com/disillusioned-labs/expense/internal/service"
	"github.com/disillusioned-labs/platform/pgutil"
)

var tracer = otel.Tracer("service/transaction")

var Categories = []string{
	"makan_minum", "transportasi", "material", "utilitas", "lainnya",
}

func IsValidCategory(c string) bool {
	for _, known := range Categories {
		if known == c {
			return true
		}
	}
	return false
}

type TransactionService interface {
	Get(ctx context.Context, actor authz.Actor, id uuid.UUID) (Detail, error)
	List(ctx context.Context, actor authz.Actor, f ListFilters, limit, offset int32) ([]Transaction, error)
	Update(ctx context.Context, actor authz.Actor, id uuid.UUID, in UpdateInput) (Detail, error)
	Delete(ctx context.Context, actor authz.Actor, id uuid.UUID) error
}

type transactionService struct {
	repo     repository.Store
	authz    *authz.Authorizer
	storage  *s3.Client
	identity contract.IdentityClient
	log      *slog.Logger
}

func NewTransactionService(repo repository.Store, authorizer *authz.Authorizer, storage *s3.Client, identity contract.IdentityClient, log *slog.Logger) TransactionService {
	return &transactionService{repo: repo, authz: authorizer, storage: storage, identity: identity, log: log}
}

const maxItems = 200

func validateItems(items []ItemInput) error {
	if len(items) > maxItems {
		return service.ErrTooManyItems
	}
	for _, it := range items {
		if it.Description == "" || it.Quantity <= 0 || it.Amount < 0 {
			return service.ErrItemInvalid
		}
	}
	return nil
}

func insertItems(ctx context.Context, q repository.Querier, txID uuid.UUID, items []ItemInput) error {
	if len(items) == 0 {
		return nil
	}
	descriptions := make([]string, len(items))
	quantities := make([]int32, len(items))
	amounts := make([]int64, len(items))
	for i, it := range items {
		descriptions[i] = it.Description
		quantities[i] = it.Quantity
		amounts[i] = it.Amount
	}
	_, err := q.InsertTransactionItems(ctx, repository.InsertTransactionItemsParams{
		TransactionID: txID,
		Descriptions:  descriptions,
		Quantities:    quantities,
		Amounts:       amounts,
	})

	return err
}

func (s *transactionService) snapshot(ctx context.Context, actor authz.Actor) (uuid.UUID, string, string, string) {
	users, err := s.identity.GetUsersInfo(ctx, []contract.UserOrgPair{
		{UserID: actor.UserID, OrganizationID: actor.OrgID},
	})
	if err != nil || len(users) == 0 {
		return actor.UserID, "unknown", "unknown", actor.Role
	}
	u := users[0]
	return actor.UserID, u.Name, u.Email, u.Role
}

func toBrief(t repository.Transaction, projectName string, docCount int64) Transaction {
	out := Transaction{
		ID:            t.ID,
		Status:        t.Status,
		TotalAmount:   t.TotalAmount,
		Currency:      t.Currency,
		Category:      pgutil.TextPtr(t.Category),
		Description:   pgutil.TextPtr(t.Description),
		DocumentCount: docCount,
		CreatedAt:     t.CreatedAt,
		SubmittedAt:   pgutil.TimePtr(t.SubmittedAt),
		DecidedAt:     pgutil.TimePtr(t.DecidedAt),
	}
	out.Project = ProjectRef{ID: t.ProjectID, Name: projectName}
	out.CreatedBy = ActorBrief{ID: t.CreatedBy, Name: t.CreatedByName, Email: t.CreatedByEmail, Role: t.CreatedByRole}
	return out
}

func toBriefList(r repository.ListTransactionsRow) Transaction {
	out := Transaction{
		ID:            r.ID,
		Status:        r.Status,
		TotalAmount:   r.TotalAmount,
		Currency:      r.Currency,
		Category:      pgutil.TextPtr(r.Category),
		Description:   pgutil.TextPtr(r.Description),
		DocumentCount: r.DocumentCount,
		CreatedAt:     r.CreatedAt,
		SubmittedAt:   pgutil.TimePtr(r.SubmittedAt),
		DecidedAt:     pgutil.TimePtr(r.DecidedAt),
	}
	out.Project = ProjectRef{ID: r.ProjectID, Name: r.ProjectName}
	out.CreatedBy = ActorBrief{ID: r.CreatedBy, Name: r.CreatedByName, Email: r.CreatedByEmail, Role: r.CreatedByRole}
	return out
}

func (s *transactionService) Get(ctx context.Context, actor authz.Actor, id uuid.UUID) (Detail, error) {
	ctx, span := tracer.Start(ctx, "TransactionService.Get")
	defer span.End()

	t, err := s.repo.GetTransaction(ctx, repository.GetTransactionParams{ID: id, OrganizationID: actor.OrgID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Detail{}, service.ErrNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "get transaction failed")
		s.log.ErrorContext(ctx, "get transaction failed", "error", err)
		return Detail{}, err
	}

	if err := s.authz.Can(ctx, actor, t.ProjectID, constant.ActionTransactionView); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "check permission failed")
		s.log.ErrorContext(ctx, "check permission failed", "error", err)
		return Detail{}, err
	}

	return s.buildDetail(ctx, t)
}

func (s *transactionService) buildDetail(ctx context.Context, t repository.Transaction) (Detail, error) {
	ctx, span := tracer.Start(ctx, "TransactionService.buildDetail")
	defer span.End()

	project, err := s.repo.GetProject(ctx, repository.GetProjectParams{ID: t.ProjectID, OrganizationID: t.OrganizationID})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "get project failed")
		s.log.ErrorContext(ctx, "get project failed", "error", err)
		return Detail{}, err
	}

	docCount, err := s.repo.CountTransactionDocuments(ctx, &t.ID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "count transaction documents failed")
		s.log.ErrorContext(ctx, "count transaction documents failed", "error", err)
		return Detail{}, err
	}

	detail := Detail{Transaction: toBrief(t, project.Name, docCount)}

	itemRows, err := s.repo.ListTransactionItems(ctx, t.ID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "list transaction items failed")
		s.log.ErrorContext(ctx, "list transaction items failed", "error", err)
		return Detail{}, err
	}

	detail.Items = make([]Item, 0, len(itemRows))
	for _, it := range itemRows {
		detail.Items = append(detail.Items, Item{
			ID:          it.ID,
			Description: it.Description,
			Quantity:    it.Quantity,
			Amount:      it.Amount,
		})
	}

	docs, err := s.repo.ListDocumentsByTransaction(ctx, &t.ID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "list documents by transaction failed")
		s.log.ErrorContext(ctx, "list documents by transaction failed", "error", err)
		return Detail{}, err
	}

	detail.Documents = make([]DocumentBrief, 0, len(docs))
	for _, d := range docs {
		brief := DocumentBrief{
			ID:        d.ID,
			FileName:  d.FileName,
			FileURL:   s.fileURL(ctx, d.StoragePath, d.FileName),
			MimeType:  d.MimeType,
			OcrStatus: d.OcrStatus,
			CreatedAt: d.CreatedAt,
		}
		detail.Documents = append(detail.Documents, brief)
	}

	approvals, err := s.repo.ListApprovalsByTransaction(ctx, t.ID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "list approvals by transaction failed")
		s.log.ErrorContext(ctx, "list approvals by transaction failed", "error", err)
		return Detail{}, err
	}

	decisions, err := s.repo.ListDecisionsByTransaction(ctx, t.ID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "list decisions by transaction failed")
		s.log.ErrorContext(ctx, "list decisions by transaction failed", "error", err)
		return Detail{}, err
	}

	byApproval := make(map[uuid.UUID]repository.ApprovalDecision, len(decisions))
	for _, d := range decisions {
		byApproval[d.ApprovalID] = d
	}

	detail.Approvals = make([]ApprovalState, 0, len(approvals))
	for _, a := range approvals {
		state := ApprovalState{
			ID:          a.ID,
			Step:        a.Step,
			Approver:    ActorBrief{ID: a.ApproverID, Name: a.ApproverName, Email: a.ApproverEmail, Role: a.ApproverRole},
			ActivatedAt: pgutil.TimePtr(a.ActivatedAt),
		}
		switch {
		case a.CancelledAt.Valid:
			state.State = "cancelled"
		case !a.ActivatedAt.Valid:
			state.State = "waiting"
		default:
			state.State = "pending"
		}
		if d, ok := byApproval[a.ID]; ok {
			state.State = "decided"
			state.Decision = &Decision{
				Decision:  d.Decision,
				Notes:     pgutil.TextPtr(d.Notes),
				DecidedBy: ActorBrief{ID: d.DecidedBy, Name: d.DecidedByName, Email: d.DecidedByEmail, Role: d.DecidedByRole},
				DecidedAt: d.DecidedAt,
			}
		}
		detail.Approvals = append(detail.Approvals, state)
	}
	return detail, nil
}

func (s *transactionService) fileURL(ctx context.Context, path, filename string) string {
	duration := time.Duration(constant.DocumentPresignTTL) * time.Minute
	url, err := s.storage.PresignGet(ctx, constant.DocumentBucket, path, duration, filename)
	if err != nil {
		s.log.WarnContext(ctx, "presign failed;", "error", err, "path", path)
		return ""
	}
	return url
}

func (s *transactionService) List(ctx context.Context, actor authz.Actor, f ListFilters, limit, offset int32) ([]Transaction, error) {
	ctx, span := tracer.Start(ctx, "TransactionService.List")
	defer span.End()

	projectIDs, err := s.authz.PlacedProjectIDs(ctx, actor, constant.ActionTransactionView)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "get placed project IDs failed")
		s.log.ErrorContext(ctx, "get placed project IDs failed", "error", err)
		return nil, err
	}

	if len(projectIDs) == 0 {
		return []Transaction{}, nil
	}
	if f.ProjectID != nil && !containsID(projectIDs, *f.ProjectID) {
		return []Transaction{}, nil
	}
	rows, err := s.repo.ListTransactions(ctx, repository.ListTransactionsParams{
		OrganizationID: actor.OrgID,
		ProjectIds:     projectIDs,
		Status:         pgutil.Text(pgutil.StringPtr(f.Status)),
		ProjectID:      f.ProjectID,
		CreatedBy:      f.CreatedBy,
		Category:       pgutil.Text(f.Category),
		DateFrom:       pgutil.Time(f.DateFrom),
		DateTo:         pgutil.Time(f.DateTo),
		Limit:          limit,
		Offset:         offset,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "created by failed")
		s.log.ErrorContext(ctx, "created by failed", "error", err)
		return nil, err
	}

	out := make([]Transaction, 0, len(rows))
	for _, r := range rows {
		out = append(out, toBriefList(r))
	}
	return out, nil
}

func containsID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, known := range ids {
		if known == id {
			return true
		}
	}
	return false
}

func (s *transactionService) canEditDraftRow(ctx context.Context, actor authz.Actor, t repository.Transaction) error {
	ctx, span := tracer.Start(ctx, "TransactionService.canEditDraftRow")
	defer span.End()

	if t.CreatedBy == actor.UserID {
		return nil
	}
	return s.authz.CheckAdmin(ctx, actor)
}

func (s *transactionService) Update(ctx context.Context, actor authz.Actor, id uuid.UUID, in UpdateInput) (Detail, error) {
	ctx, span := tracer.Start(ctx, "TransactionService.Update")
	defer span.End()

	t, err := s.repo.GetTransaction(ctx, repository.GetTransactionParams{ID: id, OrganizationID: actor.OrgID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Detail{}, service.ErrNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "get transaction failed")
		s.log.ErrorContext(ctx, "get transaction failed", "error", err)
		return Detail{}, err
	}

	if err := s.canEditDraftRow(ctx, actor, t); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "update failed")
		s.log.ErrorContext(ctx, "update failed", "error", err)
		return Detail{}, err
	}

	if in.Currency == "" {
		in.Currency = t.Currency
	}
	if in.Currency != constant.CurrencyIDR {
		return Detail{}, err
	}
	if in.Category != nil && !IsValidCategory(*in.Category) {
		return Detail{}, err
	}
	if in.Items != nil {
		if err := validateItems(in.Items); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "validate items failed")
			s.log.ErrorContext(ctx, "validate items failed", "error", err)
			return Detail{}, err
		}
	}

	var updated repository.Transaction
	if err := s.repo.ExecTx(ctx, func(q repository.Querier) error {
		row, err := q.UpdateTransactionDraft(ctx, repository.UpdateTransactionDraftParams{
			ID:             id,
			OrganizationID: t.OrganizationID,
			Description:    pgutil.Text(in.Description),
			Currency:       in.Currency,
			Category:       pgutil.Text(in.Category),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return service.ErrTransactionLocked
			}
			span.RecordError(err)
			span.SetStatus(codes.Error, "update failed")
			s.log.ErrorContext(ctx, "update failed", "error", err)
			return err
		}

		if in.Items != nil {
			if _, err := q.DeleteTransactionItems(ctx, id); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, "delete transaction items")
				s.log.ErrorContext(ctx, "delete transaction items failed", "error", err, "transaction_id", id)
				return err
			}

			if err := insertItems(ctx, q, id, in.Items); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, "insert transaction items")
				s.log.ErrorContext(ctx, "insert transaction items failed", "error", err, "transaction_id", id)
				return err
			}

			row, err = q.LockTransaction(ctx, id)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, "lock transaction")
				s.log.ErrorContext(ctx, "lock transaction failed", "error", err, "transaction_id", id)
				return err
			}
		}

		updated = row
		return s.emitUpdatedInTx(ctx, q, row, actor, SourceManual)
	}); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "update failed")
		s.log.ErrorContext(ctx, "update failed", "error", err)
		return Detail{}, err
	}

	return s.buildDetail(ctx, updated)
}

func (s *transactionService) emitUpdatedInTx(ctx context.Context, q repository.Querier, t repository.Transaction, actor authz.Actor, source string) error {
	ctx, span := tracer.Start(ctx, "TransactionService.emitUpdatedInTx")
	defer span.End()

	return service.Emit(ctx, q, "transaction", t.ID, EventTransactionUpdated, constant.TopicAudit, TransactionUpdatedEvent{
		OrganizationID: t.OrganizationID, TransactionID: t.ID,
		TotalAmount: t.TotalAmount, ActorID: actor.UserID, Source: source,
	})
}

func (s *transactionService) Delete(ctx context.Context, actor authz.Actor, id uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "TransactionService.Delete")
	defer span.End()

	t, err := s.repo.GetTransaction(ctx, repository.GetTransactionParams{ID: id, OrganizationID: actor.OrgID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return service.ErrNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "get transaction failed")
		s.log.ErrorContext(ctx, "get transaction failed", "error", err)
		return err
	}

	if err := s.canEditDraftRow(ctx, actor, t); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "delete failed")
		s.log.ErrorContext(ctx, "delete failed", "error", err)
		return err
	}

	rows, err := s.repo.SoftDeleteTransaction(ctx, repository.SoftDeleteTransactionParams{ID: id, OrganizationID: actor.OrgID})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "soft delete failed")
		s.log.ErrorContext(ctx, "soft delete failed", "error", err)
		return err
	}

	if rows == 0 {
		return service.ErrTransactionLocked
	}
	return s.repo.ExecTx(ctx, func(q repository.Querier) error {
		return service.Emit(ctx, q, "transaction", id, EventTransactionDeleted, constant.TopicAudit, TransactionDeletedEvent{
			OrganizationID: actor.OrgID, TransactionID: id, ActorID: actor.UserID,
		})
	})
}
