// Package runnerd is the sandbox runner: the ONLY component with access to
// Docker. It receives approved runs from the backend over an authenticated
// internal endpoint and executes each one in a disposable container with no
// network, a read-only root, a writable tmpfs /work, a non-root user, no
// capabilities and cpu/memory/pids/time/output limits.
//
// The runner never receives credentials and never forwards its own
// environment to the containers. See docs/architecture/code-sandbox.md.
package runnerd

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/sandbox"
)

// Config configures the runner.
type Config struct {
	// Token is the bearer token the backend must present (required).
	Token string
	// Image is the pinned sandbox image (default wfai-sandbox-python:1).
	Image string
	// RenderImage is the pinned render image (PDF pages and images to pixels,
	// render.go). Empty: POST /v1/render answers 503 render_unavailable.
	// Env SANDBOX_RENDER_IMAGE (build: docker build -t wfai-sandbox-render:1 sandbox/render).
	RenderImage string
	// Runtime is the OCI runtime of the containers ("" = Docker's default,
	// "runsc" = gVisor when installed). Env SANDBOX_RUNTIME.
	Runtime string
	// DockerBin is the docker CLI (default "docker").
	DockerBin string
	// SeccompProfile is an optional path to a stricter seccomp profile. Empty
	// keeps Docker's default profile (never "unconfined").
	SeccompProfile string
	// MaxConcurrent bounds simultaneous containers (default 2).
	MaxConcurrent int
	// Ceiling are the maximum limits the runner honours whatever the backend asks.
	Ceiling sandbox.Limits
	// WorkMB is the size of the /work tmpfs (default 256).
	WorkMB int
	// RenderExec selects how render jobs run: "docker" (default, a disposable
	// container per job, needs the Docker socket) or "local" (the fixed render
	// program as a child process of this service, which must itself run in a
	// locked-down container; no Docker at all, /v1/run answers 501). Env RENDER_EXEC.
	RenderExec string
	// RenderProgram is the fixed command of the local mode (default
	// python -B /opt/render/render.py). Injectable for tests.
	RenderProgram []string
	// RenderWorkDir is where local jobs get their scratch dirs (default /work).
	RenderWorkDir string
	// RenderEnv is extra environment for the local child (tests only).
	RenderEnv []string
	// RenderTimeout is the hard wall-clock limit of a render job (default 45 s).
	RenderTimeout time.Duration
	// RenderMemoryMB is RLIMIT_AS of the local child where the OS has it (default 704).
	RenderMemoryMB int
	// Grace is added to the timeout before the container is killed from outside.
	Grace time.Duration
	Log   *slog.Logger
}

func (c *Config) defaults() {
	if c.Image == "" {
		c.Image = "wfai-sandbox-python:1"
	}
	if c.DockerBin == "" {
		c.DockerBin = "docker"
	}
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 2
		if c.RenderExec == RenderExecLocal {
			c.MaxConcurrent = 1 // one small container: jobs queue instead of competing for its memory
		}
	}
	if c.RenderTimeout <= 0 {
		c.RenderTimeout = renderTimeout
	}
	if c.RenderMemoryMB <= 0 {
		c.RenderMemoryMB = 704
	}
	if len(c.RenderProgram) == 0 {
		c.RenderProgram = []string{"python", "-B", "/opt/render/render.py"}
	}
	if c.RenderWorkDir == "" {
		c.RenderWorkDir = "/work"
	}
	c.Ceiling = c.Ceiling.Clamp(sandbox.DefaultLimits())
	if c.WorkMB <= 0 {
		c.WorkMB = 256
	}
	if c.Grace <= 0 {
		c.Grace = 15 * time.Second
	}
	if c.Log == nil {
		c.Log = slog.Default()
	}
}

// Server is the runner HTTP service.
type Server struct {
	cfg Config
	sem chan struct{}

	mu      sync.Mutex
	running map[string]*active
}

type active struct {
	org    string
	cancel func(reason string)
}

// New builds a Server. A token is mandatory: the runner must never be open.
func New(cfg Config) (*Server, error) {
	if len(cfg.Token) < 16 {
		return nil, errors.New("runnerd: SANDBOX_TOKEN must be set (at least 16 characters)")
	}
	switch cfg.RenderExec = strings.ToLower(strings.TrimSpace(cfg.RenderExec)); cfg.RenderExec {
	case "", RenderExecDocker:
		cfg.RenderExec = RenderExecDocker
	case RenderExecLocal:
	default:
		return nil, fmt.Errorf("runnerd: RENDER_EXEC must be docker or local, got %q", cfg.RenderExec)
	}
	cfg.defaults()
	return &Server{cfg: cfg, sem: make(chan struct{}, cfg.MaxConcurrent), running: map[string]*active{}}, nil
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /v1/run", s.auth(s.handleRun))
	mux.HandleFunc("POST /v1/render", s.auth(s.handleRender))
	mux.HandleFunc("POST /v1/runs/{id}/abort", s.auth(s.handleAbort))
	mux.HandleFunc("POST /v1/abort-all", s.auth(s.handleAbortAll))
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.Token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{6,64}$`)

const maxRequestBytes = 64 << 20

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if s.local() {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "code runs are not available: this service runs in RENDER_EXEC=local mode (renders only, no Docker)"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	var req sandbox.RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := validate(req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-time.After(10 * time.Second):
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "runner busy"})
		return
	case <-r.Context().Done():
		return
	}
	ctx, cancel := context.WithCancel(r.Context()) // a closed connection aborts the run
	defer cancel()
	var reasonMu sync.Mutex
	reason := ""
	a := &active{org: req.OrgID, cancel: func(why string) {
		reasonMu.Lock()
		if reason == "" {
			reason = why
		}
		reasonMu.Unlock()
		cancel()
	}}
	s.mu.Lock()
	if _, dup := s.running[req.RunID]; dup {
		s.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "run already in progress"})
		return
	}
	s.running[req.RunID] = a
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.running, req.RunID); s.mu.Unlock() }()

	resp := s.execute(ctx, req, func() string { reasonMu.Lock(); defer reasonMu.Unlock(); return reason })
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAbort(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	a := s.running[id]
	s.mu.Unlock()
	if a != nil {
		a.cancel("aborted")
	}
	if !s.local() {
		s.kill(containerName(id))
	}
	writeJSON(w, http.StatusOK, map[string]any{"aborted": a != nil})
}

func (s *Server) handleAbortAll(w http.ResponseWriter, r *http.Request) {
	n := s.AbortAll(r.URL.Query().Get("org"))
	writeJSON(w, http.StatusOK, map[string]int{"aborted": n})
}

// AbortAll kills the running containers (of one org when org != ""), including
// orphans left by a previous runner process (found by label).
func (s *Server) AbortAll(org string) int {
	s.mu.Lock()
	var as []*active
	for _, a := range s.running {
		if org == "" || a.org == org {
			as = append(as, a)
		}
	}
	s.mu.Unlock()
	for _, a := range as {
		a.cancel("aborted")
	}
	if org == "" && !s.local() {
		s.KillOrphans()
	}
	return len(as)
}

// KillOrphans removes sandbox containers that no run of this process owns
// (call at startup: a crashed runner must not leave code running).
func (s *Server) KillOrphans() {
	if s.local() {
		return // no Docker here; stale scratch dirs are swept by CleanScratch
	}
	out, err := exec.Command(s.cfg.DockerBin, "ps", "-q", "--filter", "label=wfai.sandbox=1").Output()
	if err != nil {
		return
	}
	for _, id := range strings.Fields(string(out)) {
		_ = exec.Command(s.cfg.DockerBin, "kill", id).Run()
	}
}

func validate(req sandbox.RunRequest) error {
	switch {
	case !idRe.MatchString(req.RunID):
		return errors.New("invalid run_id")
	case strings.TrimSpace(req.Code) == "" || len(req.Code) > 2*sandbox.MaxCodeBytes:
		return errors.New("invalid code")
	case len(req.Files) > sandbox.MaxInputFiles:
		return errors.New("too many files")
	}
	total, seen := 0, map[string]bool{}
	for _, f := range req.Files {
		if !sandbox.ValidFileName(f.Name) || seen[strings.ToLower(f.Name)] {
			return fmt.Errorf("invalid file name %q", f.Name)
		}
		seen[strings.ToLower(f.Name)] = true
		total += len(f.Data)
	}
	if total > sandbox.MaxInputBytes {
		return errors.New("input files too large")
	}
	return nil
}

var nameClean = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

func containerName(runID string) string { return "wfai-sbx-" + nameClean.ReplaceAllString(runID, "_") }

// DockerArgs builds the `docker run` arguments of a run. It is exported for
// tests and for the documentation of the isolation guarantees.
func (s *Server) DockerArgs(req sandbox.RunRequest, lim sandbox.Limits) []string {
	mem := strconv.Itoa(lim.MemoryMB) + "m"
	args := []string{
		"run", "--rm", "-i", "--init", "--pull", "never",
		"--name", containerName(req.RunID),
		"--label", "wfai.sandbox=1", "--label", "wfai.run=" + nameClean.ReplaceAllString(req.RunID, "_"),
		"--network", "none",
		"--read-only",
		"--tmpfs", fmt.Sprintf("/work:rw,noexec,nosuid,nodev,size=%dm,mode=1777", s.cfg.WorkMB),
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=64m,mode=1777",
		"--user", "65532:65532",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", strconv.Itoa(lim.PIDs),
		"--memory", mem, "--memory-swap", mem,
		"--cpus", strconv.FormatFloat(lim.CPUs, 'f', -1, 64),
		"--ulimit", "nofile=256:256", "--ulimit", "core=0",
		"--shm-size", "16m",
		"--hostname", "sandbox",
		"--log-driver", "none",
		"--stop-timeout", "1",
	}
	if s.cfg.SeccompProfile != "" {
		args = append(args, "--security-opt", "seccomp="+s.cfg.SeccompProfile)
	}
	if s.cfg.Runtime != "" {
		args = append(args, "--runtime", s.cfg.Runtime)
	}
	// The ONLY environment of the container: limits for the entrypoint. Nothing
	// from the runner's environment (tokens, URLs) is ever passed.
	args = append(args,
		"-e", "SBX_TIMEOUT="+strconv.Itoa(lim.TimeoutSeconds),
		"-e", "SBX_MAX_FILE="+strconv.FormatInt(lim.MaxOutputFileBytes, 10),
		"-e", "SBX_MAX_STDIO="+strconv.Itoa(lim.MaxStdioBytes),
		"-e", "SBX_MAX_OUT_FILES="+strconv.Itoa(lim.MaxOutputFiles),
		"-e", "SBX_MAX_OUT_BYTES="+strconv.FormatInt(lim.MaxOutputBytes, 10),
		s.cfg.Image)
	return args
}

func (s *Server) kill(name string) {
	_ = exec.Command(s.cfg.DockerBin, "kill", name).Run()
}

// capBuffer collects up to max bytes and signals when the cap is exceeded.
type capBuffer struct {
	buf      bytes.Buffer
	max      int64
	over     chan struct{}
	overOnce sync.Once
}

func (c *capBuffer) Write(p []byte) (int, error) {
	if int64(c.buf.Len()+len(p)) > c.max {
		c.overOnce.Do(func() { close(c.over) })
		return 0, errors.New("output cap exceeded") // breaks the copy: docker CLI gets EPIPE
	}
	return c.buf.Write(p)
}

func (s *Server) execute(ctx context.Context, req sandbox.RunRequest, why func() string) sandbox.RunResponse {
	lim := req.Limits.Clamp(s.cfg.Ceiling)
	start := time.Now()
	fail := func(msg string) sandbox.RunResponse {
		return sandbox.RunResponse{Status: sandbox.RunFailed, Message: msg, DurationMS: time.Since(start).Milliseconds()}
	}
	input, err := buildInput(req)
	if err != nil {
		return fail("could not prepare the input")
	}
	// What the result may weigh: files + stdio + metadata (+ tar overhead).
	maxResult := lim.MaxOutputBytes + 2*int64(lim.MaxStdioBytes) + int64(lim.MaxOutputFiles)*2048 + (1 << 20)
	stdout := &capBuffer{max: maxResult, over: make(chan struct{})}
	var stderr bytes.Buffer
	cmd := exec.Command(s.cfg.DockerBin, s.DockerArgs(req, lim)...)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = stdout
	cmd.Stderr = &limitedWriter{w: &stderr, n: 8 << 10}
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "DOCKER_HOST=" + dockerHost()} // never the runner's secrets
	if err := cmd.Start(); err != nil {
		s.cfg.Log.Warn("runnerd: start docker", "err", err)
		return fail("docker is not available")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.NewTimer(time.Duration(lim.TimeoutSeconds)*time.Second + s.cfg.Grace)
	defer deadline.Stop()
	name := containerName(req.RunID)
	killed := ""
	var waitErr error
	ctxDone, overCh := ctx.Done(), stdout.over
wait:
	for {
		select {
		case waitErr = <-done:
			break wait
		case <-ctxDone:
			ctxDone = nil // fire once; keep waiting for the CLI to exit
			if killed == "" {
				killed = "aborted"
			}
			s.kill(name)
		case <-deadline.C:
			if killed == "" {
				killed = "timeout"
			}
			s.kill(name)
		case <-overCh:
			overCh = nil
			if killed == "" {
				killed = "output_limit"
			}
			s.kill(name)
		}
	}
	resp := sandbox.RunResponse{DurationMS: time.Since(start).Milliseconds()}
	switch killed {
	case "aborted":
		resp.Status = sandbox.RunAborted
		resp.Message = why()
		return resp
	case "timeout":
		resp.Status, resp.ExitCode = sandbox.RunTimeout, 137
		resp.Message = "killed by the runner after the wall-clock limit"
		return resp
	case "output_limit":
		resp.Status, resp.Message = sandbox.RunOutputLimit, "output size cap exceeded"
		return resp
	}
	res, perr := parseResult(stdout.buf.Bytes(), lim)
	if perr != nil {
		exit := -1
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			exit = ee.ExitCode()
		}
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		resp.Status, resp.ExitCode = sandbox.RunFailed, exit
		resp.Message = fmt.Sprintf("no valid result (docker exit %d): %s", exit, msg)
		if exit == 137 {
			resp.Message = "the container was killed (memory limit or runtime kill)"
		}
		return resp
	}
	resp.Stdout, resp.Stderr, resp.StdoutTruncated, resp.StderrTruncated = res.stdout, res.stderr, res.meta.StdoutTruncated, res.meta.StderrTruncated
	resp.Files, resp.Dropped, resp.ExitCode = res.files, append(res.meta.Dropped, res.dropped...), res.meta.ExitCode
	switch {
	case res.meta.TimedOut:
		resp.Status = sandbox.RunTimeout
	case res.meta.ExitCode != 0:
		resp.Status = sandbox.RunError
	default:
		resp.Status = sandbox.RunOK
	}
	return resp
}

func dockerHost() string {
	// The CLI needs to find the daemon; nothing else of the environment is passed.
	return envOr("DOCKER_HOST", "unix:///var/run/docker.sock")
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := len(p)
		if k > l.n {
			k = l.n
		}
		_, _ = l.w.Write(p[:k])
		l.n -= k
	}
	return len(p), nil
}

// buildInput builds the tar the entrypoint reads from stdin.
func buildInput(req sandbox.RunRequest) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add := func(name string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg, ModTime: time.Now()}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	if err := add("main.py", []byte(req.Code)); err != nil {
		return nil, err
	}
	for _, f := range req.Files {
		if err := add("in/"+f.Name, f.Data); err != nil {
			return nil, err
		}
	}
	// Close BEFORE taking the bytes: Close writes the last entry's padding and the
	// end-of-archive blocks (returning buf.Bytes() first sent a truncated tar).
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type meta struct {
	ExitCode        int      `json:"exit_code"`
	Signal          int      `json:"signal"`
	TimedOut        bool     `json:"timed_out"`
	DurationMS      int64    `json:"duration_ms"`
	StdoutTruncated bool     `json:"stdout_truncated"`
	StderrTruncated bool     `json:"stderr_truncated"`
	Dropped         []string `json:"dropped"`
}

type result struct {
	meta           meta
	stdout, stderr string
	files          []sandbox.RunFile
	dropped        []string
}

// parseResult reads the tar produced by the container. The container is
// untrusted: names, types, sizes, counts and extensions are re-validated here.
func parseResult(b []byte, lim sandbox.Limits) (result, error) {
	var r result
	tr := tar.NewReader(bytes.NewReader(b))
	haveMeta := false
	total := int64(0)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return r, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		read := func(max int64) ([]byte, bool) {
			data, err := io.ReadAll(io.LimitReader(tr, max+1))
			return data, err == nil && int64(len(data)) <= max
		}
		switch {
		case h.Name == "meta.json":
			data, ok := read(64 << 10)
			if !ok || json.Unmarshal(data, &r.meta) != nil {
				return r, errors.New("bad meta")
			}
			haveMeta = true
		case h.Name == "stdout.txt" || h.Name == "stderr.txt":
			data, _ := read(int64(2*lim.MaxStdioBytes) + 1024)
			if h.Name == "stdout.txt" {
				r.stdout = string(data)
			} else {
				r.stderr = string(data)
			}
		case strings.HasPrefix(h.Name, "out/"):
			name := strings.TrimPrefix(h.Name, "out/")
			ext := strings.ToLower(path.Ext(name))
			switch {
			case !sandbox.ValidFileName(name) || !sandbox.AllowedOutputExt[ext]:
				r.dropped = append(r.dropped, name+": name or extension not allowed")
			case h.Size > lim.MaxOutputFileBytes:
				r.dropped = append(r.dropped, name+": file too large")
			case len(r.files) >= lim.MaxOutputFiles:
				r.dropped = append(r.dropped, name+": too many files")
			case total+h.Size > lim.MaxOutputBytes:
				r.dropped = append(r.dropped, name+": output size cap reached")
			default:
				data, ok := read(lim.MaxOutputFileBytes)
				if !ok {
					r.dropped = append(r.dropped, name+": file too large")
					continue
				}
				total += int64(len(data))
				r.files = append(r.files, sandbox.RunFile{Name: name, Data: data})
			}
		}
	}
	if !haveMeta {
		return r, errors.New("missing meta")
	}
	return r, nil
}

func envOr(k, def string) string {
	if v := getenv(k); v != "" {
		return v
	}
	return def
}
