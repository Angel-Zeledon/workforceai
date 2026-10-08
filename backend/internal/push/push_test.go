package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/hkdf"

	"aiworkforce/backend/internal/domain"
)

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := decodeB64(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// RFC 8291 section 5 example.
func TestEncryptMatchesRFC8291Example(t *testing.T) {
	as, err := ecdh.P256().NewPrivateKey(mustB64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := encryptWith([]byte("When I grow up, I want to be a watermelon"),
		mustB64(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"),
		mustB64(t, "BTBZMqHH6r4Tts7J_aSIgg"), as, mustB64(t, "DGv6ra1nlYgDCS1FRnbzlw"))
	if err != nil {
		t.Fatal(err)
	}
	want := "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	if b64.EncodeToString(got) != want {
		t.Fatalf("ciphertext differs from RFC 8291:\n got %s\nwant %s", b64.EncodeToString(got), want)
	}
}

// decrypt is the user agent side (test only).
func decrypt(t *testing.T, body []byte, ua *ecdh.PrivateKey, authSecret []byte) []byte {
	t.Helper()
	salt, rs, idlen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	if rs != recordSize {
		t.Fatalf("record size %d", rs)
	}
	asPub := body[21 : 21+idlen]
	pk, err := ecdh.P256().NewPublicKey(asPub)
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := ua.ECDH(pk)
	info := append(append([]byte("WebPush: info\x00"), ua.PublicKey().Bytes()...), asPub...)
	ikm := make([]byte, 32)
	io.ReadFull(hkdf.New(sha256.New, secret, authSecret, info), ikm)
	prk := hkdf.Extract(sha256.New, ikm, salt)
	cek, nonce := make([]byte, 16), make([]byte, 12)
	io.ReadFull(hkdf.Expand(sha256.New, prk, []byte("Content-Encoding: aes128gcm\x00")), cek)
	io.ReadFull(hkdf.Expand(sha256.New, prk, []byte("Content-Encoding: nonce\x00")), nonce)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	pt, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		t.Fatal(err)
	}
	if pt[len(pt)-1] != 0x02 {
		t.Fatal("missing last-record delimiter")
	}
	return pt[:len(pt)-1]
}

type captured struct {
	mu   sync.Mutex
	reqs []*http.Request
	body [][]byte
}

// fakePushService serves https for "push.test" and records the requests.
func fakePushService(t *testing.T, status int) (*captured, *http.Client) {
	c := &captured{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.reqs, c.body = append(c.reqs, r), append(c.body, b)
		c.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test server
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}
	return c, client
}

func newTestService(t *testing.T, client *http.Client, store Store) *Service {
	pub, priv, err := GenerateVAPID()
	if err != nil {
		t.Fatal(err)
	}
	v, err := ParseVAPID(pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	return New(Config{VAPID: v, Subject: "mailto:ops@example.com", AllowedHosts: []string{"push.test"}, Client: client}, store)
}

func browserKeys(t *testing.T) (*ecdh.PrivateKey, string, []byte, string) {
	ua, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 16)
	rand.Read(secret)
	return ua, b64.EncodeToString(ua.PublicKey().Bytes()), secret, b64.EncodeToString(secret)
}

func TestNotifySendsOnlyTitleAndRisk(t *testing.T) {
	cap, client := fakePushService(t, http.StatusCreated)
	store := NewMemoryStore()
	svc := newTestService(t, client, store)
	ua, p256, secret, auth := browserKeys(t)
	ctx := context.Background()
	if _, err := svc.Subscribe(ctx, Subscription{OrgID: "org-a", UserID: "u1", Endpoint: "https://push.test/sub/1", P256dh: p256, Auth: auth}); err != nil {
		t.Fatal(err)
	}
	ap := domain.Approval{ID: "ap-1", Title: "send proposal: Propuesta ACME", Risk: "HIGH", Action: "send_proposal",
		Details: "Herramienta email · args {\"to\":\"ceo@acme.com\",\"iban\":\"ES12SECRET\"}"}
	n := Notifier{Inner: nopPub{}, Svc: svc}
	if err := n.Publish(ctx, domain.Event{Type: domain.EvApprovalRequest, OrgID: "org-a", Payload: map[string]any{"approval": ap}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		cap.mu.Lock()
		got := len(cap.body)
		cap.mu.Unlock()
		if got == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("push not delivered (%d requests)", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	r := cap.reqs[0]
	if r.Header.Get("Content-Encoding") != "aes128gcm" || !strings.HasPrefix(r.Header.Get("Authorization"), "vapid t=") || r.Header.Get("TTL") == "" {
		t.Fatalf("headers: %v", r.Header)
	}
	if bytes.Contains(cap.body[0], []byte("acme")) || bytes.Contains(cap.body[0], []byte("send proposal")) {
		t.Fatal("payload is not encrypted")
	}
	plain := decrypt(t, cap.body[0], ua, secret)
	for _, leak := range []string{"ceo@acme.com", "ES12SECRET", "args", "Herramienta", "send_proposal"} {
		if bytes.Contains(plain, []byte(leak)) {
			t.Fatalf("payload leaks %q: %s", leak, plain)
		}
	}
	var m Message
	if err := json.Unmarshal(plain, &m); err != nil {
		t.Fatal(err)
	}
	if m.ApprovalID != "ap-1" || m.Risk != "high" || m.URL != "/approvals/ap-1" || m.Title != ap.Title || m.Type != "approval.requested" {
		t.Fatalf("message: %+v", m)
	}
}

type nopPub struct{}

func (nopPub) Publish(context.Context, domain.Event) error { return nil }

func TestOtherEventsAndOtherOrgsGetNothing(t *testing.T) {
	cap, client := fakePushService(t, http.StatusCreated)
	store := NewMemoryStore()
	svc := newTestService(t, client, store)
	_, p256, _, auth := browserKeys(t)
	ctx := context.Background()
	svc.Subscribe(ctx, Subscription{OrgID: "org-a", Endpoint: "https://push.test/a", P256dh: p256, Auth: auth})
	svc.Notify(ctx, "org-b", Message{ApprovalID: "x"})
	Notifier{Inner: nopPub{}, Svc: svc}.Publish(ctx, domain.Event{Type: domain.EvTaskStarted, OrgID: "org-a", Payload: map[string]any{"approval": domain.Approval{ID: "y"}}})
	time.Sleep(200 * time.Millisecond)
	if len(cap.body) != 0 {
		t.Fatalf("unexpected pushes: %d", len(cap.body))
	}
	if l, _ := store.ListPushSubscriptions(ctx, "org-b"); len(l) != 0 {
		t.Fatal("org-b sees org-a subscriptions")
	}
}

func TestGoneEndpointsAreRemoved(t *testing.T) {
	_, client := fakePushService(t, http.StatusGone)
	store := NewMemoryStore()
	svc := newTestService(t, client, store)
	_, p256, _, auth := browserKeys(t)
	ctx := context.Background()
	svc.Subscribe(ctx, Subscription{OrgID: "o", Endpoint: "https://push.test/gone", P256dh: p256, Auth: auth})
	svc.Notify(ctx, "o", Message{ApprovalID: "x"})
	if l, _ := store.ListPushSubscriptions(ctx, "o"); len(l) != 0 {
		t.Fatal("gone subscription kept")
	}
}

func TestSubscribeRejectsUnsafeInput(t *testing.T) {
	svc := newTestService(t, nil, NewMemoryStore())
	_, p256, _, auth := browserKeys(t)
	for _, ep := range []string{"http://push.test/x", "https://127.0.0.1/x", "https://evil.example/x", "https://push.test:8443/x",
		"https://user@push.test/x", "https://push.test.evil.example/x", "not a url"} {
		if _, err := svc.Subscribe(context.Background(), Subscription{OrgID: "o", Endpoint: ep, P256dh: p256, Auth: auth}); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("endpoint %q accepted (%v)", ep, err)
		}
	}
	if _, err := svc.Subscribe(context.Background(), Subscription{OrgID: "o", Endpoint: "https://push.test/x", P256dh: "short", Auth: auth}); !errors.Is(err, domain.ErrInvalid) {
		t.Error("bad p256dh accepted")
	}
	d := New(Config{VAPID: svc.cfg.VAPID}, NewMemoryStore())
	if d.ValidateEndpoint("https://fcm.googleapis.com/fcm/send/abc") != nil || d.ValidateEndpoint("https://wns2-db5p.notify.windows.com/w/?token=x") != nil {
		t.Error("default allow-list rejects a real push service")
	}
}

func TestDeleteOnlyOwnSubscription(t *testing.T) {
	store := NewMemoryStore()
	svc := newTestService(t, nil, store)
	_, p256, _, auth := browserKeys(t)
	ctx := context.Background()
	svc.Subscribe(ctx, Subscription{OrgID: "o", UserID: "alice", Endpoint: "https://push.test/a", P256dh: p256, Auth: auth})
	svc.Unsubscribe(ctx, "o", "mallory", "https://push.test/a")
	if l, _ := store.ListPushSubscriptions(ctx, "o"); len(l) != 1 {
		t.Fatal("another user deleted the subscription")
	}
	svc.Unsubscribe(ctx, "o", "alice", "https://push.test/a")
	if l, _ := store.ListPushSubscriptions(ctx, "o"); len(l) != 0 {
		t.Fatal("owner could not delete")
	}
}

func TestVAPIDKeysMustMatch(t *testing.T) {
	pub, priv, _ := GenerateVAPID()
	other, _, _ := GenerateVAPID()
	if _, err := ParseVAPID(other, priv); err == nil {
		t.Fatal("mismatched key pair accepted")
	}
	if v, err := ParseVAPID(pub, priv); err != nil || v.Public != pub {
		t.Fatalf("valid pair: %v", err)
	}
}

func TestFromApprovalTruncatesTitle(t *testing.T) {
	m := FromApproval(domain.Approval{ID: "a/b", Title: strings.Repeat("é", 120), Risk: "weird"})
	if len([]rune(m.Title)) != 80 || m.Risk != "medium" || m.URL != "/approvals/a%2Fb" {
		t.Fatalf("%+v", m)
	}
}
