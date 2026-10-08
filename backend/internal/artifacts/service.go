package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// ProjectLookup names a project for the workspace view (projects.Service).
type ProjectLookup interface {
	Info(ctx context.Context, id string) (name, status string, err error)
}

// Asker submits the request behind "ask an agent about this artifact".
type Asker interface {
	Submit(ctx context.Context, text string) (string, error)
}

// Config wires the service.
type Config struct {
	Store    Store
	Rec      *application.Recorder
	Collab   CollabStore       // comments and proposals (memory when nil)
	Core     application.Store // agents of the organization (attachments, deliverables)
	Asker    Asker
	Projects ProjectLookup
	OrgID    string
	// AutoRecalc recomputes dependent sheets when a source artifact changes (default on).
	DisableAutoRecalc bool
	Now               func() time.Time
	Log               *slog.Logger
}

// Service is the artifacts use case layer.
type Service struct {
	cfg   Config
	mu    sync.Mutex
	locks map[string]*sync.Mutex // org|artifact id
	// deliverables: org|project|node -> artifact id (cache of the workspace sink)
	deliv map[string]string
}

func New(cfg Config) *Service {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Collab == nil {
		cfg.Collab = NewMemCollab()
	}
	return &Service{cfg: cfg, locks: map[string]*sync.Mutex{}, deliv: map[string]string{}}
}

func (s *Service) org(ctx context.Context) string { return application.OrgFrom(ctx, s.cfg.OrgID) }

func (s *Service) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now()
	}
	return time.Now().UTC()
}

func (s *Service) lock(org, id string) func() {
	s.mu.Lock()
	m, ok := s.locks[org+"|"+id]
	if !ok {
		m = &sync.Mutex{}
		s.locks[org+"|"+id] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

func newID() string { return "art_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16] }

func forbidden(format string, a ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrForbidden, fmt.Sprintf(format, a...))
}

func conflictErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrConflict, fmt.Sprintf(format, a...))
}

func ptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ---- events and audit ----

func (s *Service) emit(ctx context.Context, typ, agentID, entityID string, payload any, text string) {
	if s.cfg.Rec == nil {
		return
	}
	s.cfg.Rec.Emit(ctx, application.Action{Type: typ, AgentID: agentID, Entity: "artifact", EntityID: entityID, Payload: payload, Text: text, SkipAudit: true})
}

func (s *Service) audit(ctx context.Context, a Actor, action, id string, details map[string]any) {
	if s.cfg.Rec == nil {
		return
	}
	actor := a.ID
	if actor == "" {
		actor = a.Kind
	}
	s.cfg.Rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: action, Entity: "artifact", EntityID: id, Details: details})
}

func agentOf(a ActorRef) string {
	if a.Kind == ActorAgent {
		return a.ID
	}
	return ""
}

// ---- catalog ----

// Kinds is GET /artifact-kinds: the catalog with the kinds suggested for an agent first.
func (s *Service) Kinds(agentID string) []KindInfo {
	sug := SuggestedKinds[agentID]
	out := make([]KindInfo, 0, len(Kinds))
	for _, k := range Kinds {
		out = append(out, KindInfo{Kind: k, SchemaVersion: k.Schema(), Suggested: slices.Contains(sug, k)})
	}
	return out
}

// Templates is GET /artifact-templates, with the ones suggested for the agent first.
func (s *Service) Templates(agentID string, kind Kind) []Template {
	out := []Template{}
	for _, t := range templates {
		if kind != "" && t.kind != kind {
			continue
		}
		out = append(out, Template{ID: t.id, Kind: t.kind, Title: map[string]string{"es": t.es, "en": t.en}, SuggestedFor: t.suggestedFor})
	}
	slices.SortStableFunc(out, func(a, b Template) int {
		ra, rb := 0, 0
		if slices.Contains(a.SuggestedFor, agentID) {
			ra = -1
		}
		if slices.Contains(b.SuggestedFor, agentID) {
			rb = -1
		}
		return ra - rb
	})
	return out
}

// ---- reads ----

// Filter narrows List.
type Filter struct {
	Kind, AgentID, TaskID, Status, Q, ProjectID string
}

func (f Filter) match(m Meta) bool {
	if f.Status == "" && m.Status == StatusArchived {
		return false
	}
	switch {
	case f.Kind != "" && string(m.Kind) != f.Kind,
		f.Status != "" && m.Status != f.Status,
		f.ProjectID != "" && (m.ProjectID == nil || *m.ProjectID != f.ProjectID),
		f.TaskID != "" && (m.TaskID == nil || *m.TaskID != f.TaskID),
		f.Q != "" && !strings.Contains(strings.ToLower(m.Title), strings.ToLower(f.Q)):
		return false
	}
	if f.AgentID != "" {
		ok := m.CreatedBy.ID == f.AgentID
		for _, a := range m.Attachments {
			ok = ok || a.AgentID == f.AgentID
		}
		return ok
	}
	return true
}

// List returns the meta of the organization's artifacts (no content).
func (s *Service) List(ctx context.Context, f Filter) ([]Meta, error) {
	all, err := s.cfg.Store.List(ctx, s.org(ctx))
	if err != nil {
		return nil, err
	}
	out := make([]Meta, 0, len(all))
	for _, m := range all {
		if f.match(m) {
			out = append(out, m)
		}
	}
	return out, nil
}

// Get returns an artifact with its head content, or the content of a past version.
func (s *Service) Get(ctx context.Context, id string, version int) (Artifact, error) {
	org := s.org(ctx)
	cur, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return Artifact{}, err
	}
	a := Artifact{Meta: cur.Meta, Content: cur.Content, Version: cur.Meta.HeadVersion}
	if version > 0 && version != cur.Meta.HeadVersion {
		v, err := s.cfg.Store.GetVersion(ctx, org, id, version)
		if err != nil {
			return Artifact{}, err
		}
		a.Content, a.Version = v.Content, v.Version
	}
	return a, nil
}

func (s *Service) Versions(ctx context.Context, id string) ([]VersionInfo, error) {
	if _, err := s.cfg.Store.Get(ctx, s.org(ctx), id); err != nil {
		return nil, err
	}
	return s.cfg.Store.ListVersions(ctx, s.org(ctx), id)
}

// ReadAsAgent returns an artifact to an agent that has at least read access.
func (s *Service) ReadAsAgent(ctx context.Context, agentID, id string) (Artifact, error) {
	a, err := s.Get(ctx, id, 0)
	if err != nil {
		return a, err
	}
	if modeOf(a.Meta, agentID) == ModeNone {
		return Artifact{}, forbidden("agent %s has no access to this artifact", agentID)
	}
	return a, nil
}

func modeOf(m Meta, agentID string) string {
	for _, a := range m.Attachments {
		if a.AgentID == agentID {
			return a.Mode
		}
	}
	return ModeNone
}

// ---- create ----

// Create makes a new artifact (version 1). Agents create with edit access to their own artifact.
func (s *Service) Create(ctx context.Context, actor Actor, in CreateInput) (Artifact, error) {
	return s.create(ctx, actor, in, createOpts{open: actor.IsAgent(), progress: 100, build: BuildDone})
}

// createOpts are the internal knobs of create (deliverables of a project).
type createOpts struct {
	open                            bool
	progress                        int
	build                           string
	projectID, deliverableID, reqID string
}

func (s *Service) create(ctx context.Context, actor Actor, in CreateInput, o createOpts) (Artifact, error) {
	org := s.org(ctx)
	locale := in.Locale
	if locale != "en" {
		locale = "es"
	}
	kind, content := in.Kind, in.Content
	if in.TemplateID != "" {
		t, ok := findTemplate(in.TemplateID)
		if !ok {
			return Artifact{}, fmt.Errorf("%w: unknown template %q", domain.ErrNotFound, in.TemplateID)
		}
		if kind == "" || kind == t.kind {
			kind = t.kind
			if len(content) == 0 {
				content = rawOf(t.build(locale))
			}
		}
	}
	if kind == "" {
		kind = KindDoc
	}
	if !kind.Valid() {
		return Artifact{}, invalid("unknown kind %q", kind)
	}
	if len(content) == 0 {
		content = rawOf(blank(kind, locale))
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = L(locale, "Sin título", "Untitled")
	}
	if len([]rune(title)) > maxTitle {
		return Artifact{}, invalid("title is too long (limit %d)", maxTitle)
	}
	raw, root, err := parseContent(kind, content)
	if err != nil {
		return Artifact{}, err
	}
	agent := in.AgentID
	if actor.IsAgent() {
		agent = actor.ID
	}
	if agent != "" {
		if _, err := s.cfg.Core.GetAgent(ctx, org, agent); err != nil {
			return Artifact{}, fmt.Errorf("%w: unknown agent %q", domain.ErrInvalid, agent)
		}
	}
	now := s.now()
	m := Meta{ID: newID(), Kind: kind, SchemaVersion: kind.Schema(), Title: title, Status: StatusDraft, HeadVersion: 1, CreatedBy: actor.Ref(), LastAuthor: actor.Ref(),
		TaskID: ptr(in.TaskID), CustomerID: ptr(in.CustomerID), ProjectID: ptr(o.projectID), DeliverableID: ptr(o.deliverableID), RequestID: ptr(o.reqID),
		Progress: o.progress, BuildState: o.build, Locale: locale, DependsOnArtifact: []string{},
		SizeBytes: len(raw), Attachments: []Attachment{}, CreatedAt: now, UpdatedAt: now}
	switch {
	case actor.IsAgent():
		m.Attachments = []Attachment{{AgentID: actor.ID, Mode: ModeEdit}}
	case agent != "":
		m.Attachments = []Attachment{{AgentID: agent, Mode: ModeRead}} // opened by a human in an agent's desk: read by default
	}
	refs := s.resolveRefs(ctx, org, m.ID, kind, root)
	m.DependsOnArtifact = dependsOn(refs)
	v := Version{VersionInfo: VersionInfo{ArtifactID: m.ID, Version: 1, Author: actor.Ref(), Source: "create", Summary: L(locale, "Versión inicial", "Initial version"),
		TaskID: ptr(in.TaskID), ContentHash: contentHash(raw), CreatedAt: now}, Content: raw}
	if err := s.cfg.Store.Create(ctx, org, Stored{Meta: m, Content: raw}, v); err != nil {
		return Artifact{}, err
	}
	if err := s.cfg.Store.ReplaceAutoLinks(ctx, org, m.ID, s.linksFor(ctx, org, m.ID, refs)); err != nil {
		return Artifact{}, err
	}
	s.audit(ctx, actor, "artifact.created", m.ID, map[string]any{"kind": kind, "version": 1, "content_hash": v.ContentHash})
	s.emit(ctx, "artifact.created", agentOf(actor.Ref()), m.ID, map[string]any{"artifact": m, "open_in_workspace": o.open}, "")
	return Artifact{Meta: m, Content: raw, Version: 1}, nil
}

// ---- links derived from content ----

func (s *Service) resolveRefs(ctx context.Context, org, self string, kind Kind, root map[string]any) []ref {
	var out []ref
	seen := map[string]bool{}
	for _, r := range extractRefs(kind, root) {
		k := r.To + "|" + r.Relation
		if r.To == "" || r.To == self || seen[k] {
			continue
		}
		if _, err := s.cfg.Store.Get(ctx, org, r.To); err != nil {
			continue // dangling reference: nothing to link to
		}
		seen[k] = true
		out = append(out, r)
	}
	return out
}

func dependsOn(refs []ref) []string {
	out := []string{}
	for _, r := range refs {
		if r.Relation == RelSourceOf && !slices.Contains(out, r.To) {
			out = append(out, r.To)
		}
	}
	return out
}

func (s *Service) linksFor(ctx context.Context, org, from string, refs []ref) []Link {
	out := make([]Link, 0, len(refs))
	for _, r := range refs {
		l := Link{ID: "lnk_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12], From: from, To: r.To, Relation: r.Relation, Anchor: r.Anchor, Auto: true}
		if r.Alias != "" {
			a := r.Alias
			l.Alias = &a
		}
		if r.Relation == RelSourceOf {
			if src, err := s.cfg.Store.Get(ctx, org, r.To); err == nil {
				l.SyncedVersion = src.Meta.HeadVersion
			}
		}
		out = append(out, l)
	}
	return out
}

// ---- writes ----

func (s *Service) authorizeWrite(actor Actor, m Meta) error {
	if m.Status == StatusArchived {
		return conflictErr("the artifact is archived")
	}
	if actor.IsAgent() {
		switch modeOf(m, actor.ID) {
		case ModeEdit:
		case ModePropose:
			return forbidden("agent %s can only propose changes on this artifact", actor.ID)
		default:
			return forbidden("agent %s has no edit access to this artifact", actor.ID)
		}
	}
	return nil
}

func sameContent(a, b []byte) bool {
	var x, y any
	dx, dy := json.NewDecoder(bytes.NewReader(a)), json.NewDecoder(bytes.NewReader(b))
	dx.UseNumber()
	dy.UseNumber()
	if dx.Decode(&x) != nil || dy.Decode(&y) != nil {
		return false
	}
	return equal(x, y)
}

// Save stores a new version of the content. The write is based on `base_version`:
// when the head moved meanwhile the changes are merged per unit or, if the same
// unit changed on both sides, nothing is written and a *Conflict is returned
// (HTTP 409). A save without changes does not create a version.
func (s *Service) Save(ctx context.Context, actor Actor, id string, in SaveInput) (SaveResult, error) {
	org := s.org(ctx)
	res, changed, err := s.saveLocked(ctx, org, actor, id, in)
	if err != nil {
		return res, err
	}
	if changed {
		s.propagate(ctx, org, id, res.Version, 0)
	}
	return res, nil
}

// ApplyAgent is the entry point for an agent task: it writes only with edit access.
func (s *Service) ApplyAgent(ctx context.Context, agentID, taskID, id string, in SaveInput) (SaveResult, error) {
	return s.Save(ctx, Actor{Kind: ActorAgent, ID: agentID}, id, in)
}

func (s *Service) saveLocked(ctx context.Context, org string, actor Actor, id string, in SaveInput) (SaveResult, bool, error) {
	defer s.lock(org, id)()
	cur, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return SaveResult{}, false, err
	}
	m := cur.Meta
	if err := s.authorizeWrite(actor, m); err != nil {
		return SaveResult{}, false, err
	}
	if m.Locked {
		return SaveResult{}, false, conflictErr("the artifact is %s and locked: move it back to draft first", m.Status)
	}
	if in.BaseVersion <= 0 || in.BaseVersion > m.HeadVersion {
		return SaveResult{}, false, invalid("base_version must be between 1 and the head version %d", m.HeadVersion)
	}
	raw, root, err := parseContent(m.Kind, in.Content)
	if err != nil {
		return SaveResult{}, false, err
	}
	source := "human_edit"
	if actor.IsAgent() {
		source = "agent_task"
	}
	if in.source != "" {
		source = in.source
	}
	if in.BaseVersion == m.HeadVersion {
		if sameContent(raw, cur.Content) {
			return SaveResult{Version: m.HeadVersion}, false, nil
		}
		v, err := s.commit(ctx, org, cur, raw, root, actor.Ref(), source, in.Summary, &in.BaseVersion)
		return SaveResult{Version: v}, err == nil, err
	}
	// The head moved: three-way merge.
	baseV, err := s.cfg.Store.GetVersion(ctx, org, id, in.BaseVersion)
	if err != nil {
		return SaveResult{}, false, &Conflict{HeadVersion: m.HeadVersion}
	}
	var baseRoot, headRoot map[string]any
	if err := decodeNum(baseV.Content, &baseRoot); err != nil {
		return SaveResult{}, false, err
	}
	if err := decodeNum(cur.Content, &headRoot); err != nil {
		return SaveResult{}, false, err
	}
	headAuthor := m.LastAuthor
	merged, conflicts := mergeContent(m.Kind, baseRoot, root, headRoot, &headAuthor)
	if len(conflicts) > 0 {
		return SaveResult{}, false, &Conflict{HeadVersion: m.HeadVersion, Conflicts: conflicts}
	}
	mraw, err := json.Marshal(merged)
	if err != nil {
		return SaveResult{}, false, err
	}
	mraw, mroot, err := parseContent(m.Kind, mraw)
	if err != nil {
		return SaveResult{}, false, err
	}
	if sameContent(mraw, cur.Content) {
		return SaveResult{Version: m.HeadVersion, Merged: true, Content: mraw}, false, nil
	}
	rebaseSource := "rebase"
	if in.source != "" {
		rebaseSource = in.source
	}
	v, err := s.commit(ctx, org, cur, mraw, mroot, actor.Ref(), rebaseSource, in.Summary, &in.BaseVersion)
	if err != nil {
		return SaveResult{}, false, err
	}
	return SaveResult{Version: v, Merged: true, Content: mraw}, true, nil
}

func decodeNum(raw []byte, dst *map[string]any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(dst)
}

// commit appends a version (the caller holds the artifact lock).
func (s *Service) commit(ctx context.Context, org string, cur Stored, raw json.RawMessage, root map[string]any, author ActorRef, source, summary string, base *int) (int, error) {
	m := cur.Meta
	now := s.now()
	refs := s.resolveRefs(ctx, org, m.ID, m.Kind, root)
	prevHead := m.HeadVersion
	m.HeadVersion++
	m.LastAuthor, m.UpdatedAt, m.SizeBytes, m.DependsOnArtifact = author, now, len(raw), dependsOn(refs)
	if summary == "" {
		summary = map[string]string{"human_edit": L(m.Locale, "Edición manual", "Manual edit"), "rebase": L(m.Locale, "Edición manual (fusionada)", "Manual edit (merged)"),
			"agent_task": L(m.Locale, "Actualización del agente", "Agent update"), "restore": L(m.Locale, "Versión restaurada", "Restored version"),
			"recalc": L(m.Locale, "Recalculado por un cambio en una fuente", "Recalculated after a source changed")}[source]
	}
	v := Version{VersionInfo: VersionInfo{ArtifactID: m.ID, Version: m.HeadVersion, BaseVersion: base, Author: author, Source: source, Summary: summary,
		TaskID: m.TaskID, ContentHash: contentHash(raw), CreatedAt: now}, Content: raw}
	if err := s.cfg.Store.Commit(ctx, org, Stored{Meta: m, Content: raw}, v, prevHead); err != nil {
		return 0, err
	}
	if err := s.cfg.Store.ReplaceAutoLinks(ctx, org, m.ID, s.linksFor(ctx, org, m.ID, refs)); err != nil {
		s.cfg.Log.Warn("replace artifact links", "artifact", m.ID, "err", err)
	}
	var oldRoot map[string]any
	_ = decodeNum(cur.Content, &oldRoot)
	units := changedUnits(m.Kind, oldRoot, root)
	b := 0
	if base != nil {
		b = *base
	}
	s.audit(ctx, Actor{Kind: author.Kind, ID: author.ID}, "artifact.version_created", m.ID, map[string]any{"version": m.HeadVersion, "base_version": b, "source": source, "content_hash": v.ContentHash})
	text := ""
	if author.Kind == ActorAgent {
		text = fmt.Sprintf("%s: «%s» (v%d)", author.ID, truncate(m.Title, 60), m.HeadVersion)
	}
	s.emit(ctx, "artifact.version_created", agentOf(author), m.ID, map[string]any{"artifact_id": m.ID, "version": m.HeadVersion, "base_version": base,
		"author": author, "source": source, "summary": summary, "changed_units": units}, text)
	if author.Kind == ActorAgent {
		region := ""
		if len(units) > 0 {
			region = units[0]
		}
		s.emit(ctx, "artifact.agent_focus", author.ID, m.ID, map[string]any{"artifact_id": m.ID, "agent_id": author.ID, "region": region, "ttl_ms": 3000}, "")
	}
	return m.HeadVersion, nil
}

// changedUnits lists (at most 50) the cells, blocks, rows or fields that differ.
func changedUnits(kind Kind, oldRoot, newRoot map[string]any) []string {
	out := []string{}
	add := func(u string) {
		if len(out) < 50 {
			out = append(out, u)
		}
	}
	diffList := func(a, b []any, idKey string) {
		am, bm := map[string]any{}, map[string]any{}
		for _, x := range a {
			am[asStr(asMap(x)[idKey])] = x
		}
		for _, x := range b {
			id := asStr(asMap(x)[idKey])
			bm[id] = x
			if o, ok := am[id]; !ok || !equal(o, x) {
				add(id)
			}
		}
		for id := range am {
			if _, ok := bm[id]; !ok {
				add(id)
			}
		}
	}
	switch kind {
	case KindSheet:
		old := map[string]map[string]any{}
		for _, s := range asList(oldRoot["sheets"]) {
			old[asStr(asMap(s)["id"])] = asMap(asMap(s)["cells"])
		}
		for _, s := range asList(newRoot["sheets"]) {
			sm := asMap(s)
			oc, nc := old[asStr(sm["id"])], asMap(sm["cells"])
			keys := map[string]bool{}
			for k := range oc {
				keys[k] = true
			}
			for k := range nc {
				keys[k] = true
			}
			ks := make([]string, 0, len(keys))
			for k := range keys {
				ks = append(ks, k)
			}
			slices.Sort(ks)
			for _, k := range ks {
				if !equal(oc[k], nc[k]) {
					add(asStr(sm["name"]) + "!" + k)
				}
			}
		}
	case KindDoc:
		diffList(blocks(asMap(oldRoot["doc"])), blocks(asMap(newRoot["doc"])), "bid")
	case KindTable:
		diffList(asList(oldRoot["rows"]), asList(newRoot["rows"]), "id")
	case KindBoard:
		diffList(asList(oldRoot["cards"]), asList(newRoot["cards"]), "id")
	case KindAgenda:
		diffList(asList(oldRoot["events"]), asList(newRoot["events"]), "id")
	case KindInbox:
		diffList(asList(oldRoot["items"]), asList(newRoot["items"]), "id")
	default:
		for k, v := range newRoot {
			if !equal(oldRoot[k], v) {
				add(k)
			}
		}
	}
	return out
}

// Restore makes an old version the new head (a new version, history is kept).
func (s *Service) Restore(ctx context.Context, actor Actor, id string, version int) (Meta, error) {
	org := s.org(ctx)
	meta, newV, err := s.restoreLocked(ctx, org, actor, id, version)
	if err != nil {
		return Meta{}, err
	}
	s.propagate(ctx, org, id, newV, 0)
	return meta, nil
}

func (s *Service) restoreLocked(ctx context.Context, org string, actor Actor, id string, version int) (Meta, int, error) {
	defer s.lock(org, id)()
	cur, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return Meta{}, 0, err
	}
	if err := s.authorizeWrite(actor, cur.Meta); err != nil {
		return Meta{}, 0, err
	}
	if cur.Meta.Locked {
		return Meta{}, 0, conflictErr("the artifact is %s and locked: move it back to draft first", cur.Meta.Status)
	}
	old, err := s.cfg.Store.GetVersion(ctx, org, id, version)
	if err != nil {
		return Meta{}, 0, err
	}
	raw, root, err := parseContent(cur.Meta.Kind, old.Content)
	if err != nil {
		return Meta{}, 0, err
	}
	summary := L(cur.Meta.Locale, fmt.Sprintf("Restaura la versión %d", version), fmt.Sprintf("Restores version %d", version))
	b := cur.Meta.HeadVersion
	if sameContent(raw, cur.Content) {
		return cur.Meta, cur.Meta.HeadVersion, nil
	}
	v, err := s.commit(ctx, org, cur, raw, root, actor.Ref(), "restore", summary, &b)
	if err != nil {
		return Meta{}, 0, err
	}
	now, _ := s.cfg.Store.Get(ctx, org, id)
	return now.Meta, v, nil
}

// ---- metadata ----

// Patch changes the title, the status or the project/deliverable of an artifact.
// approved and sent (and leaving them) need the approve capability, which no
// agent ever has; archiving needs delete rights unless the actor created it.
func (s *Service) Patch(ctx context.Context, actor Actor, id string, in PatchInput) (Meta, error) {
	org := s.org(ctx)
	defer s.lock(org, id)()
	cur, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return Meta{}, err
	}
	m := cur.Meta
	from := m.Status
	if in.Title != nil || in.ProjectID != nil || in.DeliverableID != nil {
		if actor.IsAgent() && modeOf(m, actor.ID) != ModeEdit {
			return Meta{}, forbidden("agent %s has no edit access to this artifact", actor.ID)
		}
		if m.Locked {
			return Meta{}, conflictErr("the artifact is %s and locked", m.Status)
		}
	}
	if t := in.Title; t != nil {
		title := strings.TrimSpace(*t)
		if title == "" || len([]rune(title)) > maxTitle {
			return Meta{}, invalid("title must have 1-%d characters", maxTitle)
		}
		m.Title = title
	}
	if p := in.ProjectID; p != nil {
		if *p != "" && s.cfg.Projects != nil {
			if _, _, err := s.cfg.Projects.Info(ctx, *p); err != nil {
				return Meta{}, fmt.Errorf("%w: unknown project %q", domain.ErrInvalid, *p)
			}
		}
		m.ProjectID = ptr(*p)
	}
	if d := in.DeliverableID; d != nil {
		m.DeliverableID = ptr(*d)
	}
	if st := in.Status; st != nil && *st != from {
		if !validStatus(*st) {
			return Meta{}, invalid("unknown status %q", *st)
		}
		switch {
		case actor.IsAgent():
			if *st != StatusInReview || modeOf(m, actor.ID) != ModeEdit {
				return Meta{}, forbidden("agents can never approve or send: only a person can set status %q", *st)
			}
		case lockedStatus(*st) || lockedStatus(from):
			if !actor.CanApprove {
				return Meta{}, forbidden("approving, sending or unlocking an artifact needs the approve permission")
			}
		case *st == StatusArchived:
			if !actor.CanDelete && !(m.CreatedBy.Kind == actor.Kind && m.CreatedBy.ID == actor.ID) {
				return Meta{}, forbidden("archiving an artifact created by someone else needs delete permission")
			}
		}
		m.Status, m.Locked = *st, lockedStatus(*st)
	}
	m.UpdatedAt = s.now()
	if err := s.cfg.Store.UpdateMeta(ctx, org, m); err != nil {
		return Meta{}, err
	}
	m.HeadVersion, m.SizeBytes, m.LastAuthor = cur.Meta.HeadVersion, cur.Meta.SizeBytes, cur.Meta.LastAuthor
	if m.Status != from {
		s.audit(ctx, actor, "artifact.status_changed", id, map[string]any{"from": from, "to": m.Status})
		s.emit(ctx, "artifact.status_changed", agentOf(actor.Ref()), id, map[string]any{"artifact_id": id, "from": from, "to": m.Status, "by": actor.Ref()}, "")
		if m.Status == StatusArchived {
			s.emit(ctx, "artifact.deleted", "", id, map[string]any{"artifact_id": id}, "")
		}
	}
	s.emit(ctx, "artifact.updated", agentOf(actor.Ref()), id, map[string]any{"artifact": m}, "")
	return m, nil
}

// Archive is DELETE /artifacts/{id}: a soft delete (the versions are kept).
func (s *Service) Archive(ctx context.Context, actor Actor, id string) error {
	st := StatusArchived
	_, err := s.Patch(ctx, actor, id, PatchInput{Status: &st})
	return err
}

// Attach sets what an agent may do with an artifact: none, read, propose or
// edit. There is no approve mode: agents never approve anything. Only people
// grant attachments.
func (s *Service) Attach(ctx context.Context, actor Actor, id, agentID, mode string) (Meta, error) {
	org := s.org(ctx)
	if actor.IsAgent() {
		return Meta{}, forbidden("agents cannot change attachments")
	}
	switch mode {
	case ModeNone, ModeRead, ModePropose, ModeEdit:
	case "approve":
		return Meta{}, invalid("agents never receive the approve mode: only a person approves")
	default:
		return Meta{}, invalid("mode must be none, read, propose or edit")
	}
	if _, err := s.cfg.Core.GetAgent(ctx, org, agentID); err != nil {
		return Meta{}, fmt.Errorf("%w: unknown agent %q", domain.ErrNotFound, agentID)
	}
	defer s.lock(org, id)()
	cur, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return Meta{}, err
	}
	m := cur.Meta
	if m.Status == StatusArchived {
		return Meta{}, conflictErr("the artifact is archived")
	}
	rest := slices.DeleteFunc(slices.Clone(m.Attachments), func(a Attachment) bool { return a.AgentID == agentID })
	if mode != ModeNone {
		rest = append(rest, Attachment{AgentID: agentID, Mode: mode})
	}
	m.Attachments, m.UpdatedAt = rest, s.now()
	if err := s.cfg.Store.UpdateMeta(ctx, org, m); err != nil {
		return Meta{}, err
	}
	m.HeadVersion, m.SizeBytes, m.LastAuthor = cur.Meta.HeadVersion, cur.Meta.SizeBytes, cur.Meta.LastAuthor
	s.audit(ctx, actor, "artifact.attachment_changed", id, map[string]any{"agent_id": agentID, "mode": mode})
	s.emit(ctx, "artifact.updated", "", id, map[string]any{"artifact": m}, "")
	return m, nil
}

// ---- links and dependencies ----

// Links lists the references of an artifact; direction is in, out or both.
func (s *Service) Links(ctx context.Context, id, direction string) ([]Link, error) {
	org := s.org(ctx)
	if _, err := s.cfg.Store.Get(ctx, org, id); err != nil {
		return nil, err
	}
	all, err := s.cfg.Store.ListLinks(ctx, org, id)
	if err != nil {
		return nil, err
	}
	out := []Link{}
	for _, l := range all {
		if (direction == "out" && l.From != id) || (direction == "in" && l.To != id) {
			continue
		}
		out = append(out, s.markStale(ctx, org, l))
	}
	return out, nil
}

// markStale: a source_of link of a sheet is stale when the source moved on since the last recalculation.
func (s *Service) markStale(ctx context.Context, org string, l Link) Link {
	if l.Relation != RelSourceOf {
		return l
	}
	src, err1 := s.cfg.Store.Get(ctx, org, l.To)
	dep, err2 := s.cfg.Store.Get(ctx, org, l.From)
	if err1 == nil && err2 == nil && dep.Meta.Kind == KindSheet && src.Meta.HeadVersion > l.SyncedVersion {
		l.Stale = true
	}
	return l
}

// AddLink records a manual reference from one artifact to another.
func (s *Service) AddLink(ctx context.Context, actor Actor, from, to, relation string, anchor *Anchor) (Link, error) {
	org := s.org(ctx)
	switch relation {
	case RelEmbeds, RelSourceOf, RelDerivedFrom, RelRefersTo:
	default:
		return Link{}, invalid("relation must be embeds, source_of, derived_from or refers_to")
	}
	if from == to {
		return Link{}, invalid("an artifact cannot reference itself")
	}
	src, err := s.cfg.Store.Get(ctx, org, from)
	if err != nil {
		return Link{}, err
	}
	if err := s.authorizeWrite(actor, src.Meta); err != nil {
		return Link{}, err
	}
	dst, err := s.cfg.Store.Get(ctx, org, to)
	if err != nil {
		return Link{}, fmt.Errorf("%w: the target artifact does not exist", domain.ErrNotFound)
	}
	l := Link{ID: "lnk_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12], From: from, To: to, Relation: relation, Anchor: anchor, SyncedVersion: dst.Meta.HeadVersion}
	if err := s.cfg.Store.AddLink(ctx, org, l); err != nil {
		return Link{}, err
	}
	s.audit(ctx, actor, "artifact.link_added", from, map[string]any{"to": to, "relation": relation})
	s.emit(ctx, "artifact.link_added", agentOf(actor.Ref()), from, map[string]any{"from": from, "to": to, "relation": relation}, "")
	return l, nil
}

// propagate tells the dependents of a changed source (and recalculates their
// cached values) after version `version` of source became the head.
func (s *Service) propagate(ctx context.Context, org, source string, version, depth int) {
	if depth > 8 {
		return
	}
	links, err := s.cfg.Store.ListLinks(ctx, org, source)
	if err != nil {
		return
	}
	for _, l := range links {
		if l.To != source || l.Relation != RelSourceOf {
			continue
		}
		dep, err := s.cfg.Store.Get(ctx, org, l.From)
		if err != nil || dep.Meta.Kind != KindSheet {
			continue
		}
		s.emit(ctx, "artifact.dependency_changed", "", dep.Meta.ID, map[string]any{"artifact_id": dep.Meta.ID, "source_id": source, "source_version": version, "stale": true}, "")
		if !s.cfg.DisableAutoRecalc {
			if _, err := s.refresh(ctx, org, Actor{Kind: ActorSystem, ID: "recalc"}, dep.Meta.ID, depth+1); err != nil && !errors.Is(err, domain.ErrConflict) {
				s.cfg.Log.Warn("recalculate dependent", "artifact", dep.Meta.ID, "err", err)
			}
		}
	}
}

// RefreshResult is the answer of POST /artifacts/{id}/refresh-dependencies.
type RefreshResult struct {
	Refreshed    bool     `json:"refreshed"`
	Version      int      `json:"version"`
	ChangedUnits []string `json:"changed_units"`
	Unresolved   []string `json:"unresolved"`
}

// RefreshDependencies recalculates the cached values of a dependent sheet from the head of its sources.
func (s *Service) RefreshDependencies(ctx context.Context, actor Actor, id string) (RefreshResult, error) {
	return s.refresh(ctx, s.org(ctx), actor, id, 0)
}

func (s *Service) refresh(ctx context.Context, org string, actor Actor, id string, depth int) (RefreshResult, error) {
	res, newV, err := s.refreshLocked(ctx, org, actor, id)
	if err != nil {
		return res, err
	}
	if res.Refreshed {
		s.propagate(ctx, org, id, newV, depth)
	}
	return res, nil
}

func (s *Service) refreshLocked(ctx context.Context, org string, actor Actor, id string) (RefreshResult, int, error) {
	defer s.lock(org, id)()
	res := RefreshResult{ChangedUnits: []string{}, Unresolved: []string{}}
	cur, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return res, 0, err
	}
	if err := s.authorizeWrite(actor, cur.Meta); err != nil {
		return res, 0, err
	}
	res.Version = cur.Meta.HeadVersion
	if cur.Meta.Kind != KindSheet {
		return res, 0, nil
	}
	if cur.Meta.Locked {
		return res, 0, conflictErr("the artifact is %s and locked", cur.Meta.Status)
	}
	var root map[string]any
	if err := decodeNum(cur.Content, &root); err != nil {
		return res, 0, err
	}
	imports := map[string]map[string]any{}
	for _, im := range asList(root["imports"]) {
		m := asMap(im)
		imports[asStr(m["alias"])] = m
	}
	sources := map[string]map[string]any{}
	ref := func(alias, sheet, cell string) (cellVal, error) {
		im, ok := imports[alias]
		if !ok {
			return cellVal{}, fmt.Errorf("undeclared alias %q", alias)
		}
		srcID := asStr(im["artifact_id"])
		doc, ok := sources[srcID]
		if !ok {
			src, err := s.cfg.Store.Get(ctx, org, srcID)
			if err != nil {
				return cellVal{}, err
			}
			content := src.Content
			if pv, ok := toNum(im["pinned_version"]); ok && int(pv) > 0 && int(pv) != src.Meta.HeadVersion {
				v, err := s.cfg.Store.GetVersion(ctx, org, srcID, int(pv))
				if err != nil {
					return cellVal{}, err
				}
				content = v.Content
			}
			if err := decodeNum(content, &doc); err != nil {
				return cellVal{}, err
			}
			sources[srcID] = doc
		}
		for _, sh := range asList(doc["sheets"]) {
			sm := asMap(sh)
			if asStr(sm["name"]) != sheet {
				continue
			}
			sub := newEvaluator([]any{sm}, nil)
			return sub.cell(sheet, cell)
		}
		return cellVal{}, fmt.Errorf("sheet %q not found", sheet)
	}
	changed, unresolved := recalcSheets(root, ref)
	slices.Sort(changed)
	res.ChangedUnits, res.Unresolved = nonNil(changed), nonNil(unresolved)
	// Mark the sources as synced whatever happened to the values.
	links, _ := s.cfg.Store.ListLinks(ctx, org, id)
	var syncedTo []Link
	for _, l := range links {
		if l.From == id && l.Relation == RelSourceOf {
			if src, err := s.cfg.Store.Get(ctx, org, l.To); err == nil {
				_ = s.cfg.Store.SetSyncedVersion(ctx, org, l.ID, src.Meta.HeadVersion)
				syncedTo = append(syncedTo, Link{To: l.To})
			}
		}
	}
	if len(changed) == 0 {
		return res, 0, nil
	}
	mraw, err := json.Marshal(root)
	if err != nil {
		return res, 0, err
	}
	raw, parsed, err := parseContent(KindSheet, mraw)
	if err != nil {
		return res, 0, err
	}
	b := cur.Meta.HeadVersion
	v, err := s.commit(ctx, org, cur, raw, parsed, actor.Ref(), "recalc", "", &b)
	if err != nil {
		return res, 0, err
	}
	res.Refreshed, res.Version = true, v
	for _, l := range syncedTo {
		s.emit(ctx, "artifact.recalculated", "", id, map[string]any{"artifact_id": id, "version": v, "source_id": l.To, "changed_units": changed}, "")
		s.emit(ctx, "artifact.dependency_changed", "", id, map[string]any{"artifact_id": id, "source_id": l.To, "stale": false}, "")
	}
	return res, v, nil
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

// ---- ask an agent ----

type AskInput struct {
	AgentID string `json:"agent_id"`
	Text    string `json:"text,omitempty"`
	Mode    string `json:"mode"` // review_my_changes | free
}

// Ask submits a request for an agent about an artifact. The agent gets read
// access when it has none (it cannot review what it cannot see); the request
// goes through the normal orchestrator path (budget, approvals, audit).
func (s *Service) Ask(ctx context.Context, actor Actor, id string, in AskInput) (string, error) {
	org := s.org(ctx)
	if s.cfg.Asker == nil {
		return "", fmt.Errorf("%w: asking agents is not available", domain.ErrConflict)
	}
	if in.Mode != "review_my_changes" && in.Mode != "free" {
		return "", invalid("mode must be review_my_changes or free")
	}
	if in.Mode == "free" && strings.TrimSpace(in.Text) == "" {
		return "", invalid("text is required in free mode")
	}
	if len([]rune(in.Text)) > 2000 {
		return "", invalid("text is too long")
	}
	cur, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return "", err
	}
	agent, err := s.cfg.Core.GetAgent(ctx, org, in.AgentID)
	if err != nil {
		return "", fmt.Errorf("%w: unknown agent %q", domain.ErrNotFound, in.AgentID)
	}
	if modeOf(cur.Meta, agent.ID) == ModeNone {
		if _, err := s.Attach(ctx, actor, id, agent.ID, ModeRead); err != nil {
			return "", err
		}
	}
	m := cur.Meta
	var text string
	if in.Mode == "review_my_changes" {
		text = L(m.Locale, fmt.Sprintf("Para %s (%s): revisa los cambios que hice en el artefacto «%s» (%s, versión %d) y dime si ves errores.", agent.Title, agent.ID, m.Title, m.ID, m.HeadVersion),
			fmt.Sprintf("For %s (%s): review the changes I made in the artifact “%s” (%s, version %d) and tell me if you see any mistakes.", agent.Title, agent.ID, m.Title, m.ID, m.HeadVersion))
	} else {
		text = fmt.Sprintf("%s (%s) · %s [%s, v%d]: %s", agent.Title, agent.ID, m.Title, m.ID, m.HeadVersion, strings.TrimSpace(in.Text))
	}
	reqID, err := s.cfg.Asker.Submit(ctx, text)
	if err != nil {
		return "", err
	}
	s.audit(ctx, actor, "artifact.ask", id, map[string]any{"agent_id": agent.ID, "mode": in.Mode, "request_id": reqID})
	return reqID, nil
}
