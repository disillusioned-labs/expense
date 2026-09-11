// Submit, decide, assigned (tugas saya), and the admin-only
// approval-rules CRUD.
package approval

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	"github.com/disillusioned-labs/expense/internal/handler"
	approvalservice "github.com/disillusioned-labs/expense/internal/service/approval"
)

var tracer = otel.Tracer("handler/approval")

type Handler struct {
	service approvalservice.ApprovalService
	log     *slog.Logger
}

func NewApprovalHandler(service approvalservice.ApprovalService, log *slog.Logger) *Handler {
	return &Handler{service: service, log: log}
}

func (h *Handler) idParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "invalid id")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) transactionID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "invalid transaction id")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) submitTransaction(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.submitTransaction")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := handler.ActorFrom(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	id, ok := h.transactionID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid transaction id")
		return
	}

	result, err := h.service.Submit(ctx, actor, id)
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, toSubmitResponse(result))
}

func (h *Handler) createApprovalRule(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.createApprovalRule")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := handler.ActorFrom(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	req, ok := handler.DecodeValid[CreateRuleRequest](w, r)
	if !ok {
		span.SetStatus(codes.Error, "decode/validate failed")
		return
	}

	rule, err := h.service.CreateRule(ctx, actor, req.ToInput())
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusCreated, toRuleResponse(rule))
}

func (h *Handler) listApprovalRules(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.listApprovalRules")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := handler.ActorFrom(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	var projectID *uuid.UUID
	if v := r.URL.Query().Get("project_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "invalid project_id")
			return
		}
		projectID = &id
	}

	rules, err := h.service.ListRules(ctx, actor, projectID)
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, toRuleResponses(rules))
}

func (h *Handler) updateApprovalRule(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.updateApprovalRule")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := handler.ActorFrom(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	id, ok := h.idParam(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid id")
		return
	}

	req, ok := handler.DecodeValid[UpdateRuleRequest](w, r)
	if !ok {
		span.SetStatus(codes.Error, "decode/validate failed")
		return
	}

	rule, err := h.service.UpdateRule(ctx, actor, id, req.ToInput())
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, toRuleResponse(rule))
}

func (h *Handler) deleteApprovalRule(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.deleteApprovalRule")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := handler.ActorFrom(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	id, ok := h.idParam(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid id")
		return
	}

	if err := h.service.DeleteRule(ctx, actor, id); err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, DeleteResponse{Deleted: true})
}

// listAssignedApprovals: only active approvals; a later-step approver never
// appears before the earlier step completes.
func (h *Handler) listAssignedApprovals(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.listAssignedApprovals")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := handler.ActorFrom(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	items, err := h.service.Assigned(ctx, actor)
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, toAssignedResponses(items))
}

func (h *Handler) decideApproval(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.decideApproval")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := handler.ActorFrom(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	id, ok := h.idParam(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid id")
		return
	}

	req, ok := handler.DecodeValid[DecideRequest](w, r)
	if !ok {
		span.SetStatus(codes.Error, "decode/validate failed")
		return
	}

	result, err := h.service.Decide(ctx, actor, id, req.ToInput())
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, toDecideResponse(result))
}
