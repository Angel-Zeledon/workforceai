package runnerd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"aiworkforce/backend/internal/sandbox"
)

// Local render mode (RENDER_EXEC=local, docs/architecture/agent-vision-pdf.md):
// the same fixed render program, the same stdin/stdout tar protocol and the same
// result re-validation as the docker mode, but run as a child process of this
// service instead of a disposable container. There is therefore NO Docker
// socket anywhere: the isolation comes from the container this service itself
// runs in (read-only root, non-root, no capabilities, no network, memory/pids/cpu
// limits, tmpfs /work; see deploy/vps/docker-compose.yml) plus, per job, a
// private scratch dir, a process group killed as a whole, a wall-clock limit,
// output caps and, on Linux, RLIMIT_AS/CPU/NOFILE/FSIZE/CORE on the child.

// Render execution modes (Config.RenderExec).
const (
	RenderExecDocker = "docker"
	RenderExecLocal  = "local"
)

func (s *Server) local() bool { return s.cfg.RenderExec == RenderExecLocal }

// CleanScratch removes scratch dirs a crashed predecessor left in the work dir
// (call at startup in local mode).
func (s *Server) CleanScratch() {
	if !s.local() {
		return
	}
	old, _ := filepath.Glob(filepath.Join(s.cfg.RenderWorkDir, "r-*"))
	for _, d := range old {
		_ = os.RemoveAll(d)
	}
}

func (s *Server) renderLocal(ctx context.Context, job renderJob, input []byte, maxOut int64) sandbox.RenderResponse {
	failed := func(code, msg string) sandbox.RenderResponse {
		return sandbox.RenderResponse{Status: sandbox.RenderFailed, Code: code, Message: msg}
	}
	unavailable := func(msg string) sandbox.RenderResponse {
		return sandbox.RenderResponse{Status: sandbox.RenderUnavailable, Code: "render_unavailable", Message: msg}
	}
	dir, err := os.MkdirTemp(s.cfg.RenderWorkDir, "r-")
	if err != nil {
		s.cfg.Log.Warn("runnerd: render scratch dir", "err", err)
		return unavailable("the renderer has no scratch space")
	}
	defer os.RemoveAll(dir)
	_ = os.Chmod(dir, 0o700)

	prog := s.cfg.RenderProgram
	cmd := exec.Command(prog[0], prog[1:]...)
	cmd.Dir = dir
	// Never the service's environment (RENDER_TOKEN and friends): a fixed, minimal one.
	secs := int(s.cfg.RenderTimeout/time.Second) - 5
	cmd.Env = append([]string{
		"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + dir, "TMPDIR=" + dir, "LANG=C.UTF-8",
		"PYTHONDONTWRITEBYTECODE=1", "PYTHONUNBUFFERED=1",
		"RENDER_WORK=" + dir, "RENDER_TIMEOUT=" + strconv.Itoa(max(secs, 1)),
	}, s.cfg.RenderEnv...)
	setGroup(cmd)
	stdout := &capBuffer{max: maxOut, over: make(chan struct{})}
	var stderr bytes.Buffer
	cmd.Stdout = stdout
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4 << 10}
	cmd.WaitDelay = 2 * time.Second // a grandchild holding the pipes open cannot hang the wait
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return unavailable("the renderer could not start")
	}
	if err := cmd.Start(); err != nil {
		s.cfg.Log.Warn("runnerd: start render program", "err", err)
		return unavailable("the renderer could not start")
	}
	// Limits are applied BEFORE the untrusted bytes are written: the program reads
	// stdin first, so every allocation made on them is already bounded.
	if err := limitChild(cmd.Process.Pid, s.cfg.RenderMemoryMB); err != nil {
		s.cfg.Log.Warn("runnerd: render rlimits", "err", err)
	}
	go func() { _, _ = stdin.Write(input); _ = stdin.Close() }()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.NewTimer(s.cfg.RenderTimeout)
	defer deadline.Stop()
	killed := ""
	kill := func(why string) {
		if killed == "" {
			killed = why
		}
		killGroup(cmd)
	}
	ctxDone, overCh := ctx.Done(), stdout.over
wait:
	for {
		select {
		case <-done:
			break wait
		case <-ctxDone:
			ctxDone = nil
			kill("aborted")
		case <-deadline.C:
			kill("timeout")
		case <-overCh:
			overCh = nil
			kill("output_limit")
		}
	}
	killGroup(cmd) // nothing the program started outlives the job
	switch killed {
	case "aborted":
		return failed("aborted", "the render was cancelled")
	case "timeout":
		return failed("timeout", "the render took longer than the limit")
	case "output_limit":
		return failed("output_limit", "the rendered images were over the size limit")
	}
	resp, perr := parseRenderResult(stdout.buf.Bytes(), job)
	if perr != nil {
		s.cfg.Log.Warn("runnerd: render result", "err", perr, "stderr", strings.TrimSpace(stderr.String()))
		return failed("render_failed", "the renderer produced no valid result")
	}
	return resp
}
