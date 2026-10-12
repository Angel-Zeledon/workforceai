package memory

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/sanitize"
	"aiworkforce/backend/internal/textfold"
)

// Config wires the memory service.
type Config struct {
	Docs      DocStore
	Retriever Retriever
	// Suspects redacts the exact secrets the server knows (optional).
	Suspects *sanitize.Suspects
	// ProjectOf tells the project a request belongs to ("" when it is not a
	// project); ProjectPath its drive folder. Both optional.
	ProjectOf   func(ctx context.Context, org, requestID string) (id, name string)
	ProjectPath func(ctx context.Context, org, projectID string) string
	// Audit records a memory change by a person (optional).
	Audit func(ctx context.Context, org, actor, action, id string, details map[string]any)
	Now   func() time.Time
	Log   *slog.Logger
}

// Service is the long-term memory: it indexes finished work, retrieves what is
// relevant for a task and lets people see and delete what an agent remembers.
type Service struct{ cfg Config }

var _ application.MemorySource = (*Service)(nil)

// New builds the service.
func New(cfg Config) *Service {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{cfg: cfg}
}

// ---- indexing ----

func (s *Service) project(ctx context.Context, org, requestID string) (string, string) {
	if s.cfg.ProjectOf == nil || requestID == "" {
		return "", ""
	}
	return s.cfg.ProjectOf(ctx, org, requestID)
}

func (s *Service) scrub(text string) string {
	return sanitize.RedactSecrets(text, s.cfg.Suspects)
}

func capRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// TaskDoc is the document of a finished task: its title, the request that
// asked for it and the key fields of its output.
func TaskDoc(t domain.Task, requestText string) Doc {
	var b strings.Builder
	if rt := strings.TrimSpace(requestText); rt != "" {
		fmt.Fprintf(&b, "Solicitud: %s\n", capRunes(rt, 400))
	}
	if t.Output != nil {
		o := t.Output
		if o.Summary != "" {
			b.WriteString(o.Summary + "\n")
		}
		list := func(head string, items []string, n int) {
			for i, it := range items {
				if i >= n {
					break
				}
				if it = strings.TrimSpace(it); it != "" {
					fmt.Fprintf(&b, "%s: %s\n", head, it)
				}
			}
		}
		list("Hallazgo", o.Findings, 6)
		list("Recomendación", o.Recommendations, 4)
		keys := make([]string, 0, len(o.Metrics))
		for k := range o.Metrics {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i >= 8 {
				break
			}
			fmt.Fprintf(&b, "Métrica %s: %v\n", k, o.Metrics[k])
		}
	}
	at := time.Now().UTC()
	if t.FinishedAt != nil {
		at = *t.FinishedAt
	}
	return Doc{ID: "task_" + t.ID, Kind: KindTask, Ref: application.TaskRef(t.ID), Title: capRunes(t.Title, 300), Body: capRunes(b.String(), maxBodyChars),
		AgentID: t.AgentID, Scope: ScopeOrg, RequestID: t.RequestID, CreatedAt: at, UpdatedAt: at}
}

// ReportDoc is the document of a report.
func ReportDoc(r domain.Report) Doc {
	var b strings.Builder
	if r.Summary != "" {
		b.WriteString(r.Summary + "\n")
	}
	for _, sec := range r.Sections {
		fmt.Fprintf(&b, "%s: %s\n", sec.Heading, sec.Body)
	}
	return Doc{ID: "report_" + r.ID, Kind: KindReport, Ref: "report:" + r.ID, Title: capRunes(r.Title, 300), Body: capRunes(b.String(), maxBodyChars),
		Scope: ScopeOrg, RequestID: r.RequestID, CreatedAt: r.CreatedAt, UpdatedAt: r.CreatedAt}
}

// CleanNotes keeps the notes an agent declared that are safe to remember: at
// most MaxNotes, short, one line each, without duplicates. A note that holds a
// secret (anything the redaction would change) or looks like an injection is
// dropped, never stored redacted.
func (s *Service) CleanNotes(notes []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range notes {
		n = strings.Join(strings.Fields(n), " ")
		n = capRunes(n, MaxNoteChars)
		key := textfold.Fold(n)
		if len([]rune(n)) < 8 || seen[key] || s.scrub(n) != n {
			continue
		}
		if score, _ := sanitize.ScoreInjection(n); score >= sanitize.InjectionThreshold {
			continue
		}
		seen[key] = true
		out = append(out, n)
		if len(out) >= MaxNotes {
			break
		}
	}
	return out
}

// RememberTask implements application.MemorySource: it indexes the finished
// task and stores the notes the agent declared. It never fails the task.
func (s *Service) RememberTask(ctx context.Context, org string, t domain.Task, requestText string) {
	if t.Status != domain.TaskDone || t.Output == nil {
		return
	}
	d := TaskDoc(t, requestText)
	d.Body = s.scrub(d.Body)
	d.Title = s.scrub(d.Title)
	d.ProjectID, d.ProjectName = s.project(ctx, org, t.RequestID)
	if err := s.cfg.Docs.Upsert(ctx, org, d); err != nil {
		s.cfg.Log.Warn("memory: index task", "task", t.ID, "err", err)
	}
	for i, n := range s.CleanNotes(t.Output.MemoryNotes) {
		nd := Doc{ID: fmt.Sprintf("note_%s_%d", t.ID, i+1), Kind: KindNote, Ref: fmt.Sprintf("memory:%s.%d", t.ID, i+1), Title: capRunes(n, 120), Body: n,
			AgentID: t.AgentID, Scope: ScopeAgent, ProjectID: d.ProjectID, ProjectName: d.ProjectName, RequestID: t.RequestID,
			SourceRef: application.TaskRef(t.ID), CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
		if err := s.cfg.Docs.Upsert(ctx, org, nd); err != nil {
			s.cfg.Log.Warn("memory: store note", "task", t.ID, "err", err)
		}
	}
}

// RememberReport implements application.MemorySource.
func (s *Service) RememberReport(ctx context.Context, org string, r domain.Report) {
	d := ReportDoc(r)
	d.Body, d.Title = s.scrub(d.Body), s.scrub(d.Title)
	d.ProjectID, d.ProjectName = s.project(ctx, org, r.RequestID)
	if err := s.cfg.Docs.Upsert(ctx, org, d); err != nil {
		s.cfg.Log.Warn("memory: index report", "report", r.ID, "err", err)
	}
}

// BackfillSource is what the backfill reads (the application store).
type BackfillSource interface {
	ListTasks(ctx context.Context, orgID, agentID, status string) ([]domain.Task, error)
	ListReports(ctx context.Context, orgID string) ([]domain.Report, error)
	GetRequest(ctx context.Context, orgID, id string) (domain.Request, error)
}

// Backfill indexes the finished tasks and the reports that existed before the
// memory did (or that a crash left out). It is idempotent: documents are
// upserted by ref and a deleted memory stays deleted. It returns the number of
// documents it wrote.
func (s *Service) Backfill(ctx context.Context, org string, src BackfillSource, maxTasks, maxReports int) int {
	n := 0
	tasks, err := src.ListTasks(ctx, org, "", string(domain.TaskDone))
	if err != nil {
		s.cfg.Log.Warn("memory: backfill tasks", "org", org, "err", err)
	}
	if maxTasks > 0 && len(tasks) > maxTasks {
		tasks = tasks[:maxTasks]
	}
	reqText := map[string]string{}
	for _, t := range tasks {
		if t.Output == nil {
			continue
		}
		text, ok := reqText[t.RequestID]
		if !ok {
			if r, err := src.GetRequest(ctx, org, t.RequestID); err == nil {
				text = r.Text
			}
			reqText[t.RequestID] = text
		}
		s.RememberTask(ctx, org, t, text)
		n++
	}
	reports, err := src.ListReports(ctx, org)
	if err != nil {
		s.cfg.Log.Warn("memory: backfill reports", "org", org, "err", err)
	}
	if maxReports > 0 && len(reports) > maxReports {
		reports = reports[:maxReports]
	}
	for _, r := range reports {
		s.RememberReport(ctx, org, r)
		n++
	}
	return n
}

// ---- retrieval ----

func (s *Service) retrieve(ctx context.Context, org string, q Query) []Item {
	items, err := s.cfg.Retriever.Retrieve(ctx, org, q)
	if err != nil {
		s.cfg.Log.Warn("memory: retrieve", "err", err)
		return nil
	}
	for i := range items {
		items[i].Title = Clean(items[i].Title, s.cfg.Suspects)
		items[i].Snippet = Clean(items[i].Snippet, s.cfg.Suspects)
	}
	return items
}

// Recall implements application.MemorySource: the memories relevant to a task,
// within the budget of a prompt (MaxItems items, MaxChars characters).
func (s *Service) Recall(ctx context.Context, org string, q application.MemoryRecall) []application.RecalledMemory {
	if s.cfg.Retriever == nil || strings.TrimSpace(q.Text) == "" {
		return nil
	}
	pid, _ := s.project(ctx, org, q.RequestID)
	query := Query{AgentID: q.AgentID, Text: q.Text, Limit: MaxItems * 2, ExcludeRequest: q.RequestID, PreferProject: pid}
	if pid != "" && s.cfg.ProjectPath != nil {
		query.PreferPath = s.cfg.ProjectPath(ctx, org, pid)
	}
	var out []application.RecalledMemory
	budget := MaxChars
	for _, it := range s.retrieve(ctx, org, query) {
		cost := len([]rune(it.Title)) + len([]rune(it.Snippet))
		if len(out) >= MaxItems || cost > budget && len(out) > 0 {
			break
		}
		budget -= cost
		out = append(out, application.RecalledMemory{Ref: it.Ref, Kind: it.Kind, Title: it.Title, Snippet: it.Snippet, Project: it.Project, AgentID: it.AgentID, At: it.At})
	}
	return out
}

// Search is the memory.search tool and the search box of the Memory tab: what
// the agent would recall for a query, with the permissions of that agent.
func (s *Service) Search(ctx context.Context, org, agentID, query string, limit int) ([]Item, error) {
	if s.cfg.Retriever == nil {
		return []Item{}, nil
	}
	if limit <= 0 || limit > MaxItems*2 {
		limit = MaxItems
	}
	query = capRunes(strings.TrimSpace(query), 500)
	items := s.retrieve(ctx, org, Query{AgentID: agentID, Text: query, Limit: limit})
	if items == nil {
		items = []Item{}
	}
	return items, nil
}

// Open returns the text of one document by its ref (task:, report:, memory:),
// when the agent may see it. Files are opened with fs.read.
func (s *Service) Open(ctx context.Context, org, agentID, ref string) (Item, error) {
	d, err := s.cfg.Docs.GetByRef(ctx, org, ref)
	if err != nil || d.Suppressed || d.Scope == ScopeAgent && d.AgentID != agentID {
		return Item{}, ErrNotFound
	}
	it := docItem(DocHit{Doc: d}, nil)
	it.Snippet = Clean(capRunes(d.Body, 6000), s.cfg.Suspects)
	it.Title = Clean(it.Title, s.cfg.Suspects)
	return it, nil
}

// List is the Memory tab: what an agent produced and remembers, newest first
// (its notes first), without search.
func (s *Service) List(ctx context.Context, org, agentID string, limit int) ([]Item, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	docs, err := s.cfg.Docs.List(ctx, org, agentID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(docs))
	for _, d := range docs {
		it := docItem(DocHit{Doc: d}, nil)
		it.Snippet = Clean(Passage(d.Body, nil, MaxSnippetChars), s.cfg.Suspects)
		it.Title = Clean(it.Title, s.cfg.Suspects)
		out = append(out, it)
	}
	return out, nil
}

// Delete removes a document from the agent's memory (a person's decision,
// audited). The task or report itself is untouched; the entry will not come
// back on a re-index. Only what the agent itself produced can be removed here.
func (s *Service) Delete(ctx context.Context, org, agentID, id, actor string) error {
	d, err := s.cfg.Docs.Get(ctx, org, id)
	if err != nil || d.Suppressed || d.AgentID != agentID {
		return ErrNotFound
	}
	if err := s.cfg.Docs.Suppress(ctx, org, id, actor, s.cfg.Now().UTC()); err != nil {
		return err
	}
	if s.cfg.Audit != nil {
		s.cfg.Audit(ctx, org, actor, "memory.deleted", id, map[string]any{"agent_id": agentID, "ref": d.Ref, "kind": d.Kind})
	}
	return nil
}

// Reset forgets everything of an organization (the demo reset: the tasks the
// memories point to are gone too).
func (s *Service) Reset(ctx context.Context, org string) error { return s.cfg.Docs.Clear(ctx, org) }

// RememberNote stores a fact a person told the agent to keep (/recordar). It
// goes through the same cleaning as the agent's own notes: a text with a secret
// or an injection pattern is refused, not stored.
func (s *Service) RememberNote(ctx context.Context, org, agentID, text string) error {
	notes := s.CleanNotes([]string{text})
	if len(notes) == 0 {
		return fmt.Errorf("%w: the note is empty, too long or unsafe", domain.ErrInvalid)
	}
	n, now := notes[0], time.Now().UTC()
	id := fmt.Sprintf("note_user_%s_%d", agentID, now.UnixNano())
	return s.cfg.Docs.Upsert(ctx, org, Doc{ID: id, Kind: KindNote, Ref: "memory:" + id, Title: capRunes(n, 120), Body: n,
		AgentID: agentID, Scope: ScopeAgent, CreatedAt: now, UpdatedAt: now})
}
