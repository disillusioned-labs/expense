//go:build integration

package project

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/disillusioned-labs/expense/internal/authz"
	"github.com/disillusioned-labs/expense/internal/constant"
	"github.com/disillusioned-labs/expense/internal/repository"
	"github.com/disillusioned-labs/expense/internal/service"
	approvalservice "github.com/disillusioned-labs/expense/internal/service/approval"
	transactionservice "github.com/disillusioned-labs/expense/internal/service/transaction"
)

func TestConcurrentFinalApprovalDecisionsSerializeOnTransaction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := newIntegrationPool(t, ctx)
	store := repository.NewStore(pool)
	actor := authz.Actor{OrgID: uuid.New(), UserID: uuid.New(), Role: "member"}
	identity := activeIntegrationIdentity{}
	log := slog.New(slog.DiscardHandler)
	approvals := approvalservice.NewApprovalService(store, authz.NewAuthorizer(store, identity, log), identity, log)

	project, err := store.CreateProject(ctx, repository.CreateProjectParams{
		OrganizationID: actor.OrgID,
		Name:           "approval decision race",
		CreatedBy:      actor.UserID,
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.CreateTransaction(ctx, transactionParams(actor, project.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE transactions SET status = 'pending_approval', total_amount = 1000 WHERE id = $1`, tx.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateApproval(ctx, repository.CreateApprovalParams{
		TransactionID: tx.ID,
		ApproverID:    actor.UserID,
		ApproverName:  "Approver",
		ApproverEmail: "approver@example.test",
		ApproverRole:  actor.Role,
		Step:          1,
		ActivatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListApprovalsByTransaction(ctx, tx.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListApprovalsByTransaction() = %d rows, %v; want 1 row", len(rows), err)
	}

	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	if _, err := holder.Exec(ctx, `SELECT id FROM transactions WHERE id = $1 FOR UPDATE`, tx.ID); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := approvals.Decide(ctx, actor, rows[0].ID, approvalservice.DecideInput{Decision: constant.DecisionApproved})
			results <- err
		}()
	}
	close(start)
	waitForLockWaiters(t, ctx, pool, 2)
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	succeeded, rejected := 0, 0
	for range 2 {
		err := receiveError(t, ctx, results)
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, service.ErrInvalidTransition):
			rejected++
		default:
			t.Fatalf("Decide() error = %v; want one success and one invalid transition", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("decision outcomes = %d successes, %d rejected; want 1 each", succeeded, rejected)
	}

	var decisionCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM approval_decisions WHERE approval_id = $1`, rows[0].ID).Scan(&decisionCount); err != nil {
		t.Fatal(err)
	}
	final, err := store.GetTransaction(ctx, repository.GetTransactionParams{ID: tx.ID, OrganizationID: actor.OrgID})
	if err != nil {
		t.Fatal(err)
	}
	if decisionCount != 1 || final.Status != constant.StatusApproved {
		t.Fatalf("final state = %d decisions, %q; want 1 decision and approved", decisionCount, final.Status)
	}
}

func (activeIntegrationIdentity) IsMemberActive(context.Context, uuid.UUID, uuid.UUID) (bool, string, error) {
	return true, "member", nil
}

type approvalRaceIdentity struct{ activeIntegrationIdentity }

func (approvalRaceIdentity) GetMemberRole(context.Context, uuid.UUID, uuid.UUID) (string, error) {
	return "member", nil
}

func TestTransactionSubmitAndDeleteSerializeInBothOrders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := newIntegrationPool(t, ctx)
	store := repository.NewStore(pool)
	actor := authz.Actor{OrgID: uuid.New(), UserID: uuid.New(), Role: "member"}
	identity := approvalRaceIdentity{}
	log := slog.New(slog.DiscardHandler)
	project, err := store.CreateProject(ctx, repository.CreateProjectParams{
		OrganizationID: actor.OrgID,
		Name:           "submit and delete race",
		CreatedBy:      actor.UserID,
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
	if err := store.AddRolePermission(ctx, repository.AddRolePermissionParams{
		RoleID: role.ID, Action: string(constant.ActionTransactionView),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertProjectMember(ctx, repository.UpsertProjectMemberParams{
		OrganizationID: actor.OrgID, ProjectID: project.ID, UserID: actor.UserID, RoleID: role.ID,
		ExpectedVersion: pgtype.Int8{},
	}); err != nil {
		t.Fatal(err)
	}
	projectID := project.ID
	if _, err := store.CreateApprovalRule(ctx, repository.CreateApprovalRuleParams{
		OrganizationID: actor.OrgID, ProjectID: &projectID,
		ApproverID: actor.UserID, Step: 1, CreatedBy: actor.UserID,
	}); err != nil {
		t.Fatal(err)
	}

	authorizer := authz.NewAuthorizer(store, identity, log)
	approvals := approvalservice.NewApprovalService(store, authorizer, identity, log)
	transactions := transactionservice.NewTransactionService(store, authorizer, nil, "", identity, log)
	for _, submitFirst := range []bool{true, false} {
		name := "delete gets lock first"
		if submitFirst {
			name = "submit gets lock first"
		}
		t.Run(name, func(t *testing.T) {
			tx, err := store.CreateTransaction(ctx, transactionParams(actor, project.ID))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.InsertTransactionItems(ctx, repository.InsertTransactionItemsParams{
				TransactionID: tx.ID, Descriptions: []string{"Receipt total"}, Quantities: []int32{1}, Amounts: []int64{1200},
			}); err != nil {
				t.Fatal(err)
			}
			createIntegrationDocument(t, ctx, store, actor, project.ID, &tx.ID)

			holder, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback(context.Background())
			if _, err := holder.Exec(ctx, `SELECT id FROM transactions WHERE id = $1 FOR UPDATE`, tx.ID); err != nil {
				t.Fatal(err)
			}

			submitResult := make(chan error, 1)
			deleteResult := make(chan error, 1)
			startSubmit := func() {
				go func() {
					_, err := approvals.Submit(ctx, actor, tx.ID)
					submitResult <- err
				}()
			}
			startDelete := func() {
				go func() { deleteResult <- transactions.Delete(ctx, actor, tx.ID) }()
			}
			if submitFirst {
				startSubmit()
			} else {
				startDelete()
			}
			waitForLockWaiters(t, ctx, pool, 1)
			if submitFirst {
				startDelete()
			} else {
				startSubmit()
			}
			waitForLockWaiters(t, ctx, pool, 2)
			if err := holder.Commit(ctx); err != nil {
				t.Fatal(err)
			}

			submitErr := receiveError(t, ctx, submitResult)
			deleteErr := receiveError(t, ctx, deleteResult)
			if submitFirst {
				if submitErr != nil || !errors.Is(deleteErr, service.ErrTransactionLocked) {
					t.Fatalf("submit-first outcomes = submit %v, delete %v; want success and TRANSACTION_LOCKED", submitErr, deleteErr)
				}
			} else if deleteErr != nil || !errors.Is(submitErr, service.ErrInvalidTransition) {
				t.Fatalf("delete-first outcomes = delete %v, submit %v; want success and INVALID_TRANSITION", deleteErr, submitErr)
			}

			var status string
			var deletedAt pgtype.Timestamptz
			if err := pool.QueryRow(ctx, `SELECT status, deleted_at FROM transactions WHERE id = $1`, tx.ID).Scan(&status, &deletedAt); err != nil {
				t.Fatal(err)
			}
			var approvalCount int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM approvals WHERE transaction_id = $1`, tx.ID).Scan(&approvalCount); err != nil {
				t.Fatal(err)
			}
			if submitFirst && (status != constant.StatusPendingApproval || deletedAt.Valid || approvalCount != 1) {
				t.Fatalf("submit-first state = status %q, deleted %v, approvals %d; want pending, live, one approval", status, deletedAt.Valid, approvalCount)
			}
			if !submitFirst && (status != constant.StatusDraft || !deletedAt.Valid || approvalCount != 0) {
				t.Fatalf("delete-first state = status %q, deleted %v, approvals %d; want deleted draft and no approvals", status, deletedAt.Valid, approvalCount)
			}
		})
	}
}
