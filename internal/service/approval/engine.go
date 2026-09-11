package approval

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/disillusioned-labs/expense/internal/authz"
	"github.com/disillusioned-labs/expense/internal/constant"
	"github.com/disillusioned-labs/expense/internal/contract"
	"github.com/disillusioned-labs/expense/internal/repository"
	"github.com/disillusioned-labs/expense/internal/service"
	transactionservice "github.com/disillusioned-labs/expense/internal/service/transaction"
	"github.com/disillusioned-labs/platform/pgutil"
)

func (s *approvalService) submitTx(ctx context.Context, actor authz.Actor, txID uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "ApprovalService.submitTx")
	defer span.End()

	return s.repo.ExecTx(ctx, func(q repository.Querier) error {
		t, err := q.LockTransaction(ctx, txID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return service.ErrNotFound
			}
			return err
		}
		if t.OrganizationID != actor.OrgID {
			return service.ErrNotFound
		}
		if t.DeletedAt.Valid || t.Status != constant.StatusDraft {
			return service.ErrInvalidTransition
		}

		docs, err := q.CountTransactionDocuments(ctx, &t.ID)
		if err != nil {
			return err
		}
		if docs < 1 || t.TotalAmount <= 0 {
			return service.ErrSubmitValidation
		}

		projectID := t.ProjectID
		rules, err := q.ResolveProjectRules(ctx, repository.ResolveProjectRulesParams{
			OrganizationID: actor.OrgID,
			ProjectID:      &projectID,
		})
		if err != nil {
			return err
		}
		if len(rules) == 0 {
			rules, err = q.ResolveDefaultRules(ctx, actor.OrgID)
			if err != nil {
				return err
			}
		}
		if len(rules) == 0 {
			return service.ErrNoApproverResolved
		}

		rows, err := q.SubmitTransaction(ctx, txID)
		if err != nil {
			return err
		}
		if rows == 0 {
			return service.ErrInvalidTransition
		}

		firstStep := rules[0].Step
		// Snapshot real approver names/emails (and feed the activation
		// notification) with one batch identity lookup. The call sits inside
		// the tx because rules are only known here, and degrades to the
		// "unknown" snapshot when identity is unreachable - submit must not
		// fail over missing profile data.
		approverIDs := make([]uuid.UUID, 0, len(rules))
		seen := make(map[uuid.UUID]bool, len(rules))
		for _, r := range rules {
			if !seen[r.ApproverID] {
				seen[r.ApproverID] = true
				approverIDs = append(approverIDs, r.ApproverID)
			}
		}
		pairs := make([]contract.UserOrgPair, 0, len(approverIDs))
		for _, id := range approverIDs {
			pairs = append(pairs, contract.UserOrgPair{UserID: id, OrganizationID: actor.OrgID})
		}
		approverInfo := make(map[uuid.UUID]contract.UserInfo, len(approverIDs))
		if users, err := s.identity.GetUsersInfo(ctx, pairs); err != nil {
			s.log.WarnContext(ctx, "approver lookup failed, snapshots degrade to unknown", "error", err)
		} else {
			for _, u := range users {
				approverInfo[u.UserID] = u
			}
		}
		approverName := func(id uuid.UUID) string {
			if u, ok := approverInfo[id]; ok && u.Name != "" {
				return u.Name
			}
			return snapshotUnknownName
		}
		approverEmail := func(id uuid.UUID) string {
			if u, ok := approverInfo[id]; ok && u.Email != "" {
				return u.Email
			}
			return snapshotUnknownEmail
		}

		var firstApproverID uuid.UUID
		for _, r := range rules {
			activated := r.Step == firstStep
			var activatedAt pgtype.Timestamptz
			if activated {
				activatedAt = pgutil.Now()
				firstApproverID = r.ApproverID
			}
			approverRole, err := s.memberRole(ctx, actor.OrgID, r.ApproverID)
			if err != nil {
				return err
			}
			if err := q.CreateApproval(ctx, repository.CreateApprovalParams{
				TransactionID: t.ID,
				ApproverID:    r.ApproverID,
				ApproverName:  approverName(r.ApproverID),
				ApproverEmail: approverEmail(r.ApproverID),
				ApproverRole:  approverRole,
				Step:          r.Step,
				ActivatedAt:   activatedAt,
			}); err != nil {
				return err
			}
		}

		// Notify the first approver that a transaction is waiting on them.
		if firstApproverID != uuid.Nil {
			description := ""
			if t.Description.Valid {
				description = t.Description.String
			}
			if err := emitStepActivated(ctx, q, actor.OrgID, t.ID, firstApproverID,
				approverName(firstApproverID), approverEmail(firstApproverID),
				description, t.TotalAmount, t.Currency, int(firstStep)); err != nil {
				return err
			}
		}

		return service.Emit(ctx, q, "transaction", t.ID, transactionservice.EventTransactionSubmitted, constant.TopicAudit,
			transactionservice.TransactionSubmittedEvent{
				OrganizationID: actor.OrgID, TransactionID: t.ID,
				ActorID: actor.UserID,
			})
	})
}

func (s *approvalService) Submit(ctx context.Context, actor authz.Actor, transactionID uuid.UUID) (SubmitResult, error) {
	ctx, span := tracer.Start(ctx, "ApprovalService.Submit")
	defer span.End()

	if err := s.authz.CheckOrgWrite(ctx, actor); err != nil {
		return SubmitResult{}, err
	}
	if err := s.submitTx(ctx, actor, transactionID); err != nil {
		return SubmitResult{}, err
	}
	first, err := s.firstActivatedApprover(ctx, transactionID)
	if err != nil {
		return SubmitResult{}, err
	}
	return SubmitResult{
		TransactionID:   transactionID,
		Status:          constant.StatusPendingApproval,
		FirstApproverID: first,
	}, nil
}

func (s *approvalService) firstActivatedApprover(ctx context.Context, txID uuid.UUID) (uuid.UUID, error) {
	ctx, span := tracer.Start(ctx, "ApprovalService.firstActivatedApprover")
	defer span.End()

	approvals, err := s.repo.ListApprovalsByTransaction(ctx, txID)
	if err != nil {
		return uuid.Nil, err
	}
	for _, a := range approvals {
		if a.ActivatedAt.Valid && !a.CancelledAt.Valid {
			return a.ApproverID, nil
		}
	}
	return uuid.Nil, nil
}

func (s *approvalService) Decide(ctx context.Context, actor authz.Actor, approvalID uuid.UUID, in DecideInput) (DecideResult, error) {
	ctx, span := tracer.Start(ctx, "ApprovalService.Decide")
	defer span.End()

	decision, notes := in.Decision, in.Notes
	if decision != constant.DecisionApproved && decision != constant.DecisionRejected {
		return DecideResult{}, service.ErrInvalidTransition
	}
	if decision == constant.DecisionRejected && (notes == nil || *notes == "") {
		return DecideResult{}, service.ErrSubmitValidation
	}

	var result DecideResult
	err := s.repo.ExecTx(ctx, func(q repository.Querier) error {
		a, err := q.GetApprovalForDecide(ctx, approvalID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return service.ErrNotFound
			}
			return err
		}
		if a.OrganizationID != actor.OrgID {
			return service.ErrNotFound
		}
		if a.TransactionStatus != constant.StatusPendingApproval {
			return service.ErrInvalidTransition
		}
		if !a.ActivatedAt.Valid {
			return service.ErrNotYourTurn
		}
		if a.CancelledAt.Valid {
			return service.ErrInvalidTransition
		}
		has, err := q.ApprovalHasDecision(ctx, a.ID)
		if err != nil {
			return err
		}
		if has {
			return service.ErrInvalidTransition
		}
		if a.ApproverID != actor.UserID {
			return service.ErrForbidden
		}
		active, err := s.authz.IsMemberActive(ctx, actor, actor.UserID)
		if err != nil {
			return err
		}
		if !active {
			return service.ErrForbidden
		}

		if _, err := q.CreateApprovalDecision(ctx, repository.CreateApprovalDecisionParams{
			ApprovalID:     a.ID,
			DecidedBy:      actor.UserID,
			DecidedByName:  snapshotUnknownName,
			DecidedByEmail: snapshotUnknownEmail,
			DecidedByRole:  actor.Role,
			Decision:       decision,
			Notes:          pgutil.Text(notes),
		}); err != nil {
			return err
		}

		nextStepActivated := false
		var activatedApproval repository.Approval
		if decision == constant.DecisionRejected {
			if _, err := q.RejectTransaction(ctx, a.TransactionID); err != nil {
				return err
			}
			if _, err := q.CancelPendingApprovals(ctx, a.TransactionID); err != nil {
				return err
			}
		} else if nextStepActivated, activatedApproval, err = s.activateNext(ctx, q, a.TransactionID); err != nil {
			return err
		}

		// Notify the creator that a decision landed, and the next approver
		// that their step just went active. Both are transactional outbox
		// emissions, atomic with the decision itself.
		description := ""
		if a.Description.Valid {
			description = a.Description.String
		}
		if err := emitDecisionReceived(ctx, q, a.OrganizationID, a.TransactionID, a.CreatedBy,
			a.CreatedByEmail, a.CreatedByName, description, a.TotalAmount, a.Currency,
			decision, a.ApproverName); err != nil {
			return err
		}
		if nextStepActivated {
			if err := emitStepActivated(ctx, q, a.OrganizationID, a.TransactionID, activatedApproval.ApproverID,
				activatedApproval.ApproverName, activatedApproval.ApproverEmail,
				description, a.TotalAmount, a.Currency, int(activatedApproval.Step)); err != nil {
				return err
			}
		}

		tStatus := constant.StatusApproved
		switch {
		case decision == constant.DecisionRejected:
			tStatus = constant.StatusRejected
		case nextStepActivated:
			tStatus = constant.StatusPendingApproval
		}
		if err := service.Emit(ctx, q, "transaction", a.TransactionID, transactionservice.EventTransactionDecided, constant.TopicAudit,
			transactionservice.TransactionDecidedEvent{
				OrganizationID: actor.OrgID, TransactionID: a.TransactionID,
				ApprovalID: a.ID, Decision: decision, ActorID: actor.UserID,
			}); err != nil {
			return err
		}

		result = DecideResult{
			Approval: ApprovalStateView{
				ID:          a.ID,
				Step:        a.Step,
				State:       "decided",
				Approver:    ActorSnapshot{ID: a.ApproverID, Name: a.ApproverName, Email: a.ApproverEmail},
				ActivatedAt: pgutil.TimePtr(a.ActivatedAt),
			},
			TransactionStatus: tStatus,
			NextStepActivated: nextStepActivated,
		}
		return nil
	})
	if err != nil {
		return DecideResult{}, err
	}
	return result, nil
}

// activateNext advances the approval chain: it activates the lowest pending
// step, or approves the transaction when every step is decided. When a step
// goes active it returns that approval row so the caller can notify the
// approver.
func (s *approvalService) activateNext(ctx context.Context, q repository.Querier, txID uuid.UUID) (bool, repository.Approval, error) {
	ctx, span := tracer.Start(ctx, "ApprovalService.activateNext")
	defer span.End()

	approvals, err := q.ListApprovalsByTransaction(ctx, txID)
	if err != nil {
		return false, repository.Approval{}, err
	}

	decisions, err := q.ListDecisionsByTransaction(ctx, txID)
	if err != nil {
		return false, repository.Approval{}, err
	}

	decided := make(map[uuid.UUID]bool, len(decisions))
	for _, d := range decisions {
		decided[d.ApprovalID] = true
	}

	nextStep := int16(-1)
	for _, ap := range approvals {
		if ap.CancelledAt.Valid {
			continue
		}
		if ap.ActivatedAt.Valid && !decided[ap.ID] {
			return false, repository.Approval{}, nil // a sibling in this step still owes a decision
		}
		if !ap.ActivatedAt.Valid && (nextStep == -1 || ap.Step < nextStep) {
			nextStep = ap.Step
		}
	}
	if nextStep == -1 {
		if _, err := q.ApproveTransaction(ctx, txID); err != nil {
			return false, repository.Approval{}, err
		}
		return false, repository.Approval{}, nil
	}
	if _, err := q.ActivateApprovalStep(ctx, repository.ActivateApprovalStepParams{
		TransactionID: txID,
		Step:          nextStep,
	}); err != nil {
		return false, repository.Approval{}, err
	}
	for _, ap := range approvals {
		if ap.Step == nextStep && !ap.CancelledAt.Valid {
			return true, ap, nil
		}
	}
	return true, repository.Approval{}, nil
}

func (s *approvalService) Assigned(ctx context.Context, actor authz.Actor) ([]AssignedItem, error) {
	ctx, span := tracer.Start(ctx, "ApprovalService.Assigned")
	defer span.End()

	rows, err := s.repo.ListPendingForUser(ctx, repository.ListPendingForUserParams{
		ApproverID:     actor.UserID,
		OrganizationID: actor.OrgID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]AssignedItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, AssignedItem{
			ApprovalID:    r.ID,
			Step:          r.Step,
			ActivatedAt:   r.ActivatedAt.Time,
			TransactionID: r.TransactionID,
			Status:        r.TransactionStatus,
			TotalAmount:   r.TotalAmount,
			Currency:      r.Currency,
			Description:   pgutil.TextPtr(r.Description),
			Category:      pgutil.TextPtr(r.Category),
			ProjectID:     r.ProjectID,
			ProjectName:   r.ProjectName,
			CreatedBy:     ActorSnapshot{ID: r.CreatedBy, Name: r.CreatedByName, Email: r.CreatedByEmail, Role: r.CreatedByRole},
			DocumentCount: r.DocumentCount,
			SubmittedAt:   r.SubmittedAt.Time,
		})
	}
	return out, nil
}
