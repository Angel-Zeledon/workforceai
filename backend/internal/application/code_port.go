package application

import (
	"context"
	"errors"
)

// Port of the approval-gated code sandbox (internal/sandbox, docs/architecture/
// code-sandbox.md). Optional: with no CodeRunner set a code.run request is
// denied ("sandbox_unavailable") and nothing ever runs.

// CodeToolName and CodeActionRun identify the tool request that asks to run code.
const (
	CodeToolName  = "code"
	CodeActionRun = "run"
)

// CodeRunInput is a code.run request on its way to an approval.
type CodeRunInput struct {
	Org, AgentID, TaskID, RequestID, OnBehalfOf string
	// ApprovalID is the id the approval card will have; the run is bound to it.
	ApprovalID string
	Args       map[string]any
	// Scope (desk mode, code_auto.go) is what the backend itself knows about where
	// this agent may read without a human: never taken from the runtime. nil =
	// every run needs approval.
	Scope *CodeScope
}

// CodePlan is what a human must approve (structured, no secrets).
type CodePlan struct {
	RunID    string
	ArgsHash string
	Title    string
	Details  string
	// Card is the approval context (metadata; the full code is fetched by id).
	Card map[string]any
	// Auto: the organization is in desk mode and the run met every condition: it
	// starts without an approval (the orchestrator calls Execute with the same
	// ApprovalID). Always false when the organization did not opt in.
	Auto bool
}

// CodeExec asks to run an approved plan. The run is found by ApprovalID (the
// binding between what the human saw and what runs); ArgsHash, when set, must
// match the hash of the plan that was approved.
type CodeExec struct {
	ApprovalID, ArgsHash string
	Approvers            []string
	// Scope is re-sent for a desk-mode run so the sandbox re-checks it at the time of use.
	Scope *CodeScope
}

// CodeOutcome is the redacted result of a run, handed back to the orchestrator.
type CodeOutcome struct {
	// Status: succeeded | failed | timeout | aborted | denied
	Status     string
	DenyReason string // stable code when Status is denied
	Summary    string
	// Blocks are sanitized, delimited <untrusted_data> blocks for the runtime.
	Blocks   []string
	ExitCode int
	// OutputRefs are the references of the files written through the FileSink.
	OutputRefs []string
}

// CodeRunner prepares and executes approved code runs.
type CodeRunner interface {
	// Prepare validates a request and records a run that waits for approval.
	// The error is a *sandbox.Error with a stable code (disabled, invalid, limit).
	Prepare(ctx context.Context, in CodeRunInput) (CodePlan, error)
	// Execute runs an approved plan. It re-validates everything at execution
	// time (setting, kill switch, read-only mode, hash, inputs) and runs it at
	// most once.
	Execute(ctx context.Context, org string, ex CodeExec) CodeOutcome
	// Abandon closes the plan of an approval that will not run (reason
	// "rejected", "expired" or a block code).
	Abandon(ctx context.Context, org, approvalID, reason string)
}

// ErrCodeRunner is returned by helper code when no CodeRunner is installed.
var ErrCodeRunner = errors.New("code runner not configured")

// SetCodeRunner installs the code sandbox. Without it code.run is denied.
func (o *Orchestrator) SetCodeRunner(c CodeRunner) { o.code = c }
