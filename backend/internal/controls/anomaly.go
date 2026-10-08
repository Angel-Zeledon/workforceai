package controls

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Anomaly detection v1: four explainable rules over in-memory sliding windows.
// Every detection is an audit entry plus an "anomaly.detected" event that says
// WHICH rule fired, the observed value and the threshold. Detection never
// blocks anything by itself; the optional auto-freeze (default off) activates
// the same freeze a human could (lifting it stays an owner decision).
//
// Honest limits: counters live in process memory, so a restart forgets the
// baseline (the spend and new-domain rules warm up again before firing).

// Anomaly rule identifiers.
const (
	RuleSpendSpike       = "spend_spike"
	RuleToolBurst        = "tool_request_burst"
	RuleRejectedBurst    = "rejected_approvals"
	RuleNewRecipientSpan = "new_recipient_domains"
)

// AnomalySettings is stored in Settings.
type AnomalySettings struct {
	// Disabled switches detection off (owner only; default false = detect).
	Disabled bool `json:"disabled"`
	// AutoFreeze activates the freeze kill switch when an anomaly is detected.
	AutoFreeze bool `json:"auto_freeze"`
}

// AnomalyThresholds are fixed in v1 (documented in 07-seguridad-costos.md).
type AnomalyThresholds struct {
	SpendFactor    float64       // current hour >= factor * trailing hourly average
	SpendFloorUSD  float64       // and at least this much in the hour
	SpendWarmup    int           // hours of observed history before the rule can fire
	BurstCalls     int           // tool requests of one agent within BurstWindow
	BurstWindow    time.Duration //
	RejectedMax    int           // rejected approvals within RejectedWindow
	RejectedWindow time.Duration //
	NewDomainsMax  int           // distinct never-seen recipient domains within DomainWindow
	DomainWindow   time.Duration //
	DomainWarmup   int           // learned domains before the rule can fire
	Cooldown       time.Duration // same rule fires at most once per cooldown
}

// DefaultAnomalyThresholds are conservative: the demo scenario never trips them.
func DefaultAnomalyThresholds() AnomalyThresholds {
	return AnomalyThresholds{SpendFactor: 4, SpendFloorUSD: 2, SpendWarmup: 3, BurstCalls: 40, BurstWindow: time.Minute,
		RejectedMax: 5, RejectedWindow: time.Hour, NewDomainsMax: 3, DomainWindow: time.Hour, DomainWarmup: 5, Cooldown: 15 * time.Minute}
}

type orgDetect struct {
	spend      map[int64]float64 // unix hour -> USD
	firstHour  int64
	bursts     map[string][]time.Time // agent -> request times
	rejections []time.Time
	domains    map[string]bool
	newDomains map[string]time.Time
	lastFired  map[string]time.Time
}

type detector struct {
	mu sync.Mutex
	th AnomalyThresholds
	by map[string]*orgDetect
}

func (d *detector) org(org string) *orgDetect {
	o := d.by[org]
	if o == nil {
		o = &orgDetect{spend: map[int64]float64{}, bursts: map[string][]time.Time{}, domains: map[string]bool{},
			newDomains: map[string]time.Time{}, lastFired: map[string]time.Time{}}
		d.by[org] = o
	}
	return o
}

// SetAnomalyThresholds replaces the thresholds (tests, future configuration).
func (s *Service) SetAnomalyThresholds(th AnomalyThresholds) {
	s.detMu.Lock()
	defer s.detMu.Unlock()
	s.det = &detector{th: th, by: map[string]*orgDetect{}}
}

func (s *Service) detector() *detector {
	s.detMu.Lock()
	defer s.detMu.Unlock()
	if s.det == nil {
		s.det = &detector{th: DefaultAnomalyThresholds(), by: map[string]*orgDetect{}}
	}
	return s.det
}

// hit is a detection awaiting emission.
type hit struct {
	rule    string
	message string
	details map[string]any
}

// fire applies the cooldown; must be called with d.mu held. It returns nil when cooling down.
func (d *detector) fire(o *orgDetect, now time.Time, h hit) *hit {
	if last, ok := o.lastFired[h.rule]; ok && now.Sub(last) < d.th.Cooldown {
		return nil
	}
	o.lastFired[h.rule] = now
	return &h
}

func (s *Service) anomalyEnabled(ctx context.Context, org string) (AnomalySettings, bool) {
	st, err := s.State(ctx, org)
	if err != nil {
		return AnomalySettings{}, false
	}
	return st.Settings.Anomaly, !st.Settings.Anomaly.Disabled
}

// ObserveSpend feeds one cost record (USD) of the organization.
func (s *Service) ObserveSpend(ctx context.Context, org string, usd float64) {
	if usd <= 0 {
		return
	}
	set, ok := s.anomalyEnabled(ctx, org)
	if !ok {
		return
	}
	d, now := s.detector(), s.now()
	hour := now.Unix() / 3600
	d.mu.Lock()
	o := d.org(org)
	if o.firstHour == 0 {
		o.firstHour = hour
	}
	o.spend[hour] += usd
	var sum float64
	for h, v := range o.spend {
		switch {
		case h < hour-24:
			delete(o.spend, h)
		case h < hour:
			sum += v
		}
	}
	var res *hit
	observed := hour - o.firstHour
	if observed >= int64(d.th.SpendWarmup) {
		n := observed
		if n > 24 {
			n = 24
		}
		avg := sum / float64(n)
		cur := o.spend[hour]
		if cur >= d.th.SpendFloorUSD && cur >= d.th.SpendFactor*avg {
			res = d.fire(o, now, hit{RuleSpendSpike,
				fmt.Sprintf("spend this hour (%.2f USD) is at least %.0fx the trailing hourly average (%.2f USD)", cur, d.th.SpendFactor, avg),
				map[string]any{"current_hour_usd": cur, "trailing_hourly_avg_usd": avg, "factor": d.th.SpendFactor, "floor_usd": d.th.SpendFloorUSD}})
		}
	}
	d.mu.Unlock()
	s.raise(ctx, org, set, res)
}

// ObserveToolRequest feeds one tool request of an agent.
func (s *Service) ObserveToolRequest(ctx context.Context, org, agentID string) {
	set, ok := s.anomalyEnabled(ctx, org)
	if !ok {
		return
	}
	d, now := s.detector(), s.now()
	d.mu.Lock()
	o := d.org(org)
	cut := now.Add(-d.th.BurstWindow)
	keep := o.bursts[agentID][:0]
	for _, t := range o.bursts[agentID] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	keep = append(keep, now)
	o.bursts[agentID] = keep
	var res *hit
	if len(keep) >= d.th.BurstCalls {
		res = d.fire(o, now, hit{RuleToolBurst,
			fmt.Sprintf("agent %s made %d tool requests within %s (threshold %d)", agentID, len(keep), d.th.BurstWindow, d.th.BurstCalls),
			map[string]any{"agent_id": agentID, "requests": len(keep), "window_seconds": int(d.th.BurstWindow.Seconds()), "threshold": d.th.BurstCalls}})
	}
	d.mu.Unlock()
	s.raise(ctx, org, set, res)
}

// ObserveApprovalRejected feeds one rejected approval.
func (s *Service) ObserveApprovalRejected(ctx context.Context, org, agentID string) {
	set, ok := s.anomalyEnabled(ctx, org)
	if !ok {
		return
	}
	d, now := s.detector(), s.now()
	d.mu.Lock()
	o := d.org(org)
	cut := now.Add(-d.th.RejectedWindow)
	keep := o.rejections[:0]
	for _, t := range o.rejections {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	o.rejections = append(keep, now)
	var res *hit
	if len(o.rejections) >= d.th.RejectedMax {
		res = d.fire(o, now, hit{RuleRejectedBurst,
			fmt.Sprintf("%d approvals were rejected within %s (threshold %d): the agents keep proposing things people refuse", len(o.rejections), d.th.RejectedWindow, d.th.RejectedMax),
			map[string]any{"rejected": len(o.rejections), "window_seconds": int(d.th.RejectedWindow.Seconds()), "threshold": d.th.RejectedMax, "last_agent_id": agentID}})
	}
	d.mu.Unlock()
	s.raise(ctx, org, set, res)
}

// ObserveRecipients feeds the recipient addresses of an outbound request.
func (s *Service) ObserveRecipients(ctx context.Context, org string, addrs []string) {
	set, ok := s.anomalyEnabled(ctx, org)
	if !ok || len(addrs) == 0 {
		return
	}
	d, now := s.detector(), s.now()
	d.mu.Lock()
	o := d.org(org)
	for _, a := range addrs {
		i := strings.LastIndex(a, "@")
		if i < 0 || i == len(a)-1 {
			continue
		}
		dom := strings.ToLower(strings.TrimSpace(a[i+1:]))
		if o.domains[dom] {
			continue
		}
		if len(o.domains) >= d.th.DomainWarmup {
			o.newDomains[dom] = now
		}
		o.domains[dom] = true
	}
	cut := now.Add(-d.th.DomainWindow)
	var recent []string
	for dom, t := range o.newDomains {
		if t.After(cut) {
			recent = append(recent, dom)
		} else {
			delete(o.newDomains, dom)
		}
	}
	var res *hit
	if len(recent) >= d.th.NewDomainsMax {
		res = d.fire(o, now, hit{RuleNewRecipientSpan,
			fmt.Sprintf("%d recipient domains never contacted before within %s (threshold %d)", len(recent), d.th.DomainWindow, d.th.NewDomainsMax),
			map[string]any{"domains": recent, "window_seconds": int(d.th.DomainWindow.Seconds()), "threshold": d.th.NewDomainsMax}})
	}
	d.mu.Unlock()
	s.raise(ctx, org, set, res)
}

// raise audits and emits the detection; with auto-freeze on it freezes the org.
func (s *Service) raise(ctx context.Context, org string, set AnomalySettings, h *hit) {
	if h == nil {
		return
	}
	p := map[string]any{"rule": h.rule, "explanation": h.message, "auto_freeze": set.AutoFreeze}
	for k, v := range h.details {
		p[k] = v
	}
	s.Audit(ctx, org, "system:anomaly", "anomaly.detected", "org", org, p)
	s.Emit(ctx, org, "anomaly.detected", p)
	if set.AutoFreeze {
		if st, err := s.State(ctx, org); err == nil && st.KillSwitchLevel == LevelNone {
			_, _ = s.KillSwitch(ctx, org, LevelFreeze, "anomaly detected: "+h.rule, "system:anomaly")
		}
	}
}

// SetAnomalySettings updates anomaly detection settings (audited). Disabling
// detection weakens a safety net: the API restricts it to owners.
func (s *Service) SetAnomalySettings(ctx context.Context, org string, set AnomalySettings, actor string) (OrgState, error) {
	s.mu.Lock()
	st, err := s.Store.GetOrg(ctx, org)
	if err != nil {
		s.mu.Unlock()
		return OrgState{}, err
	}
	norm(&st)
	st.Settings.Anomaly = set
	if err := s.Store.PutOrg(ctx, org, st); err != nil {
		s.mu.Unlock()
		return OrgState{}, err
	}
	s.mu.Unlock()
	s.changed(ctx, org, actor, "control.anomaly_settings", st, map[string]any{"anomaly": set})
	return st, nil
}
