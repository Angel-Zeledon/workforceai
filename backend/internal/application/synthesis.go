package application

import (
	"context"
	"fmt"

	"aiworkforce/backend/internal/domain"
)

// W3: hierarchical synthesis. A request whose outputs fit in SynthTokenBudget
// is synthesized in ONE call, exactly as before. A larger one is split into at
// most SynthMaxGroups groups (by workflow when known, else consecutive chunks
// within the budget), each group is synthesized, and a final pass runs over
// the group syntheses: at most SynthMaxGroups+1 runtime calls, each reserved
// and recorded like any synthesis (caps, pause, ledger, audit).

// synthesize returns the final synthesis, or false when the request failed
// (or the context ended) and finish must stop.
func (o *Orchestrator) synthesize(ctx context.Context, rs *run, outs []SynthOutput, keys []string) (SynthesizeResponse, bool) {
	groups := planSynthGroups(outs, keys, o.cfg.synthBudget(), o.cfg.synthMaxGroups())
	if len(groups) <= 1 {
		return o.synthCall(ctx, rs, SynthesizeRequest{RequestText: rs.req.Text, Outputs: outs,
			Locale: rs.style.Locale, Tone: rs.style.Tone})
	}
	partials := make([]SynthOutput, 0, len(groups))
	for i, g := range groups {
		o.setState(ctx, assistantID, domain.StateWorking, fmt.Sprintf("Consolidando resultados (%d/%d)", i+1, len(groups)+1), nil, 85)
		in := make([]SynthOutput, 0, len(g))
		for _, idx := range g {
			in = append(in, outs[idx])
		}
		in = budgetSynthOutputs(in, o.cfg.synthBudget())
		res, ok := o.synthCall(ctx, rs, SynthesizeRequest{RequestText: rs.req.Text, Outputs: in,
			Locale: rs.style.Locale, Tone: rs.style.Tone, Stage: "group", Part: i + 1, Parts: len(groups)})
		if !ok {
			return SynthesizeResponse{}, false
		}
		partials = append(partials, groupDigest(i+1, res))
	}
	o.setState(ctx, assistantID, domain.StateWorking, fmt.Sprintf("Consolidando resultados (%d/%d)", len(groups)+1, len(groups)+1), nil, 90)
	return o.synthCall(ctx, rs, SynthesizeRequest{RequestText: rs.req.Text, Outputs: budgetSynthOutputs(partials, o.cfg.synthBudget()),
		Locale: rs.style.Locale, Tone: rs.style.Tone, Stage: "final", Parts: len(groups)})
}

// synthCall is one reserved, retried, recorded runtime synthesis.
func (o *Orchestrator) synthCall(ctx context.Context, rs *run, in SynthesizeRequest) (SynthesizeResponse, bool) {
	var syn SynthesizeResponse
	synRes, err := o.reserveOrPause(ctx, rs, assistantID, "", domain.UsageSynthesize)
	if err != nil {
		if ctx.Err() == nil {
			o.failRequest(ctx, rs, assistantID, "No pude consolidar el informe", err)
		}
		return syn, false
	}
	err = o.call(ctx, "synthesize", func(c context.Context) (err error) {
		syn, err = o.rt.Synthesize(c, in)
		return err
	})
	if err != nil {
		synRes.Release()
		o.failRequest(ctx, rs, assistantID, "No pude consolidar el informe", err)
		return syn, false
	}
	o.recordUsage(ctx, rs, "", assistantID, domain.UsageSynthesize, syn.Usage, nil, synRes)
	return syn, true
}

// groupDigest turns a group synthesis into the input of the final pass.
func groupDigest(part int, s SynthesizeResponse) SynthOutput {
	var findings []string
	for i, sec := range s.Sections {
		if i >= synthSectionsPerGroupInDigest {
			break
		}
		findings = append(findings, truncateText(sec.Heading+": "+sec.Body, synthSectionDigestChars))
	}
	title := s.Title
	if title == "" {
		title = fmt.Sprintf("Part %d", part)
	}
	return SynthOutput{TaskID: fmt.Sprintf("group-%d", part), AgentID: assistantID, Title: title,
		Output: domain.StructuredOutput{Summary: s.Summary, Findings: findings, Confidence: 1}}
}

// budgetSynthOutputs keeps a synthesis input within budget (summaries first,
// full text of the most relevant ones while it fits).
func budgetSynthOutputs(in []SynthOutput, budget int) []SynthOutput {
	items := make([]ctxItem, len(in))
	for i, s := range in {
		items[i] = ctxItem{ID: s.TaskID, AgentID: s.AgentID, Title: s.Title, Out: s.Output, Position: i}
	}
	fit, _ := fitOutputs(items, "", budget)
	out := make([]SynthOutput, len(fit))
	for i, f := range fit {
		out[i] = SynthOutput{TaskID: f.ID, AgentID: f.AgentID, Title: f.Title, Output: f.Out}
	}
	return out
}

// planSynthGroups returns the index groups to synthesize: a single group when
// everything fits the budget (single pass). Otherwise consecutive runs of the
// same workflow key are packed into groups within budget, oversized runs are
// split, and adjacent groups are merged until at most maxGroups remain.
func planSynthGroups(outs []SynthOutput, keys []string, budget, maxGroups int) [][]int {
	n := len(outs)
	cost := make([]int, n)
	total := 0
	for i, o := range outs {
		cost[i] = outputTokens(o.Output)
		total += cost[i]
	}
	if total <= budget || n < 2 || maxGroups < 2 {
		return [][]int{allIdx(n)}
	}
	// segments: consecutive items with the same workflow key
	var segs [][]int
	for i := 0; i < n; i++ {
		k := ""
		if i < len(keys) {
			k = keys[i]
		}
		if i > 0 {
			pk := ""
			if i-1 < len(keys) {
				pk = keys[i-1]
			}
			if k == pk {
				segs[len(segs)-1] = append(segs[len(segs)-1], i)
				continue
			}
		}
		segs = append(segs, []int{i})
	}
	// pack segments, splitting the oversized ones
	var groups [][]int
	var cur []int
	curCost := 0
	flush := func() {
		if len(cur) > 0 {
			groups = append(groups, cur)
			cur, curCost = nil, 0
		}
	}
	for _, seg := range segs {
		segCost := 0
		for _, i := range seg {
			segCost += cost[i]
		}
		if segCost > budget {
			flush()
			for _, i := range seg {
				if curCost+cost[i] > budget {
					flush()
				}
				cur, curCost = append(cur, i), curCost+cost[i]
			}
			flush()
			continue
		}
		if curCost+segCost > budget {
			flush()
		}
		cur, curCost = append(cur, seg...), curCost+segCost
	}
	flush()
	for len(groups) > maxGroups { // merge the adjacent pair with the smallest cost
		best, bestCost := 0, -1
		for g := 0; g+1 < len(groups); g++ {
			c := 0
			for _, i := range groups[g] {
				c += cost[i]
			}
			for _, i := range groups[g+1] {
				c += cost[i]
			}
			if bestCost < 0 || c < bestCost {
				best, bestCost = g, c
			}
		}
		groups[best] = append(groups[best], groups[best+1]...)
		groups = append(groups[:best+1], groups[best+2:]...)
	}
	return groups
}

func allIdx(n int) []int {
	g := make([]int, n)
	for i := range g {
		g[i] = i
	}
	return g
}
