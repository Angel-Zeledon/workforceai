package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// ---------- password ----------

func TestPasswordHashVerify(t *testing.T) {
	h := PasswordHasher{Params: fastParams}
	enc, err := h.Hash(goodPW)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=8,t=1,p=1$") {
		t.Fatalf("unexpected PHC format: %s", enc)
	}
	ok, rehash, err := h.Verify(goodPW, enc)
	if err != nil || !ok || rehash {
		t.Fatalf("verify good: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	if ok, _, _ := h.Verify(goodPW+"x", enc); ok {
		t.Fatal("wrong password accepted")
	}
	enc2, _ := h.Hash(goodPW)
	if enc == enc2 {
		t.Fatal("salts must differ between hashes")
	}
}

func TestPasswordRehashNeeded(t *testing.T) {
	weak := PasswordHasher{Params: fastParams}
	enc, _ := weak.Hash(goodPW)
	strong := PasswordHasher{Params: PasswordParams{Memory: 16, Time: 2, Threads: 1, SaltLen: 16, KeyLen: 32}}
	ok, rehash, err := strong.Verify(goodPW, enc)
	if err != nil || !ok || !rehash {
		t.Fatalf("expected ok+rehash, got ok=%v rehash=%v err=%v", ok, rehash, err)
	}
}

func TestPasswordMalformedHashes(t *testing.T) {
	h := PasswordHasher{Params: fastParams}
	good, _ := h.Hash(goodPW)
	parts := strings.Split(good, "$")
	cases := map[string]string{
		"empty":        "",
		"not phc":      "plaintext",
		"wrong algo":   strings.Replace(good, "argon2id", "argon2i", 1),
		"bad version":  strings.Replace(good, "v=19", "v=16", 1),
		"huge memory":  strings.Replace(good, "m=8", "m=4294967295", 1),
		"zero time":    strings.Replace(good, "t=1", "t=0", 1),
		"huge time":    strings.Replace(good, "t=1", "t=999", 1),
		"huge threads": strings.Replace(good, "p=1", "p=200", 1),
		"short salt":   "$argon2id$v=19$m=8,t=1,p=1$AAAA$" + parts[5],
		"bad b64":      "$argon2id$v=19$m=8,t=1,p=1$!!!!$" + parts[5],
		"short key":    "$argon2id$v=19$m=8,t=1,p=1$" + parts[4] + "$AAAA",
		"extra part":   good + "$x",
	}
	for name, enc := range cases {
		ok, _, err := h.Verify(goodPW, enc)
		if ok || err == nil {
			t.Errorf("%s: want error, got ok=%v err=%v", name, ok, err)
		}
	}
}

// ---------- JWT ----------

func newSigner(t *testing.T) *TokenSigner {
	t.Helper()
	s, err := NewTokenSigner(testSecret, "iss", "aud")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func baseClaims(now time.Time) Claims {
	return Claims{Subject: "u1", OrgID: "o1", Role: RoleMember, IssuedAt: now.Unix(), NotBefore: now.Unix(),
		ExpiresAt: now.Add(time.Minute).Unix(), ID: "j"}
}

// forge builds a token with arbitrary header/claims signed with secret.
func forge(header string, claims any, secret []byte) string {
	cb, _ := json.Marshal(claims)
	in := b64url.EncodeToString([]byte(header)) + "." + b64url.EncodeToString(cb)
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(in))
	return in + "." + b64url.EncodeToString(m.Sum(nil))
}

func TestJWTRoundTrip(t *testing.T) {
	s := newSigner(t)
	now := time.Now()
	tok, err := s.Sign(baseClaims(now))
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Verify(tok, now)
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != "u1" || c.OrgID != "o1" || c.Role != RoleMember || c.Type != "access" {
		t.Fatalf("claims = %+v", c)
	}
}

func TestJWTSecretTooShort(t *testing.T) {
	if _, err := NewTokenSigner([]byte("short"), "i", "a"); err == nil {
		t.Fatal("short secret must be rejected")
	}
}

func TestJWTExpiredAndFuture(t *testing.T) {
	s := newSigner(t)
	now := time.Now()
	tok, _ := s.Sign(baseClaims(now))
	if _, err := s.Verify(tok, now.Add(time.Minute)); err != ErrTokenExpired {
		t.Fatalf("at exp boundary want ErrTokenExpired, got %v", err)
	}
	if _, err := s.Verify(tok, now.Add(time.Hour)); err != ErrTokenExpired {
		t.Fatalf("want ErrTokenExpired, got %v", err)
	}
	if _, err := s.Verify(tok, now.Add(-time.Hour)); err != ErrInvalidToken {
		t.Fatalf("nbf in future must be invalid, got %v", err)
	}
}

func TestJWTTampering(t *testing.T) {
	s := newSigner(t)
	now := time.Now()
	good, _ := s.Sign(baseClaims(now))
	parts := strings.Split(good, ".")

	// Re-encode payload with escalated role / other org, keep original signature.
	escalate := func(mut func(*Claims)) string {
		c := baseClaims(now)
		c.Issuer, c.Audience, c.Type = "iss", "aud", "access"
		mut(&c)
		cb, _ := json.Marshal(c)
		return parts[0] + "." + b64url.EncodeToString(cb) + "." + parts[2]
	}
	otherSecret := []byte("ffffffffffffffffffffffffffffffffffff")
	valid := baseClaims(now)
	valid.Issuer, valid.Audience, valid.Type = "iss", "aud", "access"

	cases := map[string]string{
		"role escalated, old sig": escalate(func(c *Claims) { c.Role = RoleOwner }),
		"org swapped, old sig":    escalate(func(c *Claims) { c.OrgID = "o2" }),
		"exp extended, old sig":   escalate(func(c *Claims) { c.ExpiresAt += 3600 }),
		"flipped sig byte":        parts[0] + "." + parts[1] + "." + flip(parts[2]),
		"empty sig":               parts[0] + "." + parts[1] + ".",
		"alg none":                unsigned(`{"alg":"none","typ":"JWT"}`, valid),
		"alg none with sig":       forge(`{"alg":"none","typ":"JWT"}`, valid, testSecret),
		"alg HS512 label":         forge(`{"alg":"HS512","typ":"JWT"}`, valid, testSecret),
		"alg RS256 label":         forge(`{"alg":"RS256","typ":"JWT"}`, valid, testSecret),
		"wrong secret":            forge(`{"alg":"HS256","typ":"JWT"}`, valid, otherSecret),
		"empty":                   "",
		"garbage":                 "not-a-jwt",
		"two parts":               parts[0] + "." + parts[1],
		"four parts":              good + ".x",
		"non-base64 payload":      parts[0] + ".%%%." + parts[2],
		"oversize":                strings.Repeat("a", maxTokenLen+1),
	}
	for name, tok := range cases {
		if _, err := s.Verify(tok, now); err == nil {
			t.Errorf("%s: tampered token accepted", name)
		}
	}

	// Sanity: a correctly forged token with the real secret IS accepted
	// (so the failures above are due to the tampering, not the helper).
	if _, err := s.Verify(forge(`{"alg":"HS256","typ":"JWT"}`, valid, testSecret), now); err != nil {
		t.Fatalf("control token rejected: %v", err)
	}
}

func unsigned(header string, claims any) string {
	cb, _ := json.Marshal(claims)
	return b64url.EncodeToString([]byte(header)) + "." + b64url.EncodeToString(cb) + "."
}

func flip(sig string) string {
	b := []byte(sig)
	if b[0] == 'A' {
		b[0] = 'B'
	} else {
		b[0] = 'A'
	}
	return string(b)
}

func TestJWTClaimValidation(t *testing.T) {
	s := newSigner(t)
	now := time.Now()
	hdr := `{"alg":"HS256","typ":"JWT"}`
	mk := func(mut func(*Claims)) string {
		c := baseClaims(now)
		c.Issuer, c.Audience, c.Type = "iss", "aud", "access"
		mut(&c)
		return forge(hdr, c, testSecret)
	}
	bad := map[string]string{
		"wrong issuer":   mk(func(c *Claims) { c.Issuer = "evil" }),
		"wrong audience": mk(func(c *Claims) { c.Audience = "other" }),
		"refresh type":   mk(func(c *Claims) { c.Type = "refresh" }),
		"empty type":     mk(func(c *Claims) { c.Type = "" }),
		"no subject":     mk(func(c *Claims) { c.Subject = "" }),
		"no org":         mk(func(c *Claims) { c.OrgID = "" }),
		"no exp":         mk(func(c *Claims) { c.ExpiresAt = 0 }),
		"iat far ahead":  mk(func(c *Claims) { c.IssuedAt = now.Add(time.Hour).Unix() }),
	}
	for name, tok := range bad {
		if _, err := s.Verify(tok, now); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// A token from another issuer instance (different iss) is rejected.
	other, _ := NewTokenSigner(testSecret, "other-iss", "aud")
	tok, _ := other.Sign(baseClaims(now))
	if _, err := s.Verify(tok, now); err == nil {
		t.Error("cross-issuer token accepted")
	}
}

// ---------- validation ----------

func TestValidateEmail(t *testing.T) {
	good := []string{"a@b.co", "user.name+tag@example.com", "x@sub.domain.org"}
	for _, e := range good {
		if m := ValidateEmail(e); m != "" {
			t.Errorf("%q rejected: %s", e, m)
		}
	}
	bad := []string{"", "plain", "@x.com", "a@", "a@b", "a b@c.com", "Name <a@b.com>", "a@b.com, c@d.com",
		"a@b.com\n", "a\x00@b.com", "\"q\"@b.com", strings.Repeat("a", 250) + "@b.com", "<a@b.com>"}
	for _, e := range bad {
		if m := ValidateEmail(e); m == "" {
			t.Errorf("%q accepted", e)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	good := []string{"Correct-Horse-9", "abcdefgh12", "Lång-lösenord-ok1"}
	for _, p := range good {
		if m := ValidatePassword(p, "u@example.com"); m != "" {
			t.Errorf("%q rejected: %s", p, m)
		}
	}
	bad := map[string]string{
		"short":       "Ab1!",
		"common":      "Password123",
		"all same":    "aaaaaaaaaaaa",
		"one class":   "onlylowercaseletters",
		"is email":    "u@example.com",
		"is local":    "someuser",
		"too long":    strings.Repeat("aA1", 50),
		"bad utf8":    "abc\xff\xfedefghij1A",
		"only digits": "12345678901234",
	}
	for name, p := range bad {
		email := "u@example.com"
		if name == "is local" {
			email = "someuser@example.com"
		}
		if m := ValidatePassword(p, email); m == "" {
			t.Errorf("%s (%q) accepted", name, p)
		}
	}
}

func TestValidateName(t *testing.T) {
	if ValidateName("Ana", 1) != "" || ValidateName("  Acme  ", 2) != "" {
		t.Fatal("valid names rejected")
	}
	for _, n := range []string{"", "   ", "A", "x\ny", "bad\x00name", strings.Repeat("x", 101)} {
		if ValidateName(n, 2) == "" {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{"Acme Corp!": "acme-corp", "  --  ": "org", "Ñandú S.A.": "and-s-a", "": "org"}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q)=%q want %q", in, got, want)
		}
	}
	if len(slugify(strings.Repeat("a", 200))) > 40 {
		t.Error("slug too long")
	}
}

// ---------- RBAC ----------

func TestRolePermissionMatrix(t *testing.T) {
	type row struct {
		perm                         Permission
		owner, admin, member, viewer bool
	}
	rows := []row{
		{PermOrgDelete, true, false, false, false},
		{PermBillingManage, true, false, false, false},
		{PermOrgManage, true, true, false, false},
		{PermMembersManage, true, true, false, false},
		{PermMembersInvite, true, true, false, false},
		{PermAuditRead, true, true, false, false},
		{PermApprovalsDecide, true, true, false, false},
		{PermAgentsWrite, true, true, false, false},
		{PermTasksManage, true, true, false, false},
		{PermRequestsCreate, true, true, true, false},
		{PermTasksCreate, true, true, true, false},
		{PermConversationsPost, true, true, true, false},
		{PermMemoriesWrite, true, true, true, false},
		{PermMembersRead, true, true, true, true},
		{PermAgentsRead, true, true, true, true},
		{PermTasksRead, true, true, true, true},
		{PermReportsRead, true, true, true, true},
		{PermApprovalsRead, true, true, true, true},
		{PermMetricsRead, true, true, true, true},
	}
	for _, r := range rows {
		for role, want := range map[Role]bool{RoleOwner: r.owner, RoleAdmin: r.admin, RoleMember: r.member, RoleViewer: r.viewer} {
			if got := role.Has(r.perm); got != want {
				t.Errorf("%s has %s = %v, want %v", role, r.perm, got, want)
			}
		}
	}
}

func TestUnknownRoleHasNothing(t *testing.T) {
	for _, r := range []Role{"", "root", "OWNER", "superadmin"} {
		if r.Valid() || r.Has(PermAgentsRead) || r.Rank() != 0 || len(r.Permissions()) != 0 {
			t.Errorf("role %q must have no privileges", r)
		}
	}
}

func TestRolesAreNested(t *testing.T) {
	order := []Role{RoleViewer, RoleMember, RoleAdmin, RoleOwner}
	for i := 1; i < len(order); i++ {
		for _, p := range order[i-1].Permissions() {
			if !order[i].Has(p) {
				t.Errorf("%s lacks %s that %s has", order[i], p, order[i-1])
			}
		}
	}
}

func TestCanAssignAndManage(t *testing.T) {
	all := []Role{RoleOwner, RoleAdmin, RoleMember, RoleViewer}
	for _, target := range all {
		if !CanAssign(RoleOwner, target) {
			t.Errorf("owner must assign %s", target)
		}
	}
	if CanAssign(RoleAdmin, RoleOwner) || CanAssign(RoleAdmin, RoleAdmin) {
		t.Error("admin must not grant owner/admin")
	}
	if !CanAssign(RoleAdmin, RoleMember) || !CanAssign(RoleAdmin, RoleViewer) {
		t.Error("admin must grant member/viewer")
	}
	for _, a := range []Role{RoleMember, RoleViewer, "", "x"} {
		for _, target := range all {
			if CanAssign(a, target) || CanManage(a, target) {
				t.Errorf("%q must not assign/manage %s", a, target)
			}
		}
	}
	if CanAssign(RoleOwner, "nope") {
		t.Error("unknown target role assignable")
	}
	if CanManage(RoleAdmin, RoleOwner) || CanManage(RoleAdmin, RoleAdmin) || !CanManage(RoleAdmin, RoleMember) {
		t.Error("admin manage rules wrong")
	}
	if !CanManage(RoleOwner, RoleOwner) {
		t.Error("owner manages owners")
	}
}

// ---------- rate limiting ----------

func TestMemoryLimiter(t *testing.T) {
	clk := newClock()
	l := NewMemoryLimiter()
	l.SetClock(clk.Now)
	lim := Limit{Max: 3, Window: time.Minute}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		r, err := l.Allow(ctx, "k", lim)
		if err != nil || !r.Allowed || r.Remaining != 2-i {
			t.Fatalf("hit %d: %+v %v", i, r, err)
		}
	}
	r, _ := l.Allow(ctx, "k", lim)
	if r.Allowed || r.RetryAfter <= 0 || r.RetryAfter > time.Minute {
		t.Fatalf("4th hit should be denied: %+v", r)
	}
	if r, _ := l.Allow(ctx, "other", lim); !r.Allowed {
		t.Fatal("keys must be independent")
	}
	clk.Advance(61 * time.Second)
	if r, _ := l.Allow(ctx, "k", lim); !r.Allowed {
		t.Fatal("window should have reset")
	}
	if _, err := l.Allow(ctx, "k", Limit{}); err == nil {
		t.Fatal("invalid limit must error")
	}
}

func TestMemoryLimiterConcurrent(t *testing.T) {
	l := NewMemoryLimiter()
	lim := Limit{Max: 50, Window: time.Minute}
	allowed := make(chan bool, 200)
	for i := 0; i < 200; i++ {
		go func() { r, _ := l.Allow(context.Background(), "k", lim); allowed <- r.Allowed }()
	}
	n := 0
	for i := 0; i < 200; i++ {
		if <-allowed {
			n++
		}
	}
	if n != 50 {
		t.Fatalf("allowed %d, want exactly 50", n)
	}
}

func TestClientIP(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	req := func(remote, xff string) *http.Request {
		r, _ := http.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}
	cases := []struct {
		name, remote, xff string
		trusted           []netip.Prefix
		want              string
	}{
		{"direct, no proxies", "203.0.113.5:1234", "", nil, "203.0.113.5"},
		{"spoofed XFF ignored when peer untrusted", "203.0.113.5:1234", "1.2.3.4", proxies, "203.0.113.5"},
		{"trusted proxy uses XFF", "10.0.0.2:80", "198.51.100.7", proxies, "198.51.100.7"},
		{"rightmost untrusted hop", "10.0.0.2:80", "6.6.6.6, 198.51.100.7, 10.0.0.9", proxies, "198.51.100.7"},
		{"client-forged prefix ignored", "10.0.0.2:80", "1.1.1.1, 198.51.100.7", proxies, "198.51.100.7"},
		{"garbage XFF falls back to peer", "10.0.0.2:80", "not-an-ip", proxies, "10.0.0.2"},
		{"ipv6", "[2001:db8::1]:443", "", nil, "2001:db8::1"},
		{"ipv4-mapped", "[::ffff:203.0.113.5]:443", "", nil, "203.0.113.5"},
	}
	for _, c := range cases {
		if got := ClientIP(req(c.remote, c.xff), c.trusted); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}
