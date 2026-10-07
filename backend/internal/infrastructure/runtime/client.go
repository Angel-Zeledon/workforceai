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
	base string
	http *http.Client
}

// New creates a client. Per-call deadlines come from the caller's context.
func New(baseURL string) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: 5 * time.Minute}}
}

var (
	_ application.Runtime   = (*Client)(nil)
	_ application.Estimator = (*Client)(nil)
)

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
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
		return fmt.Errorf("runtime %s: status %d: %s", path, resp.StatusCode, truncate(string(data), 300))
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

// Estimate implements application.Estimator (POST /v1/estimate).
func (c *Client) Estimate(ctx context.Context, in application.EstimateRequest) (out application.EstimateResponse, err error) {
	err = c.do(ctx, http.MethodPost, "/v1/estimate", in, &out)
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
