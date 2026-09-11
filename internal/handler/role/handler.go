// Owner/admin-only role catalog endpoints scoped to a project; lazily seeds
// the system roles on first touch.
package role

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	"github.com/disillusioned-labs/expense/internal/authz"
	"github.com/disillusioned-labs/expense/internal/handler"
	roleservice "github.com/disillusioned-labs/expense/internal/service/role"
)

var tracer = otel.Tracer("handler/role")

type Handler struct {
	service roleservice.RoleService
	authz   *authz.Authorizer
	log     *slog.Logger
}

func NewRoleHandler(service roleservice.RoleService, authorizer *authz.Authorizer, log *slog.Logger) *Handler {
	return &Handler{service: service, authz: authorizer, log: log}
}

func (h *Handler) admin(w http.ResponseWriter, r *http.Request, projectID uuid.UUID) (actor authz.Actor, ok bool) {
	actor, ok = handler.ActorFrom(w, r)
	if !ok {
		return actor, false
	}
	if err := h.authz.CheckAdmin(r.Context(), actor); err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return actor, false
	}
	if err := h.service.EnsureSeed(r.Context(), actor, projectID); err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return actor, false
	}
	return actor, true
}

func (h *Handler) parseID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "invalid id")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) parseProjectID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "project_id"))
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "invalid project id")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) listRoles(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.listRoles")
	defer span.End()
	r = r.WithContext(ctx)

	projectID, ok := h.parseProjectID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid project id")
		return
	}

	actor, ok := h.admin(w, r, projectID)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	roles, err := h.service.List(ctx, actor, projectID)
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, toRoleResponses(roles))
}

func (h *Handler) createRole(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.createRole")
	defer span.End()
	r = r.WithContext(ctx)

	projectID, ok := h.parseProjectID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid project id")
		return
	}

	actor, ok := h.admin(w, r, projectID)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	req, ok := handler.DecodeValid[CreateRequest](w, r)
	if !ok {
		span.SetStatus(codes.Error, "decode/validate failed")
		return
	}

	role, err := h.service.Create(ctx, actor, projectID, req.ToInput())
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusCreated, toRoleResponse(role))
}

func (h *Handler) updateRole(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.updateRole")
	defer span.End()
	r = r.WithContext(ctx)

	projectID, ok := h.parseProjectID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid project id")
		return
	}

	actor, ok := h.admin(w, r, projectID)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	id, ok := h.parseID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid id")
		return
	}

	req, ok := handler.DecodeValid[UpdateRequest](w, r)
	if !ok {
		span.SetStatus(codes.Error, "decode/validate failed")
		return
	}

	role, err := h.service.Update(ctx, actor, projectID, id, req.ToInput())
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, toRoleResponse(role))
}

func (h *Handler) deleteRole(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.deleteRole")
	defer span.End()
	r = r.WithContext(ctx)

	projectID, ok := h.parseProjectID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid project id")
		return
	}

	actor, ok := h.admin(w, r, projectID)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	id, ok := h.parseID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid id")
		return
	}

	if err := h.service.Delete(ctx, actor, projectID, id); err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, DeleteResponse{Deleted: true})
}
