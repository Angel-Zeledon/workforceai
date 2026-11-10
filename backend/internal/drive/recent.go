package drive

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Recent files (docs/superpowers/specs/2026-11-03-agentes-ubican-archivos-y-accesos-design.md, part 1): when a person
// mentions files without a path ("the invoices of the month", "the contract I uploaded yesterday") the agent looks first
// at what THAT person uploaded or moved in the last days, by type and folder, before searching by words or meaning.
// The list is filtered by the agent's folder access on every call; files the person touched in folders the agent cannot
// see are reported only as "N files in <folder>, no access" (never names nor content), so the agent says so and offers
// to give or ask for access instead of skipping them in silence.

// ActRecent is the fs action that lists recent files.
const ActRecent = "recent"

// Recent window and bounds.
const (
	DefaultRecentDays = 14
	MaxRecentDays     = 90
	DefaultRecentList = 50
	MaxRecentList     = 200
	maxRecentScan     = 500
)

// RecentQuery selects recently touched files.
type RecentQuery struct {
	// By (optional) keeps the files this principal created, or changed last (an upload, a new version, a move), inside
	// the window; nil = anybody.
	By *Principal
	// Since is the start of the window.
	Since time.Time
	// Under is the scope (InScope: nil = everything, "!" entries exclude).
	Under []string
	// Exts keeps names ending in one of these extensions (lower case, without the dot); empty = any.
	Exts  []string
	Limit int
}

// recentKinds maps the kind argument to extensions.
var recentKinds = map[string][]string{
	"pdf":   {"pdf"},
	"sheet": {"xlsx", "xls", "csv"}, "xlsx": {"xlsx", "xls"}, "excel": {"xlsx", "xls"}, "csv": {"csv"},
	"doc": {"docx", "doc"}, "docx": {"docx", "doc"}, "word": {"docx", "doc"},
	"image":  {"png", "jpg", "jpeg", "gif", "webp"},
	"text":   {"md", "txt"},
	"slides": {"pptx"}, "pptx": {"pptx"},
}

// recentExts turns the kind argument into extensions (ok=false: unknown kind).
func recentExts(kind string) ([]string, bool) {
	k := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(kind), ".")))
	if k == "" || k == "any" || k == "all" {
		return nil, true
	}
	if e, ok := recentKinds[k]; ok {
		return e, true
	}
	if len(k) <= 5 && strings.IndexFunc(k, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') }) < 0 {
		return []string{k}, true // a plain extension ("xml")
	}
	return nil, false
}

// HasExt reports whether a file name ends in one of the extensions (empty = any).
func HasExt(name string, exts []string) bool {
	if len(exts) == 0 {
		return true
	}
	n := strings.ToLower(name)
	for _, e := range exts {
		if strings.HasSuffix(n, "."+e) {
			return true
		}
	}
	return false
}

// TouchedBy reports whether the file is in the window of q: p created it at or after since, or changed it last at or
// after since (p nil: anybody changed it in the window).
func TouchedBy(f File, p *Principal, since time.Time) bool {
	if p == nil {
		return !f.UpdatedAt.Before(since)
	}
	return f.UpdatedBy == *p && !f.UpdatedAt.Before(since) || f.CreatedBy == *p && !f.CreatedAt.Before(since)
}

// Recent lists the live files touched in the window, newest first (people: every folder; agents use fs.recent).
func (s *Service) Recent(ctx context.Context, q RecentQuery) ([]File, error) {
	if q.Limit <= 0 || q.Limit > maxRecentScan {
		q.Limit = maxRecentScan
	}
	if q.Under != nil && len(q.Under) == 0 {
		return []File{}, nil
	}
	fs, err := s.cfg.Store.RecentFiles(ctx, s.org(ctx), q)
	if fs == nil {
		fs = []File{}
	}
	return fs, err
}

type requesterKey struct{}

// WithRequester marks ctx with the person on whose behalf an agent works (GatewayCall.OnBehalfOf): fs.recent lists
// what that person uploaded or moved. "" leaves ctx unchanged.
func WithRequester(ctx context.Context, userID string) context.Context {
	if userID = strings.TrimSpace(userID); userID == "" {
		return ctx
	}
	return context.WithValue(ctx, requesterKey{}, userID)
}

func requesterFrom(ctx context.Context) string {
	s, _ := ctx.Value(requesterKey{}).(string)
	return s
}

type recentFolder struct {
	path          string
	files         []File
	kinds         map[string]int
	first, latest time.Time
}

func groupByFolder(fs []File) []*recentFolder {
	by := map[string]*recentFolder{}
	var out []*recentFolder
	for _, f := range fs {
		dir := parentPath(f.Path)
		g := by[strings.ToLower(dir)]
		if g == nil {
			g = &recentFolder{path: dir, kinds: map[string]int{}, first: f.UpdatedAt, latest: f.UpdatedAt}
			by[strings.ToLower(dir)] = g
			out = append(out, g)
		}
		g.files = append(g.files, f)
		g.kinds[recentExt(f.Name)]++
		if f.UpdatedAt.Before(g.first) {
			g.first = f.UpdatedAt
		}
		if f.UpdatedAt.After(g.latest) {
			g.latest = f.UpdatedAt
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].files) != len(out[j].files) {
			return len(out[i].files) > len(out[j].files)
		}
		return out[i].latest.After(out[j].latest)
	})
	return out
}

func parentPath(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return "/"
}

func recentExt(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 && i < len(name)-1 {
		return strings.ToLower(name[i+1:])
	}
	return "(none)"
}

func kindsLine(m map[string]int) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return m[ks[i]] > m[ks[j]] || m[ks[i]] == m[ks[j]] && ks[i] < ks[j] })
	parts := make([]string, 0, len(ks))
	for _, k := range ks {
		parts = append(parts, fmt.Sprintf("%s: %d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

// DeniedFolder is a folder with recent files of the person that the agent cannot see (count only).
type DeniedFolder struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

// runRecent answers fs.recent {days?, by? me|anyone, kind?, path?, limit?}.
func (a *AgentFS) runRecent(ctx context.Context, agentID string, args map[string]any) RunResult {
	org := a.Svc.org(ctx)
	sc, err := a.scopeOf(ctx, agentID)
	if err != nil {
		return failure(err)
	}
	days, ok := argInt(args, "days")
	if _, given := args["days"]; given && args["days"] != nil && (!ok || days < 1) {
		return RunResult{Code: "invalid_args", Summary: "invalid_args: days must be a positive integer"}
	}
	if days <= 0 {
		days = DefaultRecentDays
	}
	days = min(days, MaxRecentDays)
	exts, ok := recentExts(argStr(args, "kind"))
	if !ok {
		return RunResult{Code: "invalid_args", Summary: "invalid_args: kind must be pdf, sheet, doc, image, text, slides, any or a file extension"}
	}
	lim, _ := argInt(args, "limit")
	if lim <= 0 {
		lim = DefaultRecentList
	}
	lim = min(lim, MaxRecentList)
	var by *Principal
	note := "touched by anybody"
	if person, _ := requesterOf(ctx); !strings.EqualFold(strings.TrimSpace(argStr(args, "by")), "anyone") {
		if person != "" {
			p := Human(person)
			by, note = &p, "uploaded, changed or moved by the person who asked"
		} else {
			note = "touched by anybody (the person who asked is not known)"
		}
	}
	since := a.Svc.now().Add(-time.Duration(days) * 24 * time.Hour)
	under := a.readableRoots(ctx, org, sc)
	if p := argStr(args, "path"); p != "" {
		c, err := CleanPath(p)
		if err != nil {
			return RunResult{Code: "path_invalid", Summary: "path_invalid: " + err.(*Error).Msg}
		}
		if z := a.zoneAll(ctx, org, sc, c); z == ZonePersonDenied {
			return RunResult{Code: denyPersonNoAccess, Summary: denyPersonNoAccess + ": " + personNoAccessMsg}
		} else if !canRead(z) {
			return RunResult{Code: "not_granted", Summary: "not_granted: this folder is outside the areas you can read; tell the person and offer to give you access to it (or to ask for it)"}
		}
		under = scopeUnder(under, c)
	}
	q := RecentQuery{By: by, Since: since, Exts: exts, Under: under, Limit: maxRecentScan}
	files, err := a.Svc.Recent(ctx, q)
	if err != nil {
		return failure(err)
	}
	kept := files[:0:0]
	for _, f := range files { // defense in depth (and B12: a foreign customer's folder under a readable root)
		if canRead(a.zoneAll(ctx, org, sc, f.Path)) {
			kept = append(kept, f)
		}
	}
	files = kept

	// What the same person touched where this agent cannot look: folder and count only, never names nor content.
	// Never in a customer-scoped task (another customer's folder names stay hidden there).
	var denied []DeniedFolder
	if sc.customer == "" && by != nil && argStr(args, "path") == "" {
		q.Under = nil
		if all, err := a.Svc.Recent(ctx, q); err == nil {
			idx := map[string]int{}
			memo := map[string]string{}
			for _, f := range all {
				if z := a.zoneAll(ctx, org, sc, f.Path); canRead(z) || z == ZonePersonDenied {
					continue
				}
				if a.Svc.underOffices(ctx, org, f.Path) && a.Svc.personAccess(ctx, by.ID, f.Path, memo) == AccessNone {
					continue // an office the person cannot see (any more) is never named
				}
				dir := parentPath(f.Path)
				if i, ok := idx[strings.ToLower(dir)]; ok {
					denied[i].Count++
				} else {
					idx[strings.ToLower(dir)] = len(denied)
					denied = append(denied, DeniedFolder{Path: dir, Count: 1})
				}
			}
		}
	}

	groups := groupByFolder(files)
	var b strings.Builder
	fmt.Fprintf(&b, "recent files, last %d day(s), %s", days, note)
	if len(exts) > 0 {
		fmt.Fprintf(&b, ", kind %s", strings.Join(exts, "/"))
	}
	fmt.Fprintf(&b, ": %d file(s) in %d folder(s)\n", len(files), len(groups))
	shown := 0
	folders := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		fmt.Fprintf(&b, "[folder] %s | %d file(s) | %s | from %s to %s\n", g.path, len(g.files), kindsLine(g.kinds),
			g.first.UTC().Format("2006-01-02 15:04"), g.latest.UTC().Format("2006-01-02 15:04"))
		folders = append(folders, map[string]any{"path": g.path, "count": len(g.files)})
		for _, f := range g.files {
			if shown >= lim {
				break
			}
			fmt.Fprintf(&b, "  %s | %s | v%d | %s | by %s:%s\n", f.Name, human(f.Size), f.CurrentVersion, f.UpdatedAt.UTC().Format("2006-01-02 15:04"), f.UpdatedBy.Kind, f.UpdatedBy.ID)
			shown++
		}
	}
	if shown < len(files) {
		fmt.Fprintf(&b, "(%d more not listed: narrow with path or kind)\n", len(files)-shown)
	}
	if len(files) == 0 {
		b.WriteString("(no recent files)\n")
	}
	for _, d := range denied {
		fmt.Fprintf(&b, "no access: %d recent file(s) of this person in %s - you cannot see this folder; say so and offer the person to give you access to it (or to ask for it)\n", d.Count, d.Path)
	}
	others := false
	for _, f := range files {
		others = others || f.UpdatedBy != AgentP(agentID)
	}
	data := map[string]any{"count": len(files), "days": days, "folders": folders}
	if len(denied) > 0 {
		data["no_access"] = denied
	}
	sum := fmt.Sprintf("%d recent file(s) in %d folder(s)", len(files), len(groups))
	if len(denied) > 0 {
		sum += fmt.Sprintf(", %d folder(s) without access", len(denied))
	}
	return RunResult{OK: true, Summary: sum, Text: b.String(), SourceNote: "fs.recent", AuthoredByOthers: others, Data: data}
}
