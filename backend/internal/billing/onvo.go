// Package billing charges the plans of the catalog (internal/policy/plans.go) with ONVO Pay (Costa Rica).
//
// Nothing here trusts what the browser or a webhook says about a payment: before a plan changes, the subscription is
// read from ONVO and only an active one counts. docs/architecture/billing-onvo.md.
package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Onvo is a minimal client of the ONVO Pay API (https://docs.onvopay.com): JSON bodies, Bearer secret key.
type Onvo struct {
	Key  string // onvo_test_secret_key_... / onvo_live_secret_key_...
	Base string // default https://api.onvopay.com (tests point it at a fake)
	HTTP *http.Client
}

// APIError is an error answer of ONVO.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("onvo %d: %s", e.Status, e.Message) }

// NotFound reports whether ONVO answered 404 (or 405: the method does not exist for that resource).
func NotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && (e.Status == http.StatusNotFound || e.Status == http.StatusMethodNotAllowed)
}

func (c *Onvo) base() string {
	if c.Base != "" {
		return strings.TrimRight(c.Base, "/")
	}
	return "https://api.onvopay.com"
}

func (c *Onvo) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (c *Onvo) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		var e struct {
			Message any `json:"message"`
			Error   any `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := fmt.Sprint(e.Message)
		if e.Message == nil {
			msg = fmt.Sprint(e.Error)
		}
		return &APIError{Status: res.StatusCode, Message: msg}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// CreateCustomer creates an ONVO customer and returns its id.
func (c *Onvo) CreateCustomer(ctx context.Context, name, email string) (string, error) {
	in := map[string]any{"name": name}
	if email != "" {
		in["email"] = email
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/customers", in, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("onvo: customer without id")
	}
	return out.ID, nil
}

// Subscription is an ONVO subscription (the fields the plans use). Statuses: active, past_due, canceled, unpaid,
// incomplete, incomplete_expired, trialing.
type Subscription struct {
	ID                string
	Status            string
	Customer          string
	Metadata          map[string]string
	PriceIDs          []string
	CancelAtPeriodEnd bool
	PeriodEnd         *time.Time
}

// UnmarshalJSON reads both shapes the API documents for the customer (customerId, or a customer object or id) and
// for the items (priceId, or a price object).
func (s *Subscription) UnmarshalJSON(b []byte) error {
	var raw struct {
		ID                string            `json:"id"`
		Status            string            `json:"status"`
		CustomerID        string            `json:"customerId"`
		Customer          json.RawMessage   `json:"customer"`
		Metadata          map[string]any    `json:"metadata"`
		CancelAtPeriodEnd bool              `json:"cancelAtPeriodEnd"`
		CurrentPeriodEnd  *time.Time        `json:"currentPeriodEnd"`
		PeriodEnd         *time.Time        `json:"periodEnd"`
		Items             []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	s.ID, s.Status, s.Customer, s.CancelAtPeriodEnd = raw.ID, raw.Status, raw.CustomerID, raw.CancelAtPeriodEnd
	if s.Customer == "" && len(raw.Customer) > 0 {
		var id string
		if json.Unmarshal(raw.Customer, &id) != nil {
			var obj struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(raw.Customer, &obj)
			id = obj.ID
		}
		s.Customer = id
	}
	s.PeriodEnd = raw.CurrentPeriodEnd
	if s.PeriodEnd == nil {
		s.PeriodEnd = raw.PeriodEnd
	}
	s.Metadata = map[string]string{}
	for k, v := range raw.Metadata {
		if str, ok := v.(string); ok {
			s.Metadata[k] = str
		}
	}
	for _, it := range raw.Items {
		var item struct {
			PriceID string `json:"priceId"`
			Price   struct {
				ID string `json:"id"`
			} `json:"price"`
		}
		if json.Unmarshal(it, &item) == nil {
			if item.PriceID != "" {
				s.PriceIDs = append(s.PriceIDs, item.PriceID)
			} else if item.Price.ID != "" {
				s.PriceIDs = append(s.PriceIDs, item.Price.ID)
			}
		}
	}
	return nil
}

// CreateSubscription creates a subscription that waits for its first payment (paymentBehavior allow_incomplete):
// the browser pays it with ONVO's SDK (paymentType "subscription").
func (c *Onvo) CreateSubscription(ctx context.Context, customer, priceID string, metadata map[string]string) (Subscription, error) {
	in := map[string]any{
		"customerId":      customer,
		"items":           []map[string]any{{"priceId": priceID, "quantity": 1}},
		"paymentBehavior": "allow_incomplete",
		"metadata":        metadata,
	}
	var out Subscription
	err := c.do(ctx, http.MethodPost, "/v1/subscriptions", in, &out)
	if err == nil && out.ID == "" {
		err = errors.New("onvo: subscription without id")
	}
	return out, err
}

// GetSubscription reads a subscription as ONVO has it now.
func (c *Onvo) GetSubscription(ctx context.Context, id string) (Subscription, error) {
	var out Subscription
	err := c.do(ctx, http.MethodGet, "/v1/subscriptions/"+url.PathEscape(id), nil, &out)
	return out, err
}

// SetCancelAtPeriodEnd asks ONVO to end the subscription when the paid period ends (true), or not to (false).
func (c *Onvo) SetCancelAtPeriodEnd(ctx context.Context, id string, cancel bool) (Subscription, error) {
	var out Subscription
	err := c.do(ctx, http.MethodPost, "/v1/subscriptions/"+url.PathEscape(id), map[string]any{"cancelAtPeriodEnd": cancel}, &out)
	return out, err
}

// CancelNow ends a subscription at once (no refund: a plan change moves to the new plan immediately). DELETE when
// the API has it, otherwise cancelAt = now.
func (c *Onvo) CancelNow(ctx context.Context, id string) error {
	err := c.do(ctx, http.MethodDelete, "/v1/subscriptions/"+url.PathEscape(id), nil, nil)
	if err == nil || !NotFound(err) {
		return err
	}
	return c.do(ctx, http.MethodPost, "/v1/subscriptions/"+url.PathEscape(id),
		map[string]any{"cancelAt": time.Now().UTC().Format(time.RFC3339)}, nil)
}
