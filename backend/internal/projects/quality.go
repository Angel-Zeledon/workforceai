package projects

import (
	"context"
	"fmt"
	"strings"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Q1 quality at scale (docs/plans/large-workflows.md, "Q1 quality").
//
// Per project: Record.Quality.Review is off|low_confidence|always. A project
// without the setting (every project created before Q1, template projects, flat
// plans) is "off": nothing is reviewed and no extra runtime call happens. A
// project planned hierarchically starts at "low_confidence" (the review only
// triggers when the agent itself was unsure and only for tasks that have
// acceptance criteria), the cheapest setting that still catches weak outputs on
// big plans. A human can change it in the draft (PATCH /plan op "set_quality").

// Quality is the quality-review setting of a project.
type Quality struct {
	Review string `json:"review"` // off|low_confidence|always
}

const (
	// MaxAcceptance and MaxAcceptanceLen bound the criteria of one node.
	MaxAcceptance    = 8
	MaxAcceptanceLen = 240
	// plannerAcceptance is how many criteria the planner proposes per task at most.
	plannerAcceptance = 5
)

func validReviewMode(m string) bool {
	return m == application.ReviewOff || m == application.ReviewLowConfidence || m == application.ReviewAlways
}

// reviewModeOf is the effective mode of a record ("off" when unset).
func reviewModeOf(q *Quality) string {
	if q == nil || !validReviewMode(q.Review) {
		return application.ReviewOff
	}
	return q.Review
}

// cleanAcceptance trims, drops empty and duplicate criteria and applies the caps.
func cleanAcceptance(in []string, max int) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, c := range in {
		c = truncRunes(strings.TrimSpace(c), MaxAcceptanceLen)
		if c == "" || seen[strings.ToLower(c)] || len(out) >= max {
			continue
		}
		seen[strings.ToLower(c)] = true
		out = append(out, c)
	}
	return out
}

// ReviewSpec implements application.ReviewPolicy: the orchestrator asks, per
// task, whether and against which criteria its output is reviewed.
func (s *Service) ReviewSpec(_ context.Context, org string, t domain.Task) application.ReviewSpec {
	lp := s.lookupReq(org, t.RequestID)
	if lp == nil {
		return application.ReviewSpec{}
	}
	lp.mu.Lock()
	defer lp.mu.Unlock()
	def, ok := lp.nodes[nodeIDOf(t)]
	if !ok || lp.review == application.ReviewOff || len(def.Acceptance) == 0 || def.human() {
		return application.ReviewSpec{}
	}
	return application.ReviewSpec{Mode: lp.review, Criteria: append([]string{}, def.Acceptance...)}
}

// setQuality applies the "set_quality" op to a draft.
func setQuality(r *Record, review *string) error {
	if review == nil || !validReviewMode(*review) {
		return fmt.Errorf("%w: quality.review must be off, low_confidence or always", domain.ErrInvalid)
	}
	r.Quality = &Quality{Review: *review}
	return nil
}

// ---- planner-inserted audit node ----

// auditorOf returns the internal auditor of the planner's agents ("" when the
// organization has none).
func auditorOf(pa []application.PlanAgent) string {
	best := ""
	for _, a := range pa {
		if a.Role == application.AuditorRole && (best == "" || a.ID < best) {
			best = a.ID
		}
	}
	return best
}

const auditKey = "audit"

// withAuditTask appends the phase audit: ONE task of the internal auditor that
// depends on every other task of the phase (so it sees their outputs) and
// cross-checks the figures between them. Because the exit tasks of a phase are
// the tasks nothing else needs, the audit becomes the only exit and the next
// phases wait for it. Bounded: exactly one per phase.
func withAuditTask(tasks []application.PhaseTask, ph application.PlanPhase, auditor, locale string) []application.PhaseTask {
	deps := make([]string, 0, len(tasks))
	for _, t := range tasks {
		deps = append(deps, t.Key)
	}
	title, desc := "Auditoría de la fase: "+ph.Title, "Revisa los entregables de la fase \""+ph.Title+"\" y cruza las cifras entre tareas (totales, cantidades y fechas que deben coincidir). "+
		"Responde \"verificado\" SOLO citando la evidencia (qué tarea y qué campo comparaste) o marca cada inconsistencia con detalle."
	if locale == "en" {
		title, desc = "Audit of phase: "+ph.Title, "Review the deliverables of the \""+ph.Title+"\" phase and cross-check the figures between tasks (totals, quantities and dates that must match). "+
			"Answer \"verified\" ONLY citing the evidence (which task and which field you compared) or flag each inconsistency in detail."
	}
	return append(tasks, application.PhaseTask{Key: auditKey, Title: title, Description: desc, AgentID: auditor, DependsOn: deps, Complexity: "M"})
}
