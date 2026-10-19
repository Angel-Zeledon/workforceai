package runnerd

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // DecodeConfig of the container's output
	_ "image/png"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"aiworkforce/backend/internal/sandbox"
)

// Render jobs (POST /v1/render, docs/architecture/agent-vision-pdf.md): a PDF
// page or an image becomes PNG/JPEG pixels in a disposable container of the
// render image (Config.RenderImage, env SANDBOX_RENDER_IMAGE), with the same
// isolation as a code run (no network, read-only root, non-root, no
// capabilities, cpu/memory/pids/time limits) and its own fixed program: the
// backend sends file bytes, never code. Without a render image the endpoint
// answers 503 render_unavailable and the backend degrades in plain words.

// Render job bounds (the backend's limits are clamped to these).
const (
	renderTimeout  = 45 * time.Second
	renderMemoryMB = 768
	renderPIDs     = 64
	renderWorkMB   = 192
)

func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) {
	if !s.local() && s.cfg.RenderImage == "" {
		writeJSON(w, http.StatusServiceUnavailable, sandbox.RenderResponse{Status: sandbox.RenderUnavailable, Code: "render_unavailable",
			Message: "no render image configured on the runner (SANDBOX_RENDER_IMAGE)"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, sandbox.MaxRenderInputBytes*2)
	var req sandbox.RenderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := sandbox.ValidateRender(req); err != nil {
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
	writeJSON(w, http.StatusOK, s.render(r.Context(), req))
}

// RenderDockerArgs builds the `docker run` arguments of a render job (exported
// for tests and for the documentation of the isolation guarantees).
func (s *Server) RenderDockerArgs(req sandbox.RenderRequest) []string {
	mem := strconv.Itoa(renderMemoryMB) + "m"
	args := []string{
		"run", "--rm", "-i", "--init", "--pull", "never",
		"--name", containerName(req.RenderID),
		"--label", "wfai.sandbox=1", "--label", "wfai.render=1",
		"--network", "none",
		"--read-only",
		"--tmpfs", fmt.Sprintf("/work:rw,noexec,nosuid,nodev,size=%dm,mode=1777", renderWorkMB),
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=64m,mode=1777",
		"--user", "65532:65532",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", strconv.Itoa(renderPIDs),
		"--memory", mem, "--memory-swap", mem,
		"--cpus", "1",
		"--ulimit", "nofile=128:128", "--ulimit", "core=0",
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
	return append(args, "-e", "RENDER_TIMEOUT="+strconv.Itoa(int(renderTimeout/time.Second)-5), s.cfg.RenderImage)
}

// renderJob is the job.json the render program reads.
type renderJob struct {
	Kind     string `json:"kind"`
	Pages    []int  `json:"pages,omitempty"`
	DPI      int    `json:"dpi"`
	MaxSide  int    `json:"max_side"`
	MaxBytes int    `json:"max_bytes"`
}

func clampJob(req sandbox.RenderRequest) renderJob {
	j := renderJob{Kind: req.Kind, Pages: req.Pages, DPI: req.DPI, MaxSide: req.MaxSide, MaxBytes: req.MaxBytes}
	if j.DPI <= 0 || j.DPI > 200 {
		j.DPI = 110
	}
	if j.MaxSide <= 0 || j.MaxSide > sandbox.MaxRenderSide {
		j.MaxSide = 1568
	}
	if j.MaxBytes <= 0 || j.MaxBytes > sandbox.MaxRenderImageBytes {
		j.MaxBytes = 5 << 20
	}
	if j.Kind == sandbox.RenderPDF && len(j.Pages) == 0 {
		j.Pages = []int{1}
	}
	return j
}

func buildRenderInput(req sandbox.RenderRequest, job renderJob) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add := func(name string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg, ModTime: time.Now()}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	jb, _ := json.Marshal(job)
	if err := add("job.json", jb); err != nil {
		return nil, err
	}
	if err := add("in/input", req.Data); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Server) render(ctx context.Context, req sandbox.RenderRequest) sandbox.RenderResponse {
	job := clampJob(req)
	failed := func(code, msg string) sandbox.RenderResponse {
		return sandbox.RenderResponse{Status: sandbox.RenderFailed, Code: code, Message: msg}
	}
	input, err := buildRenderInput(req, job)
	if err != nil {
		return failed("render_failed", "could not prepare the input")
	}
	maxOut := int64(len(job.Pages)+1)*int64(job.MaxBytes) + (1 << 20)
	if s.local() {
		return s.renderLocal(ctx, job, input, maxOut)
	}
	stdout := &capBuffer{max: maxOut, over: make(chan struct{})}
	var stderr bytes.Buffer
	cmd := exec.Command(s.cfg.DockerBin, s.RenderDockerArgs(req)...)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = stdout
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4 << 10}
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "DOCKER_HOST=" + dockerHost()} // never the runner's secrets
	if err := cmd.Start(); err != nil {
		s.cfg.Log.Warn("runnerd: start docker (render)", "err", err)
		return sandbox.RenderResponse{Status: sandbox.RenderUnavailable, Code: "render_unavailable", Message: "docker is not available"}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.NewTimer(renderTimeout)
	defer deadline.Stop()
	name := containerName(req.RenderID)
	killed := ""
	ctxDone, overCh := ctx.Done(), stdout.over
wait:
	for {
		select {
		case <-done:
			break wait
		case <-ctxDone:
			ctxDone, killed = nil, "aborted"
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

type renderMeta struct {
	Status     string `json:"status"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	TotalPages int    `json:"total_pages"`
	Images     []struct {
		Name string `json:"name"`
		Page int    `json:"page"`
	} `json:"images"`
}

// parseRenderResult reads the tar the render program wrote. The container is
// untrusted: names, counts, sizes, types, pages and dimensions are re-checked.
func parseRenderResult(b []byte, job renderJob) (sandbox.RenderResponse, error) {
	tr := tar.NewReader(bytes.NewReader(b))
	var meta renderMeta
	haveMeta := false
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return sandbox.RenderResponse{}, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		switch {
		case h.Name == "meta.json":
			data, err := io.ReadAll(io.LimitReader(tr, 64<<10))
			if err != nil || json.Unmarshal(data, &meta) != nil {
				return sandbox.RenderResponse{}, errors.New("bad meta")
			}
			haveMeta = true
		case strings.HasPrefix(h.Name, "out/") && len(files) <= sandbox.MaxRenderPages:
			name := strings.TrimPrefix(h.Name, "out/")
			if !sandbox.ValidFileName(name) || h.Size > int64(job.MaxBytes) {
				continue
			}
			data, err := io.ReadAll(io.LimitReader(tr, int64(job.MaxBytes)+1))
			if err != nil || len(data) > job.MaxBytes {
				continue
			}
			files[name] = data
		}
	}
	if !haveMeta {
		return sandbox.RenderResponse{}, errors.New("missing meta")
	}
	if meta.Status != sandbox.RenderOK {
		code := meta.Code
		if !knownRenderCode[code] {
			code = "render_failed"
		}
		return sandbox.RenderResponse{Status: sandbox.RenderFailed, Code: code, Message: oneLine(meta.Message, 200), TotalPages: meta.TotalPages}, nil
	}
	want := map[int]bool{}
	for _, p := range job.Pages {
		want[p] = true
	}
	out := sandbox.RenderResponse{Status: sandbox.RenderOK, TotalPages: meta.TotalPages}
	for _, m := range meta.Images {
		data, ok := files[m.Name]
		if !ok || (job.Kind == sandbox.RenderPDF && !want[m.Page]) || len(out.Images) >= max(len(job.Pages), 1) {
			continue
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || (format != "png" && format != "jpeg") || cfg.Width > job.MaxSide+1 || cfg.Height > job.MaxSide+1 {
			continue
		}
		page := m.Page
		if job.Kind == sandbox.RenderImage {
			page = 0
		}
		out.Images = append(out.Images, sandbox.RenderImageOut{Page: page, Mime: "image/" + format, Data: data, Width: cfg.Width, Height: cfg.Height})
	}
	if len(out.Images) == 0 {
		return sandbox.RenderResponse{Status: sandbox.RenderFailed, Code: "render_failed", Message: "no image was produced", TotalPages: meta.TotalPages}, nil
	}
	return out, nil
}

var knownRenderCode = map[string]bool{"pdf_encrypted": true, "pdf_invalid": true, "pdf_pages_invalid": true, "image_invalid": true,
	"image_too_large": true, "image_unsupported": true, "timeout": true, "render_failed": true}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n]
	}
	return s
}
