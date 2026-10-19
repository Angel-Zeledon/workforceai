package application

import (
	"context"
	"math"
	"slices"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// CostBreakdown is the spend broken down by agent, task, tool, operation and
// model (improvement #3). Every number comes from the usage ledger, i.e. from
// reconciled runtime calls.
type CostBreakdown struct {
	// RequestID is set when the breakdown is limited to one request.
	RequestID string  `json:"request_id,omitempty"`
	TotalUSD  float64 `json:"total_usd"`
	Calls     int     `json:"calls"`
	// InputTokens / OutputTokens are the token totals of the same calls.
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	// UntrackedUSD is cost recorded on requests before the ledger existed (it
	// has no breakdown). Zero for data created with the ledger.
	UntrackedUSD float64 `json:"untracked_usd"`
	// LLMOnlyUSD is the cost of calls that produced no tool request (so it is
	// not attributed to any tool).
	LLMOnlyUSD  float64         `json:"llm_only_usd"`
	ByAgent     []AgentCost     `json:"by_agent"`
	ByTask      []TaskCost      `json:"by_task"`
	ByTool      []ToolCost      `json:"by_tool"`
	ByOperation []OperationCost `json:"by_operation"`
	ByModel     []ModelCost     `json:"by_model"`
	// ByCall lists the calls one by one (oldest first, at most maxBreakdownCalls) with their token counts. Only
	// when the breakdown is limited to one request; it answers "which call was the expensive one".
	ByCall []CallCost `json:"by_call"`
}

// maxBreakdownCalls bounds the per-call list of a request breakdown.
const maxBreakdownCalls = 500

// CallCost is one reconciled runtime call of the ledger.
type CallCost struct {
	TS           time.Time        `json:"ts"`
	TaskID       string           `json:"task_id,omitempty"`
	AgentID      string           `json:"agent_id"`
	Kind         domain.UsageKind `json:"kind"`
	Model        string           `json:"model"`
	InputTokens  int              `json:"input_tokens"`
	OutputTokens int              `json:"output_tokens"`
	CostUSD      float64          `json:"cost_usd"`
}

type AgentCost struct {
	AgentID      string  `json:"agent_id"`
	CostUSD      float64 `json:"cost_usd"`
	Calls        int     `json:"calls"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
}

type TaskCost struct {
	TaskID    string  `json:"task_id"`
	RequestID string  `json:"request_id"`
	Title     string  `json:"title"`
	AgentID   string  `json:"agent_id"`
	CostUSD   float64 `json:"cost_usd"`
	Calls     int     `json:"calls"`
	// InputTokens / OutputTokens: tokens of this task's calls (additive).
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ToolCost: the cost of a call that requested N tools is split equally among
// them. Tools execute at no LLM cost in this phase, so this is the LLM spend
// that led to each tool request.
type ToolCost struct {
	Tool    string  `json:"tool"` // "tool.action"
	Calls   int     `json:"calls"`
	CostUSD float64 `json:"cost_usd"`
}

type OperationCost struct {
	Kind         domain.UsageKind `json:"kind"`
	Calls        int              `json:"calls"`
	CostUSD      float64          `json:"cost_usd"`
	InputTokens  int              `json:"input_tokens"`
	OutputTokens int              `json:"output_tokens"`
}

type ModelCost struct {
	Model        string  `json:"model"`
	Calls        int     `json:"calls"`
	CostUSD      float64 `json:"cost_usd"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// CostBreakdown aggregates the ledger. requestID "" means the whole organization.
func (q *Queries) CostBreakdown(ctx context.Context, requestID string) (CostBreakdown, error) {
	out := CostBreakdown{RequestID: requestID, ByAgent: []AgentCost{}, ByTask: []TaskCost{}, ByTool: []ToolCost{},
		ByOperation: []OperationCost{}, ByModel: []ModelCost{}, ByCall: []CallCost{}}
	usage, err := q.Store.ListUsage(ctx, q.org(ctx))
	if err != nil {
		return out, err
	}
	reqs, err := q.Store.ListRequests(ctx, q.org(ctx))
	if err != nil {
		return out, err
	}
	var recorded float64
	for _, r := range reqs {
		if requestID == "" || r.ID == requestID {
			recorded += r.CostUSD
		}
	}
	tasks, err := q.Store.ListTasks(ctx, q.org(ctx), "", "")
	if err != nil {
		return out, err
	}
	taskInfo := map[string]domain.Task{}
	for _, t := range tasks {
		taskInfo[t.ID] = t
	}

	agents := map[string]*AgentCost{}
	taskCosts := map[string]*TaskCost{}
	toolCosts := map[string]*ToolCost{}
	ops := map[domain.UsageKind]*OperationCost{}
	models := map[string]*ModelCost{}
	for _, u := range usage {
		if requestID != "" && u.RequestID != requestID {
			continue
		}
		out.TotalUSD += u.CostUSD
		out.Calls++
		out.InputTokens += u.InputTokens
		out.OutputTokens += u.OutputTokens
		if requestID != "" && len(out.ByCall) < maxBreakdownCalls {
			out.ByCall = append(out.ByCall, CallCost{TS: u.TS, TaskID: u.TaskID, AgentID: u.AgentID, Kind: u.Kind, Model: u.Model,
				InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CostUSD: round6(u.CostUSD)})
		}
		a := agents[u.AgentID]
		if a == nil {
			a = &AgentCost{AgentID: u.AgentID}
			agents[u.AgentID] = a
		}
		a.CostUSD += u.CostUSD
		a.Calls++
		a.InputTokens += u.InputTokens
		a.OutputTokens += u.OutputTokens
		if u.TaskID != "" {
			t := taskCosts[u.TaskID]
			if t == nil {
				info := taskInfo[u.TaskID]
				t = &TaskCost{TaskID: u.TaskID, RequestID: u.RequestID, Title: info.Title, AgentID: info.AgentID}
				taskCosts[u.TaskID] = t
			}
			t.CostUSD += u.CostUSD
			t.Calls++
			t.InputTokens += u.InputTokens
			t.OutputTokens += u.OutputTokens
		}
		o := ops[u.Kind]
		if o == nil {
			o = &OperationCost{Kind: u.Kind}
			ops[u.Kind] = o
		}
		o.CostUSD += u.CostUSD
		o.Calls++
		o.InputTokens += u.InputTokens
		o.OutputTokens += u.OutputTokens
		mname := u.Model
		if mname == "" {
			mname = "unknown"
		}
		m := models[mname]
		if m == nil {
			m = &ModelCost{Model: mname}
			models[mname] = m
		}
		m.CostUSD += u.CostUSD
		m.Calls++
		m.InputTokens += u.InputTokens
		m.OutputTokens += u.OutputTokens
		if len(u.Tools) == 0 {
			out.LLMOnlyUSD += u.CostUSD
			continue
		}
		share := u.CostUSD / float64(len(u.Tools))
		for _, name := range u.Tools {
			tc := toolCosts[name]
			if tc == nil {
				tc = &ToolCost{Tool: name}
				toolCosts[name] = tc
			}
			tc.CostUSD += share
			tc.Calls++
		}
	}
	for _, a := range agents {
		a.CostUSD = round6(a.CostUSD)
		out.ByAgent = append(out.ByAgent, *a)
	}
	for _, t := range taskCosts {
		t.CostUSD = round6(t.CostUSD)
		out.ByTask = append(out.ByTask, *t)
	}
	for _, t := range toolCosts {
		t.CostUSD = round6(t.CostUSD)
		out.ByTool = append(out.ByTool, *t)
	}
	for _, o := range ops {
		o.CostUSD = round6(o.CostUSD)
		out.ByOperation = append(out.ByOperation, *o)
	}
	for _, m := range models {
		m.CostUSD = round6(m.CostUSD)
		out.ByModel = append(out.ByModel, *m)
	}
	slices.SortFunc(out.ByCall, func(a, b CallCost) int { return a.TS.Compare(b.TS) })
	slices.SortFunc(out.ByAgent, func(a, b AgentCost) int { return cmpDesc(a.CostUSD, b.CostUSD, a.AgentID, b.AgentID) })
	slices.SortFunc(out.ByTask, func(a, b TaskCost) int { return cmpDesc(a.CostUSD, b.CostUSD, a.TaskID, b.TaskID) })
	slices.SortFunc(out.ByTool, func(a, b ToolCost) int { return cmpDesc(a.CostUSD, b.CostUSD, a.Tool, b.Tool) })
	slices.SortFunc(out.ByOperation, func(a, b OperationCost) int {
		return cmpDesc(a.CostUSD, b.CostUSD, string(a.Kind), string(b.Kind))
	})
	slices.SortFunc(out.ByModel, func(a, b ModelCost) int { return cmpDesc(a.CostUSD, b.CostUSD, a.Model, b.Model) })
	out.TotalUSD, out.LLMOnlyUSD = round6(out.TotalUSD), round6(out.LLMOnlyUSD)
	if d := recorded - out.TotalUSD; d > 1e-6 {
		out.UntrackedUSD = round6(d)
	}
	return out, nil
}

// cmpDesc orders by cost descending, then by name.
func cmpDesc(a, b float64, an, bn string) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	}
	return strings.Compare(an, bn)
}
