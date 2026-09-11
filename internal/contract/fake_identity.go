package contract

import (
	"context"

	"github.com/google/uuid"
)

// FakeIdentityClient is a test double for IdentityClient. Each method
// delegates to the corresponding function field; nil means zero-value return.
type FakeIdentityClient struct {
	IsMemberActiveFn         func(ctx context.Context, orgID, userID uuid.UUID) (bool, string, error)
	GetMemberRoleFn          func(ctx context.Context, orgID, userID uuid.UUID) (string, error)
	GetUsersInfoFn           func(ctx context.Context, pairs []UserOrgPair) ([]UserInfo, error)
	GetOrgStatusFn           func(ctx context.Context, orgID uuid.UUID) (bool, bool, error)
	IsServiceAccessAllowedFn func(ctx context.Context, userID, orgID uuid.UUID, serviceName string) (bool, error)
}

func (f *FakeIdentityClient) IsMemberActive(ctx context.Context, orgID, userID uuid.UUID) (bool, string, error) {
	if f.IsMemberActiveFn != nil {
		return f.IsMemberActiveFn(ctx, orgID, userID)
	}
	return true, "member", nil
}

func (f *FakeIdentityClient) GetMemberRole(ctx context.Context, orgID, userID uuid.UUID) (string, error) {
	if f.GetMemberRoleFn != nil {
		return f.GetMemberRoleFn(ctx, orgID, userID)
	}
	return "member", nil
}

func (f *FakeIdentityClient) GetUsersInfo(ctx context.Context, pairs []UserOrgPair) ([]UserInfo, error) {
	if f.GetUsersInfoFn != nil {
		return f.GetUsersInfoFn(ctx, pairs)
	}
	users := make([]UserInfo, 0, len(pairs))
	for _, p := range pairs {
		users = append(users, UserInfo{
			UserID:   p.UserID,
			Name:     "unknown",
			Email:    "unknown",
			Role:     "member",
			IsActive: true,
		})
	}
	return users, nil
}

func (f *FakeIdentityClient) GetOrgStatus(ctx context.Context, orgID uuid.UUID) (bool, bool, error) {
	if f.GetOrgStatusFn != nil {
		return f.GetOrgStatusFn(ctx, orgID)
	}
	return false, false, nil
}

func (f *FakeIdentityClient) IsServiceAccessAllowed(ctx context.Context, userID, orgID uuid.UUID, serviceName string) (bool, error) {
	if f.IsServiceAccessAllowedFn != nil {
		return f.IsServiceAccessAllowedFn(ctx, userID, orgID, serviceName)
	}
	return true, nil
}
