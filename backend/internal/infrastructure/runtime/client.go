// Package runtime is the HTTP client for the agent-runtime service.
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aiworkforce/backend/internal/application"
)

type Client struct {
	base  string
	http  *http.Client
	token string
	// policy returns the organization model policy to merge into every call
	// (allowed/preferred providers, per-role provider order and model). Optional.
	policy func(ctx context.Context) application.RuntimePolicy
}

// New creates a client. Per-call deadlines come from the caller's context.
func New(baseURL string) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: 5 * time.Minute}}
}

// WithToken sets the shared secret sent as "Authorization: Bearer" (RUNTIME_TOKEN).
func (c *Client) WithToken(token string) *Client { c.token = strings.TrimSpace(token); return c }

// WithPolicy installs the source of the organization model policy, applied to
// every runtime call (the tenant comes from the context).
func (c *Client) WithPolicy(fn func(ctx context.Context) application.RuntimePolicy) *Client {
	c.policy = fn
	return c
}

// body marshals a request and merges the organization model policy into it.
func (c *Client) body(ctx context.Context, in any) ([]byte, error) {
	raw, err := json.Marshal(in)
	if err != nil || c.policy == nil {
		return raw, err
	}
	p := c.policy(ctx)
	if p.Empty() {
		return raw, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw, nil // not an object: send as is
	}
	extra, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var add map[string]json.RawMessage
	if err := json.Unmarshal(extra, &add); err != nil {
		return nil, err
	}
	for k, v := range add {
		m[k] = v
	}
	return json.Marshal(m)
}

var (
	_ application.Runtime     = (*Client)(nil)
	_ application.Estimator   = (*Client)(nil)
	_ application.ChatRuntime = (*Client)(nil)
	_ application.Reviewer    = (*Client)(nil)
)

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := c.body(ctx, in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("runtime %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("runtime %s: %w", path, err)
	}
	if resp.StatusCode/100 != 2 {
		err := fmt.Errorf("runtime %s: status %d: %s", path, resp.StatusCode, truncate(string(data), 300))
		if sc := resp.StatusCode; sc >= 400 && sc < 500 && sc != http.StatusRequestTimeout && sc != http.StatusTooManyRequests {
			err = fmt.Errorf("%w: %w", err, application.ErrNonRetryable) // retrying the same request cannot help
		}
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("runtime %s: invalid response: %w", path, err)
	}
	return nil
}

func (c *Client) Plan(ctx context.Context, in application.PlanRequest) (out application.PlanResponse, err error) {
	err = c.do(ctx, http.MethodPost, "/v1/plan", in, &out)
	return
}

// PlanPhases implements application.HierarchicalPlanner (POST /v1/plan-phases).
func (c *Client) PlanPhases(ctx context.Context, in application.PlanPhasesRequest) (out application.PlanPhasesResponse, err error) {
	err = c.do(ctx, http.MethodPost, "/v1/plan-phases", in, &out)
	return
}

// PlanPhase implements application.HierarchicalPlanner (POST /v1/plan-phase).
func (c *Client) PlanPhase(ctx context.Context, in application.PlanPhaseRequest) (out application.PlanPhaseResponse, err error) {
	err = c.do(ctx, http.MethodPost, "/v1/plan-phase", in, &out)
	return
}

// Replan implements application.Replanner (POST /v1/replan).
func (c *Client) Replan(ctx context.Context, in application.ReplanRequest) (out application.ReplanResponse, err error) {
	err = c.do(ctx, http.MethodPost, "/v1/replan", in, &out)
	return
}

func (c *Client) RunTask(ctx context.Context, in application.RunTaskRequest) (out application.RunTaskResponse, err error) {
	err = c.do(ctx, http.MethodPost, "/v1/run-task", in, &out)
	return
}

func (c *Client) Consult(ctx context.Context, in application.ConsultRequest) (out application.ConsultResponse, err error) {
	err = c.do(ctx, http.MethodPost, "/v1/consult", in, &out)
	return
}

func (c *Client) Synthesize(ctx context.Context, in application.SynthesizeRequest) (out application.SynthesizeResponse, err error) {
	err = c.do(ctx, http.MethodPost, "/v1/synthesize", in, &out)
	return
}

// Review implements application.Reviewer (POST /v1/review).
func (c *Client) Review(ctx context.Context, in application.ReviewRequest) (out application.ReviewResponse, err error) {
	err = c.do(ctx, http.MethodPost, "/v1/review", in, &out)
	return
}

// Estimate implements application.Estimator (POST /v1/estimate).
func (c *Client) Estimate(ctx context.Context, in application.EstimateRequest) (out application.EstimateResponse, err error) {
	err = c.do(ctx, http.MethodPost, "/v1/estimate", in, &out)
	return
}

// Route implements application.ChatRuntime (POST /v1/route).
func (c *Client) Route(ctx context.Context, in application.RouteRequest) (out application.RouteResponse, err error) {
	err = c.do(ctx, http.MethodPost, "/v1/route", in, &out)
	return
}

// ChatReply implements application.ChatRuntime (POST /v1/chat-reply).
func (c *Client) ChatReply(ctx context.Context, in application.ChatReplyRequest) (out application.ChatReplyResponse, err error) {
	err = c.do(ctx, http.MethodPost, "/v1/chat-reply", in, &out)
	return
}

func (c *Client) Health(ctx context.Context) (string, error) {
	var h struct {
		Status string `json:"status"`
		Mode   string `json:"mode"`
	}
	if err := c.do(ctx, http.MethodGet, "/healthz", nil, &h); err != nil {
		return "", err
	}
	return h.Mode, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
