package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/customers"
)

// Customer district API (B12, docs/architecture/customer-district.md, 08-api.md). Reading follows requests:read;
// creating and editing customers follows requests:create (whoever can ask work for a customer can register it);
// clones and folder links change what an agent may read, so they follow files:grant (admins); archiving follows
// agents:write. Every write is audited by the service. Agents never reach these endpoints.

func (s *server) mountCustomers(r chi.Router) {
	if s.Customers == nil {
		return
	}
	read, write := s.can(auth.PermRequestsRead), s.can(auth.PermRequestsCreate)
	grant, archive := s.can(auth.PermFilesGrant), s.can(auth.PermAgentsWrite)
	r.With(read).Get("/customers", s.customersList)
	r.With(write).Post("/customers", s.customersCreate)
	r.With(read).Get("/customers/district", s.customersDistrict)
	r.With(read).Get("/customers/{id}", s.customersDetail)
	r.With(write).Put("/customers/{id}", s.customersUpdate)
	r.With(archive).Delete("/customers/{id}", s.customersArchive)
	r.With(grant).Post("/customers/{id}/assignments", s.customersAddClone)
	r.With(grant).Delete("/customers/{id}/assignments/{aid}", s.customersRemoveClone)
	r.With(grant).Post("/customers/{id}/folders", s.customersLinkFolder)
	r.With(grant).Delete("/customers/{id}/folders/{fid}", s.customersUnlinkFolder)
}

func (s *server) customersList(w http.ResponseWriter, r *http.Request) {
	v, err := s.Customers.List(r.Context(), r.URL.Query().Get("include_archived") == "1" || r.URL.Query().Get("include_archived") == "true")
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list(v))
}

func (s *server) customersCreate(w http.ResponseWriter, r *http.Request) {
	var in customers.Input
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	c, err := s.Customers.Create(r.Context(), s.actor(r), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 201, c)
}

func (s *server) customersDistrict(w http.ResponseWriter, r *http.Request) {
	d, err := s.Customers.District(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, d)
}

func (s *server) customersDetail(w http.ResponseWriter, r *http.Request) {
	d, err := s.Customers.Detail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	d.Assignments, d.Requests = list(d.Assignments), list(d.Requests)
	writeJSON(w, 200, d)
}

func (s *server) customersUpdate(w http.ResponseWriter, r *http.Request) {
	var in customers.Input
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	c, err := s.Customers.Update(r.Context(), s.actor(r), chi.URLParam(r, "id"), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *server) customersArchive(w http.ResponseWriter, r *http.Request) {
	if err := s.Customers.Archive(r.Context(), s.actor(r), chi.URLParam(r, "id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) customersAddClone(w http.ResponseWriter, r *http.Request) {
	var in customers.AssignmentInput
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	a, err := s.Customers.AddAssignment(r.Context(), s.actor(r), chi.URLParam(r, "id"), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 201, a)
}

func (s *server) customersRemoveClone(w http.ResponseWriter, r *http.Request) {
	if err := s.Customers.RemoveAssignment(r.Context(), s.actor(r), chi.URLParam(r, "id"), chi.URLParam(r, "aid")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) customersLinkFolder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FolderID string `json:"folder_id"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	l, err := s.Customers.LinkFolder(r.Context(), s.actor(r), chi.URLParam(r, "id"), in.FolderID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 201, l)
}

func (s *server) customersUnlinkFolder(w http.ResponseWriter, r *http.Request) {
	if err := s.Customers.UnlinkFolder(r.Context(), s.actor(r), chi.URLParam(r, "id"), chi.URLParam(r, "fid")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
