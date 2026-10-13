package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aiworkforce/backend/internal/drive"
)

// The render capability (docs/architecture/agent-vision-pdf.md): turn a PDF page
// or an image into PNG/JPEG pixels a vision model can see. Parsing PDFs and
// image formats is heavy, untrusted-format work, so it runs ONLY in the sandbox
// runner, in a disposable container of its own pinned image (poppler's
// pdftoppm, Pillow) with no network, a read-only root, no capabilities and
// cpu/memory/pids/time limits. There is no code in a render job: the program is
// fixed in the image; the backend sends the file bytes and gets images back.

// Render kinds.
const (
	RenderPDF   = "pdf"
	RenderImage = "image"
)

// Render limits (the runner enforces its own copy of them).
const (
	MaxRenderInputBytes = 25 << 20
	MaxRenderPages      = 8
	MaxRenderSide       = 4096
	MaxRenderImageBytes = 10 << 20
)

// RenderRequest is one render job.
type RenderRequest struct {
	RenderID string `json:"render_id"`
	Kind     string `json:"kind"` // pdf | image
	Data     []byte `json:"data"` // base64 in JSON
	Pages    []int  `json:"pages,omitempty"`
	DPI      int    `json:"dpi,omitempty"`
	MaxSide  int    `json:"max_side,omitempty"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

// RenderImageOut is one produced image.
type RenderImageOut struct {
	Page   int    `json:"page,omitempty"`
	Mime   string `json:"mime"`
	Data   []byte `json:"data"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Render statuses.
const (
	RenderOK          = "ok"
	RenderFailed      = "failed"      // the file could not be rendered (Code says why)
	RenderUnavailable = "unavailable" // no render image configured on the runner
)

// RenderResponse is the result of a render job.
type RenderResponse struct {
	Status     string           `json:"status"`
	Code       string           `json:"code,omitempty"` // pdf_encrypted | pdf_invalid | image_invalid | image_too_large | timeout | ...
	Message    string           `json:"message,omitempty"`
	TotalPages int              `json:"total_pages,omitempty"`
	Images     []RenderImageOut `json:"images,omitempty"`
}

// ValidateRender checks a render request (both ends call it).
func ValidateRender(req RenderRequest) error {
	switch {
	case !validRenderID(req.RenderID):
		return errors.New("invalid render_id")
	case req.Kind != RenderPDF && req.Kind != RenderImage:
		return errors.New("invalid kind")
	case len(req.Data) == 0 || len(req.Data) > MaxRenderInputBytes:
		return errors.New("invalid data size")
	case len(req.Pages) > MaxRenderPages:
		return errors.New("too many pages")
	case req.MaxSide < 0 || req.MaxSide > MaxRenderSide || req.MaxBytes < 0 || req.MaxBytes > MaxRenderImageBytes || req.DPI < 0 || req.DPI > 300:
		return errors.New("invalid limits")
	}
	for _, p := range req.Pages {
		if p < 1 || p > 100000 {
			return errors.New("invalid page")
		}
	}
	return nil
}

func validRenderID(s string) bool {
	if len(s) < 6 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// NewRenderID returns a random render id.
func NewRenderID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "rnd_" + hex.EncodeToString(b)
}

// Render calls POST /v1/render. A runner that does not serve renders (no render
// image configured, an older runner, unreachable) is drive.ErrRenderUnavailable.
func (h *HTTPRunner) Render(ctx context.Context, req RenderRequest) (RenderResponse, error) {
	var out RenderResponse
	body, err := json.Marshal(req)
	if err != nil {
		return out, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, h.BaseURL+"/v1/render", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	hr.Header.Set("Content-Type", "application/json")
	if h.Token != "" {
		hr.Header.Set("Authorization", "Bearer "+h.Token)
	}
	resp, err := h.Client.Do(hr)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, fmt.Errorf("%w: %v", drive.ErrRenderUnavailable, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusServiceUnavailable ||
		resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusUnauthorized:
		return out, fmt.Errorf("%w: runner answered %d", drive.ErrRenderUnavailable, resp.StatusCode)
	case resp.StatusCode/100 != 2:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return out, fmt.Errorf("sandbox runner: render: %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 96<<20)).Decode(&out); err != nil {
		return out, fmt.Errorf("sandbox runner: render: invalid response: %w", err)
	}
	if out.Status == RenderUnavailable {
		return out, fmt.Errorf("%w: %s", drive.ErrRenderUnavailable, out.Message)
	}
	return out, nil
}

// RenderClient is what DriveRenderer needs (HTTPRunner, or a fake in tests).
type RenderClient interface {
	Render(ctx context.Context, req RenderRequest) (RenderResponse, error)
}

// DriveRenderer adapts the runner to drive.Renderer. It re-validates what comes
// back (the container is untrusted): counts, sizes, types and dimensions.
type DriveRenderer struct {
	R       RenderClient
	Timeout time.Duration // per render job (default 60 s)
}

var _ drive.Renderer = DriveRenderer{}

// ErrRenderFailed is a render the runner could not complete; Code is the stable reason.
type ErrRenderFailed struct{ Code, Message string }

func (e *ErrRenderFailed) Error() string { return "render failed: " + e.Code + ": " + e.Message }

// RenderCode is the stable code (drive reads it without importing this package).
func (e *ErrRenderFailed) RenderCode() string { return e.Code }

func (d DriveRenderer) call(ctx context.Context, req RenderRequest) (RenderResponse, error) {
	if d.R == nil {
		return RenderResponse{}, drive.ErrRenderUnavailable
	}
	to := d.Timeout
	if to <= 0 {
		to = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	req.RenderID = NewRenderID()
	if err := ValidateRender(req); err != nil {
		return RenderResponse{}, &ErrRenderFailed{Code: "invalid_request", Message: err.Error()}
	}
	resp, err := d.R.Render(ctx, req)
	if err != nil {
		return resp, err
	}
	if resp.Status != RenderOK {
		return resp, &ErrRenderFailed{Code: resp.Code, Message: resp.Message}
	}
	if len(resp.Images) > max(len(req.Pages), 1) {
		return resp, &ErrRenderFailed{Code: "invalid_output", Message: "the renderer returned more images than asked"}
	}
	for _, im := range resp.Images {
		if err := checkRendered(im, req); err != nil {
			return resp, &ErrRenderFailed{Code: "invalid_output", Message: err.Error()}
		}
	}
	return resp, nil
}

func checkRendered(im RenderImageOut, req RenderRequest) error {
	mime := ""
	switch {
	case bytes.HasPrefix(im.Data, []byte("\x89PNG\r\n\x1a\n")):
		mime = "image/png"
	case bytes.HasPrefix(im.Data, []byte{0xff, 0xd8, 0xff}):
		mime = "image/jpeg"
	}
	if mime == "" || mime != im.Mime {
		return errors.New("unexpected image type")
	}
	if req.MaxBytes > 0 && len(im.Data) > req.MaxBytes {
		return errors.New("image over the size limit")
	}
	side := req.MaxSide
	if side <= 0 {
		side = MaxRenderSide
	}
	if im.Width <= 0 || im.Height <= 0 || im.Width > side+1 || im.Height > side+1 {
		return errors.New("image over the side limit")
	}
	if req.Kind == RenderPDF {
		ok := false
		for _, p := range req.Pages {
			ok = ok || p == im.Page
		}
		if !ok {
			return errors.New("a page that was not asked for")
		}
	}
	return nil
}

// RenderPDF implements drive.Renderer.
func (d DriveRenderer) RenderPDF(ctx context.Context, data []byte, pages []int, opt drive.RenderOptions) ([]drive.RenderedImage, error) {
	if opt.MaxPages > 0 && len(pages) > opt.MaxPages {
		pages = pages[:opt.MaxPages]
	}
	resp, err := d.call(ctx, RenderRequest{Kind: RenderPDF, Data: data, Pages: pages, DPI: opt.DPI, MaxSide: opt.MaxSide, MaxBytes: opt.MaxBytes})
	if err != nil {
		return nil, err
	}
	out := make([]drive.RenderedImage, 0, len(resp.Images))
	for _, im := range resp.Images {
		out = append(out, drive.RenderedImage{Page: im.Page, Mime: im.Mime, Data: im.Data, Width: im.Width, Height: im.Height})
	}
	return out, nil
}

// NormalizeImage implements drive.Renderer.
func (d DriveRenderer) NormalizeImage(ctx context.Context, data []byte, _ string, opt drive.RenderOptions) (drive.RenderedImage, error) {
	resp, err := d.call(ctx, RenderRequest{Kind: RenderImage, Data: data, MaxSide: opt.MaxSide, MaxBytes: opt.MaxBytes})
	if err != nil {
		return drive.RenderedImage{}, err
	}
	if len(resp.Images) != 1 {
		return drive.RenderedImage{}, &ErrRenderFailed{Code: "invalid_output", Message: "expected one image"}
	}
	im := resp.Images[0]
	return drive.RenderedImage{Mime: im.Mime, Data: im.Data, Width: im.Width, Height: im.Height}, nil
}

// FakeRenderer is a RenderClient for tests: Fn computes the response.
type FakeRenderer struct {
	Fn func(ctx context.Context, req RenderRequest) (RenderResponse, error)
}

// Render implements RenderClient.
func (f FakeRenderer) Render(ctx context.Context, req RenderRequest) (RenderResponse, error) {
	if f.Fn == nil {
		return RenderResponse{}, drive.ErrRenderUnavailable
	}
	return f.Fn(ctx, req)
}
