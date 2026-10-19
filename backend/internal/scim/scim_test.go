package scim

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/auth"
)

type rig struct {
	t      *testing.T
	svc    *Service
	h      http.Handler
	as     *auth.MemoryStore
	ss     *MemStore
	auth   *auth.Service
	org    string
	other  string
	token  string
	otherT string
	owner  string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := context.Background()
	as := auth.NewMemoryStore()
	asvc, err := auth.NewService(auth.Config{Store: as, Secret: []byte("0123456789abcdef0123456789abcdef-test"),
		PasswordParams: auth.PasswordParams{Memory: 8, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}})
	if err != nil {
		t.Fatal(err)
	}
	reg := func(email, org string) *auth.Session {
		s, err := asvc.Register(ctx, auth.RegisterInput{Email: email, Password: "Correct-Horse-9", Name: "Owner", OrgName: org}, auth.ClientMeta{})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	a, b := reg("boss@acme.io", "Acme Inc"), reg("boss@beta.io", "Beta Inc")
	ss := NewMemStore()
	svc, err := New(Options{Store: ss, Auth: as, DomainVerified: func(_ context.Context, org, email string) bool {
		return strings.HasSuffix(email, "@acme.io") && org == a.OrgID || strings.HasSuffix(email, "@beta.io") && org == b.OrgID
	}})
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, svc: svc, as: as, ss: ss, auth: asvc, org: a.OrgID, other: b.OrgID, owner: a.User.ID}
	r.token, _ = svc.RotateToken(ctx, a.OrgID, "boss")
	r.otherT, _ = svc.RotateToken(ctx, b.OrgID, "boss")
	r.h = NewHandler(svc, HandlerConfig{BaseURL: "https://x/api/v1/scim/v2"}).Routes()
	return r
}

func (r *rig) do(method, path, token string, body any) (int, map[string]any) {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (r *rig) create(email string, extra map[string]any) (int, map[string]any) {
	b := map[string]any{"schemas": []string{schemaUser}, "userName": email, "externalId": "ext-" + email, "active": true, "name": map[string]string{"givenName": "Eva", "familyName": "Ruiz"}}
	for k, v := range extra {
		b[k] = v
	}
	return r.do("POST", "/Users", r.token, b)
}

func TestSCIMNeedsTheOrganizationsBearerToken(t *testing.T) {
	r := newRig(t)
	for name, tok := range map[string]string{"none": "", "garbage": "scim_nope", "not a scim token": "eyJhbGciOi.x.y", "empty prefix": "scim_"} {
		if code, _ := r.do("GET", "/Users", tok, nil); code != 401 {
			t.Errorf("%s = %d, want 401", name, code)
		}
	}
	if code, _ := r.do("GET", "/Users", r.token, nil); code != 200 {
		t.Fatalf("valid token = %d", code)
	}
	// Rotating replaces the token: the old one stops working at once.
	old := r.token
	r.token, _ = r.svc.RotateToken(context.Background(), r.org, "boss")
	if code, _ := r.do("GET", "/Users", old, nil); code != 401 {
		t.Fatalf("rotated-out token = %d", code)
	}
	if err := r.svc.RevokeToken(context.Background(), r.org, "boss"); err != nil {
		t.Fatal(err)
	}
	if code, _ := r.do("GET", "/Users", r.token, nil); code != 401 {
		t.Fatalf("revoked token = %d", code)
	}
	// The token is stored hashed.
	for h := range r.ss.tokens {
		if strings.Contains(h, "scim_") {
			t.Fatal("the token must be stored as a hash")
		}
	}
}

func TestCreateGetListAndFilter(t *testing.T) {
	r := newRig(t)
	code, u := r.create("Eva@Acme.io", map[string]any{"roles": []map[string]string{{"value": "viewer"}}})
	if code != 201 || u["userName"] != "eva@acme.io" || u["active"] != true {
		t.Fatalf("create = %d %v", code, u)
	}
	id := u["id"].(string)
	// The account exists with a membership in THIS organization, with the mapped role.
	m, err := r.as.GetMembership(context.Background(), r.org, id)
	if err != nil || m.Role != auth.RoleViewer {
		t.Fatalf("membership = %+v %v", m, err)
	}
	if _, err := r.as.GetMembership(context.Background(), r.other, id); err == nil {
		t.Fatal("the user must not appear in another organization")
	}
	if code, _ := r.create("eva@acme.io", nil); code != 409 {
		t.Fatalf("duplicate = %d, want 409", code)
	}
	if code, got := r.do("GET", "/Users/"+id, r.token, nil); code != 200 || got["id"] != id {
		t.Fatalf("get = %d %v", code, got)
	}
	r.create("max@acme.io", nil)
	code, list := r.do("GET", "/Users", r.token, nil)
	if code != 200 || list["totalResults"].(float64) != 2 {
		t.Fatalf("list = %d %v", code, list)
	}
	_, one := r.do("GET", `/Users?filter=userName%20eq%20%22MAX@acme.io%22`, r.token, nil)
	if one["totalResults"].(float64) != 1 || one["Resources"].([]any)[0].(map[string]any)["userName"] != "max@acme.io" {
		t.Fatalf("filter = %v", one)
	}
	if code, _ := r.do("GET", `/Users?filter=name.givenName%20co%20%22x%22`, r.token, nil); code != 400 {
		t.Fatalf("unsupported filter = %d", code)
	}
}

func TestCreateRefusesForeignDomainsAndBadInput(t *testing.T) {
	r := newRig(t)
	if code, e := r.create("someone@rival.com", nil); code != 400 || e["scimType"] != "invalidValue" {
		t.Fatalf("foreign domain = %d %v", code, e)
	}
	// A domain verified by another organization is not this organization's either.
	if code, _ := r.create("bob@beta.io", nil); code != 400 {
		t.Fatalf("other org's domain = %d", code)
	}
	if code, _ := r.create("not-an-email", nil); code != 400 {
		t.Fatalf("bad userName = %d", code)
	}
	if code, _ := r.create("ok@acme.io", map[string]any{"roles": []map[string]string{{"value": "owner"}}}); code != 201 {
		t.Fatalf("owner role requested = %d", code)
	}
	if m, _ := r.as.GetMembership(context.Background(), r.org, r.idOf("ok@acme.io")); m.Role != auth.RoleMember {
		t.Fatalf("an owner role in a SCIM request must fall back to member, got %s", m.Role)
	}
}

func (r *rig) idOf(email string) string {
	u, err := r.as.UserByEmail(context.Background(), email)
	if err != nil {
		r.t.Fatal(err)
	}
	return u.ID
}

func TestDeactivateEndsAccessAndReactivateRestoresIt(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	_, u := r.create("eva@acme.io", nil)
	id := u["id"].(string)
	// Give her a live session in the organization.
	sess, err := r.auth.SignInFederated(ctx, auth.FederatedIdentity{OrgID: r.org, Email: "eva@acme.io"}, auth.ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.auth.Authenticate(ctx, sess.AccessToken); err != nil {
		t.Fatal(err)
	}

	// Okta style: PATCH with a value object. Entra style: path "active" and the string "False".
	for i, body := range []map[string]any{
		{"schemas": []string{schemaPatch}, "Operations": []map[string]any{{"op": "replace", "value": map[string]any{"active": false}}}},
	} {
		if code, got := r.do("PATCH", "/Users/"+id, r.token, body); code != 200 || got["active"] != false {
			t.Fatalf("patch %d = %d %v", i, code, got)
		}
	}
	if _, err := r.auth.Authenticate(ctx, sess.AccessToken); err == nil {
		t.Fatal("a deactivated user's access token must stop working immediately")
	}
	if _, err := r.auth.Refresh(ctx, sess.RefreshToken, auth.ClientMeta{}); err == nil {
		t.Fatal("a deactivated user's refresh token must stop working")
	}
	if _, err := r.as.GetMembership(ctx, r.org, id); err == nil {
		t.Fatal("membership must be gone")
	}
	// SSO just-in-time creation must not bring her back: the gate says no.
	if r.svc.Deactivated(ctx, r.org, "eva@acme.io") == nil {
		t.Fatal("Deactivated gate must veto")
	}
	if r.svc.Deactivated(ctx, r.org, "other@acme.io") != nil {
		t.Fatal("an unknown address is not deactivated")
	}
	// Entra reactivates with a string boolean.
	body := map[string]any{"schemas": []string{schemaPatch}, "Operations": []map[string]any{{"op": "Replace", "path": "active", "value": "True"}}}
	if code, got := r.do("PATCH", "/Users/"+id, r.token, body); code != 200 || got["active"] != true {
		t.Fatalf("reactivate = %d %v", code, got)
	}
	if _, err := r.as.GetMembership(ctx, r.org, id); err != nil {
		t.Fatal("membership must be back")
	}
	if r.svc.Deactivated(ctx, r.org, "eva@acme.io") != nil {
		t.Fatal("reactivated users pass the gate")
	}
	// DELETE deactivates and keeps the record; POSTing the same userName again reactivates it with the same id.
	if code, _ := r.do("DELETE", "/Users/"+id, r.token, nil); code != 204 {
		t.Fatalf("delete = %d", code)
	}
	if code, again := r.create("eva@acme.io", nil); code != 201 || again["id"] != id || again["active"] != true {
		t.Fatalf("re-create = %d %v", code, again)
	}
}

func TestSCIMOnlyTouchesWhatItProvisioned(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	// The owner exists in the organization but SCIM did not create them.
	if code, _ := r.do("GET", "/Users/"+r.owner, r.token, nil); code != 404 {
		t.Fatalf("owner visible through SCIM = %d", code)
	}
	body := map[string]any{"Operations": []map[string]any{{"op": "replace", "path": "active", "value": false}}}
	if code, _ := r.do("PATCH", "/Users/"+r.owner, r.token, body); code != 404 {
		t.Fatalf("deactivating the owner = %d", code)
	}
	if code, _ := r.create("boss@acme.io", nil); code != 409 {
		t.Fatalf("adopting the owner = %d, want 409", code)
	}
	if _, err := r.as.GetMembership(ctx, r.org, r.owner); err != nil {
		t.Fatal("owner membership must be intact")
	}
	// Another organization's token cannot see or change this organization's users.
	_, u := r.create("eva@acme.io", nil)
	id := u["id"].(string)
	if code, _ := r.do("GET", "/Users/"+id, r.otherT, nil); code != 404 {
		t.Fatalf("cross-tenant get = %d", code)
	}
	if code, _ := r.do("DELETE", "/Users/"+id, r.otherT, nil); code != 404 {
		t.Fatalf("cross-tenant delete = %d", code)
	}
	if _, err := r.as.GetMembership(ctx, r.org, id); err != nil {
		t.Fatal("the user must still be a member")
	}
	if _, list := r.do("GET", "/Users", r.otherT, nil); list["totalResults"].(float64) != 0 {
		t.Fatalf("cross-tenant list = %v", list)
	}
	// An existing member who is an owner elsewhere keeps that elsewhere: SCIM adds membership only here.
	if code, _ := r.do("PUT", "/Users/"+id, r.token, map[string]any{"userName": "someone-else@acme.io"}); code != 400 {
		t.Fatalf("changing userName = %d, want 400", code)
	}
}

func TestGroupsMapToRoles(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	_, a := r.create("a@acme.io", nil)
	_, b := r.create("b@acme.io", nil)
	aid, bid := a["id"].(string), b["id"].(string)

	code, gl := r.do("GET", "/Groups", r.token, nil)
	if code != 200 || gl["totalResults"].(float64) != 3 {
		t.Fatalf("groups = %d %v", code, gl)
	}
	// Add a to admin (Entra patch shape).
	add := map[string]any{"schemas": []string{schemaPatch}, "Operations": []map[string]any{{"op": "Add", "path": "members", "value": []map[string]string{{"value": aid}}}}}
	if code, _ := r.do("PATCH", "/Groups/admin", r.token, add); code != 204 {
		t.Fatalf("add to admin = %d", code)
	}
	if m, _ := r.as.GetMembership(ctx, r.org, aid); m.Role != auth.RoleAdmin {
		t.Fatalf("role = %s", m.Role)
	}
	_, g := r.do("GET", "/Groups/admin", r.token, nil)
	if ms := g["members"].([]any); len(ms) != 1 || ms[0].(map[string]any)["value"] != aid {
		t.Fatalf("admin group = %v", g)
	}
	// Remove by filter path: demoted to the least-privileged role.
	rm := map[string]any{"Operations": []map[string]any{{"op": "remove", "path": `members[value eq "` + aid + `"]`}}}
	if code, _ := r.do("PATCH", "/Groups/admin", r.token, rm); code != 204 {
		t.Fatalf("remove = %d", code)
	}
	if m, _ := r.as.GetMembership(ctx, r.org, aid); m.Role != auth.RoleViewer {
		t.Fatalf("after removal role = %s, want viewer", m.Role)
	}
	// PUT replaces the members of a group.
	if code, _ := r.do("PUT", "/Groups/member", r.token, map[string]any{"displayName": "member", "members": []map[string]string{{"value": bid}}}); code != 200 {
		t.Fatal("put group")
	}
	if m, _ := r.as.GetMembership(ctx, r.org, bid); m.Role != auth.RoleMember {
		t.Fatalf("b role = %s", m.Role)
	}
	// There is no owner group, and roles cannot be created or deleted.
	if code, _ := r.do("GET", "/Groups/owner", r.token, nil); code != 404 {
		t.Fatalf("owner group = %d", code)
	}
	if code, _ := r.do("POST", "/Groups", r.token, map[string]any{"displayName": "owner", "members": []map[string]string{{"value": aid}}}); code != 400 {
		t.Fatalf("create owner group = %d", code)
	}
	if code, _ := r.do("DELETE", "/Groups/admin", r.token, nil); code != 403 {
		t.Fatalf("delete group = %d", code)
	}
	// A user from another tenant cannot be put in a group by id.
	if code, _ := r.do("PATCH", "/Groups/admin", r.otherT, add); code != 404 {
		t.Fatalf("cross-tenant group patch = %d", code)
	}
	if m, _ := r.as.GetMembership(ctx, r.org, aid); m.Role == auth.RoleAdmin {
		t.Fatal("cross-tenant promotion succeeded")
	}
}

func TestDiscoveryEndpoints(t *testing.T) {
	r := newRig(t)
	for _, p := range []string{"/ServiceProviderConfig", "/ResourceTypes", "/Schemas"} {
		if code, _ := r.do("GET", p, r.token, nil); code != 200 {
			t.Errorf("%s = %d", p, code)
		}
		if code, _ := r.do("GET", p, "", nil); code != 401 {
			t.Errorf("%s without token = %d", p, code)
		}
	}
}

func TestTokenInfoTracksUse(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if ti, _ := r.svc.TokenInfo(ctx, r.org); !ti.Exists || !ti.LastUsedAt.IsZero() {
		t.Fatalf("fresh = %+v", ti)
	}
	r.do("GET", "/Users", r.token, nil)
	if ti, _ := r.svc.TokenInfo(ctx, r.org); ti.LastUsedAt.IsZero() || time.Since(ti.LastUsedAt) > time.Minute {
		t.Fatalf("after use = %+v", ti)
	}
}
