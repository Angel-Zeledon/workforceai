package application

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"

	"aiworkforce/backend/internal/domain"
)

// W3: bounded context handoff. Everything here is deterministic (no model
// calls): a dependency output is either sent in full, or reduced to its own
// summary (or a truncation of its text) plus a reference id. The runtime
// treats all of it as delimited, untrusted data.

const (
	defaultDepContextTokenBudget  = 8000  // matches projects/calc.go: min(8000, 800*deps)
	defaultSynthTokenBudget       = 12000 // above this a request is synthesized hierarchically
	defaultSynthMaxGroups         = 6     // group calls; the final pass is one more
	defaultProjectContextBudget   = 1500
	maxSummaryTokens              = 80 // one-line summary cap (tokens)
	minSummaryTokens              = 12
	maxContextArtifacts           = 3
	defaultContextArtifactBudget  = 3000
	refPrefix                     = "task:"
	charsPerToken                 = 4
	ellipsis                      = "..."
	maxIndexSummaryChars          = 160
	synthSectionDigestChars       = 400
	synthSectionsPerGroupInDigest = 8
)

func (c Config) depBudget() int {
	if c.DepContextTokenBudget > 0 {
		return c.DepContextTokenBudget
	}
	return defaultDepContextTokenBudget
}

func (c Config) synthBudget() int {
	if c.SynthTokenBudget > 0 {
		return c.SynthTokenBudget
	}
	return defaultSynthTokenBudget
}

func (c Config) synthMaxGroups() int {
	if c.SynthMaxGroups > 0 {
		return c.SynthMaxGroups
	}
	return defaultSynthMaxGroups
}

func (c Config) projectContextBudget() int {
	if c.ProjectContextTokenBudget > 0 {
		return c.ProjectContextTokenBudget
	}
	return defaultProjectContextBudget
}

// TaskRef is the reference id of a task output ("task:<id>").
func TaskRef(taskID string) string { return refPrefix + taskID }

// estimateTextTokens is the shared rough estimate (4 bytes per token).
func estimateTextTokens(s string) int {
	return (len(s) + charsPerToken - 1) / charsPerToken
}

// outputTokens estimates the prompt cost of an output (its text, not its JSON keys).
func outputTokens(o domain.StructuredOutput) int {
	n := len(o.Summary)
	for _, group := range [][]string{o.Findings, o.Hypotheses, o.Evidence, o.Recommendations, o.SuggestedTasks} {
		for _, s := range group {
			n += len(s) + 2
		}
	}
	if len(o.Metrics) > 0 {
		if b, err := json.Marshal(o.Metrics); err == nil {
			n += len(b)
		}
	}
	return (n + charsPerToken - 1) / charsPerToken
}

// truncateText cuts s to at most max bytes on a rune boundary (marker included).
func truncateText(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	if max <= len(ellipsis) {
		max = len(ellipsis) + 1
	}
	cut := max - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut]) + ellipsis
}

// summaryOf is the task's own summary; without one, the first finding or
// recommendation (deterministic fallback, no model call).
func summaryOf(o domain.StructuredOutput, maxChars int) string {
	s := strings.TrimSpace(o.Summary)
	if s == "" {
		for _, group := range [][]string{o.Findings, o.Recommendations, o.Evidence, o.Hypotheses} {
			for _, c := range group {
				if strings.TrimSpace(c) != "" {
					s = c
					break
				}
			}
			if s != "" {
				break
			}
		}
	}
	return truncateText(s, maxChars)
}

// compactOutput keeps only the (capped) summary and the confidence.
func compactOutput(o domain.StructuredOutput, maxChars int) domain.StructuredOutput {
	c := domain.StructuredOutput{Summary: summaryOf(o, maxChars), Confidence: o.Confidence}
	c.Normalize()
	return c
}

// ctxItem is one output competing for a token budget.
type ctxItem struct {
	ID       string
	AgentID  string
	Title    string
	Out      domain.StructuredOutput
	Position int // original order (stable tie-break)
}

// fitted is an item after budgeting.
type fitted struct {
	ctxItem
	Truncated bool // reduced to its summary
}

// fitOutputs enforces budget (tokens) over items:
//  1. everything fits in full: returned untouched (old behaviour);
//  2. otherwise every item is reduced to a capped summary, and the most
//     relevant items (same agent as the consumer, then higher confidence, then
//     original order) are restored in full while the budget allows;
//  3. if even the summaries do not fit, the least relevant items are omitted
//     (their number is returned).
//
// The result keeps the original order. The total never exceeds budget except
// for the "everything fits" case, where it is within budget by definition.
func fitOutputs(items []ctxItem, consumerAgent string, budget int) (out []fitted, omitted int) {
	if budget <= 0 {
		budget = defaultDepContextTokenBudget
	}
	total := 0
	for _, it := range items {
		total += outputTokens(it.Out)
	}
	if total <= budget {
		out = make([]fitted, len(items))
		for i, it := range items {
			out[i] = fitted{ctxItem: it}
		}
		return out, 0
	}
	order := make([]int, len(items)) // by relevance
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ia, ib := items[order[a]], items[order[b]]
		sa, sb := ia.AgentID == consumerAgent, ib.AgentID == consumerAgent
		if sa != sb {
			return sa
		}
		if ia.Out.Confidence != ib.Out.Confidence {
			return ia.Out.Confidence > ib.Out.Confidence
		}
		return ia.Position < ib.Position
	})
	// summary cap per item shrinks with fan-in
	capTok := budget / (2 * len(items))
	capTok = max(minSummaryTokens, min(maxSummaryTokens, capTok))
	keep := len(items)
	if m := budget / capTok; keep > m {
		keep, omitted = m, len(items)-m
	}
	kept := order[:keep]
	res := make(map[int]fitted, keep)
	used := 0
	for _, i := range kept {
		c := compactOutput(items[i].Out, capTok*charsPerToken)
		used += outputTokens(c)
		res[i] = fitted{ctxItem: ctxItem{ID: items[i].ID, AgentID: items[i].AgentID, Title: items[i].Title, Out: c, Position: items[i].Position}, Truncated: true}
	}
	for _, i := range kept {
		full := outputTokens(items[i].Out)
		cur := outputTokens(res[i].Out)
		if full-cur <= budget-used {
			used += full - cur
			res[i] = fitted{ctxItem: items[i]}
		}
	}
	idx := make([]int, 0, keep)
	for i := range res {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		out = append(out, res[i])
	}
	return out, omitted
}

// fitDependencies budgets the direct dependency outputs of one task.
func fitDependencies(deps []domain.Task, consumerAgent string, budget int) (out []DependencyOutput, omitted int) {
	items := make([]ctxItem, 0, len(deps))
	for i, d := range deps {
		if d.Output != nil {
			items = append(items, ctxItem{ID: d.ID, AgentID: d.AgentID, Title: d.Title, Out: *d.Output, Position: i})
		}
	}
	fit, omitted := fitOutputs(items, consumerAgent, budget)
	out = make([]DependencyOutput, 0, len(fit))
	for _, f := range fit {
		out = append(out, DependencyOutput{TaskID: f.ID, AgentID: f.AgentID, Output: f.Out, Ref: TaskRef(f.ID), Title: f.Title, Truncated: f.Truncated})
	}
	return out, omitted
}

// buildProjectIndex is the compact, read-only index of OTHER finished tasks of
// the same request: title, agent, one-line summary and reference id, most
// recently finished first, within budget tokens.
func buildProjectIndex(done []domain.Task, budget int) (idx []ProjectTaskRef, omitted int) {
	sorted := append([]domain.Task(nil), done...)
	sort.SliceStable(sorted, func(a, b int) bool {
		fa, fb := sorted[a].FinishedAt, sorted[b].FinishedAt
		switch {
		case fa != nil && fb != nil && !fa.Equal(*fb):
			return fa.After(*fb)
		case (fa == nil) != (fb == nil):
			return fa != nil
		}
		return sorted[a].ID < sorted[b].ID
	})
	used := 0
	for i, t := range sorted {
		if t.Output == nil {
			continue
		}
		e := ProjectTaskRef{Ref: TaskRef(t.ID), TaskID: t.ID, Title: truncateText(t.Title, 100), AgentID: t.AgentID,
			Summary: summaryOf(*t.Output, maxIndexSummaryChars)}
		cost := estimateTextTokens(e.Title+e.Summary+e.AgentID+e.Ref) + 4
		if used+cost > budget {
			omitted = len(sorted) - i
			break
		}
		used += cost
		idx = append(idx, e)
	}
	return idx, omitted
}
