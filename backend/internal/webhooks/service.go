package webhooks

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/webtool"
)

// Sealer encrypts under the organization's data key (internal/vault.Vault implements it).
type Sealer interface {
	Enabled() bool
	Seal(ctx context.Context, org, purpose string, plain []byte) ([]byte, error)
	Unseal(ctx context.Context, org, purpose string, sealed []byte) ([]byte, error)
}

const sealPurpose = "webhook-secret"

// OrgLister lists the organizations (for the periodic sweep that finds pending deliveries after a restart).
type OrgLister interface {
	ListOrgIDs(ctx context.Context) ([]string, error)
}

// Service owns endpoints and delivery.
type Service struct {
	Store  Store
	Sealer Sealer
	// Guard validates URLs and pins the connection to validated public addresses (SSRF). The zero value
	// allows 80 and 443; the service narrows that to https on 443 and 8443.
	Guard webtool.NetGuard
	// Client replaces the SSRF-safe HTTP client (tests only). When set, URLs are still validated unless
	// SkipURLCheck is true.
	Client       *http.Client
	SkipURLCheck bool
	Now          func() time.Time
	Jitter       func() float64
	MaxAttempts  int
	Timeout      time.Duration
	Log          *slog.Logger

	queue chan queued
	hot   sync.Map // org -> struct{}: organizations with pending deliveries
	once  sync.Once
	http  *http.Client
}

type queued struct {
	org, event string
	data       json.RawMessage
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) jitter() float64 {
	if s.Jitter != nil {
		return s.Jitter()
	}
	return rand.Float64()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) guard() webtool.NetGuard {
	g := s.Guard
	if len(g.Ports) == 0 {
		g.Ports = []int{443, 8443}
	}
	return g
}

// checkURL requires https on a public host.
func (s *Service) checkURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		if s.SkipURLCheck && err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			return raw, nil
		}
		return "", fmt.Errorf("%w: the URL must be https", ErrInvalid)
	}
	if s.SkipURLCheck {
		return raw, nil
	}
	if _, _, err := s.guard().CheckURL(raw); err != nil {
		return "", fmt.Errorf("%w: URL rejected (%v)", ErrInvalid, err)
	}
	return raw, nil
}

func validEvents(ev []string) ([]string, error) {
	if len(ev) == 0 {
		return nil, fmt.Errorf("%w: choose at least one event", ErrInvalid)
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range ev {
		ok := false
		for _, a := range AllEvents {
			ok = ok || a == e
		}
		if !ok {
			return nil, fmt.Errorf("%w: unknown event %q", ErrInvalid, e)
		}
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out, nil
}

// CreateEndpoint registers a URL. The signing secret is returned once and stored sealed.
func (s *Service) CreateEndpoint(ctx context.Context, org, actor, rawURL, description string, events []string) (Endpoint, string, error) {
	if s.Sealer == nil || !s.Sealer.Enabled() {
		return Endpoint{}, "", ErrNoEncryption
	}
	u, err := s.checkURL(rawURL)
	if err != nil {
		return Endpoint{}, "", err
	}
	evs, err := validEvents(events)
	if err != nil {
		return Endpoint{}, "", err
	}
	existing, err := s.Store.ListEndpoints(ctx, org)
	if err != nil {
		return Endpoint{}, "", err
	}
	if len(existing) >= MaxEndpoints {
		return Endpoint{}, "", ErrLimit
	}
	secret, err := NewSecret()
	if err != nil {
		return Endpoint{}, "", err
	}
	sealed, err := s.Sealer.Seal(ctx, org, sealPurpose, []byte(secret))
	if err != nil {
		return Endpoint{}, "", err
	}
	if len(description) > 200 {
		description = description[:200]
	}
	e := Endpoint{ID: uuid.NewString(), URL: u, Description: strings.TrimSpace(description), Events: evs, Active: true,
		SecretHint: secret[len(secret)-4:], CreatedBy: actor, CreatedAt: s.now()}
	if err := s.Store.PutEndpoint(ctx, org, e, sealed); err != nil {
		return Endpoint{}, "", err
	}
	return e, secret, nil
}

// Enqueue writes one delivery per active endpoint subscribed to event. It never blocks on the network.
func (s *Service) Enqueue(ctx context.Context, org, event string, data any) error {
	eps, err := s.Store.ListEndpoints(ctx, org)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if len(raw) > 256<<10 {
		raw, _ = json.Marshal(map[string]any{"truncated": true})
	}
	now := s.now()
	var ds []Delivery
	for _, e := range eps {
		if e.Subscribed(event) {
			ds = append(ds, Delivery{ID: uuid.NewString(), EndpointID: e.ID, Event: event, Payload: raw, Status: StatusPending, NextAttemptAt: now, CreatedAt: now})
		}
	}
	if len(ds) == 0 {
		return nil
	}
	if err := s.Store.AddDeliveries(ctx, org, ds); err != nil {
		return err
	}
	s.hot.Store(org, struct{}{})
	return nil
}

// EnqueueAsync hands an event to the background worker (used from the event publisher, a hot path). A full
// queue drops the event with a warning: webhooks are best effort relative to the work itself.
func (s *Service) EnqueueAsync(org, event string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	s.once.Do(func() { s.queue = make(chan queued, 1024) })
	select {
	case s.queue <- queued{org, event, raw}:
	default:
		s.log().Warn("webhooks: queue full, event dropped", "event", event)
	}
}

// envelope is the JSON body every receiver gets.
type envelope struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	CreatedAt string          `json:"created_at"`
	OrgID     string          `json:"org_id"`
	Data      json.RawMessage `json:"data"`
}

// Body renders the exact bytes that are signed and sent (stable across retries).
func Body(org string, d Delivery) []byte {
	b, _ := json.Marshal(envelope{ID: d.ID, Type: d.Event, CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339), OrgID: org, Data: d.Payload})
	return b
}

func (s *Service) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	if s.http == nil {
		g := s.guard()
		d := &net.Dialer{Timeout: 5 * time.Second, Control: g.ControlFunc()}
		tr := &http.Transport{
			Proxy:                 nil, // an environment proxy would bypass the guard
			DisableKeepAlives:     true,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout:   8 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, webtool.ErrBadURL
				}
				addrs, err := g.Resolve(ctx, host)
				if err != nil {
					return nil, err
				}
				var last error = webtool.ErrBlockedAddr
				for _, a := range addrs {
					c, err := d.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
					if err == nil {
						return c, nil
					}
					last = err
				}
				return nil, last
			},
		}
		s.http = &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return s.http
}

// attempt performs one HTTP delivery and returns the HTTP status (0 = network error) and a short error.
func (s *Service) attempt(ctx context.Context, org string, d Delivery) (int, string) {
	ep, sealed, err := s.Store.GetEndpoint(ctx, org, d.EndpointID)
	if err != nil || !ep.Active {
		return 410, "endpoint_gone" // not retryable: dead
	}
	if s.Sealer == nil {
		return 0, "secret_unavailable"
	}
	secret, err := s.Sealer.Unseal(ctx, org, sealPurpose, sealed)
	if err != nil {
		return 0, "secret_unavailable"
	}
	if _, err := s.checkURL(ep.URL); err != nil {
		return 400, "url_not_allowed" // the URL (or its DNS) is no longer acceptable: not retryable
	}
	body := Body(org, d)
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, bytes.NewReader(body))
	if err != nil {
		return 400, "bad_request"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AIWorkforceOS-Webhooks/1")
	req.Header.Set(HeaderEvent, d.Event)
	req.Header.Set(HeaderDelivery, d.ID)
	req.Header.Set(HeaderSignature, Sign(string(secret), s.now(), body))
	resp, err := s.client().Do(req)
	if err != nil {
		return 0, shortErr(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if Succeeded(resp.StatusCode) {
		return resp.StatusCode, ""
	}
	return resp.StatusCode, fmt.Sprintf("http_%d", resp.StatusCode)
}

// shortErr never leaks internal addresses into the delivery log.
func shortErr(err error) string {
	switch {
	case err == nil:
		return ""
	case strings.Contains(err.Error(), "deadline exceeded"), strings.Contains(err.Error(), "timeout"):
		return "timeout"
	case strings.Contains(err.Error(), "address_not_allowed"), strings.Contains(err.Error(), "host_not_allowed"):
		return "address_not_allowed"
	}
	return "network_error"
}

// Process claims the due deliveries of org and delivers them (4 at a time). It returns how many were attempted.
func (s *Service) Process(ctx context.Context, org string) (int, error) {
	ds, err := s.Store.Claim(ctx, org, s.now(), 50, 2*time.Minute)
	if err != nil || len(ds) == 0 {
		return 0, err
	}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, d := range ds {
		sem <- struct{}{}
		wg.Add(1)
		go func(d Delivery) {
			defer func() { <-sem; wg.Done() }()
			s.deliver(ctx, org, d)
		}(d)
	}
	wg.Wait()
	return len(ds), nil
}

func (s *Service) deliver(ctx context.Context, org string, d Delivery) Result {
	status, errs := s.attempt(ctx, org, d)
	r := Outcome(s.now(), d.Attempts+1, status, s.jitter(), s.MaxAttempts)
	r.Err = errs
	if err := s.Store.Record(context.WithoutCancel(ctx), org, d.ID, r); err != nil {
		s.log().Warn("webhooks: record outcome", "err", err)
	}
	return r
}

// SendTest delivers a webhook.ping to one endpoint right away and reports the outcome.
func (s *Service) SendTest(ctx context.Context, org, endpointID string) (Delivery, error) {
	ep, _, err := s.Store.GetEndpoint(ctx, org, endpointID)
	if err != nil {
		return Delivery{}, err
	}
	now := s.now()
	d := Delivery{ID: uuid.NewString(), EndpointID: ep.ID, Event: EventPing, Payload: json.RawMessage(`{"ping":true}`), Status: StatusPending, NextAttemptAt: now.Add(time.Hour), CreatedAt: now}
	if err := s.Store.AddDeliveries(ctx, org, []Delivery{d}); err != nil {
		return d, err
	}
	// The row is parked an hour ahead so the worker does not race this direct attempt.
	r := s.deliver(ctx, org, d)
	d.Attempts, d.Status, d.LastStatus, d.LastError = r.Attempts, r.Status, r.HTTPStatus, r.Err
	return d, nil
}

// Run drains the async queue and delivers due rows until ctx ends. Every 2 minutes it also looks for
// organizations with pending deliveries left by a previous process.
func (s *Service) Run(ctx context.Context, orgs OrgLister) {
	s.once.Do(func() { s.queue = make(chan queued, 1024) })
	tick := time.NewTicker(5 * time.Second)
	sweep := time.NewTicker(2 * time.Minute)
	prune := time.NewTicker(6 * time.Hour)
	defer tick.Stop()
	defer sweep.Stop()
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case q := <-s.queue:
			if err := s.Enqueue(ctx, q.org, q.event, q.data); err != nil {
				s.log().Warn("webhooks: enqueue", "event", q.event, "err", err)
			}
		case <-tick.C:
			s.hot.Range(func(k, _ any) bool {
				org := k.(string)
				if _, err := s.Process(ctx, org); err != nil {
					s.log().Warn("webhooks: process", "org", org, "err", err)
				}
				if p, err := s.Store.HasPending(ctx, org); err == nil && !p {
					s.hot.Delete(org)
				}
				return true
			})
		case <-sweep.C:
			s.markPending(ctx, orgs)
		case <-prune.C:
			s.forEachOrg(ctx, orgs, func(org string) { _, _ = s.Store.Prune(ctx, org, s.now().AddDate(0, 0, -30)) })
		}
	}
}

func (s *Service) forEachOrg(ctx context.Context, orgs OrgLister, fn func(org string)) {
	if orgs == nil {
		return
	}
	ids, err := orgs.ListOrgIDs(ctx)
	if err != nil {
		return
	}
	for _, id := range ids {
		fn(id)
	}
}

func (s *Service) markPending(ctx context.Context, orgs OrgLister) {
	s.forEachOrg(ctx, orgs, func(org string) {
		if p, err := s.Store.HasPending(ctx, org); err == nil && p {
			s.hot.Store(org, struct{}{})
		}
	})
}

// Start launches Run in the background and a first sweep (deliveries left by a previous process).
func (s *Service) Start(ctx context.Context, orgs OrgLister) {
	go func() {
		s.once.Do(func() { s.queue = make(chan queued, 1024) })
		s.markPending(ctx, orgs)
		s.Run(ctx, orgs)
	}()
}
