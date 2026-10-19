package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/demodata"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/orgchart"
)

// Corporate organization model (docs/architecture/organization.md): departments, the org chart, agent job
// descriptions, delegation rules and the industry templates. Reads need agents:read; every change needs
// org:manage. Optional: no-op when Deps.OrgChart is nil.
func (s *server) mountOrgChart(r chi.Router) {
	if s.OrgChart == nil {
		return
	}
	read, write := s.can(auth.PermAgentsRead), s.can(auth.PermOrgManage)
	r.With(read).Get("/org/chart", s.getOrgChart)
	r.With(read).Get("/org/departments", s.listDepartments)
	r.With(write).Post("/org/departments", s.createDepartment)
	r.With(write).Put("/org/departments/{id}", s.updateDepartment)
	r.With(write).Delete("/org/departments/{id}", s.deleteDepartment)
	r.With(read).Get("/org/jobs", s.listJobs)
	r.With(read).Get("/agents/{id}/job", s.getJob)
	r.With(write).Put("/agents/{id}/job", s.putJob)
	r.With(read).Get("/agents/{id}/availability", s.getAvailability)
	r.With(write).Put("/org/people/{user_id}", s.putPlacement)
	r.With(read).Get("/org/delegation/rules", s.listRules)
	r.With(write).Post("/org/delegation/rules", s.createRule)
	r.With(write).Delete("/org/delegation/rules/{id}", s.deleteRule)
	r.With(read).Post("/org/delegation/check", s.checkDelegation)
	r.With(read).Get("/org/industries", s.listIndustries)
	r.With(write).Post("/org/industries/{key}/apply", s.applyIndustry)
}

func errBad(msg string) error { return fmt.Errorf("%w: %s", domain.ErrInvalid, msg) }

func (s *server) orgLocale(r *http.Request, explicit string) string {
	if explicit == "" {
		explicit = r.URL.Query().Get("locale")
	}
	if explicit != "" {
		return explicit
	}
	if s.OrgConfig != nil {
		return s.OrgConfig.Locale(r.Context(), "")
	}
	return "es"
}

func (s *server) getOrgChart(w http.ResponseWriter, r *http.Request) {
	c, err := s.OrgChart.Chart(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *server) listDepartments(w http.ResponseWriter, r *http.Request) {
	d, err := s.OrgChart.Departments(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": d})
}

func (s *server) createDepartment(w http.ResponseWriter, r *http.Request) {
	var in orgchart.DepartmentInput
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	d, err := s.OrgChart.CreateDepartment(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *server) updateDepartment(w http.ResponseWriter, r *http.Request) {
	var in orgchart.DepartmentInput
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	d, err := s.OrgChart.UpdateDepartment(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) deleteDepartment(w http.ResponseWriter, r *http.Request) {
	if err := s.OrgChart.DeleteDepartment(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listJobs(w http.ResponseWriter, r *http.Request) {
	j, err := s.OrgChart.Jobs(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": j})
}

func (s *server) getJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.OrgChart.Job(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *server) putJob(w http.ResponseWriter, r *http.Request) {
	var in orgchart.JobInput
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	j, err := s.OrgChart.PutJob(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *server) getAvailability(w http.ResponseWriter, r *http.Request) {
	at := time.Now()
	if v := r.URL.Query().Get("at"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			s.fail(w, errBad("at must be an RFC 3339 instant"))
			return
		}
		at = t
	}
	a, err := s.OrgChart.Availability(r.Context(), chi.URLParam(r, "id"), at)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *server) putPlacement(w http.ResponseWriter, r *http.Request) {
	var in orgchart.PlacementInput
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	p, err := s.OrgChart.PutPlacement(r.Context(), chi.URLParam(r, "user_id"), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *server) listRules(w http.ResponseWriter, r *http.Request) {
	rs, err := s.OrgChart.Rules(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rs})
}

func (s *server) createRule(w http.ResponseWriter, r *http.Request) {
	var in orgchart.RuleInput
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	rule, err := s.OrgChart.CreateRule(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (s *server) deleteRule(w http.ResponseWriter, r *http.Request) {
	if err := s.OrgChart.DeleteRule(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) checkDelegation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		From orgchart.Ref `json:"from"`
		To   orgchart.Ref `json:"to"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	d, err := s.OrgChart.CheckDelegation(r.Context(), in.From, in.To)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) listIndustries(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.OrgChart.Industries(s.orgLocale(r, ""))})
}

func (s *server) applyIndustry(w http.ResponseWriter, r *http.Request) {
	var in struct {
		orgchart.ApplyInput
		DemoData bool `json:"demo_data"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	in.Locale = s.orgLocale(r, in.Locale)
	key := chi.URLParam(r, "key")
	res, err := s.OrgChart.ApplyIndustry(r.Context(), key, in.ApplyInput)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := map[string]any{"result": res, "demo_files": []string{}}
	if in.DemoData {
		files, derr := s.writeIndustryDemo(r, res.DemoSet, res.Locale)
		out["demo_files"] = files
		if derr != nil {
			out["demo_error"] = derr.Error() // the organization is set up; only the sample files failed
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// writeIndustryDemo writes the sample dataset of an industry into /Shared/demo-<set> of the drive.
func (s *server) writeIndustryDemo(r *http.Request, set, locale string) ([]string, error) {
	if s.Drive == nil {
		return nil, domain.ErrInvalid
	}
	data, err := demodata.GenerateIndustry(set, demodata.Params{Locale: locale})
	if err != nil {
		return nil, err
	}
	folder := "/Shared/demo-" + strings.ReplaceAll(set, "_", "-")
	_, _ = s.Drive.Mkdir(r.Context(), s.drivePrincipal(r), folder) // already there on a second run
	var paths []string
	for _, f := range data.Files {
		res, err := s.Drive.Write(r.Context(), s.drivePrincipal(r), drive.WriteInput{Path: folder + "/" + f.Name, Data: f.Data, Source: "demo_data", Message: "sample data " + set})
		if err != nil {
			return paths, err
		}
		paths = append(paths, res.File.Path)
	}
	return paths, nil
}
