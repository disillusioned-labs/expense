// Mutations are owner/admin-only (administrative bypass); reads are
// metadata-level and open to placed members. Business data inside a project
// is gated by role actions in the service layer (D20).
package project

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	"github.com/disillusioned-labs/expense/internal/authz"
	"github.com/disillusioned-labs/expense/internal/handler"
	projectservice "github.com/disillusioned-labs/expense/internal/service/project"
)

var tracer = otel.Tracer("handler/project")

type Handler struct {
	service projectservice.ProjectService
	authz   *authz.Authorizer
	log     *slog.Logger
}

func NewProjectHandler(service projectservice.ProjectService, authorizer *authz.Authorizer, log *slog.Logger) *Handler {
	return &Handler{service: service, authz: authorizer, log: log}
}

func (h *Handler) actor(w http.ResponseWriter, r *http.Request) (authz.Actor, bool) {
	return handler.ActorFrom(w, r)
}

func (h *Handler) admin(w http.ResponseWriter, r *http.Request) (authz.Actor, bool) {
	actor, ok := h.actor(w, r)
	if !ok {
		return actor, false
	}
	if err := h.authz.CheckAdmin(r.Context(), actor); err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return actor, false
	}
	return actor, true
}

func (h *Handler) projectID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "invalid project id")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) userIDParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "user_id"))
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "invalid user id")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) listProjects(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.listProjects")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := h.actor(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}
	page, ok := handler.DecodePage(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid pagination")
		return
	}

	includeArchived := r.URL.Query().Get("include_archived") == "true"
	projects, err := h.service.List(ctx, actor, includeArchived, page.Limit, page.Offset)
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OKList(w, toProjectResponses(projects), page.Meta())
}

func (h *Handler) createProject(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.createProject")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := h.admin(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	req, ok := handler.DecodeValid[CreateRequest](w, r)
	if !ok {
		span.SetStatus(codes.Error, "decode/validate failed")
		return
	}

	project, err := h.service.Create(ctx, actor, req.ToInput())
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusCreated, toProjectResponse(project))
}

func (h *Handler) getProject(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.getProject")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := h.actor(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}
	id, ok := h.projectID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid project id")
		return
	}

	project, err := h.service.Get(ctx, actor, id)
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, toProjectResponse(project))
}

func (h *Handler) updateProject(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.updateProject")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := h.admin(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}
	id, ok := h.projectID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid project id")
		return
	}

	req, ok := handler.DecodeValid[UpdateRequest](w, r)
	if !ok {
		span.SetStatus(codes.Error, "decode/validate failed")
		return
	}

	project, err := h.service.Update(ctx, actor, id, req.ToInput())
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, toProjectResponse(project))
}

func (h *Handler) deleteProject(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.deleteProject")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := h.admin(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}
	id, ok := h.projectID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid project id")
		return
	}
	if err := h.service.Delete(ctx, actor, id); err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, DeleteResponse{Deleted: true})
}

func (h *Handler) archiveProject(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.archiveProject")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := h.admin(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}
	id, ok := h.projectID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid project id")
		return
	}

	project, err := h.service.Archive(ctx, actor, id)
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, toProjectResponse(project))
}

func (h *Handler) unarchiveProject(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.unarchiveProject")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := h.admin(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}
	id, ok := h.projectID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid project id")
		return
	}

	project, err := h.service.Unarchive(ctx, actor, id)
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, toProjectResponse(project))
}

func (h *Handler) listProjectMembers(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.listProjectMembers")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := h.actor(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}
	id, ok := h.projectID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid project id")
		return
	}

	members, err := h.service.ListMembers(ctx, actor, id)
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, toMemberResponses(members))
}

func (h *Handler) placeProjectMember(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.placeProjectMember")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := h.admin(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}
	id, ok := h.projectID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid project id")
		return
	}

	userID, ok := h.userIDParam(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid user id")
		return
	}

	req, ok := handler.DecodeValid[PlaceMemberRequest](w, r)
	if !ok {
		span.SetStatus(codes.Error, "decode/validate failed")
		return
	}

	member, err := h.service.PlaceMember(ctx, actor, id, req.ToInput(userID))
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, toMemberResponse(member))
}

func (h *Handler) removeProjectMember(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.removeProjectMember")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := h.admin(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}
	id, ok := h.projectID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid project id")
		return
	}

	userID, ok := h.userIDParam(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid user id")
		return
	}

	if err := h.service.RemoveMember(ctx, actor, id, userID); err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, RemoveMemberResponse{Removed: true})
}
