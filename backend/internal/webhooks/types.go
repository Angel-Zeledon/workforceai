// Package webhooks delivers organization events to customer endpoints: HMAC-signed JSON, an outbox table with
// retries and exponential backoff, SSRF-safe delivery (the same network guard as the agents' web tool) and a
// signing secret that is sealed at rest and shown once. See docs/architecture/governance.md.
package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Event names a customer can subscribe to.
const (
	EventRequestDone       = "request.done"
	EventApprovalRequested = "approval.requested"
	EventReportCreated     = "report.created"
	// EventPing is only sent by "send test event"; it cannot be subscribed to.
	EventPing = "webhook.ping"
)

// AllEvents are the subscribable events.
var AllEvents = []string{EventRequestDone, EventApprovalRequested, EventReportCreated}

// MapEvent translates an internal event type (WebSocket contract) to the public event name.
func MapEvent(internal string) (string, bool) {
	switch internal {
	case "request.completed":
		return EventRequestDone, true
	case "approval.requested":
		return EventApprovalRequested, true
	case "report.created":
		return EventReportCreated, true
	}
	return "", false
}

// Delivery states.
const (
	StatusPending   = "pending"
	StatusDelivered = "delivered"
	StatusDead      = "dead"
)

// Limits.
const (
	MaxEndpoints     = 10
	MaxAttempts      = 7
	FailureStreakOff = 50 // consecutive failed deliveries after which an endpoint is switched off
)

var (
	ErrNotFound     = errors.New("webhooks: not found")
	ErrInvalid      = errors.New("webhooks: invalid")
	ErrNoEncryption = errors.New("webhooks: no encryption key configured (set CONNECTIONS_KEK): refusing to store a signing secret")
	ErrLimit        = errors.New("webhooks: too many endpoints")
)

// Endpoint is a customer URL subscribed to some events.
type Endpoint struct {
	ID             string    `json:"id"`
	URL            string    `json:"url"`
	Description    string    `json:"description"`
	Events         []string  `json:"events"`
	Active         bool      `json:"active"`
	DisabledReason string    `json:"disabled_reason,omitempty"`
	SecretHint     string    `json:"secret_hint"` // last 4 characters, to tell secrets apart
	FailureStreak  int       `json:"failure_streak"`
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
}

// Subscribed reports whether e receives event.
func (e Endpoint) Subscribed(event string) bool {
	if !e.Active {
		return false
	}
	for _, x := range e.Events {
		if x == event {
			return true
		}
	}
	return false
}

// Delivery is one outbox row.
type Delivery struct {
	ID            string          `json:"id"`
	EndpointID    string          `json:"endpoint_id"`
	Event         string          `json:"event"`
	Payload       json.RawMessage `json:"-"`
	Status        string          `json:"status"`
	Attempts      int             `json:"attempts"`
	NextAttemptAt time.Time       `json:"next_attempt_at"`
	LastStatus    int             `json:"last_status"`
	LastError     string          `json:"last_error"`
	CreatedAt     time.Time       `json:"created_at"`
	DeliveredAt   *time.Time      `json:"delivered_at,omitempty"`
}

// Result is what one attempt changed.
type Result struct {
	Status      string // StatusPending (retry later), StatusDelivered or StatusDead
	Attempts    int
	Next        time.Time
	HTTPStatus  int
	Err         string
	DeliveredAt *time.Time
}

// Store is the persistence of endpoints and the outbox. Every method is scoped to one organization (RLS).
type Store interface {
	PutEndpoint(ctx context.Context, org string, e Endpoint, sealedSecret []byte) error
	ListEndpoints(ctx context.Context, org string) ([]Endpoint, error)
	// GetEndpoint returns the endpoint and its sealed secret.
	GetEndpoint(ctx context.Context, org, id string) (Endpoint, []byte, error)
	SetActive(ctx context.Context, org, id string, active bool, reason string) error
	DeleteEndpoint(ctx context.Context, org, id string) error

	AddDeliveries(ctx context.Context, org string, ds []Delivery) error
	// Claim returns up to limit pending deliveries that are due and leases them (their next attempt moves
	// forward by lease), so concurrent replicas never deliver the same row twice.
	Claim(ctx context.Context, org string, now time.Time, limit int, lease time.Duration) ([]Delivery, error)
	// Record stores the outcome of an attempt and updates the failure streak of the endpoint.
	Record(ctx context.Context, org, deliveryID string, r Result) error
	HasPending(ctx context.Context, org string) (bool, error)
	ListDeliveries(ctx context.Context, org, endpointID string, limit int) ([]Delivery, error)
	// Prune removes delivered and dead rows older than before.
	Prune(ctx context.Context, org string, before time.Time) (int, error)
}
