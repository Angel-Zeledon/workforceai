package sso

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/auth"
)

// dnsFake answers TXT lookups from a map; a missing name is "not found" (a real lapse), fail makes every lookup a transport error.
type dnsFake struct {
	mu   sync.Mutex
	txt  map[string][]string
	fail bool
}

func (d *dnsFake) LookupTXT(_ context.Context, name string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fail {
		return nil, errors.New("i/o timeout")
	}
	if v, ok := d.txt[name]; ok {
		return v, nil
	}
	return nil, ErrNoRecord
}

type auditLog struct {
	mu sync.Mutex
	a  []string
}

func (l *auditLog) add(_ context.Context, org, _ string, action string, _ map[string]any) {
	l.mu.Lock()
	l.a = append(l.a, org+":"+action)
	l.mu.Unlock()
}
func (l *auditLog) has(s string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, x := range l.a {
		if x == s {
			return true
		}
	}
	return false
}

type janFixture struct {
	j     *Janitor
	store *MemStore
	dns   *dnsFake
	log   *auditLog
	now   time.Time
	svc   *Service
	d     DomainView
}

func newJanFixture(t *testing.T) *janFixture {
	t.Helper()
	f := &janFixture{store: NewMemStore(), dns: &dnsFake{txt: map[string][]string{}}, log: &auditLog{}, now: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}
	svc, err := New(Options{Store: f.store, Resolver: f.dns, Now: func() time.Time { return f.now }, Audit: f.log.add})
	if err != nil {
		t.Fatal(err)
	}
	f.svc = svc
	f.j = &Janitor{Store: f.store, Resolver: f.dns, Now: func() time.Time { return f.now }, Audit: f.log.add, Grace: 7 * 24 * time.Hour, Recheck: 24 * time.Hour}
	ctx := context.Background()
	f.d, err = svc.AddDomain(ctx, orgA, "admin", "acme.io")
	if err != nil {
		t.Fatal(err)
	}
	f.dns.txt[f.d.Challenge.Name] = []string{f.d.Challenge.Value}
	if _, err := svc.VerifyDomain(ctx, orgA, "admin", "acme.io"); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *janFixture) verified(t *testing.T) bool {
	t.Helper()
	_, err := f.store.OrgForVerifiedDomain(context.Background(), "acme.io")
	return err == nil
}

func TestJanitorPurgesExpiredLoginRecords(t *testing.T) {
	f := newJanFixture(t)
	ctx := context.Background()
	_ = f.store.SavePending(ctx, Pending{Kind: KindState, Hash: "old", OrgID: orgA, ExpiresAt: f.now.Add(-time.Minute)})
	_ = f.store.SavePending(ctx, Pending{Kind: KindState, Hash: "live", OrgID: orgA, ExpiresAt: f.now.Add(time.Minute)})
	f.j.RunOnce(ctx)
	if len(f.store.pending) != 1 {
		t.Fatalf("pending = %d, want only the live record", len(f.store.pending))
	}
	if _, ok := f.store.pending[[2]string{KindState, "live"}]; !ok {
		t.Fatal("the live record must stay")
	}
}

func TestJanitorKeepsDomainWhileRecordIsThere(t *testing.T) {
	f := newJanFixture(t)
	ctx := context.Background()
	f.now = f.now.Add(48 * time.Hour)
	r := f.j.RunOnce(ctx)
	if r.Checked != 1 || r.Lapsed != 0 || r.Revoked != 0 || !f.verified(t) {
		t.Fatalf("report = %+v verified=%v", r, f.verified(t))
	}
}

func TestJanitorLapseGraceThenRevoke(t *testing.T) {
	f := newJanFixture(t)
	ctx := context.Background()
	delete(f.dns.txt, f.d.Challenge.Name) // the record disappears

	f.now = f.now.Add(25 * time.Hour)
	if r := f.j.RunOnce(ctx); r.Lapsed != 1 || r.Revoked != 0 {
		t.Fatalf("first miss = %+v", r)
	}
	if !f.verified(t) || !f.log.has(orgA+":domain.lapsed") {
		t.Fatal("the domain keeps working during the grace period, and the lapse is audited")
	}
	// Still inside the grace period: nothing changes.
	f.now = f.now.Add(3 * 24 * time.Hour)
	if r := f.j.RunOnce(ctx); r.Revoked != 0 || !f.verified(t) {
		t.Fatalf("inside grace = %+v", r)
	}
	// Past the grace period: revoked.
	f.now = f.now.Add(5 * 24 * time.Hour)
	if r := f.j.RunOnce(ctx); r.Revoked != 1 {
		t.Fatalf("after grace = %+v", r)
	}
	if f.verified(t) || !f.log.has(orgA+":domain.verification_revoked") {
		t.Fatal("a lapsed domain must leave the verified index and be audited")
	}
	d, _ := f.store.GetDomain(ctx, orgA, "acme.io")
	if d.Verified() || !d.LapsedSince.IsZero() {
		t.Fatalf("domain row = %+v", d)
	}
	if f.svc.Available(ctx, "x@acme.io") {
		t.Fatal("SSO must stop routing to the organization")
	}
	// The organization can verify it again once the record is back.
	f.dns.txt[f.d.Challenge.Name] = []string{f.d.Challenge.Value}
	if _, err := f.svc.VerifyDomain(ctx, orgA, "admin", "acme.io"); err != nil || !f.verified(t) {
		t.Fatalf("re-verify = %v", err)
	}
}

func TestJanitorRecoveryClearsTheLapse(t *testing.T) {
	f := newJanFixture(t)
	ctx := context.Background()
	saved := f.dns.txt[f.d.Challenge.Name]
	delete(f.dns.txt, f.d.Challenge.Name)
	f.now = f.now.Add(25 * time.Hour)
	f.j.RunOnce(ctx)
	f.dns.txt[f.d.Challenge.Name] = saved // back before the grace ran out
	f.now = f.now.Add(25 * time.Hour)
	if r := f.j.RunOnce(ctx); r.Recovered != 1 {
		t.Fatalf("recover = %+v", r)
	}
	// Even long after, the old lapse is forgotten: a new miss restarts the grace period, it does not revoke at once.
	delete(f.dns.txt, f.d.Challenge.Name)
	f.now = f.now.Add(30 * 24 * time.Hour)
	if r := f.j.RunOnce(ctx); r.Revoked != 0 || r.Lapsed != 1 || !f.verified(t) {
		t.Fatalf("new miss = %+v", r)
	}
}

func TestJanitorIgnoresDNSFailures(t *testing.T) {
	f := newJanFixture(t)
	ctx := context.Background()
	delete(f.dns.txt, f.d.Challenge.Name)
	f.dns.fail = true // resolver is down: this says nothing about the domain
	for i := 0; i < 20; i++ {
		f.now = f.now.Add(25 * time.Hour)
		if r := f.j.RunOnce(ctx); r.Lapsed+r.Revoked != 0 || r.Errors == 0 {
			t.Fatalf("run %d = %+v", i, r)
		}
	}
	if !f.verified(t) {
		t.Fatal("a resolver outage must never revoke a domain")
	}
}

func TestJanitorWrongTokenCountsAsMissing(t *testing.T) {
	f := newJanFixture(t)
	ctx := context.Background()
	f.dns.txt[f.d.Challenge.Name] = []string{"aiworkforce-verification=someone-else"} // the domain changed hands
	f.now = f.now.Add(25 * time.Hour)
	if r := f.j.RunOnce(ctx); r.Lapsed != 1 {
		t.Fatalf("= %+v", r)
	}
}

func TestJanitorRechecksOnlyWhenDue(t *testing.T) {
	f := newJanFixture(t)
	ctx := context.Background()
	f.now = f.now.Add(25 * time.Hour)
	if r := f.j.RunOnce(ctx); r.Checked != 1 {
		t.Fatal("first sweep must check")
	}
	f.now = f.now.Add(time.Hour)
	if r := f.j.RunOnce(ctx); r.Checked != 0 {
		t.Fatalf("checked again after one hour: %+v", r)
	}
}

func TestRevokingTheLastDomainSwitchesOffEnforcementAndAutoJoin(t *testing.T) {
	f := newJanFixture(t)
	ctx := context.Background()
	if _, err := f.svc.SetAutoJoin(ctx, orgA, "admin", true, auth.RoleViewer); err != nil {
		t.Fatal(err)
	}
	_ = f.store.PutPolicy(ctx, Policy{OrgID: orgA, EnforceSSO: true, RequireMFA: true})
	delete(f.dns.txt, f.d.Challenge.Name)
	f.now = f.now.Add(25 * time.Hour)
	f.j.RunOnce(ctx)
	f.now = f.now.Add(8 * 24 * time.Hour)
	f.j.RunOnce(ctx)
	p, _ := f.store.GetPolicy(ctx, orgA)
	if p.EnforceSSO || !p.RequireMFA {
		t.Fatalf("policy = %+v: enforcement must lift (nobody could reach SSO) but MFA must stay", p)
	}
	if a, _ := f.store.GetAutoJoin(ctx, orgA); a.Enabled {
		t.Fatal("auto-join without a verified domain must be switched off")
	}
	if _, _, ok, _ := f.svc.AutoJoinFor(ctx, "new@acme.io"); ok {
		t.Fatal("nobody auto-joins through a revoked domain")
	}
}

func TestJanitorStartStopsWithContext(t *testing.T) {
	f := newJanFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	f.j.Start(ctx, time.Hour)
	cancel()
}
