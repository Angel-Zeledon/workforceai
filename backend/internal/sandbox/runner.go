package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Runner executes a run in the sandbox. The production implementation is the
// HTTP client of the sandbox-runner service.
type Runner interface {
	// Run executes the request and blocks until it ends. Cancelling ctx aborts
	// the run (the runner kills the container).
	Run(ctx context.Context, req RunRequest) (RunResponse, error)
	// Abort kills the container of a running run (best effort).
	Abort(ctx context.Context, org, runID string) error
}

// HTTPRunner talks to the sandbox-runner over an internal, authenticated
// endpoint (bearer token, like RUNTIME_TOKEN).
type HTTPRunner struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

// NewHTTPRunner builds the client. There is no overall client timeout: the
// run duration is bounded by the limits and by the caller's context.
func NewHTTPRunner(baseURL, token string) *HTTPRunner {
	return &HTTPRunner{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, Client: &http.Client{}}
}

func (h *HTTPRunner) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.BaseURL+path, rd)
	if err != nil {
		return err
	}
	if h.Token != "" {
		req.Header.Set("Authorization", "Bearer "+h.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("sandbox runner: %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 256<<20)).Decode(out)
}

// Run implements Runner.
func (h *HTTPRunner) Run(ctx context.Context, req RunRequest) (RunResponse, error) {
	var out RunResponse
	err := h.do(ctx, http.MethodPost, "/v1/run", req, &out)
	return out, err
}

// Abort implements Runner.
func (h *HTTPRunner) Abort(ctx context.Context, org, runID string) error {
	return h.do(ctx, http.MethodPost, "/v1/runs/"+url.PathEscape(runID)+"/abort", nil, nil)
}

// FakeRunner is a Runner for tests: Fn computes the response.
type FakeRunner struct {
	Fn      func(ctx context.Context, req RunRequest) (RunResponse, error)
	mu      sync.Mutex
	calls   []RunRequest
	aborted []string
}

// Calls returns the requests received so far.
func (f *FakeRunner) Calls() []RunRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]RunRequest(nil), f.calls...)
}

// Aborted returns the run ids that were aborted through Abort.
func (f *FakeRunner) Aborted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.aborted...)
}

// Run implements Runner.
func (f *FakeRunner) Run(ctx context.Context, req RunRequest) (RunResponse, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	f.mu.Unlock()
	if f.Fn == nil {
		return RunResponse{Status: RunOK}, nil
	}
	return f.Fn(ctx, req)
}

// Abort implements Runner.
func (f *FakeRunner) Abort(_ context.Context, _, runID string) error {
	f.mu.Lock()
	f.aborted = append(f.aborted, runID)
	f.mu.Unlock()
	return nil
}

var errNotConfigured = errors.New("sandbox runner is not configured")

// NoRunner is the Runner used when no SANDBOX_URL is configured: every run fails closed.
type NoRunner struct{}

// Run implements Runner.
func (NoRunner) Run(context.Context, RunRequest) (RunResponse, error) {
	return RunResponse{}, errNotConfigured
}

// Abort implements Runner.
func (NoRunner) Abort(context.Context, string, string) error { return nil }
