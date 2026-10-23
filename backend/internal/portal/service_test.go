package portal

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/projects"
)

type auditSpy struct {
	mu  sync.Mutex
	got []string
	det []map[string]any
}

func (a *auditSpy) fn(_ context.Context, org, actor, action, id string, d map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.got = append(a.got, org+"|"+actor+"|"+action)
	a.det = append(a.det, d)
}

func (a *auditSpy) count(action string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, g := range a.got {
		if strings.HasSuffix(g, "|"+action) {
			n++
		}
	}
	return n
}

type fixture struct {
	svc   *Service
	store *MemStore
	audit *auditSpy
	clock time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	d, metas := secretDetail(now)
	f := &fixture{store: NewMemStore(), audit: &auditSpy{}, clock: now}
	f.svc = New(Config{Store: f.store, OrgID: "demo", Audit: f.audit.fn, Now: func() time.Time { return f.clock },
		Project: func(ctx context.Context, org, id string) (projects.Detail, error) {
			if application.OrgFrom(ctx, "") != org {
				t.Errorf("the project is read inside the link's organization (ctx %q, org %q)", application.OrgFrom(ctx, ""), org)
			}
			if org != "org-a" || id != d.Project.ID {
				return projects.Detail{}, domain.ErrNotFound
			}
			return d, nil
		},
		Deliverables: func(_ context.Context, org, pid string) ([]artifacts.Meta, error) {
			if org != "org-a" || pid != d.Project.ID {
				return nil, nil
			}
			return metas, nil
		},
		OrgName: func(context.Context, string) (string, error) { return "Org A Inc", nil },
	})
	return f
}

func TestLinkLifecycle(t *testing.T) {
	f := newFixture(t)
	ctxA := application.WithOrg(context.Background(), "org-a")
	ctxB := application.WithOrg(context.Background(), "org-b")
	pid := "proj-SECRET-ID"

	// Validation.
	if _, err := f.svc.CreateLink(ctxB, "u1", pid, LinkInput{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("another org's project: %v", err)
	}
	if _, err := f.svc.CreateLink(ctxA, "u1", pid, LinkInput{ExpiresInDays: 400}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("expiry cap: %v", err)
	}
	if _, err := f.svc.CreateLink(ctxA, "u1", pid, LinkInput{DeliverableIDs: []string{"art-of-another-project"}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("foreign deliverable: %v", err)
	}
	if _, err := f.svc.CreateLink(ctxA, "u1", pid, LinkInput{Label: strings.Repeat("x", MaxLabel+1)}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("label cap: %v", err)
	}

	c, err := f.svc.CreateLink(ctxA, "u1", pid, LinkInput{Label: " Cliente López\n", ExpiresInDays: 7, DeliverableIDs: []string{"art-SECRET-1", "art-SECRET-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Token) != 43 || c.Label != "Cliente López" || c.Status != LinkActive || len(c.DeliverableIDs) != 1 || c.ExpiresAt == nil {
		t.Fatalf("created: %+v", c)
	}
	stored, _ := f.store.GetLink(context.Background(), "org-a", c.ID)
	if stored.TokenHash != HashToken(c.Token) || strings.Contains(stored.TokenHash, c.Token) {
		t.Fatal("only the hash is stored")
	}
	if f.audit.count("portal_link.created") != 1 {
		t.Fatalf("audit: %v", f.audit.got)
	}
	for _, d := range f.audit.det {
		for _, v := range d {
			if s, ok := v.(string); ok && strings.Contains(s, c.Token) {
				t.Fatal("the token is never audited")
			}
		}
	}

	// Public view: works without any tenant in ctx; first view audited once.
	v, err := f.svc.PublicView(context.Background(), c.Token)
	if err != nil {
		t.Fatal(err)
	}
	if v.Firm.Name != "Org A Inc" || len(v.Deliverables) != 1 || v.Project.TasksTotal != 3 {
		t.Fatalf("view: %+v", v)
	}
	if _, err := f.svc.PublicView(context.Background(), c.Token); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(2 * time.Minute)
	if _, err := f.svc.PublicView(context.Background(), c.Token); err != nil {
		t.Fatal(err)
	}
	if n := f.audit.count("portal_link.first_viewed"); n != 1 {
		t.Fatalf("first view audited %d times", n)
	}
	got, _ := f.store.GetLink(context.Background(), "org-a", c.ID)
	if got.FirstViewedAt == nil || got.LastViewedAt == nil || !got.LastViewedAt.After(*got.FirstViewedAt) {
		t.Fatalf("views: %+v", got)
	}

	// Garbage and unknown tokens are indistinguishable.
	for _, tok := range []string{"", "x", strings.Repeat("A", 43), c.Token + "A", "../../etc/passwd"} {
		if _, err := f.svc.PublicView(context.Background(), tok); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("token %q: %v", tok, err)
		}
	}

	// Editing: deliverables and label change, the token keeps working.
	if _, err := f.svc.UpdateLink(ctxB, "u2", pid, c.ID, LinkInput{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("edit from another org: %v", err)
	}
	if _, err := f.svc.UpdateLink(ctxA, "u2", "other-project", c.ID, LinkInput{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("edit through another project: %v", err)
	}
	up, err := f.svc.UpdateLink(ctxA, "u2", pid, c.ID, LinkInput{Label: "López", DeliverableIDs: []string{"art-SECRET-1", "art-SECRET-3"}})
	if err != nil || len(up.DeliverableIDs) != 2 {
		t.Fatalf("update: %+v %v", up, err)
	}
	v, _ = f.svc.PublicView(context.Background(), c.Token)
	if len(v.Deliverables) != 2 {
		t.Fatalf("after update: %+v", v.Deliverables)
	}

	// Revocation kills the token at once, and the logo with it.
	if _, err := f.svc.RevokeLink(ctxA, "u3", pid, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RevokeLink(ctxA, "u3", pid, c.ID); err != nil {
		t.Fatal("revoking twice is idempotent")
	}
	if f.audit.count("portal_link.revoked") != 1 {
		t.Fatalf("revocation audited once: %v", f.audit.got)
	}
	if _, err := f.svc.PublicView(context.Background(), c.Token); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("revoked: %v", err)
	}
	if _, _, err := f.svc.PublicLogo(context.Background(), c.Token); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("revoked logo: %v", err)
	}
	if _, err := f.svc.UpdateLink(ctxA, "u2", pid, c.ID, LinkInput{}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a revoked link cannot be edited: %v", err)
	}
	ls, _ := f.svc.ListLinks(ctxA, pid)
	if len(ls) != 1 || ls[0].Status != LinkRevoked || ls[0].RevokedBy != "u3" {
		t.Fatalf("list: %+v", ls)
	}

	// Expiry.
	c2, _ := f.svc.CreateLink(ctxA, "u1", pid, LinkInput{ExpiresInDays: 1})
	if _, err := f.svc.PublicView(context.Background(), c2.Token); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(25 * time.Hour)
	if _, err := f.svc.PublicView(context.Background(), c2.Token); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expired: %v", err)
	}
	// No expiry.
	c3, _ := f.svc.CreateLink(ctxA, "u1", pid, LinkInput{})
	f.clock = f.clock.Add(1000 * 24 * time.Hour)
	if _, err := f.svc.PublicView(context.Background(), c3.Token); err != nil || c3.ExpiresAt != nil {
		t.Fatalf("no expiry: %v", err)
	}
}

func TestActiveLinksPerProjectAreCapped(t *testing.T) {
	f := newFixture(t)
	ctxA := application.WithOrg(context.Background(), "org-a")
	for i := 0; i < MaxActivePerProj; i++ {
		if _, err := f.svc.CreateLink(ctxA, "u", "proj-SECRET-ID", LinkInput{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.CreateLink(ctxA, "u", "proj-SECRET-ID", LinkInput{}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("cap: %v", err)
	}
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: 90, B: 200, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestBrandingAndLogo(t *testing.T) {
	f := newFixture(t)
	ctxA := application.WithOrg(context.Background(), "org-a")
	if b, err := f.svc.Branding(ctxA); err != nil || b.DisplayName != "" || b.HasLogo {
		t.Fatalf("defaults: %+v %v", b, err)
	}
	if _, err := f.svc.SetBranding(ctxA, "u", BrandingInput{AccentColor: "red"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("color: %v", err)
	}
	if _, err := f.svc.SetBranding(ctxA, "u", BrandingInput{AccentColor: "#ff0000;background:url(x)"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("css injection: %v", err)
	}
	b, err := f.svc.SetBranding(ctxA, "u", BrandingInput{DisplayName: "  Despacho\tPérez ", AccentColor: "#0B5FFF"})
	if err != nil || b.DisplayName != "DespachoPérez" || b.AccentColor != "#0b5fff" {
		t.Fatalf("set: %+v %v", b, err)
	}
	if _, err := f.svc.SetLogo(ctxA, "u", []byte("<svg onload=alert(1)>")); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("svg/garbage is refused: %v", err)
	}
	b, err = f.svc.SetLogo(ctxA, "u", pngBytes(t, 1200, 600))
	if err != nil || !b.HasLogo {
		t.Fatalf("logo: %+v %v", b, err)
	}
	mime, data, err := f.svc.Logo(ctxA)
	if err != nil || (mime != "image/png" && mime != "image/jpeg") {
		t.Fatalf("logo read: %s %v", mime, err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width > LogoMaxSide || cfg.Height > LogoMaxSide {
		t.Fatalf("the logo is downscaled: %+v %v", cfg, err)
	}
	if b, _ := f.svc.Branding(ctxA); b.DisplayName != "DespachoPérez" {
		t.Fatal("setting the logo keeps the name")
	}
	c, _ := f.svc.CreateLink(ctxA, "u", "proj-SECRET-ID", LinkInput{})
	v, _ := f.svc.PublicView(context.Background(), c.Token)
	if v.Firm.Name != "DespachoPérez" || !v.Firm.HasLogo || v.Firm.AccentColor != "#0b5fff" {
		t.Fatalf("firm: %+v", v.Firm)
	}
	if _, pdata, err := f.svc.PublicLogo(context.Background(), c.Token); err != nil || !bytes.Equal(pdata, data) {
		t.Fatalf("public logo: %v", err)
	}
	if _, err := f.svc.DeleteLogo(ctxA, "u"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.PublicLogo(context.Background(), c.Token); !errors.Is(err, ErrNoLogo) {
		t.Fatalf("no logo: %v", err)
	}
	if n := f.audit.count("portal_branding.updated") + f.audit.count("portal_branding.logo_set") + f.audit.count("portal_branding.logo_removed"); n != 3 {
		t.Fatalf("branding audit: %v", f.audit.got)
	}
	// Another org sees its own (empty) branding.
	if b, _ := f.svc.Branding(application.WithOrg(context.Background(), "org-b")); b.DisplayName != "" || b.HasLogo {
		t.Fatal("branding is per organization")
	}
}

func TestCandidatesListTheProjectDeliverables(t *testing.T) {
	f := newFixture(t)
	ctxA := application.WithOrg(context.Background(), "org-a")
	cs, err := f.svc.Candidates(ctxA, "proj-SECRET-ID")
	if err != nil || len(cs) != 3 {
		t.Fatalf("candidates: %+v %v", cs, err)
	}
	done := 0
	for _, c := range cs {
		if c.Completed {
			done++
		}
	}
	if done != 2 {
		t.Fatalf("approved ones are completed: %+v", cs)
	}
	if _, err := f.svc.Candidates(application.WithOrg(context.Background(), "org-b"), "proj-SECRET-ID"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("another org: %v", err)
	}
}
