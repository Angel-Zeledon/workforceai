package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/mailer"
)

// The notification outbox (migration 784): a durable retry queue for e-mail, Slack and Teams messages. A message is
// written to the queue instead of being sent inline; a worker leases due rows, sends them, and retries failures with
// exponential backoff until MaxOutboxAttempts, after which the row is 'dead' (kept 30 days for inspection).
//
// The webhook URL is never stored in a row: it is read from the organization's settings at send time, so removing a
// channel (or a recipient) stops what is still queued for it.

// Channels of the outbox.
const (
	ChanEmail = "email"
	ChanSlack = KindSlack
	ChanTeams = KindTeams
)

// Statuses of an outbox row.
const (
	OutboxPending = "pending"
	OutboxSent    = "sent"
	OutboxDead    = "dead"
)

// Outbox tuning.
const (
	// MaxOutboxAttempts is the number of tries before a message becomes dead.
	MaxOutboxAttempts = 6
	// OutboxRetention is how long sent and dead rows are kept.
	OutboxRetention = 30 * 24 * time.Hour
	outboxBatch     = 20
	outboxLease     = 2 * time.Minute
)

// outboxSchedule[i] is the wait after the (i+1)-th failed attempt.
var outboxSchedule = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 6 * time.Hour}

// OutboxBackoff returns the wait after `attempt` failed attempts (1-based); jitter in [0,1) spreads it by +-10%.
func OutboxBackoff(attempt int, jitter float64) time.Duration {
	i := attempt - 1
	if i < 0 {
		i = 0
	}
	if i >= len(outboxSchedule) {
		i = len(outboxSchedule) - 1
	}
	return time.Duration(float64(outboxSchedule[i]) * (0.9 + 0.2*jitter))
}

// OutboxItem is one queued message for one destination.
type OutboxItem struct {
	OrgID     string
	ID        string
	Channel   string // email | slack | teams
	Recipient string // e-mail address; empty for chat
	// Payload is the e-mail (JSON of emailPayload) or the chat body. Sealed says it is encrypted with the vault.
	Payload       []byte
	Sealed        bool
	Status        string
	Attempts      int
	NextAttemptAt time.Time
	LastError     string
	CreatedAt     time.Time
	SentAt        *time.Time
}

// OutboxStore persists the queue. Everything is scoped to one organization.
type OutboxStore interface {
	Enqueue(ctx context.Context, it OutboxItem) error
	// Claim leases up to limit due pending rows: their next_attempt_at moves forward by lease, so a worker that dies
	// leaves them to be retried and two workers never take the same row.
	Claim(ctx context.Context, org string, now time.Time, limit int, lease time.Duration) ([]OutboxItem, error)
	// Record stores the outcome of an attempt.
	Record(ctx context.Context, org, id, status string, attempts int, next time.Time, lastErr string, sentAt *time.Time) error
	HasPending(ctx context.Context, org string) (bool, error)
	// Prune deletes sent and dead rows created before the cutoff.
	Prune(ctx context.Context, org string, before time.Time) (int, error)
}

// DeadLetterStore is the optional OutboxStore capability behind the admin view of dead messages.
type DeadLetterStore interface {
	// ListDead returns the dead rows of org, newest first (at most limit).
	ListDead(ctx context.Context, org string, limit int) ([]OutboxItem, error)
	// Requeue returns a dead row to pending with attempts reset to 0, due at now. It returns false when the row does
	// not exist or is not dead (already retried, or sent): the UPDATE is the claim, so a double click retries once.
	Requeue(ctx context.Context, org, id string, now time.Time) (bool, error)
}

// DeadLetter is what the admin console shows of a dead row. The payload (it can carry signed links) never leaves.
type DeadLetter struct {
	ID        string    `json:"id"`
	Channel   string    `json:"channel"`
	Recipient string    `json:"recipient,omitempty"`
	Attempts  int       `json:"attempts"`
	LastError string    `json:"last_error"`
	CreatedAt time.Time `json:"created_at"`
}

// DeadLetterLimit bounds the list.
const DeadLetterLimit = 100

var errDeadLettersOff = fmt.Errorf("%w: the notification queue does not keep dead letters here", domain.ErrInvalid)

func toDeadLetter(it OutboxItem) DeadLetter {
	return DeadLetter{ID: it.ID, Channel: it.Channel, Recipient: it.Recipient, Attempts: it.Attempts, LastError: it.LastError, CreatedAt: it.CreatedAt}
}

// ListDeadLetters lists the dead messages of the organization.
func (s *Service) ListDeadLetters(ctx context.Context, org string) ([]DeadLetter, error) {
	st, ok := s.Outbox.(DeadLetterStore)
	if !ok {
		return nil, errDeadLettersOff
	}
	items, err := st.ListDead(ctx, org, DeadLetterLimit)
	if err != nil {
		return nil, err
	}
	out := make([]DeadLetter, 0, len(items))
	for _, it := range items {
		out = append(out, toDeadLetter(it))
	}
	return out, nil
}

// RetryDeadLetter puts a dead message back in the queue (attempts reset) and wakes the worker. ErrNotFound when the
// message does not exist in this organization or is not dead.
func (s *Service) RetryDeadLetter(ctx context.Context, org, id string) (DeadLetter, error) {
	st, ok := s.Outbox.(DeadLetterStore)
	if !ok {
		return DeadLetter{}, errDeadLettersOff
	}
	dl := DeadLetter{ID: id}
	if items, err := st.ListDead(ctx, org, DeadLetterLimit); err == nil {
		for _, it := range items {
			if it.ID == id {
				dl = toDeadLetter(it)
			}
		}
	}
	done, err := st.Requeue(ctx, org, id, s.now())
	if err != nil {
		return DeadLetter{}, err
	}
	if !done {
		return DeadLetter{}, domain.ErrNotFound
	}
	s.hot.Store(org, struct{}{})
	return dl, nil
}

type emailPayload struct {
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html"`
}

// ---- delivery ----

// httpStatusError is a webhook that answered with a non-2xx status (the URL is a secret and is never in the text).
type httpStatusError struct{ Code int }

func (e *httpStatusError) Error() string { return fmt.Sprintf("webhook answered %d", e.Code) }

// errChannelOff: the channel or recipient was removed after the message was queued.
var errChannelOff = errors.New("channel is not configured any more")

// permanent says whether retrying cannot help: the address is not allowed, the channel is gone, or the receiver
// rejected the message on purpose (4xx other than timeout / too early / rate limit).
func permanent(err error) bool {
	if errors.Is(err, errChannelOff) || errors.Is(err, domain.ErrInvalid) {
		return true
	}
	var se *httpStatusError
	if errors.As(err, &se) {
		switch {
		case se.Code == http.StatusRequestTimeout, se.Code == http.StatusTooEarly, se.Code == http.StatusTooManyRequests:
			return false
		case se.Code >= 400 && se.Code < 500:
			return true
		}
	}
	return false
}

// Outcome is what happens to a row after an attempt (attempts already counts it).
type Outcome struct {
	Status    string
	Next      time.Time
	LastError string
	SentAt    *time.Time
}

// OutboxOutcome decides the fate of a row after an attempt.
func OutboxOutcome(now time.Time, attempts int, err error, jitter float64, maxAttempts int) Outcome {
	if maxAttempts <= 0 {
		maxAttempts = MaxOutboxAttempts
	}
	if err == nil {
		t := now
		return Outcome{Status: OutboxSent, SentAt: &t}
	}
	msg := Clip(oneLine(err.Error()), 200)
	if permanent(err) || attempts >= maxAttempts {
		return Outcome{Status: OutboxDead, LastError: msg}
	}
	return Outcome{Status: OutboxPending, Next: now.Add(OutboxBackoff(attempts, jitter)), LastError: msg}
}

// enqueue writes a message to the outbox and wakes the worker for the organization.
func (s *Service) enqueue(ctx context.Context, org, channel, recipient string, payload []byte) error {
	sealed := false
	if sealOn(s.Sealer) {
		enc, err := s.Sealer.Seal(ctx, org, sealOutbox, payload)
		if err != nil {
			return err // never queue a payload (it can carry signed links) in the clear when sealing was expected
		}
		payload, sealed = enc, true
	}
	now := s.now()
	err := s.Outbox.Enqueue(ctx, OutboxItem{OrgID: org, ID: uuid.NewString(), Channel: channel, Recipient: recipient, Payload: payload,
		Sealed: sealed, Status: OutboxPending, NextAttemptAt: now, CreatedAt: now})
	if err == nil {
		s.hot.Store(org, struct{}{})
	}
	return err
}

// sendEmail queues an e-mail, or sends it right away when there is no outbox (or the queue refused it).
func (s *Service) sendEmail(ctx context.Context, org, to string, m mailer.Message) {
	if s.Outbox != nil {
		b, _ := json.Marshal(emailPayload{Subject: m.Subject, Text: m.Text, HTML: m.HTML})
		if err := s.enqueue(ctx, org, ChanEmail, to, b); err == nil {
			return
		} else {
			s.log().Warn("notify: outbox", "org", org, "channel", ChanEmail, "err", err)
		}
	}
	m.To = to
	if err := s.Mail.Send(ctx, m); err != nil {
		s.log().Warn("notify: e-mail", "org", org, "err", err)
	}
}

// sendChat queues a chat body for kind, or posts it right away without an outbox.
func (s *Service) sendChat(ctx context.Context, org, kind, hook string, body []byte) {
	if s.Outbox != nil {
		if err := s.enqueue(ctx, org, kind, "", body); err == nil {
			return
		} else {
			s.log().Warn("notify: outbox", "org", org, "channel", kind, "err", err)
		}
	}
	if err := s.post(ctx, kind, hook, body); err != nil {
		s.log().Warn("notify: "+kind, "org", org, "err", err)
	}
}

// deliver sends one leased row. The channel is re-checked against the current settings.
func (s *Service) deliver(ctx context.Context, it OutboxItem) error {
	payload := it.Payload
	if it.Sealed {
		if !sealOn(s.Sealer) {
			return errors.New("sealed payload and no vault key")
		}
		b, err := s.Sealer.Unseal(ctx, it.OrgID, sealOutbox, payload)
		if err != nil {
			return errors.New("sealed payload cannot be opened")
		}
		payload = b
	}
	st, err := s.Settings.GetSettings(ctx, it.OrgID)
	if err != nil {
		return err // transient
	}
	switch it.Channel {
	case ChanEmail:
		if !st.EmailEnabled || s.Mail == nil || !containsFold(st.EmailTo, it.Recipient) {
			return errChannelOff
		}
		var ep emailPayload
		if err := json.Unmarshal(payload, &ep); err != nil {
			return fmt.Errorf("%w: bad e-mail payload", domain.ErrInvalid)
		}
		return s.Mail.Send(ctx, mailer.Message{To: it.Recipient, Subject: ep.Subject, Text: ep.Text, HTML: ep.HTML})
	case ChanSlack, ChanTeams:
		hook := st.SlackWebhook
		if it.Channel == ChanTeams {
			hook = st.TeamsWebhook
		}
		if hook == "" {
			return errChannelOff
		}
		return s.post(ctx, it.Channel, hook, payload)
	}
	return fmt.Errorf("%w: unknown channel", domain.ErrInvalid)
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// ProcessOutbox sends the due messages of one organization and returns how many rows were attempted.
func (s *Service) ProcessOutbox(ctx context.Context, org string) (int, error) {
	if s.Outbox == nil || s.Settings == nil {
		return 0, nil
	}
	items, err := s.Outbox.Claim(ctx, org, s.now(), outboxBatch, outboxLease)
	if err != nil {
		return 0, err
	}
	for _, it := range items {
		if ctx.Err() != nil {
			break
		}
		attempts := it.Attempts + 1
		sendErr := s.deliver(ctx, it)
		o := OutboxOutcome(s.now(), attempts, sendErr, s.jitter(), s.MaxAttempts)
		if sendErr != nil {
			s.log().Warn("notify: delivery failed", "org", org, "channel", it.Channel, "attempt", attempts, "status", o.Status, "err", o.LastError)
		}
		next := o.Next
		if next.IsZero() {
			next = s.now()
		}
		if err := s.Outbox.Record(ctx, org, it.ID, o.Status, attempts, next, o.LastError, o.SentAt); err != nil {
			s.log().Warn("notify: outbox record", "org", org, "err", err)
		}
	}
	return len(items), nil
}

func (s *Service) jitter() float64 {
	if s.Jitter != nil {
		return s.Jitter()
	}
	return 0.5
}

// RunOutbox is the worker loop; it blocks until ctx ends. Organizations that enqueued something are processed every
// few seconds; a slower sweep over all organizations picks up rows left by a restart or another replica.
func (s *Service) RunOutbox(ctx context.Context, orgs OrgLister) {
	tick := time.NewTicker(10 * time.Second)
	sweep := time.NewTicker(2 * time.Minute)
	prune := time.NewTicker(6 * time.Hour)
	defer tick.Stop()
	defer sweep.Stop()
	defer prune.Stop()
	s.markPending(ctx, orgs)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.hot.Range(func(k, _ any) bool {
				org := k.(string)
				if _, err := s.ProcessOutbox(ctx, org); err != nil {
					s.log().Warn("notify: outbox", "org", org, "err", err)
				}
				if p, err := s.Outbox.HasPending(ctx, org); err == nil && !p {
					s.hot.Delete(org)
				}
				return true
			})
		case <-sweep.C:
			s.markPending(ctx, orgs)
		case <-prune.C:
			s.forEachOrg(ctx, orgs, func(org string) { _, _ = s.Outbox.Prune(ctx, org, s.now().Add(-OutboxRetention)) })
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
		if ctx.Err() != nil {
			return
		}
		fn(id)
	}
}

func (s *Service) markPending(ctx context.Context, orgs OrgLister) {
	s.forEachOrg(ctx, orgs, func(org string) {
		if p, err := s.Outbox.HasPending(ctx, org); err == nil && p {
			s.hot.Store(org, struct{}{})
		}
	})
}

// StartOutbox runs RunOutbox in the background.
func (s *Service) StartOutbox(ctx context.Context, orgs OrgLister) {
	if s.Outbox == nil {
		return
	}
	go s.RunOutbox(ctx, orgs)
}

// ---- in-memory store ----

// MemOutbox is the in-memory OutboxStore (tests and deployments without Postgres).
type MemOutbox struct {
	mu   sync.Mutex
	rows map[string]*OutboxItem // org/id
}

// NewMemOutbox returns an empty outbox.
func NewMemOutbox() *MemOutbox { return &MemOutbox{rows: map[string]*OutboxItem{}} }

func okey(org, id string) string { return org + "/" + id }

func (m *MemOutbox) Enqueue(_ context.Context, it OutboxItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := it
	m.rows[okey(it.OrgID, it.ID)] = &cp
	return nil
}

func (m *MemOutbox) Claim(_ context.Context, org string, now time.Time, limit int, lease time.Duration) ([]OutboxItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []*OutboxItem
	for _, r := range m.rows {
		if r.OrgID == org && r.Status == OutboxPending && !r.NextAttemptAt.After(now) {
			due = append(due, r)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].NextAttemptAt.Before(due[j].NextAttemptAt) })
	if len(due) > limit {
		due = due[:limit]
	}
	out := make([]OutboxItem, 0, len(due))
	for _, r := range due {
		out = append(out, *r)
		r.NextAttemptAt = now.Add(lease)
	}
	return out, nil
}

func (m *MemOutbox) Record(_ context.Context, org, id, status string, attempts int, next time.Time, lastErr string, sentAt *time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[okey(org, id)]
	if !ok {
		return domain.ErrNotFound
	}
	r.Status, r.Attempts, r.LastError, r.SentAt = status, attempts, lastErr, sentAt
	if status == OutboxPending {
		r.NextAttemptAt = next
	}
	return nil
}

func (m *MemOutbox) HasPending(_ context.Context, org string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rows {
		if r.OrgID == org && r.Status == OutboxPending {
			return true, nil
		}
	}
	return false, nil
}

func (m *MemOutbox) Prune(_ context.Context, org string, before time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k, r := range m.rows {
		if r.OrgID == org && r.Status != OutboxPending && r.CreatedAt.Before(before) {
			delete(m.rows, k)
			n++
		}
	}
	return n, nil
}

func (m *MemOutbox) ListDead(_ context.Context, org string, limit int) ([]OutboxItem, error) {
	var out []OutboxItem
	for _, r := range m.Rows(org) {
		if r.Status == OutboxDead {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemOutbox) Requeue(_ context.Context, org, id string, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[okey(org, id)]
	if !ok || r.Status != OutboxDead {
		return false, nil
	}
	r.Status, r.Attempts, r.NextAttemptAt, r.LastError = OutboxPending, 0, now, ""
	return true, nil
}

// Rows returns a copy of the rows of org, oldest first (tests and diagnostics).
func (m *MemOutbox) Rows(org string) []OutboxItem {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []OutboxItem
	for _, r := range m.rows {
		if r.OrgID == org {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) || (out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID < out[j].ID) })
	return out
}

// ---- Postgres ----

const outCols = `id, channel, recipient, payload, sealed, status, attempts, next_attempt_at, last_error, created_at, sent_at`

func (p *PG) Enqueue(ctx context.Context, it OutboxItem) error {
	return p.DB.WithOrgTx(ctx, it.OrgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO notify_outbox (org_id, id, channel, recipient, payload, sealed, status, attempts, next_attempt_at, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,'pending',0,$7,$8)`, it.OrgID, it.ID, it.Channel, it.Recipient, it.Payload, it.Sealed, it.NextAttemptAt, it.CreatedAt)
		return err
	})
}

func (p *PG) Claim(ctx context.Context, org string, now time.Time, limit int, lease time.Duration) ([]OutboxItem, error) {
	var out []OutboxItem
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE notify_outbox SET next_attempt_at = $2::timestamptz + make_interval(secs => $4)
			WHERE org_id=$1 AND id IN (
				SELECT id FROM notify_outbox WHERE org_id=$1 AND status='pending' AND next_attempt_at <= $2
				ORDER BY next_attempt_at LIMIT $3 FOR UPDATE SKIP LOCKED)
			RETURNING `+outCols, org, now, limit, lease.Seconds())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			it := OutboxItem{OrgID: org}
			if err := rows.Scan(&it.ID, &it.Channel, &it.Recipient, &it.Payload, &it.Sealed, &it.Status, &it.Attempts, &it.NextAttemptAt, &it.LastError, &it.CreatedAt, &it.SentAt); err != nil {
				return err
			}
			out = append(out, it)
		}
		return rows.Err()
	})
	return out, err
}

func (p *PG) Record(ctx context.Context, org, id, status string, attempts int, next time.Time, lastErr string, sentAt *time.Time) error {
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		// A delivered row drops its payload (it can carry signed approval links); a dead one keeps it for inspection.
		_, err := tx.Exec(ctx, `UPDATE notify_outbox SET status=$3, attempts=$4,
				next_attempt_at = CASE WHEN $3='pending' THEN $5 ELSE next_attempt_at END,
				last_error=$6, sent_at=$7,
				payload = CASE WHEN $3='sent' THEN '\x'::bytea ELSE payload END
			WHERE org_id=$1 AND id=$2`, org, id, status, attempts, next, lastErr, sentAt)
		return err
	})
}

func (p *PG) HasPending(ctx context.Context, org string) (bool, error) {
	var has bool
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM notify_outbox WHERE org_id=$1 AND status='pending')`, org).Scan(&has)
	})
	return has, err
}

func (p *PG) Prune(ctx context.Context, org string, before time.Time) (int, error) {
	n := 0
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM notify_outbox WHERE org_id=$1 AND status<>'pending' AND created_at < $2`, org, before)
		n = int(tag.RowsAffected())
		return err
	})
	return n, err
}

func (p *PG) ListDead(ctx context.Context, org string, limit int) ([]OutboxItem, error) {
	var out []OutboxItem
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, channel, recipient, attempts, last_error, created_at FROM notify_outbox
			WHERE org_id=$1 AND status='dead' ORDER BY created_at DESC LIMIT $2`, org, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			it := OutboxItem{OrgID: org, Status: OutboxDead}
			if err := rows.Scan(&it.ID, &it.Channel, &it.Recipient, &it.Attempts, &it.LastError, &it.CreatedAt); err != nil {
				return err
			}
			out = append(out, it)
		}
		return rows.Err()
	})
	return out, err
}

func (p *PG) Requeue(ctx context.Context, org, id string, now time.Time) (bool, error) {
	done := false
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE notify_outbox SET status='pending', attempts=0, next_attempt_at=$3, last_error='', sent_at=NULL
			WHERE org_id=$1 AND id=$2 AND status='dead'`, org, id, now)
		done = err == nil && tag.RowsAffected() == 1
		return err
	})
	return done, err
}
