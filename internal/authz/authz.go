package authz

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/disillusioned-labs/expense/internal/constant"
	"github.com/disillusioned-labs/expense/internal/contract"
	"github.com/disillusioned-labs/expense/internal/repository"
	"github.com/disillusioned-labs/expense/internal/service"
)

const (
	RoleOwner = "owner"
	RoleAdmin = "admin"
)

type Actor struct {
	UserID uuid.UUID
	OrgID  uuid.UUID
	Role   string
}

type Authorizer struct {
	repo     repository.Store
	identity contract.IdentityClient
	log      *slog.Logger
}

func NewAuthorizer(repo repository.Store, identity contract.IdentityClient, log *slog.Logger) *Authorizer {
	return &Authorizer{repo: repo, identity: identity, log: log}
}

func (a *Authorizer) isAdmin(ctx context.Context, actor Actor) (bool, error) {
	role, err := a.identity.GetMemberRole(ctx, actor.OrgID, actor.UserID)
	if err != nil {
		return false, service.ErrIdentityUnavailable
	}
	return role == RoleOwner || role == RoleAdmin, nil
}

func (a *Authorizer) CheckOrgWrite(ctx context.Context, actor Actor) error {
	isFrozen, isDeleted, err := a.identity.GetOrgStatus(ctx, actor.OrgID)
	if err != nil {
		return service.ErrIdentityUnavailable
	}
	if isDeleted {
		return service.ErrOrgDeleted
	}
	if isFrozen {
		return service.ErrOrgFrozen
	}
	return nil
}

func (a *Authorizer) CheckAdmin(ctx context.Context, actor Actor) error {
	admin, err := a.isAdmin(ctx, actor)
	if err != nil {
		return err
	}
	if !admin {
		return service.ErrForbidden
	}
	return nil
}

func (a *Authorizer) Can(ctx context.Context, actor Actor, projectID uuid.UUID, action constant.Action) error {
	actions, err := a.repo.GetUserProjectActions(ctx, repository.GetUserProjectActionsParams{
		ProjectID: projectID,
		UserID:    actor.UserID,
	})
	if err != nil {
		return err
	}
	for _, known := range actions {
		if known == string(action) {
			return nil
		}
	}
	return service.ErrForbidden
}

func (a *Authorizer) PlacedProjectIDs(ctx context.Context, actor Actor, action constant.Action) ([]uuid.UUID, error) {
	members, err := a.repo.ListActivePlacementsWithAction(ctx, repository.ListActivePlacementsWithActionParams{
		OrganizationID: actor.OrgID,
		UserID:         actor.UserID,
		Action:         string(action),
	})
	if err != nil {
		return nil, err
	}
	return members, nil
}

func (a *Authorizer) IsPlacedInProject(ctx context.Context, actor Actor, projectID uuid.UUID) (bool, error) {
	actions, err := a.repo.GetUserProjectActions(ctx, repository.GetUserProjectActionsParams{
		ProjectID: projectID,
		UserID:    actor.UserID,
	})
	if err != nil {
		return false, err
	}
	return len(actions) > 0, nil
}

func (a *Authorizer) IsMemberActive(ctx context.Context, actor Actor, userID uuid.UUID) (bool, error) {
	active, _, err := a.identity.IsMemberActive(ctx, actor.OrgID, userID)
	if err != nil {
		return false, service.ErrIdentityUnavailable
	}
	return active, nil
}

func (a *Authorizer) IsMemberActiveStrict(ctx context.Context, actor Actor, userID uuid.UUID) (bool, error) {
	checker, ok := a.identity.(contract.StrictMembershipChecker)
	if !ok {
		return false, service.ErrIdentityUnavailable
	}
	active, _, err := checker.IsMemberActiveStrict(ctx, actor.OrgID, userID)
	if err != nil {
		return false, service.ErrIdentityUnavailable
	}
	return active, nil
}

// IsServiceAllowed checks whether the actor has access to the expense service.
func (a *Authorizer) IsServiceAllowed(ctx context.Context, actor Actor) (bool, error) {
	allowed, err := a.identity.IsServiceAccessAllowed(ctx, actor.UserID, actor.OrgID, "expense")
	if err != nil {
		return false, service.ErrIdentityUnavailable
	}
	if !allowed {
		return false, service.ErrServiceNotAllowed
	}
	return true, nil
}

// HoldExpenseWriteAccess checks permission under an Identity lock that remains
// held until the caller finishes its Expense write.
func (a *Authorizer) HoldExpenseWriteAccess(ctx context.Context, actor Actor) (contract.ExpenseWriteAccessLease, error) {
	lease, err := a.identity.HoldExpenseWriteAccess(ctx, actor.OrgID, actor.UserID)
	if err != nil || lease.Context == nil || lease.Release == nil {
		lease.Close()
		return contract.ExpenseWriteAccessLease{}, service.ErrIdentityUnavailable
	}
	if lease.IsDeleted {
		lease.Close()
		return contract.ExpenseWriteAccessLease{}, service.ErrOrgDeleted
	}
	if lease.IsFrozen {
		lease.Close()
		return contract.ExpenseWriteAccessLease{}, service.ErrOrgFrozen
	}
	if !lease.MemberActive {
		lease.Close()
		return contract.ExpenseWriteAccessLease{}, service.ErrForbidden
	}
	if !lease.ServiceAllowed {
		lease.Close()
		return contract.ExpenseWriteAccessLease{}, service.ErrServiceNotAllowed
	}
	return lease, nil
}
