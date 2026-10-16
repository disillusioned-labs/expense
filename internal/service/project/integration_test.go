//go:build integration

package project

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/disillusioned-labs/expense/db/migrations"
	"github.com/disillusioned-labs/expense/internal/authz"
	"github.com/disillusioned-labs/expense/internal/constant"
	"github.com/disillusioned-labs/expense/internal/contract"
	"github.com/disillusioned-labs/expense/internal/repository"
	"github.com/disillusioned-labs/expense/internal/service"
	documentservice "github.com/disillusioned-labs/expense/internal/service/document"
	"github.com/disillusioned-labs/expense/internal/service/ocrconsumer"
	transactionservice "github.com/disillusioned-labs/expense/internal/service/transaction"
	"github.com/disillusioned-labs/platform/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

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

func TestDocumentDeleteSerializesWithOCRAndRejectsFinalTransactions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := newIntegrationPool(t, ctx)
	store := repository.NewStore(pool)
	log := slog.New(slog.DiscardHandler)
	actor := authz.Actor{OrgID: uuid.New(), UserID: uuid.New(), Role: "member"}
	permissions := authz.NewAuthorizer(store, activeIntegrationIdentity{}, log)
	documents := documentservice.NewDocumentService(store, permissions, nil, nil, nil, log)

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

func (activeIntegrationIdentity) IsMemberActiveStrict(context.Context, uuid.UUID, uuid.UUID) (bool, string, error) {
	return true, "member", nil
}

func (activeIntegrationIdentity) HoldExpenseWriteAccess(ctx context.Context, _, _ uuid.UUID) (contract.ExpenseWriteAccessLease, error) {
	leaseCtx, release := context.WithCancel(ctx)
	return contract.ExpenseWriteAccessLease{
		Context: leaseCtx, MemberActive: true, ServiceAllowed: true, Release: release,
	}, nil
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
