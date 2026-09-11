// Package contract defines the interfaces for external service dependencies.
// Each interface represents the expense service's view of an upstream service,
// using domain types (uuid.UUID) rather than transport types (proto strings).
// Implementations handle gRPC transport, error mapping, and fallback logic.
package contract

import (
	"context"

	"github.com/google/uuid"
)

// IdentityClient is the expense service's view of the identity gRPC service.
// All methods accept domain types and return domain errors. Implementations
// handle fallback to JWT claims when the gRPC call fails.
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
