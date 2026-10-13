package drive_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/officedoc"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), 80, uint8(y), 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func textPDF(t *testing.T, paragraphs ...string) []byte {
	t.Helper()
	var blocks []any
	for _, p := range paragraphs {
		blocks = append(blocks, map[string]any{"type": "paragraph", "text": p})
	}
	doc, err := officedoc.Parse(map[string]any{"title": "Informe", "blocks": blocks})
	if err != nil {
		t.Fatal(err)
	}
	data, err := officedoc.Build("pdf", doc)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func scannedPDF(t *testing.T, pages int) []byte {
	t.Helper()
	p := fpdf.New("P", "mm", "A4", "")
	for i := 0; i < pages; i++ {
		p.AddPage()
		p.Rect(20, 20, 120, 160, "F")
	}
	var b bytes.Buffer
	if err := p.Output(&b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func (e *agentEnv) put(t *testing.T, path string, data []byte) {
	t.Helper()
	if _, err := e.svc.Write(e.ctx, drive.Human("u"), drive.WriteInput{Path: path, Data: data}); err != nil {
		t.Fatal(err)
	}
}

// fakeRender renders "pages" as tiny PNGs and records what it was asked.
type fakeRender struct {
	pages [][]int
	norm  int
	err   error
}

func (f *fakeRender) RenderPDF(_ context.Context, data []byte, pages []int, opt drive.RenderOptions) ([]drive.RenderedImage, error) {
	f.pages = append(f.pages, pages)
	if f.err != nil {
		return nil, f.err
	}
	var out []drive.RenderedImage
	for _, p := range pages {
		out = append(out, drive.RenderedImage{Page: p, Mime: "image/png", Data: []byte("\x89PNG\r\n\x1a\nfake"), Width: 100, Height: 140})
	}
	return out, nil
}

func (f *fakeRender) NormalizeImage(_ context.Context, data []byte, _ string, _ drive.RenderOptions) (drive.RenderedImage, error) {
	f.norm++
	if f.err != nil {
		return drive.RenderedImage{}, f.err
	}
	return drive.RenderedImage{Mime: "image/png", Data: []byte("\x89PNG\r\n\x1a\nnorm"), Width: 10, Height: 10}, nil
}

const informe = "Las ventas del tercer trimestre crecieron un doce por ciento y los costos operativos se mantuvieron estables."

func TestReadPDFTextWithPages(t *testing.T) {
	e := newAgentEnv(t)
	e.put(t, "/Shared/informe.pdf", textPDF(t, informe))
	f, _ := e.svc.FileByPath(e.ctx, "/Shared/informe.pdf")
	if f.Extraction != "pdf" || !strings.Contains(f.SearchText, "tercer trimestre") {
		t.Fatalf("a PDF must be indexed with its text: extraction=%q", f.Extraction)
	}
	r := e.fs.Run(e.ctx, "sales", "read", map[string]any{"path": "/Shared/informe.pdf"}, false)
	if !r.OK || !strings.Contains(r.Text, "[page 1]") || !strings.Contains(r.Text, "tercer trimestre") || !strings.Contains(r.Text, "pages: 1") {
		t.Fatalf("pdf text read: %+v", r)
	}
	if strings.Contains(r.Text, "scanned: true") || len(r.Images) != 0 {
		t.Fatal("a text PDF is not scanned and attaches nothing")
	}
	// fs.search finds the PDF by its text.
	s := e.fs.Run(e.ctx, "sales", "search", map[string]any{"query": "trimestre"}, false)
	if !strings.Contains(s.Text, "/Shared/informe.pdf") {
		t.Fatalf("search must find the PDF: %q", s.Text)
	}
	// The API text endpoint carries the flags.
	tx, err := e.svc.ReadText(e.ctx, f, 0, 0, 0)
	if err != nil || tx.Info.Kind != "pdf" || tx.Info.Pages != 1 || tx.Info.Scanned {
		t.Fatalf("ReadText: %+v %v", tx.Info, err)
	}
	if r := e.fs.Run(e.ctx, "sales", "read", map[string]any{"path": "/Shared/informe.pdf", "pages": "x-y"}, false); r.Code != "invalid_args" {
		t.Fatalf("bad page range: %+v", r)
	}
}

func TestReadScannedPDFDegradesWithoutRenderer(t *testing.T) {
	e := newAgentEnv(t)
	e.put(t, "/Shared/escaneo.pdf", scannedPDF(t, 2))
	r := e.fs.Run(e.ctx, "sales", "read", map[string]any{"path": "/Shared/escaneo.pdf"}, false)
	if !r.OK || !strings.Contains(r.Text, "scanned: true") || !strings.Contains(r.Text, "cannot be rendered here") {
		t.Fatalf("scanned pdf without renderer: %q", r.Text)
	}
	v := e.fs.Run(e.ctx, "sales", "read", map[string]any{"path": "/Shared/escaneo.pdf", "view": "image", "pages": "1-2"}, false)
	if !v.OK || len(v.Images) != 0 || !strings.Contains(v.Text, drive.ViewRenderUnavailable) {
		t.Fatalf("view without renderer must say so in plain words: %+v", v)
	}
}

func TestReadScannedPDFPagesWithRenderer(t *testing.T) {
	e := newAgentEnv(t)
	fr := &fakeRender{}
	e.fs.Render = fr
	e.put(t, "/Shared/escaneo.pdf", scannedPDF(t, 6))
	r := e.fs.Run(e.ctx, "sales", "read", map[string]any{"path": "/Shared/escaneo.pdf"}, false)
	if !strings.Contains(r.Text, `view:\"image\"`) && !strings.Contains(r.Text, `view:"image"`) {
		t.Fatalf("a scanned PDF must explain how to see its pages: %q", r.Text)
	}
	v := e.fs.Run(e.ctx, "sales", "read", map[string]any{"path": "/Shared/escaneo.pdf", "view": "image", "pages": "2-6"}, false)
	if !v.OK || len(v.Images) != 4 || v.Images[0].Page != 2 || v.Images[0].SourcePath != "/Shared/escaneo.pdf" {
		t.Fatalf("pages as images (capped at 4): %+v", v)
	}
	if len(fr.pages) != 1 || len(fr.pages[0]) != 4 || !strings.Contains(v.Text, "pages from 6") {
		t.Fatalf("the renderer must get at most 4 pages and the agent must know: %v %q", fr.pages, v.Text)
	}
	if !strings.Contains(v.Text, "DATA") {
		t.Fatal("the block must say that what is seen is data")
	}
}

func TestReadImageAttachesNormalizedImage(t *testing.T) {
	e := newAgentEnv(t)
	e.put(t, "/Shared/grafico.png", pngBytes(t, 2400, 1200))
	r := e.fs.Run(e.ctx, "sales", "read", map[string]any{"path": "/Shared/grafico.png"}, false)
	if !r.OK || len(r.Images) != 1 {
		t.Fatalf("image read: %+v", r)
	}
	im := r.Images[0]
	if im.Width != 1568 || im.Height != 784 || im.SourcePath != "/Shared/grafico.png" || len(im.Data) == 0 {
		t.Fatalf("normalized with the Go fallback: %dx%d %s", im.Width, im.Height, im.SourcePath)
	}
	// With a renderer the sandbox prepares it.
	fr := &fakeRender{}
	e.fs.Render = fr
	r = e.fs.Run(e.ctx, "sales", "read", map[string]any{"path": "/Shared/grafico.png"}, false)
	if fr.norm != 1 || len(r.Images) != 1 || string(r.Images[0].Data) != "\x89PNG\r\n\x1a\nnorm" {
		t.Fatalf("renderer path: %+v", r)
	}
	// An unreachable renderer falls back to the Go decoders.
	fr.err = drive.ErrRenderUnavailable
	r = e.fs.Run(e.ctx, "sales", "read", map[string]any{"path": "/Shared/grafico.png"}, false)
	if len(r.Images) != 1 || r.Images[0].Width != 1568 {
		t.Fatalf("fallback after an unavailable renderer: %+v", r)
	}
	// view:image on a text file is explained, never attached.
	v := e.fs.Run(e.ctx, "sales", "read", map[string]any{"path": "/Shared/politica.md", "view": "image"}, false)
	if len(v.Images) != 0 || !strings.Contains(v.Text, drive.ViewNotImage) {
		t.Fatalf("view on text: %+v", v)
	}
}

// The image part follows the permission checks: an agent never gets the pixels
// of a file outside what it may read, through the Gateway tool or AgentFS.
func TestImagePartFollowsPermissions(t *testing.T) {
	e := newAgentEnv(t)
	e.fs.Render = &fakeRender{}
	e.put(t, "/Clients/Acme/foto.png", pngBytes(t, 50, 50))
	e.put(t, "/Agents/legal/escaneo.pdf", scannedPDF(t, 1))
	for _, p := range []string{"/Clients/Acme/foto.png", "/Agents/legal/escaneo.pdf"} {
		r := e.fs.Run(e.ctx, "sales", "read", map[string]any{"path": p, "view": "image", "pages": "1"}, false)
		if r.OK || r.Code != "not_granted" || len(r.Images) != 0 {
			t.Fatalf("%s must be refused without pixels: %+v", p, r)
		}
	}
	gw := &drive.GatewayTool{FS: e.fs}
	call := application.GatewayCall{Org: "o1", AgentID: "sales", Tool: "fs", Action: "read", Args: map[string]any{"path": "/Clients/Acme/foto.png"}}
	res := gw.Run(e.ctx, call, false)
	if len(res.Images) != 0 || !strings.Contains(strings.Join(res.Blocks, ""), "not_granted") {
		t.Fatalf("gateway must not attach a refused image: %+v", res)
	}
	// The tool label is checked too: an agent without fs/fs_read gets nothing.
	gw.AgentTools = func(context.Context, string, string) ([]string, error) { return []string{"email"}, nil }
	call.Args = map[string]any{"path": "/Agents/sales/x.png"}
	e.put(t, "/Agents/sales/x.png", pngBytes(t, 20, 20))
	if res := gw.Run(e.ctx, call, false); res.Code != "tool_not_granted" || len(res.Images) != 0 {
		t.Fatalf("tool label: %+v", res)
	}
	// After a human grant the same image is attached, and the block marks it as data.
	gw.AgentTools = nil
	if _, err := e.svc.AddGrant(e.ctx, drive.Human("u"), "sales", "/Clients/Acme"); err != nil {
		t.Fatal(err)
	}
	call.Args = map[string]any{"path": "/Clients/Acme/foto.png"}
	res = gw.Run(e.ctx, call, false)
	if res.Status != "succeeded" || len(res.Images) != 1 || !res.Tainted || len(res.Blocks) != 1 || !strings.Contains(res.Blocks[0], "untrusted") {
		t.Fatalf("granted image: status=%s images=%d tainted=%v blocks=%q", res.Status, len(res.Images), res.Tainted, res.Blocks)
	}
}
