// Package grpc serves expense's internal gRPC surface. Today that is the
// ExpenseService (decision D2): identity calls expense before committing a
// member removal, because approval rules live in the expense database and
// only expense can answer whether the member is still an active approver.
package grpc

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/disillusioned-labs/expense/internal/service"
	approvalservice "github.com/disillusioned-labs/expense/internal/service/approval"
	expensepb "github.com/disillusioned-labs/platform/contract/expense"
)

var tracer = otel.Tracer("handler/grpc")

// ExpenseServer adapts the approval service to the ExpenseService proto.
type ExpenseServer struct {
	expensepb.UnimplementedExpenseServiceServer
	approvals approvalservice.ApprovalService
	log       *slog.Logger
}

func NewExpenseServer(approvals approvalservice.ApprovalService, log *slog.Logger) *ExpenseServer {
	return &ExpenseServer{approvals: approvals, log: log}
}

// CheckApproverAssignments answers identity's pre-removal check: the caller
// blocks (409 APPROVER_STILL_ASSIGNED) when has_active_rules is true.
func (s *ExpenseServer) CheckApproverAssignments(
	ctx context.Context,
	req *expensepb.CheckApproverAssignmentsRequest,
) (*expensepb.CheckApproverAssignmentsResponse, error) {
	ctx, span := tracer.Start(ctx, "ExpenseServer.CheckApproverAssignments")
	defer span.End()

	orgID, userID, err := parseOrgUser(req.GetOrganizationId(), req.GetUserId())
	if err != nil {
		return nil, err
	}

	hasRules, rules, err := s.approvals.ApproverAssignments(ctx, orgID, userID)
	if err != nil {
		return nil, writeServiceErr(ctx, err)
	}

	pbRules := make([]*expensepb.ApprovalRule, 0, len(rules))
	for _, r := range rules {
		ref := &expensepb.ApprovalRule{
			Id:   r.ID.String(),
			Step: int32(r.Step),
		}
		if r.ProjectID != nil {
			ref.ProjectId = r.ProjectID.String()
		}
		pbRules = append(pbRules, ref)
	}

	span.SetAttributes(attribute.Bool("has_active_rules", hasRules), attribute.Int("rule_count", len(pbRules)))
	return &expensepb.CheckApproverAssignmentsResponse{
		HasActiveRules: hasRules,
		Rules:          pbRules,
	}, nil
}

// ReassignApproverRules moves the member's rules to the replacement the admin
// picked, then identity commits the removal. Fails closed: if the reassign
// does not happen, identity never removes the member.
func (s *ExpenseServer) ReassignApproverRules(
	ctx context.Context,
	req *expensepb.ReassignApproverRulesRequest,
) (*expensepb.ReassignApproverRulesResponse, error) {
	ctx, span := tracer.Start(ctx, "ExpenseServer.ReassignApproverRules")
	defer span.End()

	orgID, fromID, err := parseOrgUser(req.GetOrganizationId(), req.GetFromUserId())
	if err != nil {
		return nil, err
	}
	toID, err := uuid.Parse(req.GetToUserId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid to_user_id")
	}
	actorID, err := uuid.Parse(req.GetActorId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid actor_id")
	}

	reassigned, err := s.approvals.ReassignRules(ctx, orgID, fromID, toID, actorID)
	if err != nil {
		return nil, writeServiceErr(ctx, err)
	}
	return &expensepb.ReassignApproverRulesResponse{Reassigned: reassigned}, nil
}

func parseOrgUser(orgIDStr, userIDStr string) (uuid.UUID, uuid.UUID, error) {
	orgID, err := uuid.Parse(orgIDStr)
	if err != nil {
		return uuid.Nil, uuid.Nil, status.Error(codes.InvalidArgument, "invalid organization_id")
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return uuid.Nil, uuid.Nil, status.Error(codes.InvalidArgument, "invalid user_id")
	}
	return orgID, userID, nil
}

// writeServiceErr maps domain errors to gRPC codes, context outcomes first.
func writeServiceErr(ctx context.Context, err error) error {
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		return status.Error(codes.DeadlineExceeded, "deadline exceeded")
	case ctx.Err() == context.Canceled:
		return status.Error(codes.Canceled, "canceled")
	}

	var svcErr *service.Error
	if errors.As(err, &svcErr) {
		switch svcErr.Status {
		case 400, 422:
			return status.Error(codes.InvalidArgument, svcErr.Message)
		case 403:
			return status.Error(codes.PermissionDenied, svcErr.Message)
		case 404:
			return status.Error(codes.NotFound, svcErr.Message)
		case 409:
			return status.Error(codes.AlreadyExists, svcErr.Message)
		default:
			return status.Error(codes.Internal, svcErr.Message)
		}
	}

	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
	span.SetStatus(otelcodes.Error, "internal error")
	return status.Error(codes.Internal, "internal server error")
}
