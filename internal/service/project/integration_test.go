//go:build integration

package project

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/disillusioned-labs/expense/db/migrations"
	"github.com/disillusioned-labs/expense/internal/authz"
	"github.com/disillusioned-labs/expense/internal/constant"
	"github.com/disillusioned-labs/expense/internal/contract"
	"github.com/disillusioned-labs/expense/internal/ocr"
	"github.com/disillusioned-labs/expense/internal/repository"
	"github.com/disillusioned-labs/expense/internal/service"
	documentservice "github.com/disillusioned-labs/expense/internal/service/document"
	"github.com/disillusioned-labs/expense/internal/service/ocrconsumer"
	transactionservice "github.com/disillusioned-labs/expense/internal/service/transaction"
	"github.com/disillusioned-labs/platform/postgres"
	"github.com/disillusioned-labs/platform/s3"
)

func TestGetProjectRequiresAdminOrProjectPlacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool := newIntegrationPool(t, ctx)
	store := repository.NewStore(pool)
	log := slog.New(slog.DiscardHandler)
	organizationID, ownerID, memberID := uuid.New(), uuid.New(), uuid.New()
	project, err := store.CreateProject(ctx, repository.CreateProjectParams{
		OrganizationID: organizationID,
		Name:           "project metadata access",
		CreatedBy:      ownerID,
	})
	if err != nil {
		t.Fatal(err)
	}

	memberActor := authz.Actor{OrgID: organizationID, UserID: memberID}
	projects := NewProjectService(store, authz.NewAuthorizer(store, projectGetIdentity{role: "member"}, log), log)
	if _, err := projects.Get(ctx, memberActor, project.ID); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("Get() for unplaced member error = %v, want %v", err, service.ErrForbidden)
	}

	var roleID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO roles (project_id, name, created_by) VALUES ($1, 'viewer', $2) RETURNING id`, project.ID, ownerID).Scan(&roleID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO role_permissions (role_id, action) VALUES ($1, 'transaction.view')`, roleID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO project_members (organization_id, project_id, user_id, role_id) VALUES ($1, $2, $3, $4)`, organizationID, project.ID, memberID, roleID); err != nil {
		t.Fatal(err)
	}
	if _, err := projects.Get(ctx, memberActor, project.ID); err != nil {
		t.Fatalf("Get() for placed member: %v", err)
	}

	adminActor := authz.Actor{OrgID: organizationID, UserID: ownerID}
	adminProjects := NewProjectService(store, authz.NewAuthorizer(store, projectGetIdentity{role: "owner"}, log), log)
	if _, err := adminProjects.Get(ctx, adminActor, project.ID); err != nil {
		t.Fatalf("Get() for owner without placement: %v", err)
	}
}

func TestClearOrganizationMemberPlacementsRemovesOnlyThatOrganizationsAccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool := newIntegrationPool(t, ctx)
	store := repository.NewStore(pool)
	log := slog.New(slog.DiscardHandler)
	organizationID, otherOrganizationID, ownerID, targetID, otherUserID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, orgID := range []uuid.UUID{organizationID, organizationID, otherOrganizationID} {
		project, err := store.CreateProject(ctx, repository.CreateProjectParams{
			OrganizationID: orgID,
			Name:           "rejoin access reset",
			CreatedBy:      ownerID,
		})
		if err != nil {
			t.Fatal(err)
		}
		var roleID uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO roles (project_id, name, created_by) VALUES ($1, 'viewer', $2) RETURNING id`, project.ID, ownerID).Scan(&roleID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO role_permissions (role_id, action) VALUES ($1, 'transaction.view')`, roleID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO project_members (organization_id, project_id, user_id, role_id) VALUES ($1, $2, $3, $4), ($1, $2, $5, $4)`, orgID, project.ID, targetID, roleID, otherUserID); err != nil {
			t.Fatal(err)
		}
	}

	service := NewProjectService(store, nil, log)
	removed, err := service.ClearOrganizationMemberPlacements(ctx, authz.Actor{OrgID: organizationID, UserID: ownerID}, targetID)
	if err != nil || removed != 2 {
		t.Fatalf("ClearOrganizationMemberPlacements() = %d, %v; want 2, nil", removed, err)
	}
	var targetCount, otherUserCount, auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM project_members WHERE organization_id = $1 AND user_id = $2`, organizationID, targetID).Scan(&targetCount); err != nil || targetCount != 0 {
		t.Fatalf("target placements = %d, error = %v; want 0", targetCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM project_members WHERE organization_id = $1 AND user_id = $2`, organizationID, otherUserID).Scan(&otherUserCount); err != nil || otherUserCount != 2 {
		t.Fatalf("other member placements = %d, error = %v; want 2", otherUserCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM project_members WHERE organization_id = $1 AND user_id = $2`, otherOrganizationID, targetID).Scan(&targetCount); err != nil || targetCount != 1 {
		t.Fatalf("other organization target placements = %d, error = %v; want 1", targetCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_type = 'project_member' AND event_type = 'project_member.removed' AND payload->>'reason' = 'organization_membership_reactivated' AND payload->>'actor_id' = $1`, targetID.String()).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("placement cleanup audit events = %d, error = %v; want 2", auditCount, err)
	}
	removed, err = service.ClearOrganizationMemberPlacements(ctx, authz.Actor{OrgID: organizationID, UserID: ownerID}, targetID)
	if err != nil || removed != 0 {
		t.Fatalf("idempotent cleanup = %d, %v; want 0, nil", removed, err)
	}
}

type projectGetIdentity struct {
	contract.IdentityClient
	role string
}

func (i projectGetIdentity) GetMemberRole(context.Context, uuid.UUID, uuid.UUID) (string, error) {
	return i.role, nil
}

func TestProjectDeleteSerializesWithTransactionCreation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := newIntegrationPool(t, ctx)
	store := repository.NewStore(pool)
	projects := NewProjectService(store, nil, slog.New(slog.DiscardHandler))
	actor := authz.Actor{OrgID: uuid.New(), UserID: uuid.New(), Role: authz.RoleOwner}
	project, err := store.CreateProject(ctx, repository.CreateProjectParams{
		OrganizationID: actor.OrgID,
		Name:           "transaction race",
		CreatedBy:      actor.UserID,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("transaction wins and project remains", func(t *testing.T) {
		holder, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Rollback(context.Background())

		_, err = repository.New(pool).WithTx(holder).CreateTransaction(ctx, transactionParams(actor, project.ID))
		if err != nil {
			t.Fatal(err)
		}

		deleted := make(chan error, 1)
		go func() { deleted <- projects.Delete(ctx, actor, project.ID) }()
		waitForLockWaiters(t, ctx, pool, 1)
		if err := holder.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := receiveError(t, ctx, deleted); !errors.Is(err, service.ErrProjectHasTransactions) {
			t.Fatalf("Delete() error = %v, want %v", err, service.ErrProjectHasTransactions)
		}
		if _, err := store.GetProject(ctx, repository.GetProjectParams{ID: project.ID, OrganizationID: actor.OrgID}); err != nil {
			t.Fatalf("project was deleted after transaction committed: %v", err)
		}
	})

	t.Run("delete wins and transaction creation is rejected", func(t *testing.T) {
		project, err := store.CreateProject(ctx, repository.CreateProjectParams{
			OrganizationID: actor.OrgID,
			Name:           "delete race",
			CreatedBy:      actor.UserID,
		})
		if err != nil {
			t.Fatal(err)
		}
		holder, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Rollback(context.Background())
		if _, err := repository.New(pool).WithTx(holder).LockProjectForDelete(ctx, repository.LockProjectForDeleteParams{
			ID: project.ID, OrganizationID: actor.OrgID,
		}); err != nil {
			t.Fatal(err)
		}

		deleted := make(chan error, 1)
		go func() { deleted <- projects.Delete(ctx, actor, project.ID) }()
		waitForLockWaiters(t, ctx, pool, 1)
		created := make(chan error, 1)
		go func() {
			created <- store.ExecTx(ctx, func(q repository.Querier) error {
				_, err := q.CreateTransaction(ctx, transactionParams(actor, project.ID))
				return err
			})
		}()
		waitForLockWaiters(t, ctx, pool, 2)
		if err := holder.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := receiveError(t, ctx, deleted); err != nil {
			t.Fatalf("Delete() error = %v, want nil", err)
		}
		if err := receiveError(t, ctx, created); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("CreateTransaction() error = %v, want %v", err, pgx.ErrNoRows)
		}
		if _, err := store.GetProject(ctx, repository.GetProjectParams{ID: project.ID, OrganizationID: actor.OrgID}); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("GetProject() error = %v, want deleted project", err)
		}
		count, err := store.CountProjectTransactions(ctx, project.ID)
		if err != nil || count != 0 {
			t.Fatalf("transaction count = %d, error = %v; want 0, nil", count, err)
		}
	})

	t.Run("member placement and audit outbox roll back together", func(t *testing.T) {
		project, err := store.CreateProject(ctx, repository.CreateProjectParams{
			OrganizationID: actor.OrgID,
			Name:           "outbox rollback",
			CreatedBy:      actor.UserID,
		})
		if err != nil {
			t.Fatal(err)
		}
		role, err := store.CreateRole(ctx, repository.CreateRoleParams{
			ProjectID: project.ID, Name: "member", CreatedBy: actor.UserID,
		})
		if err != nil {
			t.Fatal(err)
		}
		memberID := uuid.New()
		projectService := NewProjectService(store, authz.NewAuthorizer(store, activeIntegrationIdentity{}, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler))
		installOutboxFailure(t, ctx, pool)
		if _, err := projectService.PlaceMember(ctx, actor, project.ID, PlaceMemberInput{UserID: memberID, RoleID: role.ID}); err == nil {
			t.Fatal("PlaceMember() succeeded while outbox insert was forced to fail")
		}
		if _, err := store.GetProjectMember(ctx, repository.GetProjectMemberParams{ProjectID: project.ID, UserID: memberID}); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("member after failed placement: error = %v, want no row", err)
		}
		assertProjectMemberEventCount(t, ctx, pool, project.ID, 0)
		dropOutboxFailure(t, ctx, pool)

		if _, err := projectService.PlaceMember(ctx, actor, project.ID, PlaceMemberInput{UserID: memberID, RoleID: role.ID}); err != nil {
			t.Fatalf("PlaceMember() setup: %v", err)
		}
		installOutboxFailure(t, ctx, pool)
		if err := projectService.RemoveMember(ctx, actor, project.ID, memberID); err == nil {
			t.Fatal("RemoveMember() succeeded while outbox insert was forced to fail")
		}
		if _, err := store.GetProjectMember(ctx, repository.GetProjectMemberParams{ProjectID: project.ID, UserID: memberID}); err != nil {
			t.Fatalf("member after failed removal: %v", err)
		}
		assertProjectMemberEventCount(t, ctx, pool, project.ID, 1)
		dropOutboxFailure(t, ctx, pool)
	})

	t.Run("stale member placement version is rejected", func(t *testing.T) {
		project, err := store.CreateProject(ctx, repository.CreateProjectParams{
			OrganizationID: actor.OrgID,
			Name:           "member version",
			CreatedBy:      actor.UserID,
		})
		if err != nil {
			t.Fatal(err)
		}
		role, err := store.CreateRole(ctx, repository.CreateRoleParams{
			ProjectID: project.ID, Name: "member", CreatedBy: actor.UserID,
		})
		if err != nil {
			t.Fatal(err)
		}
		projectService := NewProjectService(store, authz.NewAuthorizer(store, activeIntegrationIdentity{}, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler))
		memberID := uuid.New()
		first, err := projectService.PlaceMember(ctx, actor, project.ID, PlaceMemberInput{UserID: memberID, RoleID: role.ID})
		if err != nil || first.Version != 1 {
			t.Fatalf("first placement version = %d, error = %v; want 1, nil", first.Version, err)
		}
		expected := first.Version
		second, err := projectService.PlaceMember(ctx, actor, project.ID, PlaceMemberInput{UserID: memberID, RoleID: role.ID, ExpectedVersion: &expected})
		if err != nil || second.Version != 2 {
			t.Fatalf("second placement version = %d, error = %v; want 2, nil", second.Version, err)
		}
		stale := first.Version
		if _, err := projectService.PlaceMember(ctx, actor, project.ID, PlaceMemberInput{UserID: memberID, RoleID: role.ID, ExpectedVersion: &stale}); !errors.Is(err, service.ErrProjectMemberChanged) {
			t.Fatalf("stale placement error = %v, want %v", err, service.ErrProjectMemberChanged)
		}
		member, err := store.GetProjectMember(ctx, repository.GetProjectMemberParams{ProjectID: project.ID, UserID: memberID})
		if err != nil || member.Version != 2 {
			t.Fatalf("member after stale placement = version %d, error = %v; want 2, nil", member.Version, err)
		}
	})
}

func TestTransactionVersionCoversManualAndItemChanges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := newIntegrationPool(t, ctx)
	store := repository.NewStore(pool)
	log := slog.New(slog.DiscardHandler)
	actor := authz.Actor{OrgID: uuid.New(), UserID: uuid.New(), Role: "member"}
	project, err := store.CreateProject(ctx, repository.CreateProjectParams{
		OrganizationID: actor.OrgID, Name: "transaction version", CreatedBy: actor.UserID,
	})
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateRole(ctx, repository.CreateRoleParams{
		ProjectID: project.ID, Name: "viewer", CreatedBy: actor.UserID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddRolePermission(ctx, repository.AddRolePermissionParams{RoleID: role.ID, Action: string(constant.ActionTransactionView)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertProjectMember(ctx, repository.UpsertProjectMemberParams{
		OrganizationID: actor.OrgID, ProjectID: project.ID, UserID: actor.UserID, RoleID: role.ID,
		ExpectedVersion: pgtype.Int8{},
	}); err != nil {
		t.Fatal(err)
	}
	tx, err := store.CreateTransaction(ctx, transactionParams(actor, project.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertTransactionItems(ctx, repository.InsertTransactionItemsParams{
		TransactionID: tx.ID, Descriptions: []string{"Receipt total"}, Quantities: []int32{1}, Amounts: []int64{1200},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetTransaction(ctx, repository.GetTransactionParams{ID: tx.ID, OrganizationID: actor.OrgID})
	if err != nil || before.Version != 2 || before.TotalAmount != 1200 {
		t.Fatalf("after initial item insert: version = %d, total = %d, error = %v; want 2, 1200, nil", before.Version, before.TotalAmount, err)
	}

	transactions := transactionservice.NewTransactionService(
		store,
		authz.NewAuthorizer(store, activeIntegrationIdentity{}, log),
		nil,
		"",
		nil,
		log,
	)
	description, category := "Corrected receipt", "lainnya"
	updated, err := transactions.Update(ctx, actor, tx.ID, transactionservice.UpdateInput{
		Description: &description, Currency: "IDR", Category: &category, ExpectedVersion: before.Version,
	})
	if err != nil || updated.Version != before.Version+1 {
		t.Fatalf("manual update version = %d, error = %v; want %d, nil", updated.Version, err, before.Version+1)
	}

	if _, err := store.InsertTransactionItems(ctx, repository.InsertTransactionItemsParams{
		TransactionID: tx.ID, Descriptions: []string{"OCR append"}, Quantities: []int32{1}, Amounts: []int64{300},
	}); err != nil {
		t.Fatal(err)
	}
	afterOCR, err := store.GetTransaction(ctx, repository.GetTransactionParams{ID: tx.ID, OrganizationID: actor.OrgID})
	if err != nil || afterOCR.Version != updated.Version+1 || afterOCR.TotalAmount != 1500 {
		t.Fatalf("after item append: version = %d, total = %d, error = %v; want %d, 1500, nil", afterOCR.Version, afterOCR.TotalAmount, err, updated.Version+1)
	}

	staleDescription := "stale overwrite"
	_, err = transactions.Update(ctx, actor, tx.ID, transactionservice.UpdateInput{
		Description: &staleDescription, Currency: "IDR", Category: &category, ExpectedVersion: updated.Version,
	})
	var domainErr *service.Error
	if !errors.As(err, &domainErr) || domainErr.Code != "TRANSACTION_CHANGED" {
		t.Fatalf("stale update error = %v, want TRANSACTION_CHANGED", err)
	}
	final, err := store.GetTransaction(ctx, repository.GetTransactionParams{ID: tx.ID, OrganizationID: actor.OrgID})
	if err != nil || final.Version != afterOCR.Version || final.TotalAmount != 1500 || !final.Description.Valid || final.Description.String != description {
		t.Fatalf("after stale update: version = %d, total = %d, description = %q, error = %v; latest data must remain", final.Version, final.TotalAmount, final.Description.String, err)
	}
}

func TestProjectArchiveSerializesWithOCRDraftCreation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := newIntegrationPool(t, ctx)
	store := repository.NewStore(pool)
	log := slog.New(slog.DiscardHandler)
	projects := NewProjectService(store, nil, log)
	actor := authz.Actor{OrgID: uuid.New(), UserID: uuid.New(), Role: "member"}

	t.Run("OCR draft wins the project lock", func(t *testing.T) {
		project := createIntegrationProjectWithView(t, ctx, store, actor)
		holder, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Rollback(context.Background())
		if _, err := repository.New(pool).WithTx(holder).CreateTransaction(ctx, transactionParams(actor, project.ID)); err != nil {
			t.Fatal(err)
		}
		archived := make(chan error, 1)
		go func() {
			_, err := projects.Archive(ctx, actor, project.ID)
			archived <- err
		}()
		waitForLockWaiters(t, ctx, pool, 1)
		if err := holder.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := receiveError(t, ctx, archived); err != nil {
			t.Fatalf("Archive() after draft creation: %v", err)
		}
		stored, err := store.GetProject(ctx, repository.GetProjectParams{ID: project.ID, OrganizationID: actor.OrgID})
		if err != nil || !stored.ArchivedAt.Valid {
			t.Fatalf("project archived = %t, error = %v; want archived", stored.ArchivedAt.Valid, err)
		}
		if count, err := store.CountProjectTransactions(ctx, project.ID); err != nil || count != 1 {
			t.Fatalf("draft count = %d, error = %v; want the draft that won the lock", count, err)
		}
	})

	t.Run("archive wins and pending OCR is retained without a draft", func(t *testing.T) {
		project := createIntegrationProjectWithView(t, ctx, store, actor)
		doc := createIntegrationDocument(t, ctx, store, actor, project.ID, nil)
		if _, err := projects.Archive(ctx, actor, project.ID); err != nil {
			t.Fatalf("Archive(): %v", err)
		}
		if err := ocrconsumer.NewService(store, activeIntegrationIdentity{}, log).Handle(ctx, completedOCRMessage(doc.ID)); err != nil {
			t.Fatalf("Handle() after archive: %v", err)
		}
		if count, err := store.CountProjectTransactions(ctx, project.ID); err != nil || count != 0 {
			t.Fatalf("draft count = %d, error = %v; want no draft", count, err)
		}
		var status string
		var transactionID pgtype.UUID
		var hasOCRData bool
		if err := pool.QueryRow(ctx, `SELECT ocr_status, transaction_id, ocr_data IS NOT NULL FROM documents WHERE id = $1`, doc.ID).Scan(&status, &transactionID, &hasOCRData); err != nil {
			t.Fatal(err)
		}
		if status != constant.OCRStatusNeedsReview || transactionID.Valid || !hasOCRData {
			t.Fatalf("document status=%q transaction=%v has_ocr_data=%t; want needs_review, no transaction, retained result", status, transactionID.Valid, hasOCRData)
		}
	})
}

func TestProjectDeleteWaitsForReceiptUploadReservation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := newIntegrationPool(t, ctx)
	store := repository.NewStore(pool)
	projects := NewProjectService(store, nil, slog.New(slog.DiscardHandler))
	actor := authz.Actor{OrgID: uuid.New(), UserID: uuid.New(), Role: "owner"}
	project, err := store.CreateProject(ctx, repository.CreateProjectParams{
		OrganizationID: actor.OrgID, Name: "upload reservation", CreatedBy: actor.UserID,
	})
	if err != nil {
		t.Fatal(err)
	}

	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	q := repository.New(pool).WithTx(holder)
	if _, err := q.LockProjectForDocumentUpload(ctx, repository.LockProjectForDocumentUploadParams{
		ID: project.ID, OrganizationID: actor.OrgID,
	}); err != nil {
		t.Fatal(err)
	}
	id, key := uuid.New(), uuid.New()
	if _, err := q.ReserveDocumentUpload(ctx, repository.ReserveDocumentUploadParams{
		ID: id, ProjectID: project.ID, StoragePath: "receipt/reserved.jpg", FileName: "receipt.jpg",
		FileSize: 12, MimeType: "image/jpeg", UploadedBy: actor.UserID, UploadedByName: "Test user",
		UploadedByEmail: "test@example.test", UploadedByRole: actor.Role, IdempotencyKey: &key,
	}); err != nil {
		t.Fatal(err)
	}

	deleted := make(chan error, 1)
	go func() { deleted <- projects.Delete(ctx, actor, project.ID) }()
	waitForLockWaiters(t, ctx, pool, 1)
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var domainErr *service.Error
	if err := receiveError(t, ctx, deleted); !errors.As(err, &domainErr) || domainErr.Code != "PROJECT_HAS_DOCUMENTS" {
		t.Fatalf("Delete() error = %v, want PROJECT_HAS_DOCUMENTS", err)
	}
	if _, err := store.GetProject(ctx, repository.GetProjectParams{ID: project.ID, OrganizationID: actor.OrgID}); err != nil {
		t.Fatalf("project with an in-flight receipt reservation was deleted: %v", err)
	}
}

func TestTransactionListUsesStableTieBreakerForOffsetPages(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := newIntegrationPool(t, ctx)
	store := repository.NewStore(pool)
	actor := authz.Actor{OrgID: uuid.New(), UserID: uuid.New(), Role: "member"}
	project, err := store.CreateProject(ctx, repository.CreateProjectParams{
		OrganizationID: actor.OrgID, Name: "stable pagination", CreatedBy: actor.UserID,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateTransaction(ctx, transactionParams(actor, project.ID))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateTransaction(ctx, transactionParams(actor, project.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE transactions SET created_at = '2026-01-01T00:00:00Z' WHERE id = ANY($1::uuid[])`, []uuid.UUID{first.ID, second.ID}); err != nil {
		t.Fatal(err)
	}

	page := func(offset int32) []repository.ListTransactionsRow {
		rows, err := store.ListTransactions(ctx, repository.ListTransactionsParams{
			OrganizationID: actor.OrgID, Offset: offset, Limit: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	firstPage, secondPage := page(0), page(1)
	if len(firstPage) != 1 || len(secondPage) != 1 || firstPage[0].ID != second.ID || secondPage[0].ID != first.ID {
		t.Fatalf("equal-timestamp pages = %v then %v; want descending UUID tie-breaker", firstPage, secondPage)
	}
}

func TestDocumentDeleteSerializesWithOCRAndRejectsFinalTransactions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := newIntegrationPool(t, ctx)
	store := repository.NewStore(pool)
	log := slog.New(slog.DiscardHandler)
	actor := authz.Actor{OrgID: uuid.New(), UserID: uuid.New(), Role: "member"}
	permissions := authz.NewAuthorizer(store, activeIntegrationIdentity{}, log)
	documents := documentservice.NewDocumentService(store, permissions, nil, "", nil, nil, log)

	t.Run("late OCR event after delete creates no draft", func(t *testing.T) {
		project := createIntegrationProjectWithView(t, ctx, store, actor)
		doc := createIntegrationDocument(t, ctx, store, actor, project.ID, nil)
		if err := documents.Delete(ctx, actor, doc.ID); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if err := ocrconsumer.NewService(store, activeIntegrationIdentity{}, log).Handle(ctx, completedOCRMessage(doc.ID)); err != nil {
			t.Fatalf("Handle() after delete error = %v", err)
		}
		if count, err := store.CountProjectTransactions(ctx, project.ID); err != nil || count != 0 {
			t.Fatalf("draft count = %d, error = %v; want 0, nil", count, err)
		}
		if _, err := store.GetDocument(ctx, repository.GetDocumentParams{ID: doc.ID, OrganizationID: actor.OrgID}); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("GetDocument() error = %v, want deleted document", err)
		}
	})

	t.Run("final transaction cannot lose its document", func(t *testing.T) {
		project := createIntegrationProjectWithView(t, ctx, store, actor)
		tx, err := store.CreateTransaction(ctx, transactionParams(actor, project.ID))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SubmitTransaction(ctx, tx.ID); err != nil {
			t.Fatal(err)
		}
		doc := createIntegrationDocument(t, ctx, store, actor, project.ID, &tx.ID)
		if err := documents.Delete(ctx, actor, doc.ID); !errors.Is(err, service.ErrTransactionLocked) {
			t.Fatalf("Delete() error = %v, want %v", err, service.ErrTransactionLocked)
		}
		if _, err := store.GetDocument(ctx, repository.GetDocumentParams{ID: doc.ID, OrganizationID: actor.OrgID}); err != nil {
			t.Fatalf("document missing after rejected delete: %v", err)
		}
	})

	t.Run("OCR attachment committed while delete waits is rechecked", func(t *testing.T) {
		project := createIntegrationProjectWithView(t, ctx, store, actor)
		doc := createIntegrationDocument(t, ctx, store, actor, project.ID, nil)
		holder, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Rollback(context.Background())
		q := repository.New(pool).WithTx(holder)
		if rows, err := q.ClaimDocumentForOCR(ctx, doc.ID); err != nil || rows != 1 {
			t.Fatalf("ClaimDocumentForOCR() rows = %d, error = %v; want 1, nil", rows, err)
		}
		if _, err := q.GetDocumentForProcessing(ctx, doc.ID); err != nil {
			t.Fatal(err)
		}
		tx, err := q.CreateTransaction(ctx, transactionParams(actor, project.ID))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.InsertTransactionItems(ctx, repository.InsertTransactionItemsParams{
			TransactionID: tx.ID, Descriptions: []string{"Receipt total"}, Quantities: []int32{1}, Amounts: []int64{7500},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := q.AttachDocumentToTransaction(ctx, repository.AttachDocumentToTransactionParams{
			ID: doc.ID, TransactionID: &tx.ID, OcrStatus: constant.OCRStatusCompleted, OcrData: []byte(`{"total":7500}`),
		}); err != nil {
			t.Fatal(err)
		}

		deleted := make(chan error, 1)
		go func() { deleted <- documents.Delete(ctx, actor, doc.ID) }()
		waitForLockWaiters(t, ctx, pool, 1)
		if err := holder.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := receiveError(t, ctx, deleted); err != nil {
			t.Fatalf("Delete() error = %v, want nil", err)
		}
		if _, err := store.GetDocument(ctx, repository.GetDocumentParams{ID: doc.ID, OrganizationID: actor.OrgID}); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("GetDocument() error = %v, want deleted document", err)
		}
		remaining, err := store.GetTransaction(ctx, repository.GetTransactionParams{ID: tx.ID, OrganizationID: actor.OrgID})
		if err != nil || remaining.TotalAmount != 7500 {
			t.Fatalf("OCR draft after delete: total = %d, error = %v; want draft total 7500", remaining.TotalAmount, err)
		}
	})
}

func TestDocumentUploadReservationIsIdempotentAcrossConcurrentRetries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := newIntegrationPool(t, ctx)
	store := repository.NewStore(pool)
	actor := authz.Actor{OrgID: uuid.New(), UserID: uuid.New(), Role: authz.RoleOwner}
	project := createIntegrationProjectWithView(t, ctx, store, actor)
	key := uuid.New()
	type result struct {
		id   uuid.UUID
		path string
		err  error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			id := uuid.Must(uuid.NewV7())
			path := fmt.Sprintf("%s/%s/%s.jpg", actor.OrgID, project.ID, id)
			var upload repository.GetDocumentUploadByKeyRow
			err := store.ExecTx(ctx, func(q repository.Querier) error {
				if _, err := q.LockProjectForDocumentUpload(ctx, repository.LockProjectForDocumentUploadParams{
					ID: project.ID, OrganizationID: actor.OrgID,
				}); err != nil {
					return err
				}
				_, err := q.ReserveDocumentUpload(ctx, repository.ReserveDocumentUploadParams{
					ID: id, ProjectID: project.ID, StoragePath: path, FileName: "receipt.jpg", FileSize: 12,
					MimeType: "image/jpeg", FileHash: pgtype.Text{String: "same-content", Valid: true},
					UploadedBy: actor.UserID, UploadedByName: "Test user", UploadedByEmail: "test@example.test",
					UploadedByRole: actor.Role, IdempotencyKey: &key,
				})
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
				upload, err = q.GetDocumentUploadByKey(ctx, repository.GetDocumentUploadByKeyParams{
					ProjectID: project.ID, UploadedBy: actor.UserID, IdempotencyKey: &key,
				})
				return err
			})
			results <- result{id: upload.ID, path: upload.StoragePath, err: err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("reservation errors: %v, %v", first.err, second.err)
	}
	if first.id != second.id || first.path != second.path {
		t.Fatalf("retries resolved to different upload reservations: (%s, %s), (%s, %s)", first.id, first.path, second.id, second.path)
	}
	docs, err := store.ListDocumentsByProject(ctx, project.ID)
	if err != nil || len(docs) != 0 {
		t.Fatalf("incomplete upload visible in project documents: %d rows, error %v; want 0", len(docs), err)
	}
	if rows, err := store.CompleteDocumentUpload(ctx, first.id); err != nil || rows != 1 {
		t.Fatalf("complete upload rows = %d, error = %v; want 1, nil", rows, err)
	}
	docs, err = store.ListDocumentsByProject(ctx, project.ID)
	if err != nil || len(docs) != 1 || docs[0].ID != first.id {
		t.Fatalf("completed upload list = %v rows, error %v; want the one reserved document", len(docs), err)
	}
}

func TestDocumentUploadStoresObjectKeyAndReplaysSameKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := newIntegrationPool(t, ctx)
	store := repository.NewStore(pool)
	log := slog.New(slog.DiscardHandler)
	actor := authz.Actor{OrgID: uuid.New(), UserID: uuid.New(), Role: "member"}
	project, err := store.CreateProject(ctx, repository.CreateProjectParams{
		OrganizationID: actor.OrgID, Name: "idempotent upload", CreatedBy: actor.UserID,
	})
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateRole(ctx, repository.CreateRoleParams{
		ProjectID: project.ID, Name: "uploader", CreatedBy: actor.UserID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddRolePermission(ctx, repository.AddRolePermissionParams{RoleID: role.ID, Action: string(constant.ActionDocumentUpload)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertProjectMember(ctx, repository.UpsertProjectMemberParams{
		OrganizationID: actor.OrgID, ProjectID: project.ID, UserID: actor.UserID, RoleID: role.ID,
		ExpectedVersion: pgtype.Int8{},
	}); err != nil {
		t.Fatal(err)
	}
	objectStore := &captureObjectStore{}
	ocrID := uuid.New()
	var submissions []ocr.SubmitInput
	identity := activeIntegrationIdentity{}
	documents := documentservice.NewDocumentService(
		store,
		authz.NewAuthorizer(store, identity, log),
		objectStore,
		"expense",
		submitterFunc(func(_ context.Context, in ocr.SubmitInput) (string, error) {
			submissions = append(submissions, in)
			return ocrID.String(), nil
		}),
		identity,
		log,
	)
	key := uuid.New()
	newInput := func() documentservice.UploadInput {
		return documentservice.UploadInput{
			IdempotencyKey: key, FileName: "receipt.jpg", MimeType: "image/jpeg", Size: 12,
			Content: strings.NewReader("receipt-data"),
		}
	}
	first, err := documents.UploadToProject(ctx, actor, project.ID, newInput())
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}
	var uploadComplete bool
	if err := pool.QueryRow(ctx, `SELECT upload_complete FROM documents WHERE id = $1`, first.ID).Scan(&uploadComplete); err != nil || !uploadComplete {
		t.Fatalf("document %s reservation complete = %v, error = %v; want true", first.ID, uploadComplete, err)
	}
	stored, err := store.GetDocument(ctx, repository.GetDocumentParams{ID: first.ID, OrganizationID: actor.OrgID})
	if err != nil {
		t.Fatal(err)
	}
	if stored.StoragePath != objectStore.key || stored.StoragePath == objectStore.etag {
		t.Fatalf("stored path = %q; want object key %q, never ETag %q", stored.StoragePath, objectStore.key, objectStore.etag)
	}
	second, err := documents.UploadToProject(ctx, actor, project.ID, newInput())
	if err != nil {
		t.Fatalf("retry upload: %v", err)
	}
	if first.ID != second.ID || objectStore.putCount != 1 {
		t.Fatalf("retry result id = %s (first %s), PUT count = %d; want same document and one object write", second.ID, first.ID, objectStore.putCount)
	}
	differentPayload := newInput()
	differentPayload.Content = strings.NewReader("receipt-evil")
	if _, err := documents.UploadToProject(ctx, actor, project.ID, differentPayload); !errors.Is(err, service.ErrIdempotencyKeyReused) {
		t.Fatalf("same key with different bytes error = %v; want %v", err, service.ErrIdempotencyKeyReused)
	}
	if objectStore.putCount != 1 {
		t.Fatalf("different payload reached storage: PUT count = %d, want 1", objectStore.putCount)
	}
	if len(submissions) != 2 || submissions[0].IdempotencyKey != first.ID.String() || submissions[1].IdempotencyKey != first.ID.String() {
		t.Fatalf("OCR submissions = %#v; want two retries with the same document identity", submissions)
	}
	for _, submission := range submissions {
		if submission.Bucket != "expense" || submission.StoragePath != objectStore.key {
			t.Fatalf("OCR source = bucket %q path %q; want configured bucket and persisted object key", submission.Bucket, submission.StoragePath)
		}
	}
	var auditEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_type = 'document' AND aggregate_id = $1`, first.ID).Scan(&auditEvents); err != nil {
		t.Fatal(err)
	}
	if auditEvents != 1 {
		t.Fatalf("document audit event count = %d, want 1", auditEvents)
	}
}

func TestStaleOCRSubmissionsRetryWithBackoffAndStableIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := newIntegrationPool(t, ctx)
	store := repository.NewStore(pool)
	actor := authz.Actor{OrgID: uuid.New(), UserID: uuid.New(), Role: "member"}
	project, err := store.CreateProject(ctx, repository.CreateProjectParams{
		OrganizationID: actor.OrgID, Name: "ocr recovery", CreatedBy: actor.UserID,
	})
	if err != nil {
		t.Fatal(err)
	}
	first := createIntegrationDocument(t, ctx, store, actor, project.ID, nil)
	second := createIntegrationDocument(t, ctx, store, actor, project.ID, nil)
	if _, err := pool.Exec(ctx, `UPDATE documents SET created_at = now() - interval '1 hour' WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE documents SET created_at = now() - interval '30 minutes' WHERE id = $1`, second.ID); err != nil {
		t.Fatal(err)
	}

	firstJobID, secondJobID := uuid.New(), uuid.New()
	var submissions []ocr.SubmitInput
	failedOnce := false
	submitter := submitterFunc(func(_ context.Context, in ocr.SubmitInput) (string, error) {
		submissions = append(submissions, in)
		if in.ExternalRef == first.ID.String() && !failedOnce {
			failedOnce = true
			return "", errors.New("temporary gateway failure")
		}
		if in.ExternalRef == first.ID.String() {
			return firstJobID.String(), nil
		}
		return secondJobID.String(), nil
	})
	sweeper := ocrconsumer.NewSweeper(store, emptyStatusReader{}, submitter, "expense", activeIntegrationIdentity{}, slog.New(slog.DiscardHandler))

	if count, err := sweeper.RetryUnsubmitted(ctx, time.Minute, time.Hour, 1); err != nil || count != 0 {
		t.Fatalf("first recovery count = %d, error = %v; want 0 after gateway error", count, err)
	}
	if count, err := sweeper.RetryUnsubmitted(ctx, time.Minute, time.Hour, 1); err != nil || count != 1 {
		t.Fatalf("second recovery count = %d, error = %v; want the next document to proceed", count, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE documents SET ocr_submit_attempted_at = now() - interval '2 hours' WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if count, err := sweeper.RetryUnsubmitted(ctx, time.Minute, time.Hour, 1); err != nil || count != 1 {
		t.Fatalf("backoff retry count = %d, error = %v; want failed document retried", count, err)
	}
	if count, err := sweeper.RetryUnsubmitted(ctx, time.Minute, time.Hour, 10); err != nil || count != 0 {
		t.Fatalf("recovery after stored job IDs count = %d, error = %v; want 0", count, err)
	}

	if len(submissions) != 3 {
		t.Fatalf("gateway submissions = %d, want 3", len(submissions))
	}
	if submissions[0].ExternalRef != first.ID.String() || submissions[1].ExternalRef != second.ID.String() || submissions[2].ExternalRef != first.ID.String() {
		t.Fatalf("submission order/identity = %q, %q, %q", submissions[0].ExternalRef, submissions[1].ExternalRef, submissions[2].ExternalRef)
	}
	for _, in := range submissions {
		if in.IdempotencyKey != in.ExternalRef || in.Bucket != "expense" || in.StoragePath == "" || in.SizeBytes != 128 || in.DeclaredMime != "image/jpeg" {
			t.Fatalf("recovery submit input = %+v; want persisted source and stable document key", in)
		}
	}
	var gotJobID pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT ocr_document_id FROM documents WHERE id = $1`, second.ID).Scan(&gotJobID); err != nil {
		t.Fatal(err)
	}
	if !gotJobID.Valid || uuid.UUID(gotJobID.Bytes) != secondJobID {
		t.Fatalf("second document job id = %v, want %s", gotJobID, secondJobID)
	}
	if err := pool.QueryRow(ctx, `SELECT ocr_document_id FROM documents WHERE id = $1`, first.ID).Scan(&gotJobID); err != nil {
		t.Fatal(err)
	}
	if !gotJobID.Valid || uuid.UUID(gotJobID.Bytes) != firstJobID {
		t.Fatalf("first document job id = %v, want %s after retry", gotJobID, firstJobID)
	}

	frozenDoc := createIntegrationDocument(t, ctx, store, actor, project.ID, nil)
	if _, err := pool.Exec(ctx, `UPDATE documents SET created_at = now() - interval '1 hour' WHERE id = $1`, frozenDoc.ID); err != nil {
		t.Fatal(err)
	}
	submittedWhileFrozen := false
	frozenSweeper := ocrconsumer.NewSweeper(store, emptyStatusReader{}, submitterFunc(func(context.Context, ocr.SubmitInput) (string, error) {
		submittedWhileFrozen = true
		return uuid.New().String(), nil
	}), "expense", frozenIntegrationIdentity{}, slog.New(slog.DiscardHandler))
	if count, err := frozenSweeper.RetryUnsubmitted(ctx, time.Minute, time.Hour, 10); err != nil || count != 0 {
		t.Fatalf("frozen recovery count = %d, error = %v; want 0", count, err)
	}
	if submittedWhileFrozen {
		t.Fatal("frozen organization document was submitted for OCR")
	}
}

type emptyStatusReader struct{}

func (emptyStatusReader) GetDocumentStatus(context.Context, string) (ocr.DocumentStatusSnapshot, error) {
	return ocr.DocumentStatusSnapshot{}, nil
}

func createIntegrationProjectWithView(t *testing.T, ctx context.Context, store repository.Store, actor authz.Actor) repository.Project {
	t.Helper()
	project, err := store.CreateProject(ctx, repository.CreateProjectParams{
		OrganizationID: actor.OrgID, Name: "document race", CreatedBy: actor.UserID,
	})
	if err != nil {
		t.Fatal(err)
	}
	role, err := store.CreateRole(ctx, repository.CreateRoleParams{ProjectID: project.ID, Name: "viewer", CreatedBy: actor.UserID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddRolePermission(ctx, repository.AddRolePermissionParams{RoleID: role.ID, Action: string(constant.ActionTransactionView)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertProjectMember(ctx, repository.UpsertProjectMemberParams{
		OrganizationID: actor.OrgID, ProjectID: project.ID, UserID: actor.UserID, RoleID: role.ID,
		ExpectedVersion: pgtype.Int8{},
	}); err != nil {
		t.Fatal(err)
	}
	return project
}

func createIntegrationDocument(t *testing.T, ctx context.Context, store repository.Store, actor authz.Actor, projectID uuid.UUID, transactionID *uuid.UUID) repository.CreateDocumentRow {
	t.Helper()
	doc, err := store.CreateDocument(ctx, repository.CreateDocumentParams{
		ProjectID: projectID, TransactionID: transactionID, StoragePath: "test/receipt.jpg",
		FileName: "receipt.jpg", FileSize: 128, MimeType: "image/jpeg",
		FileHash: pgtype.Text{String: "test-hash", Valid: true}, UploadedBy: actor.UserID,
		UploadedByName: "Test user", UploadedByEmail: "test@example.test", UploadedByRole: actor.Role,
	})
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func completedOCRMessage(documentID uuid.UUID) ocrconsumer.ProcessedMessage {
	return ocrconsumer.ProcessedMessage{
		EventID: "late-ocr-event", DocumentID: documentID.String(), ExternalRef: documentID,
		Status: ocrconsumer.StatusCompleted,
		Result: &ocrconsumer.DocumentResult{AvgConfidence: 0.95, Fields: []ocrconsumer.ExtractField{{
			Name: "total", Amount: 7500, Currency: "IDR", Status: ocrconsumer.FieldStatusExtracted,
		}}},
	}
}

type activeIntegrationIdentity struct{ contract.IdentityClient }

type frozenIntegrationIdentity struct{ activeIntegrationIdentity }

func (frozenIntegrationIdentity) HoldExpenseWriteAccess(ctx context.Context, _, _ uuid.UUID) (contract.ExpenseWriteAccessLease, error) {
	leaseCtx, release := context.WithCancel(ctx)
	return contract.ExpenseWriteAccessLease{
		Context: leaseCtx, MemberActive: true, ServiceAllowed: true, IsFrozen: true, Release: release,
	}, nil
}

func (activeIntegrationIdentity) GetOrgStatus(context.Context, uuid.UUID) (bool, bool, error) {
	return false, false, nil
}

func (activeIntegrationIdentity) GetUsersInfo(_ context.Context, pairs []contract.UserOrgPair) ([]contract.UserInfo, error) {
	users := make([]contract.UserInfo, 0, len(pairs))
	for _, pair := range pairs {
		users = append(users, contract.UserInfo{UserID: pair.UserID, Name: "Test user", Email: "test@example.test", Role: "member", IsActive: true})
	}
	return users, nil
}

func (activeIntegrationIdentity) IsMemberActiveStrict(context.Context, uuid.UUID, uuid.UUID) (bool, string, error) {
	return true, "member", nil
}

func (activeIntegrationIdentity) HoldExpenseWriteAccess(ctx context.Context, _, _ uuid.UUID) (contract.ExpenseWriteAccessLease, error) {
	leaseCtx, release := context.WithCancel(ctx)
	return contract.ExpenseWriteAccessLease{
		Context: leaseCtx, MemberActive: true, ServiceAllowed: true, Release: release,
	}, nil
}

type captureObjectStore struct {
	bucket   string
	key      string
	data     []byte
	etag     string
	putCount int
}

func (s *captureObjectStore) Put(_ context.Context, bucket, key string, _ int64, _ string, r io.Reader) (string, error) {
	s.data, _ = io.ReadAll(r)
	s.bucket, s.key, s.etag = bucket, key, "etag-is-not-the-key"
	s.putCount++
	return s.etag, nil
}

func (*captureObjectStore) Get(context.Context, string, string) (s3.FileBody, string, error) {
	return nil, "", errors.New("not implemented")
}

func (*captureObjectStore) Stat(context.Context, string, string) (int64, string, error) {
	return 0, "", errors.New("not implemented")
}

func (*captureObjectStore) Delete(context.Context, string, string) error { return nil }

func (*captureObjectStore) PresignGet(_ context.Context, _, key string, _ time.Duration, _ string) (string, error) {
	return "https://files.example/" + key, nil
}

type submitterFunc func(context.Context, ocr.SubmitInput) (string, error)

func (f submitterFunc) SubmitDocument(ctx context.Context, in ocr.SubmitInput) (string, error) {
	return f(ctx, in)
}

func installOutboxFailure(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS integration_reject_outbox ON outbox_events`); err != nil {
			t.Errorf("clean up outbox failure trigger: %v", err)
		}
		if _, err := pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS integration_reject_outbox()`); err != nil {
			t.Errorf("clean up outbox failure trigger function: %v", err)
		}
	})
	if _, err := pool.Exec(ctx, `CREATE FUNCTION integration_reject_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced outbox failure'; END $$`); err != nil {
		t.Fatalf("create outbox failure trigger function: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER integration_reject_outbox BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION integration_reject_outbox()`); err != nil {
		t.Fatalf("create outbox failure trigger: %v", err)
	}
}

func dropOutboxFailure(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DROP TRIGGER integration_reject_outbox ON outbox_events`); err != nil {
		t.Fatalf("drop outbox failure trigger: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP FUNCTION integration_reject_outbox()`); err != nil {
		t.Fatalf("drop outbox failure trigger function: %v", err)
	}
}

func assertProjectMemberEventCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_type = 'project_member' AND aggregate_id = $1`, projectID).Scan(&got); err != nil {
		t.Fatalf("count project member outbox events: %v", err)
	}
	if got != want {
		t.Fatalf("project member outbox event count = %d, want %d", got, want)
	}
}

func newIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "postgres:18-alpine",
			ExposedPorts: []string{"5432/tcp"},
			Env: map[string]string{
				"POSTGRES_DB":       "expense",
				"POSTGRES_USER":     "postgres",
				"POSTGRES_PASSWORD": "postgres",
			},
			WaitingFor: wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start PostgreSQL container: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := container.Terminate(cleanupCtx); err != nil {
			t.Errorf("terminate PostgreSQL container: %v", err)
		}
	})
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	dsn := fmt.Sprintf("postgres://postgres:postgres@%s/expense?sslmode=disable", net.JoinHostPort(host, port.Port()))
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.Migrate(ctx, pool, migrations.FS, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return pool
}

func transactionParams(actor authz.Actor, projectID uuid.UUID) repository.CreateTransactionParams {
	return repository.CreateTransactionParams{
		OrganizationID: actor.OrgID,
		ProjectID:      projectID,
		Description:    pgtype.Text{String: "Race test", Valid: true},
		Currency:       "IDR",
		Category:       pgtype.Text{String: "lainnya", Valid: true},
		CreatedBy:      actor.UserID,
		CreatedByName:  "Test owner",
		CreatedByEmail: "owner@example.test",
		CreatedByRole:  authz.RoleOwner,
	}
}

func waitForLockWaiters(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var got int
		err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()`).Scan(&got)
		if err != nil {
			t.Fatalf("inspect PostgreSQL lock waiters: %v", err)
		}
		if got >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d PostgreSQL lock waiters", want)
}

func receiveError(t *testing.T, ctx context.Context, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		t.Fatal("operation did not finish before context expired")
		return ctx.Err()
	}
}
