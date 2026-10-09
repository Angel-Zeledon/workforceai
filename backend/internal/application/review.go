package application

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Q1 quality review (docs/plans/large-workflows.md, "Q1 quality").
//
// After a task produced its output and before its tool requests are handled, a
// reviewer judges the output against the task's acceptance criteria:
//
//	pass   - the output is kept.
//	rework - the task runs again (at most Config.QualityMaxRework times) with
//	         the reviewer's notes as delimited data; the new output is reviewed again.
//	fail   - the task fails (a human can retry or skip it, W2).
//
// Review is opt-in: the ReviewPolicy (implemented by the projects layer) says
// per task whether it is reviewed ("off" by default, so existing flows and the
// $50,000 scenario never call the reviewer). Every review and rework call goes
// through budget reserve/record like any other runtime call. A reviewer that is
// unavailable never blocks work (fail-open) but never produces a verdict either.

// Review modes (project setting quality.review).
const (
	ReviewOff           = "off"
	ReviewLowConfidence = "low_confidence"
	ReviewAlways        = "always"
)

// AuditorRole is the role template of the internal auditor.
const AuditorRole = "internal_auditor"

// Defaults of the review knobs (QUALITY_MAX_REWORK, QUALITY_LOW_CONFIDENCE).
const (
	defaultQualityMaxRework     = 1
	maxQualityMaxRework         = 3
	defaultQualityLowConfidence = 0.6
	maxReviewReasons            = 6
	maxReviewText               = 400
)

// ReviewSpec is what the review needs to know about one task.
type ReviewSpec struct {
	Mode     string   // off|low_confidence|always ("" = off)
	Criteria []string // acceptance criteria; a task without any is never reviewed
}

// ReviewPolicy is the optional capability of the TaskGate (implemented by the
// projects layer) that says whether and against what a task is reviewed.
type ReviewPolicy interface {
	ReviewSpec(ctx context.Context, org string, t domain.Task) ReviewSpec
}

// ReworkInfo goes to the runtime when a task is run again after a review.
type ReworkInfo struct {
	Attempt         int      `json:"attempt"`          // 1 for the first rework
	Notes           []string `json:"notes"`            // reviewer reasons (untrusted data for the runtime)
	PreviousSummary string   `json:"previous_summary"` // summary of the output being redone
}

// ReviewRequest is POST /v1/review (optional runtime capability).
type ReviewRequest struct {
	Task       RunTaskInfo             `json:"task"`
	Acceptance []string                `json:"acceptance"`
	Output     domain.StructuredOutput `json:"output"`
	Reviewer   RunAgentInfo            `json:"reviewer"`
	// Round is 0 for the first review of a task and grows with each rework.
	Round  int    `json:"round"`
	Locale string `json:"locale,omitempty"`
	Tone   string `json:"tone,omitempty"`
}

// ReviewResponse is the reviewer's judgement.
type ReviewResponse struct {
	Verdict  string                   `json:"verdict"` // pass|rework|fail
	Reasons  []string                 `json:"reasons"`
	Criteria []domain.CriterionResult `json:"criteria,omitempty"`
	Evidence []string                 `json:"evidence,omitempty"`
	Usage    Usage                    `json:"usage"`
}

// Reviewer is the optional runtime capability behind POST /v1/review. A runtime
// without it simply never reviews (the task completes unreviewed, with an audit entry).
type Reviewer interface {
	Review(ctx context.Context, in ReviewRequest) (ReviewResponse, error)
}

func (c Config) maxRework() int {
	return min(max(c.QualityMaxRework, 0), maxQualityMaxRework)
}

func (c Config) lowConfidence() float64 {
	if c.QualityLowConfidence <= 0 || c.QualityLowConfidence > 1 {
		return defaultQualityLowConfidence
	}
	return c.QualityLowConfidence
}

// reviewSpecOf asks the installed policy (nil without projects).
func (o *Orchestrator) reviewSpecOf(ctx context.Context, t domain.Task) ReviewSpec {
	p := o.conn.reviewPolicy
	if p == nil {
		return ReviewSpec{}
	}
	sp := p.ReviewSpec(ctx, o.org(ctx), t)
	if sp.Mode != ReviewLowConfidence && sp.Mode != ReviewAlways {
		return ReviewSpec{}
	}
	return sp
}

// reviewerAgent is the internal auditor of the organization when it has one
// (first by id), else a neutral reviewer persona owned by the assistant.
func reviewerAgent(rs *run, locale string) (RunAgentInfo, string) {
	ids := make([]string, 0, len(rs.agents))
	for id, a := range rs.agents {
		if a.Role == AuditorRole {
			ids = append(ids, id)
		}
	}
	if len(ids) > 0 {
		slices.Sort(ids)
		a := rs.agents[ids[0]]
		return RunAgentInfo{ID: a.ID, Role: a.Role, Title: a.Title, Persona: a.Persona, Responsibilities: a.Responsibilities,
			Tools: []string{}, Area: areaOf(a.Role, locale)}, a.ID
	}
	persona := "Revisor neutral: compara el entregable con los criterios de aceptación, sin favorecer a nadie y sin inventar datos."
	title := "Revisor"
	if locale == "en" {
		persona, title = "Neutral reviewer: compares the deliverable with the acceptance criteria, without favoring anyone and without inventing data.", "Reviewer"
	}
	return RunAgentInfo{ID: "reviewer", Role: "reviewer", Title: title, Persona: persona, Responsibilities: []string{}, Tools: []string{}}, assistantID
}

func cleanReasons(in []string) []string {
	out := make([]string, 0, len(in))
	for _, r := range in {
		if r = strings.TrimSpace(r); r != "" && len(out) < maxReviewReasons {
			out = append(out, truncate(r, maxReviewText))
		}
	}
	return out
}

// qualityReview runs the review loop on a fresh response. It returns the response
// to continue with. stop=true means the task ended (failed or interrupted) and
// out is its outcome.
func (o *Orchestrator) qualityReview(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, in RunTaskRequest, resp RunTaskResponse, spec ReviewSpec) (_ RunTaskResponse, out Outcome, stop bool) {
	rv, ok := o.rt.(Reviewer)
	if !ok {
		o.rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "task.review_skipped", Entity: "task", EntityID: t.ID,
			Details: map[string]any{"reason": "runtime_without_review"}})
		return resp, OutcomeDone, false
	}
	reviewer, reviewerID := reviewerAgent(rs, rs.style.Locale)
	reworks := 0
	for round := 0; ; round++ {
		// low_confidence reviews the first output only when the agent itself was unsure;
		// whatever came out of a rework is always reviewed again.
		if round == 0 && spec.Mode == ReviewLowConfidence && resp.Output.Confidence >= o.cfg.lowConfidence() {
			return resp, OutcomeDone, false
		}
		if !o.admitTask(ctx, rs, *t) {
			return resp, OutcomeFailed, true
		}
		rr, ok := o.reviewOnce(ctx, rs, rv, *t, spec, resp.Output, reviewer, reviewerID, round)
		if !ok {
			if ctx.Err() != nil {
				return resp, OutcomeFailed, true
			}
			return resp, OutcomeDone, false // reviewer unavailable: fail-open, no verdict recorded
		}
		verdict := rr.Verdict
		exhausted := verdict == domain.VerdictRework && reworks >= o.cfg.maxRework()
		resp.Output.Review = &domain.Review{Verdict: verdict, Reasons: cleanReasons(rr.Reasons), Criteria: rr.Criteria, Evidence: cleanReasons(rr.Evidence),
			Reviewer: reviewerID, Reworks: reworks, Exhausted: exhausted, At: time.Now().UTC()}
		o.announceReview(ctx, *t, resp.Output.Review)
		switch {
		case verdict == domain.VerdictPass || exhausted:
			return resp, OutcomeDone, false
		case verdict == domain.VerdictFail:
			t.Output = &resp.Output
			o.failTask(ctx, rs, *t, fmt.Errorf("quality review failed: %s", strings.Join(resp.Output.Review.Reasons, "; ")))
			return resp, OutcomeFailed, true
		}
		// rework: run the task again with the reviewer's notes, once more per loop turn.
		next, ok, out := o.rework(ctx, rs, t, agent, in, resp, reworks+1)
		if !ok {
			return resp, out, out != OutcomeDone // out == OutcomeDone: keep the reviewed output, rework unavailable
		}
		reworks++
		prev := resp.Output.Review
		next.Output.Review = prev
		resp = next
	}
}

// reviewOnce is one reserved, recorded reviewer call. ok=false means no verdict.
func (o *Orchestrator) reviewOnce(ctx context.Context, rs *run, rv Reviewer, t domain.Task, spec ReviewSpec, out domain.StructuredOutput, reviewer RunAgentInfo, reviewerID string, round int) (ReviewResponse, bool) {
	tid := t.ID
	o.setState(ctx, t.AgentID, domain.StateWorking, "Revisión de calidad: "+t.Title, &tid, 70)
	res, err := o.reserveOrPause(ctx, rs, reviewerID, t.ID, domain.UsageReview)
	if err != nil {
		if ctx.Err() == nil {
			o.log.Warn("review: no budget", "task", t.ID, "err", err)
			o.rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "task.review_skipped", Entity: "task", EntityID: t.ID,
				Details: map[string]any{"reason": "budget", "error": truncate(err.Error(), 200)}})
		}
		return ReviewResponse{}, false
	}
	forReview := out
	forReview.Review = nil
	in := ReviewRequest{Task: RunTaskInfo{ID: t.ID, Title: t.Title, Description: t.Description, AgentID: t.AgentID, Acceptance: spec.Criteria},
		Acceptance: spec.Criteria, Output: forReview, Reviewer: reviewer, Round: round, Locale: rs.style.Locale, Tone: rs.style.ToneFor(reviewerID)}
	var rr ReviewResponse
	err = o.call(ctx, "review", func(c context.Context) (err error) {
		rr, err = rv.Review(c, in)
		return err
	})
	if err != nil {
		res.Release()
		if ctx.Err() == nil {
			o.rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "task.review_skipped", Entity: "task", EntityID: t.ID,
				Details: map[string]any{"reason": "reviewer_error", "error": truncate(err.Error(), 200)}})
		}
		return ReviewResponse{}, false
	}
	o.recordUsage(ctx, rs, t.ID, reviewerID, domain.UsageReview, rr.Usage, nil, res)
	rr.Verdict = strings.ToLower(strings.TrimSpace(rr.Verdict))
	switch rr.Verdict {
	case domain.VerdictPass, domain.VerdictRework, domain.VerdictFail:
	default: // an unknown verdict is no verdict at all
		o.rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "task.review_skipped", Entity: "task", EntityID: t.ID,
			Details: map[string]any{"reason": "invalid_verdict"}})
		return ReviewResponse{}, false
	}
	return rr, true
}

// rework runs the task again with the reviewer's notes. ok=false: the rework could not
// run; out==OutcomeDone then means "keep the reviewed output", anything else ends the task.
func (o *Orchestrator) rework(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, in RunTaskRequest, prev RunTaskResponse, attempt int) (RunTaskResponse, bool, Outcome) {
	if !o.admitTask(ctx, rs, *t) {
		return RunTaskResponse{}, false, OutcomeFailed
	}
	res, err := o.reserveOrPause(ctx, rs, t.AgentID, t.ID, domain.UsageRunTask)
	if err != nil {
		if ctx.Err() != nil {
			return RunTaskResponse{}, false, OutcomeFailed
		}
		o.failTask(ctx, rs, *t, err)
		return RunTaskResponse{}, false, OutcomeFailed
	}
	rin := in
	rin.Rework = &ReworkInfo{Attempt: attempt, Notes: prev.Output.Review.Reasons, PreviousSummary: truncate(prev.Output.Summary, maxReviewText)}
	tid := t.ID
	o.setState(ctx, t.AgentID, domain.StateWorking, fmt.Sprintf("Rehaciendo (%d): %s", attempt, t.Title), &tid, 50)
	var resp RunTaskResponse
	err = o.call(ctx, "run-task", func(c context.Context) (err error) {
		resp, err = o.rt.RunTask(c, rin)
		return err
	})
	if err != nil {
		res.Release()
		if ctx.Err() != nil {
			return RunTaskResponse{}, false, OutcomeFailed
		}
		o.rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "task.rework_failed", Entity: "task", EntityID: t.ID,
			Details: map[string]any{"attempt": attempt, "error": truncate(err.Error(), 200)}})
		return RunTaskResponse{}, false, OutcomeDone
	}
	o.recordUsage(ctx, rs, t.ID, t.AgentID, domain.UsageRunTask, resp.Usage, resp.ToolRequests, res)
	resp.Output.Normalize()
	resp = o.connectionReads(ctx, rs, t, agent, rin, resp)
	resp.Output.Normalize()
	if agent.Role == AuditorRole {
		enforceAuditEvidence(&resp.Output)
	}
	o.rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "task.reworked", Entity: "task", EntityID: t.ID,
		Details: map[string]any{"attempt": attempt, "agent_id": t.AgentID, "notes": len(rin.Rework.Notes)}})
	return resp, true, OutcomeDone
}

func (o *Orchestrator) announceReview(ctx context.Context, t domain.Task, r *domain.Review) {
	d := map[string]any{"verdict": r.Verdict, "reasons": r.Reasons, "reworks": r.Reworks, "rework_exhausted": r.Exhausted, "reviewer": r.Reviewer, "agent_id": t.AgentID}
	o.rec.Audit(ctx, domain.AuditLog{Actor: r.Reviewer, Action: "task.reviewed", Entity: "task", EntityID: t.ID, Details: d})
	o.rec.Emit(ctx, Action{Type: domain.EvTaskReviewed, AgentID: t.AgentID, Entity: "task", EntityID: t.ID, SkipAudit: true,
		Payload: map[string]any{"task_id": t.ID, "request_id": t.RequestID, "verdict": r.Verdict, "reasons": r.Reasons, "reworks": r.Reworks,
			"rework_exhausted": r.Exhausted, "reviewer": r.Reviewer},
		Text: fmt.Sprintf("Revisión (%s): %s", r.Verdict, t.Title)})
}

// ---- internal auditor: never "verified" without evidence ----

// Audit statuses carried in output.metrics["audit_status"] by the auditor's tasks.
const (
	AuditVerified      = "verified"
	AuditInconsistency = "inconsistency"
	AuditUnverified    = "unverified"
)

// minAuditEvidence: comparing figures needs at least two sources.
const minAuditEvidence = 2

// enforceAuditEvidence downgrades a "verified" audit that cites no evidence: the
// auditor may not claim a verification it cannot back with references.
func enforceAuditEvidence(out *domain.StructuredOutput) {
	st, _ := out.Metrics["audit_status"].(string)
	if !strings.EqualFold(st, AuditVerified) {
		return
	}
	refs := 0
	for _, e := range out.Evidence {
		if strings.TrimSpace(e) != "" {
			refs++
		}
	}
	if refs >= minAuditEvidence {
		return
	}
	out.Metrics["audit_status"] = AuditUnverified
	out.Findings = append(out.Findings, "NOT VERIFIED: the audit cites no evidence (which task outputs and fields were compared)")
	if out.Confidence > 0.4 {
		out.Confidence = 0.4
	}
}

// ---- quality section of the final report ----

// qualityTally counts the review outcomes of a request (one final verdict per task)
// and the inconsistencies found by internal-auditor tasks.
type qualityTally struct {
	Pass, Reworked, Exhausted, Fail, Reviews int // Reworked: passed after at least one rework; Exhausted: still "rework" when the bound was reached
	Audits, Inconsistencies, Unverified      int
	Details                                  []string // inconsistency findings, bounded
}

func (q qualityTally) empty() bool { return q.Reviews == 0 && q.Audits == 0 }

func tallyQuality(tasks []domain.Task) qualityTally {
	var q qualityTally
	for _, t := range tasks {
		if t.Output == nil {
			continue
		}
		if r := t.Output.Review; r != nil {
			q.Reviews++
			switch {
			case r.Verdict == domain.VerdictFail:
				q.Fail++
			case r.Verdict == domain.VerdictRework:
				q.Exhausted++
			case r.Reworks > 0:
				q.Reworked++
			default:
				q.Pass++
			}
		}
		if st, _ := t.Output.Metrics["audit_status"].(string); st != "" && t.Status == domain.TaskDone {
			q.Audits++
			switch strings.ToLower(st) {
			case AuditInconsistency:
				q.Inconsistencies++
				for _, f := range t.Output.Findings {
					if len(q.Details) < 5 {
						q.Details = append(q.Details, truncate(t.Title+": "+f, maxReviewText))
					}
				}
			case AuditUnverified:
				q.Unverified++
			}
		}
	}
	return q
}

// text is the short, deterministic summary used both as synthesis input and as report section.
func (q qualityTally) text(locale string) (title, summary string) {
	if locale == "en" {
		return "Quality review", fmt.Sprintf("Reviewed outputs: %d. Passed: %d, passed after rework: %d, still needing rework: %d, failed: %d. "+
			"Audits: %d (inconsistencies found: %d, not verified: %d).", q.Reviews, q.Pass, q.Reworked, q.Exhausted, q.Fail, q.Audits, q.Inconsistencies, q.Unverified)
	}
	return "Revisión de calidad", fmt.Sprintf("Entregables revisados: %d. Aprobados: %d, aprobados tras rehacer: %d, aún por rehacer: %d, fallidos: %d. "+
		"Auditorías: %d (inconsistencias encontradas: %d, sin verificar: %d).", q.Reviews, q.Pass, q.Reworked, q.Exhausted, q.Fail, q.Audits, q.Inconsistencies, q.Unverified)
}

// synthOutput is the quality section as one more synthesis input.
func (q qualityTally) synthOutput(locale string) SynthOutput {
	title, summary := q.text(locale)
	out := domain.StructuredOutput{Summary: summary, Findings: append([]string{}, q.Details...), Confidence: 1,
		Metrics: map[string]any{"reviewed": q.Reviews, "pass": q.Pass, "reworked": q.Reworked, "rework_exhausted": q.Exhausted, "fail": q.Fail,
			"inconsistencies": q.Inconsistencies, "unverified": q.Unverified}}
	out.Normalize()
	return SynthOutput{TaskID: "quality", AgentID: assistantID, Title: title, Output: out}
}

// section is the same tally appended to the report after synthesis, so it survives
// whatever the (possibly hierarchical) synthesis kept.
func (q qualityTally) section(locale string) domain.Section {
	title, summary := q.text(locale)
	body := summary
	if len(q.Details) > 0 {
		body += "\n- " + strings.Join(q.Details, "\n- ")
	}
	return domain.Section{Heading: title, Body: body}
}

// qualityOf reads the tasks of the request; the zero tally means nothing was reviewed or audited.
func (o *Orchestrator) qualityOf(ctx context.Context, rs *run) qualityTally {
	tasks, err := o.store.ListTasksByRequest(ctx, o.org(ctx), rs.req.ID)
	if err != nil {
		o.log.Warn("quality tally", "request", rs.req.ID, "err", err)
		return qualityTally{}
	}
	return tallyQuality(tasks)
}
