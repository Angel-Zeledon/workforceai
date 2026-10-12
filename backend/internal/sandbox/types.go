// Package sandbox implements the approval-gated code sandbox (tool code.run).
//
// An agent can ask to run Python, but the request ALWAYS becomes a high-risk
// approval that shows the full code, the input files, the purpose and the
// limits. Only after a human approves does the backend send the run to the
// sandbox runner (a separate service, the only component with Docker access),
// which executes it in a disposable container without network. See
// docs/architecture/code-sandbox.md.
package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Tool and action names of the sandbox.
const (
	Tool       = "code"
	Action     = "run"
	LangPython = "python"
)

// Run statuses.
const (
	StatusAwaiting  = "awaiting_approval"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded" // exit code 0
	StatusFailed    = "failed"    // non-zero exit code, or the runner failed
	StatusTimeout   = "timeout"
	StatusAborted   = "aborted" // kill switch, pause or read-only mode while running
	StatusRejected  = "rejected"
	StatusDenied    = "denied" // approved but not run (see DenyReason)
)

// Stable deny / error codes.
const (
	CodeDisabled         = "code_execution_disabled"
	CodeInvalid          = "code_invalid"
	CodeDailyLimit       = "code_daily_limit"
	CodeApprovalMismatch = "approval_mismatch"
	CodeInputsChanged    = "inputs_changed"
	CodeUnavailable      = "sandbox_unavailable"
	CodeNotGranted       = "code_tool_not_granted"
)

// Errors returned by Prepare. Each carries a stable code.
type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

// CodeOf returns the stable code of an error from this package ("" otherwise).
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func invalid(format string, a ...any) error {
	return &Error{Code: CodeInvalid, Detail: fmt.Sprintf(format, a...)}
}

// Limits are the resource limits of one run. They are shown on the approval
// card and enforced by the runner (which also applies its own ceilings).
type Limits struct {
	TimeoutSeconds     int     `json:"timeout_seconds"`
	MemoryMB           int     `json:"memory_mb"`
	CPUs               float64 `json:"cpus"`
	PIDs               int     `json:"pids"`
	MaxOutputBytes     int64   `json:"max_output_bytes"`      // sum of produced files
	MaxOutputFileBytes int64   `json:"max_output_file_bytes"` // one produced file
	MaxOutputFiles     int     `json:"max_output_files"`
	MaxStdioBytes      int     `json:"max_stdio_bytes"` // stdout and stderr, each
}

// Hard ceilings of the model: nothing configurable goes above them.
const (
	HardMaxTimeoutSeconds = 600
	HardMaxMemoryMB       = 2048
)

// DefaultLimits are the limits of a run unless the environment changes them.
func DefaultLimits() Limits {
	return Limits{TimeoutSeconds: 60, MemoryMB: 512, CPUs: 1, PIDs: 128,
		MaxOutputBytes: 10 << 20, MaxOutputFileBytes: 5 << 20, MaxOutputFiles: 10, MaxStdioBytes: 64 << 10}
}

// Clamp returns l with every value limited to ceil (zero values take the ceiling).
func (l Limits) Clamp(ceil Limits) Limits {
	minI := func(a, b int) int {
		if a <= 0 || a > b {
			return b
		}
		return a
	}
	minI64 := func(a, b int64) int64 {
		if a <= 0 || a > b {
			return b
		}
		return a
	}
	l.TimeoutSeconds = minI(l.TimeoutSeconds, minI(ceil.TimeoutSeconds, HardMaxTimeoutSeconds))
	l.MemoryMB = minI(l.MemoryMB, minI(ceil.MemoryMB, HardMaxMemoryMB))
	if l.CPUs <= 0 || l.CPUs > ceil.CPUs {
		l.CPUs = ceil.CPUs
	}
	l.PIDs = minI(l.PIDs, ceil.PIDs)
	l.MaxOutputBytes = minI64(l.MaxOutputBytes, ceil.MaxOutputBytes)
	l.MaxOutputFileBytes = minI64(l.MaxOutputFileBytes, ceil.MaxOutputFileBytes)
	l.MaxOutputFiles = minI(l.MaxOutputFiles, ceil.MaxOutputFiles)
	l.MaxStdioBytes = minI(l.MaxStdioBytes, ceil.MaxStdioBytes)
	return l
}

// Input size / count caps of a request.
const (
	MaxCodeBytes     = 64 << 10
	MaxInputFiles    = 10
	MaxInputBytes    = 25 << 20 // all inputs together
	MaxPurposeRunes  = 500
	MaxExpectedNames = 10
)

// AllowedOutputExt are the extensions of produced files that are collected
// (everything else is dropped). No html/svg/js: they could carry active content.
var AllowedOutputExt = map[string]bool{
	".csv": true, ".tsv": true, ".xlsx": true, ".png": true, ".jpg": true, ".jpeg": true,
	".pdf": true, ".txt": true, ".json": true, ".md": true,
}

// Request is the normalized code.run request of an agent.
type Request struct {
	Language        string   `json:"language"`
	Code            string   `json:"code"`
	InputFiles      []string `json:"input_files"`
	Purpose         string   `json:"purpose"`
	ExpectedOutputs []string `json:"expected_outputs"`
}

// ParseArgs validates and normalizes the args of a code.run tool request.
// Unknown keys are ignored (and therefore not part of the approval hash).
func ParseArgs(args map[string]any) (Request, error) {
	var r Request
	str := func(k string) (string, bool) {
		v, ok := args[k]
		if !ok || v == nil {
			return "", true
		}
		s, ok := v.(string)
		return s, ok
	}
	list := func(k string) ([]string, error) {
		v, ok := args[k]
		if !ok || v == nil {
			return nil, nil
		}
		raw, ok := v.([]any)
		if !ok {
			if ss, ok2 := v.([]string); ok2 {
				return ss, nil
			}
			return nil, invalid("%s must be a list of strings", k)
		}
		out := make([]string, 0, len(raw))
		for _, x := range raw {
			s, ok := x.(string)
			if !ok {
				return nil, invalid("%s must be a list of strings", k)
			}
			out = append(out, s)
		}
		return out, nil
	}
	var ok bool
	if r.Language, ok = str("language"); !ok {
		return r, invalid("language must be a string")
	}
	r.Language = strings.ToLower(strings.TrimSpace(r.Language))
	if r.Language != LangPython {
		return r, invalid("only language %q is supported", LangPython)
	}
	if r.Code, ok = str("code"); !ok {
		return r, invalid("code must be a string")
	}
	if strings.TrimSpace(r.Code) == "" {
		return r, invalid("code is required")
	}
	if len(r.Code) > MaxCodeBytes {
		return r, invalid("code is larger than %d bytes", MaxCodeBytes)
	}
	if !utf8.ValidString(r.Code) || strings.ContainsRune(r.Code, 0) {
		return r, invalid("code must be valid UTF-8 text")
	}
	r.Code = strings.ReplaceAll(r.Code, "\r\n", "\n")
	if r.Purpose, ok = str("purpose"); !ok {
		return r, invalid("purpose must be a string")
	}
	r.Purpose = strings.TrimSpace(r.Purpose)
	if r.Purpose == "" {
		return r, invalid("purpose is required: say why the code needs to run")
	}
	if utf8.RuneCountInString(r.Purpose) > MaxPurposeRunes {
		return r, invalid("purpose is longer than %d characters", MaxPurposeRunes)
	}
	ids, err := list("input_files")
	if err != nil {
		return r, err
	}
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > 200 {
			return r, invalid("input_files has an empty or too long id")
		}
		if !seen[id] {
			seen[id] = true
			r.InputFiles = append(r.InputFiles, id)
		}
	}
	if len(r.InputFiles) > MaxInputFiles {
		return r, invalid("at most %d input files", MaxInputFiles)
	}
	outs, err := list("expected_outputs")
	if err != nil {
		return r, err
	}
	for _, n := range outs {
		n = strings.TrimSpace(n)
		if !ValidFileName(n) || !AllowedOutputExt[strings.ToLower(path.Ext(n))] {
			return r, invalid("expected output %q must be a plain file name with an allowed extension", n)
		}
		r.ExpectedOutputs = append(r.ExpectedOutputs, n)
	}
	if len(r.ExpectedOutputs) > MaxExpectedNames {
		return r, invalid("at most %d expected outputs", MaxExpectedNames)
	}
	return r, nil
}

// ValidFileName reports whether n is a plain, safe file name (no path, no dots-only).
func ValidFileName(n string) bool {
	if n == "" || len(n) > 120 || n == "." || n == ".." || strings.HasPrefix(n, ".") {
		return false
	}
	for _, r := range n {
		if r < 0x20 || r == '/' || r == '\\' || r == 0x7f || r == ':' {
			return false
		}
	}
	return utf8.ValidString(n)
}

// InputInfo describes an input file of an approved run. SHA256 binds the
// approval to the exact content that was shown, not only to the file id.
type InputInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Hash binds an approval to the exact code and inputs (args_hash pattern):
// any change of the code, language, input contents or expected outputs gives a
// different hash, so it needs a new approval.
func Hash(r Request, inputs []InputInfo) string {
	type in struct {
		ID     string `json:"id"`
		SHA256 string `json:"sha256"`
	}
	ins := make([]in, 0, len(inputs))
	for _, i := range inputs {
		ins = append(ins, in{i.ID, i.SHA256})
	}
	sort.Slice(ins, func(a, b int) bool { return ins[a].ID < ins[b].ID })
	outs := append([]string(nil), r.ExpectedOutputs...)
	sort.Strings(outs)
	b, _ := json.Marshal(map[string]any{"tool": Tool, "action": Action, "language": r.Language, "code": r.Code, "inputs": ins, "expected_outputs": outs})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// CodeHash is the hash of the code alone (audit trail).
func CodeHash(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])
}

// OutputInfo is a file produced by a run, stored through the FileSink.
type OutputInfo struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	FileRef string `json:"file_ref,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Run is the record of one code.run request, from approval to result.
type Run struct {
	ID         string `json:"id"`
	OrgID      string `json:"org_id"`
	AgentID    string `json:"agent_id"`
	TaskID     string `json:"task_id"`
	RequestID  string `json:"request_id"`
	OnBehalfOf string `json:"on_behalf_of,omitempty"`
	ApprovalID string `json:"approval_id"`

	Language        string      `json:"language"`
	Code            string      `json:"code"`
	CodeHash        string      `json:"code_hash"`
	Hash            string      `json:"args_hash"`
	Purpose         string      `json:"purpose"`
	ExpectedOutputs []string    `json:"expected_outputs"`
	Inputs          []InputInfo `json:"inputs"`
	Limits          Limits      `json:"limits"`

	Status     string   `json:"status"`
	DenyReason string   `json:"deny_reason,omitempty"`
	Approvers  []string `json:"approvers,omitempty"`
	ExitCode   *int     `json:"exit_code,omitempty"`
	DurationMS int64    `json:"duration_ms"`

	Stdout          string       `json:"stdout"`
	Stderr          string       `json:"stderr"`
	StdoutTruncated bool         `json:"stdout_truncated"`
	StderrTruncated bool         `json:"stderr_truncated"`
	Outputs         []OutputInfo `json:"outputs"`

	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Settings is the per-organization sandbox configuration. The zero value
// (disabled) is the default: code execution is off until an owner turns it on.
type Settings struct {
	Enabled bool `json:"enabled"`
	// MaxRunsPerDay and MaxRuntimeSeconds lower the platform ceilings for this
	// organization (0 = use the platform value).
	MaxRunsPerDay     int       `json:"max_runs_per_day"`
	MaxRuntimeSeconds int       `json:"max_runtime_seconds"`
	UpdatedBy         string    `json:"updated_by,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

// ---- runner wire protocol (backend <-> sandbox-runner) ----

// RunFile is a file sent to or received from the runner.
type RunFile struct {
	Name string `json:"name"`
	Data []byte `json:"data"` // base64 in JSON
}

// RunRequest is what the backend sends to POST /v1/run of the runner. It never
// carries credentials: only the code, copies of the approved inputs and limits.
type RunRequest struct {
	RunID  string    `json:"run_id"`
	OrgID  string    `json:"org_id"`
	Code   string    `json:"code"`
	Files  []RunFile `json:"files"`
	Limits Limits    `json:"limits"`
}

// Runner statuses.
const (
	RunOK          = "ok"
	RunError       = "error" // non-zero exit code
	RunTimeout     = "timeout"
	RunAborted     = "aborted"
	RunOutputLimit = "output_limit"
	RunFailed      = "runner_failed" // infrastructure problem (docker, malformed result)
)

// RunResponse is the runner's answer.
type RunResponse struct {
	Status          string    `json:"status"`
	ExitCode        int       `json:"exit_code"`
	DurationMS      int64     `json:"duration_ms"`
	Stdout          string    `json:"stdout"`
	Stderr          string    `json:"stderr"`
	StdoutTruncated bool      `json:"stdout_truncated"`
	StderrTruncated bool      `json:"stderr_truncated"`
	Files           []RunFile `json:"files"`
	Dropped         []string  `json:"dropped,omitempty"` // produced files not collected (extension, size, count)
	Message         string    `json:"message,omitempty"`
}
