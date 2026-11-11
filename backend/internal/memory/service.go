package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
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
	// Embedder embeds new documents so they can be found by meaning (nil or
	// refusing: they are stored without a vector and found by keyword).
	Embedder Embedder
	// Suspects redacts the exact secrets the server knows (optional).
	Suspects *sanitize.Suspects
	// ProjectOf tells the project a request belongs to ("" when it is not a
	// project); ProjectPath its drive folder. Both optional.
	ProjectOf   func(ctx context.Context, org, requestID string) (id, name string)
	ProjectPath func(ctx context.Context, org, projectID string) string
	// CustomerOf tells the customer a request is scoped to (B12; optional: nil = no customers). An
	// error at recall time recalls nothing (fail closed).
	CustomerOf func(ctx context.Context, org, requestID string) (string, error)
	// ExtraRecall (optional, E2 offices) adds memories that do not live in the index: the notes of the agent's
	// office, of the person it works for and the company's knowledge. They come first, within the same budget.
	ExtraRecall func(ctx context.Context, org, agentID, requestID, text string) []application.RecalledMemory
	// AgentOffice (D3) tells the office an agent works in ("" for General's agents, unknown agents and when
	// there are no offices: then everything stays organization memory, as before). What a department's agents
	// learn is written with ScopeOffice and only the agents of that office recall it. An error means the office is
	// unknown: nothing is written (fail closed) and nothing of an office is read.
	AgentOffice func(ctx context.Context, org, agentID string) (string, error)
	// OfficeSealed tells whether an office is private. A report whose authors belong to several offices is only
	// indexed when none of them is private (nil = unknown: such a report is not indexed).
	OfficeSealed func(ctx context.Context, org, office string) (bool, error)
	// OfficeMember (D3) tells whether a person is a member of an office. An agent of a private office working for a
	// person who is not a member (a shared or directory agent) does not recall that office's memory; nil: no person
	// gets a private office's memory (fails closed). Work without a person (the agent's own) is not affected.
	OfficeMember func(ctx context.Context, org, office, userID string) (bool, error)
	// Audit records a memory change by a person (optional).
	Audit func(ctx context.Context, org, actor, action, id string, details map[string]any)
	Now   func() time.Time
	Log   *slog.Logger
}

// Service is the long-term memory: it indexes finished work, retrieves what is
// relevant for a task and lets people see and delete what an agent remembers.
type Service struct {
	cfg   Config
	offMu sync.RWMutex // guards cfg.AgentOffice / cfg.OfficeSealed / cfg.OfficeMember / cfg.ExtraRecall (set after New)
	topMu sync.Mutex
	topAt map[string]time.Time // last lazy embedding top-up per organization
}

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

// customer is the customer a request is scoped to ("" = none or unknown, see callers).
func (s *Service) customer(ctx context.Context, org, requestID string) (string, error) {
	if s.cfg.CustomerOf == nil || requestID == "" {
		return "", nil
	}
	return s.cfg.CustomerOf(ctx, org, requestID)
}

// tagCustomer sets the customer of the documents of a request. When it cannot be determined the
// documents stay untagged: an untagged document is only ever seen by tasks without a customer.
func (s *Service) tagCustomer(ctx context.Context, org, requestID string, docs ...*Doc) {
	cid, err := s.customer(ctx, org, requestID)
	if err != nil {
		s.cfg.Log.Warn("memory: customer of request", "request", requestID, "err", err)
	}
	for _, d := range docs {
		d.CustomerID = cid
	}
}

func (s *Service) officeFns() (func(context.Context, string, string) (string, error), func(context.Context, string, string) (bool, error)) {
	s.offMu.RLock()
	defer s.offMu.RUnlock()
	return s.cfg.AgentOffice, s.cfg.OfficeSealed
}

// officeOf is the office of an agent ("" = General or no offices); an error means unknown.
func (s *Service) officeOf(ctx context.Context, org, agentID string) (string, error) {
	fn, _ := s.officeFns()
	if fn == nil || agentID == "" {
		return "", nil
	}
	return fn(ctx, org, agentID)
}

// office is the office of the agent who READS: unknown counts as none, so it sees no office documents.
func (s *Service) office(ctx context.Context, org, agentID string) string {
	off, err := s.officeOf(ctx, org, agentID)
	if err != nil {
		return ""
	}
	return off
}

// readOffice is the office whose memory the agent READS for the person it works for (application.PersonFrom):
// its office, except a private one when that person is not a member of it (I6). Without a person (the agent's own
// work) it is the agent's office. Anything that cannot be told counts as no office.
func (s *Service) readOffice(ctx context.Context, org, agentID string) string {
	off := s.office(ctx, org, agentID)
	person := application.PersonFrom(ctx)
	if off == "" || person == "" {
		return off
	}
	s.offMu.RLock()
	sealed, member := s.cfg.OfficeSealed, s.cfg.OfficeMember
	s.offMu.RUnlock()
	if sealed == nil {
		return ""
	}
	priv, err := sealed(ctx, org, off)
	if err != nil {
		return ""
	}
	if !priv {
		return off
	}
	if member == nil {
		return ""
	}
	if ok, err := member(ctx, org, off, person); err != nil || !ok {
		return ""
	}
	return off
}

// assistantAgent is the planner/synthesizer that signs every report next to the agents that worked on it.
const assistantAgent = "assistant"

// reportOffice decides where a report is remembered from the offices of its contributors: all of one office
// (not General) -> that office; only General's or open offices -> the organization; anything involving a private
// office with other authors -> ok=false (not remembered). An office that cannot be determined now (the offices model
// failed) -> err: the caller leaves what is stored as it is and does not index, so a passing failure never forgets
// a report for good.
func (s *Service) reportOffice(ctx context.Context, org string, r domain.Report) (office string, ok bool, err error) {
	offs, general := map[string]bool{}, false
	for _, a := range r.Contributors {
		if a == assistantAgent || a == "" {
			continue
		}
		off, err := s.officeOf(ctx, org, a)
		if err != nil {
			return "", false, err
		}
		if off == "" {
			general = true
		} else {
			offs[off] = true
		}
	}
	switch {
	case len(offs) == 0:
		return "", true, nil
	case len(offs) == 1 && !general:
		for o := range offs {
			return o, true, nil
		}
	}
	_, sealed := s.officeFns()
	if sealed == nil {
		return "", false, errNoOfficeRule
	}
	for o := range offs {
		priv, err := sealed(ctx, org, o)
		if err != nil {
			return "", false, err
		}
		if priv {
			return "", false, nil
		}
	}
	return "", true, nil
}

// errNoOfficeRule: the office scope is not installed, so whether a mixed report involves a private office is unknown.
var errNoOfficeRule = errors.New("memory: office visibility rule not installed")

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
	off, err := s.officeOf(ctx, org, t.AgentID)
	if err != nil { // fail closed: an unknown office must not turn department work into organization memory
		s.cfg.Log.Warn("memory: office of agent, task not indexed", "task", t.ID, "err", err)
		return
	}
	if off != "" { // D3: what a department learns stays in the department
		d.Scope, d.OfficeID = ScopeOffice, off
	}
	docs := []*Doc{&d}
	for i, n := range s.CleanNotes(t.Output.MemoryNotes) {
		docs = append(docs, &Doc{ID: fmt.Sprintf("note_%s_%d", t.ID, i+1), Kind: KindNote, Ref: fmt.Sprintf("memory:%s.%d", t.ID, i+1), Title: capRunes(n, 120), Body: n,
			AgentID: t.AgentID, Scope: ScopeAgent, ProjectID: d.ProjectID, ProjectName: d.ProjectName, RequestID: t.RequestID,
			SourceRef: application.TaskRef(t.ID), CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt})
	}
	s.tagCustomer(ctx, org, t.RequestID, docs...)
	s.embedDocs(ctx, org, docs...) // one provider call for the task and its notes
	for i, doc := range docs {
		if err := s.cfg.Docs.Upsert(ctx, org, *doc); err != nil {
			if i == 0 {
				s.cfg.Log.Warn("memory: index task", "task", t.ID, "err", err)
			} else {
				s.cfg.Log.Warn("memory: store note", "task", t.ID, "err", err)
			}
		}
	}
}

// RememberReport implements application.MemorySource.
func (s *Service) RememberReport(ctx context.Context, org string, r domain.Report) {
	off, ok, err := s.reportOffice(ctx, org, r)
	if err != nil { // not now: what is stored stays, the next backfill retries
		s.cfg.Log.Warn("memory: report office unknown, not indexed", "report", r.ID, "err", err)
		return
	}
	if !ok {
		s.cfg.Log.Warn("memory: report of several offices with a private one, not indexed", "report", r.ID)
		s.forgetReport(ctx, org, r.ID) // I4: one remembered before D3 as organization memory is taken out too
		return
	}
	d := ReportDoc(r)
	if off != "" {
		d.Scope, d.OfficeID = ScopeOffice, off
	}
	d.Body, d.Title = s.scrub(d.Body), s.scrub(d.Title)
	d.ProjectID, d.ProjectName = s.project(ctx, org, r.RequestID)
	s.tagCustomer(ctx, org, r.RequestID, &d)
	s.embedDocs(ctx, org, &d)
	if err := s.cfg.Docs.Upsert(ctx, org, d); err != nil {
		s.cfg.Log.Warn("memory: index report", "report", r.ID, "err", err)
	}
}

// forgetReport suppresses the document of a report that must not be remembered (it mixes a private office with
// other authors, or its office cannot be told) when one was written before (as organization memory, before D3).
func (s *Service) forgetReport(ctx context.Context, org, reportID string) {
	d, err := s.cfg.Docs.GetByRef(ctx, org, "report:"+reportID)
	if err != nil || d.Suppressed {
		return
	}
	if err := s.cfg.Docs.Suppress(ctx, org, d.ID, sealActor, s.cfg.Now().UTC()); err != nil {
		s.cfg.Log.Warn("memory: suppress a mixed report", "report", reportID, "err", err)
	}
}

// sealActor is who suppresses or moves documents to seal the departments (D3), never a person.
const sealActor = "system:d3"

// resealReport fixes the existing organization document of a report the backfill does
// not re-read (beyond its cap): suppressed when the report must not be remembered, moved to its office (re-indexed) when all
// its authors are of one office; left alone when the offices cannot be read now. It reports whether it changed something.
func (s *Service) resealReport(ctx context.Context, org string, r domain.Report) bool {
	d, err := s.cfg.Docs.GetByRef(ctx, org, "report:"+r.ID)
	if err != nil || d.Suppressed || d.Scope != ScopeOrg {
		return false
	}
	off, ok, err := s.reportOffice(ctx, org, r)
	switch {
	case err != nil:
		return false
	case !ok:
		s.forgetReport(ctx, org, r.ID)
		return true
	case off != "":
		s.RememberReport(ctx, org, r)
		return true
	}
	return false
}

// SealOfficeMemory moves the organization memory that the agents of PRIVATE offices wrote before D3 (their task
// documents) to their office, with no cap, and returns how many documents moved. Idempotent; an agent whose office
// cannot be told is left as it is (and the error is returned). Stores that cannot do it move nothing.
func (s *Service) SealOfficeMemory(ctx context.Context, org string) (int, error) {
	rt, ok := s.cfg.Docs.(OfficeRetagger)
	agentOffice, sealed := s.officeFns()
	if !ok || agentOffice == nil || sealed == nil {
		return 0, nil
	}
	agents, err := rt.OrgScopeAgents(ctx, org)
	if err != nil {
		return 0, err
	}
	byOffice := map[string][]string{}
	var errs []error
	for _, a := range agents {
		off, err := agentOffice(ctx, org, a)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if off == "" {
			continue
		}
		priv, err := sealed(ctx, org, off)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if priv {
			byOffice[off] = append(byOffice[off], a)
		}
	}
	n := 0
	for off, as := range byOffice {
		m, err := rt.RetagOffice(ctx, org, off, as)
		n += m
		if err != nil {
			errs = append(errs, err)
		}
	}
	return n, errors.Join(errs...)
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
	for i, r := range reports {
		if maxReports > 0 && i >= maxReports {
			// I4: beyond the cap, only the organization documents that D3 seals (cheap: no re-index, no embedding)
			if s.resealReport(ctx, org, r) {
				n++
			}
			continue
		}
		s.RememberReport(ctx, org, r)
		n++
	}
	s.EmbedPending(ctx, org, 500) // vectors the cap or the provider refused earlier
	return n
}

// ---- retrieval ----

func (s *Service) retrieve(ctx context.Context, org string, q Query) []Item {
	// B12: the customer of the query and of ctx are the same thing (documents and drive files).
	if q.CustomerID == "" {
		q.CustomerID = application.CustomerFrom(ctx)
	}
	ctx = application.WithCustomer(ctx, q.CustomerID)
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
	if strings.TrimSpace(q.Text) == "" {
		return nil
	}
	var out []application.RecalledMemory
	budget := MaxChars
	add := func(m application.RecalledMemory) bool {
		cost := len([]rune(m.Title)) + len([]rune(m.Snippet))
		if len(out) >= MaxItems || cost > budget && len(out) > 0 {
			return false
		}
		budget -= cost
		out = append(out, m)
		return true
	}
	// E2: notes people wrote (the office's, the person's, the company's) are instructions like /recordar, never
	// customer data, so they also reach customer-scoped tasks
	s.offMu.RLock()
	extra := s.cfg.ExtraRecall
	s.offMu.RUnlock()
	if extra != nil {
		for _, m := range extra(ctx, org, q.AgentID, q.RequestID, q.Text) {
			m.Title, m.Snippet = Clean(m.Title, s.cfg.Suspects), Clean(m.Snippet, s.cfg.Suspects)
			if len([]rune(m.Title))+len([]rune(m.Snippet)) > budget {
				continue // too big for what is left: the next ones may fit
			}
			add(m)
		}
	}
	if s.cfg.Retriever == nil {
		return out
	}
	cid, err := s.customer(ctx, org, q.RequestID)
	if err != nil {
		s.cfg.Log.Warn("memory: customer of request, recalling nothing", "request", q.RequestID, "err", err)
		return out // fail closed: an unknown scope must not see every customer's work
	}
	s.topUp(ctx, org)
	pid, _ := s.project(ctx, org, q.RequestID)
	query := Query{AgentID: q.AgentID, OfficeID: s.readOffice(ctx, org, q.AgentID), Text: q.Text, Limit: MaxItems * 2, ExcludeRequest: q.RequestID, PreferProject: pid, CustomerID: cid}
	if pid != "" && s.cfg.ProjectPath != nil {
		query.PreferPath = s.cfg.ProjectPath(ctx, org, pid)
	}
	for _, it := range s.retrieve(ctx, org, query) {
		if !add(application.RecalledMemory{Ref: it.Ref, Kind: it.Kind, Title: it.Title, Snippet: it.Snippet, Project: it.Project, AgentID: it.AgentID, At: it.At}) {
			break
		}
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
	items := s.retrieve(ctx, org, Query{AgentID: agentID, OfficeID: s.readOffice(ctx, org, agentID), Text: query, Limit: limit})
	if items == nil {
		items = []Item{}
	}
	return items, nil
}

// Open returns the text of one document by its ref (task:, report:, memory:),
// when the agent may see it. Files are opened with fs.read.
func (s *Service) Open(ctx context.Context, org, agentID, ref string) (Item, error) {
	d, err := s.cfg.Docs.GetByRef(ctx, org, ref)
	if err != nil || d.Suppressed || d.Scope == ScopeAgent && d.AgentID != agentID || d.Scope == ScopeOffice && (d.OfficeID == "" || d.OfficeID != s.readOffice(ctx, org, agentID)) || !VisibleToCustomer(d, application.CustomerFrom(ctx)) {
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

// Doc returns one stored document (suppressed ones are not found).
func (s *Service) Doc(ctx context.Context, org, id string) (Doc, error) {
	d, err := s.cfg.Docs.Get(ctx, org, id)
	if err != nil || d.Suppressed {
		return Doc{}, ErrNotFound
	}
	return d, nil
}

// RetagCustomer moves documents between customer tags (B12, an explicit and audited decision of a person taken in
// internal/customers; never automatic). It fails when the store cannot do it.
func (s *Service) RetagCustomer(ctx context.Context, org string, sel RetagSelector, from, to string) (int, error) {
	rt, ok := s.cfg.Docs.(CustomerRetagger)
	if !ok {
		return 0, fmt.Errorf("%w: this memory store cannot change customer tags", domain.ErrInvalid)
	}
	return rt.RetagCustomer(ctx, org, sel, from, to)
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
	id := fmt.Sprintf("%s%s_%d", UserNotePrefix, agentID, now.UnixNano())
	d := Doc{ID: id, Kind: KindNote, Ref: "memory:" + id, Title: capRunes(n, 120), Body: n,
		AgentID: agentID, Scope: ScopeAgent, CreatedAt: now, UpdatedAt: now}
	s.embedDocs(ctx, org, &d)
	return s.cfg.Docs.Upsert(ctx, org, d)
}

// SetOfficeScope installs Config.AgentOffice and Config.OfficeSealed after construction (safe at any time; the
// server installs them before the backfill starts, so no document is ever written without them).
func (s *Service) SetOfficeScope(agentOffice func(ctx context.Context, org, agentID string) (string, error), sealed func(ctx context.Context, org, office string) (bool, error)) {
	s.offMu.Lock()
	defer s.offMu.Unlock()
	s.cfg.AgentOffice, s.cfg.OfficeSealed = agentOffice, sealed
}

// SetOfficeMembers installs Config.OfficeMember after construction (safe at any time).
func (s *Service) SetOfficeMembers(fn func(ctx context.Context, org, office, userID string) (bool, error)) {
	s.offMu.Lock()
	defer s.offMu.Unlock()
	s.cfg.OfficeMember = fn
}

// SetExtraRecall installs Config.ExtraRecall after construction (the offices are wired after the memory). Safe at
// any time, like SetOfficeScope.
func (s *Service) SetExtraRecall(fn func(ctx context.Context, org, agentID, requestID, text string) []application.RecalledMemory) {
	s.offMu.Lock()
	defer s.offMu.Unlock()
	s.cfg.ExtraRecall = fn
}
