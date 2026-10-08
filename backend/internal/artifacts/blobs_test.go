package artifacts_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/domain"
)

const onePagePDF = "%PDF-1.7\n1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n2 0 obj << /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >> endobj\n" +
	"3 0 obj << /Type /Page /Parent 2 0 R >> endobj\n4 0 obj << /Type/Page /Parent 2 0 R >> endobj\n%%EOF\n"

func pdfArt(e *env) artifacts.Artifact {
	return e.create(e.me, artifacts.CreateInput{Kind: artifacts.KindPDF, Title: "Contrato"})
}

func TestUploadPDFWritesVersionAndAudits(t *testing.T) {
	e := newEnv(t, nil)
	a := pdfArt(e)
	res, err := e.svc.UploadPDF(e.ctx, e.me, a.ID, artifacts.UploadInput{Filename: `C:\docs\con"trato`, ContentType: "application/pdf; charset=binary", Data: []byte(onePagePDF)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != 2 || res.Pages != 2 || res.Blob.Filename != "contrato.pdf" || len(res.Blob.SHA256) != 64 {
		t.Fatalf("result: %+v", res)
	}
	got, _ := e.svc.Get(e.ctx, a.ID, 0)
	if !strings.Contains(string(got.Content), `"blob_id":"`+res.Blob.ID+`"`) || !strings.Contains(string(got.Content), `"pages":2`) {
		t.Fatalf("content: %s", got.Content)
	}
	b, err := e.svc.PDFBlob(e.ctx, e.me, res.Blob.ID, true)
	if err != nil || string(b.Data) != onePagePDF {
		t.Fatalf("blob: %v", err)
	}
	var up, down bool
	for _, l := range e.store.Audit() {
		up = up || l.Action == "artifact.pdf_uploaded"
		down = down || l.Action == "artifact.pdf_downloaded"
	}
	if !up || !down {
		t.Errorf("audit entries: upload=%v download=%v", up, down)
	}
	// Version 1 still has no file: the old content is kept in history.
	v1, _ := e.svc.Get(e.ctx, a.ID, 1)
	if !strings.Contains(string(v1.Content), `"blob_id":null`) {
		t.Errorf("v1: %s", v1.Content)
	}
}

func TestUploadPDFRejections(t *testing.T) {
	ro := false
	e := newEnv(t, func(c *artifacts.Config) {
		c.WriteGate = func(context.Context, string) error {
			if ro {
				return artifacts.ErrReadOnly
			}
			return nil
		}
	})
	a := pdfArt(e)
	up := func(actor artifacts.Actor, ct, data string) error {
		_, err := e.svc.UploadPDF(e.ctx, actor, a.ID, artifacts.UploadInput{Filename: "x.pdf", ContentType: ct, Data: []byte(data)})
		return err
	}
	if err := up(e.me, "text/html", onePagePDF); !errors.Is(err, artifacts.ErrUnsupportedMedia) {
		t.Errorf("content type: %v", err)
	}
	if err := up(e.me, "application/pdf", "<script>alert(1)</script>"); !errors.Is(err, artifacts.ErrNotPDF) || !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("magic bytes: %v", err)
	}
	if err := up(e.me, "application/pdf", " %PDF-1.4"); !errors.Is(err, artifacts.ErrNotPDF) {
		t.Errorf("signature not at offset 0: %v", err)
	}
	if err := up(e.me, "application/pdf", "%PDF-"+strings.Repeat("x", artifacts.MaxPDFBytes)); !errors.Is(err, artifacts.ErrTooLarge) {
		t.Errorf("size: %v", err)
	}
	if err := up(artifacts.Actor{Kind: artifacts.ActorAgent, ID: "legal"}, "application/pdf", onePagePDF); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("agent upload: %v", err)
	}
	ro = true
	if err := up(e.me, "application/pdf", onePagePDF); !errors.Is(err, artifacts.ErrReadOnly) {
		t.Errorf("read-only: %v", err)
	}
	ro = false
	sheet := e.sheetArt(`{}`)
	if _, err := e.svc.UploadPDF(e.ctx, e.me, sheet.ID, artifacts.UploadInput{ContentType: "application/pdf", Data: []byte(onePagePDF)}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("pdf into a sheet: %v", err)
	}
	if _, err := e.svc.PDFBlob(e.ctx, e.me, "../../etc/passwd", false); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("malformed blob id: %v", err)
	}
	// Nothing was written by the refused uploads.
	if got, _ := e.svc.Get(e.ctx, a.ID, 0); got.Version != 1 {
		t.Errorf("refused uploads wrote version %d", got.Version)
	}
}

func TestBlobsAreScopedByOrganization(t *testing.T) {
	b := artifacts.NewMemBlobs()
	ctx := context.Background()
	if err := b.PutBlob(ctx, "org-a", artifacts.Blob{ID: "blob_0123456789abcdef0123", Data: []byte("%PDF-")}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.GetBlob(ctx, "org-b", "blob_0123456789abcdef0123"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-org read: %v", err)
	}
	if err := b.PutBlob(ctx, "org-a", artifacts.Blob{ID: "blob_0123456789abcdef0123"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("blobs must be immutable: %v", err)
	}
}
