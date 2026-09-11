package approval

import "github.com/go-chi/chi/v5"

func (h *Handler) ProtectedRoutes(r chi.Router) {
	r.Post("/transactions/{id}/submit", h.submitTransaction)

	r.Route("/approval-rules", func(r chi.Router) {
		r.Get("/", h.listApprovalRules)
		r.Post("/", h.createApprovalRule)
		r.Patch("/{id}", h.updateApprovalRule)
		r.Delete("/{id}", h.deleteApprovalRule)
	})

	r.Get("/approvals/assigned", h.listAssignedApprovals)
	r.Get("/approvals/aging", h.listAgingApprovals)
	r.Post("/approvals/{id}/decide", h.decideApproval)
}
