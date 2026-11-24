package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/offices"
	"aiworkforce/backend/internal/positions"
	"aiworkforce/backend/internal/roles"
)

// Hiring for an office (design 2026-11-23 §3; docs/architecture/08-api.md): POST /agents/from-template also accepts the
// office, the position, the skills and who may ask the new agent. Everything is checked first and applied as one
// operation: when a step fails the agent is removed again and nothing is left half done.

// maxHireSkills bounds the skills a hire assigns directly.
const maxHireSkills = 30

// hireBody is the body of POST /agents/from-template.
type hireBody struct {
	application.HireInput
	OfficeID   string          `json:"office_id"`
	PositionID string          `json:"position_id"`
	Skills     []string        `json:"skills"`
	Visibility string          `json:"visibility"`
	Shares     []offices.Share `json:"shares"`
}

func (b hireBody) plain() bool {
	return strings.TrimSpace(b.OfficeID) == "" && strings.TrimSpace(b.PositionID) == "" && len(b.Skills) == 0 && b.Visibility == "" && b.Shares == nil
}

func (s *server) mountHireSuggestions(r chi.Router) {
	r.With(s.can(auth.PermAgentsRead)).Get("/role-templates/{id}/suggested-skills", s.suggestedSkills)
}

// hireWithOffice validates and applies a hire with office. It writes the error answer itself and reports ok=false.
func (s *server) hireWithOffice(w http.ResponseWriter, r *http.Request, b hireBody) (domain.Agent, bool) {
	ctx, org := r.Context(), s.org(r)
	if s.Offices != nil {
		defer s.Offices.Invalidate(org) // the new agent (or its absence, if the hire was undone) is visible at once
	}
	if b.plain() {
		a, err := s.Orch.HireFromTemplate(ctx, b.HireInput)
		if err != nil {
			s.fail(w, err)
		}
		return a, err == nil
	}
	office := strings.TrimSpace(b.OfficeID)
	explicit := office != "" || b.Visibility != "" || b.Shares != nil
	if office == "" {
		office = offices.GeneralID
	}
	if explicit {
		if s.Offices == nil {
			if office != offices.GeneralID || b.Visibility != "" || b.Shares != nil {
				s.fail(w, fmt.Errorf("%w: offices are not available", domain.ErrInvalid))
				return domain.Agent{}, false
			}
		} else {
			o, err := s.Offices.CheckHire(ctx, org, s.viewer(r), office, offices.AccessInput{Visibility: b.Visibility, Shares: b.Shares})
			if err != nil {
				s.fail(w, err)
				return domain.Agent{}, false
			}
			office = o
		}
	}
	access := explicit && s.Offices != nil && (b.Visibility != "" || b.Shares != nil)

	var pos positions.Position
	positionID := strings.TrimSpace(b.PositionID)
	if positionID != "" {
		if s.Positions == nil || office == offices.GeneralID {
			s.fail(w, fmt.Errorf("%w: positions belong to an office other than General", domain.ErrInvalid))
			return domain.Agent{}, false
		}
		p, err := s.Positions.Check(ctx, s.positionActor(r), office, positionID)
		if err != nil {
			s.positionFail(w, err)
			return domain.Agent{}, false
		}
		pos = p
	}
	var direct []string
	if len(b.Skills) > 0 {
		if s.Skills == nil {
			s.fail(w, fmt.Errorf("%w: skills are not available", domain.ErrInvalid))
			return domain.Agent{}, false
		}
		if len(b.Skills) > maxHireSkills {
			s.fail(w, fmt.Errorf("%w: at most %d skills per hire", domain.ErrInvalid, maxHireSkills))
			return domain.Agent{}, false
		}
		seen := map[string]bool{}
		for _, id := range pos.Skills {
			seen[id] = true // the position gives it already
		}
		for _, id := range b.Skills {
			id = strings.TrimSpace(id)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			if err := s.Skills.CanAssignTo(ctx, s.skillActor(r), office, id); err != nil {
				s.skillFail(w, err)
				return domain.Agent{}, false
			}
			direct = append(direct, id)
		}
	}
	if office != offices.GeneralID && s.OrgChart == nil {
		s.fail(w, fmt.Errorf("%w: the organization chart is not available", domain.ErrInvalid))
		return domain.Agent{}, false
	}

	a, err := s.Orch.HireWithSetup(ctx, b.HireInput, func(ctx context.Context, ag domain.Agent) error {
		var undo []func()
		fail := func(err error) error {
			for i := len(undo) - 1; i >= 0; i-- {
				undo[i]()
			}
			return err
		}
		if office != offices.GeneralID {
			if err := s.OrgChart.AssignAgent(ctx, ag.ID, office); err != nil {
				return fail(err)
			}
			undo = append(undo, func() { _ = s.OrgChart.Store.DeleteJob(ctx, org, ag.ID) })
		}
		if s.Offices != nil {
			if _, err := s.Offices.Fresh(ctx, org); err != nil { // the model must know the new agent and its department
				return fail(err)
			}
		}
		if positionID != "" {
			if err := s.Positions.Assign(ctx, s.positionActor(r), ag.ID, positionID); err != nil {
				return fail(err)
			}
			undo = append(undo, func() { _ = s.Positions.Unassign(ctx, s.positionActor(r), ag.ID) })
		}
		for _, id := range direct {
			id := id
			if err := s.Skills.Assign(ctx, s.skillActor(r), ag.ID, id); err != nil {
				return fail(err)
			}
			undo = append(undo, func() { _ = s.Skills.Unassign(ctx, s.skillActor(r), ag.ID, id) })
		}
		if access {
			if _, err := s.Offices.PlaceNewAgent(ctx, org, s.viewer(r), ag.ID, office, offices.AccessInput{Visibility: b.Visibility, Shares: b.Shares}); err != nil {
				return fail(err)
			}
			undo = append(undo, func() { _ = s.Offices.ForgetAgent(ctx, org, ag.ID) })
		}
		return nil
	})
	if err != nil {
		var pe *positions.Error
		if errors.As(err, &pe) {
			s.positionFail(w, err)
		} else {
			s.fail(w, err)
		}
		return domain.Agent{}, false
	}
	return a, true
}

// suggestedSkills answers what a role template and, optionally, a position and an office suggest as skills for a
// new agent: GET /role-templates/{id}/suggested-skills?office_id=&position_id=
func (s *server) suggestedSkills(w http.ResponseWriter, r *http.Request) {
	t, ok := roles.Get(chi.URLParam(r, "id"))
	if !ok {
		s.fail(w, domain.ErrNotFound)
		return
	}
	office := strings.TrimSpace(r.URL.Query().Get("office_id"))
	if office == "" {
		office = offices.GeneralID
	}
	var pos *positions.Position
	var posSkills []string
	posName := ""
	if id := strings.TrimSpace(r.URL.Query().Get("position_id")); id != "" {
		if s.Positions == nil || office == offices.GeneralID {
			s.fail(w, fmt.Errorf("%w: positions belong to an office other than General", domain.ErrInvalid))
			return
		}
		p, err := s.Positions.Check(r.Context(), s.positionActor(r), office, id)
		if err != nil {
			s.positionFail(w, err)
			return
		}
		pos, posSkills, posName = &p, p.Skills, p.Name
	}
	out := []map[string]any{}
	if s.Skills != nil {
		sug, err := s.Skills.Suggest(r.Context(), s.skillActor(r), office, t.ID, posName, posSkills)
		if err != nil {
			s.skillFail(w, err)
			return
		}
		for _, sk := range sug {
			out = append(out, map[string]any{"id": sk.ID, "name": sk.Name, "description": sk.Description, "office_id": sk.OfficeID,
				"via": sk.Via, "position_name": sk.PositionName, "servable": sk.Servable})
		}
	}
	resp := map[string]any{"template_id": t.ID, "office_id": office, "skills": out}
	if pos != nil {
		resp["position"] = map[string]any{"id": pos.ID, "name": pos.Name}
	}
	writeJSON(w, http.StatusOK, resp)
}
