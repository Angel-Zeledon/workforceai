package rituals_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/quality"
	"aiworkforce/backend/internal/rituals"
	"aiworkforce/backend/internal/roles"
)

type nopPub struct{}

func (nopPub) Publish(context.Context, domain.Event) error { return nil }

type env struct {
	t       *testing.T
	mem     *memory.Store
	org     string
	now     time.Time
	verdict map[string]application.GuardVerdict // agent id ("" = organization) -> verdict
	svc     *rituals.Service
	store   *rituals.MemStore
	cfg     rituals.Config

	mu      sync.Mutex
	notes   []string
	events  []application.Action
	audits  []domain.AuditLog
	summary func(application.SummarizeRequest) (application.SummaryResult, error)
	sumCall int
}

func newEnv(t *testing.T, mut ...func(*rituals.Config)) *env {
	t.Helper()
	mem := memory.New()
	cfg := application.DefaultConfig()
	if err := mem.Seed(context.Background(), domain.SeedOrg(cfg.BudgetUSD), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, mem: mem, org: cfg.OrgID, now: time.Date(2026, 10, 21, 15, 31, 0, 0, time.UTC), // Wednesday 09:31 in Mexico City
		verdict: map[string]application.GuardVerdict{}, store: rituals.NewMemStore()}
	e.cfg = rituals.Config{Store: e.store, Core: mem, OrgID: cfg.OrgID, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return e.now },
		Guard: func(_ context.Context, id string) application.GuardVerdict {
			if v, ok := e.verdict[id]; ok {
				return v
			}
			return application.GuardVerdict{Allowed: true}
		},
		Notice: func(_ context.Context, text string) error {
			e.mu.Lock()
			e.notes = append(e.notes, text)
			e.mu.Unlock()
			return nil
		},
		Emit: func(_ context.Context, a application.Action) {
			e.mu.Lock()
			e.events = append(e.events, a)
			e.mu.Unlock()
		},
		Audit: func(_ context.Context, a domain.AuditLog) { e.mu.Lock(); e.audits = append(e.audits, a); e.mu.Unlock() },
		Summarize: func(_ context.Context, in application.SummarizeRequest) (application.SummaryResult, error) {
			e.sumCall++
			if e.summary != nil {
				return e.summary(in)
			}
			return application.SummaryResult{Text: "model summary", Source: "runtime"}, nil
		},
	}
	for _, m := range mut {
		m(&e.cfg)
	}
	e.svc = rituals.New(e.cfg)
	return e
}

func (e *env) ctx() context.Context {
	return application.WithActor(application.WithOrg(context.Background(), e.org), "ana@example.com")
}

func (e *env) task(id, agent string, st domain.TaskStatus, created time.Time, finished *time.Time, out *domain.StructuredOutput) {
	e.t.Helper()
	if err := e.mem.CreateTask(context.Background(), e.org, domain.Task{ID: id, Title: "Tarea " + id, AgentID: agent, Status: st, CreatedAt: created, FinishedAt: finished, Output: out}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) approval(id, agent string, st domain.ApprovalStatus, note string, resolved *time.Time) {
	e.t.Helper()
	if err := e.mem.CreateApproval(context.Background(), e.org, domain.Approval{ID: id, TaskID: "t-" + id, AgentID: agent, Title: "Enviar " + id, Status: st, Note: note,
		CreatedAt: e.now.Add(-48 * time.Hour), ResolvedAt: resolved}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) audited(action string) int {
	n := 0
	for _, a := range e.audits {
		if a.Action == action {
			n++
		}
	}
	return n
}

func ptr(t time.Time) *time.Time { return &t }

func entryOf(r rituals.Ritual, agent string) (rituals.Entry, bool) {
	for _, e := range r.Entries {
		if e.AgentID == agent {
			return e, true
		}
	}
	return rituals.Entry{}, false
}

func titles(items []rituals.Item) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.Title+"/"+it.Status)
	}
	return out
}

// The stand-up is built from the real tasks and approvals, deterministically, and it is announced (office event,
// chat note, audit).
func TestStandupFromRealTasks(t *testing.T) {
	e := newEnv(t)
	n := e.now
	e.task("old", "sales", domain.TaskDone, n.Add(-80*time.Hour), ptr(n.Add(-30*time.Hour)), nil) // before the window (24 h)
	e.task("d1", "sales", domain.TaskDone, n.Add(-20*time.Hour), ptr(n.Add(-10*time.Hour)), nil)
	e.task("d2", "sales", domain.TaskFailed, n.Add(-20*time.Hour), ptr(n.Add(-2*time.Hour)), nil)
	e.task("p1", "sales", domain.TaskPending, n.Add(-3*time.Hour), nil, nil)
	e.task("r1", "sales", domain.TaskRunning, n.Add(-1*time.Hour), nil, nil)
	e.task("b1", "sales", domain.TaskBlocked, n.Add(-5*time.Hour), nil, nil)
	e.task("w1", "sales", domain.TaskAwaitingApproval, n.Add(-4*time.Hour), nil, nil)
	e.approval("w1x", "sales", domain.ApprovalPending, "", nil)

	r, err := e.svc.RunNow(e.ctx(), rituals.KindStandup)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != rituals.StatusHeld || r.Origin != rituals.OriginManual || r.CreatedBy != "ana@example.com" || r.SummarySource != rituals.SummaryDeterministic {
		t.Fatalf("ritual: %+v", r)
	}
	s, ok := entryOf(r, "sales")
	if !ok {
		t.Fatal("sales takes part")
	}
	if got := titles(s.Done); !reflect.DeepEqual(got, []string{"Tarea d2/failed", "Tarea d1/done"}) {
		t.Errorf("done = %v (newest first, the old one is outside the window)", got)
	}
	if got := titles(s.Next); !reflect.DeepEqual(got, []string{"Tarea r1/running", "Tarea p1/pending"}) {
		t.Errorf("next = %v (running first)", got)
	}
	if got := titles(s.Blockers); !reflect.DeepEqual(got, []string{"Tarea b1/blocked", "Tarea w1/awaiting_approval", "Enviar w1x/approval_pending"}) {
		t.Errorf("blockers = %v", got)
	}
	if len(s.Lines) != 3 || !strings.HasPrefix(s.Lines[0], "Hice: ") || !strings.Contains(s.Lines[0], "«Tarea d2» (falló)") ||
		!strings.HasPrefix(s.Lines[1], "Haré: «Tarea r1» (en curso)") || !strings.HasPrefix(s.Lines[2], "Bloqueos: ") {
		t.Errorf("lines = %q", s.Lines)
	}
	// an agent with nothing to report is present, quiet, with two lines
	quiet := 0
	for _, x := range r.Entries {
		if x.Quiet {
			quiet++
			if len(x.Lines) != 2 {
				t.Errorf("a quiet entry has 2 lines: %q", x.Lines)
			}
		}
	}
	if quiet == 0 || quiet != len(r.Entries)-1 {
		t.Errorf("quiet = %d of %d", quiet, len(r.Entries))
	}
	if len(e.notes) != 1 || !strings.Contains(e.notes[0], "Stand-up diario") || !strings.Contains(e.notes[0], "«Tarea r1»") || !strings.Contains(e.notes[0], "Sin novedades: ") {
		t.Errorf("chat note: %q", e.notes)
	}
	if len(e.events) != 1 || e.events[0].Type != "ritual.held" || !e.events[0].SkipAudit {
		t.Fatalf("event: %+v", e.events)
	}
	ids := e.events[0].Payload.(map[string]any)["agent_ids"].([]string)
	if len(ids) != len(r.Entries) {
		t.Errorf("every participant walks to the meeting room: %v", ids)
	}
	if e.audited("ritual.held") != 1 || e.sumCall != 0 {
		t.Errorf("audits %v, model calls %d (use_model is off by default)", e.audits, e.sumCall)
	}

	// A minute later, with the same data: the open work is the same.
	e.now = e.now.Add(time.Minute)
	r2, err := e.svc.RunNow(e.ctx(), rituals.KindStandup)
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := entryOf(r2, "sales")
	// the window of the second one starts at the first one, so d1/d2 are no longer "since the last stand-up"
	if len(s2.Done) != 0 || !reflect.DeepEqual(s2.Next, s.Next) || !reflect.DeepEqual(s2.Blockers, s.Blockers) {
		t.Errorf("second stand-up: %+v", s2)
	}
	got, err := e.svc.Get(e.ctx(), r.ID)
	if err != nil || len(got.Entries) != len(r.Entries) {
		t.Fatalf("get: %v %v", got, err)
	}
	list, _ := e.svc.List(e.ctx(), rituals.KindStandup, 0)
	if len(list) != 2 || list[0].ID != r2.ID {
		t.Errorf("list newest first: %v", list)
	}
}

func TestEnglishLines(t *testing.T) {
	e := newEnv(t, func(c *rituals.Config) { c.Locale = func(context.Context) string { return "en" } })
	e.task("d1", "sales", domain.TaskDone, e.now.Add(-5*time.Hour), ptr(e.now.Add(-time.Hour)), nil)
	r, err := e.svc.RunNow(e.ctx(), rituals.KindStandup)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := entryOf(r, "sales")
	if r.Locale != "en" || s.Lines[0] != "Did: “Tarea d1”." || s.Lines[1] != "Next: nothing assigned; available for new work." {
		t.Errorf("lines = %q", s.Lines)
	}
}

func TestKillSwitchBlocksManualAndSkipsScheduled(t *testing.T) {
	e := newEnv(t)
	e.verdict[""] = application.GuardVerdict{Code: "kill_switch_active"}
	_, err := e.svc.RunNow(e.ctx(), rituals.KindStandup)
	var re *rituals.Error
	if !errors.As(err, &re) || re.Code != rituals.CodeBlocked {
		t.Fatalf("manual under kill switch = %v", err)
	}
	if len(e.notes) != 0 || len(e.events) != 0 {
		t.Error("nothing is announced")
	}
	if _, err := e.svc.PutSettings(e.ctx(), rituals.Settings{Timezone: "America/Mexico_City", Standup: rituals.Slot{Enabled: true, Time: "09:30"}}); err != nil {
		t.Fatal(err)
	}
	if n := e.svc.RunDue(context.Background(), []string{e.org}); n != 1 {
		t.Fatalf("the skipped occurrence is recorded once: %d", n)
	}
	if n := e.svc.RunDue(context.Background(), []string{e.org}); n != 0 {
		t.Fatalf("not twice: %d", n)
	}
	list, _ := e.svc.List(e.ctx(), "", 0)
	if len(list) != 1 || list[0].Status != rituals.StatusSkipped || list[0].SkipReason != "kill_switch_active" || list[0].PeriodKey != "2026-10-21" {
		t.Fatalf("list = %+v", list)
	}
	if len(e.notes) != 0 || e.audited("ritual.skipped") != 1 {
		t.Errorf("skip audited, not announced: %v %v", e.notes, e.audits)
	}
}

func TestOperatingHoursAndPausedAgents(t *testing.T) {
	e := newEnv(t)
	e.verdict[""] = application.GuardVerdict{Code: "outside_operating_hours"}
	e.verdict["sales"] = application.GuardVerdict{Code: "agent_paused"}
	r, err := e.svc.RunNow(e.ctx(), rituals.KindStandup)
	if err != nil || r.Status != rituals.StatusHeld {
		t.Fatalf("a person may call a stand-up outside the operating hours: %v %v", r, err)
	}
	if _, ok := entryOf(r, "sales"); ok || len(r.Absent) != 1 || r.Absent[0].AgentID != "sales" || r.Absent[0].Reason != "paused" {
		t.Errorf("a paused agent does not take part: %+v", r.Absent)
	}
	if !strings.Contains(e.notes[0], "En pausa (no participan): ") {
		t.Errorf("note: %q", e.notes[0])
	}
	if _, err := e.svc.PutSettings(e.ctx(), rituals.Settings{Timezone: "America/Mexico_City", Standup: rituals.Slot{Enabled: true, Time: "09:30"}}); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(time.Minute)
	e.svc.RunDue(context.Background(), []string{e.org})
	list, _ := e.svc.List(e.ctx(), rituals.KindStandup, 0)
	if list[0].Origin != rituals.OriginScheduled || list[0].Status != rituals.StatusSkipped || list[0].SkipReason != "outside_operating_hours" {
		t.Errorf("the schedule stays quiet outside the operating hours: %+v", list[0])
	}
}

func TestScheduleOncePerPeriod(t *testing.T) {
	e := newEnv(t)
	if n := e.svc.RunDue(context.Background(), []string{e.org}); n != 0 {
		t.Fatalf("both rituals are off by default: %d", n)
	}
	if _, err := e.svc.PutSettings(e.ctx(), rituals.Settings{Timezone: "America/Mexico_City",
		Standup: rituals.Slot{Enabled: true, Time: "09:30", Weekdays: []int{3, 1}}, Retro: rituals.Slot{Enabled: true, Time: "09:00", Weekdays: []int{3}}}); err != nil {
		t.Fatal(err)
	}
	if n := e.svc.RunDue(context.Background(), []string{e.org}); n != 2 {
		t.Fatalf("stand-up and retro are due: %d", n)
	}
	if n := e.svc.RunDue(context.Background(), []string{e.org}); n != 0 {
		t.Fatalf("each occurrence once: %d", n)
	}
	list, _ := e.svc.List(e.ctx(), "", 0)
	keys := map[rituals.Kind]string{}
	for _, r := range list {
		keys[r.Kind] = r.PeriodKey
		if r.CreatedBy != "scheduler" || r.Origin != rituals.OriginScheduled {
			t.Errorf("scheduled ritual: %+v", r)
		}
	}
	if keys[rituals.KindStandup] != "2026-10-21" || keys[rituals.KindRetro] != "2026-W43" {
		t.Errorf("period keys = %v", keys)
	}
	// Next day 09:29 (before the slot) nothing; at 12:00 the 09:30 slot is stale (server was down) and is not held late.
	e.now = time.Date(2026, 10, 22, 15, 29, 0, 0, time.UTC)
	if n := e.svc.RunDue(context.Background(), []string{e.org}); n != 0 {
		t.Errorf("Thursday is not a stand-up day here: %d", n)
	}
	e.now = time.Date(2026, 10, 26, 18, 0, 0, 0, time.UTC) // Monday 12:00 local
	if n := e.svc.RunDue(context.Background(), []string{e.org}); n != 0 {
		t.Errorf("stale slot: %d", n)
	}
	e.now = time.Date(2026, 10, 26, 15, 45, 0, 0, time.UTC) // Monday 09:45 local
	if n := e.svc.RunDue(context.Background(), []string{e.org}); n != 1 {
		t.Errorf("Monday stand-up: %d", n)
	}
	last, _ := e.svc.List(e.ctx(), rituals.KindStandup, 1)
	if last[0].WindowFrom == nil || !last[0].WindowFrom.Equal(time.Date(2026, 10, 21, 15, 31, 0, 0, time.UTC)) {
		t.Errorf("Monday covers since the previous stand-up (Wednesday): %v", last[0].WindowFrom)
	}
}

func TestRetroProposalsFromOwnSignals(t *testing.T) {
	score := 62
	e := newEnv(t, func(c *rituals.Config) {
		c.Quality = func(context.Context) (map[string]rituals.QualitySnapshot, error) {
			return map[string]rituals.QualitySnapshot{
				"sales":      {Score: &score, Level: quality.LevelFair, Trend: quality.TrendDown},
				"accounting": {Score: &score, Level: quality.LevelFair, Trend: quality.TrendStable},
			}, nil
		}
	})
	n := e.now
	day := 24 * time.Hour
	// sales: 3 human rejections (+1 system one that does not count), 2 reworks, 2 failures
	for i := 0; i < 3; i++ {
		e.approval(fmt.Sprintf("rj%d", i), "sales", domain.ApprovalRejected, "importe incorrecto", ptr(n.Add(-time.Duration(i+1)*day)))
	}
	e.approval("sys", "sales", domain.ApprovalRejected, "sin respuesta: tiempo de espera agotado", ptr(n.Add(-day)))
	e.approval("old", "sales", domain.ApprovalRejected, "x", ptr(n.Add(-9*day)))
	rw := &domain.StructuredOutput{Review: &domain.Review{Verdict: "pass", Reworks: 1, Reasons: []string{"faltan cifras"}}}
	e.task("rw1", "sales", domain.TaskDone, n.Add(-3*day), ptr(n.Add(-2*day)), rw)
	e.task("rw2", "sales", domain.TaskDone, n.Add(-3*day), ptr(n.Add(-2*day)), rw)
	e.task("f1", "sales", domain.TaskFailed, n.Add(-3*day), ptr(n.Add(-2*day)), nil)
	e.task("f2", "sales", domain.TaskFailed, n.Add(-3*day), ptr(n.Add(-day)), nil)
	// finance: one clean task and a fair score -> a skill suggestion from its best task
	e.task("ok", "accounting", domain.TaskDone, n.Add(-3*day), ptr(n.Add(-day)), nil)

	r, err := e.svc.RunNow(e.ctx(), rituals.KindRetro)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := entryOf(r, "sales")
	if s.Retro == nil || s.Retro.Rejected != 3 || s.Retro.Reworks != 2 || s.Retro.TasksFailed != 2 || s.Retro.TasksDone != 2 || *s.Retro.Score != 62 {
		t.Fatalf("sales stats: %+v", s.Retro)
	}
	got, err := e.svc.Get(e.ctx(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	var salesP, finP []rituals.Proposal
	for _, p := range got.Proposals {
		switch p.AgentID {
		case "sales":
			salesP = append(salesP, p)
		case "accounting":
			finP = append(finP, p)
		}
	}
	if len(salesP) != 2 {
		t.Fatalf("at most 2 proposals per agent: %+v", salesP)
	}
	sig := map[string]bool{salesP[0].Signal: true, salesP[1].Signal: true}
	if !sig[rituals.SignalRejected] || !sig[rituals.SignalReworks] {
		t.Errorf("the strongest signals win (rejections 3, then reworks 2 before failures 2): %v", sig)
	}
	for _, p := range salesP {
		if p.Status != rituals.ProposalPending || p.Kind != rituals.ProposalInstructionNote || p.RitualID != r.ID || len(p.Evidence) == 0 {
			t.Errorf("proposal: %+v", p)
		}
		if strings.Contains(p.Text, "Tarea") || strings.Contains(p.Text, "Enviar") {
			t.Errorf("the note never carries third-party text: %q", p.Text)
		}
	}
	if len(finP) != 1 || finP[0].Kind != rituals.ProposalSkillSuggestion || finP[0].Signal != rituals.SignalQuality || !strings.Contains(strings.Join(finP[0].Evidence, " "), "Tarea ok") {
		t.Errorf("finance: %+v", finP)
	}
	if !strings.Contains(r.Summary, "3 propuestas") {
		t.Errorf("summary: %q", r.Summary)
	}
}

func TestModelSummaryOnlyWhenEnabledAndWithinBudget(t *testing.T) {
	e := newEnv(t)
	e.task("r1", "sales", domain.TaskRunning, e.now.Add(-time.Hour), nil, nil)
	if _, err := e.svc.PutSettings(e.ctx(), rituals.Settings{UseModel: true}); err != nil {
		t.Fatal(err)
	}
	r, err := e.svc.RunNow(e.ctx(), rituals.KindStandup)
	if err != nil || r.Summary != "model summary" || r.SummarySource != rituals.SummaryRuntime || e.sumCall != 1 {
		t.Fatalf("model summary: %+v %v (calls %d)", r, err, e.sumCall)
	}
	stored, _ := e.svc.Get(e.ctx(), r.ID)
	if stored.Summary != "model summary" {
		t.Errorf("stored summary: %q", stored.Summary)
	}
	e.summary = func(application.SummarizeRequest) (application.SummaryResult, error) {
		return application.SummaryResult{}, &application.Exceeded{Scope: "org"}
	}
	r, err = e.svc.RunNow(e.ctx(), rituals.KindStandup)
	if err != nil || r.SummarySource != rituals.SummaryDeterministic || !strings.HasPrefix(r.Summary, "Stand-up con") || e.audited("ritual.summary_skipped") != 1 {
		t.Errorf("over budget the deterministic summary stays: %+v %v", r, err)
	}
}

func TestManualRunsPerDayAreCapped(t *testing.T) {
	e := newEnv(t, func(c *rituals.Config) { c.ManualPerDay = 2 })
	for i := 0; i < 2; i++ {
		if _, err := e.svc.RunNow(e.ctx(), rituals.KindStandup); err != nil {
			t.Fatal(err)
		}
	}
	var re *rituals.Error
	if _, err := e.svc.RunNow(e.ctx(), rituals.KindStandup); !errors.As(err, &re) || re.Code != rituals.CodeLimit {
		t.Fatalf("third run = %v", err)
	}
	if _, err := e.svc.RunNow(e.ctx(), rituals.KindRetro); err != nil {
		t.Errorf("the cap is per kind: %v", err)
	}
	if _, err := e.svc.RunNow(e.ctx(), "party"); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("unknown kind = %v", err)
	}
	if _, err := e.svc.RunNow(application.WithActor(application.WithOrg(context.Background(), e.org), "sales"), rituals.KindRetro); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("an agent cannot call a ritual: %v", err)
	}
}

type fakeProfiles struct {
	view application.ProfileView
	fail error
	in   []application.ProfileInput
}

func (f *fakeProfiles) Get(context.Context, string) (application.ProfileView, error) {
	return f.view, nil
}
func (f *fakeProfiles) Update(_ context.Context, _ string, in application.ProfileInput) (application.ProfileView, error) {
	if f.fail != nil {
		return application.ProfileView{}, f.fail
	}
	f.in = append(f.in, in)
	f.view.Instructions, f.view.Version = in.Instructions, f.view.Version+1
	return f.view, nil
}

func retroWithProposals(t *testing.T, e *env) []rituals.Proposal {
	t.Helper()
	for i := 0; i < 2; i++ {
		e.approval(fmt.Sprintf("rj%d", i), "sales", domain.ApprovalRejected, "no", ptr(e.now.Add(-time.Hour)))
		e.task(fmt.Sprintf("f%d", i), "sales", domain.TaskFailed, e.now.Add(-2*time.Hour), ptr(e.now.Add(-time.Hour)), nil)
	}
	r, err := e.svc.RunNow(e.ctx(), rituals.KindRetro)
	if err != nil {
		t.Fatal(err)
	}
	ps, err := e.svc.Proposals(e.ctx(), rituals.ProposalQuery{RitualID: r.ID})
	if err != nil || len(ps) != 2 {
		t.Fatalf("proposals = %+v %v", ps, err)
	}
	return ps
}

func TestAcceptAppliesNoteThroughTheProfileOnce(t *testing.T) {
	prof := &fakeProfiles{}
	prof.view.Instructions, prof.view.Creativity, prof.view.Skills, prof.view.Version = "Sé breve.", "low", []string{"ventas"}, 3
	e := newEnv(t, func(c *rituals.Config) { c.Profiles = prof })
	ps := retroWithProposals(t, e)

	if _, err := e.svc.AcceptProposal(application.WithActor(application.WithOrg(context.Background(), e.org), "agent:sales"), ps[0].ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("agents never accept: %v", err)
	}
	res, err := e.svc.AcceptProposal(e.ctx(), ps[0].ID)
	if err != nil || !res.Applied || res.ProfileVersion != 4 || res.Proposal.Status != rituals.ProposalAccepted || !res.Proposal.Applied || res.Proposal.ResolvedBy != "ana@example.com" {
		t.Fatalf("accept = %+v %v", res, err)
	}
	if len(prof.in) != 1 || prof.in[0].Instructions != "Sé breve.\n\n"+ps[0].Text || prof.in[0].Creativity != "low" || !reflect.DeepEqual(prof.in[0].Skills, []string{"ventas"}) || prof.in[0].Tone != nil {
		t.Errorf("profile edit = %+v (only the instructions change)", prof.in)
	}
	if _, err := e.svc.AcceptProposal(e.ctx(), ps[0].ID); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("twice = %v", err)
	}
	if e.audited("ritual.proposal_accepted") != 1 {
		t.Error("accept is audited")
	}

	// A failing profile edit leaves the proposal pending.
	prof.fail = fmt.Errorf("%w: instructions are too long", domain.ErrInvalid)
	if _, err := e.svc.AcceptProposal(e.ctx(), ps[1].ID); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("failing edit = %v", err)
	}
	if p, _ := e.cfg.Store.GetProposal(context.Background(), e.org, ps[1].ID); p.Status != rituals.ProposalPending {
		t.Errorf("back to pending: %+v", p)
	}
	d, err := e.svc.DismissProposal(e.ctx(), ps[1].ID)
	if err != nil || d.Status != rituals.ProposalDismissed {
		t.Fatalf("dismiss = %+v %v", d, err)
	}
	if _, err := e.svc.DismissProposal(e.ctx(), "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing = %v", err)
	}
	pend, _ := e.svc.Proposals(e.ctx(), rituals.ProposalQuery{Status: rituals.ProposalPending})
	if len(pend) != 0 {
		t.Errorf("pending left: %+v", pend)
	}
}

func TestAcceptWithoutProfilesOnlyRecords(t *testing.T) {
	e := newEnv(t)
	ps := retroWithProposals(t, e)
	res, err := e.svc.AcceptProposal(e.ctx(), ps[0].ID)
	if err != nil || res.Applied || res.Proposal.Status != rituals.ProposalAccepted {
		t.Fatalf("accept = %+v %v", res, err)
	}
}

func TestSettingsValidation(t *testing.T) {
	e := newEnv(t)
	def, _ := e.svc.Settings(e.ctx())
	if def.Standup.Enabled || def.Retro.Enabled || def.UseModel || def.Standup.Time != "09:30" || !reflect.DeepEqual(def.Retro.Weekdays, []int{5}) {
		t.Errorf("defaults = %+v", def)
	}
	for _, bad := range []rituals.Settings{
		{Timezone: "Mars/Olympus"},
		{Standup: rituals.Slot{Time: "25:00"}},
		{Standup: rituals.Slot{Time: "9:30"}},
		{Standup: rituals.Slot{Weekdays: []int{1, 1}}},
		{Standup: rituals.Slot{Weekdays: []int{7}}},
		{Retro: rituals.Slot{Weekdays: []int{1, 5}}},
	} {
		if _, err := e.svc.PutSettings(e.ctx(), bad); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%+v accepted: %v", bad, err)
		}
	}
	got, err := e.svc.PutSettings(e.ctx(), rituals.Settings{Timezone: "Europe/Madrid", Standup: rituals.Slot{Enabled: true, Weekdays: []int{5, 1}}})
	if err != nil || got.Standup.Time != "09:30" || !reflect.DeepEqual(got.Standup.Weekdays, []int{1, 5}) || got.Retro.Time != "16:00" {
		t.Fatalf("normalized = %+v %v", got, err)
	}
	if e.audited("ritual.settings_changed") != 1 {
		t.Error("settings change is audited")
	}
}
