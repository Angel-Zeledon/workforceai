package governance

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Report dimensions.
const (
	ByAgent      = "agent"
	ByProject    = "project"
	ByDepartment = "department"
)

// ValidDimension reports whether d is a report dimension.
func ValidDimension(d string) bool { return d == ByAgent || d == ByProject || d == ByDepartment }

// UsageRow is one line of a usage and cost report.
type UsageRow struct {
	Key          string  `json:"key"`   // agent id / project id / department ("" = unassigned)
	Label        string  `json:"label"` // display name
	Department   string  `json:"department,omitempty"`
	Requests     int     `json:"requests"`
	Calls        int     `json:"calls"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	SharePct     float64 `json:"share_pct"`
}

// UsageReport is a report plus its totals.
type UsageReport struct {
	By    string     `json:"by"`
	From  time.Time  `json:"from"`
	To    time.Time  `json:"to"`
	Rows  []UsageRow `json:"rows"`
	Total UsageRow   `json:"total"`
}

// Finish sorts rows (most expensive first), computes the shares and the total.
func (r *UsageReport) Finish() {
	var t UsageRow
	for _, x := range r.Rows {
		t.Requests += x.Requests
		t.Calls += x.Calls
		t.InputTokens += x.InputTokens
		t.OutputTokens += x.OutputTokens
		t.CostUSD += x.CostUSD
	}
	t.Label = "total"
	sort.SliceStable(r.Rows, func(i, j int) bool {
		if r.Rows[i].CostUSD != r.Rows[j].CostUSD {
			return r.Rows[i].CostUSD > r.Rows[j].CostUSD
		}
		return r.Rows[i].Key < r.Rows[j].Key
	})
	for i := range r.Rows {
		if t.CostUSD > 0 {
			r.Rows[i].SharePct = round2(r.Rows[i].CostUSD / t.CostUSD * 100)
		}
	}
	t.SharePct = 100
	r.Total = t
	if r.Rows == nil {
		r.Rows = []UsageRow{}
	}
}

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

// ReportStore reads the cost ledger.
type ReportStore interface {
	Usage(ctx context.Context, org, by string, from, to time.Time) ([]UsageRow, error)
}

// BuildUsageReport validates the range and dimension and builds the report. The range defaults to the
// current month and is bounded to 400 days.
func BuildUsageReport(ctx context.Context, st ReportStore, org, by string, from, to *time.Time, now time.Time) (UsageReport, error) {
	if !ValidDimension(by) {
		return UsageReport{}, fmt.Errorf("%w: by must be agent, project or department", ErrInvalid)
	}
	f := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	t := now.Add(time.Second)
	if from != nil {
		f = *from
	}
	if to != nil {
		t = *to
	}
	if !f.Before(t) {
		return UsageReport{}, fmt.Errorf("%w: from must be before to", ErrInvalid)
	}
	if t.Sub(f) > 400*24*time.Hour {
		return UsageReport{}, fmt.Errorf("%w: the range is limited to 400 days", ErrInvalid)
	}
	rows, err := st.Usage(ctx, org, by, f, t)
	if err != nil {
		return UsageReport{}, err
	}
	rep := UsageReport{By: by, From: f, To: t, Rows: rows}
	rep.Finish()
	return rep, nil
}

// csvCell neutralizes spreadsheet formulas (CSV injection): agent, project and department names are
// user-controlled.
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// WriteCSV renders the report; the last line is the total.
func (r UsageReport) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	head := []string{r.By, "name", "department", "requests", "calls", "input_tokens", "output_tokens", "cost_usd", "share_pct"}
	if err := cw.Write(head); err != nil {
		return err
	}
	rec := func(x UsageRow, key string) []string {
		return []string{csvCell(key), csvCell(x.Label), csvCell(x.Department), strconv.Itoa(x.Requests), strconv.Itoa(x.Calls),
			strconv.FormatInt(x.InputTokens, 10), strconv.FormatInt(x.OutputTokens, 10), strconv.FormatFloat(x.CostUSD, 'f', 6, 64),
			strconv.FormatFloat(x.SharePct, 'f', 2, 64)}
	}
	for _, x := range r.Rows {
		if err := cw.Write(rec(x, x.Key)); err != nil {
			return err
		}
	}
	if err := cw.Write(rec(r.Total, "TOTAL")); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

// ---- departments, limits and the spend overview ----

// MaxDepartmentLen bounds the department tag.
const MaxDepartmentLen = 60

// NormalizeDepartment trims and validates a department tag ("" clears it).
func NormalizeDepartment(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) > MaxDepartmentLen {
		return "", fmt.Errorf("%w: department is limited to %d characters", ErrInvalid, MaxDepartmentLen)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%w: department has control characters", ErrInvalid)
		}
	}
	return s, nil
}

// AgentSpend is one agent in the console: tag, month-to-date spend and its monthly cap.
type AgentSpend struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Role          string  `json:"role"`
	Department    string  `json:"department"`
	MonthSpendUSD float64 `json:"month_spend_usd"`
	CapUSD        float64 `json:"cap_usd"` // 0 = no explicit cap
}

// DepartmentSpend is one department (tag) with its limit.
type DepartmentSpend struct {
	Name          string  `json:"name"` // "" = agents without a department
	Agents        int     `json:"agents"`
	MonthSpendUSD float64 `json:"month_spend_usd"`
	LimitUSD      float64 `json:"limit_usd"` // 0 = no limit
	UsedPct       float64 `json:"used_pct"`
	Over          bool    `json:"over"`
}

// Overview groups agents by department and attaches limits.
func Overview(agents []AgentSpend, limits map[string]float64) []DepartmentSpend {
	by := map[string]*DepartmentSpend{}
	get := func(n string) *DepartmentSpend {
		if d, ok := by[n]; ok {
			return d
		}
		d := &DepartmentSpend{Name: n, LimitUSD: limits[n]}
		by[n] = d
		return d
	}
	for _, a := range agents {
		d := get(a.Department)
		d.Agents++
		d.MonthSpendUSD += a.MonthSpendUSD
	}
	for n := range limits {
		get(n) // a limit on a department with no agents still shows
	}
	out := make([]DepartmentSpend, 0, len(by))
	for _, d := range by {
		if d.LimitUSD > 0 {
			d.UsedPct = round2(d.MonthSpendUSD / d.LimitUSD * 100)
			d.Over = d.MonthSpendUSD >= d.LimitUSD
		}
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
