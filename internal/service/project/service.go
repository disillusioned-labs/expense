package project

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/disillusioned-labs/expense/internal/authz"
	"github.com/disillusioned-labs/expense/internal/constant"
	"github.com/disillusioned-labs/expense/internal/repository"
	"github.com/disillusioned-labs/expense/internal/service"
)

var tracer = otel.Tracer("service/project")

var errProjectHasDocuments = service.NewError("PROJECT_HAS_DOCUMENTS", 409, "project has uploaded receipts")

type ProjectService interface {
	Create(ctx context.Context, actor authz.Actor, in CreateInput) (Project, error)
	Get(ctx context.Context, actor authz.Actor, id uuid.UUID) (Project, error)
	List(ctx context.Context, actor authz.Actor, includeArchived bool, limit, offset int32) ([]Project, error)
	Update(ctx context.Context, actor authz.Actor, id uuid.UUID, in UpdateInput) (Project, error)
	Delete(ctx context.Context, actor authz.Actor, id uuid.UUID) error
	Archive(ctx context.Context, actor authz.Actor, id uuid.UUID) (Project, error)
	Unarchive(ctx context.Context, actor authz.Actor, id uuid.UUID) (Project, error)

	PlaceMember(ctx context.Context, actor authz.Actor, projectID uuid.UUID, in PlaceMemberInput) (Member, error)
	RemoveMember(ctx context.Context, actor authz.Actor, projectID, userID uuid.UUID) error
	// ClearOrganizationMemberPlacements removes placements left by a prior membership.
	ClearOrganizationMemberPlacements(ctx context.Context, actor authz.Actor, userID uuid.UUID) (int64, error)
	ListMembers(ctx context.Context, actor authz.Actor, projectID uuid.UUID) ([]Member, error)
}

type projectService struct {
	repo  repository.Store
	authz *authz.Authorizer
	log   *slog.Logger
}

func NewProjectService(repo repository.Store, authorizer *authz.Authorizer, log *slog.Logger) ProjectService {
	return &projectService{repo: repo, authz: authorizer, log: log}
}

func toProject(p repository.Project) Project {
	return Project{
		ID:        p.ID,
		Name:      p.Name,
		CreatedBy: p.CreatedBy,
		CreatedAt: p.CreatedAt,
		Archived:  p.ArchivedAt.Valid,
	}
}

func (s *projectService) Create(ctx context.Context, actor authz.Actor, in CreateInput) (Project, error) {
	ctx, span := tracer.Start(ctx, "ProjectService.Create")
	defer span.End()

	name := in.Name

	var created repository.Project
	err := s.repo.ExecTx(ctx, func(q repository.Querier) error {
		p, err := q.CreateProject(ctx, repository.CreateProjectParams{
			OrganizationID: actor.OrgID,
			Name:           name,
			CreatedBy:      actor.UserID,
		})
		if err != nil {
			return err
		}
		created = p
		for roleName, actions := range constant.SystemRoleActions() {
			role, err := q.CreateRole(ctx, repository.CreateRoleParams{
				ProjectID: p.ID, Name: roleName, IsSystem: true, CreatedBy: actor.UserID,
			})
			if err != nil {
				return err
			}
			for _, action := range actions {
				if err := q.AddRolePermission(ctx, repository.AddRolePermissionParams{RoleID: role.ID, Action: string(action)}); err != nil {
					return err
				}
			}
		}
		if err := q.CreateDefaultApprovalRule(ctx, repository.CreateDefaultApprovalRuleParams{
			OrganizationID: actor.OrgID,
			ApproverID:     actor.UserID,
		}); err != nil {
			return err
		}
		admin, err := q.GetRoleByName(ctx, repository.GetRoleByNameParams{ProjectID: p.ID, Name: constant.SystemRoleAdmin})
		if err != nil {
			return err
		}
		if _, err := q.UpsertProjectMember(ctx, repository.UpsertProjectMemberParams{
			OrganizationID: actor.OrgID, ProjectID: p.ID, UserID: actor.UserID,
			RoleID: admin.ID, ExpectedVersion: pgtype.Int8{},
		}); err != nil {
			return err
		}
		return service.Emit(ctx, q, "project", p.ID, EventProjectCreated, constant.TopicAudit, ProjectCreatedEvent{
			OrganizationID: actor.OrgID, ProjectID: created.ID, ActorID: actor.UserID,
		})
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "create project failed")
		s.log.ErrorContext(ctx, "create project failed", "error", err)
		return Project{}, err
	}

	return toProject(created), nil
}

func (s *projectService) Get(ctx context.Context, actor authz.Actor, id uuid.UUID) (Project, error) {
	ctx, span := tracer.Start(ctx, "ProjectService.Get")
	defer span.End()

	admin, err := s.isAdmin(ctx, actor)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "check admin failed")
		return Project{}, err
	}
	if !admin {
		placed, err := s.authz.IsPlacedInProject(ctx, actor, id)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "check project placement failed")
			return Project{}, err
		}
		if !placed {
			span.SetStatus(codes.Error, "project access denied")
			return Project{}, service.ErrForbidden
		}
	}

	p, err := s.get(ctx, actor, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "get project failed")
		s.log.ErrorContext(ctx, "get project failed", "error", err)
		return Project{}, err
	}

	return toProject(p), nil
}

func (s *projectService) get(ctx context.Context, actor authz.Actor, id uuid.UUID) (repository.Project, error) {
	ctx, span := tracer.Start(ctx, "ProjectService.get")
	defer span.End()

	p, err := s.repo.GetProject(ctx, repository.GetProjectParams{ID: id, OrganizationID: actor.OrgID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.Project{}, service.ErrNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "get project failed")
		s.log.ErrorContext(ctx, "get project failed", "error", err)
		return repository.Project{}, err
	}

	return p, nil
}

func (s *projectService) List(ctx context.Context, actor authz.Actor, includeArchived bool, limit, offset int32) ([]Project, error) {
	ctx, span := tracer.Start(ctx, "ProjectService.List")
	defer span.End()

	admin, err := s.isAdmin(ctx, actor)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "check admin failed")
		s.log.ErrorContext(ctx, "check admin failed", "error", err)
		return nil, err
	}

	var rows []repository.Project
	if admin {
		rows, err = s.repo.ListProjects(ctx, repository.ListProjectsParams{
			OrganizationID:  actor.OrgID,
			IncludeArchived: includeArchived,
			Limit:           limit,
			Offset:          offset,
		})
	} else {
		rows, err = s.repo.ListPlacedProjects(ctx, repository.ListPlacedProjectsParams{
			OrganizationID:  actor.OrgID,
			UserID:          actor.UserID,
			IncludeArchived: includeArchived,
			Limit:           limit,
			Offset:          offset,
		})
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "list projects failed")
		s.log.ErrorContext(ctx, "list projects failed", "error", err)
		return nil, err
	}

	out := make([]Project, 0, len(rows))
	for _, p := range rows {
		out = append(out, toProject(p))
	}
	return out, nil
}

func (s *projectService) isAdmin(ctx context.Context, actor authz.Actor) (bool, error) {
	ctx, span := tracer.Start(ctx, "ProjectService.isAdmin")
	defer span.End()

	if err := s.authz.CheckAdmin(ctx, actor); err != nil {
		if errors.Is(err, service.ErrForbidden) {
			return false, nil
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "check admin failed")
		s.log.ErrorContext(ctx, "check admin failed", "error", err)
		return false, err
	}

	return true, nil
}

func (s *projectService) Update(ctx context.Context, actor authz.Actor, id uuid.UUID, in UpdateInput) (Project, error) {
	ctx, span := tracer.Start(ctx, "ProjectService.Update")
	defer span.End()

	name := in.Name
	var p repository.Project
	err := s.repo.ExecTx(ctx, func(q repository.Querier) error {
		var err error
		p, err = q.UpdateProjectName(ctx, repository.UpdateProjectNameParams{
			ID: id, Name: name, OrganizationID: actor.OrgID,
		})
		if err != nil {
			return err
		}
		return service.Emit(ctx, q, "project", id, EventProjectUpdated, constant.TopicAudit, ProjectUpdatedEvent{
			OrganizationID: actor.OrgID, ProjectID: id, ActorID: actor.UserID,
		})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Project{}, service.ErrNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "emit project updated failed")
		s.log.ErrorContext(ctx, "emit project updated failed", "error", err)
		return Project{}, err
	}

	return toProject(p), nil
}

func (s *projectService) Delete(ctx context.Context, actor authz.Actor, id uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "ProjectService.Delete")
	defer span.End()

	err := s.repo.ExecTx(ctx, func(q repository.Querier) error {
		if _, err := q.LockProjectForDelete(ctx, repository.LockProjectForDeleteParams{
			ID: id, OrganizationID: actor.OrgID,
		}); err != nil {
			return err
		}
		count, err := q.CountProjectTransactions(ctx, id)
		if err != nil {
			return err
		}
		if count > 0 {
			return service.ErrProjectHasTransactions
		}
		documentCount, err := q.CountProjectDocuments(ctx, id)
		if err != nil {
			return err
		}
		if documentCount > 0 {
			return errProjectHasDocuments
		}
		rows, err := q.SoftDeleteProject(ctx, repository.SoftDeleteProjectParams{ID: id, OrganizationID: actor.OrgID})
		if err != nil {
			return err
		}
		if rows == 0 {
			return service.ErrNotFound
		}
		return service.Emit(ctx, q, "project", id, EventProjectDeleted, constant.TopicAudit, ProjectDeletedEvent{
			OrganizationID: actor.OrgID, ProjectID: id, ActorID: actor.UserID,
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return service.ErrNotFound
	}
	if err != nil && !errors.Is(err, service.ErrProjectHasTransactions) && !errors.Is(err, errProjectHasDocuments) && !errors.Is(err, service.ErrNotFound) {
		span.RecordError(err)
		span.SetStatus(codes.Error, "delete project failed")
		s.log.ErrorContext(ctx, "delete project failed", "error", err)
	}
	return err
}

func (s *projectService) Archive(ctx context.Context, actor authz.Actor, id uuid.UUID) (Project, error) {
	ctx, span := tracer.Start(ctx, "ProjectService.Archive")
	defer span.End()

	var p repository.Project
	if err := s.repo.ExecTx(ctx, func(q repository.Querier) error {
		var err error
		p, err = q.ArchiveProject(ctx, repository.ArchiveProjectParams{ID: id, OrganizationID: actor.OrgID})
		if err != nil {
			return err
		}
		return service.Emit(ctx, q, "project", id, EventProjectArchived, constant.TopicAudit, ProjectArchivedEvent{
			OrganizationID: actor.OrgID, ProjectID: id, ActorID: actor.UserID,
		})
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Project{}, service.ErrNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "archive project failed")
		s.log.ErrorContext(ctx, "archive project failed", "error", err)
		return Project{}, err
	}

	return toProject(p), nil
}

func (s *projectService) Unarchive(ctx context.Context, actor authz.Actor, id uuid.UUID) (Project, error) {
	ctx, span := tracer.Start(ctx, "ProjectService.Unarchive")
	defer span.End()

	var p repository.Project
	if err := s.repo.ExecTx(ctx, func(q repository.Querier) error {
		var err error
		p, err = q.UnarchiveProject(ctx, repository.UnarchiveProjectParams{ID: id, OrganizationID: actor.OrgID})
		if err != nil {
			return err
		}
		return service.Emit(ctx, q, "project", id, EventProjectUnarchived, constant.TopicAudit, ProjectArchivedEvent{
			OrganizationID: actor.OrgID, ProjectID: id, ActorID: actor.UserID,
		})
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Project{}, service.ErrNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "unarchive project failed")
		s.log.ErrorContext(ctx, "unarchive project failed", "error", err)
		return Project{}, err
	}

	return toProject(p), nil
}

func (s *projectService) PlaceMember(ctx context.Context, actor authz.Actor, projectID uuid.UUID, in PlaceMemberInput) (Member, error) {
	ctx, span := tracer.Start(ctx, "ProjectService.PlaceMember")
	defer span.End()

	userID, roleID := in.UserID, in.RoleID
	p, err := s.get(ctx, actor, projectID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "place member failed")
		s.log.ErrorContext(ctx, "place member failed", "error", err)
		return Member{}, err
	}

	if p.ArchivedAt.Valid {
		return Member{}, service.ErrProjectArchived
	}
	active, err := s.authz.IsMemberActiveStrict(ctx, actor, userID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "verify organization membership failed")
		s.log.ErrorContext(ctx, "verify organization membership failed", "error", err)
		return Member{}, err
	}
	if !active {
		return Member{}, service.ErrProjectMemberNotActive
	}
	role, err := s.repo.GetRole(ctx, repository.GetRoleParams{ID: roleID, ProjectID: projectID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Member{}, service.ErrNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "get role failed")
		s.log.ErrorContext(ctx, "get role failed", "error", err)
		return Member{}, err
	}

	expectedVersion := pgtype.Int8{}
	if in.ExpectedVersion != nil {
		expectedVersion = pgtype.Int8{Int64: *in.ExpectedVersion, Valid: true}
	}
	var m repository.ProjectMember
	err = s.repo.ExecTx(ctx, func(q repository.Querier) error {
		locked, err := q.LockProjectForDelete(ctx, repository.LockProjectForDeleteParams{
			ID: projectID, OrganizationID: actor.OrgID,
		})
		if err != nil {
			return err
		}
		if locked.ArchivedAt.Valid {
			return service.ErrProjectArchived
		}
		m, err = q.UpsertProjectMember(ctx, repository.UpsertProjectMemberParams{
			OrganizationID:  actor.OrgID,
			ProjectID:       projectID,
			UserID:          userID,
			RoleID:          roleID,
			ExpectedVersion: expectedVersion,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return service.ErrProjectMemberChanged
			}
			return err
		}
		return service.Emit(ctx, q, "project_member", projectID, EventProjectMemberPlaced, constant.TopicAudit, ProjectMemberPlacedEvent{
			OrganizationID: actor.OrgID, ProjectID: projectID,
			UserID: userID, RoleID: roleID, ActorID: actor.UserID,
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Member{}, service.ErrNotFound
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "place member failed")
		s.log.ErrorContext(ctx, "place member failed", "error", err)
		return Member{}, err
	}

	return Member{
		UserID:    m.UserID,
		RoleID:    m.RoleID,
		RoleName:  role.Name,
		IsSystem:  role.IsSystem,
		CreatedAt: m.CreatedAt,
		Version:   m.Version,
	}, nil
}

func (s *projectService) RemoveMember(ctx context.Context, actor authz.Actor, projectID, userID uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "ProjectService.RemoveMember")
	defer span.End()

	err := s.repo.ExecTx(ctx, func(q repository.Querier) error {
		if _, err := q.LockProjectForDelete(ctx, repository.LockProjectForDeleteParams{
			ID: projectID, OrganizationID: actor.OrgID,
		}); err != nil {
			return err
		}
		rows, err := q.DeleteProjectMember(ctx, repository.DeleteProjectMemberParams{
			ProjectID: projectID,
			UserID:    userID,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return service.ErrNotFound
		}
		return service.Emit(ctx, q, "project_member", projectID, EventProjectMemberRemoved, constant.TopicAudit, ProjectMemberRemovedEvent{
			OrganizationID: actor.OrgID, ProjectID: projectID,
			UserID: userID, ActorID: actor.UserID,
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return service.ErrNotFound
	}
	if err != nil && !errors.Is(err, service.ErrNotFound) {
		span.RecordError(err)
		span.SetStatus(codes.Error, "delete project member failed")
		s.log.ErrorContext(ctx, "delete project member failed", "error", err)
	}
	return err
}

func (s *projectService) ClearOrganizationMemberPlacements(ctx context.Context, actor authz.Actor, userID uuid.UUID) (int64, error) {
	ctx, span := tracer.Start(ctx, "ProjectService.ClearOrganizationMemberPlacements")
	defer span.End()

	var projectIDs []uuid.UUID
	err := s.repo.ExecTx(ctx, func(q repository.Querier) error {
		var err error
		projectIDs, err = q.DeleteProjectMembersForOrganizationUser(ctx, repository.DeleteProjectMembersForOrganizationUserParams{
			OrganizationID: actor.OrgID,
			UserID:         userID,
		})
		if err != nil {
			return err
		}
		for _, projectID := range projectIDs {
			// Accepting the invitation triggers this cleanup, even though an admin authorizes the RPC.
			if err := service.Emit(ctx, q, "project_member", projectID, EventProjectMemberRemoved, constant.TopicAudit, ProjectMemberRemovedEvent{
				OrganizationID: actor.OrgID,
				ProjectID:      projectID,
				UserID:         userID,
				ActorID:        userID,
				Reason:         "organization_membership_reactivated",
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "clear organization member placements failed")
		s.log.ErrorContext(ctx, "clear organization member placements failed", "error", err)
		return 0, err
	}
	span.SetAttributes(attribute.Int("project_placements.removed", len(projectIDs)))
	return int64(len(projectIDs)), nil
}

func (s *projectService) ListMembers(ctx context.Context, actor authz.Actor, projectID uuid.UUID) ([]Member, error) {
	ctx, span := tracer.Start(ctx, "ProjectService.ListMembers")
	defer span.End()

	if err := s.authz.CheckAdmin(ctx, actor); err != nil {
		placed, err := s.authz.IsPlacedInProject(ctx, actor, projectID)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "check placement failed")
			s.log.ErrorContext(ctx, "check placement failed", "error", err)
			return nil, err
		}
		if !placed {
			return nil, service.ErrForbidden
		}
	}

	rows, err := s.repo.ListProjectMembers(ctx, projectID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "list project members failed")
		s.log.ErrorContext(ctx, "list project members failed", "error", err)
		return nil, err
	}

	out := make([]Member, 0, len(rows))
	for _, m := range rows {
		out = append(out, Member{
			UserID:    m.UserID,
			RoleID:    m.RoleID,
			RoleName:  m.RoleName,
			IsSystem:  m.RoleIsSystem,
			CreatedAt: m.CreatedAt,
			Version:   m.Version,
		})
	}
	return out, nil
}
