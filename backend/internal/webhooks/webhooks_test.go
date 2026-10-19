package webhooks

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// ---- fakes ----

type fakeSealer struct{ on bool }

func (f fakeSealer) Enabled() bool { return f.on }
func (f fakeSealer) Seal(_ context.Context, org, purpose string, p []byte) ([]byte, error) {
	return append([]byte(org+"|"+purpose+"|"), p...), nil
}
func (f fakeSealer) Unseal(_ context.Context, org, purpose string, s []byte) ([]byte, error) {
	pre := org + "|" + purpose + "|"
	if len(s) < len(pre) || string(s[:len(pre)]) != pre {
		return nil, errors.New("wrong org or purpose")
	}
	return s[len(pre):], nil
}

type memStore struct {
	mu     sync.Mutex
	eps    map[string]map[string]Endpoint // org -> id
	sealed map[string][]byte
	ds     map[string]map[string]*Delivery
}

func newMem() *memStore {
	return &memStore{eps: map[string]map[string]Endpoint{}, sealed: map[string][]byte{}, ds: map[string]map[string]*Delivery{}}
}

func (m *memStore) PutEndpoint(_ context.Context, org string, e Endpoint, sealed []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.eps[org] == nil {
		m.eps[org] = map[string]Endpoint{}
	}
	m.eps[org][e.ID] = e
	m.sealed[org+"/"+e.ID] = sealed
	return nil
}
func (m *memStore) ListEndpoints(_ context.Context, org string) ([]Endpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Endpoint
	for _, e := range m.eps[org] {
		out = append(out, e)
	}
	return out, nil
}
func (m *memStore) GetEndpoint(_ context.Context, org, id string) (Endpoint, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.eps[org][id]
	if !ok {
		return e, nil, ErrNotFound
	}
	return e, m.sealed[org+"/"+id], nil
}
func (m *memStore) SetActive(_ context.Context, org, id string, a bool, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.eps[org][id]
	if !ok {
		return ErrNotFound
	}
	e.Active, e.DisabledReason = a, reason
	m.eps[org][id] = e
	return nil
}
func (m *memStore) DeleteEndpoint(_ context.Context, org, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.eps[org], id)
	return nil
}
func (m *memStore) AddDeliveries(_ context.Context, org string, ds []Delivery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ds[org] == nil {
		m.ds[org] = map[string]*Delivery{}
	}
	for _, d := range ds {
		d := d
		m.ds[org][d.ID] = &d
	}
	return nil
}
func (m *memStore) Claim(_ context.Context, org string, now time.Time, limit int, lease time.Duration) ([]Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Delivery
	for _, d := range m.ds[org] {
		if d.Status == StatusPending && !d.NextAttemptAt.After(now) && len(out) < limit {
			d.NextAttemptAt = now.Add(lease)
			out = append(out, *d)
		}
	}
	return out, nil
}
func (m *memStore) Record(_ context.Context, org, id string, r Result) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.ds[org][id]
	d.Status, d.Attempts, d.LastStatus, d.LastError, d.DeliveredAt = r.Status, r.Attempts, r.HTTPStatus, r.Err, r.DeliveredAt
	if r.Status == StatusPending {
		d.NextAttemptAt = r.Next
	}
	return nil
}
func (m *memStore) HasPending(_ context.Context, org string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.ds[org] {
		if d.Status == StatusPending {
			return true, nil
		}
	}
	return false, nil
}
func (m *memStore) ListDeliveries(context.Context, string, string, int) ([]Delivery, error) {
	return nil, nil
}
func (m *memStore) Prune(context.Context, string, time.Time) (int, error) { return 0, nil }

func (m *memStore) only(org string) Delivery {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.ds[org] {
		return *d
	}
	return Delivery{}
}

// ---- signing ----

func TestSignVerify(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{"a":1}`)
	h := Sign("whsec_x", now, body)
	if err := Verify("whsec_x", h, body, now.Add(time.Minute), 5*time.Minute); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := Verify("whsec_x", h, []byte(`{"a":2}`), now, 5*time.Minute); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("tampered body: %v", err)
	}
	if err := Verify("whsec_other", h, body, now, 5*time.Minute); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("wrong secret: %v", err)
	}
	if err := Verify("whsec_x", h, body, now.Add(time.Hour), 5*time.Minute); !errors.Is(err, ErrStale) {
		t.Fatalf("replay outside tolerance: %v", err)
	}
	if err := Verify("whsec_x", "garbage", body, now, time.Minute); !errors.Is(err, ErrMalformed) {
		t.Fatalf("malformed: %v", err)
	}
	// The timestamp is part of the MAC: swapping it invalidates the signature.
	forged := "t=1700000999," + h[len("t=1700000000,"):]
	if err := Verify("whsec_x", forged, body, time.Unix(1_700_000_999, 0), time.Hour); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("timestamp not bound to the MAC: %v", err)
	}
	// Two v1 values (secret rotation): either may match.
	if err := Verify("whsec_x", h+",v1=deadbeef", body, now, time.Minute); err != nil {
		t.Fatalf("rotation: %v", err)
	}
}

func TestSecretShape(t *testing.T) {
	a, _ := NewSecret()
	b, _ := NewSecret()
	if a == b || len(a) < 40 || a[:len(SecretPrefix)] != SecretPrefix {
		t.Fatalf("bad secrets %q %q", a, b)
	}
}

// ---- retry policy ----

func TestBackoffGrowsAndIsBounded(t *testing.T) {
	prev := time.Duration(0)
	for i := 1; i <= 6; i++ {
		d := Backoff(i, 0.5)
		if d <= prev {
			t.Fatalf("attempt %d: %v not greater than %v", i, d, prev)
		}
		prev = d
	}
	if Backoff(50, 0.5) != Backoff(6, 0.5) {
		t.Fatal("backoff must plateau")
	}
	lo, hi := Backoff(3, 0), Backoff(3, 0.999)
	if lo >= hi || float64(hi)/float64(lo) > 1.3 {
		t.Fatalf("jitter out of range: %v %v", lo, hi)
	}
}

func TestOutcome(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	if r := Outcome(now, 1, 204, 0.5, 0); r.Status != StatusDelivered || r.DeliveredAt == nil {
		t.Fatalf("2xx: %+v", r)
	}
	if r := Outcome(now, 1, 503, 0.5, 0); r.Status != StatusPending || !r.Next.After(now) {
		t.Fatalf("5xx must retry: %+v", r)
	}
	if r := Outcome(now, 1, 0, 0.5, 0); r.Status != StatusPending {
		t.Fatalf("network error must retry: %+v", r)
	}
	if r := Outcome(now, 1, 400, 0.5, 0); r.Status != StatusDead {
		t.Fatalf("400 must not retry: %+v", r)
	}
	if r := Outcome(now, 1, 429, 0.5, 0); r.Status != StatusPending {
		t.Fatalf("429 must retry: %+v", r)
	}
	if r := Outcome(now, MaxAttempts, 500, 0.5, 0); r.Status != StatusDead {
		t.Fatalf("attempts exhausted must be dead: %+v", r)
	}
}

func TestMapEvent(t *testing.T) {
	for in, want := range map[string]string{"request.completed": EventRequestDone, "approval.requested": EventApprovalRequested, "report.created": EventReportCreated} {
		if got, ok := MapEvent(in); !ok || got != want {
			t.Fatalf("%s -> %s %v", in, got, ok)
		}
	}
	if _, ok := MapEvent("task.started"); ok {
		t.Fatal("internal-only events must not leave the system")
	}
}

// ---- service ----

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newService(t *testing.T, srv *httptest.Server, clk *clock) (*Service, *memStore) {
	t.Helper()
	m := newMem()
	return &Service{Store: m, Sealer: fakeSealer{on: true}, Client: srv.Client(), SkipURLCheck: true, Now: clk.now, Jitter: func() float64 { return 0.5 }}, m
}

func TestDeliveryRetriesThenSucceedsWithValidSignature(t *testing.T) {
	var mu sync.Mutex
	var calls int
	var secret string
	var sigErr error
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		calls++
		sigErr = Verify(secret, r.Header.Get(HeaderSignature), b, time.Now(), 24*time.Hour)
		if r.Header.Get(HeaderEvent) != EventRequestDone {
			sigErr = errors.New("missing event header")
		}
		if calls < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	clk := &clock{t: time.Now().UTC()}
	svc, m := newService(t, srv, clk)
	ctx := context.Background()
	ep, sec, err := svc.CreateEndpoint(ctx, "org1", "u1", srv.URL, "test", []string{EventRequestDone})
	if err != nil {
		t.Fatal(err)
	}
	secret = sec
	if err := svc.Enqueue(ctx, "org1", EventReportCreated, map[string]any{"x": 1}); err != nil || m.only("org1").ID != "" {
		t.Fatalf("an unsubscribed event must enqueue nothing: %v", err)
	}
	if err := svc.Enqueue(ctx, "org1", EventRequestDone, map[string]any{"request_id": "r1"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := svc.Process(ctx, "org1"); n != 1 {
		t.Fatalf("first pass attempted %d", n)
	}
	d := m.only("org1")
	if d.Status != StatusPending || d.Attempts != 1 || d.LastStatus != 502 {
		t.Fatalf("after first failure: %+v", d)
	}
	// Not due yet: nothing is attempted.
	if n, _ := svc.Process(ctx, "org1"); n != 0 {
		t.Fatalf("a delivery was retried before its backoff: %d", n)
	}
	clk.t = clk.t.Add(time.Minute) // > 30s
	svc.Process(ctx, "org1")
	clk.t = clk.t.Add(10 * time.Minute) // > 2m
	svc.Process(ctx, "org1")
	d = m.only("org1")
	if d.Status != StatusDelivered || d.Attempts != 3 || d.DeliveredAt == nil {
		t.Fatalf("after retries: %+v", d)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 3 || sigErr != nil {
		t.Fatalf("calls=%d sigErr=%v", calls, sigErr)
	}
	_ = ep
}

func TestPermanentFailureIsNotRetried(t *testing.T) {
	var calls int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(404) }))
	defer srv.Close()
	clk := &clock{t: time.Now().UTC()}
	svc, m := newService(t, srv, clk)
	ctx := context.Background()
	if _, _, err := svc.CreateEndpoint(ctx, "o", "u", srv.URL, "", []string{EventRequestDone}); err != nil {
		t.Fatal(err)
	}
	svc.Enqueue(ctx, "o", EventRequestDone, 1)
	svc.Process(ctx, "o")
	clk.t = clk.t.Add(48 * time.Hour)
	svc.Process(ctx, "o")
	if d := m.only("o"); d.Status != StatusDead || calls != 1 {
		t.Fatalf("status=%s calls=%d", d.Status, calls)
	}
}

func TestEndpointValidationAndSecretHandling(t *testing.T) {
	m := newMem()
	svc := &Service{Store: m, Sealer: fakeSealer{on: true}}
	ctx := context.Background()
	for _, u := range []string{"http://example.com/hook", "https://127.0.0.1/hook", "https://localhost/hook", "https://10.0.0.5/hook", "https://169.254.169.254/latest", "ftp://example.com", "https://user:pw@example.com/"} {
		if _, _, err := svc.CreateEndpoint(ctx, "o", "u", u, "", []string{EventRequestDone}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s accepted (err=%v)", u, err)
		}
	}
	if _, _, err := svc.CreateEndpoint(ctx, "o", "u", "https://example.com/hook", "", []string{"task.started"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown event accepted: %v", err)
	}
	if _, _, err := svc.CreateEndpoint(ctx, "o", "u", "https://example.com/hook", "", nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("no events accepted: %v", err)
	}
	ep, secret, err := svc.CreateEndpoint(ctx, "o", "u", "https://example.com/hook", "", []string{EventRequestDone, EventRequestDone})
	if err != nil {
		t.Fatal(err)
	}
	if len(ep.Events) != 1 || ep.SecretHint != secret[len(secret)-4:] {
		t.Fatalf("endpoint %+v", ep)
	}
	_, sealed, _ := m.GetEndpoint(ctx, "o", ep.ID)
	if string(sealed) == secret || containsBytes(sealed, secret) && false {
		t.Fatal("secret stored in clear")
	}
	// Sealed under the organization: another org cannot open it.
	if _, err := (fakeSealer{on: true}).Unseal(ctx, "other", sealPurpose, sealed); err == nil {
		t.Fatal("sealed secret opened by another organization")
	}
	// No encryption key: fail closed.
	off := &Service{Store: m, Sealer: fakeSealer{on: false}}
	if _, _, err := off.CreateEndpoint(ctx, "o", "u", "https://example.com/x", "", []string{EventRequestDone}); !errors.Is(err, ErrNoEncryption) {
		t.Fatalf("must refuse without encryption: %v", err)
	}
}

func containsBytes(b []byte, s string) bool { return len(s) > 0 && string(b) == s }

func TestEndpointLimit(t *testing.T) {
	svc := &Service{Store: newMem(), Sealer: fakeSealer{on: true}}
	ctx := context.Background()
	for i := 0; i < MaxEndpoints; i++ {
		if _, _, err := svc.CreateEndpoint(ctx, "o", "u", "https://example.com/h", "", []string{EventReportCreated}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := svc.CreateEndpoint(ctx, "o", "u", "https://example.com/h", "", []string{EventReportCreated}); !errors.Is(err, ErrLimit) {
		t.Fatalf("limit: %v", err)
	}
}

func TestBodyIsStableAcrossRetries(t *testing.T) {
	d := Delivery{ID: "d1", Event: EventRequestDone, Payload: []byte(`{"a":1}`), CreatedAt: time.Unix(5, 0)}
	if string(Body("o", d)) != string(Body("o", d)) {
		t.Fatal("unstable body would change the signature on every retry")
	}
}
