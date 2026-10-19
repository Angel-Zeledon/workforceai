package webhooks

import (
	"net/http"
	"time"
)

// schedule[i] is the wait after the (i+1)-th failed attempt.
var schedule = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}

// Backoff returns the wait before the next attempt after `attempt` failed attempts (1-based). jitter in [0,1)
// spreads retries by up to +-10% so a recovering endpoint is not hit by every queued delivery at once.
func Backoff(attempt int, jitter float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	i := attempt - 1
	if i >= len(schedule) {
		i = len(schedule) - 1
	}
	base := schedule[i]
	f := 0.9 + 0.2*jitter
	return time.Duration(float64(base) * f)
}

// Retryable says whether a response status deserves another attempt. Network errors (status 0), 408, 425, 429
// and 5xx are transient; any other 4xx means the receiver rejected the delivery on purpose.
func Retryable(status int) bool {
	switch {
	case status == 0:
		return true
	case status == http.StatusRequestTimeout, status == http.StatusTooEarly, status == http.StatusTooManyRequests:
		return true
	case status >= 500:
		return true
	}
	return false
}

// Succeeded reports a 2xx answer.
func Succeeded(status int) bool { return status >= 200 && status < 300 }

// Outcome decides what happens to a delivery after an attempt (attempts already includes it).
func Outcome(now time.Time, attempts, status int, jitter float64, maxAttempts int) Result {
	if maxAttempts <= 0 {
		maxAttempts = MaxAttempts
	}
	r := Result{Attempts: attempts, HTTPStatus: status}
	switch {
	case Succeeded(status):
		t := now
		r.Status, r.DeliveredAt = StatusDelivered, &t
	case !Retryable(status) || attempts >= maxAttempts:
		r.Status = StatusDead
	default:
		r.Status, r.Next = StatusPending, now.Add(Backoff(attempts, jitter))
	}
	return r
}
