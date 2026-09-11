package transaction

import "github.com/go-chi/chi/v5"

// Drafts are born from the OCR consumer (upload → document.processed), so
// there is no manual create route; everything else is CRUD on what exists.
func (h *Handler) ProtectedRoutes(r chi.Router) {
	r.Route("/transactions", func(r chi.Router) {
		r.Get("/", h.listTransactions)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.getTransaction)
			r.Patch("/", h.updateTransaction)
			r.Delete("/", h.deleteTransaction)
		})
	})
}
