package tools

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FakeSuite bundles the in-memory fake tools, sharing a clock and CRM contacts.
type FakeSuite struct {
	Email       *Email
	Calendar    *Calendar
	CRM         *CRM
	Documents   *Documents
	Spreadsheet *Spreadsheet
	Calculator  *Calculator
}

// NewFakeSuite builds seeded fakes. now may be nil (time.Now).
func NewFakeSuite(now func() time.Time) *FakeSuite {
	if now == nil {
		now = time.Now
	}
	return &FakeSuite{
		Email:       NewEmail(now),
		Calendar:    NewCalendar(now),
		CRM:         NewCRM(now),
		Documents:   NewDocuments(now),
		Spreadsheet: NewSpreadsheet(),
		Calculator:  NewCalculator(),
	}
}

// All returns every tool.
func (s *FakeSuite) All() []Tool {
	return []Tool{s.Email, s.Calendar, s.CRM, s.Documents, s.Spreadsheet, s.Calculator}
}

// NewFakeRegistry returns a registry with the fake tools registered and the
// seed agent permissions applied.
func NewFakeRegistry(s *FakeSuite) *Registry {
	r := NewRegistry()
	for _, t := range s.All() {
		_ = r.Register(t)
	}
	SeedPermissions(r)
	return r
}

type idGen struct {
	mu sync.Mutex
	n  int
}

func (g *idGen) next(prefix string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n++
	return fmt.Sprintf("%s-%03d", prefix, g.n)
}

func argStr(a map[string]any, k string) string {
	if v, ok := a[k]; ok {
		switch t := v.(type) {
		case string:
			return strings.TrimSpace(t)
		case fmt.Stringer:
			return t.String()
		case nil:
			return ""
		default:
			return fmt.Sprint(t)
		}
	}
	return ""
}

func argNum(a map[string]any, k string) (float64, bool) {
	switch t := a[k].(type) {
	case float64:
		return t, !math.IsNaN(t) && !math.IsInf(t, 0)
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	return 0, false
}

func argInt(a map[string]any, k string, def int) int {
	if f, ok := argNum(a, k); ok {
		return int(f)
	}
	return def
}

func argList(a map[string]any, k string) []string {
	switch t := a[k].(type) {
	case string:
		var out []string
		for _, p := range strings.FieldsFunc(t, func(r rune) bool { return r == ',' || r == ';' }) {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	case []string:
		return append([]string(nil), t...)
	case []any:
		var out []string
		for _, x := range t {
			if s := strings.TrimSpace(fmt.Sprint(x)); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func parseTime(s string) (time.Time, error) {
	for _, l := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, invalid("bad time %q (use RFC3339 or YYYY-MM-DD[ HH:MM])", s)
}

func unknownAction(tool, action string) error {
	return fmt.Errorf("%w: %s has no action %q", ErrInvalidArgs, tool, action)
}
