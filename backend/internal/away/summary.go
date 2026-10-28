package away

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
)

// Version of the summary method (sections, ranking and caps). Bump it when the output changes meaning.
const Version = 1

// Section keys, in the order the summary lists them.
const (
	SectionAlerts      = "alerts"      // security alerts, anomalies, kill switch / read-only, budget caps, connections
	SectionProblems    = "problems"    // failed requests, failed and blocked tasks
	SectionDone        = "done"        // completed requests (with their report)
	SectionSuggestions = "suggestions" // proactive suggestions (B2) raised by agents
	SectionDecided     = "decided"     // approvals resolved during the absence (by people or automatically)
)

// Item kinds. The frontend renders each kind with its own i18n string; Title and Detail are data (task, request
// or approval titles), never UI text.
const (
	KindApproval         = "approval"
	KindAlert            = "alert"
	KindTaskFailed       = "task_failed"
	KindTaskBlocked      = "task_blocked"
	KindRequestFailed    = "request_failed"
	KindRequestCompleted = "request_completed"
	KindInitiative       = "initiative"
	KindDecision         = "decision"
)

// Severities, highest first.
const (
	SevHigh   = "high"
	SevMedium = "medium"
	SevLow    = "low"
	SevInfo   = "info"
)

// Caps of the output: a 60-second read, not an audit export. Totals are always exact.
const (
	MaxActNow       = 5
	MaxSectionItems = 5
	MaxAgents       = 6
	MaxFrames       = 400
)

// Event types the summary reads from the event log. The controls, connections and gateway types are plain strings
// (their packages emit them through the Recorder like any other event).
const (
	evAnomaly        = "anomaly.detected"
	evControl        = "control.changed"
	evAgentControl   = "agent.control_changed"
	evSecurity       = "security.alert"
	evConnStatus     = "connection.status_changed"
	evConnRevoked    = "connection.revoked"
	evRequestFailure = "request.status.failed" // audit action written by the orchestrator when a request fails
)

// NotableTypes are the event types listed item by item (the rest are only counted).
var NotableTypes = []string{
	domain.EvRequestCompleted, domain.EvTaskFailed, domain.EvTaskBlocked, domain.EvApprovalResolved, domain.EvInitiativeRaised,
	domain.EvBudgetExceeded, domain.EvBudgetWarning, evAnomaly, evControl, evAgentControl, evSecurity, evConnStatus, evConnRevoked,
}

// Facts are the raw inputs of one summary, gathered by the service from the stores.
type Facts struct {
	Agents []domain.Agent
	// Pending are the approvals pending right now (whenever they were created).
	Pending []domain.Approval
	// Counts are the events of the window by type (exact).
	Counts map[string]int
	// Notable are the events of NotableTypes in the window, newest first (capped by the store).
	Notable []domain.Event
	// Completed are task.completed events of the window (per-agent counts; capped by the store).
	Completed []domain.Event
	// States are the agent.state_changed events of the window, oldest first (capped by the store).
	States []domain.Event
	// FailedRequests are the audit entries of requests that failed in the window, newest first.
	FailedRequests []domain.AuditLog
	// Requests are the requests referenced by the items, by id (titles only).
	Requests map[string]domain.Request
	// Spend is the cost ledger of the window per agent.
	Spend []domain.SpendRow
}

// Viewer is who asks: approvals are "waiting for you" only when this person may decide them.
type Viewer struct {
	UserID        string
	Role          string // organization role; the demo without auth acts as owner
	CanDecide     bool   // has approvals:decide
	ReadApprovals bool   // has approvals:read (otherwise approvals are left out entirely)
}

// Item is one line of the summary: what happened and what it points to.
type Item struct {
	Kind string `json:"kind"`
	// Ref and RefID say what to open: approval | task | request | report | initiative | connection | agent | org.
	Ref      string    `json:"ref"`
	RefID    string    `json:"ref_id"`
	Title    string    `json:"title"`
	Detail   string    `json:"detail,omitempty"`
	Code     string    `json:"code,omitempty"` // machine code for the i18n text (alert kind, decision, reason)
	AgentID  string    `json:"agent_id,omitempty"`
	Severity string    `json:"severity"`
	At       time.Time `json:"at"`
	// New marks a pending approval created during the absence.
	New bool `json:"new,omitempty"`
	// Repeats counts identical events folded into this item (same task blocked twice, same alert again).
	Repeats int    `json:"repeats,omitempty"`
	Risk    string `json:"risk,omitempty"`
	// RequiredApprovals > 1 is a double approval.
	RequiredApprovals int `json:"required_approvals,omitempty"`
}

// Section is a ranked group of items; Total counts every item before the cap.
type Section struct {
	Key   string `json:"key"`
	Total int    `json:"total"`
	Items []Item `json:"items"`
}

// Counts are the headline numbers (exact, never capped).
type Counts struct {
	RequestsCompleted     int `json:"requests_completed"`
	RequestsFailed        int `json:"requests_failed"`
	TasksCompleted        int `json:"tasks_completed"`
	TasksFailed           int `json:"tasks_failed"`
	TasksBlocked          int `json:"tasks_blocked"`
	ApprovalsWaiting      int `json:"approvals_waiting"`       // pending, and this person may decide them
	ApprovalsWaitingNew   int `json:"approvals_waiting_new"`   // ... of which created during the absence
	ApprovalsWaitingOther int `json:"approvals_waiting_other"` // pending for someone else (role, self-approval, already approved)
	ApprovalsDecided      int `json:"approvals_decided"`
	Initiatives           int `json:"initiatives"`
	Alerts                int `json:"alerts"`
	Messages              int `json:"messages"`
}

// Spend is what the absence cost, from the cost ledger.
type Spend struct {
	USD   float64      `json:"usd"`
	Calls int          `json:"calls"`
	Top   []AgentSpend `json:"top"`
}

// AgentSpend is one agent's share.
type AgentSpend struct {
	AgentID string  `json:"agent_id"`
	USD     float64 `json:"usd"`
	Calls   int     `json:"calls"`
}

// AgentLine is what one agent did during the absence.
type AgentLine struct {
	AgentID        string  `json:"agent_id"`
	Name           string  `json:"name"`
	TasksCompleted int     `json:"tasks_completed"`
	TasksFailed    int     `json:"tasks_failed"`
	USD            float64 `json:"usd"`
	State          string  `json:"state"` // current state
}

// Frame is one agent state change of the timelapse.
type Frame struct {
	At       time.Time `json:"at"`
	AgentID  string    `json:"agent_id"`
	State    string    `json:"state"`
	Activity string    `json:"activity,omitempty"`
}

// Timelapse replays the agent state changes of the absence (from the event log), sampled to MaxFrames.
type Timelapse struct {
	Available bool    `json:"available"`
	Total     int     `json:"total"`   // state changes in the window
	Sampled   bool    `json:"sampled"` // frames is a subset
	Frames    []Frame `json:"frames"`
}

// Summary is the response of GET /away-summary.
type Summary struct {
	Version     int        `json:"version"`
	Method      string     `json:"method"` // always "deterministic": no model was called
	GeneratedAt time.Time  `json:"generated_at"`
	FirstVisit  bool       `json:"first_visit"`
	Since       *time.Time `json:"since"`
	Until       *time.Time `json:"until"`
	AwayMinutes int        `json:"away_minutes"`
	MinHours    float64    `json:"min_hours"`
	// Clipped: the absence was longer than the lookback limit and Since was moved forward.
	Clipped   bool `json:"clipped"`
	Dismissed bool `json:"dismissed"`
	// Quiet: nothing happened and nothing waits for this person.
	Quiet bool `json:"quiet"`
	// Show: the frontend should open the summary on its own (away long enough, not dismissed, not quiet).
	Show      bool        `json:"show"`
	Counts    Counts      `json:"counts"`
	Spend     Spend       `json:"spend"`
	ActNow    []Item      `json:"act_now"`
	Sections  []Section   `json:"sections"`
	Agents    []AgentLine `json:"agents"`
	Timelapse Timelapse   `json:"timelapse"`
}

// emptySummary has every list non-nil (the JSON never carries null lists).
func emptySummary(now time.Time, minHours float64) Summary {
	return Summary{Version: Version, Method: "deterministic", GeneratedAt: now, MinHours: minHours,
		ActNow: []Item{}, Sections: []Section{}, Agents: []AgentLine{}, Spend: Spend{Top: []AgentSpend{}},
		Timelapse: Timelapse{Frames: []Frame{}}}
}

// CanDecide reports whether viewer may approve ap right now: the same rules as Approvals.Decide (role, no
// self-approval, a different person for each approval of a double approval). Agents never decide.
func CanDecide(ap domain.Approval, v Viewer) bool {
	if ap.Status != domain.ApprovalPending || !v.CanDecide || v.UserID == "" || v.UserID == ap.AgentID {
		return false
	}
	r := policy.RoleRank(v.Role)
	if r == 0 || r < policy.RoleRank(ap.RequiredRole) {
		return false
	}
	if ap.NoSelfApproval && ap.RequestedBy != "" && ap.RequestedBy == v.UserID {
		return false
	}
	for _, d := range ap.Decisions {
		if d.By == v.UserID {
			return false
		}
	}
	return true
}

func riskRank(r string) int {
	switch strings.ToLower(r) {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	}
	return 0
}

func sevRank(s string) int {
	switch s {
	case SevHigh:
		return 4
	case SevMedium:
		return 3
	case SevLow:
		return 2
	}
	return 1
}

// payload decodes an event payload into a generic map (memory events carry Go values, Postgres events raw JSON).
func payload(e domain.Event) map[string]any {
	var raw []byte
	switch p := e.Payload.(type) {
	case nil:
		return map[string]any{}
	case json.RawMessage:
		raw = p
	case []byte:
		raw = p
	default:
		b, err := json.Marshal(p)
		if err != nil {
			return map[string]any{}
		}
		raw = b
	}
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	return m
}

func str(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	switch v := m[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func sub(m map[string]any, k string) map[string]any {
	if v, ok := m[k].(map[string]any); ok {
		return v
	}
	return nil
}

// clip keeps a data string short and on one line.
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// itemOf turns a notable event into an item, or ok=false when the event is not worth a line (a connection that
// became active again, an agent resumed, a budget warning folded under the exceeded one).
func itemOf(e domain.Event, requests map[string]domain.Request) (section string, it Item, ok bool) {
	p := payload(e)
	it = Item{At: e.TS, AgentID: e.AgentID}
	switch e.Type {
	case domain.EvRequestCompleted:
		id := str(p, "request_id")
		it.Kind, it.Ref, it.RefID, it.Severity = KindRequestCompleted, "request", id, SevInfo
		if rep := str(p, "report_id"); rep != "" {
			it.Ref, it.RefID, it.Detail = "report", rep, id
		}
		it.Title = clip(requests[id].Text, 140)
		return SectionDone, it, id != ""
	case domain.EvTaskFailed, domain.EvTaskBlocked:
		t := sub(p, "task")
		it.Ref, it.RefID, it.Title = "task", str(t, "id"), clip(str(t, "title"), 140)
		if it.AgentID == "" {
			it.AgentID = str(t, "agent_id")
		}
		if e.Type == domain.EvTaskFailed {
			it.Kind, it.Severity, it.Detail = KindTaskFailed, SevMedium, clip(str(p, "error"), 200)
		} else {
			it.Kind, it.Severity, it.Code = KindTaskBlocked, SevLow, clip(str(p, "reason"), 60)
		}
		return SectionProblems, it, it.RefID != ""
	case domain.EvApprovalResolved:
		a := sub(p, "approval")
		it.Kind, it.Ref, it.RefID, it.Title, it.Severity = KindDecision, "approval", str(a, "id"), clip(str(a, "title"), 140), SevInfo
		it.Code, it.Risk = str(a, "status"), str(a, "risk")
		note := strings.ToLower(str(a, "note"))
		switch {
		case strings.HasPrefix(note, "sin respuesta"):
			it.Code = "expired" // nobody answered in time
		case strings.HasPrefix(note, "superseded:"):
			it.Code = "superseded" // the server restarted
		}
		if ds, _ := a["decisions"].([]any); len(ds) > 0 {
			if d, _ := ds[len(ds)-1].(map[string]any); d != nil {
				it.Detail = str(d, "by")
			}
		}
		if it.AgentID == "" {
			it.AgentID = str(a, "agent_id")
		}
		return SectionDecided, it, it.RefID != ""
	case domain.EvInitiativeRaised:
		it.Kind, it.Ref, it.RefID, it.Title, it.Severity, it.Code = KindInitiative, "initiative", str(p, "initiative_id"), clip(str(p, "title"), 140), SevInfo, str(p, "signal")
		return SectionSuggestions, it, it.RefID != ""
	case domain.EvBudgetExceeded, domain.EvBudgetWarning:
		it.Kind, it.Ref, it.RefID = KindAlert, "org", str(p, "scope_id")
		it.Code = "budget_warning:" + str(p, "scope")
		it.Severity = SevMedium
		if e.Type == domain.EvBudgetExceeded {
			it.Code, it.Severity = "budget_exceeded:"+str(p, "scope"), SevHigh
		}
		it.Detail = str(p, "cap_usd")
		return SectionAlerts, it, true
	case evAnomaly:
		it.Kind, it.Ref, it.Code, it.Severity, it.Detail = KindAlert, "org", "anomaly:"+str(p, "rule"), SevHigh, clip(str(p, "explanation"), 200)
		return SectionAlerts, it, true
	case evSecurity:
		it.Kind, it.Ref, it.Code, it.Severity = KindAlert, "agent", "security:"+str(p, "kind"), SevHigh
		it.RefID, it.Detail = str(p, "agent_id"), str(p, "tool")
		if it.AgentID == "" {
			it.AgentID = it.RefID
		}
		return SectionAlerts, it, true
	case evControl:
		it.Kind, it.Ref, it.Detail = KindAlert, "org", str(p, "by")
		switch lvl, mode := str(p, "kill_switch_level"), str(p, "mode"); {
		case lvl != "" && lvl != "none":
			it.Code, it.Severity = "kill_switch:"+lvl, SevHigh
		case mode == "read_only":
			it.Code, it.Severity = "read_only", SevMedium
		default:
			it.Code, it.Severity = "controls_normal", SevLow
		}
		return SectionAlerts, it, true
	case evAgentControl:
		if str(p, "control") != "paused" {
			return "", it, false
		}
		it.Kind, it.Ref, it.RefID, it.Code, it.Severity, it.Detail = KindAlert, "agent", str(p, "agent_id"), "agent_paused", SevMedium, str(p, "by")
		it.AgentID = it.RefID
		return SectionAlerts, it, true
	case evConnStatus, evConnRevoked:
		to := str(p, "to")
		if e.Type == evConnRevoked {
			to = "revoked"
		}
		switch to {
		case "needs_reauth", "error", "suspended", "revoked", "expired":
		default:
			return "", it, false // pending -> active and the like are good news, not alerts
		}
		it.Kind, it.Ref, it.RefID, it.Code, it.Severity = KindAlert, "connection", str(p, "connection_id"), "connection:"+to, SevMedium
		return SectionAlerts, it, true
	}
	return "", it, false
}

// foldKey says which items are the same thing repeated (kept once, newest, with Repeats).
func foldKey(section string, it Item) string {
	switch section {
	case SectionAlerts:
		return it.Code + "|" + it.RefID
	case SectionProblems:
		return it.Kind + "|" + it.RefID
	}
	return it.Kind + "|" + it.RefID + "|" + it.At.String()
}

func rankItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		if a, b := sevRank(items[i].Severity), sevRank(items[j].Severity); a != b {
			return a > b
		}
		if !items[i].At.Equal(items[j].At) {
			return items[i].At.After(items[j].At)
		}
		return items[i].RefID < items[j].RefID
	})
}

// Build assembles the summary of [from, to) from the facts. It is pure: same facts, same summary.
func Build(f Facts, v Viewer, from, to, now time.Time, minHours float64) Summary {
	s := emptySummary(now, minHours)
	s.Since, s.Until = &from, &to
	s.AwayMinutes = int(to.Sub(from) / time.Minute)
	c := f.Counts
	s.Counts = Counts{
		RequestsCompleted: c[domain.EvRequestCompleted], RequestsFailed: len(f.FailedRequests),
		TasksCompleted: c[domain.EvTaskCompleted], TasksFailed: c[domain.EvTaskFailed], TasksBlocked: c[domain.EvTaskBlocked],
		ApprovalsDecided: c[domain.EvApprovalResolved], Initiatives: c[domain.EvInitiativeRaised], Messages: c[domain.EvMessageSent],
	}
	if !v.ReadApprovals {
		s.Counts.ApprovalsDecided = 0
	}

	// Act now: the approvals this person can decide, riskiest and oldest first.
	if v.ReadApprovals {
		mine := []domain.Approval{}
		for _, ap := range f.Pending {
			if ap.Status != domain.ApprovalPending {
				continue
			}
			if CanDecide(ap, v) {
				mine = append(mine, ap)
				if !ap.CreatedAt.Before(from) {
					s.Counts.ApprovalsWaitingNew++
				}
			} else {
				s.Counts.ApprovalsWaitingOther++
			}
		}
		s.Counts.ApprovalsWaiting = len(mine)
		sort.SliceStable(mine, func(i, j int) bool {
			if a, b := riskRank(mine[i].Risk), riskRank(mine[j].Risk); a != b {
				return a > b
			}
			if mine[i].RequiredApprovals != mine[j].RequiredApprovals {
				return mine[i].RequiredApprovals > mine[j].RequiredApprovals
			}
			if !mine[i].CreatedAt.Equal(mine[j].CreatedAt) {
				return mine[i].CreatedAt.Before(mine[j].CreatedAt)
			}
			return mine[i].ID < mine[j].ID
		})
		for _, ap := range mine {
			if len(s.ActNow) == MaxActNow {
				break
			}
			sev := SevMedium
			if riskRank(ap.Risk) == 3 || ap.RequiredApprovals >= 2 {
				sev = SevHigh
			}
			s.ActNow = append(s.ActNow, Item{Kind: KindApproval, Ref: "approval", RefID: ap.ID, Title: clip(ap.Title, 140), Code: ap.Action,
				AgentID: ap.AgentID, Severity: sev, At: ap.CreatedAt, New: !ap.CreatedAt.Before(from), Risk: ap.Risk, RequiredApprovals: ap.RequiredApprovals})
		}
	}

	// Sections from the notable events, folded and ranked.
	groups := map[string][]Item{}
	seen := map[string]int{}      // fold key -> index in its group
	for _, e := range f.Notable { // newest first: the first of a fold is the most recent
		sec, it, ok := itemOf(e, f.Requests)
		if !ok || (sec == SectionDecided && !v.ReadApprovals) {
			continue
		}
		k := sec + "|" + foldKey(sec, it)
		if i, dup := seen[k]; dup {
			groups[sec][i].Repeats++
			continue
		}
		seen[k] = len(groups[sec])
		groups[sec] = append(groups[sec], it)
	}
	for _, a := range f.FailedRequests {
		it := Item{Kind: KindRequestFailed, Ref: "request", RefID: a.EntityID, Title: clip(f.Requests[a.EntityID].Text, 140), Severity: SevHigh, At: a.TS}
		k := SectionProblems + "|" + foldKey(SectionProblems, it)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = len(groups[SectionProblems])
		groups[SectionProblems] = append(groups[SectionProblems], it)
	}
	s.Counts.Alerts = len(groups[SectionAlerts])
	for _, key := range []string{SectionAlerts, SectionProblems, SectionDone, SectionSuggestions, SectionDecided} {
		items := groups[key]
		if len(items) == 0 {
			continue
		}
		rankItems(items)
		total := len(items)
		if len(items) > MaxSectionItems {
			items = items[:MaxSectionItems]
		}
		s.Sections = append(s.Sections, Section{Key: key, Total: total, Items: items})
	}

	// Spend and the agents that worked.
	names := map[string]domain.Agent{}
	for _, a := range f.Agents {
		names[a.ID] = a
	}
	lines := map[string]*AgentLine{}
	line := func(id string) *AgentLine {
		if id == "" {
			return nil
		}
		l := lines[id]
		if l == nil {
			a := names[id]
			l = &AgentLine{AgentID: id, Name: a.Name, State: string(a.State)}
			lines[id] = l
		}
		return l
	}
	for _, r := range f.Spend {
		s.Spend.USD += r.CostUSD
		s.Spend.Calls += r.Calls
		if r.AgentID != "" && len(s.Spend.Top) < MaxAgents {
			s.Spend.Top = append(s.Spend.Top, AgentSpend{AgentID: r.AgentID, USD: round4(r.CostUSD), Calls: r.Calls})
		}
		if l := line(r.AgentID); l != nil {
			l.USD = round4(l.USD + r.CostUSD)
		}
	}
	s.Spend.USD = round4(s.Spend.USD)
	for _, e := range f.Completed {
		id := e.AgentID
		if id == "" {
			id = str(sub(payload(e), "task"), "agent_id")
		}
		if l := line(id); l != nil {
			l.TasksCompleted++
		}
	}
	for _, e := range f.Notable {
		if e.Type == domain.EvTaskFailed {
			if l := line(e.AgentID); l != nil {
				l.TasksFailed++
			}
		}
	}
	for _, l := range lines {
		if l.TasksCompleted+l.TasksFailed > 0 || l.USD > 0 {
			s.Agents = append(s.Agents, *l)
		}
	}
	sort.Slice(s.Agents, func(i, j int) bool {
		a, b := s.Agents[i], s.Agents[j]
		if a.TasksCompleted != b.TasksCompleted {
			return a.TasksCompleted > b.TasksCompleted
		}
		if a.USD != b.USD {
			return a.USD > b.USD
		}
		return a.AgentID < b.AgentID
	})
	if len(s.Agents) > MaxAgents {
		s.Agents = s.Agents[:MaxAgents]
	}

	s.Timelapse = buildTimelapse(f.States, c[domain.EvAgentState])
	s.Quiet = s.Counts.ApprovalsWaiting == 0 && len(s.Sections) == 0 && s.Counts.TasksCompleted == 0 && s.Spend.USD == 0
	return s
}

func round4(x float64) float64 { return math.Round(x*10000) / 10000 }

// buildTimelapse turns the state changes (oldest first) into frames, sampled evenly to MaxFrames while always
// keeping the last change of every agent, so the replay ends where the office really is.
func buildTimelapse(states []domain.Event, total int) Timelapse {
	frames := make([]Frame, 0, len(states))
	for _, e := range states {
		p := payload(e)
		id := e.AgentID
		if id == "" {
			id = str(p, "agent_id")
		}
		st := str(p, "state")
		if id == "" || st == "" {
			continue
		}
		frames = append(frames, Frame{At: e.TS, AgentID: id, State: st, Activity: clip(str(p, "activity"), 80)})
	}
	if total < len(frames) {
		total = len(frames)
	}
	tl := Timelapse{Available: len(frames) > 0, Total: total, Sampled: total > len(frames), Frames: frames}
	if len(frames) <= MaxFrames {
		return tl
	}
	last := map[string]int{}
	for i, f := range frames {
		last[f.AgentID] = i
	}
	keep := map[int]bool{}
	for _, i := range last {
		keep[i] = true
	}
	step := float64(len(frames)) / float64(MaxFrames-len(last))
	for x := 0.0; int(x) < len(frames) && len(keep) < MaxFrames; x += step {
		keep[int(x)] = true
	}
	out := make([]Frame, 0, len(keep))
	for i, f := range frames {
		if keep[i] {
			out = append(out, f)
		}
	}
	tl.Frames, tl.Sampled = out, true
	return tl
}
