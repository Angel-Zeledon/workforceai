package sandbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/sandbox"
)

var pngSig = []byte("\x89PNG\r\n\x1a\nxx")

func TestDriveRendererValidatesOutput(t *testing.T) {
	ok := func(_ context.Context, req sandbox.RenderRequest) (sandbox.RenderResponse, error) {
		var out sandbox.RenderResponse
		out.Status = sandbox.RenderOK
		for _, p := range req.Pages {
			out.Images = append(out.Images, sandbox.RenderImageOut{Page: p, Mime: "image/png", Data: pngSig, Width: 100, Height: 120})
		}
		return out, nil
	}
	d := sandbox.DriveRenderer{R: sandbox.FakeRenderer{Fn: ok}}
	imgs, err := d.RenderPDF(context.Background(), []byte("%PDF-1.4"), []int{2, 3}, drive.RenderOptions{MaxSide: 1568, MaxBytes: 1 << 20, MaxPages: 4})
	if err != nil || len(imgs) != 2 || imgs[1].Page != 3 {
		t.Fatalf("%v %v", imgs, err)
	}
	bad := []sandbox.RenderImageOut{
		{Page: 9, Mime: "image/png", Data: pngSig, Width: 10, Height: 10},           // a page not asked for
		{Page: 2, Mime: "image/png", Data: []byte("GIF89a"), Width: 10, Height: 10}, // wrong type
		{Page: 2, Mime: "image/png", Data: pngSig, Width: 9000, Height: 10},         // over the side
	}
	for i, im := range bad {
		d := sandbox.DriveRenderer{R: sandbox.FakeRenderer{Fn: func(context.Context, sandbox.RenderRequest) (sandbox.RenderResponse, error) {
			return sandbox.RenderResponse{Status: sandbox.RenderOK, Images: []sandbox.RenderImageOut{im}}, nil
		}}}
		if _, err := d.RenderPDF(context.Background(), []byte("%PDF-1.4"), []int{2}, drive.RenderOptions{MaxSide: 1568, MaxBytes: 1 << 20}); err == nil {
			t.Fatalf("case %d: untrusted output must be re-validated", i)
		}
	}
	// A failed render carries its stable code.
	d = sandbox.DriveRenderer{R: sandbox.FakeRenderer{Fn: func(context.Context, sandbox.RenderRequest) (sandbox.RenderResponse, error) {
		return sandbox.RenderResponse{Status: sandbox.RenderFailed, Code: "pdf_encrypted"}, nil
	}}}
	_, err = d.RenderPDF(context.Background(), []byte("%PDF-1.4"), []int{1}, drive.RenderOptions{})
	var rf *sandbox.ErrRenderFailed
	if !errors.As(err, &rf) || rf.RenderCode() != "pdf_encrypted" {
		t.Fatalf("code: %v", err)
	}
	// No client: unavailable.
	if _, err := (sandbox.DriveRenderer{}).NormalizeImage(context.Background(), pngSig, "image/png", drive.RenderOptions{}); !errors.Is(err, drive.ErrRenderUnavailable) {
		t.Fatalf("nil client: %v", err)
	}
}

func TestHTTPRunnerRender(t *testing.T) {
	var got sandbox.RenderRequest
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/render", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(sandbox.RenderResponse{Status: sandbox.RenderOK, Images: []sandbox.RenderImageOut{{Mime: "image/png", Data: pngSig, Width: 5, Height: 5}}})
	})
	hs := httptest.NewServer(mux)
	defer hs.Close()
	d := sandbox.DriveRenderer{R: sandbox.NewHTTPRunner(hs.URL, "tok")}
	im, err := d.NormalizeImage(context.Background(), []byte("\xff\xd8\xffjpeg"), "image/jpeg", drive.RenderOptions{MaxSide: 800, MaxBytes: 1 << 20})
	if err != nil || im.Width != 5 || got.Kind != sandbox.RenderImage || string(got.Data) != "\xff\xd8\xffjpeg" || got.MaxSide != 800 {
		t.Fatalf("%+v %v %+v", im, err, got)
	}
	// A runner without the render endpoint / image, or unreachable, is "unavailable".
	off := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer off.Close()
	for _, url := range []string{off.URL, "http://127.0.0.1:1"} {
		_, err := (sandbox.DriveRenderer{R: sandbox.NewHTTPRunner(url, "tok")}).NormalizeImage(context.Background(), pngSig, "image/png", drive.RenderOptions{})
		if !errors.Is(err, drive.ErrRenderUnavailable) {
			t.Fatalf("%s: %v", url, err)
		}
	}
}

func TestValidateRender(t *testing.T) {
	good := sandbox.RenderRequest{RenderID: "rnd_abcdef", Kind: "pdf", Data: []byte("x"), Pages: []int{1}}
	if err := sandbox.ValidateRender(good); err != nil {
		t.Fatal(err)
	}
	for _, r := range []sandbox.RenderRequest{
		{RenderID: "bad id!", Kind: "pdf", Data: []byte("x")},
		{RenderID: "rnd_abcdef", Kind: "exe", Data: []byte("x")},
		{RenderID: "rnd_abcdef", Kind: "pdf"},
		{RenderID: "rnd_abcdef", Kind: "pdf", Data: []byte("x"), Pages: []int{1, 2, 3, 4, 5, 6, 7, 8, 9}},
		{RenderID: "rnd_abcdef", Kind: "pdf", Data: []byte("x"), Pages: []int{0}},
		{RenderID: "rnd_abcdef", Kind: "image", Data: []byte("x"), MaxSide: 99999},
	} {
		if sandbox.ValidateRender(r) == nil {
			t.Fatalf("must be refused: %+v", r)
		}
	}
}
