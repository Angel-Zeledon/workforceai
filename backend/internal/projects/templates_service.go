package projects

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Customizable work templates (G2): organization templates that users create,
// edit, duplicate, share and version. Built-in templates live in code and are
// never edited: "duplicate" gives an editable copy. Authorization by role is in
// the API (members create and edit, admins delete); the service repeats the
// delete rule when the role is known and enforces who can see a private template.

// TemplateVersion is one saved snapshot of an organization template.
type TemplateVersion struct {
	TemplateID string    `json:"template_id"`
	Version    int       `json:"version"`
	Name       string    `json:"name"`
	SavedBy    string    `json:"saved_by"`
	SavedAt    time.Time `json:"saved_at"`
	Note       string    `json:"note,omitempty"`
	Template   *Template `json:"template,omitempty"`
}

// Template categories offered by the library.
var TemplateCategories = []string{"finance", "operations", "legal", "sales", "marketing", "hr", "it", "compliance", "general"}

// TemplateInput is what a user sends to create or edit a template.
type TemplateInput struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Category    string              `json:"category"`
	Shared      *bool               `json:"shared,omitempty"`
	Params      []TemplateParam     `json:"params"`
	Objectives  []TemplateObjective `json:"objectives"`
	// BaseVersion, when set, must match the stored version (409 otherwise): two editors never overwrite each other.
	BaseVersion int    `json:"base_version,omitempty"`
	Note        string `json:"note,omitempty"`
}

// SaveOpts customize "save a project as a template".
type SaveOpts struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	Shared      *bool  `json:"shared,omitempty"`
}

// TemplateIssues is the answer of a validation.
type TemplateIssues struct {
	OK     bool    `json:"ok"`
	Issues []Issue `json:"issues"`
}

// PreviewRequest asks for the expanded plan of a template without creating a project.
type PreviewRequest struct {
	TemplateID string            `json:"template_id,omitempty"`
	Template   *TemplateInput    `json:"template,omitempty"` // an unsaved draft of the builder
	Params     map[string]string `json:"params,omitempty"`
	Goal       string            `json:"goal,omitempty"`
	Locale     string            `json:"locale,omitempty"`
}

// Preview is the expanded plan: size and estimate, before creating the project.
type Preview struct {
	Name       string            `json:"name"`
	Tasks      int               `json:"tasks"`
	Gates      int               `json:"gates"`
	Workflows  int               `json:"workflows"`
	Objectives int               `json:"objectives"`
	Nodes      int               `json:"nodes"`
	Levels     int               `json:"levels"` // longest dependency chain
	Parallel   int               `json:"parallel"`
	Agents     []string          `json:"agents"`
	Estimate   *Estimate         `json:"estimate,omitempty"`
	Issues     []Issue           `json:"issues"`
	Params     map[string]string `json:"params,omitempty"`
	Sample     []string          `json:"sample,omitempty"` // titles of the first work items
}

func (s *Service) actor(ctx context.Context) string { return application.ActorFrom(ctx, "user") }

func (s *Service) auditTemplate(ctx context.Context, action, id string, details map[string]any) {
	if s.cfg.Rec == nil {
		return
	}
	s.cfg.Rec.Audit(ctx, domain.AuditLog{Actor: s.actor(ctx), Action: action, Entity: "project_template", EntityID: id, Details: details})
}

func (s *Service) canSee(ctx context.Context, t Template) bool {
	return t.Builtin || t.Shared || t.CreatedBy == "" || t.CreatedBy == s.actor(ctx)
}

func (s *Service) canEdit(ctx context.Context, t Template) error {
	if t.Builtin {
		return fmt.Errorf("%w: a built-in template cannot be edited; duplicate it", domain.ErrInvalid)
	}
	if !s.canSee(ctx, t) {
		return fmt.Errorf("%w: template %q", domain.ErrNotFound, t.ID)
	}
	if !t.Shared && t.CreatedBy != "" && t.CreatedBy != s.actor(ctx) {
		return domain.ErrForbidden
	}
	return nil
}

// stripStats removes computed fields before a template is stored.
func stripStats(t Template) Template { t.Stats = nil; return t }

func (s *Service) validCategory(c string) string {
	if slices.Contains(TemplateCategories, c) {
		return c
	}
	return "general"
}

// GetTemplate returns one template of the library (built-in or organization).
func (s *Service) GetTemplate(ctx context.Context, id string) (Template, error) {
	locale := s.locale(ctx, "")
	if t, ok := builtinByID(id); ok {
		t.Stats = s.statsCached(t, locale)
		return t.resolved(locale), nil
	}
	t, err := s.cfg.Store.GetTemplate(ctx, s.org(ctx), id)
	if err != nil {
		return Template{}, err
	}
	if !s.canSee(ctx, t) {
		return Template{}, domain.ErrNotFound
	}
	t.Stats = statsOf(t, locale, s.cfg.Limits)
	return t.resolved(locale), nil
}

type statKey struct{ id, locale string }

func (s *Service) statsCached(t Template, locale string) *TemplateStat {
	k := statKey{t.ID, locale}
	s.mu.Lock()
	if v, ok := s.stats[k]; ok {
		s.mu.Unlock()
		return v
	}
	s.mu.Unlock()
	st := statsOf(t, locale, s.cfg.Limits)
	s.mu.Lock()
	if s.stats == nil {
		s.stats = map[statKey]*TemplateStat{}
	}
	s.stats[k] = st
	s.mu.Unlock()
	return st
}

// agentList returns the agent ids of the organization (empty when unknown).
func (s *Service) agentList(ctx context.Context) []string {
	ids, err := s.agentIDs(ctx)
	if err != nil {
		return nil
	}
	return ids
}

func (s *Service) templateFromInput(ctx context.Context, in TemplateInput, base Template) Template {
	t := base
	t.Name = strings.TrimSpace(in.Name)
	t.Description = strings.TrimSpace(in.Description)
	t.Category = s.validCategory(in.Category)
	t.Params = in.Params
	if t.Params == nil {
		t.Params = []TemplateParam{}
	}
	for i := range t.Params {
		t.Params[i].LabelKey, t.Params[i].DefaultKey = "", ""
		t.Params[i].Label = strings.TrimSpace(t.Params[i].Label)
		if t.Params[i].Label == "" {
			t.Params[i].Label = t.Params[i].Key
		}
	}
	t.Objectives = in.Objectives
	for oi := range t.Objectives {
		o := &t.Objectives[oi]
		o.TitleKey, o.Params = "", nil
		o.Title = strings.TrimSpace(o.Title)
		for wi := range o.Workflows {
			w := &o.Workflows[wi]
			w.TitleKey, w.Params = "", nil
			w.Title = strings.TrimSpace(w.Title)
			if w.Nodes == nil {
				w.Nodes = []TemplateNode{}
			}
			for ni := range w.Nodes {
				n := &w.Nodes[ni]
				n.TitleKey, n.Params = "", nil
				n.Title = strings.TrimSpace(n.Title)
				n.Description = strings.TrimSpace(n.Description)
				n.Acceptance = cleanAcceptance(n.Acceptance, MaxAcceptance)
				if n.Delegate != nil {
					n.Delegate.TitleKey = ""
				}
				if n.Kind == KindTask {
					n.Kind = ""
				}
			}
		}
		if o.Workflows == nil {
			o.Workflows = []TemplateWorkflow{}
		}
	}
	if in.Shared != nil {
		t.Shared = *in.Shared
	}
	t.Builtin, t.BudgetHint, t.Stats = false, nil, nil
	t.NameKey, t.DescriptionKey = "", ""
	t.Name, t.Description = truncRunes(t.Name, maxTplName), truncRunes(t.Description, maxTplDesc)
	// Keys that do not fit the format (a saved project) are rewritten, and titles are cut to their limits.
	normalizeKeys(&t)
	for oi := range t.Objectives {
		t.Objectives[oi].Title = truncRunes(t.Objectives[oi].Title, maxTplTitle)
		for wi := range t.Objectives[oi].Workflows {
			w := &t.Objectives[oi].Workflows[wi]
			w.Title = truncRunes(w.Title, maxTplTitle)
			for ni := range w.Nodes {
				n := &w.Nodes[ni]
				n.Title, n.Description = truncRunes(n.Title, maxTplTitle), truncRunes(n.Description, maxTplDesc)
			}
		}
	}
	_ = ctx
	return t
}

func issuesError(issues []Issue) error {
	var parts []string
	for _, i := range issues {
		if i.Severity == "error" {
			parts = append(parts, strings.TrimSpace(i.Code+" "+i.NodeID))
		}
		if len(parts) >= 6 {
			break
		}
	}
	return fmt.Errorf("%w: invalid template: %s", domain.ErrInvalid, strings.Join(parts, ", "))
}

// ValidateTemplate checks a template definition without saving it.
func (s *Service) ValidateTemplate(ctx context.Context, in TemplateInput) TemplateIssues {
	t := s.templateFromInput(ctx, in, Template{})
	issues := ValidateTemplate(t, s.agentList(ctx), s.cfg.Limits)
	if issues == nil {
		issues = []Issue{}
	}
	return TemplateIssues{OK: !hasErrors(issues), Issues: issues}
}

func (s *Service) saveVersion(ctx context.Context, t Template, note string) error {
	cp := stripStats(t)
	return s.cfg.Store.PutTemplateVersion(ctx, s.org(ctx), TemplateVersion{TemplateID: t.ID, Version: t.Version, Name: t.Name,
		SavedBy: s.actor(ctx), SavedAt: s.now(), Note: truncRunes(note, 200), Template: &cp})
}

// CreateTemplate stores a new organization template (version 1).
func (s *Service) CreateTemplate(ctx context.Context, in TemplateInput) (Template, error) {
	return s.createTemplate(ctx, in, "", "created", nil)
}

func (s *Service) createTemplate(ctx context.Context, in TemplateInput, forkedFrom, note string, skipAgents []string) (Template, error) {
	now := s.now()
	id := "tpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	t := s.templateFromInput(ctx, in, Template{ID: id, Key: "custom_" + id[4:], Version: 1, ForkedFrom: forkedFrom, Shared: true})
	t.CreatedBy, t.UpdatedBy, t.CreatedAt, t.UpdatedAt = s.actor(ctx), s.actor(ctx), &now, &now
	agents := s.agentList(ctx)
	if skipAgents != nil {
		agents = nil
	}
	if issues := ValidateTemplate(t, agents, s.cfg.Limits); hasErrors(issues) {
		return Template{}, issuesError(issues)
	}
	s.tplMu.Lock()
	defer s.tplMu.Unlock()
	if err := s.cfg.Store.PutTemplate(ctx, s.org(ctx), t); err != nil {
		return Template{}, err
	}
	if err := s.saveVersion(ctx, t, note); err != nil {
		return Template{}, err
	}
	s.auditTemplate(ctx, "template.created", t.ID, map[string]any{"name": t.Name, "forked_from": forkedFrom, "shared": t.Shared})
	t.Stats = statsOf(t, s.locale(ctx, ""), s.cfg.Limits)
	return t, nil
}

// UpdateTemplate saves a new version of an organization template.
func (s *Service) UpdateTemplate(ctx context.Context, id string, in TemplateInput) (Template, error) {
	s.tplMu.Lock()
	defer s.tplMu.Unlock()
	cur, err := s.cfg.Store.GetTemplate(ctx, s.org(ctx), id)
	if err != nil {
		return Template{}, err
	}
	if err := s.canEdit(ctx, cur); err != nil {
		return Template{}, err
	}
	if in.BaseVersion != 0 && in.BaseVersion != cur.Version {
		return Template{}, fmt.Errorf("%w: template_version_conflict: the template is at version %d", domain.ErrConflict, cur.Version)
	}
	return s.replaceTemplate(ctx, cur, in, in.Note, "template.updated")
}

func (s *Service) replaceTemplate(ctx context.Context, cur Template, in TemplateInput, note, action string) (Template, error) {
	next := s.templateFromInput(ctx, in, cur)
	next.Version = cur.Version + 1
	now := s.now()
	next.UpdatedBy, next.UpdatedAt = s.actor(ctx), &now
	if in.Shared == nil {
		next.Shared = cur.Shared
	}
	// Only the creator can take a template back to private.
	if cur.Shared && !next.Shared && cur.CreatedBy != "" && cur.CreatedBy != s.actor(ctx) {
		return Template{}, domain.ErrForbidden
	}
	if issues := ValidateTemplate(next, s.agentList(ctx), s.cfg.Limits); hasErrors(issues) {
		return Template{}, issuesError(issues)
	}
	if err := s.cfg.Store.PutTemplate(ctx, s.org(ctx), next); err != nil {
		return Template{}, err
	}
	if err := s.saveVersion(ctx, next, note); err != nil {
		return Template{}, err
	}
	s.auditTemplate(ctx, action, next.ID, map[string]any{"version": next.Version, "note": truncRunes(note, 200)})
	next.Stats = statsOf(next, s.locale(ctx, ""), s.cfg.Limits)
	return next, nil
}

func inputOf(t Template) TemplateInput {
	sh := t.Shared
	return TemplateInput{Name: t.Name, Description: t.Description, Category: t.Category, Shared: &sh, Params: t.Params, Objectives: t.Objectives}
}

// DeleteTemplate removes an organization template (admins and owners).
func (s *Service) DeleteTemplate(ctx context.Context, id string) error {
	if r := application.ActorRoleFrom(ctx, ""); r != "" && r != "admin" && r != "owner" {
		return domain.ErrForbidden
	}
	s.tplMu.Lock()
	defer s.tplMu.Unlock()
	cur, err := s.cfg.Store.GetTemplate(ctx, s.org(ctx), id)
	if err != nil {
		return err
	}
	if err := s.cfg.Store.DeleteTemplate(ctx, s.org(ctx), id); err != nil {
		return err
	}
	s.auditTemplate(ctx, "template.deleted", id, map[string]any{"name": cur.Name, "version": cur.Version})
	return nil
}

// DuplicateTemplate makes an editable copy of any template of the library.
// A built-in is turned into plain text (in the locale of the organization);
// a template that is generated by code (group close, cash close...) is copied at its default size.
func (s *Service) DuplicateTemplate(ctx context.Context, id, name string) (Template, error) {
	locale := s.locale(ctx, "")
	var src Template
	if b, ok := builtinByID(id); ok {
		x, _, err := expandedFor(b, "", nil, locale, s.cfg.Limits)
		if err != nil {
			return Template{}, err
		}
		x.Params = b.Params // keep the parameters; the structure is the one of the defaults
		if b.hasRepeat() {
			x = b // data templates keep their repeat blocks: the copy stays parametric
		}
		src = literalize(x, locale)
		if !b.hasRepeat() {
			src = dropUnusedParams(src) // the size parameters of generated templates no longer apply to the copy
		}
	} else if strings.HasPrefix(id, catalogPrefix) {
		key := strings.TrimPrefix(id, catalogPrefix)
		v, ok := s.cfg.Catalog.Workflow(key, locale)
		if !ok {
			return Template{}, domain.ErrNotFound
		}
		src = catalogTemplate(v)
	} else {
		cur, err := s.cfg.Store.GetTemplate(ctx, s.org(ctx), id)
		if err != nil {
			return Template{}, err
		}
		if !s.canSee(ctx, cur) {
			return Template{}, domain.ErrNotFound
		}
		src = cur
	}
	in := inputOf(src)
	in.Name = strings.TrimSpace(name)
	if in.Name == "" {
		in.Name = src.Name + " " + tr(locale, "tpl.copySuffix", nil)
		if !hasKey("tpl.copySuffix") {
			in.Name = src.Name + " (copy)"
		}
	}
	if in.Category == "" {
		in.Category = categoryOfBuiltin(src.Key)
	}
	sh := true
	in.Shared = &sh
	return s.createTemplate(ctx, in, id, "duplicate of "+id, []string{})
}

// literalize resolves the i18n keys of a template to plain text (placeholders of
// parameters like {city} stay) so it can be stored as an editable template.
func literalize(t Template, locale string) Template {
	out := t
	out.NameKey, out.DescriptionKey = "", ""
	out.Name, out.Description = tr(locale, t.NameKey, nil), tr(locale, t.DescriptionKey, nil)
	if t.NameKey == "" {
		out.Name = t.Name
	}
	if t.DescriptionKey == "" {
		out.Description = t.Description
	}
	out.Params = make([]TemplateParam, len(t.Params))
	for i, p := range t.Params {
		if p.LabelKey != "" {
			p.Label = tr(locale, p.LabelKey, nil)
		}
		if p.DefaultKey != "" {
			p.Default = tr(locale, p.DefaultKey, nil)
		}
		p.LabelKey, p.DefaultKey = "", ""
		if p.Label == "" {
			p.Label = p.Key
		}
		out.Params[i] = p
	}
	text := func(key, lit string, params map[string]string) string {
		if key != "" && hasKey(key) {
			return tr(locale, key, params)
		}
		if lit != "" {
			return lit
		}
		return key
	}
	out.Objectives = make([]TemplateObjective, len(t.Objectives))
	for oi, o := range t.Objectives {
		no := TemplateObjective{Key: o.Key, Title: text(o.TitleKey, o.Title, o.Params)}
		for _, w := range o.Workflows {
			nw := TemplateWorkflow{Key: w.Key, Title: text(w.TitleKey, w.Title, w.Params), Repeat: w.Repeat}
			for _, n := range w.Nodes {
				nn := n
				nn.Title, nn.TitleKey, nn.Params = text(n.TitleKey, n.Title, n.Params), "", nil
				if n.Delegate != nil {
					d := *n.Delegate
					d.Title, d.TitleKey = text(d.TitleKey, d.Title, nil), ""
					nn.Delegate = &d
				}
				nw.Nodes = append(nw.Nodes, nn)
			}
			no.Workflows = append(no.Workflows, nw)
		}
		out.Objectives[oi] = no
	}
	return out
}

// TemplateVersions lists the history of an organization template (newest first, without content).
func (s *Service) TemplateVersions(ctx context.Context, id string) ([]TemplateVersion, error) {
	cur, err := s.cfg.Store.GetTemplate(ctx, s.org(ctx), id)
	if err != nil {
		return nil, err
	}
	if !s.canSee(ctx, cur) {
		return nil, domain.ErrNotFound
	}
	vs, err := s.cfg.Store.ListTemplateVersions(ctx, s.org(ctx), id)
	if err != nil {
		return nil, err
	}
	for i := range vs {
		vs[i].Template = nil
	}
	return vs, nil
}

// RestoreTemplate saves the content of an older version as a new version.
func (s *Service) RestoreTemplate(ctx context.Context, id string, version int) (Template, error) {
	s.tplMu.Lock()
	defer s.tplMu.Unlock()
	cur, err := s.cfg.Store.GetTemplate(ctx, s.org(ctx), id)
	if err != nil {
		return Template{}, err
	}
	if err := s.canEdit(ctx, cur); err != nil {
		return Template{}, err
	}
	vs, err := s.cfg.Store.ListTemplateVersions(ctx, s.org(ctx), id)
	if err != nil {
		return Template{}, err
	}
	i := slices.IndexFunc(vs, func(v TemplateVersion) bool { return v.Version == version })
	if i < 0 || vs[i].Template == nil {
		return Template{}, fmt.Errorf("%w: version %d of template %q", domain.ErrNotFound, version, id)
	}
	in := inputOf(*vs[i].Template)
	sh := cur.Shared
	in.Shared = &sh
	return s.replaceTemplate(ctx, cur, in, fmt.Sprintf("restored version %d", version), "template.restored")
}

// SaveAsTemplateWith is SaveAsTemplate with a name, category and sharing.
func (s *Service) SaveAsTemplateWith(ctx context.Context, projectID string, o SaveOpts) (Template, error) {
	rec, err := s.get(ctx, projectID)
	if err != nil {
		return Template{}, err
	}
	src := templateFromRecord(rec, "", "")
	in := inputOf(src)
	if n := strings.TrimSpace(o.Name); n != "" {
		in.Name = n
	}
	if d := strings.TrimSpace(o.Description); d != "" {
		in.Description = d
	}
	in.Category = o.Category
	in.Shared = o.Shared // nil: shared with the organization
	in.Params = []TemplateParam{}
	if in.Name == "" {
		in.Name = rec.Name
	}
	t, err := s.createTemplate(ctx, in, "", "saved from project "+rec.ID, []string{})
	if err != nil {
		return t, err
	}
	s.audit(ctx, "project.saved_as_template", projectID, map[string]any{"template": t.ID})
	return t, nil
}

// PreviewTemplate expands a template (stored, built-in or an unsaved draft) with
// parameters and reports its size and estimate, without creating anything.
func (s *Service) PreviewTemplate(ctx context.Context, req PreviewRequest) (Preview, error) {
	locale := s.locale(ctx, req.Locale)
	var tpl Template
	switch {
	case req.Template != nil:
		tpl = s.templateFromInput(ctx, *req.Template, Template{ID: "tpl-preview", Key: "preview"})
		now := s.now()
		tpl.CreatedAt = &now // organization template: the data expansion applies
		if issues := ValidateTemplate(tpl, s.agentList(ctx), s.cfg.Limits); hasErrors(issues) {
			return Preview{Issues: issues}, nil
		}
	case req.TemplateID != "":
		in := NewProject{TemplateID: req.TemplateID, Params: req.Params, Goal: req.Goal}
		t, _, _, err := s.pickTemplate(ctx, in, strings.TrimSpace(req.Goal), locale)
		if err != nil {
			return Preview{}, err
		}
		if !s.canSee(ctx, t) {
			return Preview{}, domain.ErrNotFound
		}
		tpl = t
	default:
		return Preview{}, fmt.Errorf("%w: template_id or template is required", domain.ErrInvalid)
	}
	inst, err := tpl.instantiateWith(s.cfg.Limits, "prv", req.Goal, req.Params, locale)
	if err == nil {
		err = s.cfg.Limits.checkSize(inst.Nodes)
	}
	if err != nil {
		return Preview{}, err
	}
	est := estimatePlanWith(inst.Nodes, inst.Objectives, s.estimator(ctx, nil))
	p := Preview{Name: inst.Name, Estimate: est, Params: req.Params, Agents: []string{}, Issues: validatePlan(inst.Nodes, s.agentList(ctx))}
	agents := map[string]bool{}
	byLevel := map[int]int{}
	for _, n := range inst.Nodes {
		if n.isGroup() {
			p.Workflows++
			continue
		}
		p.Tasks++
		if n.Kind == KindGate {
			p.Gates++
		}
		if n.AgentID != nil {
			agents[*n.AgentID] = true
		}
		byLevel[n.DagLevel]++
		p.Levels = max(p.Levels, n.DagLevel+1)
		p.Parallel = max(p.Parallel, byLevel[n.DagLevel])
		if len(p.Sample) < 6 {
			p.Sample = append(p.Sample, n.Title)
		}
	}
	for a := range agents {
		p.Agents = append(p.Agents, a)
	}
	slices.Sort(p.Agents)
	p.Objectives, p.Nodes = len(inst.Objectives), len(inst.Nodes)
	return p, nil
}

func categoryOfBuiltin(key string) string {
	switch key {
	case "financial_close", "cash_close", "group_consolidated_close", "annual_budget":
		return "finance"
	case "branch_opening", "market_expansion", "product_launch":
		return "operations"
	case "acquisition_due_diligence", "post_merger_integration":
		return "legal"
	case "compliance_program", "year_end_audit":
		return "compliance"
	case "erp_migration":
		return "it"
	}
	return "general"
}

// dropUnusedParams removes the parameters no text refers to.
func dropUnusedParams(t Template) Template {
	var all strings.Builder
	all.WriteString(t.Name + " " + t.Description)
	for _, o := range t.Objectives {
		all.WriteString(" " + o.Title)
		for _, w := range o.Workflows {
			all.WriteString(" " + w.Title)
			for _, n := range w.Nodes {
				all.WriteString(" " + n.Title + " " + n.Description)
			}
		}
	}
	txt := all.String()
	kept := []TemplateParam{}
	for _, p := range t.Params {
		if strings.Contains(txt, "{"+p.Key+"}") {
			kept = append(kept, p)
		}
	}
	t.Params = kept
	return t
}
