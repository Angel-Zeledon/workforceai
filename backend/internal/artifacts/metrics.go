package artifacts

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Numeric series for the metric_change signal of proactive employees (application/initiative_signals.go).
//
// The extraction is deliberately plain: a column whose header names a margin, sales or cash figure
// (application.ClassifyMetric), read top to bottom; its last two numeric values are the previous and the current
// period. Only JSON numbers count (cached "v" of a sheet cell, a number in a table cell); text that looks like a
// number is ignored rather than guessed. Rows labelled "total" and SUM formulas are skipped, so a total row is never
// compared with the last period. Series laid out horizontally (periods in columns) are not read.

const (
	metricLookback       = 30 * 24 * time.Hour
	maxMetricArtifacts   = 50
	maxSeriesPerArtifact = 10
)

// MetricSeries lists the numeric series of the organization's sheet and table artifacts updated in the last 30 days
// (newest first, at most 50 artifacts, archived ones skipped). It reads stored content only; it never edits.
func (s *Service) MetricSeries(ctx context.Context) ([]application.MetricSeries, error) {
	org := s.org(ctx)
	all, err := s.cfg.Store.List(ctx, org)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].UpdatedAt.After(all[j].UpdatedAt) })
	now := s.now()
	out := []application.MetricSeries{}
	seen := 0
	for _, m := range all {
		if (m.Kind != KindSheet && m.Kind != KindTable) || m.Status == StatusArchived || now.Sub(m.UpdatedAt) > metricLookback {
			continue
		}
		if seen == maxMetricArtifacts {
			break
		}
		seen++
		st, err := s.cfg.Store.Get(ctx, org, m.ID)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		} else if err != nil {
			return nil, err
		}
		out = append(out, ExtractMetricSeries(st.Meta, st.Content)...)
	}
	return out, nil
}

// ExtractMetricSeries reads the metric columns of one sheet or table artifact.
func ExtractMetricSeries(m Meta, content json.RawMessage) []application.MetricSeries {
	var out []application.MetricSeries
	switch m.Kind {
	case KindSheet:
		out = sheetSeries(m, content)
	case KindTable:
		out = tableSeries(m, content)
	}
	creator := ""
	if m.CreatedBy.Kind == ActorAgent {
		creator = m.CreatedBy.ID
	}
	for i := range out {
		out[i].ArtifactID, out[i].ArtifactTitle, out[i].CreatorAgent = m.ID, m.Title, creator
	}
	if len(out) > maxSeriesPerArtifact {
		out = out[:maxSeriesPerArtifact]
	}
	return out
}

func isTotalLabel(s string) bool { return strings.Contains(strings.ToLower(s), "total") }

// localized reads a name that may be a plain string or a {"es": .., "en": ..} map (templates).
func localized(v any, loc string) string {
	if s, ok := v.(string); ok {
		return s
	}
	m := asMap(v)
	if s := asStr(m[loc]); s != "" {
		return s
	}
	if s := asStr(m["es"]); s != "" {
		return s
	}
	return asStr(m["en"])
}

// splitCell splits "B14" into ("B", 14); columns are ordered with colIndex (statement_import.go).
func splitCell(key string) (string, int, bool) {
	if !cellKeyRe.MatchString(key) {
		return "", 0, false
	}
	i := strings.IndexFunc(key, func(r rune) bool { return r >= '0' && r <= '9' })
	row, err := strconv.Atoi(key[i:])
	if err != nil {
		return "", 0, false
	}
	return key[:i], row, true
}

type sheetCell struct {
	v any
	f string
}

func sheetSeries(m Meta, content json.RawMessage) []application.MetricSeries {
	var doc struct {
		Sheets []struct {
			ID    string                     `json:"id"`
			Name  any                        `json:"name"`
			Cells map[string]json.RawMessage `json:"cells"`
		} `json:"sheets"`
	}
	if json.Unmarshal(content, &doc) != nil {
		return nil
	}
	var out []application.MetricSeries
	for _, sh := range doc.Sheets {
		grid := map[string]map[int]sheetCell{} // column -> row -> cell
		for key, raw := range sh.Cells {
			col, row, ok := splitCell(key)
			if !ok {
				continue
			}
			var c struct {
				V any    `json:"v"`
				F string `json:"f"`
			}
			if json.Unmarshal(raw, &c) != nil {
				continue
			}
			if grid[col] == nil {
				grid[col] = map[int]sheetCell{}
			}
			grid[col][row] = sheetCell{v: c.V, f: c.F}
		}
		cols := make([]string, 0, len(grid))
		for c := range grid {
			cols = append(cols, c)
		}
		sort.Slice(cols, func(i, j int) bool { return colIndex(cols[i]) < colIndex(cols[j]) })
		// label of a row: its left-most text cell left of column c.
		label := func(row int, c string) string {
			for _, x := range cols {
				if colIndex(x) >= colIndex(c) {
					break
				}
				if s, ok := grid[x][row].v.(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
			return ""
		}
		name := localized(sh.Name, m.Locale)
		for _, col := range cols {
			rows := make([]int, 0, len(grid[col]))
			for r := range grid[col] {
				rows = append(rows, r)
			}
			sort.Ints(rows)
			header, metric := "", ""
			var vals []float64
			var at []int
			for _, r := range rows {
				c := grid[col][r]
				if s, ok := c.v.(string); ok {
					if mt := application.ClassifyMetric(s); mt != "" {
						if metric != "" && len(vals) >= 2 {
							break // a second block starts below: keep the first one
						}
						header, metric, vals, at = strings.TrimSpace(s), mt, nil, nil
					}
					continue
				}
				f, ok := c.v.(float64)
				if !ok || metric == "" {
					continue
				}
				uf := strings.ToUpper(c.f)
				if strings.Contains(uf, "SUM(") || strings.Contains(uf, "SUMA(") || isTotalLabel(label(r, col)) {
					continue
				}
				vals, at = append(vals, f), append(at, r)
			}
			if metric == "" || len(vals) < 2 {
				continue
			}
			n := len(vals)
			out = append(out, application.MetricSeries{Sheet: name, Column: header, Metric: metric,
				Ref: sh.ID + "!" + col + strconv.Itoa(at[n-1]), Period: label(at[n-1], col), Previous: vals[n-2], Current: vals[n-1]})
		}
	}
	return out
}

func tableSeries(m Meta, content json.RawMessage) []application.MetricSeries {
	var doc struct {
		Columns []struct {
			Key   string `json:"key"`
			Label string `json:"label"`
		} `json:"columns"`
		Rows []struct {
			ID    string         `json:"id"`
			Cells map[string]any `json:"cells"`
		} `json:"rows"`
	}
	if json.Unmarshal(content, &doc) != nil {
		return nil
	}
	// label of a row: its first text cell in column order (other than the metric column).
	label := func(cells map[string]any, skip string) string {
		for _, c := range doc.Columns {
			if s, ok := cells[c.Key].(string); ok && c.Key != skip && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}
	var out []application.MetricSeries
	for _, c := range doc.Columns {
		header := c.Label
		if strings.TrimSpace(header) == "" {
			header = c.Key
		}
		metric := application.ClassifyMetric(header)
		if metric == "" {
			continue
		}
		var vals []float64
		var at []int
		for i, r := range doc.Rows {
			f, ok := r.Cells[c.Key].(float64)
			if !ok || r.ID == "" || isTotalLabel(label(r.Cells, c.Key)) {
				continue
			}
			vals, at = append(vals, f), append(at, i)
		}
		if len(vals) < 2 {
			continue
		}
		n := len(vals)
		last := doc.Rows[at[n-1]]
		out = append(out, application.MetricSeries{Column: strings.TrimSpace(header), Metric: metric,
			Ref: last.ID + ":" + c.Key, Period: label(last.Cells, c.Key), Previous: vals[n-2], Current: vals[n-1]})
	}
	return out
}
