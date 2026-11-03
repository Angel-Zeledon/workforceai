package application

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// "Necesita tu ayuda" (docs/superpowers/specs/2026-11-03-proyectos-por-cliente-y-ayuda-design.md, part 2;
// docs/architecture/approvals-inbox.md, questions). An agent that cannot get or decide something by itself asks a
// person instead of inventing or failing. The question is stored, its task waits (needs_input) without calling the
// model, and a person answers with text, an option, a file, by reassigning the task or by skipping it. The agent
// never decides because nobody answered: an open question only reminds (24 h, then every day for 3 days, then weekly).

// Question statuses and answer kinds.
const (
	QuestionOpen       = "open"
	QuestionAnswered   = "answered"
	QuestionReassigned = "reassigned"
	QuestionSkipped    = "skipped"
	QuestionCancelled  = "cancelled"

	AnswerText     = "text"
	AnswerOption   = "option"
	AnswerFile     = "file"
	AnswerReassign = "reassign"
	AnswerSkip     = "skip"

	// MaxQuestionsPerTask bounds how many times one task may ask: a fourth question fails the task (recoverable)
	// instead of making a person answer forever.
	MaxQuestionsPerTask = 3
	MaxAnswerRunes      = 2000
	maxQuestionText     = 600
	maxQuestionOptions  = 5
)

// Question is one "needs your help" of a task.
type Question struct {
	ID           string   `json:"id"`
	TaskID       string   `json:"task_id"`
	RequestID    string   `json:"request_id"`
	AgentID      string   `json:"agent_id"`
	ProjectID    string   `json:"project_id,omitempty"`
	GroupKey     string   `json:"group_key,omitempty"` // batch:<id> | project:<id> | request:<id>: what notices are coalesced by
	Kind         string   `json:"kind"`                // data|file|decision|contradiction|other
	WhatHappened string   `json:"what_happened"`
	Need         string   `json:"need"`
	Options      []string `json:"options"`
	Status       string   `json:"status"`
	// AskedFor is the person the question is for (who launched the work); "" = the owners and admins.
	AskedFor   string     `json:"asked_for,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
	ResolvedBy string     `json:"resolved_by,omitempty"`
	AnswerKind string     `json:"answer_kind,omitempty"`
	Answer     string     `json:"answer,omitempty"`
	FilePath   string     `json:"file_path,omitempty"`
	ReassignTo string     `json:"reassign_to,omitempty"`
	Reminders  int        `json:"reminders"`
}

// QuestionStore persists the questions. ResolveQuestion writes the resolution only while the question is open and
// returns domain.ErrConflict otherwise, so two answers can never both win.
type QuestionStore interface {
	CreateQuestion(ctx context.Context, orgID string, q Question) error
	GetQuestion(ctx context.Context, orgID, id string) (Question, error)
	ResolveQuestion(ctx context.Context, orgID string, q Question) error
	SetQuestionReminders(ctx context.Context, orgID, id string, n int) error
	// ListQuestions returns the questions with that status ("" = all), newest first.
	ListQuestions(ctx context.Context, orgID, status string) ([]Question, error)
	// ListQuestionsByTask returns the questions of one task, oldest first.
	ListQuestionsByTask(ctx context.Context, orgID, taskID string) ([]Question, error)
}

// Resolution is what a person (or the system, for a cancellation) decided about a question.
type Resolution struct {
	Kind     string `json:"kind"`
	Text     string `json:"text,omitempty"`
	Option   string `json:"option,omitempty"`
	FilePath string `json:"file_path,omitempty"`
	AgentID  string `json:"agent_id,omitempty"`
}

// NextReminder is when the n-th reminder (1-based) of a question created at created is due: every base for the first
// four (24 h, then every day for 3 days), then every 7 bases (weekly). base <= 0 means 24 h.
func NextReminder(created time.Time, n int, base time.Duration) time.Time {
	if base <= 0 {
		base = 24 * time.Hour
	}
	n = max(n, 1)
	if n <= 4 {
		return created.Add(time.Duration(n) * base)
	}
	return created.Add(4*base + time.Duration(n-4)*7*base)
}

// Questions keeps the waiters of the open questions (like Approvals) and the coalesced notices.
type Questions struct {
	Store QuestionStore
	Rec   *Recorder
	// ReminderBase is the reminder unit (default 24 h; tests shorten it). NoticeWindow is how long the questions of
	// one group for one person are gathered into one notice after the first (default 3 minutes).
	ReminderBase time.Duration
	NoticeWindow time.Duration

	mu      sync.Mutex
	waiters map[string]chan Question
	notices *helpCoalescer
}

func (q *Questions) org(ctx context.Context) string {
	if q.Rec != nil {
		return q.Rec.org(ctx)
	}
	return OrgFrom(ctx, domain.DemoOrgID)
}

func cleanLine(s string, n int) string {
	s = strings.Join(strings.Fields(SanitizeChatText(s)), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return s
}

// Ask stores an open question, registers its waiter and announces it (question.asked and a coalesced help.notice).
func (q *Questions) Ask(ctx context.Context, qu Question) (Question, <-chan Question, error) {
	if qu.ID == "" {
		qu.ID = newID()
	}
	qu.WhatHappened, qu.Need = cleanLine(qu.WhatHappened, maxQuestionText), cleanLine(qu.Need, maxQuestionText)
	if qu.Need == "" {
		qu.Need = qu.WhatHappened
	}
	opts := make([]string, 0, len(qu.Options))
	for _, o := range qu.Options {
		if o = cleanLine(o, 160); o != "" && len(opts) < maxQuestionOptions {
			opts = append(opts, o)
		}
	}
	qu.Options = opts
	switch qu.Kind {
	case "data", "file", "decision", "contradiction":
	default:
		qu.Kind = "other"
	}
	qu.Status, qu.CreatedAt, qu.Reminders = QuestionOpen, time.Now().UTC(), 0
	if qu.GroupKey == "" {
		qu.GroupKey = "request:" + qu.RequestID
	}
	if err := q.Store.CreateQuestion(ctx, q.org(ctx), qu); err != nil {
		return qu, nil, err
	}
	ch := make(chan Question, 1)
	q.mu.Lock()
	if q.waiters == nil {
		q.waiters = map[string]chan Question{}
	}
	q.waiters[qu.ID] = ch
	q.mu.Unlock()
	q.Rec.Emit(ctx, Action{Type: domain.EvQuestionAsked, AgentID: qu.AgentID, Entity: "question", EntityID: qu.ID,
		Payload: map[string]any{"question": qu}, Text: "Necesita tu ayuda: " + truncate(qu.Need, 120)})
	q.notice(ctx, qu, false)
	return qu, ch, nil
}

// Attach registers a waiter for a question that already exists (a task resumed after a restart). When the question
// was resolved meanwhile, the channel already holds it.
func (q *Questions) Attach(ctx context.Context, id string) (Question, <-chan Question, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	qu, err := q.Store.GetQuestion(ctx, q.org(ctx), id)
	if err != nil {
		return qu, nil, err
	}
	ch := make(chan Question, 1)
	if qu.Status != QuestionOpen {
		ch <- qu
		return qu, ch, nil
	}
	if q.waiters == nil {
		q.waiters = map[string]chan Question{}
	}
	q.waiters[id] = ch
	return qu, ch, nil
}

// OpenFor returns the open question of a task, if any.
func (q *Questions) OpenFor(ctx context.Context, taskID string) (Question, bool) {
	qs, err := q.Store.ListQuestionsByTask(ctx, q.org(ctx), taskID)
	if err != nil {
		return Question{}, false
	}
	for i := len(qs) - 1; i >= 0; i-- {
		if qs[i].Status == QuestionOpen {
			return qs[i], true
		}
	}
	return Question{}, false
}

// Resolve applies a resolution (already validated by the caller for reassign/skip) and wakes the waiting task.
// actor is the person ("system" for a cancellation). A question that is no longer open is a conflict.
func (q *Questions) Resolve(ctx context.Context, id, actor string, r Resolution) (Question, error) {
	org := q.org(ctx)
	qu, err := q.Store.GetQuestion(ctx, org, id)
	if err != nil {
		return qu, err
	}
	if qu.Status != QuestionOpen {
		return qu, fmt.Errorf("%w: the question is already %s", domain.ErrConflict, qu.Status)
	}
	now := time.Now().UTC()
	qu.ResolvedAt, qu.ResolvedBy, qu.AnswerKind = &now, actor, r.Kind
	switch r.Kind {
	case AnswerText, AnswerOption, AnswerFile:
		qu.Status, qu.Answer, qu.FilePath = QuestionAnswered, r.Text, r.FilePath
		if r.Kind == AnswerOption {
			qu.Answer = r.Option
		}
	case AnswerReassign:
		qu.Status, qu.ReassignTo, qu.Answer = QuestionReassigned, r.AgentID, r.Text
	case AnswerSkip:
		qu.Status, qu.Answer = QuestionSkipped, r.Text
	case QuestionCancelled:
		qu.Status, qu.Answer = QuestionCancelled, r.Text
	default:
		return qu, fmt.Errorf("%w: kind must be text, option, file, reassign or skip", domain.ErrInvalid)
	}
	if err := q.Store.ResolveQuestion(ctx, org, qu); err != nil {
		return qu, err
	}
	q.Rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: "question." + qu.Status, Entity: "question", EntityID: qu.ID, RequestID: qu.RequestID,
		Details: map[string]any{"task_id": qu.TaskID, "agent_id": qu.AgentID, "answer_kind": qu.AnswerKind, "chars": len([]rune(qu.Answer)),
			"file_path": qu.FilePath, "reassign_to": qu.ReassignTo}})
	q.Rec.Emit(ctx, Action{Type: domain.EvQuestionResolved, AgentID: qu.AgentID, Entity: "question", EntityID: qu.ID, SkipAudit: true,
		Payload: map[string]any{"question": qu}})
	q.mu.Lock()
	ch := q.waiters[id]
	delete(q.waiters, id)
	q.mu.Unlock()
	if ch != nil {
		ch <- qu
	}
	return qu, nil
}

// CancelRequest closes, as the system, every open question of a request (its project was cancelled).
func (q *Questions) CancelRequest(ctx context.Context, requestID, note string) int {
	qs, err := q.Store.ListQuestions(ctx, q.org(ctx), QuestionOpen)
	if err != nil {
		return 0
	}
	n := 0
	for _, qu := range qs {
		if qu.RequestID == requestID {
			if _, err := q.Resolve(ctx, qu.ID, "system", Resolution{Kind: QuestionCancelled, Text: note}); err == nil {
				n++
			}
		}
	}
	return n
}

// Wait blocks until the question is resolved or ctx ends, reminding on the NextReminder cadence (which follows the
// question's age, so a restart does not reset it). It never resolves the question by itself.
func (q *Questions) Wait(ctx context.Context, ch <-chan Question, qu Question) (Question, error) {
	select {
	case res := <-ch:
		return res, nil
	default:
	}
	n := qu.Reminders
	for {
		due := NextReminder(qu.CreatedAt, n+1, q.ReminderBase)
		timer := time.NewTimer(max(time.Until(due), 0))
		select {
		case res := <-ch:
			timer.Stop()
			return res, nil
		case <-timer.C:
			n++
			q.remind(ctx, qu, n)
		case <-ctx.Done():
			timer.Stop()
			q.mu.Lock()
			delete(q.waiters, qu.ID)
			q.mu.Unlock()
			return Question{}, ctx.Err()
		}
	}
}

func (q *Questions) remind(ctx context.Context, qu Question, n int) {
	cur, err := q.Store.GetQuestion(ctx, q.org(ctx), qu.ID)
	if err != nil || cur.Status != QuestionOpen {
		return
	}
	_ = q.Store.SetQuestionReminders(ctx, q.org(ctx), qu.ID, n)
	cur.Reminders = n
	waited := time.Since(cur.CreatedAt)
	q.Rec.Emit(ctx, Action{Type: domain.EvQuestionReminder, AgentID: cur.AgentID, Entity: "question", EntityID: cur.ID, SkipAudit: true,
		Payload: map[string]any{"question": cur, "reminder": n, "waiting_seconds": int(waited.Seconds())},
		Text:    fmt.Sprintf("Recordatorio: una pregunta espera desde hace %s: %s", waited.Round(time.Minute), truncate(cur.Need, 100))})
	q.notice(ctx, cur, true)
}

// ---- coalesced notices ----

func (q *Questions) notice(ctx context.Context, qu Question, reminder bool) {
	q.mu.Lock()
	if q.notices == nil {
		w := q.NoticeWindow
		if w <= 0 {
			w = 3 * time.Minute
		}
		q.notices = &helpCoalescer{window: w, open: map[string]*noticeWindow{}, emit: q.emitNotice}
	}
	c := q.notices
	q.mu.Unlock()
	c.add(q.org(ctx), qu, reminder)
}

func (q *Questions) emitNotice(org string, first Question, ids []string, reminder bool) {
	ctx := WithOrg(context.Background(), org)
	q.Rec.Emit(ctx, Action{Type: domain.EvHelpNotice, AgentID: first.AgentID, Entity: "question", EntityID: first.ID, SkipAudit: true,
		Payload: map[string]any{"asked_for": first.AskedFor, "group": first.GroupKey, "project_id": first.ProjectID, "count": len(ids),
			"question_ids": ids, "reminder": reminder, "need": first.Need}})
}

// helpCoalescer turns a burst of questions of one group for one person into few notices: the first goes out at
// once, the ones that arrive during the window go out together when it closes ("5 more need your help"). Weekly
// reminders (the 5th and later) gather over a day, so a batch gets one weekly summary instead of one per customer.
type helpCoalescer struct {
	window time.Duration
	emit   func(org string, first Question, ids []string, reminder bool)
	mu     sync.Mutex
	open   map[string]*noticeWindow
}

type noticeWindow struct {
	first    Question
	ids      []string
	reminder bool
}

func (c *helpCoalescer) add(org string, qu Question, reminder bool) {
	window := c.window
	if reminder && qu.Reminders >= 5 {
		window = max(window, 24*time.Hour)
	}
	key := fmt.Sprintf("%s|%s|%s|%t", org, qu.GroupKey, qu.AskedFor, reminder)
	c.mu.Lock()
	if w, ok := c.open[key]; ok {
		w.ids = append(w.ids, qu.ID)
		c.mu.Unlock()
		return
	}
	w := &noticeWindow{first: qu, reminder: reminder}
	c.open[key] = w
	c.mu.Unlock()
	c.emit(org, qu, []string{qu.ID}, reminder)
	time.AfterFunc(window, func() {
		c.mu.Lock()
		delete(c.open, key)
		ids := w.ids
		c.mu.Unlock()
		if len(ids) > 0 {
			c.emit(org, w.first, ids, reminder)
		}
	})
}
