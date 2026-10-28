package rituals

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Core is what the rituals read from the application store.
type Core interface {
	ListAgents(ctx context.Context, orgID string) ([]domain.Agent, error)
	GetAgent(ctx context.Context, orgID, id string) (domain.Agent, error)
	ListTasks(ctx context.Context, orgID, agentID, status string) ([]domain.Task, error)
	ListApprovals(ctx context.Context, orgID, status string) ([]domain.Approval, error)
}

// ProfileEditor is the agent work profile (application.ProfileService): the normal, versioned and audited path a
// person uses to change an agent's instructions. It refuses agents as actors.
type ProfileEditor interface {
	Get(ctx context.Context, agentID string) (application.ProfileView, error)
	Update(ctx context.Context, agentID string, in application.ProfileInput) (application.ProfileView, error)
}

// Config wires the service. Only Store and Core are required.
type Config struct {
	Store Store
	Core  Core
	OrgID string // default organization (open demo)
	// Guard is the execution guard (kill switch, controls availability, agent pause, operating hours). nil = allowed.
	Guard func(ctx context.Context, agentID string) application.GuardVerdict
	// Summarize is the normal summarize path (budget reservation, ledger, local fallback). nil = never a model.
	Summarize func(ctx context.Context, in application.SummarizeRequest) (application.SummaryResult, error)
	// Notice posts a system line in the office chat. nil = no chat note.
	Notice func(ctx context.Context, text string) error
	// Emit publishes an event (office: agents walk to the meeting room). nil = none.
	Emit func(ctx context.Context, a application.Action)
	// Audit records every write and every run or skip. nil = none.
	Audit func(ctx context.Context, a domain.AuditLog)
	// Locale is the organization language ("es" when nil).
	Locale func(ctx context.Context) string
	// Quality reads the advisory quality score of every agent (retro). nil = the retro uses no score.
	Quality func(ctx context.Context) (map[string]QualitySnapshot, error)
	// Profiles applies an accepted instruction note. nil = accepting only records the decision.
	Profiles ProfileEditor
	// ManualPerDay caps the "now" runs per kind and UTC day (0 = 10).
	ManualPerDay int
	Now          func() time.Time
	Log          *slog.Logger
}

// Service holds rituals and resolves their proposals.
type Service struct{ cfg Config }

// New builds the service.
func New(cfg Config) *Service {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.ManualPerDay <= 0 {
		cfg.ManualPerDay = 10
	}
	return &Service{cfg: cfg}
}

const (
	// staleWindow: a scheduled ritual missed by more than this (server down) is not held late.
	staleWindow = 2 * time.Hour
	// standupLookback bounds "since the last stand-up" (a Monday stand-up covers the weekend; at most a week).
	standupLookback = 7 * 24 * time.Hour
	retroWindow     = 7 * 24 * time.Hour
	// qualityWindowDays is the window of the quality score the retro reads (its default window).
	qualityWindowDays = 30
)

func (s *Service) org(ctx context.Context) string { return application.OrgFrom(ctx, s.cfg.OrgID) }

func (s *Service) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) locale(ctx context.Context) string {
	if s.cfg.Locale != nil && s.cfg.Locale(ctx) == "en" {
		return "en"
	}
	return "es"
}

func (s *Service) audit(ctx context.Context, actor, action, entity, id string, d map[string]any) {
	if s.cfg.Audit != nil {
		s.cfg.Audit(ctx, domain.AuditLog{Actor: actor, Action: action, Entity: entity, EntityID: id, Details: d})
	}
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// isAgentActor tells an agent principal from a person (agents never run rituals or accept proposals by themselves).
func (s *Service) isAgentActor(ctx context.Context, actor string) bool {
	a := strings.ToLower(strings.TrimSpace(actor))
	if strings.HasPrefix(a, "agent:") {
		return true
	}
	if a == "" || a == "user" || a == "scheduler" {
		return false
	}
	_, err := s.cfg.Core.GetAgent(ctx, s.org(ctx), actor)
	return err == nil
}

// ---- settings ----

// Settings returns the stored settings or the defaults.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	st, ok, err := s.cfg.Store.GetSettings(ctx, s.org(ctx))
	if err != nil {
		return Settings{}, err
	}
	if !ok {
		return DefaultSettings(), nil
	}
	if n, err := st.Normalize(); err == nil {
		return n, nil
	}
	return DefaultSettings(), nil
}

// PutSettings validates and stores the settings (audited).
func (s *Service) PutSettings(ctx context.Context, in Settings) (Settings, error) {
	n, err := in.Normalize()
	if err != nil {
		return Settings{}, err
	}
	if err := s.cfg.Store.PutSettings(ctx, s.org(ctx), n); err != nil {
		return Settings{}, err
	}
	s.audit(ctx, application.ActorFrom(ctx, "user"), "ritual.settings_changed", "ritual_settings", s.org(ctx), map[string]any{
		"timezone": n.Timezone, "standup_enabled": n.Standup.Enabled, "standup_time": n.Standup.Time, "standup_weekdays": n.Standup.Weekdays,
		"retro_enabled": n.Retro.Enabled, "retro_time": n.Retro.Time, "retro_weekdays": n.Retro.Weekdays, "use_model": n.UseModel})
	return n, nil
}

// ---- reading ----

// List returns the rituals newest first (kind "" = both).
func (s *Service) List(ctx context.Context, kind Kind, limit int) ([]Ritual, error) {
	if kind != "" && !kind.Valid() {
		return nil, fmt.Errorf("%w: unknown ritual kind", domain.ErrInvalid)
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	return s.cfg.Store.ListRituals(ctx, s.org(ctx), kind, time.Time{}, limit)
}

// Get returns one ritual with its proposals.
func (s *Service) Get(ctx context.Context, id string) (Ritual, error) {
	org := s.org(ctx)
	r, err := s.cfg.Store.GetRitual(ctx, org, id)
	if err != nil {
		return Ritual{}, err
	}
	if r.Kind == KindRetro {
		ps, err := s.cfg.Store.ListProposals(ctx, org, ProposalQuery{RitualID: r.ID, Limit: 200})
		if err != nil {
			return Ritual{}, err
		}
		r.Proposals = ps
	}
	return r, nil
}

// Proposals lists retro proposals (status "" = any).
func (s *Service) Proposals(ctx context.Context, q ProposalQuery) ([]Proposal, error) {
	switch q.Status {
	case "", ProposalPending, ProposalAccepted, ProposalDismissed:
	default:
		return nil, fmt.Errorf("%w: unknown status", domain.ErrInvalid)
	}
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 100
	}
	return s.cfg.Store.ListProposals(ctx, s.org(ctx), q)
}

// ---- holding a ritual ----

// RunNow holds a ritual because a person asked ("Hacer stand-up ahora"). It is refused while the kill switch is on;
// outside the operating hours it still runs (a person asked; nothing is executed and nothing leaves the platform).
func (s *Service) RunNow(ctx context.Context, kind Kind) (Ritual, error) {
	if !kind.Valid() {
		return Ritual{}, fmt.Errorf("%w: unknown ritual kind", domain.ErrInvalid)
	}
	actor := application.ActorFrom(ctx, "user")
	if s.isAgentActor(ctx, actor) {
		return Ritual{}, fmt.Errorf("%w: agents cannot call a ritual", domain.ErrForbidden)
	}
	org, now := s.org(ctx), s.now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	today, err := s.cfg.Store.ListRituals(ctx, org, kind, day, 200)
	if err != nil {
		return Ritual{}, err
	}
	n := 0
	for _, r := range today {
		if r.Origin == OriginManual {
			n++
		}
	}
	if n >= s.cfg.ManualPerDay {
		return Ritual{}, &Error{Code: CodeLimit, Msg: fmt.Sprintf("at most %d manual runs per day", s.cfg.ManualPerDay)}
	}
	r, _, err := s.hold(ctx, org, kind, OriginManual, "", actor)
	return r, err
}

// hold builds and stores one ritual. For a scheduled one, held is false when another instance already held it.
func (s *Service) hold(ctx context.Context, org string, kind Kind, origin, periodKey, actor string) (Ritual, bool, error) {
	ctx = application.WithOrg(ctx, org)
	now, loc := s.now(), s.locale(ctx)
	r := Ritual{ID: newID("rit_"), Kind: kind, Origin: origin, PeriodKey: periodKey, Status: StatusHeld, Locale: loc,
		Entries: []Entry{}, Absent: []Absent{}, CreatedBy: actor, HeldAt: now}

	// Controls: the kill switch stops everything; outside the operating hours only the schedule stays quiet.
	if v := s.guard(ctx, ""); !v.Allowed {
		blocked := v.Code != "outside_operating_hours" && v.Code != "read_only_mode"
		if origin == OriginManual && blocked {
			s.audit(ctx, actor, "ritual.blocked", "ritual", string(kind), map[string]any{"kind": kind, "reason": v.Code})
			return Ritual{}, false, &Error{Code: CodeBlocked, Msg: v.Code}
		}
		if origin == OriginScheduled && v.Code != "read_only_mode" {
			r.Status, r.SkipReason = StatusSkipped, v.Code
			ok, err := s.cfg.Store.CreateRitual(ctx, org, r)
			if err != nil || !ok {
				return Ritual{}, false, err
			}
			s.audit(ctx, actor, "ritual.skipped", "ritual", r.ID, map[string]any{"kind": kind, "origin": origin, "reason": v.Code, "period": periodKey})
			return r, true, nil
		}
	}

	agents, err := s.cfg.Core.ListAgents(ctx, org)
	if err != nil {
		return Ritual{}, false, err
	}
	tasks, err := s.cfg.Core.ListTasks(ctx, org, "", "")
	if err != nil {
		return Ritual{}, false, err
	}
	approvals, err := s.cfg.Core.ListApprovals(ctx, org, "")
	if err != nil {
		return Ritual{}, false, err
	}
	data := groupByAgent(tasks, approvals)

	from, to := now.Add(-24*time.Hour), now
	if kind == KindRetro {
		from = now.Add(-retroWindow)
	} else if prev := s.previousStandup(ctx, org, now); prev != nil {
		from = *prev
	}
	r.WindowFrom, r.WindowTo = &from, &to

	var snaps map[string]QualitySnapshot
	if kind == KindRetro && s.cfg.Quality != nil {
		if snaps, err = s.cfg.Quality(ctx); err != nil {
			s.cfg.Log.Warn("rituals: quality score unavailable for the retro", "org", org, "err", err)
			snaps = nil
		}
	}
	var proposals []Proposal
	for _, a := range agents {
		if v := s.guard(ctx, a.ID); !v.Allowed && v.Code == "agent_paused" {
			r.Absent = append(r.Absent, Absent{AgentID: a.ID, Name: a.Name, Reason: "paused"})
			continue
		}
		if kind == KindStandup {
			r.Entries = append(r.Entries, standupEntry(a, data[a.ID], from, to, loc))
			continue
		}
		qs, ok := snaps[a.ID]
		e, ps := retroEntry(a, data[a.ID], from, to, qs, ok, loc)
		r.Entries = append(r.Entries, e)
		for _, p := range ps {
			p.ID, p.RitualID, p.Status, p.CreatedAt = newID("rp_"), r.ID, ProposalPending, now
			proposals = append(proposals, p)
		}
	}
	r.Summary, r.SummarySource = deterministicSummary(r, len(proposals), loc), SummaryDeterministic

	ok, err := s.cfg.Store.CreateRitual(ctx, org, r)
	if err != nil || !ok {
		return Ritual{}, false, err
	}
	if len(proposals) > 0 {
		if err := s.cfg.Store.CreateProposals(ctx, org, proposals); err != nil {
			s.cfg.Log.Warn("rituals: store proposals", "org", org, "ritual", r.ID, "err", err)
		} else {
			r.Proposals = proposals
		}
	}
	s.modelSummary(ctx, org, &r, actor)
	s.announce(ctx, r, actor, len(proposals))
	return r, true, nil
}

// previousStandup is when the last held stand-up happened, when it is recent enough to be "the last one".
func (s *Service) previousStandup(ctx context.Context, org string, now time.Time) *time.Time {
	list, err := s.cfg.Store.ListRituals(ctx, org, KindStandup, now.Add(-standupLookback), 50)
	if err != nil {
		return nil
	}
	for _, r := range list { // newest first
		if r.Status == StatusHeld && r.HeldAt.Before(now) {
			t := r.HeldAt
			return &t
		}
	}
	return nil
}

func (s *Service) guard(ctx context.Context, agentID string) application.GuardVerdict {
	if s.cfg.Guard == nil {
		return application.GuardVerdict{Allowed: true}
	}
	return s.cfg.Guard(ctx, agentID)
}

// modelSummary lets the summarize path write the team summary when the organization enabled it. Only the
// deterministic lines are sent (as data); a budget refusal or any failure keeps the deterministic summary.
func (s *Service) modelSummary(ctx context.Context, org string, r *Ritual, actor string) {
	if s.cfg.Summarize == nil || r.Status != StatusHeld {
		return
	}
	st, err := s.Settings(ctx)
	if err != nil || !st.UseModel {
		return
	}
	var msgs []application.ChatHistoryItem
	var names []string
	for _, e := range r.Entries {
		names = append(names, e.Name)
		if e.Quiet {
			continue
		}
		msgs = append(msgs, application.ChatHistoryItem{From: e.Name, Text: strings.Join(e.Lines, " ")})
	}
	if len(msgs) == 0 {
		return
	}
	topic := tr(r.Locale, "Stand-up diario: qué hizo cada uno, qué hará y qué lo bloquea", "Daily stand-up: what each one did, will do and what blocks them")
	if r.Kind == KindRetro {
		topic = tr(r.Locale, "Retrospectiva semanal: resultados de la semana y mejoras propuestas", "Weekly retrospective: results of the week and proposed improvements")
	}
	res, err := s.cfg.Summarize(ctx, application.SummarizeRequest{Mode: "meeting", Topic: topic, Participants: names, Messages: msgs, Locale: r.Locale})
	if err != nil || strings.TrimSpace(res.Text) == "" {
		reason := "empty"
		var ex *application.Exceeded
		if errors.As(err, &ex) {
			reason = "budget_exceeded"
		} else if err != nil {
			reason = "error"
		}
		s.audit(ctx, actor, "ritual.summary_skipped", "ritual", r.ID, map[string]any{"kind": r.Kind, "reason": reason})
		return
	}
	src := SummaryLocal
	if res.Source == "runtime" {
		src = SummaryRuntime
	}
	text := strings.TrimSpace(res.Text)
	if err := s.cfg.Store.SetRitualSummary(ctx, org, r.ID, text, src); err != nil {
		s.cfg.Log.Warn("rituals: store summary", "ritual", r.ID, "err", err)
		return
	}
	r.Summary, r.SummarySource = text, src
}

// announce publishes the event the office reacts to (agents walk to the meeting room), posts the note in the
// office chat and audits the run.
func (s *Service) announce(ctx context.Context, r Ritual, actor string, proposals int) {
	ids := make([]string, 0, len(r.Entries))
	for _, e := range r.Entries {
		ids = append(ids, e.AgentID)
	}
	if s.cfg.Emit != nil {
		text := tr(r.Locale, "Stand-up diario en la sala de reuniones", "Daily stand-up in the meeting room")
		if r.Kind == KindRetro {
			text = tr(r.Locale, "Retrospectiva semanal en la sala de reuniones", "Weekly retrospective in the meeting room")
		}
		s.cfg.Emit(ctx, application.Action{Type: "ritual.held", Entity: "ritual", EntityID: r.ID, SkipAudit: true, Text: text,
			Payload: map[string]any{"ritual_id": r.ID, "kind": r.Kind, "origin": r.Origin, "agent_ids": ids, "proposals": proposals}})
	}
	if s.cfg.Notice != nil {
		if err := s.cfg.Notice(ctx, chatNote(r, r.Locale)); err != nil {
			s.cfg.Log.Warn("rituals: chat note", "ritual", r.ID, "err", err)
		}
	}
	s.audit(ctx, actor, "ritual.held", "ritual", r.ID, map[string]any{"kind": r.Kind, "origin": r.Origin, "period": r.PeriodKey,
		"agents": len(r.Entries), "absent": len(r.Absent), "proposals": proposals, "summary_source": r.SummarySource})
}

// ---- schedule ----

// due tells whether the slot has an occurrence to hold now and its period key.
func due(now time.Time, tz string, slot Slot, kind Kind) (string, bool) {
	if !slot.Enabled {
		return "", false
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return "", false
	}
	hh, mm, err := parseClock(slot.Time)
	if err != nil {
		return "", false
	}
	local := now.In(loc)
	if !slices.Contains(slot.Weekdays, int(local.Weekday())) {
		return "", false
	}
	at := time.Date(local.Year(), local.Month(), local.Day(), hh, mm, 0, 0, loc)
	if local.Before(at) || local.Sub(at) > staleWindow {
		return "", false
	}
	if kind == KindRetro {
		y, w := local.ISOWeek()
		return fmt.Sprintf("%d-W%02d", y, w), true
	}
	return local.Format("2006-01-02"), true
}

// RunDue holds the scheduled rituals that are due in the given organizations. Safe with several instances: the
// occurrence is claimed by its period key, so each one is held at most once. It returns how many were stored.
func (s *Service) RunDue(ctx context.Context, orgs []string) int {
	n := 0
	now := s.now()
	for _, org := range orgs {
		octx := application.WithActor(application.WithOrg(ctx, org), "scheduler")
		st, err := s.Settings(octx)
		if err != nil {
			s.cfg.Log.Warn("rituals: settings", "org", org, "err", err)
			continue
		}
		for _, kind := range []Kind{KindStandup, KindRetro} {
			slot := st.Standup
			if kind == KindRetro {
				slot = st.Retro
			}
			key, ok := due(now, st.Timezone, slot, kind)
			if !ok {
				continue
			}
			if done, err := s.cfg.Store.HasScheduled(octx, org, kind, key); err != nil || done {
				continue
			}
			if _, held, err := s.hold(octx, org, kind, OriginScheduled, key, "scheduler"); err != nil {
				s.cfg.Log.Warn("rituals: scheduled run", "org", org, "kind", kind, "err", err)
			} else if held {
				n++
			}
		}
	}
	return n
}

// Start polls the schedule until ctx ends. lister may be nil (only the default organization is visited).
func (s *Service) Start(ctx context.Context, every time.Duration, lister application.OrgLister) {
	if every <= 0 {
		every = time.Minute
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				orgs := []string{s.cfg.OrgID}
				if lister != nil {
					if ids, err := lister.ListOrgIDs(ctx); err == nil {
						orgs = application.WithDefaultOrg(ids, s.cfg.OrgID)
					} else {
						s.cfg.Log.Warn("rituals: list organizations", "err", err)
					}
				}
				s.RunDue(ctx, orgs)
			}
		}
	}()
}

// ---- proposals ----

// AcceptResult is an accepted proposal and what happened to it.
type AcceptResult struct {
	Proposal Proposal `json:"proposal"`
	// Applied is true when the instruction note was added to the agent work profile (a new profile version).
	Applied bool `json:"applied"`
	// ProfileVersion is that new version (0 when nothing was applied).
	ProfileVersion int `json:"profile_version,omitempty"`
}

// AcceptProposal records a person's acceptance. An instruction note is appended to the agent's instructions through
// the work profile (the same path as editing it by hand: versioned, audited, reversible with "restore"); a skill
// suggestion is only recorded (a person creates the skill, which an administrator reviews). Two people cannot both
// accept it (compare-and-set); if the profile edit fails, the proposal goes back to pending.
func (s *Service) AcceptProposal(ctx context.Context, id string) (AcceptResult, error) {
	org, actor, now := s.org(ctx), application.ActorFrom(ctx, "user"), s.now()
	if s.isAgentActor(ctx, actor) {
		return AcceptResult{}, fmt.Errorf("%w: only a person can accept a proposal", domain.ErrForbidden)
	}
	p, err := s.cfg.Store.ResolveProposal(ctx, org, id, ProposalPending, ProposalAccepted, actor, false, now)
	if err != nil {
		return AcceptResult{}, err
	}
	res := AcceptResult{Proposal: p}
	if p.Kind == ProposalInstructionNote && s.cfg.Profiles != nil {
		v, err := s.applyNote(ctx, p)
		if err != nil {
			_, _ = s.cfg.Store.ResolveProposal(context.WithoutCancel(ctx), org, id, ProposalAccepted, ProposalPending, "", false, now)
			return AcceptResult{}, err
		}
		if p, err = s.cfg.Store.ResolveProposal(ctx, org, id, ProposalAccepted, ProposalAccepted, actor, true, now); err != nil {
			return AcceptResult{}, err
		}
		res = AcceptResult{Proposal: p, Applied: true, ProfileVersion: v}
	}
	s.audit(ctx, actor, "ritual.proposal_accepted", "ritual_proposal", p.ID, map[string]any{"agent_id": p.AgentID, "kind": p.Kind,
		"signal": p.Signal, "ritual_id": p.RitualID, "applied": res.Applied, "profile_version": res.ProfileVersion})
	return res, nil
}

// applyNote appends the note to the instructions, keeping creativity, skills and tone as they are.
func (s *Service) applyNote(ctx context.Context, p Proposal) (int, error) {
	cur, err := s.cfg.Profiles.Get(ctx, p.AgentID)
	if err != nil {
		return 0, err
	}
	instr := strings.TrimSpace(cur.Instructions)
	if !strings.Contains(instr, p.Text) {
		if instr != "" {
			instr += "\n\n"
		}
		instr += p.Text
	}
	v, err := s.cfg.Profiles.Update(ctx, p.AgentID, application.ProfileInput{Instructions: instr, Creativity: cur.Creativity, Skills: cur.Skills})
	if err != nil {
		return 0, err
	}
	return v.Version, nil
}

// DismissProposal discards a pending proposal.
func (s *Service) DismissProposal(ctx context.Context, id string) (Proposal, error) {
	actor := application.ActorFrom(ctx, "user")
	if s.isAgentActor(ctx, actor) {
		return Proposal{}, fmt.Errorf("%w: only a person can dismiss a proposal", domain.ErrForbidden)
	}
	p, err := s.cfg.Store.ResolveProposal(ctx, s.org(ctx), id, ProposalPending, ProposalDismissed, actor, false, s.now())
	if err != nil {
		return Proposal{}, err
	}
	s.audit(ctx, actor, "ritual.proposal_dismissed", "ritual_proposal", p.ID, map[string]any{"agent_id": p.AgentID, "kind": p.Kind, "signal": p.Signal})
	return p, nil
}
