package grpc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/disillusioned-labs/expense/internal/authz"
	"github.com/disillusioned-labs/expense/internal/contract"
	projectservice "github.com/disillusioned-labs/expense/internal/service/project"
	expensepb "github.com/disillusioned-labs/platform/contract/expense"
)

func TestClearRejoinedMemberProjectAccessRequiresAdminAndInactiveMember(t *testing.T) {
	orgID, adminID, memberID := uuid.New(), uuid.New(), uuid.New()
	for _, tc := range []struct {
		name        string
		role        string
		active      bool
		identityErr error
		wantCode    codes.Code
		wantCalls   int
	}{
		{name: "active admin clears inactive member", role: authz.RoleOwner, wantCode: codes.OK, wantCalls: 1},
		{name: "non-admin cannot clear access", role: "member", wantCode: codes.PermissionDenied},
		{name: "active member is never cleared", role: authz.RoleOwner, active: true, wantCode: codes.FailedPrecondition},
		{name: "identity outage fails closed", role: authz.RoleOwner, identityErr: errors.New("identity unavailable"), wantCode: codes.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projects := &projectPlacementResetStub{removed: 2}
			identity := &rejoinIdentityStub{role: tc.role, active: tc.active, err: tc.identityErr}
			server := NewExpenseServer(nil, projects, authz.NewAuthorizer(nil, identity, slog.New(slog.NewTextHandler(io.Discard, nil))), slog.New(slog.NewTextHandler(io.Discard, nil)))
			resp, err := server.ClearRejoinedMemberProjectAccess(context.Background(), &expensepb.ClearRejoinedMemberProjectAccessRequest{
				OrganizationId: orgID.String(), UserId: memberID.String(), ActorId: adminID.String(),
			})
			if status.Code(err) != tc.wantCode {
				t.Fatalf("ClearRejoinedMemberProjectAccess() code = %s, error = %v; want %s", status.Code(err), err, tc.wantCode)
			}
			if projects.calls != tc.wantCalls {
				t.Fatalf("project cleanup calls = %d, want %d", projects.calls, tc.wantCalls)
			}
			if err == nil && resp.GetRemoved() != 2 {
				t.Fatalf("removed = %d, want 2", resp.GetRemoved())
			}
		})
	}
}

type projectPlacementResetStub struct {
	projectservice.ProjectService
	calls   int
	removed int64
}

func (s *projectPlacementResetStub) ClearOrganizationMemberPlacements(context.Context, authz.Actor, uuid.UUID) (int64, error) {
	s.calls++
	return s.removed, nil
}

type rejoinIdentityStub struct {
	contract.IdentityClient
	role   string
	active bool
	err    error
}

func (s *rejoinIdentityStub) GetMemberRole(context.Context, uuid.UUID, uuid.UUID) (string, error) {
	return s.role, s.err
}

func (s *rejoinIdentityStub) IsMemberActiveStrict(context.Context, uuid.UUID, uuid.UUID) (bool, string, error) {
	return s.active, s.role, s.err
}
