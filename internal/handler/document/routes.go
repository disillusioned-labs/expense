package document

import "github.com/go-chi/chi/v5"

// Project-scoped routes key on the project id, transaction-scoped ones on the
// transaction id, standalone ones on the document id.
func (h *Handler) ProtectedRoutes(r chi.Router) {
	// Standalone upload: OCR auto-creates the draft transaction from the
	// document.processed event.
	r.Route("/projects/{id}/documents", func(r chi.Router) {
		r.Post("/", h.uploadToProject)
		r.Get("/", h.listByProject)
	})
	// Attach path: a second nota bound to a draft that already exists; the
	// consumer appends its OCR items to that draft.
	r.Route("/transactions/{id}/documents", func(r chi.Router) {
		r.Post("/", h.attachToTransaction)
		r.Get("/", h.listByTransaction)
	})
	r.Get("/documents/{id}", h.getDocument)
	r.Delete("/documents/{id}", h.deleteDocument)
}
