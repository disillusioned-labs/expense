package approval

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	"github.com/disillusioned-labs/expense/internal/authz"
	"github.com/disillusioned-labs/expense/internal/constant"
	"github.com/disillusioned-labs/expense/internal/contract"
	"github.com/disillusioned-labs/expense/internal/repository"
	"github.com/disillusioned-labs/expense/internal/service"
)

var tracer = otel.Tracer("service/approval")

const (
	snapshotUnknownName  = "unknown"
	snapshotUnknownEmail = "unknown"
)

type ApprovalService interface {
	CreateRule(ctx context.Context, actor authz.Actor, in CreateRuleInput) (Rule, error)
	ListRules(ctx context.Context, actor authz.Actor, projectID *uuid.UUID) ([]Rule, error)
	UpdateRule(ctx context.Context, actor authz.Actor, id uuid.UUID, in UpdateRuleInput) (Rule, error)
	DeleteRule(ctx context.Context, actor authz.Actor, id uuid.UUID) error

	Submit(ctx context.Context, actor authz.Actor, transactionID uuid.UUID) (SubmitResult, error)
	Decide(ctx context.Context, actor authz.Actor, approvalID uuid.UUID, in DecideInput) (DecideResult, error)
	Assigned(ctx context.Context, actor authz.Actor) ([]AssignedItem, error)

	// Aging lists pending transactions whose oldest active approval step has
	// waited longer than the given number of days (owner/admin scope).
	Aging(ctx context.Context, actor authz.Actor, days int, limit, offset int32) ([]AgingItem, error)

	// ApproverAssignments and ReassignRules serve identity's member-removal
	// flow over the internal gRPC channel (decision D2): identity calls
	// ApproverAssignments before committing a removal and ReassignRules when
	// the admin picks a replacement approver.
	ApproverAssignments(ctx context.Context, orgID, userID uuid.UUID) (bool, []ApproverRuleRef, error)
	ReassignRules(ctx context.Context, orgID, fromUserID, toUserID, actorID uuid.UUID) (int64, error)
}

type approvalService struct {
	repo     repository.Store
	authz    *authz.Authorizer
	identity contract.IdentityClient
	log      *slog.Logger
}

func NewApprovalService(repo repository.Store, authorizer *authz.Authorizer, identity contract.IdentityClient, log *slog.Logger) ApprovalService {
	return &approvalService{repo: repo, authz: authorizer, identity: identity, log: log}
}

func (s *approvalService) toRule(ctx context.Context, r repository.ApprovalRule, orgID uuid.UUID) Rule {
	rule := Rule{
		ID:         r.ID,
		ProjectID:  r.ProjectID,
		ApproverID: r.ApproverID,
		Step:       r.Step,
	}
	users, err := s.identity.GetUsersInfo(ctx, []contract.UserOrgPair{
		{UserID: r.ApproverID, OrganizationID: orgID},
	})
	if err == nil && len(users) > 0 {
		rule.Approver = ActorSnapshot{ID: r.ApproverID, Name: users[0].Name, Email: users[0].Email, Role: users[0].Role}
	} else {
		rule.Approver = ActorSnapshot{ID: r.ApproverID, Name: snapshotUnknownName, Email: snapshotUnknownEmail}
	}
	return rule
}

func (s *approvalService) CreateRule(ctx context.Context, actor authz.Actor, in CreateRuleInput) (Rule, error) {
	ctx, span := tracer.Start(ctx, "ApprovalService.CreateRule")
	defer span.End()

	projectID, approverID, step := in.ProjectID, in.ApproverID, in.Step
	if err := s.authz.CheckAdmin(ctx, actor); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "check admin failed")
		s.log.ErrorContext(ctx, "check admin failed", "error", err)
		return Rule{}, err
	}

	if err := s.authz.CheckOrgWrite(ctx, actor); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "check org write failed")
		s.log.ErrorContext(ctx, "check org write failed", "error", err)
		return Rule{}, err
	}

	if step < 1 || step > 10 {
		return Rule{}, service.ErrInvalidTransition
	}
	active, err := s.authz.IsMemberActive(ctx, actor, approverID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "check member active failed")
		s.log.ErrorContext(ctx, "check member active failed", "error", err)
		return Rule{}, err
	}

	if !active {
		return Rule{}, service.ErrApproverNotMember
	}
	if projectID != nil {
		if _, err := s.repo.GetProject(ctx, repository.GetProjectParams{ID: *projectID, OrganizationID: actor.OrgID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Rule{}, service.ErrNotFound
			}
			span.RecordError(err)
			span.SetStatus(codes.Error, "get project failed")
			s.log.ErrorContext(ctx, "get project failed", "error", err)
			return Rule{}, err
		}
	}

	var created repository.ApprovalRule
	err = s.repo.ExecTx(ctx, func(q repository.Querier) error {
		r, err := q.CreateApprovalRule(ctx, repository.CreateApprovalRuleParams{
			OrganizationID: actor.OrgID,
			ProjectID:      projectID,
			ApproverID:     approverID,
			Step:           step,
			CreatedBy:      actor.UserID,
		})
		if err != nil {
			if service.IsUniqueViolation(err) {
				return service.ErrApproverAlreadyExists
			}
			span.RecordError(err)
			span.SetStatus(codes.Error, "create rule failed")
			s.log.ErrorContext(ctx, "create rule failed", "error", err)
			return err
		}

		created = r
		return service.Emit(ctx, q, "approval_rule", r.ID, EventApprovalRuleCreated, constant.TopicAudit, ApprovalRuleCreatedEvent{
			OrganizationID: actor.OrgID, ProjectID: projectID,
			ApproverID: approverID, Step: int(step), ActorID: actor.UserID,
		})
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "create rule failed")
		s.log.ErrorContext(ctx, "create rule failed", "error", err)
		return Rule{}, err
	}

	return s.toRule(ctx, created, actor.OrgID), nil
}

func (s *approvalService) ListRules(ctx context.Context, actor authz.Actor, projectID *uuid.UUID) ([]Rule, error) {
	ctx, span := tracer.Start(ctx, "ApprovalService.ListRules")
	defer span.End()

	if err := s.authz.CheckAdmin(ctx, actor); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "check admin failed")
		s.log.ErrorContext(ctx, "check admin failed", "error", err)
		return nil, err
	}

	rows, err := s.repo.ListApprovalRules(ctx, repository.ListApprovalRulesParams{
		OrganizationID: actor.OrgID,
		ProjectID:      projectID,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "list approval rules failed")
		s.log.ErrorContext(ctx, "list approval rules failed", "error", err)
		return nil, err
	}

	out := make([]Rule, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.toRule(ctx, r, actor.OrgID))
	}
	return out, nil
}

func (s *approvalService) UpdateRule(ctx context.Context, actor authz.Actor, id uuid.UUID, in UpdateRuleInput) (Rule, error) {
	ctx, span := tracer.Start(ctx, "ApprovalService.UpdateRule")
	defer span.End()

	step := in.Step
	if err := s.authz.CheckAdmin(ctx, actor); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "check admin failed")
		s.log.ErrorContext(ctx, "check admin failed", "error", err)
		return Rule{}, err
	}

	if step < 1 || step > 10 {
		return Rule{}, service.ErrInvalidTransition
	}
	var updated repository.ApprovalRule
	err := s.repo.ExecTx(ctx, func(q repository.Querier) error {
		r, err := q.UpdateApprovalRuleStep(ctx, repository.UpdateApprovalRuleStepParams{
			ID: id, OrganizationID: actor.OrgID, Step: step,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return service.ErrNotFound
			}
			if service.IsUniqueViolation(err) {
				return service.ErrApproverAlreadyExists
			}
			span.RecordError(err)
			span.SetStatus(codes.Error, "update rule failed")
			s.log.ErrorContext(ctx, "update rule failed", "error", err)
			return err
		}

		updated = r
		return service.Emit(ctx, q, "approval_rule", id, EventApprovalRuleUpdated, constant.TopicAudit, ApprovalRuleUpdatedEvent{
			OrganizationID: actor.OrgID, RuleID: id, Step: int(step), ActorID: actor.UserID,
		})
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "update rule failed")
		s.log.ErrorContext(ctx, "update rule failed", "error", err)
		return Rule{}, err
	}

	return s.toRule(ctx, updated, actor.OrgID), nil
}

func (s *approvalService) DeleteRule(ctx context.Context, actor authz.Actor, id uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "ApprovalService.DeleteRule")
	defer span.End()

	if err := s.authz.CheckAdmin(ctx, actor); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "check admin failed")
		s.log.ErrorContext(ctx, "check admin failed", "error", err)
		return err
	}

	rule, err := s.repo.GetApprovalRule(ctx, repository.GetApprovalRuleParams{ID: id, OrganizationID: actor.OrgID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return service.ErrNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "get approval rule failed")
		s.log.ErrorContext(ctx, "get approval rule failed", "error", err)
		return err
	}

	if rule.ProjectID == nil {
		count, err := s.repo.CountDefaultOrgRules(ctx, actor.OrgID)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "count default org rules failed")
			s.log.ErrorContext(ctx, "count default org rules failed", "error", err)
			return err
		}

		if count <= 1 {
			return service.ErrLastApprovalRule
		}
	}

	return s.repo.ExecTx(ctx, func(q repository.Querier) error {
		rows, err := q.SoftDeleteApprovalRule(ctx, repository.SoftDeleteApprovalRuleParams{ID: id, OrganizationID: actor.OrgID})
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "soft delete failed")
			s.log.ErrorContext(ctx, "soft delete failed", "error", err)
			return err
		}

		if rows == 0 {
			return service.ErrNotFound
		}
		return service.Emit(ctx, q, "approval_rule", id, EventApprovalRuleDeleted, constant.TopicAudit, ApprovalRuleDeletedEvent{
			OrganizationID: actor.OrgID, RuleID: id, ActorID: actor.UserID,
		})
	})
}

func (s *approvalService) memberRole(ctx context.Context, orgID, userID uuid.UUID) (string, error) {
	role, err := s.identity.GetMemberRole(ctx, orgID, userID)
	if err != nil {
		return "", err
	}
	return role, nil
}
