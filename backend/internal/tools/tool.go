// Package tools defines the Tool interface, the permission Registry, the
// policy-enforcing Executor and in-memory FAKE implementations of email,
// calendar, crm, documents, spreadsheet and calculator.
package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"aiworkforce/backend/internal/policy"
)

// ActionSpec describes one action of a tool.
type ActionSpec struct {
	Name        string `json:"name"`
	ReadOnly    bool   `json:"read_only"`
	Description string `json:"description"`
}

// Call is what a Tool receives. It only reaches a Tool after policy allowed it.
type Call struct {
	Agent  policy.AgentIdentity
	Tool   string
	Action string
	Args   map[string]any
}

// Result is a tool outcome.
type Result struct {
	OK      bool           `json:"ok"`
	Summary string         `json:"summary"`
	Data    map[string]any `json:"data,omitempty"`
	// ExternalContent holds text written by third parties (email bodies,
	// documents). The orchestrator must hand it to the runtime as delimited
	// data (external_content), never as instructions.
	ExternalContent []string `json:"external_content,omitempty"`
}

// Tool is a capability agents can use.
type Tool interface {
	Name() string
	Actions() []ActionSpec
	Execute(ctx context.Context, c Call) (Result, error)
}

// ArgResolver is optionally implemented by tools. It returns TRUSTED facts
// derived from the tool's own state (e.g. the real recipient of a reply, the
// real type of a document). The executor overlays them on the agent-supplied
// args before asking policy, so an agent cannot hide what an action really does.
type ArgResolver interface {
	ResolveArgs(ctx context.Context, c Call) map[string]any
}

// Errors.
var (
	ErrInvalidArgs = errors.New("tools: invalid arguments")
	ErrNotFound    = errors.New("tools: not found")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidArgs, fmt.Sprintf(format, a...))
}

// Registry holds tools and per-agent permissions (coarse gate; fine-grained
// authorization is always policy's job).
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	perm  map[string]map[string]map[string]bool // agent -> tool -> action ("*" = all)
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}, perm: map[string]map[string]map[string]bool{}}
}

// Register adds a tool (replacing one with the same name).
func (r *Registry) Register(t Tool) error {
	n := policy.NormalizeName(t.Name())
	if !policy.ValidName(n) {
		return fmt.Errorf("tools: invalid tool name %q", t.Name())
	}
	r.mu.Lock()
	r.tools[n] = t
	r.mu.Unlock()
	return nil
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[policy.NormalizeName(name)]
	return t, ok
}

// Names lists registered tools.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tools))
	for n := range r.tools {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Allow lets agent use tool; with no actions, every action of the tool.
func (r *Registry) Allow(agent, tool string, actions ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tool = policy.NormalizeName(tool)
	if r.perm[agent] == nil {
		r.perm[agent] = map[string]map[string]bool{}
	}
	if r.perm[agent][tool] == nil {
		r.perm[agent][tool] = map[string]bool{}
	}
	if len(actions) == 0 {
		r.perm[agent][tool]["*"] = true
		return
	}
	for _, a := range actions {
		r.perm[agent][tool][policy.NormalizeName(a)] = true
	}
}

// Revoke removes agent's access to tool.
func (r *Registry) Revoke(agent, tool string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.perm[agent], policy.NormalizeName(tool))
}

// Permitted reports whether agent may use tool.action at registry level.
func (r *Registry) Permitted(agent, tool, action string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m := r.perm[agent][policy.NormalizeName(tool)]
	return m["*"] || m[policy.NormalizeName(action)]
}

// ToolsFor lists tool names an agent has access to (for Agent.tools in the API).
func (r *Registry) ToolsFor(agent string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []string
	for t, m := range r.perm[agent] {
		if len(m) > 0 {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// ToolRequest is the runtime's tool_requests item: {tool, action, args, risk}.
type ToolRequest struct {
	Tool   string         `json:"tool"`
	Action string         `json:"action"`
	Args   map[string]any `json:"args"`
	Risk   string         `json:"risk"`
}

// ToolCall translates a runtime tool_request into a policy.ToolCall. source is
// informational (e.g. task id).
func (r ToolRequest) ToolCall(source string) (policy.ToolCall, error) {
	tool, action := policy.NormalizeName(r.Tool), policy.NormalizeName(r.Action)
	if tool == "" || action == "" {
		return policy.ToolCall{}, fmt.Errorf("tools: tool_request needs tool and action")
	}
	risk := strings.ToLower(strings.TrimSpace(r.Risk))
	switch risk {
	case "", "low", "medium", "high":
	default:
		risk = "high" // unknown risk labels are treated conservatively
	}
	return policy.ToolCall{Tool: tool, Action: action, Args: r.Args, Risk: risk, Source: source}, nil
}

// ToolRequestsFromRuntime converts a whole run-task tool_requests list.
func ToolRequestsFromRuntime(rs []ToolRequest, source string) ([]policy.ToolCall, error) {
	out := make([]policy.ToolCall, 0, len(rs))
	for i, r := range rs {
		c, err := r.ToolCall(source)
		if err != nil {
			return nil, fmt.Errorf("tool_request %d: %w", i, err)
		}
		out = append(out, c)
	}
	return out, nil
}

// SeedPermissions gives the 7 seed agents their tool sets (registry level).
func SeedPermissions(r *Registry) {
	for _, a := range []string{"assistant", "sales"} {
		for _, t := range []string{"email", "calendar", "crm", "documents", "spreadsheet", "calculator"} {
			r.Allow(a, t)
		}
	}
	for _, a := range []string{"hr"} {
		for _, t := range []string{"email", "calendar", "documents", "spreadsheet", "calculator"} {
			r.Allow(a, t)
		}
	}
	for _, t := range []string{"email", "crm", "documents", "calculator"} {
		r.Allow("legal", t)
	}
	for _, t := range []string{"spreadsheet", "calculator", "documents", "crm"} {
		r.Allow("accounting", t)
		r.Allow("analyst", t)
	}
	for _, t := range []string{"calendar", "spreadsheet", "calculator", "documents", "crm"} {
		r.Allow("operations", t)
	}
}
