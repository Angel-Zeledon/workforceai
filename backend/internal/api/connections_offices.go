package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/offices"
)

// Connections of a department (D3, equipos-oficinas.md §4.2): a connection may belong to an office. A connection of
// General or of an open office is everybody's to see; one of a private office only its members' (admins included);
// one of an office that no longer exists only the admins'. What is hidden answers like what does not exist: left
// out of the listings, 404 in a detail, an edit or a grant. The connection's office is managed by an admin or by the
// office's responsible (lead), who needs no admin permission for the connections of the offices they lead. Granting
// write capabilities stays with connections:grant:write.

// connScope is the offices rule for the connections of one HTTP request (nil model: nothing hidden, no leads).
type connScope struct {
	m *offices.Model
	v offices.Viewer
}

func (s *server) connScope(r *http.Request) (connScope, error) {
	m, v, err := s.agentModel(r)
	return connScope{m: m, v: v}, err
}

// sees reports whether the person sees a connection.
func (c connScope) sees(conn connections.Connection) bool {
	if c.m == nil || conn.OfficeID == "" {
		return true
	}
	o, ok := c.m.Office(conn.OfficeID)
	if !ok {
		return c.v.Admin // its office is gone: the admins move it or revoke it
	}
	return o.Visibility != offices.Private || c.m.IsMember(c.v.UserID, conn.OfficeID)
}

// leads reports whether the person is a responsible of the office (never of General through this path).
func (c connScope) leads(office string) bool {
	return c.m != nil && office != "" && c.m.IsLead(c.v.UserID, office)
}

// manages reports whether the person may do what perm allows on a connection: holding perm, or leading its office.
func (s *server) manages(r *http.Request, sc connScope, conn connections.Connection, perm auth.Permission) bool {
	return s.has(r, perm) || sc.leads(conn.OfficeID)
}

// connRoute guards the routes of the connection {id}: a connection the person may not see answers 404 like a missing
// one; then perm, which the lead of the connection's office holds too for that connection.
func (s *server) connRoute(perm auth.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := s.Conns.Store.GetConnection(r.Context(), s.org(r), chi.URLParam(r, "id"))
			if err != nil {
				s.connErr(w, err)
				return
			}
			sc, err := s.connScope(r)
			if err != nil {
				s.connErr(w, err)
				return
			}
			if !sc.sees(c) {
				codeErr(w, http.StatusNotFound, "not_found", "not found")
				return
			}
			if !s.manages(r, sc, c, perm) {
				codeErr(w, http.StatusForbidden, "forbidden", "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// officeOK checks the office a connection is given (create or move): "" (General) needs connections:manage; an
// office must exist and the person must hold connections:manage or lead it. It writes the answer when it refuses.
func (s *server) officeOK(w http.ResponseWriter, r *http.Request, sc connScope, office string) bool {
	if office == "" {
		if !s.has(r, auth.PermConnectionsManage) {
			codeErr(w, http.StatusForbidden, "forbidden", "only an admin manages the company's connections")
			return false
		}
		return true
	}
	known := false
	if s.Offices != nil {
		m, err := s.Offices.Model(r.Context(), s.org(r))
		if err != nil {
			s.connErr(w, err)
			return false
		}
		o, ok := m.Office(office)
		known = ok && !o.General
	}
	if !known {
		codeErr(w, http.StatusBadRequest, "unknown_office", "office_id is not an office of the organization")
		return false
	}
	if !s.has(r, auth.PermConnectionsManage) && !sc.leads(office) {
		codeErr(w, http.StatusForbidden, "forbidden", "only an admin or the department's responsible manages its connections")
		return false
	}
	return true
}

// connOut decorates a connection for the person: whether they may manage it.
func (s *server) connOut(r *http.Request, sc connScope, c connections.Connection) connections.Connection {
	c.CanManage = s.manages(r, sc, c, auth.PermConnectionsManage)
	return c
}
