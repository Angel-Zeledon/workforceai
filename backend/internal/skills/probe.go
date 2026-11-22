package skills

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/sanitize"
)

// Test a skill with a case before activating it (F8, docs/architecture/skills.md "Probar una habilidad").
//
// A person saves test cases against a skill (a request in plain words and, optionally, what they expect to see) and
// runs one with an agent that may use the skill and ONE version of the skill loaded: a draft or one in review as
// well as the active one. The run has NO side effects by construction, not by prompt:
//
//   - it never goes through the Tool Gateway, the drive, the connections or the approvals: the Tester only holds the
//     runtime's RunTask, the budget and the library;
//   - the agent is told it has no tools (Tools: []) and the only request the Tester ever serves is skill.load of the
//     skill under test, answered from the version under test;
//   - every other request for a tool, whatever it is, is dropped and recorded as "tool.action" (never the arguments).
//
// A run counts against the budget like any other call (agent cap and organization limit), the estimate is shown
// before and the real cost after, and an agent can never start one (the actor must be a person).

// Limits of the test cases and runs.
const (
	MaxTestCases   = 10   // per skill
	MaxCaseTitle   = 120  // characters
	MaxCasePrompt  = 2000 // the request in plain words
	MaxCaseExpect  = 1000 // what the person expects to see
	MaxRunOutput   = 12000
	MaxTestRounds  = 2   // calls to the runtime per run: the first one and the one after skill.load
	testRunsListed = 30  // runs returned by Overview
	testRunsScan   = 500 // runs read to compute "last tested" per version
)

// Statuses of a run.
const (
	RunDone   = "done"
	RunFailed = "failed"
)

// TestCase is a saved case of a skill (it belongs to the skill, not to a version).
type TestCase struct {
	ID        string    `json:"id"`
	SkillID   string    `json:"skill_id"`
	Title     string    `json:"title"`
	Prompt    string    `json:"prompt"`
	Expect    string    `json:"expect"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TestRun is one run of a case: which version, which agent, what it answered, whether it used the skill, how long
// and how much.
type TestRun struct {
	ID        string `json:"id"`
	SkillID   string `json:"skill_id"`
	CaseID    string `json:"case_id"`
	CaseTitle string `json:"case_title"`
	// PairID joins the two runs of a side-by-side comparison (the tested version and the active one).
	PairID  string `json:"pair_id,omitempty"`
	Version int    `json:"version"`
	// Active: the version was the active one (what agents use today) when it ran.
	Active   bool   `json:"active"`
	AgentID  string `json:"agent_id"`
	Status   string `json:"status"`
	FailCode string `json:"fail_code,omitempty"`
	Output   string `json:"output"`
	// UsedSkill: the agent loaded the full text of the skill (skill.load without a file).
	UsedSkill bool `json:"used_skill"`
	// Blocked lists the tools the agent asked for that were not run ("tool.action"). A test has no effects.
	Blocked      []string  `json:"blocked"`
	EstimateUSD  float64   `json:"estimate_usd"`
	CostUSD      float64   `json:"cost_usd"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	DurationMS   int       `json:"duration_ms"`
	Model        string    `json:"model,omitempty"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
}

// CaseInput is what a person writes for a case.
type CaseInput struct {
	Title  string `json:"title"`
	Prompt string `json:"prompt"`
	Expect string `json:"expect"`
}

// RunInput asks for a test run. Version 0 = the latest version (what a reviewer is looking at).
type RunInput struct {
	CaseID  string `json:"case_id"`
	AgentID string `json:"agent_id"`
	Version int    `json:"version"`
	// Compare also runs the active version for the same case (when there is one and it is another version).
	Compare bool `json:"compare"`
}

// TestEstimate is the cost shown before a run: the worst case of the calls that would be made.
type TestEstimate struct {
	Runs        int       `json:"runs"`
	Versions    []int     `json:"versions"`
	EstimateUSD float64   `json:"estimate_usd"`
	PerRunUSD   []float64 `json:"per_run_usd"`
}

// Tested is the last time a version was tested (for the review screen: "Probada el ...").
type Tested struct {
	Version   int       `json:"version"`
	At        time.Time `json:"at"`
	Status    string    `json:"status"`
	UsedSkill bool      `json:"used_skill"`
	CaseID    string    `json:"case_id"`
	RunID     string    `json:"run_id"`
}

// TestOverview is everything the review screen needs about the tests of a skill.
type TestOverview struct {
	Cases []TestCase `json:"cases"`
	Runs  []TestRun  `json:"runs"`
	// LastByVersion is the most recent finished run of each version, newest version first.
	LastByVersion []Tested `json:"last_by_version"`
	Limits        struct {
		Cases  int `json:"cases"`
		Title  int `json:"title"`
		Prompt int `json:"prompt"`
		Expect int `json:"expect"`
	} `json:"limits"`
}

// TestStore persists cases and runs. Every call is scoped by organization (RLS in Postgres).
type TestStore interface {
	ListCases(ctx context.Context, org, skillID string) ([]TestCase, error)
	GetCase(ctx context.Context, org, skillID, id string) (TestCase, error)
	// PutCase inserts or replaces the case (same id).
	PutCase(ctx context.Context, org string, c TestCase) error
	DeleteCase(ctx context.Context, org, skillID, id string) error
	AddRun(ctx context.Context, org string, r TestRun) error
	// ListRuns returns newest first; caseID "" = every case of the skill.
	ListRuns(ctx context.Context, org, skillID, caseID string, limit int) ([]TestRun, error)
}

// TestRuntime is the only door to the model a test has: one agent call. No gateway, no tools.
type TestRuntime interface {
	RunTask(ctx context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error)
}

// TestBudget is what a test needs from the orchestrator: the execution guard (kill switch, agent pause), the
// estimate and the reservation/ledger of an agent call outside any request.
type TestBudget interface {
	AdmitReview(ctx context.Context, agentID string) application.GuardVerdict
	EstimateAgentCall(payload any) float64
	ReserveAgentCall(ctx context.Context, agentID string, payload any) (*application.Reservation, *application.Exceeded)
	RecordAgentCall(ctx context.Context, agentID string, u application.Usage, res *application.Reservation)
	NoteProviderNoBalance(ctx context.Context, agentID string, err error) bool
}

// TesterConfig wires the tester.
type TesterConfig struct {
	Svc     *Service
	Store   TestStore
	Runtime TestRuntime
	Budget  TestBudget
	// Agent and Agents read the organization's agents.
	Agent  func(ctx context.Context, org, id string) (domain.Agent, error)
	Agents func(ctx context.Context, org string) ([]domain.Agent, error)
	Locale func(ctx context.Context) string
	Now    func() time.Time
}

// Tester runs the test cases of the skills.
type Tester struct{ cfg TesterConfig }

// NewTester builds the tester.
func NewTester(cfg TesterConfig) *Tester { return &Tester{cfg: cfg} }

func (t *Tester) now() time.Time {
	if t.cfg.Now != nil {
		return t.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

func newTestID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}


func (t *Tester) orgOf(ctx context.Context) string { return t.cfg.Svc.org(ctx) }

// ---- cases ----

func cleanCase(in CaseInput) (CaseInput, error) {
	in.Title, in.Prompt, in.Expect = strings.TrimSpace(in.Title), strings.TrimSpace(in.Prompt), strings.TrimSpace(in.Expect)
	switch {
	case in.Prompt == "":
		return in, invalid("case_prompt_required", "write the request the agent should handle")
	case utf8.RuneCountInString(in.Prompt) > MaxCasePrompt:
		return in, invalid("case_prompt_too_long", "the request is longer than %d characters", MaxCasePrompt)
	case utf8.RuneCountInString(in.Expect) > MaxCaseExpect:
		return in, invalid("case_expect_too_long", "what you expect is longer than %d characters", MaxCaseExpect)
	case utf8.RuneCountInString(in.Title) > MaxCaseTitle:
		return in, invalid("case_title_too_long", "the title is longer than %d characters", MaxCaseTitle)
	}
	if in.Title == "" {
		in.Title = clipText(strings.Join(strings.Fields(in.Prompt), " "), 60)
	}
	return in, nil
}

// Cases lists the saved cases of a skill the actor can see.
func (t *Tester) Cases(ctx context.Context, a Actor, skillID string) ([]TestCase, error) {
	org := t.orgOf(ctx)
	if _, err := t.cfg.Svc.visible(ctx, org, a, skillID); err != nil {
		return nil, err
	}
	l, err := t.cfg.Store.ListCases(ctx, org, skillID)
	if l == nil {
		l = []TestCase{}
	}
	return l, err
}

// SaveCase creates a case (id "") or replaces the text of one. Whoever may change the skill may save cases.
func (t *Tester) SaveCase(ctx context.Context, a Actor, skillID, id string, in CaseInput) (TestCase, error) {
	org := t.orgOf(ctx)
	if a.System {
		return TestCase{}, forbidden("humans_only", "only people save test cases")
	}
	sk, err := t.cfg.Svc.writable(ctx, org, a, skillID)
	if err != nil {
		return TestCase{}, err
	}
	in, err = cleanCase(in)
	if err != nil {
		return TestCase{}, err
	}
	in.Prompt, in.Expect = sanitize.RedactSecrets(in.Prompt, nil), sanitize.RedactSecrets(in.Expect, nil)
	now := t.now()
	var c TestCase
	if id == "" {
		l, err := t.cfg.Store.ListCases(ctx, org, skillID)
		if err != nil {
			return TestCase{}, err
		}
		if len(l) >= MaxTestCases {
			return TestCase{}, conflict("too_many_cases", "a skill can have at most %d test cases; delete one first", MaxTestCases)
		}
		c = TestCase{ID: newTestID("stc_"), SkillID: sk.ID, CreatedBy: a.ID, CreatedAt: now}
	} else {
		if c, err = t.cfg.Store.GetCase(ctx, org, skillID, id); err != nil {
			return TestCase{}, mapStore(err, "case")
		}
	}
	c.Title, c.Prompt, c.Expect, c.UpdatedAt = in.Title, in.Prompt, in.Expect, now
	if err := t.cfg.Store.PutCase(ctx, org, c); err != nil {
		return TestCase{}, err
	}
	act := "skill.test_case_saved"
	t.cfg.Svc.audit(ctx, org, a, act, sk.ID, map[string]any{"name": sk.Name, "case_id": c.ID})
	return c, nil
}

// DeleteCase removes a case (its runs stay in the history, with the title they had).
func (t *Tester) DeleteCase(ctx context.Context, a Actor, skillID, id string) error {
	org := t.orgOf(ctx)
	if a.System {
		return forbidden("humans_only", "only people delete test cases")
	}
	sk, err := t.cfg.Svc.writable(ctx, org, a, skillID)
	if err != nil {
		return err
	}
	if err := t.cfg.Store.DeleteCase(ctx, org, skillID, id); err != nil {
		return mapStore(err, "case")
	}
	t.cfg.Svc.audit(ctx, org, a, "skill.test_case_deleted", sk.ID, map[string]any{"name": sk.Name, "case_id": id})
	return nil
}

// ---- candidates ----

// Candidates are the agents that can use the skill (assigned to them or a default of their role), whatever the
// state of the skill: a draft can be tried before it is activated.
func (t *Tester) Candidates(ctx context.Context, a Actor, skillID string) ([]domain.Agent, error) {
	org := t.orgOf(ctx)
	if _, err := t.cfg.Svc.visible(ctx, org, a, skillID); err != nil {
		return nil, err
	}
	if t.cfg.Agents == nil {
		return []domain.Agent{}, nil
	}
	all, err := t.cfg.Agents(ctx, org)
	if err != nil {
		return nil, err
	}
	out := []domain.Agent{}
	for _, ag := range all {
		if t.eligible(ctx, ag.ID, skillID) {
			out = append(out, ag)
		}
	}
	return out, nil
}

func (t *Tester) eligible(ctx context.Context, agentID, skillID string) bool {
	l, err := t.cfg.Svc.AgentSkills(ctx, agentID)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(l, func(s AgentSkill) bool { return s.ID == skillID })
}

// ---- preparing a run ----

type target struct {
	version int
	active  bool
	pkg     Package
	desc    string
}

type prepared struct {
	org     string
	sk      Skill
	c       TestCase
	agent   domain.Agent
	targets []target
}

func (t *Tester) prepare(ctx context.Context, a Actor, skillID string, in RunInput) (prepared, error) {
	org := t.orgOf(ctx)
	ctx = application.WithOrg(ctx, org)
	if a.System || a.ID == "" {
		return prepared{}, forbidden("humans_only", "only a person can start a test; an agent cannot")
	}
	sk, err := t.cfg.Svc.visible(ctx, org, a, skillID)
	if err != nil {
		return prepared{}, err
	}
	c, err := t.cfg.Store.GetCase(ctx, org, skillID, in.CaseID)
	if err != nil {
		return prepared{}, mapStore(err, "case")
	}
	if t.cfg.Agent == nil || t.cfg.Runtime == nil {
		return prepared{}, &Error{Code: "tests_unavailable", Kind: domain.ErrConflict, Msg: "tests are not available in this installation"}
	}
	ag, err := t.cfg.Agent(ctx, org, in.AgentID)
	if err != nil {
		return prepared{}, notFound("agent not found")
	}
	if !t.eligible(ctx, ag.ID, sk.ID) {
		return prepared{}, invalid("agent_not_eligible", "that agent cannot use this skill: assign it first")
	}
	ver := in.Version
	if ver == 0 {
		ver = sk.CurrentVersion
	}
	p := prepared{org: org, sk: sk, c: c, agent: ag}
	versions := []int{ver}
	if in.Compare && sk.ActiveVersion > 0 && sk.ActiveVersion != ver {
		versions = append(versions, sk.ActiveVersion)
	}
	for _, n := range versions {
		v, err := t.cfg.Svc.cfg.Store.VersionOf(ctx, org, sk.ID, n)
		if err != nil {
			return prepared{}, mapStore(err, "version")
		}
		pkg, err := t.cfg.Svc.readPackage(ctx, org, sk, v) // verifies the hashes: a tampered version is not tested
		if err != nil {
			return prepared{}, err
		}
		p.targets = append(p.targets, target{version: n, active: n == sk.ActiveVersion, pkg: pkg, desc: oneLine(v.Description, MaxRunDescription)})
	}
	return p, nil
}

// request builds the call to the runtime. Tools is empty BY CONSTRUCTION: the agent is told it has none.
func (t *Tester) request(ctx context.Context, p prepared, tg target, loaded []application.TrustedBlock) application.RunTaskRequest {
	locale := "es"
	if t.cfg.Locale != nil {
		if l := t.cfg.Locale(ctx); l != "" {
			locale = l
		}
	}
	prompt := sanitize.RedactSecrets(p.c.Prompt, nil)
	return application.RunTaskRequest{
		Task: application.RunTaskInfo{ID: "skilltest", Title: "Prueba de habilidad: " + p.sk.Name, Description: prompt, AgentID: p.agent.ID},
		Agent: application.RunAgentInfo{ID: p.agent.ID, Role: p.agent.Role, Title: p.agent.Title, Persona: p.agent.Persona,
			Responsibilities: p.agent.Responsibilities, Tools: []string{}},
		Context:      application.RunContext{RequestText: prompt, DependencyOutputs: []application.DependencyOutput{}, Memory: []application.MemoryEntry{}},
		Locale:       locale,
		Skills:       []application.SkillRef{{Name: p.sk.Name, Description: tg.desc}},
		LoadedSkills: loaded,
	}
}

// Estimate is the worst-case cost of a run, shown before it starts: the first call and the one after skill.load, for
// each version that would run.
func (t *Tester) Estimate(ctx context.Context, a Actor, skillID string, in RunInput) (TestEstimate, error) {
	p, err := t.prepare(ctx, a, skillID, in)
	if err != nil {
		return TestEstimate{}, err
	}
	out := TestEstimate{Runs: len(p.targets), Versions: []int{}, PerRunUSD: []float64{}}
	if t.cfg.Budget == nil {
		for _, tg := range p.targets {
			out.Versions, out.PerRunUSD = append(out.Versions, tg.version), append(out.PerRunUSD, 0)
		}
		return out, nil
	}
	for _, tg := range p.targets {
		one := t.estimateRun(ctx, p, tg)
		out.Versions, out.PerRunUSD = append(out.Versions, tg.version), append(out.PerRunUSD, one)
		out.EstimateUSD += one
	}
	return out, nil
}

func (t *Tester) estimateRun(ctx context.Context, p prepared, tg target) float64 {
	first := t.cfg.Budget.EstimateAgentCall(t.request(ctx, p, tg, nil))
	ld, err := t.cfg.Svc.render(ctx, p.org, p.sk, tg.version, tg.pkg, "")
	if err != nil || MaxTestRounds < 2 {
		return first
	}
	second := t.cfg.Budget.EstimateAgentCall(t.request(ctx, p, tg, []application.TrustedBlock{loadedBlock(ld)}))
	return first + second
}

func loadedBlock(l Loaded) application.TrustedBlock {
	return application.TrustedBlock{Kind: "skill", Name: l.Name, Version: l.Version, File: l.File, Text: l.Text, Files: l.Files}
}

// ---- running ----

// Run executes the case with the agent and the version asked for (and the active one when Compare). The returned
// runs are stored; a run that could not happen (budget, a stopped agent, the runtime) is a stored run with
// status "failed" and a stable fail_code.
func (t *Tester) Run(ctx context.Context, a Actor, skillID string, in RunInput) ([]TestRun, error) {
	p, err := t.prepare(ctx, a, skillID, in)
	if err != nil {
		return nil, err
	}
	pair := ""
	if len(p.targets) > 1 {
		pair = newTestID("stp_")
	}
	runs := []TestRun{}
	for _, tg := range p.targets {
		r := t.runOne(ctx, a, p, tg, pair)
		if err := t.cfg.Store.AddRun(ctx, p.org, r); err != nil {
			return runs, err
		}
		t.cfg.Svc.audit(ctx, p.org, a, "skill.test_run", p.sk.ID, map[string]any{"name": p.sk.Name, "version": r.Version, "case_id": r.CaseID,
			"agent_id": r.AgentID, "status": r.Status, "fail_code": r.FailCode, "used_skill": r.UsedSkill, "blocked": len(r.Blocked), "cost_usd": r.CostUSD})
		runs = append(runs, r)
	}
	return runs, nil
}

func isLoadOf(tr application.ToolRequest, name string) bool {
	if tr.Tool != ToolName || tr.Action != ActLoad {
		return false
	}
	n, _ := tr.Args["name"].(string)
	return strings.TrimSpace(n) == name
}

func (t *Tester) runOne(ctx context.Context, a Actor, p prepared, tg target, pair string) TestRun {
	r := TestRun{ID: newTestID("str_"), SkillID: p.sk.ID, CaseID: p.c.ID, CaseTitle: p.c.Title, PairID: pair, Version: tg.version,
		Active: tg.active, AgentID: p.agent.ID, Blocked: []string{}, CreatedBy: a.ID, CreatedAt: t.now()}
	fail := func(code string) TestRun { r.Status, r.FailCode = RunFailed, code; return r }
	b := t.cfg.Budget
	if b != nil {
		r.EstimateUSD = t.estimateRun(ctx, p, tg)
	}
	start := time.Now()
	var loaded []application.TrustedBlock
	var last application.RunTaskResponse
	for round := 0; round < MaxTestRounds; round++ {
		req := t.request(ctx, p, tg, loaded)
		var res *application.Reservation
		if b != nil {
			if v := b.AdmitReview(ctx, p.agent.ID); !v.Allowed {
				return fail("blocked:" + v.Code)
			}
			var ex *application.Exceeded
			if res, ex = b.ReserveAgentCall(ctx, p.agent.ID, req); ex != nil {
				return fail("budget_exceeded:" + string(ex.Scope))
			}
			req.MaxCostUSD = res.Allowance()
		}
		resp, err := t.cfg.Runtime.RunTask(ctx, req)
		if b != nil {
			b.NoteProviderNoBalance(ctx, p.agent.ID, err)
			b.RecordAgentCall(ctx, p.agent.ID, resp.Usage, res) // also releases the reservation when the call failed
		}
		r.CostUSD += resp.Usage.CostUSD
		r.InputTokens += resp.Usage.InputTokens
		r.OutputTokens += resp.Usage.OutputTokens
		if resp.Usage.Model != "" {
			r.Model = resp.Usage.Model
		}
		if err != nil {
			r.DurationMS = int(time.Since(start).Milliseconds())
			if errors.Is(err, application.ErrProviderNoBalance) {
				return fail("provider_no_balance")
			}
			return fail("runtime_unavailable")
		}
		last = resp
		// What the agent asked for. Only skill.load of THIS skill is served, from the version under test; the rest is
		// dropped and recorded by name. The last round serves nothing: the run ends with the answer it has.
		var pending []application.ToolRequest
		for _, tr := range resp.ToolRequests {
			if round < MaxTestRounds-1 && isLoadOf(tr, p.sk.Name) && len(pending) < 2 {
				pending = append(pending, tr)
				continue
			}
			r.Blocked = append(r.Blocked, clipText(tr.Tool+"."+tr.Action, 80))
		}
		if len(pending) == 0 {
			break
		}
		for _, tr := range pending {
			file, _ := tr.Args["file"].(string)
			l, err := t.cfg.Svc.render(ctx, p.org, p.sk, tg.version, tg.pkg, strings.TrimSpace(file))
			if err != nil {
				continue // an unknown file: the agent just does not get it
			}
			loaded = append(loaded, loadedBlock(l))
			if l.File == "" {
				r.UsedSkill = true
			}
		}
		if len(loaded) == 0 {
			break
		}
	}
	r.DurationMS = int(time.Since(start).Milliseconds())
	r.Output = outputText(last.Output)
	r.Status = RunDone
	if r.Output == "" {
		return fail("empty_answer")
	}
	return r
}

// outputText flattens the structured answer into readable text: redacted, bounded.
func outputText(o domain.StructuredOutput) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(o.Summary))
	add := func(head string, items []string) {
		if len(items) == 0 {
			return
		}
		b.WriteString("\n\n" + head + "\n")
		for _, it := range items {
			if it = strings.TrimSpace(it); it != "" {
				b.WriteString("- " + it + "\n")
			}
		}
	}
	add("Findings", o.Findings)
	add("Recommendations", o.Recommendations)
	s := strings.TrimSpace(sanitize.RedactSecrets(b.String(), nil))
	return clipText(s, MaxRunOutput)
}

// ---- reading ----

// Runs is the history of a case (newest first).
func (t *Tester) Runs(ctx context.Context, a Actor, skillID, caseID string, limit int) ([]TestRun, error) {
	org := t.orgOf(ctx)
	if _, err := t.cfg.Svc.visible(ctx, org, a, skillID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = testRunsListed
	}
	l, err := t.cfg.Store.ListRuns(ctx, org, skillID, caseID, limit)
	if l == nil {
		l = []TestRun{}
	}
	return l, err
}

// Overview is the cases, the latest runs and the last test of each version: the review screen reads it.
func (t *Tester) Overview(ctx context.Context, a Actor, skillID string) (TestOverview, error) {
	org := t.orgOf(ctx)
	if _, err := t.cfg.Svc.visible(ctx, org, a, skillID); err != nil {
		return TestOverview{}, err
	}
	out := TestOverview{Cases: []TestCase{}, Runs: []TestRun{}, LastByVersion: []Tested{}}
	out.Limits.Cases, out.Limits.Title, out.Limits.Prompt, out.Limits.Expect = MaxTestCases, MaxCaseTitle, MaxCasePrompt, MaxCaseExpect
	var err error
	if out.Cases, err = t.cfg.Store.ListCases(ctx, org, skillID); err != nil {
		return out, err
	}
	if out.Cases == nil {
		out.Cases = []TestCase{}
	}
	all, err := t.cfg.Store.ListRuns(ctx, org, skillID, "", testRunsScan)
	if err != nil {
		return out, err
	}
	seen := map[int]bool{}
	for i, r := range all {
		if i < testRunsListed {
			out.Runs = append(out.Runs, r)
		}
		if r.Status == RunDone && !seen[r.Version] {
			seen[r.Version] = true
			out.LastByVersion = append(out.LastByVersion, Tested{Version: r.Version, At: r.CreatedAt, Status: r.Status, UsedSkill: r.UsedSkill, CaseID: r.CaseID, RunID: r.ID})
		}
	}
	sort.Slice(out.LastByVersion, func(i, j int) bool { return out.LastByVersion[i].Version > out.LastByVersion[j].Version })
	return out, nil
}

// ---- in-memory store ----

// MemTestStore is the in-memory TestStore (tests and the database-less demo).
type MemTestStore struct {
	mu    sync.Mutex
	cases map[string][]TestCase // org -> cases
	runs  map[string][]TestRun  // org -> runs, oldest first
}

// NewMemTestStore returns an empty store.
func NewMemTestStore() *MemTestStore {
	return &MemTestStore{cases: map[string][]TestCase{}, runs: map[string][]TestRun{}}
}

var _ TestStore = (*MemTestStore)(nil)

func (m *MemTestStore) ListCases(_ context.Context, org, skillID string) ([]TestCase, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []TestCase{}
	for _, c := range m.cases[org] {
		if c.SkillID == skillID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *MemTestStore) GetCase(_ context.Context, org, skillID, id string) (TestCase, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.cases[org] {
		if c.ID == id && c.SkillID == skillID {
			return c, nil
		}
	}
	return TestCase{}, domain.ErrNotFound
}

func (m *MemTestStore) PutCase(_ context.Context, org string, c TestCase) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, o := range m.cases[org] {
		if o.ID == c.ID {
			m.cases[org][i] = c
			return nil
		}
	}
	m.cases[org] = append(m.cases[org], c)
	return nil
}

func (m *MemTestStore) DeleteCase(_ context.Context, org, skillID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, c := range m.cases[org] {
		if c.ID == id && c.SkillID == skillID {
			m.cases[org] = append(m.cases[org][:i:i], m.cases[org][i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (m *MemTestStore) AddRun(_ context.Context, org string, r TestRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[org] = append(m.runs[org], r)
	return nil
}

func (m *MemTestStore) ListRuns(_ context.Context, org, skillID, caseID string, limit int) ([]TestRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []TestRun{}
	for i := len(m.runs[org]) - 1; i >= 0; i-- {
		r := m.runs[org][i]
		if r.SkillID != skillID || (caseID != "" && r.CaseID != caseID) {
			continue
		}
		out = append(out, r)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// String is only for error messages in tests.
func (r TestRun) String() string {
	return fmt.Sprintf("run %s v%d %s/%s", r.ID, r.Version, r.Status, r.FailCode)
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
