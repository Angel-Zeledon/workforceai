package api

import (
	"cmp"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/customers"
)

// Customer district API (B12, docs/architecture/customer-district.md, 08-api.md). Reading follows requests:read;
// creating and editing customers follows requests:create (whoever can ask work for a customer can register it);
// clones, folder links and assigning earlier work change what an agent may read, so they follow files:grant (admins); archiving follows
// agents:write. Every write is audited by the service. Agents never reach these endpoints.

func (s *server) mountCustomers(r chi.Router) {
	if s.Customers == nil {
		return
	}
	read, write := s.can(auth.PermRequestsRead), s.can(auth.PermRequestsCreate)
	grant, archive := s.can(auth.PermFilesGrant), s.can(auth.PermAgentsWrite)
	r.With(read).Get("/customers", s.customersList)
	r.With(write).Post("/customers", s.customersCreate)
	// Import a list (projects for many customers): preview first, nothing is created until the import itself.
	r.With(write).Post("/customers/import/preview", s.customersImportPreview)
	r.With(write).Post("/customers/import", s.customersImport)
	r.With(read).Get("/customers/district", s.customersDistrict)
	r.With(read).Get("/customers/{id}", s.customersDetail)
	r.With(write).Put("/customers/{id}", s.customersUpdate)
	r.With(archive).Delete("/customers/{id}", s.customersArchive)
	r.With(grant).Post("/customers/{id}/assignments", s.customersAddClone)
	r.With(grant).Delete("/customers/{id}/assignments/{aid}", s.customersRemoveClone)
	r.With(grant).Post("/customers/{id}/folders", s.customersLinkFolder)
	r.With(grant).Delete("/customers/{id}/folders/{fid}", s.customersUnlinkFolder)
	// Earlier work without a customer (before B12): assigned explicitly by an admin, audited and reversible.
	r.With(grant).Get("/customers/history/candidates", s.customersHistoryCandidates)
	r.With(grant).Post("/customers/history", s.customersAssignHistory)
}

type importBody struct {
	Rows []customers.ImportRow `json:"rows"`
	// Choices per row index ("new", "skip" or a customer id), import only.
	Choices map[int]string `json:"choices"`
}

func (s *server) customersImportPreview(w http.ResponseWriter, r *http.Request) {
	var body importBody
	if err := s.decodeArtifact(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	res, err := s.Customers.PreviewImport(r.Context(), body.Rows)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": res})
}

func (s *server) customersImport(w http.ResponseWriter, r *http.Request) {
	var body importBody
	if err := s.decodeArtifact(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	res, err := s.Customers.ApplyImport(r.Context(), s.actor(r), body.Rows, body.Choices)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": res})
}

func (s *server) customersHistoryCandidates(w http.ResponseWriter, r *http.Request) {
	v, err := s.Customers.Candidates(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	if v.Projects, v.Requests, err = s.visibleCustomerWork(r, v.Projects, v.Requests); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

// customersAssignHistory: POST /customers/history {kind: project|request|memory, ref_id, customer_id} ("" undoes).
func (s *server) customersAssignHistory(w http.ResponseWriter, r *http.Request) {
	var in customers.HistoryInput
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	res, err := s.Customers.AssignHistory(r.Context(), s.actor(r), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, res)
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
	ps, rs, err := s.visibleCustomerWork(r, d.Projects, d.Requests)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(ps) != len(d.Projects) || len(rs) != len(d.Requests) { // the status counts only what the person sees
		d.Projects, d.Requests, d.Health = ps, rs, customers.HealthOf(ps, rs, time.Now())
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

// visibleCustomerWork leaves out the requests the person may not see and the projects that are only work they may not
// see (D3: the same rule as /requests and /projects).
func (s *server) visibleCustomerWork(r *http.Request, ps []customers.ProjectInfo, rs []customers.RequestInfo) ([]customers.ProjectInfo, []customers.RequestInfo, error) {
	ws := s.work(r)
	if err := ws.load(); err != nil || ws.open() {
		return ps, rs, err
	}
	if err := ws.indexAll(); err != nil {
		return nil, nil, err
	}
	rs = slices.DeleteFunc(rs, func(x customers.RequestInfo) bool { return !ws.request(x.ID) })
	if s.Projects == nil {
		return ps, rs, nil
	}
	var err error
	ps = slices.DeleteFunc(ps, func(p customers.ProjectInfo) bool {
		pv, ok, e := s.projectView(r, p.ID)
		err = cmp.Or(err, e)
		return e != nil || ok && pv.all
	})
	return ps, rs, err
}
