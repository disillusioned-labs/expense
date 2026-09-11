package approval

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/disillusioned-labs/expense/internal/authz"
	"github.com/disillusioned-labs/expense/internal/constant"
	"github.com/disillusioned-labs/expense/internal/repository"
	"github.com/disillusioned-labs/expense/internal/service"
)

// Aging builds the admin-side bottleneck list: pending transactions whose
// oldest active approval step has been waiting longer than the given number
// of days. It makes the stall visible without changing anyone's authority
// (the D14/MVP limitation stays; visibility is the remedy).
func (s *approvalService) Aging(ctx context.Context, actor authz.Actor, days int, limit, offset int32) ([]AgingItem, error) {
	ctx, span := tracer.Start(ctx, "ApprovalService.Aging")
	defer span.End()

	if err := s.authz.CheckAdmin(ctx, actor); err != nil {
		return nil, err
	}
	if days < 1 {
		return nil, service.ErrInvalidAction
	}

	cutoff := pgtype.Timestamptz{Time: time.Now().AddDate(0, 0, -days), Valid: true}
	rows, err := s.repo.ListAgingTransactions(ctx, repository.ListAgingTransactionsParams{
		OrganizationID: actor.OrgID,
		ActivatedAt:    cutoff,
		Limit:          limit,
		Offset:         offset,
	})
	if err != nil {
		return nil, err
	}

	items := make([]AgingItem, 0, len(rows))
	for _, r := range rows {
		var description *string
		if r.Description.Valid {
			d := r.Description.String
			description = &d
		}
		approvers := []string{}
		if r.PendingApproverNames != "" {
			approvers = strings.Split(r.PendingApproverNames, ", ")
		}
		items = append(items, AgingItem{
			TransactionID:    r.ID,
			Description:      description,
			TotalAmount:      r.TotalAmount,
			Currency:         r.Currency,
			ProjectID:        r.ProjectID,
			ProjectName:      r.ProjectName,
			SubmittedAt:      r.SubmittedAt.Time,
			WaitingSince:     r.WaitingSince,
			DaysWaiting:      int(time.Since(r.WaitingSince).Hours() / 24),
			PendingApprovers: approvers,
			PendingCount:     r.PendingApproverCount,
		})
	}
	return items, nil
}

// ApproverAssignments answers identity's pre-removal check (D2): is the user
// still an approver on any live rule, and which ones.
func (s *approvalService) ApproverAssignments(ctx context.Context, orgID, userID uuid.UUID) (bool, []ApproverRuleRef, error) {
	ctx, span := tracer.Start(ctx, "ApprovalService.ApproverAssignments")
	defer span.End()

	rules, err := s.repo.ListRulesByApprover(ctx, repository.ListRulesByApproverParams{
		OrganizationID: orgID,
		ApproverID:     userID,
	})
	if err != nil {
		return false, nil, err
	}
	if len(rules) == 0 {
		return false, []ApproverRuleRef{}, nil
	}
	refs := make([]ApproverRuleRef, 0, len(rules))
	for _, r := range rules {
		refs = append(refs, ApproverRuleRef{ID: r.ID, ProjectID: r.ProjectID, Step: int(r.Step)})
	}
	return true, refs, nil
}

// ReassignRules moves every live rule of fromUserID to toUserID in one
// transaction and emits the audit event that records who made the call
// (bukan auto-reassign: identity hanya memanggil ini setelah admin memilih
// penggantinya, dan nama pemutus ikut tercatat).
func (s *approvalService) ReassignRules(ctx context.Context, orgID, fromUserID, toUserID, actorID uuid.UUID) (int64, error) {
	ctx, span := tracer.Start(ctx, "ApprovalService.ReassignRules")
	defer span.End()

	if fromUserID == toUserID {
		return 0, service.ErrInvalidAction
	}
	active, err := s.authz.IsMemberActive(ctx, authz.Actor{OrgID: orgID}, toUserID)
	if err != nil {
		return 0, err
	}
	if !active {
		return 0, service.ErrApproverNotMember
	}

	var rows int64
	err = s.repo.ExecTx(ctx, func(q repository.Querier) error {
		rules, err := q.ListRulesByApprover(ctx, repository.ListRulesByApproverParams{
			OrganizationID: orgID,
			ApproverID:     fromUserID,
		})
		if err != nil {
			return err
		}
		if len(rules) == 0 {
			return service.ErrNotFound
		}

		rows, err = q.ReassignApprovalRules(ctx, repository.ReassignApprovalRulesParams{
			OrganizationID: orgID,
			ApproverID:     fromUserID,
			ApproverID_2:   toUserID,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return service.ErrNotFound
		}

		return service.Emit(ctx, q, "approval_rule", fromUserID, EventApprovalRuleReassigned, constant.TopicAudit, ApprovalRuleReassignedEvent{
			OrganizationID: orgID,
			FromUserID:     fromUserID,
			ToUserID:       toUserID,
			RuleCount:      int(rows),
			ActorID:        actorID,
		})
	})
	if err != nil {
		if service.IsUniqueViolation(err) {
			return 0, service.ErrApproverAlreadyExists
		}
		return 0, err
	}
	return rows, nil
}
