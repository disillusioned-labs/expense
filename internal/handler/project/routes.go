package project

import "github.com/go-chi/chi/v5"

func (h *Handler) ProtectedRoutes(r chi.Router) {
	r.Route("/projects", func(r chi.Router) {
		r.Get("/", h.listProjects)
		r.Post("/", h.createProject)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.getProject)
			r.Patch("/", h.updateProject)
			r.Delete("/", h.deleteProject)
			r.Post("/archive", h.archiveProject)
			r.Post("/unarchive", h.unarchiveProject)
			r.Get("/members", h.listProjectMembers)
			r.Put("/members/{user_id}", h.placeProjectMember)
			r.Delete("/members/{user_id}", h.removeProjectMember)
		})
	})
}
