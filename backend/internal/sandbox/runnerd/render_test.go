package runnerd

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"

	"aiworkforce/backend/internal/sandbox"
)

func smallPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{1, 2, 3, 255})
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func resultTar(t *testing.T, meta any, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	mb, _ := json.Marshal(meta)
	add := func(name string, d []byte) {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(d)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(d)
	}
	add("meta.json", mb)
	for n, d := range files {
		add("out/"+n, d)
	}
	_ = tw.Close()
	return b.Bytes()
}

func TestParseRenderResultRevalidates(t *testing.T) {
	job := renderJob{Kind: sandbox.RenderPDF, Pages: []int{1, 2}, MaxSide: 100, MaxBytes: 1 << 20}
	meta := map[string]any{"status": "ok", "total_pages": 5, "images": []map[string]any{
		{"name": "page-1.png", "page": 1}, {"name": "page-2.png", "page": 2}, {"name": "page-3.png", "page": 3}, {"name": "../x.png", "page": 2}}}
	files := map[string][]byte{"page-1.png": smallPNG(80, 100), "page-2.png": smallPNG(400, 400), "page-3.png": smallPNG(10, 10)}
	resp, err := parseRenderResult(resultTar(t, meta, files), job)
	if err != nil {
		t.Fatal(err)
	}
	// page 2 is over max_side, page 3 was not asked for, ../x.png is not a valid name.
	if resp.Status != sandbox.RenderOK || len(resp.Images) != 1 || resp.Images[0].Page != 1 || resp.Images[0].Width != 80 || resp.TotalPages != 5 {
		t.Fatalf("%+v", resp)
	}
	// A failure keeps only known codes.
	resp, _ = parseRenderResult(resultTar(t, map[string]any{"status": "failed", "code": "rm -rf", "message": "x"}, nil), job)
	if resp.Status != sandbox.RenderFailed || resp.Code != "render_failed" {
		t.Fatalf("%+v", resp)
	}
	resp, _ = parseRenderResult(resultTar(t, map[string]any{"status": "failed", "code": "pdf_encrypted"}, nil), job)
	if resp.Code != "pdf_encrypted" {
		t.Fatalf("%+v", resp)
	}
	if _, err := parseRenderResult([]byte("not a tar"), job); err == nil {
		t.Fatal("garbage must fail")
	}
}

func TestRenderEndpointWithoutImage(t *testing.T) {
	srv, err := New(Config{Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	body, _ := json.Marshal(sandbox.RenderRequest{RenderID: "rnd_abcdef", Kind: "image", Data: []byte("x")})
	req, _ := http.NewRequest(http.MethodPost, hs.URL+"/v1/render", bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("render needs the token: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodPost, hs.URL+"/v1/render", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("no render image configured: %d", resp.StatusCode)
	}
}

func TestRenderDockerArgsIsolation(t *testing.T) {
	srv, _ := New(Config{Token: testToken, RenderImage: "wfai-sandbox-render:1"})
	args := srv.RenderDockerArgs(sandbox.RenderRequest{RenderID: "rnd_abcdef"})
	joined := strings.Join(args, " ")
	for _, want := range []string{"--network none", "--read-only", "--cap-drop ALL", "no-new-privileges", "--user 65532:65532", "--pull never", "--pids-limit"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
	if args[len(args)-1] != "wfai-sandbox-render:1" || slices.Contains(args, "-v") || strings.Contains(joined, "docker.sock") {
		t.Fatalf("unexpected args: %s", joined)
	}
}

// Integration with REAL Docker and the render image
// (docker build -t wfai-sandbox-render:1 sandbox/render). Skipped without them.
func TestRenderIntegration(t *testing.T) {
	img := os.Getenv("SANDBOX_RENDER_IMAGE")
	if img == "" {
		img = "wfai-sandbox-render:1"
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not available")
	}
	if err := exec.Command("docker", "image", "inspect", img).Run(); err != nil {
		t.Skipf("render image %s not built (docker build -t %s sandbox/render)", img, img)
	}
	// The runner starts docker with a scrubbed environment (no secrets), which also drops DOCKER_HOST: on Windows the
	// default daemon address is a named pipe the scrubbed child cannot find, so the test needs it spelled out.
	if runtime.GOOS == "windows" && os.Getenv("DOCKER_HOST") == "" {
		t.Skip("set DOCKER_HOST=npipe:////./pipe/docker_engine to run this test on Windows")
	}
	srv, err := New(Config{Token: testToken, RenderImage: img, Runtime: os.Getenv("SANDBOX_RUNTIME")})
	if err != nil {
		t.Fatal(err)
	}
	p := fpdf.New("P", "mm", "A4", "")
	p.SetFont("Helvetica", "", 14)
	for i := 0; i < 3; i++ {
		p.AddPage()
		p.Rect(20, 20, 100, 60, "F")
		p.Cell(40, 10, "Scanned page")
	}
	var pdf bytes.Buffer
	_ = p.Output(&pdf)
	resp := srv.render(context.Background(), sandbox.RenderRequest{RenderID: "rnd_itest1", Kind: "pdf", Data: pdf.Bytes(), Pages: []int{2, 3, 9}, DPI: 110, MaxSide: 800, MaxBytes: 2 << 20})
	if resp.Status != sandbox.RenderOK || len(resp.Images) != 2 || resp.TotalPages != 3 || resp.Images[0].Page != 2 {
		t.Fatalf("pdf render: %+v", resp)
	}
	for _, im := range resp.Images {
		if max(im.Width, im.Height) > 800 {
			t.Fatalf("over max side: %dx%d", im.Width, im.Height)
		}
	}
	resp = srv.render(context.Background(), sandbox.RenderRequest{RenderID: "rnd_itest2", Kind: "image", Data: smallPNG(2000, 1000), MaxSide: 500, MaxBytes: 2 << 20})
	if resp.Status != sandbox.RenderOK || len(resp.Images) != 1 || resp.Images[0].Width != 500 {
		t.Fatalf("image render: %+v", resp)
	}
	resp = srv.render(context.Background(), sandbox.RenderRequest{RenderID: "rnd_itest3", Kind: "pdf", Data: []byte("%PDF-1.4 garbage"), Pages: []int{1}, MaxSide: 800, MaxBytes: 2 << 20})
	if resp.Status != sandbox.RenderFailed || resp.Code == "" {
		t.Fatalf("corrupt pdf must fail with a code: %+v", resp)
	}
	// Password-protected: a stable code, no pixels.
	enc := fpdf.New("P", "mm", "A4", "")
	enc.SetProtection(fpdf.CnProtectPrint, "user-secret", "owner-secret")
	enc.AddPage()
	var eb bytes.Buffer
	_ = enc.Output(&eb)
	resp = srv.render(context.Background(), sandbox.RenderRequest{RenderID: "rnd_itest4", Kind: "pdf", Data: eb.Bytes(), Pages: []int{1}, MaxSide: 800, MaxBytes: 2 << 20})
	if resp.Status != sandbox.RenderFailed || resp.Code != "pdf_encrypted" {
		t.Fatalf("encrypted pdf: %+v", resp)
	}
	// A hostile, gigantic page (5 m x 5 m) is rasterized within max_side, not at 110 dpi.
	big := fpdf.NewCustom(&fpdf.InitType{UnitStr: "mm", Size: fpdf.SizeType{Wd: 5000, Ht: 5000}})
	big.AddPage()
	big.Rect(100, 100, 3000, 3000, "F")
	var bb bytes.Buffer
	_ = big.Output(&bb)
	resp = srv.render(context.Background(), sandbox.RenderRequest{RenderID: "rnd_itest5", Kind: "pdf", Data: bb.Bytes(), Pages: []int{1}, DPI: 110, MaxSide: 1000, MaxBytes: 2 << 20})
	if resp.Status != sandbox.RenderOK || len(resp.Images) != 1 || max(resp.Images[0].Width, resp.Images[0].Height) > 1000 {
		t.Fatalf("giant page: %+v", resp)
	}
}
