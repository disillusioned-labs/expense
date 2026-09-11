// Transaction CRUD on drafts and the list/detail views. Submit lives in the
// approval handler; documents in the document handler. Data access is
// placement-gated inside the service (D20).
package transaction

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	"github.com/disillusioned-labs/expense/internal/handler"
	transactionservice "github.com/disillusioned-labs/expense/internal/service/transaction"
)

var tracer = otel.Tracer("handler/transaction")

type Handler struct {
	service transactionservice.TransactionService
	log     *slog.Logger
}

func NewTransactionHandler(service transactionservice.TransactionService, log *slog.Logger) *Handler {
	return &Handler{service: service, log: log}
}

func (h *Handler) transactionID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "invalid transaction id")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) getTransaction(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.getTransaction")
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

	detail, err := h.service.Get(ctx, actor, id)
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}

	handler.OK(w, http.StatusOK, ToDetailResponse(detail))
}

func (h *Handler) listTransactions(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.listTransactions")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := handler.ActorFrom(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	page, ok := handler.DecodePage(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid pagination")
		return
	}

	f, ok := h.listFilters(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid filters")
		return
	}

	items, err := h.service.List(ctx, actor, f, page.Limit, page.Offset)
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OKList(w, toTransactionResponses(items), page.Meta())
}

func (h *Handler) listFilters(w http.ResponseWriter, r *http.Request) (transactionservice.ListFilters, bool) {
	f := transactionservice.ListFilters{}
	if v := r.URL.Query().Get("status"); v != "" {
		f.Status = v
	}
	if v := r.URL.Query().Get("project_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "invalid project_id")
			return f, false
		}
		f.ProjectID = &id
	}
	if v := r.URL.Query().Get("created_by"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "invalid created_by")
			return f, false
		}
		f.CreatedBy = &id
	}
	if v := r.URL.Query().Get("category"); v != "" {
		f.Category = &v
	}
	for _, pair := range [][2]string{{"date_from", "DateFrom"}, {"date_to", "DateTo"}} {
		v := r.URL.Query().Get(pair[0])
		if v == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "invalid "+pair[0]+" (RFC3339)")
			return f, false
		}
		if pair[1] == "DateFrom" {
			f.DateFrom = &t
		} else {
			f.DateTo = &t
		}
	}
	return f, true
}

func (h *Handler) updateTransaction(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.updateTransaction")
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

	req, ok := handler.DecodeValid[UpdateRequest](w, r)
	if !ok {
		span.SetStatus(codes.Error, "decode/validate failed")
		return
	}

	detail, err := h.service.Update(ctx, actor, id, req.ToInput())
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, ToDetailResponse(detail))
}

func (h *Handler) deleteTransaction(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.deleteTransaction")
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

	if err := h.service.Delete(ctx, actor, id); err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, DeleteResponse{Deleted: true})
}
