package approval

import (
	"net/http"
	"strconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	"github.com/disillusioned-labs/expense/internal/handler"
	approvalservice "github.com/disillusioned-labs/expense/internal/service/approval"
)

var agingTracer = otel.Tracer("handler/approval")

// AgingResponse is the wire shape of one pending transaction on the admin's
// bottleneck list.
type AgingResponse struct {
	TransactionID    string   `json:"transaction_id"`
	Description      *string  `json:"description"`
	TotalAmount      int64    `json:"total_amount"`
	Currency         string   `json:"currency"`
	ProjectID        string   `json:"project_id"`
	ProjectName      string   `json:"project_name"`
	SubmittedAt      string   `json:"submitted_at"`
	WaitingSince     string   `json:"waiting_since"`
	DaysWaiting      int      `json:"days_waiting"`
	PendingApprovers []string `json:"pending_approvers"`
	PendingCount     int64    `json:"pending_approver_count"`
}

func toAgingResponse(a approvalservice.AgingItem) AgingResponse {
	return AgingResponse{
		TransactionID:    a.TransactionID.String(),
		Description:      a.Description,
		TotalAmount:      a.TotalAmount,
		Currency:         a.Currency,
		ProjectID:        a.ProjectID.String(),
		ProjectName:      a.ProjectName,
		SubmittedAt:      a.SubmittedAt.Format("2006-01-02T15:04:05Z07:00"),
		WaitingSince:     a.WaitingSince.Format("2006-01-02T15:04:05Z07:00"),
		DaysWaiting:      a.DaysWaiting,
		PendingApprovers: a.PendingApprovers,
		PendingCount:     a.PendingCount,
	}
}

func toAgingResponses(items []approvalservice.AgingItem) []AgingResponse {
	out := make([]AgingResponse, 0, len(items))
	for _, a := range items {
		out = append(out, toAgingResponse(a))
	}
	return out
}

// listAgingApprovals serves the admin bottleneck view: pending transactions
// whose oldest active approval step has waited longer than ?days= (default
// 3). Visibility only - the response never changes authority.
func (h *Handler) listAgingApprovals(w http.ResponseWriter, r *http.Request) {
	ctx, span := agingTracer.Start(r.Context(), "Handler.listAgingApprovals")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := handler.ActorFrom(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	days := 3
	if v := r.URL.Query().Get("days"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 1 {
			handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "invalid days")
			return
		}
		days = parsed
	}

	page, ok := handler.DecodePage(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid page")
		return
	}

	items, err := h.service.Aging(ctx, actor, days, page.Limit, page.Offset)
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OKList(w, toAgingResponses(items), page.Meta())
}
