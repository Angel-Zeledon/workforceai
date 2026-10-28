package application

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"aiworkforce/backend/internal/domain"
)

// Metric signals of proactive employees (docs/architecture/initiative.md, "Señales de métricas").
//
// Besides a failed task, the platform looks at data it already stores and, when a number crosses a threshold the
// organization chose, an employee raises a suggestion ("the margin dropped 3 points, shall I look into it?"). The
// detection is deterministic: plain arithmetic over stored data, no model call. Every candidate goes through
// InitiativeService.Raise, so the initiative budget (daily, monthly, per agent), the on/off switch and the dedupe
// window apply exactly as for a failed task; accepting it submits a normal request. Nothing runs on its own.
//
// The four signals:
//   - metric_change: the last value of a margin / sales / cash column in a sheet or table artifact moved beyond a
//     threshold against the previous row (the previous period).
//   - spend_pace: at the pace of this calendar month, AI spend will exceed what the month had available under the
//     organization cap.
//   - approval_aging: approvals have been waiting for a human longer than a threshold.
//   - quality_drop: the advisory quality score (B11) of an agent fell against the previous window.

// Signal kinds raised by the scanner.
const (
	SignalMetricChange  = "metric_change"
	SignalSpendPace     = "spend_pace"
	SignalApprovalAging = "approval_aging"
	SignalQualityDrop   = "quality_drop"
)

// Metric families recognized in sheet and table headers (ClassifyMetric).
const (
	MetricMargin = "margin"
	MetricSales  = "sales"
	MetricCash   = "cash"
)

// MetricChangeSignal: a margin moved MarginPoints percentage points, or sales / cash moved ChangePct percent.
type MetricChangeSignal struct {
	Enabled      bool    `json:"enabled"`
	ChangePct    float64 `json:"change_pct"`
	MarginPoints float64 `json:"margin_points"`
}

// SpendPaceSignal: the month's spend projected to its last day reaches PacePct % of what the month had available,
// evaluated once MinDays days of the month have passed (earlier projections are noise).
type SpendPaceSignal struct {
	Enabled bool `json:"enabled"`
	PacePct int  `json:"pace_pct"`
	MinDays int  `json:"min_days"`
}

// ApprovalAgingSignal: at least MinCount approvals have been pending for MinAgeHours hours or more.
type ApprovalAgingSignal struct {
	Enabled     bool `json:"enabled"`
	MinAgeHours int  `json:"min_age_hours"`
	MinCount    int  `json:"min_count"`
}

// QualityDropSignal: an agent's quality score is DropPoints points (or more) below the previous window.
type QualityDropSignal struct {
	Enabled    bool `json:"enabled"`
	DropPoints int  `json:"drop_points"`
}

// SignalSettings is the per-organization configuration of the metric signals.
type SignalSettings struct {
	MetricChange  MetricChangeSignal  `json:"metric_change"`
	SpendPace     SpendPaceSignal     `json:"spend_pace"`
	ApprovalAging ApprovalAgingSignal `json:"approval_aging"`
	QualityDrop   QualityDropSignal   `json:"quality_drop"`
}

// Defaults and accepted ranges of the thresholds.
const (
	DefaultMetricChangePct    = 10
	DefaultMetricMarginPoints = 3
	DefaultSpendPacePct       = 100
	DefaultSpendMinDays       = 3
	DefaultApprovalAgeHours   = 24
	DefaultApprovalMinCount   = 1
	DefaultQualityDropPoints  = 10

	minChangePct, maxChangePct       = 1.0, 1000.0
	minMarginPoints, maxMarginPoints = 0.5, 100.0
	minPacePct, maxPacePct           = 50, 500
	minSpendDays, maxSpendDays       = 1, 27
	minAgeHours, maxAgeHours         = 1, 720
	minApprovals, maxApprovals       = 1, 100
	minQualityDrop, maxQualityDrop   = 1, 100

	// maxMetricCandidates bounds the metric suggestions considered per scan (largest moves first).
	maxMetricCandidates = 5
	// approvalsListed is how many aged approvals the request text names.
	approvalsListed = 5
)

// signalDedupe is how long the same signal key is not suggested again. Metric and spend keys include the period
// (row / month), so a new period can be suggested as soon as it appears.
var signalDedupe = map[string]time.Duration{
	SignalMetricChange:  7 * 24 * time.Hour,
	SignalSpendPace:     7 * 24 * time.Hour,
	SignalApprovalAging: 24 * time.Hour,
	SignalQualityDrop:   7 * 24 * time.Hour,
}

// SignalDedupe returns the dedupe window of a signal (0 = the service default).
func SignalDedupe(signal string) time.Duration { return signalDedupe[signal] }

// DefaultSignalSettings: every signal on with conservative thresholds; the initiative budget still bounds how many
// suggestions reach people.
func DefaultSignalSettings() SignalSettings {
	return SignalSettings{
		MetricChange:  MetricChangeSignal{Enabled: true, ChangePct: DefaultMetricChangePct, MarginPoints: DefaultMetricMarginPoints},
		SpendPace:     SpendPaceSignal{Enabled: true, PacePct: DefaultSpendPacePct, MinDays: DefaultSpendMinDays},
		ApprovalAging: ApprovalAgingSignal{Enabled: true, MinAgeHours: DefaultApprovalAgeHours, MinCount: DefaultApprovalMinCount},
		QualityDrop:   QualityDropSignal{Enabled: true, DropPoints: DefaultQualityDropPoints},
	}
}

// Validate checks every threshold (also of disabled signals, so turning one on never activates a broken value).
func (s SignalSettings) Validate() error {
	bad := func(field string, lo, hi any) error {
		return fmt.Errorf("%w: signals.%s must be between %v and %v", domain.ErrInvalid, field, lo, hi)
	}
	m := s.MetricChange
	switch {
	case math.IsNaN(m.ChangePct) || m.ChangePct < minChangePct || m.ChangePct > maxChangePct:
		return bad("metric_change.change_pct", minChangePct, maxChangePct)
	case math.IsNaN(m.MarginPoints) || m.MarginPoints < minMarginPoints || m.MarginPoints > maxMarginPoints:
		return bad("metric_change.margin_points", minMarginPoints, maxMarginPoints)
	case s.SpendPace.PacePct < minPacePct || s.SpendPace.PacePct > maxPacePct:
		return bad("spend_pace.pace_pct", minPacePct, maxPacePct)
	case s.SpendPace.MinDays < minSpendDays || s.SpendPace.MinDays > maxSpendDays:
		return bad("spend_pace.min_days", minSpendDays, maxSpendDays)
	case s.ApprovalAging.MinAgeHours < minAgeHours || s.ApprovalAging.MinAgeHours > maxAgeHours:
		return bad("approval_aging.min_age_hours", minAgeHours, maxAgeHours)
	case s.ApprovalAging.MinCount < minApprovals || s.ApprovalAging.MinCount > maxApprovals:
		return bad("approval_aging.min_count", minApprovals, maxApprovals)
	case s.QualityDrop.DropPoints < minQualityDrop || s.QualityDrop.DropPoints > maxQualityDrop:
		return bad("quality_drop.drop_points", minQualityDrop, maxQualityDrop)
	}
	return nil
}

// AuditDetails is the flat summary written to the audit trail when the settings change.
func (s *SignalSettings) AuditDetails() map[string]any {
	if s == nil {
		return nil
	}
	return map[string]any{
		"metric_change": s.MetricChange.Enabled, "metric_change_pct": s.MetricChange.ChangePct, "metric_margin_points": s.MetricChange.MarginPoints,
		"spend_pace": s.SpendPace.Enabled, "spend_pace_pct": s.SpendPace.PacePct, "spend_min_days": s.SpendPace.MinDays,
		"approval_aging": s.ApprovalAging.Enabled, "approval_min_age_hours": s.ApprovalAging.MinAgeHours, "approval_min_count": s.ApprovalAging.MinCount,
		"quality_drop": s.QualityDrop.Enabled, "quality_drop_points": s.QualityDrop.DropPoints,
	}
}

// MetricSeries is the tail of one numeric column of a sheet or table artifact: its last two values, in row order.
// The artifacts package extracts it (artifacts.Service.MetricSeries); the scanner only does the arithmetic.
type MetricSeries struct {
	ArtifactID    string
	ArtifactTitle string
	Sheet         string // sheet name; "" for a table
	Column        string // the header as written
	Metric        string // MetricMargin | MetricSales | MetricCash
	// Ref identifies the cell / row of the latest value; it is part of the dedupe key, so a new period is a new key.
	Ref               string
	Period            string // label of the latest row ("" when the row has none)
	Previous, Current float64
	CreatorAgent      string // agent that created the artifact, if one did
}

// QualityChange is the quality score of an agent in the current and the previous window (both scored).
type QualityChange struct {
	AgentID         string
	Score, Previous int
}

var (
	metricExclude = map[string]bool{"cost": true, "costs": true, "costo": true, "costos": true, "coste": true, "costes": true,
		"custo": true, "custos": true, "gasto": true, "gastos": true, "expense": true, "expenses": true, "despesa": true, "despesas": true}
	metricWords = []struct {
		metric string
		words  []string
	}{
		{MetricMargin, []string{"margen", "margenes", "margin", "margins", "margem", "margens"}},
		{MetricSales, []string{"ventas", "venta", "sales", "revenue", "revenues", "ingresos", "vendas", "receita", "receitas", "facturacion", "faturamento"}},
		{MetricCash, []string{"caja", "cash", "caixa", "efectivo", "tesoreria", "liquidez"}},
	}
	foldAccents = strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n", "ã", "a", "õ", "o", "â", "a",
		"ê", "e", "ô", "o", "ç", "c", "à", "a")
)

// ClassifyMetric maps a column header to a metric family ("" when it is not one). Headers that name a cost
// ("costo de ventas", "cost of sales") are not sales.
func ClassifyMetric(label string) string {
	words := strings.FieldsFunc(foldAccents.Replace(strings.ToLower(label)), func(r rune) bool { return !unicode.IsLetter(r) })
	for _, w := range words {
		if metricExclude[w] {
			return ""
		}
	}
	for _, f := range metricWords {
		for _, w := range words {
			for _, k := range f.words {
				if w == k {
					return f.metric
				}
			}
		}
	}
	return ""
}

// InitiativeScanner turns the metric signals into suggestions. Every source is optional; a nil one is skipped.
type InitiativeScanner struct {
	Svc  *InitiativeService
	Core Store // agents, pending approvals, usage ledger, lifetime cost
	// OrgCap is the organization cap in force (Budget.OrgCap); 0 = none, and spend_pace is skipped.
	OrgCap func(ctx context.Context) float64
	// Series lists the numeric series of recent sheet / table artifacts.
	Series func(ctx context.Context) ([]MetricSeries, error)
	// Quality lists the agents whose score can be compared with the previous window.
	Quality func(ctx context.Context) ([]QualityChange, error)
	// Allowed reports whether an agent may be bothered now (kill switch off, agent not paused). nil = always.
	Allowed func(ctx context.Context, org, agentID string) bool
	// Locale is the organization language of the stored text and the request (es | en | pt; default es).
	Locale func(ctx context.Context) string
	Now    func() time.Time
}

// ScanResult is what happened to one candidate: raised, or why not (the RaiseOutcome reasons plus "controls" when
// the agent is paused or the kill switch is on, "no_agent" when nobody fits, "error").
type ScanResult struct {
	Signal       string `json:"signal"`
	SignalKey    string `json:"signal_key"`
	AgentID      string `json:"agent_id,omitempty"`
	Raised       bool   `json:"raised"`
	Reason       string `json:"reason,omitempty"`
	InitiativeID string `json:"initiative_id,omitempty"`
}

// signalCandidate is one suggestion waiting for an agent and the budget.
type signalCandidate struct {
	signal, key string
	agent       string   // explicit agent (quality_drop: the agent itself)
	roles       []string // otherwise the first agent with one of these roles, in order
	fallback    string   // else this agent
	title, body string
	prompt      string
	params      map[string]string
	weight      float64 // ordering among metric candidates
}

// Preferred roles per signal (role template ids, internal/roles/templates).
var (
	rolesMargin    = []string{"accounting", "finance_treasury", "analyst", "data_analyst"}
	rolesCash      = []string{"finance_treasury", "accounting", "analyst"}
	rolesSales     = []string{"sales", "analyst", "data_analyst"}
	rolesSpend     = []string{"finance_treasury", "accounting", "analyst"}
	rolesApprovals = []string{"assistant", "operations", "project_manager"}
)

func (s *InitiativeScanner) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Scan evaluates the enabled signals of the organization in ctx and raises what crosses its threshold. Source
// errors do not stop the other signals; they come back joined.
func (s *InitiativeScanner) Scan(ctx context.Context) ([]ScanResult, error) {
	cfg, err := s.Svc.Settings(ctx)
	if err != nil {
		return nil, err
	}
	results := []ScanResult{}
	if !cfg.Enabled {
		return results, nil
	}
	sig := DefaultSignalSettings()
	if cfg.Signals != nil {
		sig = *cfg.Signals
	}
	org, now, loc := s.Svc.org(ctx), s.now(), "es"
	if s.Locale != nil {
		loc = signalLocale(s.Locale(ctx))
	}
	agents, err := s.Core.ListAgents(ctx, org)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, a := range agents {
		names[a.ID] = a.Name
	}
	var cands []signalCandidate
	var errs []error
	if sig.SpendPace.Enabled && s.OrgCap != nil {
		c, ok, err := s.spendPace(ctx, org, now, sig.SpendPace, loc)
		if err != nil {
			errs = append(errs, fmt.Errorf("spend_pace: %w", err))
		} else if ok {
			cands = append(cands, c)
		}
	}
	if sig.MetricChange.Enabled && s.Series != nil {
		if series, err := s.Series(ctx); err != nil {
			errs = append(errs, fmt.Errorf("metric_change: %w", err))
		} else {
			cands = append(cands, metricCandidates(series, sig.MetricChange, loc)...)
		}
	}
	if sig.QualityDrop.Enabled && s.Quality != nil {
		if qs, err := s.Quality(ctx); err != nil {
			errs = append(errs, fmt.Errorf("quality_drop: %w", err))
		} else {
			cands = append(cands, qualityCandidates(qs, names, sig.QualityDrop, loc)...)
		}
	}
	if sig.ApprovalAging.Enabled {
		if aps, err := s.Core.ListApprovals(ctx, org, string(domain.ApprovalPending)); err != nil {
			errs = append(errs, fmt.Errorf("approval_aging: %w", err))
		} else if c, ok := approvalCandidate(aps, now, sig.ApprovalAging, loc); ok {
			cands = append(cands, c)
		}
	}
	for _, c := range cands {
		r := ScanResult{Signal: c.signal, SignalKey: strings.ToLower(c.key), AgentID: pickAgent(agents, c)}
		switch {
		case r.AgentID == "":
			r.Reason = "no_agent"
		case s.Allowed != nil && !s.Allowed(ctx, org, r.AgentID):
			r.Reason = "controls"
		default:
			out, err := s.Svc.Raise(ctx, RaiseInput{AgentID: r.AgentID, Signal: c.signal, SignalKey: c.key, Title: c.title, Body: c.body,
				Prompt: c.prompt, Params: c.params, Dedupe: signalDedupe[c.signal]})
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", c.signal, err))
				r.Reason = "error"
			} else {
				r.Raised, r.Reason, r.InitiativeID = out.Raised, out.Reason, out.Initiative.ID
			}
		}
		results = append(results, r)
	}
	return results, errors.Join(errs...)
}

// pickAgent resolves the agent of a candidate: the explicit one, else the first agent (by id) with a preferred
// role, else the fallback; "" when none exists.
func pickAgent(agents []domain.Agent, c signalCandidate) string {
	sorted := append([]domain.Agent(nil), agents...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	has := func(id string) bool {
		for _, a := range sorted {
			if a.ID == id {
				return true
			}
		}
		return false
	}
	if c.agent != "" {
		if has(c.agent) {
			return c.agent
		}
		return ""
	}
	for _, role := range c.roles {
		for _, a := range sorted {
			if a.Role == role {
				return a.ID
			}
		}
	}
	if c.fallback != "" && has(c.fallback) {
		return c.fallback
	}
	return ""
}

// ---- metric_change ----

// metricMove returns the change of a series and whether it crosses the threshold. Margins compare percentage
// points (values within ±1.5 are read as fractions: 0.31 = 31 %); sales and cash compare the relative change.
func metricMove(ms MetricSeries, cfg MetricChangeSignal) (delta float64, points bool, hit bool) {
	if ms.Metric == MetricMargin {
		p, c := ms.Previous, ms.Current
		if math.Abs(p) <= 1.5 && math.Abs(c) <= 1.5 {
			p, c = p*100, c*100
		}
		delta = c - p
		return delta, true, math.Abs(delta) >= cfg.MarginPoints-1e-9
	}
	if ms.Previous == 0 {
		return 0, false, false
	}
	delta = (ms.Current - ms.Previous) / math.Abs(ms.Previous) * 100
	return delta, false, math.Abs(delta) >= cfg.ChangePct-1e-9
}

func metricCandidates(series []MetricSeries, cfg MetricChangeSignal, loc string) []signalCandidate {
	var out []signalCandidate
	for _, ms := range series {
		delta, points, hit := metricMove(ms, cfg)
		if !hit || ms.ArtifactID == "" || ms.Ref == "" {
			continue
		}
		roles := rolesSales
		switch ms.Metric {
		case MetricMargin:
			roles = rolesMargin
		case MetricCash:
			roles = rolesCash
		}
		dir, threshold := "down", cfg.ChangePct
		if delta > 0 {
			dir = "up"
		}
		prev, cur := fmtAmount(ms.Previous), fmtAmount(ms.Current)
		unit := "pct"
		if points {
			unit, threshold = "points", cfg.MarginPoints
			p, c := ms.Previous, ms.Current
			if math.Abs(p) <= 1.5 && math.Abs(c) <= 1.5 {
				p, c = p*100, c*100
			}
			prev, cur = fmtOne(p)+"%", fmtOne(c)+"%"
		}
		params := map[string]string{"artifact": clip(ms.ArtifactTitle, 80), "column": clip(ms.Column, 60), "period": clip(ms.Period, 40),
			"previous": prev, "current": cur, "change": fmtOne(math.Abs(delta)), "direction": dir, "unit": unit, "metric": ms.Metric}
		out = append(out, signalCandidate{
			signal: SignalMetricChange, key: ms.ArtifactID + ":" + ms.Ref, roles: roles, fallback: ms.CreatorAgent,
			title: render(loc, "metric.title."+dir+"."+unit, params), body: render(loc, metricBodyKey(ms.Period), params),
			prompt: strings.ReplaceAll(render(loc, "metric.prompt", params), " ()", ""), params: params, weight: math.Abs(delta) / threshold,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].weight > out[j].weight })
	if len(out) > maxMetricCandidates {
		out = out[:maxMetricCandidates]
	}
	return out
}

func metricBodyKey(period string) string {
	if period == "" {
		return "metric.body"
	}
	return "metric.body.period"
}

// ---- spend_pace ----

// spendPace projects the month's spend (usage ledger since the 1st, UTC) to the end of the month and compares it
// with what the month had available: the cap in force minus what was spent before the month began. For a plan
// whose cap resets monthly that is the monthly allowance; for a lifetime cap it is what was left on the 1st.
func (s *InitiativeScanner) spendPace(ctx context.Context, org string, now time.Time, cfg SpendPaceSignal, loc string) (signalCandidate, bool, error) {
	limit := s.OrgCap(ctx)
	if limit <= 0 {
		return signalCandidate{}, false, nil
	}
	start := PeriodStart(now)
	elapsed := now.Sub(start).Hours() / 24
	if elapsed < float64(cfg.MinDays) {
		return signalCandidate{}, false, nil
	}
	usage, err := s.Core.ListUsage(ctx, org)
	if err != nil {
		return signalCandidate{}, false, err
	}
	month := 0.0
	for _, u := range usage {
		if !u.TS.Before(start) {
			month += u.CostUSD
		}
	}
	lifetime, err := s.Core.OrgCost(ctx, org)
	if err != nil {
		return signalCandidate{}, false, err
	}
	available := limit - math.Max(0, lifetime-month)
	if available <= 0 || month <= 0 {
		return signalCandidate{}, false, nil // already at the cap: the budget itself blocks and explains it
	}
	days := float64(start.AddDate(0, 1, 0).Sub(start).Hours() / 24)
	projected := month * days / elapsed
	pace := projected / available * 100
	if pace < float64(cfg.PacePct) {
		return signalCandidate{}, false, nil
	}
	params := map[string]string{"spent": fmtMoney(month), "projected": fmtMoney(projected), "available": fmtMoney(available),
		"pace": strconv.Itoa(int(math.Round(pace))), "month": start.Format("2006-01")}
	return signalCandidate{
		signal: SignalSpendPace, key: "org:" + start.Format("2006-01"), roles: rolesSpend,
		title: render(loc, "spend.title", params), body: render(loc, "spend.body", params), prompt: render(loc, "spend.prompt", params),
		params: params,
	}, true, nil
}

// ---- approval_aging ----

func approvalCandidate(aps []domain.Approval, now time.Time, cfg ApprovalAgingSignal, loc string) (signalCandidate, bool) {
	minAge := time.Duration(cfg.MinAgeHours) * time.Hour
	var aged []domain.Approval
	for _, ap := range aps {
		if ap.Status == domain.ApprovalPending && !ap.CreatedAt.IsZero() && now.Sub(ap.CreatedAt) >= minAge {
			aged = append(aged, ap)
		}
	}
	if len(aged) == 0 || len(aged) < cfg.MinCount {
		return signalCandidate{}, false
	}
	sort.SliceStable(aged, func(i, j int) bool { return aged[i].CreatedAt.Before(aged[j].CreatedAt) })
	oldest := aged[0]
	hours := func(ap domain.Approval) string { return strconv.Itoa(int(now.Sub(ap.CreatedAt).Hours())) }
	params := map[string]string{"count": strconv.Itoa(len(aged)), "hours": strconv.Itoa(cfg.MinAgeHours),
		"oldest": clip(oldest.Title, 80), "age": hours(oldest)}
	var list strings.Builder
	for i, ap := range aged {
		if i == approvalsListed {
			break
		}
		fmt.Fprintf(&list, "\n- «%s» (%s, %s h)", clip(ap.Title, 120), ap.Risk, hours(ap))
	}
	return signalCandidate{
		signal: SignalApprovalAging, key: oldest.ID, roles: rolesApprovals, fallback: oldest.AgentID,
		title: render(loc, "approvals.title", params), body: render(loc, "approvals.body", params),
		prompt: render(loc, "approvals.prompt", params) + list.String(), params: params,
	}, true
}

// ---- quality_drop ----

func qualityCandidates(qs []QualityChange, names map[string]string, cfg QualityDropSignal, loc string) []signalCandidate {
	var out []signalCandidate
	for _, q := range qs {
		drop := q.Previous - q.Score
		if q.AgentID == "" || drop < cfg.DropPoints {
			continue
		}
		name := names[q.AgentID]
		if name == "" {
			name = q.AgentID
		}
		params := map[string]string{"agent": clip(name, 60), "previous": strconv.Itoa(q.Previous), "current": strconv.Itoa(q.Score), "drop": strconv.Itoa(drop)}
		out = append(out, signalCandidate{
			signal: SignalQualityDrop, key: q.AgentID, agent: q.AgentID,
			title: render(loc, "quality.title", params), body: render(loc, "quality.body", params), prompt: render(loc, "quality.prompt", params),
			params: params,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

// ---- text ----

func fmtOne(f float64) string   { return strconv.FormatFloat(math.Round(f*10)/10, 'f', 1, 64) }
func fmtMoney(f float64) string { return strconv.FormatFloat(f, 'f', 2, 64) }

// fmtAmount prints a value as stored, with at most two decimals and no thousands separators.
func fmtAmount(f float64) string {
	return strconv.FormatFloat(math.Round(f*100)/100, 'f', -1, 64)
}

func signalLocale(l string) string {
	switch l = strings.ToLower(strings.TrimSpace(l)); {
	case strings.HasPrefix(l, "en"):
		return "en"
	case strings.HasPrefix(l, "pt"):
		return "pt"
	}
	return "es"
}

// render fills a text template ({name} placeholders) in the organization language. These are the stored
// fallback texts and the request a person submits by accepting; the UI localizes from signal + params.
func render(loc, key string, params map[string]string) string {
	t, ok := signalTexts[loc][key]
	if !ok {
		t = signalTexts["es"][key]
	}
	pairs := make([]string, 0, 2*len(params))
	for k, v := range params {
		pairs = append(pairs, "{"+k+"}", v)
	}
	return strings.NewReplacer(pairs...).Replace(t)
}

var signalTexts = map[string]map[string]string{
	"es": {
		"metric.title.down.pct":    "«{column}» bajó {change} % en «{artifact}»",
		"metric.title.up.pct":      "«{column}» subió {change} % en «{artifact}»",
		"metric.title.down.points": "«{column}» bajó {change} puntos en «{artifact}»",
		"metric.title.up.points":   "«{column}» subió {change} puntos en «{artifact}»",
		"metric.body":              "Pasó de {previous} a {current} frente al periodo anterior. ¿Lo reviso?",
		"metric.body.period":       "{period}: pasó de {previous} a {current} frente al periodo anterior. ¿Lo reviso?",
		"metric.prompt": "Revisa por qué «{column}» pasó de {previous} a {current} ({period}) en «{artifact}». Busca las causas en los datos " +
			"disponibles y propón qué hacer. No modifiques el archivo ni apruebes nada: entrega un análisis.",
		"spend.title": "El gasto de IA va camino de superar lo disponible este mes",
		"spend.body":  "Llevamos US$ {spent} este mes; a este ritmo serían US$ {projected} y el mes tenía US$ {available} disponibles. ¿Reviso en qué se va?",
		"spend.prompt": "Analiza el gasto de IA de este mes (US$ {spent}; proyección US$ {projected} frente a US$ {available} disponibles): " +
			"qué empleados y tareas concentran el costo y qué se puede ajustar. No cambies topes ni configuraciones: entrega una propuesta.",
		"approvals.title": "Hay aprobaciones esperando: {count} con más de {hours} h",
		"approvals.body":  "La más antigua es «{oldest}» ({age} h). ¿Preparo un resumen para que decidas?",
		"approvals.prompt": "Prepara un resumen breve de estas aprobaciones pendientes para que una persona decida: qué pide cada una, el riesgo y " +
			"qué pasa si sigue esperando. No apruebes ni rechaces nada.",
		"quality.title":  "El puntaje de calidad de {agent} bajó {drop} puntos",
		"quality.body":   "Pasó de {previous} a {current} frente a la ventana anterior. ¿Reviso qué cambió?",
		"quality.prompt": "Revisa por qué bajó el puntaje de calidad de {agent} (de {previous} a {current}): aprobaciones rechazadas, tareas fallidas y revisiones recientes. Propón cómo mejorar; no cambies configuraciones.",
	},
	"en": {
		"metric.title.down.pct":    "\"{column}\" fell {change}% in \"{artifact}\"",
		"metric.title.up.pct":      "\"{column}\" rose {change}% in \"{artifact}\"",
		"metric.title.down.points": "\"{column}\" fell {change} points in \"{artifact}\"",
		"metric.title.up.points":   "\"{column}\" rose {change} points in \"{artifact}\"",
		"metric.body":              "It went from {previous} to {current} against the previous period. Shall I look into it?",
		"metric.body.period":       "{period}: it went from {previous} to {current} against the previous period. Shall I look into it?",
		"metric.prompt": "Review why \"{column}\" went from {previous} to {current} ({period}) in \"{artifact}\". Look for the causes in the " +
			"available data and propose what to do. Do not modify the file or approve anything: deliver an analysis.",
		"spend.title": "AI spend is on track to exceed what this month has available",
		"spend.body":  "We have spent US$ {spent} this month; at this pace it would be US$ {projected}, and the month had US$ {available} available. Shall I check where it goes?",
		"spend.prompt": "Analyze this month's AI spend (US$ {spent}; projected US$ {projected} against US$ {available} available): which " +
			"employees and tasks concentrate the cost and what could be adjusted. Do not change caps or settings: deliver a proposal.",
		"approvals.title": "Approvals waiting: {count} older than {hours} h",
		"approvals.body":  "The oldest is \"{oldest}\" ({age} h). Shall I prepare a summary so you can decide?",
		"approvals.prompt": "Prepare a short summary of these pending approvals so a person can decide: what each one asks for, the risk and " +
			"what happens if it keeps waiting. Do not approve or reject anything.",
		"quality.title":  "{agent}'s quality score dropped {drop} points",
		"quality.body":   "It went from {previous} to {current} against the previous window. Shall I check what changed?",
		"quality.prompt": "Review why {agent}'s quality score dropped (from {previous} to {current}): rejected approvals, failed tasks and recent reviews. Propose how to improve; do not change settings.",
	},
	"pt": {
		"metric.title.down.pct":    "«{column}» caiu {change} % em «{artifact}»",
		"metric.title.up.pct":      "«{column}» subiu {change} % em «{artifact}»",
		"metric.title.down.points": "«{column}» caiu {change} pontos em «{artifact}»",
		"metric.title.up.points":   "«{column}» subiu {change} pontos em «{artifact}»",
		"metric.body":              "Passou de {previous} para {current} em relação ao período anterior. Quer que eu verifique?",
		"metric.body.period":       "{period}: passou de {previous} para {current} em relação ao período anterior. Quer que eu verifique?",
		"metric.prompt": "Analise por que «{column}» passou de {previous} para {current} ({period}) em «{artifact}». Procure as causas nos dados " +
			"disponíveis e proponha o que fazer. Não modifique o arquivo nem aprove nada: entregue uma análise.",
		"spend.title": "O gasto de IA está a caminho de superar o disponível neste mês",
		"spend.body":  "Gastamos US$ {spent} neste mês; neste ritmo seriam US$ {projected}, e o mês tinha US$ {available} disponíveis. Quer que eu veja para onde vai?",
		"spend.prompt": "Analise o gasto de IA deste mês (US$ {spent}; projeção de US$ {projected} contra US$ {available} disponíveis): quais " +
			"funcionários e tarefas concentram o custo e o que pode ser ajustado. Não altere limites nem configurações: entregue uma proposta.",
		"approvals.title": "Aprovações esperando: {count} com mais de {hours} h",
		"approvals.body":  "A mais antiga é «{oldest}» ({age} h). Quer que eu prepare um resumo para você decidir?",
		"approvals.prompt": "Prepare um resumo breve destas aprovações pendentes para que uma pessoa decida: o que cada uma pede, o risco e o " +
			"que acontece se continuar esperando. Não aprove nem rejeite nada.",
		"quality.title":  "A pontuação de qualidade de {agent} caiu {drop} pontos",
		"quality.body":   "Passou de {previous} para {current} em relação à janela anterior. Quer que eu veja o que mudou?",
		"quality.prompt": "Analise por que a pontuação de qualidade de {agent} caiu (de {previous} para {current}): aprovações rejeitadas, tarefas com falha e revisões recentes. Proponha como melhorar; não altere configurações.",
	},
}
