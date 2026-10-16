// Package contract defines the interfaces for external service dependencies.
// Each interface represents the expense service's view of an upstream service,
// using domain types (uuid.UUID) rather than transport types (proto strings).
// Implementations handle gRPC transport and map errors from authorization
// lookups so callers can fail closed.
package contract

import (
	"context"

	"github.com/google/uuid"
)

// IdentityClient is the expense service's view of the identity gRPC service.
// All methods accept domain types. Authorization lookups must return errors
// when identity cannot be reached; callers cannot infer grants from a timeout.
type IdentityClient interface {
	// IsMemberActive reports whether userID is an active member of orgID.
	// Returns isActive, role ("owner"|"admin"|"member"), and error.
	IsMemberActive(ctx context.Context, orgID, userID uuid.UUID) (bool, string, error)

	// GetMemberRole returns the user's role within an organization.
	GetMemberRole(ctx context.Context, orgID, userID uuid.UUID) (string, error)

	// GetUsersInfo returns info for a batch of user-org pairs.
	GetUsersInfo(ctx context.Context, pairs []UserOrgPair) ([]UserInfo, error)

	// GetOrgStatus returns whether an organization is frozen or deleted.
	GetOrgStatus(ctx context.Context, orgID uuid.UUID) (isFrozen, isDeleted bool, err error)

	// IsServiceAccessAllowed reports whether userID can access serviceName in orgID.
	IsServiceAccessAllowed(ctx context.Context, userID, orgID uuid.UUID, serviceName string) (bool, error)

	// HoldExpenseWriteAccess keeps the Identity authorization row locked until the
	// returned lease is released or its context is canceled.
	HoldExpenseWriteAccess(ctx context.Context, orgID, userID uuid.UUID) (ExpenseWriteAccessLease, error)
}

// ExpenseWriteAccessLease is held for the full lifetime of an Expense write.
type ExpenseWriteAccessLease struct {
	Context        context.Context
	MemberActive   bool
	ServiceAllowed bool
	IsFrozen       bool
	IsDeleted      bool
	Release        context.CancelFunc
}

func (l ExpenseWriteAccessLease) Close() {
	if l.Release != nil {
		l.Release()
	}
}

// StrictMembershipChecker verifies membership for operations that require an
// explicit freshness check.
type StrictMembershipChecker interface {
	IsMemberActiveStrict(ctx context.Context, orgID, userID uuid.UUID) (bool, string, error)
}

// UserOrgPair identifies a user within an organization for batch lookups.
type UserOrgPair struct {
	UserID         uuid.UUID
	OrganizationID uuid.UUID
}

// UserInfo holds identity data for a user within an organization.
type UserInfo struct {
	UserID   uuid.UUID
	Name     string
	Email    string
	Role     string
	IsActive bool
}
