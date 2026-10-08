package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

type auditSink struct {
	mu sync.Mutex
	ev []AuditEvent
}

func (a *auditSink) add(_ context.Context, e AuditEvent) {
	a.mu.Lock()
	a.ev = append(a.ev, e)
	a.mu.Unlock()
}
func (a *auditSink) has(action string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.ev {
		if e.Action == action {
			return true
		}
	}
	return false
}

func TestInvitationLifecycle(t *testing.T) {
	sink := &auditSink{}
	e := newEnv(t, func(c *Config) { c.Audit = sink.add })
	owner := e.register(t, "owner@example.com", "acme")
	op := e.principal(t, owner)

	inv, err := e.svc.CreateInvitation(bg, op, "New@Example.com", RoleMember)
	if err != nil || inv.Token == "" || inv.Email != "new@example.com" || inv.Status != InvitationPending {
		t.Fatalf("create: %+v %v", inv, err)
	}
	if inv.TokenHash != "" && inv.TokenHash == inv.Token {
		t.Fatal("token stored in plaintext")
	}
	// Weak password and missing name are rejected; the invitation stays usable.
	if _, err := e.svc.AcceptInvitation(bg, AcceptInput{Token: inv.Token, Password: "x"}, ClientMeta{}); !errors.Is(err, ErrValidation) {
		t.Fatalf("weak accept: %v", err)
	}
	s, err := e.svc.AcceptInvitation(bg, AcceptInput{Token: inv.Token, Name: "Nueva", Password: goodPW}, ClientMeta{})
	if err != nil || s.OrgID != owner.OrgID || s.Role != RoleMember {
		t.Fatalf("accept: %+v %v", s, err)
	}
	// Single use.
	if _, err := e.svc.AcceptInvitation(bg, AcceptInput{Token: inv.Token, Name: "X", Password: goodPW}, ClientMeta{}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("reuse: %v", err)
	}
	if _, err := e.svc.AcceptInvitation(bg, AcceptInput{Token: "nope", Password: goodPW}, ClientMeta{}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("unknown token: %v", err)
	}
	// Already a member: no new invitation.
	if _, err := e.svc.CreateInvitation(bg, op, "new@example.com", RoleViewer); !errors.Is(err, ErrAlreadyMember) {
		t.Fatalf("invite member: %v", err)
	}
	list, _ := e.svc.ListInvitations(bg, op)
	if len(list) != 1 || list[0].Status != InvitationAccepted || list[0].AcceptedAt == nil {
		t.Fatalf("list: %+v", list)
	}
	for _, a := range []string{"invitation.created", "invitation.accepted"} {
		if !sink.has(a) {
			t.Errorf("missing audit %s", a)
		}
	}
}

func TestInvitationExpiryAndRevocation(t *testing.T) {
	e := newEnv(t)
	owner := e.register(t, "owner@example.com", "acme")
	op := e.principal(t, owner)
	inv, _ := e.svc.CreateInvitation(bg, op, "late@example.com", RoleViewer)
	e.clock.Advance(8 * 24 * time.Hour)
	if _, err := e.svc.AcceptInvitation(bg, AcceptInput{Token: inv.Token, Name: "L", Password: goodPW}, ClientMeta{}); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired: %v", err)
	}
	if err := e.svc.RevokeInvitation(bg, op, inv.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoke expired: %v", err)
	}
	// A new invitation for the same e-mail revokes the previous pending one.
	a, _ := e.svc.CreateInvitation(bg, op, "two@example.com", RoleViewer)
	b, _ := e.svc.CreateInvitation(bg, op, "two@example.com", RoleMember)
	if _, err := e.svc.AcceptInvitation(bg, AcceptInput{Token: a.Token, Name: "T", Password: goodPW}, ClientMeta{}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("superseded: %v", err)
	}
	if err := e.svc.RevokeInvitation(bg, op, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AcceptInvitation(bg, AcceptInput{Token: b.Token, Name: "T", Password: goodPW}, ClientMeta{}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("revoked: %v", err)
	}
}

func TestInvitationRoleEscalation(t *testing.T) {
	e := newEnv(t)
	owner := e.register(t, "owner@example.com", "acme")
	adminID := e.addUserTo(t, owner, "admin@example.com", RoleAdmin)
	memberID := e.addUserTo(t, owner, "member@example.com", RoleMember)
	ap := Principal{UserID: adminID, OrgID: owner.OrgID, Role: RoleAdmin}
	mp := Principal{UserID: memberID, OrgID: owner.OrgID, Role: RoleMember}
	for _, r := range []Role{RoleAdmin, RoleOwner} {
		if _, err := e.svc.CreateInvitation(bg, ap, "x@example.com", r); !errors.Is(err, ErrForbidden) {
			t.Fatalf("admin invites %s: %v", r, err)
		}
	}
	if _, err := e.svc.CreateInvitation(bg, mp, "x@example.com", RoleViewer); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member invites: %v", err)
	}
	// An admin can't revoke an invitation they could not have created.
	oi, _ := e.svc.CreateInvitation(bg, e.principal(t, owner), "boss@example.com", RoleAdmin)
	if err := e.svc.RevokeInvitation(bg, ap, oi.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin revokes admin invite: %v", err)
	}
	// Invitations of an inviter who lost the right to grant the role stop working.
	ai, err := e.svc.CreateInvitation(bg, ap, "viewer@example.com", RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ChangeMemberRole(bg, e.principal(t, owner), adminID, RoleViewer); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AcceptInvitation(bg, AcceptInput{Token: ai.Token, Name: "V", Password: goodPW}, ClientMeta{}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("demoted inviter: %v", err)
	}
}

func TestInvitationCrossTenantAndExistingAccount(t *testing.T) {
	e := newEnv(t)
	a := e.register(t, "a@example.com", "alpha")
	b := e.register(t, "b@example.com", "beta")
	ap, bp := e.principal(t, a), e.principal(t, b)
	inv, _ := e.svc.CreateInvitation(bg, ap, "b@example.com", RoleViewer)
	if l, _ := e.svc.ListInvitations(bg, bp); len(l) != 0 {
		t.Fatalf("org B sees org A invitations: %+v", l)
	}
	if err := e.svc.RevokeInvitation(bg, bp, inv.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant revoke: %v", err)
	}
	// The invitee already has an account: the token alone is not enough.
	if _, err := e.svc.AcceptInvitation(bg, AcceptInput{Token: inv.Token, Password: "Wrong-Password-1"}, ClientMeta{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	s, err := e.svc.AcceptInvitation(bg, AcceptInput{Token: inv.Token, Password: goodPW}, ClientMeta{})
	if err != nil || s.OrgID != a.OrgID || s.User.ID != b.User.ID || s.Role != RoleViewer {
		t.Fatalf("link existing: %+v %v", s, err)
	}
	if ms, _ := e.store.MembershipsOf(bg, b.User.ID); len(ms) != 2 {
		t.Fatalf("memberships: %+v", ms)
	}
}

func TestInvitationHTTP(t *testing.T) {
	h := newHTTPEnv(t, HandlerConfig{})
	r := chi.NewRouter()
	r.Mount("/auth", NewHandler(h.svc, HandlerConfig{}).Routes())
	r.Mount("/invitations", NewHandler(h.svc, HandlerConfig{}).InvitationRoutes())
	h.srv = r
	owner := h.register(t, "owner@example.com", "acme")
	if w := h.do("GET", "/auth/config", "", nil); w.Code != http.StatusOK || !jsonHas(w.Body.String(), `"enabled":true`) {
		t.Fatalf("config: %d %s", w.Code, w.Body)
	}
	if w := h.do("POST", "/invitations", "", map[string]any{"email": "x@example.com", "role": "member"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous invite: %d", w.Code)
	}
	w := h.do("POST", "/invitations", owner.AccessToken, map[string]any{"email": "x@example.com", "role": "member"})
	if w.Code != http.StatusCreated || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("invite: %d %s", w.Code, w.Body)
	}
	var created CreatedInvitation
	decodeBody(t, w, &created)
	if w := h.do("GET", "/invitations", owner.AccessToken, nil); w.Code != http.StatusOK || jsonHas(w.Body.String(), created.Token) {
		t.Fatalf("list leaks token or fails: %d", w.Code)
	}
	w = h.do("POST", "/auth/invitations/accept", "", map[string]any{"token": created.Token, "name": "X", "password": goodPW})
	if w.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", w.Code, w.Body)
	}
	if w := h.do("POST", "/auth/invitations/accept", "", map[string]any{"token": created.Token, "name": "X", "password": goodPW}); w.Code != http.StatusUnauthorized {
		t.Fatalf("reuse: %d", w.Code)
	}
}

// invitationContract is run against every Store (memory always, Postgres with TEST_DATABASE_URL).
func invitationContract(t *testing.T, st Store) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	u, o, m := User{ID: "u-" + randomHex(4), Email: "ic-" + randomHex(3) + "@example.com", Name: "N", PasswordHash: "h", CreatedAt: now},
		Org{ID: "o-" + randomHex(4), Name: "O", Slug: "ic-" + randomHex(4)}, Membership{}
	m = Membership{OrgID: o.ID, UserID: u.ID, Role: RoleOwner, CreatedAt: now}
	if err := st.CreateAccount(bg, u, o, m); err != nil {
		t.Fatal(err)
	}
	inv := Invitation{ID: "i-" + randomHex(4), OrgID: o.ID, Email: "inv-" + randomHex(3) + "@example.com", Role: RoleMember,
		TokenHash: randomHex(32), InvitedBy: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := st.CreateInvitation(bg, inv, now); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := st.InvitationByTokenHash(bg, inv.TokenHash)
	if err != nil || got.ID != inv.ID || got.Status(now) != InvitationPending {
		t.Fatalf("by hash: %+v %v", got, err)
	}
	if _, err := st.GetInvitation(bg, "other-org", inv.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org get: %v", err)
	}
	nu := &User{ID: "u-" + randomHex(4), Email: inv.Email, Name: "I", PasswordHash: "h", CreatedAt: now}
	if err := st.AcceptInvitation(bg, inv.TokenHash, nu, Membership{OrgID: o.ID, UserID: nu.ID, Role: RoleMember, CreatedAt: now}, now); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := st.AcceptInvitation(bg, inv.TokenHash, nil, Membership{OrgID: o.ID, UserID: nu.ID, Role: RoleMember}, now); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("double accept: %v", err)
	}
	if _, err := st.GetMembership(bg, o.ID, nu.ID); err != nil {
		t.Fatalf("membership: %v", err)
	}
	exp := inv
	exp.ID, exp.Email, exp.TokenHash, exp.ExpiresAt = "i-"+randomHex(4), "exp-"+randomHex(3)+"@example.com", randomHex(32), now.Add(-time.Minute)
	if err := st.CreateInvitation(bg, exp, now); err != nil {
		t.Fatal(err)
	}
	if err := st.AcceptInvitation(bg, exp.TokenHash, nil, Membership{OrgID: o.ID, UserID: u.ID, Role: RoleMember}, now); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired accept: %v", err)
	}
	if err := st.RevokeInvitation(bg, o.ID, exp.ID, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoke expired: %v", err)
	}
	if l, _ := st.ListInvitations(bg, o.ID); len(l) != 2 {
		t.Fatalf("list: %d", len(l))
	}
}

func TestMemoryInvitationContract(t *testing.T) { invitationContract(t, NewMemoryStore()) }

func jsonHas(body, s string) bool { return strings.Contains(body, s) }

func decodeBody(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %s: %v", w.Body, err)
	}
}

func TestSwitchOrg(t *testing.T) {
	e := newEnv(t)
	a := e.register(t, "a@example.com", "alpha")
	b := e.register(t, "b@example.com", "beta")
	if _, err := e.svc.SwitchOrg(bg, e.principal(t, a), b.OrgID, ClientMeta{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("switch to foreign org: %v", err)
	}
	inv, _ := e.svc.CreateInvitation(bg, e.principal(t, b), "a@example.com", RoleViewer)
	if _, err := e.svc.AcceptInvitation(bg, AcceptInput{Token: inv.Token, Password: goodPW}, ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	s, err := e.svc.SwitchOrg(bg, e.principal(t, a), b.OrgID, ClientMeta{})
	if err != nil || s.OrgID != b.OrgID || s.Role != RoleViewer {
		t.Fatalf("switch: %+v %v", s, err)
	}
	orgs, _ := e.svc.Orgs(bg, a.User.ID)
	names := map[string]OrgRef{}
	for _, o := range orgs {
		names[o.OrgID] = o
	}
	if len(orgs) != 2 || names[a.OrgID].Name != "alpha Inc" || names[b.OrgID].Name != "beta Inc" || names[b.OrgID].Role != RoleViewer {
		t.Fatalf("orgs: %+v", orgs)
	}
}
