package role

import "github.com/go-chi/chi/v5"

func (h *Handler) ProtectedRoutes(r chi.Router) {
	r.Route("/projects/{project_id}/roles", func(r chi.Router) {
		r.Get("/", h.listRoles)
		r.Post("/", h.createRole)
		r.Route("/{id}", func(r chi.Router) {
			r.Patch("/", h.updateRole)
			r.Delete("/", h.deleteRole)
		})
	})
}
