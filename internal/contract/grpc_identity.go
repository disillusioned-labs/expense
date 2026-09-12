package contract

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	identitypb "github.com/disillusioned-labs/platform/contract/identity"
	platformgrpc "github.com/disillusioned-labs/platform/grpc"
)

var tracer = otel.Tracer("contract/identity")

// grpcIdentityClient wraps the identity gRPC client with domain types,
// fallback logic, and observability.
type grpcIdentityClient struct {
	client identitypb.IdentityServiceClient
	log    *slog.Logger
}

func NewGRPCIdentityClient(conn *platformgrpc.Client, log *slog.Logger) IdentityClient {
	return &grpcIdentityClient{
		client: identitypb.NewIdentityServiceClient(conn.Conn()),
		log:    log,
	}
}

func (c *grpcIdentityClient) IsMemberActive(ctx context.Context, orgID, userID uuid.UUID) (bool, string, error) {
	ctx, span := tracer.Start(ctx, "IdentityClient.IsMemberActive")
	defer span.End()

	span.SetAttributes(
		attribute.String("organization.id", orgID.String()),
		attribute.String("user.id", userID.String()),
	)

	resp, err := c.client.IsMemberActive(ctx, &identitypb.IsMemberActiveRequest{
		OrganizationId: orgID.String(),
		UserId:         userID.String(),
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "gRPC call failed")
		c.log.WarnContext(ctx, "identity gRPC unavailable, falling back to claims",
			"error", err, "user_id", userID, "org_id", orgID)
		return true, "", nil
	}

	return resp.IsActive, resp.Role, nil
}

func (c *grpcIdentityClient) GetMemberRole(ctx context.Context, orgID, userID uuid.UUID) (string, error) {
	ctx, span := tracer.Start(ctx, "IdentityClient.GetMemberRole")
	defer span.End()

	span.SetAttributes(
		attribute.String("organization.id", orgID.String()),
		attribute.String("user.id", userID.String()),
	)

	resp, err := c.client.GetMemberRole(ctx, &identitypb.GetMemberRoleRequest{
		OrganizationId: orgID.String(),
		UserId:         userID.String(),
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "gRPC call failed")
		c.log.WarnContext(ctx, "identity gRPC unavailable, falling back to claims",
			"error", err, "user_id", userID, "org_id", orgID)
		return "", nil
	}

	return resp.Role, nil
}

func (c *grpcIdentityClient) GetUsersInfo(ctx context.Context, pairs []UserOrgPair) ([]UserInfo, error) {
	ctx, span := tracer.Start(ctx, "IdentityClient.GetUsersInfo")
	defer span.End()

	span.SetAttributes(attribute.Int("user.count", len(pairs)))

	pbPairs := make([]*identitypb.UserOrgPair, 0, len(pairs))
	for _, p := range pairs {
		pbPairs = append(pbPairs, &identitypb.UserOrgPair{
			UserId:         p.UserID.String(),
			OrganizationId: p.OrganizationID.String(),
		})
	}

	resp, err := c.client.GetUsersInfo(ctx, &identitypb.GetUsersInfoRequest{
		Users: pbPairs,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "gRPC call failed")
		c.log.WarnContext(ctx, "identity gRPC unavailable, falling back to claims",
			"error", err, "pair_count", len(pairs))
		return nil, nil
	}

	users := make([]UserInfo, 0, len(resp.GetUsers()))
	for _, u := range resp.GetUsers() {
		uid, err := uuid.Parse(u.GetUserId())
		if err != nil {
			c.log.WarnContext(ctx, "invalid user_id in GetUsersInfo response",
				"user_id", u.GetUserId(), "error", err)
			continue
		}
		users = append(users, UserInfo{
			UserID:   uid,
			Name:     u.GetName(),
			Email:    u.GetEmail(),
			Role:     u.GetRole(),
			IsActive: u.GetIsActive(),
		})
	}

	return users, nil
}

func (c *grpcIdentityClient) GetOrgStatus(ctx context.Context, orgID uuid.UUID) (bool, bool, error) {
	ctx, span := tracer.Start(ctx, "IdentityClient.GetOrgStatus")
	defer span.End()

	span.SetAttributes(attribute.String("organization.id", orgID.String()))

	resp, err := c.client.GetOrgStatus(ctx, &identitypb.GetOrgStatusRequest{
		OrganizationId: orgID.String(),
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "gRPC call failed")
		c.log.WarnContext(ctx, "identity gRPC unavailable, falling back to claims",
			"error", err, "org_id", orgID)
		return false, false, nil
	}

	return resp.GetIsFrozen(), resp.GetIsDeleted(), nil
}

func (c *grpcIdentityClient) IsServiceAccessAllowed(ctx context.Context, userID, orgID uuid.UUID, serviceName string) (bool, error) {
	ctx, span := tracer.Start(ctx, "IdentityClient.IsServiceAccessAllowed")
	defer span.End()

	span.SetAttributes(
		attribute.String("user.id", userID.String()),
		attribute.String("organization.id", orgID.String()),
		attribute.String("service.name", serviceName),
	)

	resp, err := c.client.IsServiceAccessAllowed(ctx, &identitypb.IsServiceAccessAllowedRequest{
		UserId:         userID.String(),
		OrganizationId: orgID.String(),
		ServiceName:    serviceName,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "gRPC call failed")
		c.log.WarnContext(ctx, "identity gRPC unavailable, falling back to claims",
			"error", err, "user_id", userID, "org_id", orgID, "service_name", serviceName)
		return true, nil
	}

	return resp.GetAllowed(), nil
}
