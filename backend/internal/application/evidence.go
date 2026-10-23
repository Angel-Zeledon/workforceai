package application

import (
	"context"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"aiworkforce/backend/internal/domain"
)

// B6 evidence chain (docs/architecture/evidence-chain.md). An agent may attach
// citations to the figures of its output ({value, label, ref}). The backend
// never takes a citation on trust: every ref is checked against what THIS task
// really saw (its dependencies and project index, the memories recalled for
// it, the files and connector items its reads returned, the artifacts handed
// to it as context) and only then gets status "verified" and a resolved
// source. Anything else is kept as "unverified" with a reason and no source,
// so the UI never makes it clickable. Whatever status/source the runtime sends
// is discarded.

// Bounds of what the backend keeps from the runtime.
const (
	maxCitations     = 30
	maxCitationValue = 40
	maxCitationLabel = 120
	maxCitationRef   = 300
)

// Citation kinds (the prefix of the ref).
const (
	CiteTask   = "task"
	CiteReport = "report"
	CiteMemory = "memory"
	CiteFile   = "file"
	CiteCell   = "cell"
	CiteConn   = "conn"
)

// Reasons of an unverified citation.
const (
	ReasonMalformed     = "malformed"
	ReasonUnknownKind   = "unknown_kind"
	ReasonNotSeen       = "not_seen"
	ReasonNotFound      = "not_found"
	ReasonValueMismatch = "value_mismatch"
)

// CitationResolver reads one cell of an artifact for the evidence chain. It
// must re-check that the agent may read the artifact (or that it belongs to the
// request). ok=false: no such artifact/sheet/cell, or not readable.
type CitationResolver interface {
	ResolveCell(ctx context.Context, org, agentID, requestID, artifactID, sheet, cell string) (value string, ok bool)
}

// SetCitationResolver installs the artifact cell reader (nil: cell citations
// can never be verified).
func (o *Orchestrator) SetCitationResolver(r CitationResolver) { o.citeRes = r }

// taskSeen is what the reads of one task returned (ids only).
type taskSeen struct {
	conn   map[string]seenItem // tool + "\x00" + item id
	files  []seenItem
	recall map[string]string // ref -> title
}

type seenItem struct {
	src ReadSource
	at  time.Time
}

// recordSources remembers the ids a successful read of a task returned.
func (r *run) recordSources(taskID string, srcs []ReadSource, at time.Time) {
	if len(srcs) == 0 {
		return
	}
	r.ext.mu.Lock()
	defer r.ext.mu.Unlock()
	if r.ext.seen == nil {
		r.ext.seen = map[string]*taskSeen{}
	}
	s := r.ext.seen[taskID]
	if s == nil {
		s = &taskSeen{conn: map[string]seenItem{}, recall: map[string]string{}}
		r.ext.seen[taskID] = s
	}
	for _, src := range srcs {
		switch src.Kind {
		case SourceConn:
			if src.ItemID != "" && len(s.conn) < 500 {
				s.conn[strings.ToLower(src.Tool)+"\x00"+src.ItemID] = seenItem{src: src, at: at}
			}
		case SourceFile:
			if src.FileID != "" && len(s.files) < 200 {
				s.files = append(s.files, seenItem{src: src, at: at})
			}
		case SourceRecall:
			if src.Ref != "" && len(s.recall) < 200 {
				s.recall[src.Ref] = src.Title
			}
		}
	}
}

// seenOf returns a copy of what a task read (never nil).
func (r *run) seenOf(taskID string) taskSeen {
	r.ext.mu.Lock()
	defer r.ext.mu.Unlock()
	out := taskSeen{conn: map[string]seenItem{}, recall: map[string]string{}}
	if s := r.ext.seen[taskID]; s != nil {
		for k, v := range s.conn {
			out.conn[k] = v
		}
		out.files = append(out.files, s.files...)
		for k, v := range s.recall {
			out.recall[k] = v
		}
	}
	return out
}

// checkCitations validates (in place) the citations of a fresh runtime output.
func (o *Orchestrator) checkCitations(ctx context.Context, rs *run, t domain.Task, in RunTaskRequest, out *domain.StructuredOutput) {
	if out == nil || len(out.Citations) == 0 {
		return
	}
	v := citationCheck{o: o, ctx: ctx, rs: rs, t: t, in: in, seen: rs.seenOf(t.ID)}
	raw := out.Citations
	dropped := 0
	if len(raw) > maxCitations {
		dropped = len(raw) - maxCitations
		raw = raw[:maxCitations]
	}
	res := make([]domain.Citation, 0, len(raw))
	verified, reasons := 0, map[string]int{}
	for _, c := range raw {
		nc, keep := v.check(c)
		if !keep {
			dropped++
			continue
		}
		if nc.Status == domain.CitationVerified {
			verified++
		} else {
			reasons[nc.Reason]++
		}
		res = append(res, nc)
	}
	out.Citations = res
	if len(res) == 0 {
		out.Citations = nil
	}
	details := map[string]any{"agent_id": t.AgentID, "verified": verified, "unverified": len(res) - verified, "dropped": dropped}
	if len(reasons) > 0 {
		details["reasons"] = reasons
	}
	o.rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "evidence.citations_checked", Entity: "task", EntityID: t.ID, Details: details})
}

type citationCheck struct {
	o    *Orchestrator
	ctx  context.Context
	rs   *run
	t    domain.Task
	in   RunTaskRequest
	seen taskSeen
}

// check returns the cleaned citation with its verdict; keep=false drops it
// (no figure at all: nothing to show).
func (v citationCheck) check(c domain.Citation) (domain.Citation, bool) {
	nc := domain.Citation{Value: oneLine(c.Value, maxCitationValue), Label: oneLine(c.Label, maxCitationLabel), Ref: strings.TrimSpace(c.Ref)}
	if nc.Value == "" || !strings.ContainsFunc(nc.Value, unicode.IsDigit) {
		return nc, false
	}
	unverified := func(reason string) (domain.Citation, bool) {
		nc.Status, nc.Reason, nc.Source, nc.ValueChecked = domain.CitationUnverified, reason, nil, false
		return nc, true
	}
	if nc.Ref == "" || len(nc.Ref) > maxCitationRef || strings.ContainsFunc(nc.Ref, unicode.IsControl) {
		nc.Ref = oneLine(nc.Ref, maxCitationRef)
		return unverified(ReasonMalformed)
	}
	kind, rest, ok := strings.Cut(nc.Ref, ":")
	kind = strings.ToLower(strings.TrimSpace(kind))
	rest = strings.TrimSpace(rest)
	if !ok || rest == "" {
		return unverified(ReasonMalformed)
	}
	nc.Kind = kind
	var src *domain.CitationSource
	reason := ""
	switch kind {
	case CiteTask:
		src, nc.ValueChecked, reason = v.task(rest, nc.Value)
	case CiteReport, CiteMemory:
		src, nc.ValueChecked, reason = v.recall(kind+":"+rest, nc.Value)
	case CiteFile:
		src, nc.ValueChecked, reason = v.file(rest, nc.Value)
	case CiteCell:
		src, reason = v.cell(rest, nc.Value)
		nc.ValueChecked = reason == ""
	case CiteConn:
		src, reason = v.conn(rest)
	default:
		nc.Kind = ""
		return unverified(ReasonUnknownKind)
	}
	if reason != "" {
		return unverified(reason)
	}
	nc.Status, nc.Source = domain.CitationVerified, src
	return nc, true
}

// task: a dependency output, a task of the project index or a recalled task.
func (v citationCheck) task(id, value string) (*domain.CitationSource, bool, string) {
	for _, d := range v.in.Context.DependencyOutputs {
		if d.TaskID == id {
			return &domain.CitationSource{TaskID: id, Title: d.Title, AgentID: d.AgentID}, containsFigure(outputText(d.Output), value), ""
		}
	}
	if pc := v.in.Context.ProjectContext; pc != nil {
		for _, e := range pc.Index {
			if e.TaskID == id {
				return &domain.CitationSource{TaskID: id, Title: e.Title, AgentID: e.AgentID}, containsFigure(e.Title+"\n"+e.Summary, value), ""
			}
		}
	}
	if src, checked, reason := v.recall(CiteTask+":"+id, value); reason == "" {
		src.TaskID = id
		return src, checked, ""
	}
	return nil, false, ReasonNotSeen
}

// recall: a memory handed to the task (Recalled) or returned by memory.search.
func (v citationCheck) recall(ref, value string) (*domain.CitationSource, bool, string) {
	for _, m := range v.in.Recalled {
		if m.Ref == ref {
			return &domain.CitationSource{Title: m.Title, AgentID: m.AgentID}, containsFigure(m.Title+"\n"+m.Snippet, value), ""
		}
	}
	if title, ok := v.seen.recall[ref]; ok {
		return &domain.CitationSource{Title: title}, false, ""
	}
	return nil, false, ReasonNotSeen
}

// file: a drive file the task read with fs.read (by path or file id), or a
// recalled file memory ("file:/path").
func (v citationCheck) file(rest, value string) (*domain.CitationSource, bool, string) {
	var best *seenItem
	for i := range v.seen.files {
		f := v.seen.files[i]
		if f.src.FileID == rest || f.src.Path == rest {
			if best == nil || f.src.Version > best.src.Version {
				best = &f
			}
		}
	}
	if best != nil {
		at := best.at
		return &domain.CitationSource{FileID: best.src.FileID, Path: best.src.Path, Version: best.src.Version, ReadAt: &at}, false, ""
	}
	if strings.HasPrefix(rest, "/") {
		if src, checked, reason := v.recall(CiteFile+":"+rest, value); reason == "" {
			src.Path = rest
			return src, checked, ""
		}
	}
	return nil, false, ReasonNotSeen
}

// cell: "<artifact id>!<sheet>!<A1>" of an artifact handed to the task as
// context; the cell must exist and hold the cited figure.
func (v citationCheck) cell(rest, value string) (*domain.CitationSource, string) {
	first, last := strings.Index(rest, "!"), strings.LastIndex(rest, "!")
	if first <= 0 || last <= first || last == len(rest)-1 {
		return nil, ReasonMalformed
	}
	art, sheet, cell := rest[:first], rest[first+1:last], strings.ToUpper(rest[last+1:])
	if !a1Re.MatchString(cell) {
		return nil, ReasonMalformed
	}
	seen := false
	if pc := v.in.Context.ProjectContext; pc != nil {
		for _, a := range pc.Artifacts {
			if a.ID == art {
				seen = true
				break
			}
		}
	}
	if !seen {
		return nil, ReasonNotSeen
	}
	if v.o.citeRes == nil {
		return nil, ReasonNotFound
	}
	got, ok := v.o.citeRes.ResolveCell(v.ctx, v.o.org(v.ctx), v.t.AgentID, v.t.RequestID, art, sheet, cell)
	if !ok {
		return nil, ReasonNotFound
	}
	if !sameFigure(got, value) {
		return nil, ReasonValueMismatch
	}
	return &domain.CitationSource{ArtifactID: art, Sheet: sheet, Cell: cell, CellValue: oneLine(got, 80)}, ""
}

// conn: "<tool>:<item id>" of an item a connector read of this task returned.
func (v citationCheck) conn(rest string) (*domain.CitationSource, string) {
	tool, id, ok := strings.Cut(rest, ":")
	tool, id = strings.ToLower(strings.TrimSpace(tool)), strings.TrimSpace(id)
	if !ok || tool == "" || id == "" {
		return nil, ReasonMalformed
	}
	it, found := v.seen.conn[tool+"\x00"+id]
	if !found {
		return nil, ReasonNotSeen
	}
	at := it.at
	return &domain.CitationSource{Provider: it.src.Provider, Tool: it.src.Tool, ItemID: it.src.ItemID, ReadAt: &at}, ""
}

var a1Re = regexp.MustCompile(`^[A-Z]{1,3}[1-9][0-9]{0,5}$`)

// oneLine trims, folds whitespace and bounds a runtime string.
func oneLine(s string, n int) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return s
}

// outputText is the text of an output where its figures live.
func outputText(o domain.StructuredOutput) string {
	var b strings.Builder
	b.WriteString(o.Summary)
	for _, g := range [][]string{o.Findings, o.Evidence, o.Recommendations, o.Hypotheses} {
		for _, s := range g {
			b.WriteString("\n")
			b.WriteString(s)
		}
	}
	for k, m := range o.Metrics {
		b.WriteString("\n" + k + ": ")
		switch x := m.(type) {
		case string:
			b.WriteString(x)
		case float64:
			b.WriteString(strconv.FormatFloat(x, 'f', -1, 64))
		case int:
			b.WriteString(strconv.Itoa(x))
		}
	}
	return b.String()
}

// ---- figures ----

var figureRe = regexp.MustCompile(`[-−]?\d[\d.,]*\d|[-−]?\d`)

type figure struct {
	n   float64
	pct bool
}

// figures extracts the numbers of a text ("$50,000", "24 %", "1.200.000,50").
func figures(s string) []figure {
	var out []figure
	for _, loc := range figureRe.FindAllStringIndex(s, 50) {
		tok := s[loc[0]:loc[1]]
		if r, size := utf8.DecodeLastRuneInString(s[:loc[0]]); size > 0 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			tok = strings.TrimLeft(tok, "-−") // "3-4 weeks": a range, not a negative number
		}
		f, ok := parseNumber(tok)
		if !ok {
			continue
		}
		tail := strings.TrimLeft(s[loc[1]:], " ")
		f.pct = strings.HasPrefix(tail, "%")
		out = append(out, f)
	}
	return out
}

// parseNumber reads one numeric token with either decimal convention: when both
// separators appear the last one is the decimal mark; a lone separator followed
// by exactly three digits (or repeated) groups thousands.
func parseNumber(tok string) (figure, bool) {
	neg := strings.HasPrefix(tok, "-") || strings.HasPrefix(tok, "−")
	tok = strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) || r == '.' || r == ',' {
			return r
		}
		return -1
	}, tok)
	if tok == "" {
		return figure{}, false
	}
	lastDot, lastComma := strings.LastIndex(tok, "."), strings.LastIndex(tok, ",")
	dec := -1
	switch {
	case lastDot >= 0 && lastComma >= 0:
		dec = max(lastDot, lastComma)
	case lastDot >= 0 || lastComma >= 0:
		sep := byte('.')
		pos := lastDot
		if lastComma >= 0 {
			sep, pos = ',', lastComma
		}
		if strings.Count(tok, string(sep)) == 1 && len(tok)-pos-1 != 3 {
			dec = pos
		}
	}
	var b strings.Builder
	for i := 0; i < len(tok); i++ {
		switch {
		case i == dec:
			b.WriteByte('.')
		case tok[i] == '.' || tok[i] == ',':
		default:
			b.WriteByte(tok[i])
		}
	}
	n, err := strconv.ParseFloat(b.String(), 64)
	if err != nil {
		return figure{}, false
	}
	if neg {
		n = -n
	}
	return figure{n: n}, true
}

func near(a, b float64) bool {
	return math.Abs(a-b) <= 1e-6*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// matches compares a cited figure with one found in a source; a percentage also
// matches its fraction (24% = 0.24).
func (c figure) matches(s figure) bool {
	if near(c.n, s.n) {
		return true
	}
	return c.pct && !s.pct && near(c.n/100, s.n)
}

// containsFigure reports whether the main number of value appears in text.
func containsFigure(text, value string) bool {
	vs := figures(value)
	if len(vs) == 0 {
		return false
	}
	for _, f := range figures(text) {
		if vs[0].matches(f) {
			return true
		}
	}
	return false
}

// sameFigure compares a cell value with the cited figure (numbers by value,
// anything else by text).
func sameFigure(cell, value string) bool {
	vs, cs := figures(value), figures(cell)
	if len(vs) > 0 && len(cs) > 0 {
		return vs[0].matches(cs[0])
	}
	return strings.EqualFold(strings.TrimSpace(cell), strings.TrimSpace(value))
}

// ---- read model ----

// ReportCitation is a citation of one task of a report's request.
type ReportCitation struct {
	domain.Citation
	TaskID  string `json:"task_id"`
	AgentID string `json:"agent_id"`
}

// ReportEvidence is GET /reports/{id}/evidence: the checked citations of the
// tasks behind a report (verified first). The report text itself is the
// synthesis; the UI links the figures of it that match a verified citation.
type ReportEvidence struct {
	ReportID  string           `json:"report_id"`
	RequestID string           `json:"request_id"`
	Citations []ReportCitation `json:"citations"`
	Verified  int              `json:"verified"`
	Total     int              `json:"total"`
}

// ReportEvidence collects the citations of the tasks of a report's request.
func (q *Queries) ReportEvidence(ctx context.Context, reportID string) (ReportEvidence, error) {
	r, err := q.Store.GetReport(ctx, q.org(ctx), reportID)
	if err != nil {
		return ReportEvidence{}, err
	}
	tasks, err := q.Store.ListTasksByRequest(ctx, q.org(ctx), r.RequestID)
	if err != nil {
		return ReportEvidence{}, err
	}
	ev := ReportEvidence{ReportID: r.ID, RequestID: r.RequestID, Citations: []ReportCitation{}}
	var unverified []ReportCitation
	for _, t := range tasks {
		if t.Output == nil {
			continue
		}
		for _, c := range t.Output.Citations {
			rc := ReportCitation{Citation: c, TaskID: t.ID, AgentID: t.AgentID}
			if c.Status == domain.CitationVerified {
				ev.Citations = append(ev.Citations, rc)
				ev.Verified++
			} else {
				rc.Source = nil // never clickable
				unverified = append(unverified, rc)
			}
		}
	}
	ev.Citations = append(ev.Citations, unverified...)
	ev.Total = len(ev.Citations)
	return ev, nil
}
