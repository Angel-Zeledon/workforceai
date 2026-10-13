package sandbox

import (
	"context"
	"fmt"
	"strings"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Desk mode (docs/architecture/code-sandbox.md, "Desk mode"): an OPT-IN, per
// organization mode in which a code.run confined to the requesting agent's own
// files starts without a human approval. Off by default; with it off nothing in
// this file changes the behavior of Prepare or Execute.

// Values of Settings.AutoRun.
const (
	AutoOff  = "off"
	AutoDesk = application.AutoRunDesk
)

// Desk-mode caps. The defaults are smaller than the approval-mode caps and an
// organization can raise them only up to the Auto ceilings, which are still
// smaller than MaxCodeBytes / MaxInputBytes.
const (
	DefaultAutoMaxCodeBytes  = 16 << 10
	DefaultAutoMaxInputBytes = 5 << 20
	DefaultAutoRunsPerTask   = 5
	CeilingAutoMaxCodeBytes  = 32 << 10
	CeilingAutoMaxInputBytes = 10 << 20
	CeilingAutoRunsPerTask   = 20
	maxAutoDetailRunes       = 160
)

// Stable reasons why a desk-mode request still needs a human approval.
const (
	AutoNoScope            = "no_scope"
	AutoPolicy             = "policy_requires_approval"
	AutoScopeMismatch      = "scope_mismatch"
	AutoCodeTooLarge       = "code_too_large"
	AutoInputsTooLarge     = "inputs_too_large"
	AutoInputOutsideScope  = "input_outside_scope"
	AutoInputUnverifiable  = "input_unverifiable"
	AutoTaskRunLimit       = "task_run_limit"
	AutoTaskLimitUnchecked = "task_limit_unavailable"
	// CodeAutoNotAllowed denies a desk-mode run whose conditions no longer hold at execution time.
	CodeAutoNotAllowed = "auto_run_not_allowed"
)

// FileLoc is the canonical location of a drive file.
type FileLoc struct{ ID, Path string }

// FileLocator resolves an input reference (file id or path) to its canonical
// location. A FileSource that does not implement it can never auto-run.
type FileLocator interface {
	Locate(ctx context.Context, org, ref string) (FileLoc, error)
}

// TaskRunCounter counts the desk-mode runs of one task that started (optional
// capability of the Store; without it desk mode falls back to approvals).
type TaskRunCounter interface {
	CountAutoRunsOfTask(ctx context.Context, org, taskID string) (int, error)
}

func normalizeAutoRun(m string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "", AutoOff:
		return AutoOff, true
	case AutoDesk:
		return AutoDesk, true
	}
	return "", false
}

// NormalizeAutoRun maps a stored value to "off" | "desk" (anything unknown is "off", false).
func NormalizeAutoRun(m string) (string, bool) {
	v, ok := normalizeAutoRun(m)
	if !ok {
		return AutoOff, false
	}
	return v, true
}

func (s Settings) autoMaxCode() int {
	if s.AutoMaxCodeBytes > 0 {
		return min(s.AutoMaxCodeBytes, CeilingAutoMaxCodeBytes)
	}
	return DefaultAutoMaxCodeBytes
}

func (s Settings) autoMaxInput() int64 {
	if s.AutoMaxInputBytes > 0 {
		return min(s.AutoMaxInputBytes, CeilingAutoMaxInputBytes)
	}
	return DefaultAutoMaxInputBytes
}

func (s Settings) autoMaxPerTask() int {
	if s.AutoMaxRunsPerTask > 0 {
		return min(s.AutoMaxRunsPerTask, CeilingAutoRunsPerTask)
	}
	return DefaultAutoRunsPerTask
}

// AutoCaps are the effective desk-mode caps of the settings (for the API view).
func (s Settings) AutoCaps() (codeBytes int, inputBytes int64, perTask int) {
	return s.autoMaxCode(), s.autoMaxInput(), s.autoMaxPerTask()
}

// validateAuto checks the desk-mode fields of a PUT. prev is the stored value:
// turning desk mode ON needs the explicit acknowledgement.
func validateAuto(in *Settings, prev Settings) error {
	m, ok := normalizeAutoRun(in.AutoRun)
	if !ok {
		return fmt.Errorf("%w: auto_run must be %q or %q", domain.ErrInvalid, AutoOff, AutoDesk)
	}
	in.AutoRun = m
	if in.AutoMaxCodeBytes < 0 || in.AutoMaxCodeBytes > CeilingAutoMaxCodeBytes ||
		in.AutoMaxInputBytes < 0 || in.AutoMaxInputBytes > CeilingAutoMaxInputBytes ||
		in.AutoMaxRunsPerTask < 0 || in.AutoMaxRunsPerTask > CeilingAutoRunsPerTask {
		return fmt.Errorf("%w: invalid desk mode limits", domain.ErrInvalid)
	}
	if m == AutoDesk && prev.AutoRun != AutoDesk && !in.AcknowledgeRisk {
		return fmt.Errorf("%w: turning on desk mode needs acknowledge_risk=true", domain.ErrInvalid)
	}
	in.AcknowledgeRisk = false // request-only, never stored
	return nil
}

// AutoRunMode implements application.CodeAutoModer: "desk" only when code
// execution is enabled AND the organization opted in; "off" on any doubt.
func (s *Service) AutoRunMode(ctx context.Context, org string) string {
	st, err := s.Store.GetSettings(ctx, org)
	if err != nil || !st.Enabled {
		return AutoOff
	}
	if m, ok := normalizeAutoRun(st.AutoRun); ok {
		return m
	}
	return AutoOff
}

var _ application.CodeAutoModer = (*Service)(nil)

func inFolder(p, folder string) bool {
	folder = strings.TrimRight(folder, "/")
	return folder != "" && strings.HasPrefix(p, folder+"/")
}

// inScope reports whether a file may be read by a desk-mode run: inside the
// agent's own desk, inside the project folder of the task, or one of the
// user's attachments of this conversation. Nothing else (other agents' desks,
// /Shared files that are not attachments, other projects) qualifies.
func inScope(loc FileLoc, sc *application.CodeScope) bool {
	if loc.Path == "" || !strings.HasPrefix(loc.Path, "/") || strings.Contains(loc.Path, "/../") {
		return false
	}
	if strings.HasPrefix(sc.Desk, "/Agents/") && inFolder(loc.Path, sc.Desk) {
		return true
	}
	if strings.HasPrefix(sc.Project, "/Projects/") && inFolder(loc.Path, sc.Project) {
		return true
	}
	for _, a := range sc.Attached {
		if (a.FileID != "" && a.FileID == loc.ID) || (a.Path != "" && a.Path == loc.Path) {
			return true
		}
	}
	return false
}

// autoCheck decides whether a run may start without a human. It returns ""
// when it may, otherwise a stable reason plus a short detail. It is called at
// Prepare and again at Execute (time of use). The conditions that are not about
// the request itself (kill switch, pause, read-only mode, the daily limit, no
// network, no secrets) are enforced by Prepare and Execute for every run.
func (s *Service) autoCheck(ctx context.Context, org string, st Settings, sc *application.CodeScope, agentID, taskID, code string, refs []string, ins []InputInfo) (string, string) {
	switch {
	case sc == nil:
		return AutoNoScope, ""
	case !sc.PolicyClean:
		return AutoPolicy, ""
	case sc.AgentID != agentID || sc.TaskID != taskID || agentID == "":
		return AutoScopeMismatch, ""
	case len(code) > st.autoMaxCode():
		return AutoCodeTooLarge, fmt.Sprintf("%d > %d bytes", len(code), st.autoMaxCode())
	}
	var total int64
	for _, i := range ins {
		total += i.Size
	}
	if total > st.autoMaxInput() {
		return AutoInputsTooLarge, fmt.Sprintf("%d > %d bytes", total, st.autoMaxInput())
	}
	if len(refs) > 0 {
		loc, ok := s.Files.(FileLocator)
		if !ok {
			return AutoInputUnverifiable, ""
		}
		for _, ref := range refs {
			l, err := loc.Locate(ctx, org, ref)
			if err != nil {
				return AutoInputUnverifiable, truncateRunes(ref, maxAutoDetailRunes)
			}
			if !inScope(l, sc) {
				return AutoInputOutsideScope, truncateRunes(l.Path, maxAutoDetailRunes)
			}
		}
	}
	cnt, ok := s.Store.(TaskRunCounter)
	if !ok {
		return AutoTaskLimitUnchecked, ""
	}
	n, err := cnt.CountAutoRunsOfTask(ctx, org, taskID)
	if err != nil {
		return AutoTaskLimitUnchecked, ""
	}
	if n >= st.autoMaxPerTask() {
		return AutoTaskRunLimit, fmt.Sprintf("%d runs of this task", n)
	}
	return "", ""
}

// autoFallbackText is the English sentence for the reason codes (the UI
// translates the code; this is the fallback and the text on the approval).
func autoFallbackText(code, detail string) string {
	t := map[string]string{
		AutoNoScope:            "the files this run may use could not be determined",
		AutoPolicy:             "an organization rule or the agent's autonomy level requires a person to decide",
		AutoScopeMismatch:      "the run does not match the task it was requested for",
		AutoCodeTooLarge:       "the code is larger than the desk-mode limit",
		AutoInputsTooLarge:     "the input files are larger than the desk-mode limit",
		AutoInputOutsideScope:  "an input file is outside this agent's desk, its project folder and your attachments",
		AutoInputUnverifiable:  "an input file could not be verified to be in this agent's own files",
		AutoTaskRunLimit:       "this task already used its runs without approval",
		AutoTaskLimitUnchecked: "the per-task run count could not be checked",
	}[code]
	if t == "" {
		t = code
	}
	if detail != "" {
		t += " (" + detail + ")"
	}
	return t
}
