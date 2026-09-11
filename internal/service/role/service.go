package role

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	"github.com/disillusioned-labs/expense/internal/authz"
	"github.com/disillusioned-labs/expense/internal/constant"
	"github.com/disillusioned-labs/expense/internal/repository"
	"github.com/disillusioned-labs/expense/internal/service"
)

var tracer = otel.Tracer("service/role")

type RoleService interface {
	EnsureSeed(ctx context.Context, actor authz.Actor, projectID uuid.UUID) error
	Create(ctx context.Context, actor authz.Actor, projectID uuid.UUID, in CreateInput) (Role, error)
	Get(ctx context.Context, actor authz.Actor, projectID, id uuid.UUID) (Role, error)
	List(ctx context.Context, actor authz.Actor, projectID uuid.UUID) ([]Role, error)
	Update(ctx context.Context, actor authz.Actor, projectID, id uuid.UUID, in UpdateInput) (Role, error)
	Delete(ctx context.Context, actor authz.Actor, projectID, id uuid.UUID) error
}

type roleService struct {
	repo repository.Store
	log  *slog.Logger
}

func NewRoleService(repo repository.Store, log *slog.Logger) RoleService {
	return &roleService{repo: repo, log: log}
}

func toRole(r repository.Role, actions []string) Role {
	return Role{
		ID:        r.ID,
		ProjectID: r.ProjectID,
		Name:      r.Name,
		IsSystem:  r.IsSystem,
		Actions:   actions,
		CreatedAt: r.CreatedAt.Format(time.RFC3339),
	}
}

func (s *roleService) loadActions(ctx context.Context, roleID uuid.UUID) ([]string, error) {
	ctx, span := tracer.Start(ctx, "RoleService.loadActions")
	defer span.End()

	actions, err := s.repo.ListRoleActions(ctx, roleID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "list role actions failed")
		s.log.ErrorContext(ctx, "list role actions failed", "error", err)
		return nil, err
	}

	if actions == nil {
		actions = []string{}
	}

	return actions, nil
}

func (s *roleService) EnsureSeed(ctx context.Context, actor authz.Actor, projectID uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "RoleService.EnsureSeed")
	defer span.End()

	_, err := s.repo.GetRoleByName(ctx, repository.GetRoleByNameParams{
		ProjectID: projectID,
		Name:      constant.SystemRoleViewer,
	})
	switch {
	case err == nil:
		return nil // already seeded
	case !errors.Is(err, pgx.ErrNoRows):
		span.RecordError(err)
		span.SetStatus(codes.Error, "get role by name failed")
		s.log.ErrorContext(ctx, "get role by name failed", "error", err)
		return err
	}

	return s.repo.ExecTx(ctx, func(q repository.Querier) error {
		for name, actions := range constant.SystemRoleActions() {
			r, err := q.CreateRole(ctx, repository.CreateRoleParams{
				ProjectID: projectID,
				Name:      name,
				IsSystem:  true,
				CreatedBy: actor.UserID,
			})
			if err != nil {
				if !service.IsUniqueViolation(err) {
					return err
				}
				r2, err := q.GetRoleByName(ctx, repository.GetRoleByNameParams{
					ProjectID: projectID, Name: name,
				})
				if err != nil {
					return err
				}
				r = repository.Role{
					ID: r2.ID, ProjectID: r2.ProjectID, Name: r2.Name,
					IsSystem: r2.IsSystem, CreatedBy: r2.CreatedBy,
					CreatedAt: r2.CreatedAt, UpdatedAt: r2.UpdatedAt, DeletedAt: r2.DeletedAt,
				}
			}
			for _, action := range actions {
				if err := q.AddRolePermission(ctx, repository.AddRolePermissionParams{
					RoleID: r.ID,
					Action: string(action),
				}); err != nil {
					return err
				}
			}
		}
		if _, err := q.CreateDefaultApprovalRule(ctx, repository.CreateDefaultApprovalRuleParams{
			OrganizationID: actor.OrgID,
			ApproverID:     actor.UserID,
		}); err != nil && !service.IsUniqueViolation(err) {
			span.RecordError(err)
			span.SetStatus(codes.Error, "create default approval rule failed")
			s.log.ErrorContext(ctx, "create default approval rule failed", "error", err)
			return err
		}

		return nil
	})
}

func (s *roleService) Create(ctx context.Context, actor authz.Actor, projectID uuid.UUID, in CreateInput) (Role, error) {
	ctx, span := tracer.Start(ctx, "RoleService.Create")
	defer span.End()

	for _, a := range in.Actions {
		if !constant.IsValidAction(a) {
			return Role{}, service.ErrInvalidAction
		}
	}
	name, actions := in.Name, in.Actions
	var created repository.Role
	err := s.repo.ExecTx(ctx, func(q repository.Querier) error {
		_, err := q.GetRoleByName(ctx, repository.GetRoleByNameParams{
			ProjectID: projectID,
			Name:      name,
		})
		if err == nil {
			return service.ErrRoleNameTaken
		} else if !errors.Is(err, pgx.ErrNoRows) {
			span.RecordError(err)
			span.SetStatus(codes.Error, "get role by name failed")
			s.log.ErrorContext(ctx, "get role by name failed", "error", err)
			return err
		}

		r, err := q.CreateRole(ctx, repository.CreateRoleParams{
			ProjectID: projectID,
			Name:      name,
			CreatedBy: actor.UserID,
		})
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "create role failed")
			s.log.ErrorContext(ctx, "create role failed", "error", err)
			return err
		}

		created = r
		for _, a := range actions {
			if err := q.AddRolePermission(ctx, repository.AddRolePermissionParams{RoleID: r.ID, Action: a}); err != nil {
				return err
			}
		}
		return service.Emit(ctx, q, "role", r.ID, EventRoleCreated, constant.TopicAudit, RoleCreatedEvent{
			ProjectID: projectID, RoleID: r.ID, Name: name, Actions: actions, ActorID: actor.UserID,
		})
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "create failed")
		s.log.ErrorContext(ctx, "create failed", "error", err)
		return Role{}, err
	}

	actions, loadErr := s.loadActions(ctx, created.ID)
	if loadErr != nil {
		return Role{}, loadErr
	}
	return Role{
		ID:        created.ID,
		ProjectID: created.ProjectID,
		Name:      created.Name,
		IsSystem:  created.IsSystem,
		Actions:   actions,
		CreatedAt: created.CreatedAt.Format(time.RFC3339),
	}, nil
}

func (s *roleService) Get(ctx context.Context, actor authz.Actor, projectID, id uuid.UUID) (Role, error) {
	ctx, span := tracer.Start(ctx, "RoleService.Get")
	defer span.End()

	r, err := s.get(ctx, projectID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "get failed")
		s.log.ErrorContext(ctx, "get failed", "error", err)
		return Role{}, err
	}

	actions, err := s.loadActions(ctx, r.ID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "load actions failed")
		s.log.ErrorContext(ctx, "load actions failed", "error", err)
		return Role{}, err
	}

	return toRole(r, actions), nil
}

func (s *roleService) get(ctx context.Context, projectID, id uuid.UUID) (repository.Role, error) {
	ctx, span := tracer.Start(ctx, "RoleService.get")
	defer span.End()

	r, err := s.repo.GetRole(ctx, repository.GetRoleParams{ID: id, ProjectID: projectID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.Role{}, service.ErrNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "get role failed")
		s.log.ErrorContext(ctx, "get role failed", "error", err)
		return repository.Role{}, err
	}

	return r, nil
}

func (s *roleService) List(ctx context.Context, actor authz.Actor, projectID uuid.UUID) ([]Role, error) {
	ctx, span := tracer.Start(ctx, "RoleService.List")
	defer span.End()

	rows, err := s.repo.ListRoles(ctx, projectID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "list roles failed")
		s.log.ErrorContext(ctx, "list roles failed", "error", err)
		return nil, err
	}

	out := make([]Role, 0, len(rows))
	for _, r := range rows {
		actions, err := s.loadActions(ctx, r.ID)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "load actions failed")
			s.log.ErrorContext(ctx, "load actions failed", "error", err)
			return nil, err
		}

		out = append(out, Role{
			ID:        r.ID,
			ProjectID: r.ProjectID,
			Name:      r.Name,
			IsSystem:  r.IsSystem,
			Actions:   actions,
			CreatedAt: r.CreatedAt.Format(time.RFC3339),
		})
	}

	return out, nil
}

func (s *roleService) Update(ctx context.Context, actor authz.Actor, projectID, id uuid.UUID, in UpdateInput) (Role, error) {
	ctx, span := tracer.Start(ctx, "RoleService.Update")
	defer span.End()

	if in.Actions != nil {
		for _, a := range *in.Actions {
			if !constant.IsValidAction(a) {
				return Role{}, service.ErrInvalidAction
			}
		}
	}
	name, actions := in.Name, in.Actions
	existing, err := s.get(ctx, projectID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "update failed")
		s.log.ErrorContext(ctx, "update failed", "error", err)
		return Role{}, err
	}

	if existing.IsSystem {
		return Role{}, service.ErrSystemRoleLocked
	}

	if err := s.repo.ExecTx(ctx, func(q repository.Querier) error {
		if name != nil {
			if _, err := q.UpdateRoleName(ctx, repository.UpdateRoleNameParams{ID: id, Name: *name, ProjectID: projectID}); err != nil {
				if service.IsUniqueViolation(err) {
					return service.ErrRoleNameTaken
				}

				return err
			}
		}
		if actions != nil {
			if err := q.DeleteRolePermissions(ctx, id); err != nil {
				return err
			}
			for _, a := range *actions {
				if err := q.AddRolePermission(ctx, repository.AddRolePermissionParams{RoleID: id, Action: a}); err != nil {
					return err
				}
			}
		}
		return service.Emit(ctx, q, "role", id, EventRoleUpdated, constant.TopicAudit, RoleUpdatedEvent{
			ProjectID: projectID, RoleID: id, ActorID: actor.UserID,
		})
	}); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "update failed")
		s.log.ErrorContext(ctx, "update failed", "error", err)
		return Role{}, err
	}

	return s.Get(ctx, actor, projectID, id)
}

func (s *roleService) Delete(ctx context.Context, actor authz.Actor, projectID, id uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "RoleService.Delete")
	defer span.End()

	existing, err := s.get(ctx, projectID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "delete failed")
		s.log.ErrorContext(ctx, "delete failed", "error", err)
		return err
	}

	if existing.IsSystem {
		return service.ErrSystemRoleLocked
	}
	usage, err := s.repo.CountRoleUsage(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "count role usage failed")
		s.log.ErrorContext(ctx, "count role usage failed", "error", err)
		return err
	}

	if usage > 0 {
		return service.ErrRoleInUse
	}
	return s.repo.ExecTx(ctx, func(q repository.Querier) error {
		rows, err := q.SoftDeleteRole(ctx, repository.SoftDeleteRoleParams{ID: id, ProjectID: projectID})
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "soft delete failed")
			s.log.ErrorContext(ctx, "soft delete failed", "error", err)
			return err
		}

		if rows == 0 {
			return service.ErrNotFound
		}
		return service.Emit(ctx, q, "role", id, EventRoleDeleted, constant.TopicAudit, RoleDeletedEvent{
			ProjectID: projectID, RoleID: id, ActorID: actor.UserID,
		})
	})
}
