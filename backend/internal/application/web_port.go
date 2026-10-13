package application

import "context"

// Port of the web tool (internal/webtool, docs/architecture/agent-web.md). Optional: with no WebRunner set a
// web request is dropped with a plain notice and nothing is fetched.

// WebToolName is the tool grant and the tool name in tool requests.
const WebToolName = "web"

// WebCall is a web tool request on its way to the runner.
type WebCall struct {
	Org, AgentID, TaskID string
	Action               string // search | fetch
	Args                 map[string]any
}

// WebResult is the outcome of one web call.
type WebResult struct {
	// Status: ok | denied | failed
	Status     string
	DenyReason string
	Summary    string
	// Block is the sanitized, delimited <untrusted_data> block for the runtime.
	Block string
	// Notice is a trusted status line for the agent (never page content).
	Notice             string
	CostUSD            float64
	Tainted            bool
	InjectionSuspected bool
}

// WebRunner runs web searches and fetches under the organization's policy, quotas and audit.
type WebRunner interface {
	Run(ctx context.Context, c WebCall) WebResult
}

// SetWebRunner installs the web tool. Without it web requests are not served.
func (o *Orchestrator) SetWebRunner(w WebRunner) { o.web = w }
