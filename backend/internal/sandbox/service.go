package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/sanitize"
)

// AuditFunc writes an entry of the tamper-evident audit trail.
type AuditFunc func(ctx context.Context, org, actor, action, entity, entityID string, details map[string]any)

// EmitFunc publishes a realtime event (also the hook for the thought-audit
// trace of the V2 interfaces: they can subscribe to code.run.* events).
type EmitFunc func(ctx context.Context, org, eventType string, payload map[string]any)

// Service prepares and executes approval-gated code runs. It implements
// application.CodeRunner.
type Service struct {
	Store  Store
	Runner Runner
	Files  FileSource // optional: without it input_files are refused
	Sink   FileSink   // optional: without it produced files are not stored
	// Guard (optional) re-checks kill switch, agent pause, per-tool kill switch
	// and read-only mode at request time, at execution time and while running.
	Guard application.ExecutionGuard
	// Ceiling are the platform limits (env); organizations can only lower them.
	Ceiling Limits
	// MaxRunsPerDay is the platform ceiling of runs per organization per UTC day.
	MaxRunsPerDay int
	Audit         AuditFunc
	Emit          EmitFunc
	Suspects      *sanitize.Suspects
	Log           *slog.Logger
	Now           func() time.Time
	// Poll is how often a running run re-checks the guard (default 1 s).
	Poll time.Duration
}

var _ application.CodeRunner = (*Service)(nil)

// DefaultMaxRunsPerDay is the platform ceiling when none is configured.
const DefaultMaxRunsPerDay = 20

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) audit(ctx context.Context, org, actor, action, id string, d map[string]any) {
	if s.Audit != nil {
		s.Audit(ctx, org, actor, action, "code_run", id, d)
	}
}

func (s *Service) emit(ctx context.Context, org, typ string, p map[string]any) {
	if s.Emit != nil {
		s.Emit(ctx, org, typ, p)
	}
}

func newRunID() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return "run_" + hex.EncodeToString(b)
}

// Settings returns the effective settings of an organization (disabled by default).
func (s *Service) Settings(ctx context.Context, org string) (Settings, error) {
	return s.Store.GetSettings(ctx, org)
}

// PutSettings stores the organization settings (the caller checks the role).
func (s *Service) PutSettings(ctx context.Context, org, actor string, in Settings) (Settings, error) {
	if in.MaxRunsPerDay < 0 || in.MaxRuntimeSeconds < 0 || in.MaxRuntimeSeconds > HardMaxTimeoutSeconds {
		return Settings{}, fmt.Errorf("%w: invalid limits", domain.ErrInvalid)
	}
	in.UpdatedBy, in.UpdatedAt = actor, s.now()
	if err := s.Store.PutSettings(ctx, org, in); err != nil {
		return Settings{}, err
	}
	s.audit(ctx, org, actor, "code.settings.updated", org, map[string]any{"enabled": in.Enabled,
		"max_runs_per_day": in.MaxRunsPerDay, "max_runtime_seconds": in.MaxRuntimeSeconds})
	return in, nil
}

// effectiveLimits applies the organization overrides to the platform ceiling.
func (s *Service) effectiveLimits(st Settings) Limits {
	c := s.Ceiling.Clamp(DefaultLimits())
	if st.MaxRuntimeSeconds > 0 && st.MaxRuntimeSeconds < c.TimeoutSeconds {
		c.TimeoutSeconds = st.MaxRuntimeSeconds
	}
	return c
}

func (s *Service) runsPerDay(st Settings) int {
	m := s.MaxRunsPerDay
	if m <= 0 {
		m = DefaultMaxRunsPerDay
	}
	if st.MaxRunsPerDay > 0 && st.MaxRunsPerDay < m {
		m = st.MaxRunsPerDay
	}
	return m
}

func dayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// blocked returns a stable code when the controls forbid running code now
// ("" = allowed): kill switch, agent pause, per-tool kill switch, read-only mode.
func (s *Service) blocked(ctx context.Context, org, agentID string) string {
	g := s.Guard
	if g == nil {
		return ""
	}
	if v := g.Admit(ctx, org, agentID); !v.Allowed {
		if v.Code == "" {
			return "controls_unavailable"
		}
		return v.Code
	}
	if g.ToolBlocked(ctx, org, Tool, Action) {
		return "tool_disabled"
	}
	// Running code can have any effect: it counts as a side effect, so read-only
	// mode (organization or agent) blocks it. Errors block (fail closed).
	if g.SideEffectsBlocked(ctx, org, agentID) {
		return "read_only_mode"
	}
	return ""
}

// safeName turns a file name into a plain name usable inside the container.
func safeName(name string, idx int, used map[string]bool) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f || r == '/' || r == ':' {
			r = '_'
		}
		b.WriteRune(r)
	}
	name = strings.TrimLeft(b.String(), ".")
	if name == "" || !utf8.ValidString(name) {
		name = fmt.Sprintf("input_%d", idx+1)
	}
	if len(name) > 120 {
		name = name[len(name)-120:]
	}
	base := name
	for i := 2; used[strings.ToLower(name)]; i++ {
		name = fmt.Sprintf("%d_%s", i, base)
	}
	used[strings.ToLower(name)] = true
	return name
}

type inputData struct {
	Info InputInfo
	Data []byte
}

// readInputs reads the input files through the FileSource (bounded).
func (s *Service) readInputs(ctx context.Context, org string, ids []string) ([]inputData, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if s.Files == nil {
		return nil, invalid("input files are not available (no file source configured)")
	}
	var out []inputData
	used := map[string]bool{}
	total := int64(0)
	for i, id := range ids {
		name, rc, err := s.Files.Read(ctx, org, id)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrForbidden) {
				return nil, invalid("input file %q was not found", id)
			}
			return nil, &Error{Code: CodeUnavailable, Detail: "reading input file: " + err.Error()}
		}
		b, err := io.ReadAll(io.LimitReader(rc, MaxInputBytes-total+1))
		rc.Close()
		if err != nil {
			return nil, &Error{Code: CodeUnavailable, Detail: "reading input file: " + err.Error()}
		}
		total += int64(len(b))
		if total > MaxInputBytes {
			return nil, invalid("input files are larger than %d bytes in total", MaxInputBytes)
		}
		sum := sha256.Sum256(b)
		out = append(out, inputData{Info: InputInfo{ID: id, Name: safeName(name, i, used), Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:])}, Data: b})
	}
	return out, nil
}

// Prepare implements application.CodeRunner: it validates the request and
// records a run waiting for human approval. It NEVER runs anything.
func (s *Service) Prepare(ctx context.Context, in application.CodeRunInput) (application.CodePlan, error) {
	st, err := s.Store.GetSettings(ctx, in.Org)
	if err != nil {
		return application.CodePlan{}, &Error{Code: CodeUnavailable, Detail: "settings: " + err.Error()}
	}
	if !st.Enabled {
		return application.CodePlan{}, &Error{Code: CodeDisabled}
	}
	if c := s.blocked(ctx, in.Org, in.AgentID); c != "" {
		return application.CodePlan{}, &Error{Code: c}
	}
	req, err := ParseArgs(in.Args)
	if err != nil {
		return application.CodePlan{}, err
	}
	n, err := s.Store.CountStartedSince(ctx, in.Org, dayStart(s.now()))
	if err != nil {
		return application.CodePlan{}, &Error{Code: CodeUnavailable, Detail: err.Error()}
	}
	if n >= s.runsPerDay(st) {
		return application.CodePlan{}, &Error{Code: CodeDailyLimit, Detail: fmt.Sprintf("%d runs today", n)}
	}
	ins, err := s.readInputs(ctx, in.Org, req.InputFiles)
	if err != nil {
		return application.CodePlan{}, err
	}
	infos := make([]InputInfo, 0, len(ins))
	for _, i := range ins {
		infos = append(infos, i.Info)
	}
	lim := s.effectiveLimits(st)
	run := Run{ID: newRunID(), OrgID: in.Org, AgentID: in.AgentID, TaskID: in.TaskID, RequestID: in.RequestID, OnBehalfOf: in.OnBehalfOf,
		ApprovalID: in.ApprovalID, Language: req.Language, Code: req.Code, CodeHash: CodeHash(req.Code), Hash: Hash(req, infos),
		Purpose: req.Purpose, ExpectedOutputs: req.ExpectedOutputs, Inputs: infos, Limits: lim, Status: StatusAwaiting, CreatedAt: s.now()}
	if run.ExpectedOutputs == nil {
		run.ExpectedOutputs = []string{}
	}
	if run.Inputs == nil {
		run.Inputs = []InputInfo{}
	}
	if err := s.Store.CreateRun(ctx, run); err != nil {
		return application.CodePlan{}, &Error{Code: CodeUnavailable, Detail: err.Error()}
	}
	s.audit(ctx, in.Org, in.AgentID, "code.run.requested", run.ID, map[string]any{"code_hash": run.CodeHash, "args_hash": run.Hash,
		"approval_id": run.ApprovalID, "task_id": run.TaskID, "inputs": inputRefs(infos), "limits": lim, "lines": strings.Count(run.Code, "\n") + 1})
	files := make([]map[string]any, 0, len(infos))
	for _, i := range infos {
		files = append(files, map[string]any{"name": i.Name, "size": i.Size})
	}
	return application.CodePlan{
		RunID: run.ID, ArgsHash: run.Hash,
		Title:   fmt.Sprintf("Run Python code (%d lines)", strings.Count(run.Code, "\n")+1),
		Details: fmt.Sprintf("code.run: %s | code_hash=%s | inputs=%d | timeout=%ds | network=none", truncateRunes(run.Purpose, 200), run.CodeHash[:12], len(infos), lim.TimeoutSeconds),
		Card: map[string]any{"kind": "code_run", "run_id": run.ID, "code_hash": run.CodeHash, "args_hash": run.Hash, "purpose": run.Purpose,
			"input_files": files, "expected_outputs": run.ExpectedOutputs, "limits": lim, "language": run.Language},
	}, nil
}

func inputRefs(in []InputInfo) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, i := range in {
		out = append(out, map[string]any{"id": i.ID, "name": i.Name, "size": i.Size, "sha256": i.SHA256})
	}
	return out
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "..."
}

// Abandon implements application.CodeRunner.
func (s *Service) Abandon(ctx context.Context, org, approvalID, reason string) {
	run, err := s.Store.GetRunByApproval(ctx, org, approvalID)
	if err != nil {
		s.log().Warn("sandbox: abandon: run not found", "approval", approvalID, "err", err)
		return
	}
	runID := run.ID
	status := StatusRejected
	if reason != "rejected" {
		status = StatusDenied
	}
	ok, err := s.Store.CloseRun(context.WithoutCancel(ctx), org, runID, status, reason, s.now())
	if err != nil {
		s.log().Warn("sandbox: close run", "run", runID, "err", err)
		return
	}
	if ok {
		s.audit(ctx, org, "system", "code.run."+status, runID, map[string]any{"reason": reason})
		s.emit(ctx, org, "code.run.finished", map[string]any{"run_id": runID, "status": status, "reason": reason})
	}
}

func (s *Service) deny(ctx context.Context, org string, run Run, code string) application.CodeOutcome {
	if ok, err := s.Store.CloseRun(context.WithoutCancel(ctx), org, run.ID, StatusDenied, code, s.now()); err != nil {
		s.log().Warn("sandbox: close run", "run", run.ID, "err", err)
	} else if ok {
		s.audit(ctx, org, run.AgentID, "code.run.denied", run.ID, map[string]any{"reason": code, "code_hash": run.CodeHash,
			"approval_id": run.ApprovalID, "task_id": run.TaskID})
		s.emit(ctx, org, "code.run.finished", map[string]any{"run_id": run.ID, "status": StatusDenied, "reason": code, "agent_id": run.AgentID})
	}
	return application.CodeOutcome{Status: "denied", DenyReason: code, Summary: "code was approved but not run: " + code}
}

// Execute implements application.CodeRunner. It is only reachable with an
// approval id; everything is re-validated here (time of use).
func (s *Service) Execute(ctx context.Context, org string, ex application.CodeExec) application.CodeOutcome {
	run, err := s.Store.GetRunByApproval(ctx, org, ex.ApprovalID)
	if err != nil {
		return application.CodeOutcome{Status: "denied", DenyReason: CodeUnavailable, Summary: "run record not available"}
	}
	// The approval binds to the exact code + inputs: the stored plan must hash
	// to what was approved, and what was approved must be what is stored.
	recomputed := Hash(Request{Language: run.Language, Code: run.Code, ExpectedOutputs: run.ExpectedOutputs}, run.Inputs)
	if run.ApprovalID == "" || run.ApprovalID != ex.ApprovalID || (ex.ArgsHash != "" && run.Hash != ex.ArgsHash) || recomputed != run.Hash {
		return s.deny(ctx, org, run, CodeApprovalMismatch)
	}
	st, err := s.Store.GetSettings(ctx, org)
	if err != nil {
		return s.deny(ctx, org, run, CodeUnavailable)
	}
	if !st.Enabled {
		return s.deny(ctx, org, run, CodeDisabled)
	}
	if c := s.blocked(ctx, org, run.AgentID); c != "" {
		return s.deny(ctx, org, run, c)
	}
	n, err := s.Store.CountStartedSince(ctx, org, dayStart(s.now()))
	if err != nil {
		return s.deny(ctx, org, run, CodeUnavailable)
	}
	if n >= s.runsPerDay(st) {
		return s.deny(ctx, org, run, CodeDailyLimit)
	}
	ids := make([]string, 0, len(run.Inputs))
	for _, i := range run.Inputs {
		ids = append(ids, i.ID)
	}
	ins, err := s.readInputs(ctx, org, ids)
	if err != nil {
		if CodeOf(err) == CodeUnavailable {
			return s.deny(ctx, org, run, CodeUnavailable)
		}
		return s.deny(ctx, org, run, CodeInputsChanged)
	}
	for i, in := range ins {
		if in.Info.SHA256 != run.Inputs[i].SHA256 {
			return s.deny(ctx, org, run, CodeInputsChanged) // the file changed after the approval
		}
	}
	// Last gate, atomic: the run starts at most once.
	began, err := s.Store.BeginRun(ctx, org, run.ID, ex.ApprovalID, ex.Approvers, s.now())
	if err != nil {
		return s.deny(ctx, org, run, CodeUnavailable)
	}
	if !began {
		return application.CodeOutcome{Status: "denied", DenyReason: "already_executed", Summary: "this approval was already executed"}
	}
	run.Approvers = ex.Approvers
	s.audit(ctx, org, run.AgentID, "code.run.started", run.ID, map[string]any{"code_hash": run.CodeHash, "args_hash": run.Hash,
		"approval_id": ex.ApprovalID, "approvers": ex.Approvers, "task_id": run.TaskID, "inputs": inputRefs(run.Inputs), "limits": run.Limits})

	req := RunRequest{RunID: run.ID, OrgID: org, Code: run.Code, Limits: run.Limits}
	for _, in := range ins {
		req.Files = append(req.Files, RunFile{Name: in.Info.Name, Data: in.Data})
	}

	rctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	abortReason := make(chan string, 1)
	watchDone := make(chan struct{})
	go func() { // kill switch / pause / read-only mode / cancellation abort the container
		defer close(watchDone)
		poll := s.Poll
		if poll <= 0 {
			poll = time.Second
		}
		t := time.NewTicker(poll)
		defer t.Stop()
		for {
			select {
			case <-rctx.Done():
				return
			case <-ctx.Done():
				abortReason <- "cancelled"
				cancel()
				return
			case <-t.C:
				if c := s.blocked(rctx, org, run.AgentID); c != "" {
					abortReason <- c
					cancel()
					actx, ac := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
					_ = s.Runner.Abort(actx, org, run.ID)
					ac()
					return
				}
			}
		}
	}()
	start := s.now()
	resp, rerr := s.Runner.Run(rctx, req)
	cancel()
	<-watchDone
	var aborted string
	select {
	case aborted = <-abortReason:
	default:
	}
	if resp.DurationMS == 0 {
		resp.DurationMS = s.now().Sub(start).Milliseconds()
	}
	return s.finish(ctx, org, run, resp, rerr, aborted)
}

func cleanText(s string, max int, sus *sanitize.Suspects) (string, bool) {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == utf8.RuneError || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == 0x2028 || r == 0x2029:
		default:
			b.WriteRune(r)
		}
	}
	t := sanitize.RedactSecrets(b.String(), sus)
	t = sanitize.Neutralize(t)
	if max > 0 && len(t) > max {
		t = strings.ToValidUTF8(t[:max], "") + "\n[truncated]"
		return t, true
	}
	return t, false
}

func (s *Service) finish(ctx context.Context, org string, run Run, resp RunResponse, rerr error, aborted string) application.CodeOutcome {
	fin := s.now()
	run.FinishedAt, run.DurationMS = &fin, resp.DurationMS
	max := run.Limits.MaxStdioBytes
	var t1, t2 bool
	run.Stdout, t1 = cleanText(resp.Stdout, max, s.Suspects)
	run.Stderr, t2 = cleanText(resp.Stderr, max, s.Suspects)
	run.StdoutTruncated, run.StderrTruncated = t1 || resp.StdoutTruncated, t2 || resp.StderrTruncated
	exit := resp.ExitCode
	run.ExitCode = &exit
	run.Outputs = []OutputInfo{}
	switch {
	case aborted != "":
		run.Status, run.DenyReason = StatusAborted, aborted
	case rerr != nil:
		run.Status, run.DenyReason = StatusFailed, CodeUnavailable
		s.log().Warn("sandbox: runner failed", "run", run.ID, "err", rerr)
		run.ExitCode = nil
	default:
		switch resp.Status {
		case RunOK:
			run.Status = StatusSucceeded
		case RunTimeout:
			run.Status = StatusTimeout
		case RunAborted:
			run.Status, run.DenyReason = StatusAborted, "aborted"
		case RunOutputLimit:
			run.Status, run.DenyReason = StatusFailed, "output_limit"
		case RunError:
			run.Status = StatusFailed
		default:
			run.Status, run.DenyReason = StatusFailed, "runner_failed"
			if resp.Message != "" {
				run.DenyReason = "runner_failed: " + truncateRunes(resp.Message, 120)
			}
		}
	}
	var dropped []string
	dropped = append(dropped, resp.Dropped...)
	if rerr == nil && aborted == "" {
		run.Outputs, dropped = s.storeOutputs(ctx, org, run, resp.Files, dropped)
	}
	if err := s.Store.FinishRun(context.WithoutCancel(ctx), org, run); err != nil {
		s.log().Warn("sandbox: finish run", "run", run.ID, "err", err)
	}
	refs := []string{}
	outNames := []string{}
	for _, o := range run.Outputs {
		outNames = append(outNames, o.Name)
		if o.FileRef != "" {
			refs = append(refs, o.FileRef)
		}
	}
	s.audit(ctx, org, run.AgentID, "code.run.finished", run.ID, map[string]any{"status": run.Status, "reason": run.DenyReason, "exit_code": run.ExitCode,
		"duration_ms": run.DurationMS, "code_hash": run.CodeHash, "args_hash": run.Hash, "approval_id": run.ApprovalID, "approvers": run.Approvers,
		"task_id": run.TaskID, "inputs": inputRefs(run.Inputs), "outputs": run.Outputs, "dropped": dropped,
		"stdout_bytes": len(resp.Stdout), "stderr_bytes": len(resp.Stderr)})
	s.emit(ctx, org, "code.run.finished", map[string]any{"run_id": run.ID, "status": run.Status, "exit_code": run.ExitCode, "duration_ms": run.DurationMS,
		"agent_id": run.AgentID, "task_id": run.TaskID, "code_hash": run.CodeHash, "outputs": outNames})
	out := application.CodeOutcome{Status: run.Status, DenyReason: run.DenyReason, ExitCode: exit, OutputRefs: refs}
	out.Summary = fmt.Sprintf("code run %s: %s (exit %d, %d ms, %d output file(s))", run.ID, run.Status, exit, run.DurationMS, len(refs))
	out.Blocks = []string{s.resultBlock(run, dropped)}
	return out
}

func (s *Service) storeOutputs(ctx context.Context, org string, run Run, files []RunFile, dropped []string) ([]OutputInfo, []string) {
	outs := []OutputInfo{}
	total := int64(0)
	for _, f := range files {
		ext := strings.ToLower(path.Ext(f.Name))
		switch {
		case !ValidFileName(f.Name) || !AllowedOutputExt[ext]:
			dropped = append(dropped, f.Name+": extension or name not allowed")
			continue
		case len(outs) >= run.Limits.MaxOutputFiles:
			dropped = append(dropped, f.Name+": too many files")
			continue
		case int64(len(f.Data)) > run.Limits.MaxOutputFileBytes:
			dropped = append(dropped, f.Name+": file too large")
			continue
		case total+int64(len(f.Data)) > run.Limits.MaxOutputBytes:
			dropped = append(dropped, f.Name+": output size cap reached")
			continue
		}
		total += int64(len(f.Data))
		sum := sha256.Sum256(f.Data)
		o := OutputInfo{Name: f.Name, Size: int64(len(f.Data)), SHA256: hex.EncodeToString(sum[:])}
		if s.Sink == nil {
			o.Error = "no_file_sink"
		} else if ref, err := s.Sink.WriteOutput(ctx, org, run.AgentID, "code-runs/"+run.ID, f.Name, bytes.NewReader(f.Data)); err != nil {
			o.Error = "write_failed"
			s.log().Warn("sandbox: write output", "run", run.ID, "file", f.Name, "err", err)
		} else {
			o.FileRef = ref
		}
		outs = append(outs, o)
	}
	return outs, dropped
}

// resultBlock is the delimited, redacted tool result handed to the agent. The
// agent treats it as DATA: stdout can contain anything the code (or an input
// file it read) wanted to say.
func (s *Service) resultBlock(run Run, dropped []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "code.run %s: status=%s", run.ID, run.Status)
	if run.ExitCode != nil {
		fmt.Fprintf(&b, " exit_code=%d", *run.ExitCode)
	}
	fmt.Fprintf(&b, " duration_ms=%d\n", run.DurationMS)
	if run.DenyReason != "" {
		fmt.Fprintf(&b, "reason: %s\n", run.DenyReason)
	}
	fmt.Fprintf(&b, "[stdout%s]\n%s\n[stderr%s]\n%s\n", trunc(run.StdoutTruncated), run.Stdout, trunc(run.StderrTruncated), run.Stderr)
	if len(run.Outputs) > 0 {
		b.WriteString("[output_files]\n")
		for _, o := range run.Outputs {
			fmt.Fprintf(&b, "%s (%d bytes) ref=%s %s\n", o.Name, o.Size, o.FileRef, o.Error)
		}
	}
	missing := []string{}
	for _, e := range run.ExpectedOutputs {
		found := false
		for _, o := range run.Outputs {
			found = found || o.Name == e
		}
		if !found {
			missing = append(missing, e)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(&b, "[missing_expected_outputs] %s\n", strings.Join(missing, ", "))
	}
	if len(dropped) > 0 {
		fmt.Fprintf(&b, "[dropped] %s\n", strings.Join(dropped, "; "))
	}
	text := sanitize.Neutralize(b.String())
	return sanitize.Wrap(run.ID, "tool:code.run", "", text)
}

func trunc(t bool) string {
	if t {
		return " truncated"
	}
	return ""
}
