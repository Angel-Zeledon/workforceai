package drive

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/officedoc"
)

// ProjectRef names a project an agent works on.
type ProjectRef struct {
	ID   string
	Name string
}

// ProjectDirectory tells which projects an agent is assigned to (a launched
// project with at least one node for the agent). Implemented in cmd/server on
// top of the projects store.
type ProjectDirectory interface {
	AgentProjects(ctx context.Context, org, agentID string) ([]ProjectRef, error)
}

// Zone classes of a path, from the point of view of one agent.
const (
	ZoneDesk      = "desk"          // /Agents/<me>/...
	ZoneProject   = "project"       // /Projects/<assigned project>/...
	ZoneShared    = "shared"        // /Shared/...
	ZoneGranted   = "granted"       // a folder a human granted for reading
	ZoneOtherDesk = "other_desk"    // /Agents/<someone else>/...
	ZoneOtherProj = "other_project" // /Projects/<not assigned>/...
	ZoneClients   = "clients"       // /Clients/...
	ZoneOutside   = "outside"       // anything else
)

// AgentFS is the policy layer between agents and the drive. It decides, per
// action and path, whether an agent may act freely, needs a human or is
// refused, and it runs the action as the agent (every write is attributed to
// the agent and is versioned, hence reversible).
//
//	read      own desk, assigned projects, /Shared, granted folders; anything else is refused
//	write     own desk and assigned projects: no approval. Everything else: approval.
//	move      both ends inside the writable areas: no approval. Otherwise approval.
//	delete    always approval (high risk); it is a soft delete, restorable
type AgentFS struct {
	Svc      *Service
	Projects ProjectDirectory
}

// Plan is the verdict about an agent action BEFORE running it (pure: no writes).
type Plan struct {
	// Deny is a stable code when the action is refused outright.
	Deny string
	// DenyMsg is a short English explanation (never user content).
	DenyMsg string
	// Approval is true when a human must approve first.
	Approval bool
	// Flags explain why approval is needed (outside_scope, delete, moves_out_of_scope).
	Flags []string
	// Risk is medium (other writes) or high (delete).
	Risk string
	// Path is the normalized target path.
	Path string
	// Bytes is the size of the content of a write.
	Bytes int
	// Reversibility of the action: versioned | trash.
	Reversibility string
	// data is the built file of a write_document; docErr is why the document
	// could not be built (reported to the agent by Run, which writes nothing).
	data   []byte
	docErr string
}

type scope struct {
	agent    string
	projects map[string]bool
	grants   []string
}

func (a *AgentFS) scopeOf(ctx context.Context, agentID string) (scope, error) {
	org := a.Svc.org(ctx)
	sc := scope{agent: agentID, projects: map[string]bool{}}
	if a.Projects != nil {
		ps, err := a.Projects.AgentProjects(ctx, org, agentID)
		if err != nil {
			return sc, err
		}
		for _, p := range ps {
			sc.projects[p.ID] = true
		}
	}
	gs, err := a.Svc.cfg.Store.Grants(ctx, org, agentID)
	if err != nil {
		return sc, err
	}
	for _, g := range gs {
		sc.grants = append(sc.grants, g.Path)
	}
	return sc, nil
}

// zone classifies a normalized path for the agent.
func (a *AgentFS) zone(ctx context.Context, org string, sc scope, p string) string {
	segs, _ := Segments(p)
	if len(segs) == 0 {
		return ZoneOutside
	}
	switch segs[0] {
	case RootAgents:
		if len(segs) >= 2 && segs[1] == sc.agent {
			return ZoneDesk
		}
		return ZoneOtherDesk
	case RootProjects:
		if len(segs) >= 2 {
			if f, err := a.Svc.cfg.Store.FolderByPath(ctx, org, JoinPath(segs[:2]...)); err == nil && f.RefID != "" && sc.projects[f.RefID] {
				return ZoneProject
			}
		}
		for _, g := range sc.grants {
			if IsUnder(p, g) {
				return ZoneGranted
			}
		}
		return ZoneOtherProj
	case RootShared:
		return ZoneShared
	case RootClients:
		for _, g := range sc.grants {
			if IsUnder(p, g) {
				return ZoneGranted
			}
		}
		return ZoneClients
	}
	return ZoneOutside
}

func (a *AgentFS) grantedAnywhere(sc scope, p string) bool {
	for _, g := range sc.grants {
		if IsUnder(p, g) {
			return true
		}
	}
	return false
}

func canRead(z string) bool {
	return z == ZoneDesk || z == ZoneProject || z == ZoneShared || z == ZoneGranted
}

func autoWrite(z string) bool { return z == ZoneDesk || z == ZoneProject }

// zoneAll also honours grants for zones that are readable by grant (other
// desks, other projects).
func (a *AgentFS) zoneAll(ctx context.Context, org string, sc scope, p string) string {
	z := a.zone(ctx, org, sc, p)
	if (z == ZoneOtherDesk || z == ZoneOtherProj || z == ZoneClients || z == ZoneOutside) && a.grantedAnywhere(sc, p) {
		return ZoneGranted
	}
	return z
}

// ---- argument helpers ----

func argStr(args map[string]any, k string) string {
	s, _ := args[k].(string)
	return s
}

func argInt(args map[string]any, k string) (int, bool) {
	switch v := args[k].(type) {
	case float64:
		return int(v), v == float64(int(v))
	case int:
		return v, true
	case int64:
		return int(v), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		return n, err == nil
	}
	if n, ok := args[k].(interface{ Int64() (int64, error) }); ok {
		x, err := n.Int64()
		return int(x), err == nil
	}
	return 0, false
}

func argTags(args map[string]any) []string {
	var out []string
	switch v := args["tags"].(type) {
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	case []string:
		out = v
	case string:
		for _, t := range strings.Split(v, ",") {
			out = append(out, t)
		}
	default:
		return nil
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func deny(code, msg string) Plan { return Plan{Deny: code, DenyMsg: msg} }

// Actions of the fs tool.
const (
	ActList   = "list"
	ActRead   = "read"
	ActSearch = "search"
	ActWrite  = "write"
	// ActWriteDocument writes a real office document (xlsx, docx, pdf) or a
	// rendered md/csv from a structured document (see internal/officedoc).
	ActWriteDocument = "write_document"
	ActMove          = "move"
	ActDelete        = "delete"
)

// IsWriteAction reports whether the action changes the drive.
func IsWriteAction(action string) bool {
	return action == ActWrite || action == ActWriteDocument || action == ActMove || action == ActDelete
}

// Plan decides what an action needs. It is pure and is evaluated again at
// execution time (an approved call is re-checked for everything but approval).
func (a *AgentFS) Plan(ctx context.Context, agentID, action string, args map[string]any) Plan {
	action = canonicalAction(action, args)
	org := a.Svc.org(ctx)
	sc, err := a.scopeOf(ctx, agentID)
	if err != nil {
		return deny("drive_unavailable", "folder access could not be determined") // fail closed
	}
	switch action {
	case ActList:
		p := argStr(args, "path")
		if p == "" {
			p = "/"
		}
		c, err := CleanPath(p)
		if err != nil {
			return deny("path_invalid", err.(*Error).Msg)
		}
		if len(strings.Split(strings.Trim(c, "/"), "/")) <= 1 { // "/" and the roots are filtered, not refused
			return Plan{Path: c}
		}
		if !canRead(a.zoneAll(ctx, org, sc, c)) {
			return deny("not_granted", "this folder is outside the areas you can read")
		}
		return Plan{Path: c}
	case ActRead:
		return a.planRead(ctx, org, sc, args)
	case ActSearch:
		if strings.TrimSpace(argStr(args, "query")) == "" {
			return deny("invalid_args", "query is required")
		}
		if p := argStr(args, "path"); p != "" {
			c, err := CleanPath(p)
			if err != nil {
				return deny("path_invalid", err.(*Error).Msg)
			}
			if !canRead(a.zoneAll(ctx, org, sc, c)) {
				return deny("not_granted", "this folder is outside the areas you can read")
			}
		}
		return Plan{}
	case ActWrite:
		return a.planWrite(ctx, org, sc, args)
	case ActWriteDocument:
		return a.planWriteDocument(ctx, org, sc, args)
	case ActMove:
		return a.planMove(ctx, org, sc, args)
	case ActDelete:
		return a.planDelete(ctx, org, sc, args)
	}
	return deny("unknown_action", "unknown fs action")
}

// targetFile resolves path or file_id to a file.
func (a *AgentFS) targetFile(ctx context.Context, args map[string]any, key string) (File, error) {
	if id := argStr(args, "file_id"); id != "" {
		return a.Svc.GetFile(ctx, id)
	}
	p := argStr(args, key)
	if p == "" {
		return File{}, invalid("invalid_args", "path or file_id is required")
	}
	return a.Svc.FileByPath(ctx, p)
}

func (a *AgentFS) planRead(ctx context.Context, org string, sc scope, args map[string]any) Plan {
	f, err := a.targetFile(ctx, args, "path")
	if err != nil {
		return planErr(err)
	}
	if f.DeletedAt != nil || !canRead(a.zoneAll(ctx, org, sc, f.Path)) {
		return deny("not_granted", "this file is outside the areas you can read")
	}
	return Plan{Path: f.Path}
}

func planErr(err error) Plan {
	var e *Error
	if errors.As(err, &e) {
		if errors.Is(err, domain.ErrNotFound) {
			return deny("not_found", "no such file")
		}
		return deny(e.Code, e.Msg)
	}
	if errors.Is(err, domain.ErrNotFound) {
		return deny("not_found", "no such file")
	}
	return deny("drive_unavailable", "the drive could not be read")
}

func (a *AgentFS) planWrite(ctx context.Context, org string, sc scope, args map[string]any) Plan {
	p := argStr(args, "path")
	folder, name, err := SplitFile(p)
	if err != nil {
		return deny("path_invalid", err.(*Error).Msg)
	}
	c := folder + "/" + name
	if _, err := CleanName(name); err != nil {
		return deny("name_invalid", err.(*Error).Msg)
	}
	if !IsTextName(name) {
		return deny("unsupported_type", "write takes text files only (md, txt, csv, json, code); for xlsx, docx or pdf use write_document with a structured document")
	}
	content, ok := args["content"].(string)
	if !ok || content == "" {
		return deny("invalid_args", "content is required")
	}
	lim := a.Svc.cfg.Limits
	if int64(len(content)) > lim.MaxFileBytes {
		return deny("file_too_large", fmt.Sprintf("the file exceeds the limit of %d MB", lim.MaxFileBytes>>20))
	}
	if !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return deny("content_mismatch", "content must be valid text")
	}
	if tags := argTags(args); tags != nil {
		if _, err := CleanTags(tags); err != nil {
			return deny("tags_invalid", err.(*Error).Msg)
		}
	}
	pl := Plan{Path: c, Bytes: len(content), Reversibility: "versioned", Risk: "medium"}
	if z := a.zoneAll(ctx, org, sc, c); !autoWrite(z) {
		pl.Approval, pl.Flags = true, []string{"outside_scope"}
		if z == ZoneOtherDesk {
			pl.Flags = append(pl.Flags, "other_agent_desk")
		}
	}
	return pl
}

// canonicalAction routes a write that carries a structured "document" to
// write_document, so a model that puts the document on fs.write still works.
func canonicalAction(action string, args map[string]any) string {
	if action == ActWrite {
		if d, ok := args["document"]; ok && d != nil {
			return ActWriteDocument
		}
	}
	return action
}

// planWriteDocument is planWrite for a structured document: the same zones and
// approvals (desk and assigned projects free, anything else asks a human), but
// the bytes are built by the backend from the model, never taken from the agent.
func (a *AgentFS) planWriteDocument(ctx context.Context, org string, sc scope, args map[string]any) Plan {
	p := argStr(args, "path")
	folder, name, err := SplitFile(p)
	if err != nil {
		return deny("path_invalid", err.(*Error).Msg)
	}
	c := folder + "/" + name
	if _, err := CleanName(name); err != nil {
		return deny("name_invalid", err.(*Error).Msg)
	}
	format := officedoc.FormatOfName(name)
	if format == "" {
		return deny("unsupported_type", "write_document makes xlsx, docx, pdf, md or csv files: the path must end in one of them")
	}
	if tags := argTags(args); tags != nil {
		if _, err := CleanTags(tags); err != nil {
			return deny("tags_invalid", err.(*Error).Msg)
		}
	}
	pl := Plan{Path: c, Reversibility: "versioned", Risk: "medium"}
	doc, perr := officedoc.Parse(args["document"])
	var data []byte
	if perr == nil {
		data, perr = officedoc.Build(format, doc)
	}
	if perr != nil {
		pl.docErr = perr.Error()
		var oe *officedoc.Error
		if errors.As(perr, &oe) {
			pl.docErr = oe.Msg
		}
		return pl
	}
	if int64(len(data)) > a.Svc.cfg.Limits.MaxFileBytes {
		return deny("file_too_large", fmt.Sprintf("the file exceeds the limit of %d MB", a.Svc.cfg.Limits.MaxFileBytes>>20))
	}
	pl.data, pl.Bytes = data, len(data)
	if z := a.zoneAll(ctx, org, sc, c); !autoWrite(z) {
		pl.Approval, pl.Flags = true, []string{"outside_scope"}
		if z == ZoneOtherDesk {
			pl.Flags = append(pl.Flags, "other_agent_desk")
		}
	}
	return pl
}

func (a *AgentFS) planMove(ctx context.Context, org string, sc scope, args map[string]any) Plan {
	f, err := a.targetFile(ctx, args, "path")
	if err != nil {
		return planErr(err)
	}
	zs := a.zoneAll(ctx, org, sc, f.Path)
	if f.DeletedAt != nil || !(canRead(zs) || autoWrite(zs)) {
		return deny("not_granted", "this file is outside the areas you can read")
	}
	to := argStr(args, "to")
	folder, name, err := SplitFile(to)
	if err != nil {
		return deny("path_invalid", err.(*Error).Msg)
	}
	if !sameExt(f.Name, name) {
		return deny("extension_change", "the file extension cannot change")
	}
	dest := folder + "/" + name
	pl := Plan{Path: f.Path, Reversibility: "versioned", Risk: "medium"}
	if !autoWrite(zs) || !autoWrite(a.zoneAll(ctx, org, sc, dest)) {
		pl.Approval = true
		pl.Flags = []string{"outside_scope"}
		if autoWrite(zs) {
			pl.Flags = []string{"moves_out_of_scope"}
		}
	}
	return pl
}

func (a *AgentFS) planDelete(ctx context.Context, org string, sc scope, args map[string]any) Plan {
	f, err := a.targetFile(ctx, args, "path")
	if err != nil {
		return planErr(err)
	}
	zs := a.zoneAll(ctx, org, sc, f.Path)
	if f.DeletedAt != nil || !(canRead(zs) || autoWrite(zs)) {
		return deny("not_granted", "this file is outside the areas you can read")
	}
	pl := Plan{Path: f.Path, Approval: true, Risk: "high", Reversibility: "trash", Flags: []string{"delete"}}
	if !autoWrite(zs) {
		pl.Flags = append(pl.Flags, "outside_scope")
	}
	return pl
}

// ---- run ----

// RunResult is the outcome of an action run as an agent.
type RunResult struct {
	OK      bool
	Code    string // stable failure code when !OK
	Summary string // one short trusted line (no user text)
	// Text is untrusted data (names, snippets, file content) the caller must
	// sanitize and delimit before it reaches a model. Empty for writes.
	Text string
	// SourceNote names the data for the delimiter ("fs.read /path v3").
	SourceNote string
	// AuthoredByOthers is true when the text came from files not written by this agent.
	AuthoredByOthers bool
	Data             map[string]any
}

func failure(err error) RunResult {
	var e *Error
	switch {
	case IsErr(err, ErrReadOnly):
		return RunResult{Code: "read_only_mode", Summary: "the organization is in read-only mode"}
	case errors.As(err, &e):
		r := RunResult{Code: e.Code, Summary: e.Code + ": " + e.Msg}
		if e.Code == "version_conflict" && e.Current > 0 {
			r.Data = map[string]any{"current_version": e.Current}
		}
		return r
	case errors.Is(err, domain.ErrNotFound):
		return RunResult{Code: "not_found", Summary: "not_found: no such file or folder"}
	}
	return RunResult{Code: "drive_error", Summary: "drive_error: the operation could not be completed"}
}

// IsErr is errors.Is for the exported sentinels.
func IsErr(err, target error) bool { return errors.Is(err, target) }

// Run executes an action as the agent. It re-checks the plan: an approval
// satisfies only "needs approval", never a refusal.
func (a *AgentFS) Run(ctx context.Context, agentID, action string, args map[string]any, approved bool) RunResult {
	action = canonicalAction(action, args)
	pl := a.Plan(ctx, agentID, action, args)
	if pl.Deny != "" {
		return RunResult{Code: pl.Deny, Summary: pl.Deny + ": " + pl.DenyMsg}
	}
	if pl.Approval && !approved {
		return RunResult{Code: "approval_required", Summary: "approval_required: a human must approve this action first"}
	}
	me := AgentP(agentID)
	switch action {
	case ActList:
		return a.runList(ctx, agentID, pl.Path)
	case ActRead:
		return a.runRead(ctx, agentID, args)
	case ActSearch:
		return a.runSearch(ctx, agentID, args)
	case ActWrite:
		in := WriteInput{Path: pl.Path, Data: []byte(argStr(args, "content")), Message: argStr(args, "message"), Tags: argTags(args), Source: "agent_tool"}
		if n, ok := args["expected_version"]; ok && n != nil {
			v, ok := argInt(args, "expected_version")
			if !ok || v < 0 {
				return RunResult{Code: "invalid_args", Summary: "invalid_args: expected_version must be a non-negative integer"}
			}
			in.Expected = &v
		}
		res, err := a.Svc.Write(ctx, me, in)
		if err != nil {
			return failure(err)
		}
		sum := fmt.Sprintf("wrote %s (version %d, %d bytes)", res.File.Path, res.File.CurrentVersion, res.File.Size)
		if res.Unchanged {
			sum = fmt.Sprintf("%s already has this content (version %d), nothing written", res.File.Path, res.File.CurrentVersion)
		}
		return RunResult{OK: true, Summary: sum, Data: map[string]any{"file_id": res.File.ID, "path": res.File.Path, "version": res.File.CurrentVersion, "created": res.Created}}
	case ActWriteDocument:
		if pl.docErr != "" {
			return RunResult{Code: "invalid_args", Summary: "invalid_args: " + pl.docErr}
		}
		in := WriteInput{Path: pl.Path, Data: pl.data, Message: argStr(args, "message"), Tags: argTags(args), Source: "agent_tool"}
		res, err := a.Svc.Write(ctx, me, in)
		if err != nil {
			return failure(err)
		}
		sum := fmt.Sprintf("wrote %s (version %d, %d bytes)", res.File.Path, res.File.CurrentVersion, res.File.Size)
		return RunResult{OK: true, Summary: sum, Data: map[string]any{"file_id": res.File.ID, "path": res.File.Path, "version": res.File.CurrentVersion, "created": res.Created}}
	case ActMove:
		f, err := a.targetFile(ctx, args, "path")
		if err != nil {
			return failure(err)
		}
		nf, err := a.Svc.MoveFile(ctx, me, f.ID, argStr(args, "to"))
		if err != nil {
			return failure(err)
		}
		return RunResult{OK: true, Summary: fmt.Sprintf("moved %s to %s", f.Path, nf.Path), Data: map[string]any{"file_id": nf.ID, "path": nf.Path}}
	case ActDelete:
		f, err := a.targetFile(ctx, args, "path")
		if err != nil {
			return failure(err)
		}
		if err := a.Svc.DeleteFile(ctx, me, f.ID); err != nil {
			return failure(err)
		}
		return RunResult{OK: true, Summary: fmt.Sprintf("moved %s to the trash (restorable)", f.Path), Data: map[string]any{"file_id": f.ID}}
	}
	return RunResult{Code: "unknown_action", Summary: "unknown_action"}
}

func human(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func fileLine(f File) string {
	return fmt.Sprintf("[file] %s | %s | v%d | %s | updated %s by %s:%s", f.Name, human(f.Size), f.CurrentVersion, f.Mime,
		f.UpdatedAt.UTC().Format(time.DateOnly), f.UpdatedBy.Kind, f.UpdatedBy.ID)
}

// readableRoots lists the folder paths the agent can read as search/list roots.
func (a *AgentFS) readableRoots(ctx context.Context, org string, sc scope) []string {
	roots := []string{JoinPath(RootAgents, sc.agent), "/" + RootShared}
	if len(sc.projects) > 0 {
		if top, err := a.Svc.cfg.Store.FolderByPath(ctx, org, "/"+RootProjects); err == nil {
			if kids, err := a.Svc.cfg.Store.ChildFolders(ctx, org, top.ID); err == nil {
				for _, k := range kids {
					if sc.projects[k.RefID] {
						roots = append(roots, k.Path)
					}
				}
			}
		}
	}
	return append(roots, sc.grants...)
}

func (a *AgentFS) runList(ctx context.Context, agentID, p string) RunResult {
	org := a.Svc.org(ctx)
	sc, err := a.scopeOf(ctx, agentID)
	if err != nil {
		return failure(err)
	}
	var b strings.Builder
	// "/" or a root the agent can only partly read (/Agents, /Projects, /Clients):
	// show only what it can reach. A wholly readable root (/Shared) lists its content.
	if p == "/" || strings.Count(p, "/") == 1 && !canRead(a.zone(ctx, org, sc, p)) {
		roots := a.readableRoots(ctx, org, sc)
		slices.Sort(roots)
		fmt.Fprintf(&b, "folders you can use under %s:\n", p)
		n := 0
		for _, r := range roots {
			if p == "/" || IsUnder(r, p) || IsUnder(p, r) {
				tag := ""
				if IsUnder(r, JoinPath(RootAgents, agentID)) {
					tag = " (your desk: write freely)"
				} else if a.zone(ctx, org, sc, r) == ZoneProject {
					tag = " (your project: write freely)"
				} else if r == "/"+RootShared {
					tag = " (shared: read; writes need approval)"
				} else {
					tag = " (read only)"
				}
				fmt.Fprintf(&b, "[dir] %s/%s\n", r, tag)
				n++
			}
		}
		return RunResult{OK: true, Summary: fmt.Sprintf("%d folder(s) reachable", n), Text: b.String(), SourceNote: "fs.list " + p}
	}
	l, err := a.Svc.List(ctx, p)
	if err != nil {
		return failure(err)
	}
	others := false
	for _, f := range l.Folders {
		fmt.Fprintf(&b, "[dir] %s/\n", f.Name)
	}
	for _, f := range l.Files {
		b.WriteString(fileLine(f) + "\n")
		others = others || f.UpdatedBy != AgentP(agentID)
	}
	if len(l.Folders)+len(l.Files) == 0 {
		b.WriteString("(empty)\n")
	}
	return RunResult{OK: true, Summary: fmt.Sprintf("%d folder(s), %d file(s) in %s", len(l.Folders), len(l.Files), l.Folder.Path), Text: b.String(),
		SourceNote: "fs.list " + l.Folder.Path, AuthoredByOthers: others}
}

func (a *AgentFS) runRead(ctx context.Context, agentID string, args map[string]any) RunResult {
	f, err := a.targetFile(ctx, args, "path")
	if err != nil {
		return failure(err)
	}
	ver, _ := argInt(args, "version")
	off, _ := argInt(args, "offset")
	mx, _ := argInt(args, "max_chars")
	res, err := a.Svc.ReadText(ctx, f, ver, off, mx)
	if err != nil {
		return failure(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "path: %s\nversion: %d (current %d)\nmime: %s\nsize: %s\nupdated_by: %s:%s\n", f.Path, res.Version.Version, f.CurrentVersion, f.Mime, human(f.Size), f.UpdatedBy.Kind, f.UpdatedBy.ID)
	if !res.Info.Extracted {
		fmt.Fprintf(&b, "text: not available (%s)\n", res.Info.Note)
		return RunResult{OK: true, Summary: fmt.Sprintf("%s: text not available", f.Path), Text: b.String(), SourceNote: "fs.read " + f.Path,
			AuthoredByOthers: f.UpdatedBy != AgentP(agentID), Data: map[string]any{"file_id": f.ID, "version": res.Version.Version}}
	}
	fmt.Fprintf(&b, "extraction: %s", res.Info.Kind)
	if res.Info.Note != "" {
		fmt.Fprintf(&b, " (%s)", res.Info.Note)
	}
	b.WriteString("\n---\n")
	b.WriteString(res.Text)
	if res.More {
		fmt.Fprintf(&b, "\n[truncated: characters %d-%d shown; call fs.read again with offset=%d for the next part]", res.Offset, res.Offset+res.Returned, res.NextStart)
	}
	return RunResult{OK: true, Summary: fmt.Sprintf("read %s (version %d, %d chars)", f.Path, res.Version.Version, res.Returned), Text: b.String(), SourceNote: "fs.read " + f.Path,
		AuthoredByOthers: f.UpdatedBy != AgentP(agentID), Data: map[string]any{"file_id": f.ID, "version": res.Version.Version}}
}

func (a *AgentFS) runSearch(ctx context.Context, agentID string, args map[string]any) RunResult {
	org := a.Svc.org(ctx)
	sc, err := a.scopeOf(ctx, agentID)
	if err != nil {
		return failure(err)
	}
	under := a.readableRoots(ctx, org, sc)
	if p := argStr(args, "path"); p != "" {
		c, _ := CleanPath(p)
		under = []string{c}
	}
	lim, _ := argInt(args, "limit")
	if lim <= 0 || lim > 10 {
		lim = 10
	}
	hits, err := a.Svc.Retrieve(ctx, RetrieveQuery{Query: argStr(args, "query"), Under: under, Limit: lim})
	if err != nil {
		return failure(err)
	}
	var b strings.Builder
	others := false
	for _, h := range hits {
		fmt.Fprintf(&b, "%s | v%d | %s\n  %s\n", h.Path, h.Version, human(h.Size), h.Snippet)
		others = others || h.UpdatedBy != AgentP(agentID)
	}
	if len(hits) == 0 {
		b.WriteString("(no matches)\n")
	}
	return RunResult{OK: true, Summary: fmt.Sprintf("%d match(es)", len(hits)), Text: b.String(), SourceNote: "fs.search", AuthoredByOthers: others}
}

// ProjectFolder returns the folder path of a project, creating it (and the
// agent desk) when asked. Used by the deliverable bridge and the run request.
func (s *Service) ProjectFolder(ctx context.Context, projectID, name string, create bool) (string, error) {
	if !create {
		top, err := s.cfg.Store.FolderByPath(ctx, s.org(ctx), "/"+RootProjects)
		if err != nil {
			return "", nil
		}
		kids, err := s.cfg.Store.ChildFolders(ctx, s.org(ctx), top.ID)
		if err != nil {
			return "", err
		}
		for _, k := range kids {
			if k.RefID == projectID {
				return k.Path, nil
			}
		}
		return "", nil
	}
	f, err := s.EnsureOwnedFolder(ctx, RootProjects, projectID, name)
	return f.Path, err
}
