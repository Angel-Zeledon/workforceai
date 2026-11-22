package positions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/offices"
)

// Actor is the person asking. Admin: owner or administrator. Open: the demo without accounts (no people to tell
// apart: everything is visible and manageable, like the rest of the demo).
type Actor struct {
	ID    string
	Admin bool
	Open  bool
}

// SkillInfo is a skill of the library as positions see it.
type SkillInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	OfficeID    string `json:"office_id"`
}

// Catalog lists the live skills of an organization (every one, the department ones included).
type Catalog interface {
	Skills(ctx context.Context, org string) ([]SkillInfo, error)
}

// Folders knows the drive side of a department: its bounds and whether a folder exists.
type Folders interface {
	Bounds(ctx context.Context, org, officeID string) (Bounds, error)
	FolderExists(ctx context.Context, org, path string) (bool, error)
}

// Config wires the service.
type Config struct {
	Store Store
	// Model is the offices model (departments, members, responsibles, the office of each agent).
	Model func(ctx context.Context, org string) (*offices.Model, error)
	// Skills (optional): without it a position carries no skills.
	Skills Catalog
	// Folders (optional): without it a position carries no library.
	Folders Folders
	// Audit records every change (nil = none).
	Audit func(ctx context.Context, org, actor, action, id string, d map[string]any)
	OrgID string
	Now   func() time.Time
	Log   *slog.Logger
}

// Service is the positions use-case layer.
type Service struct {
	cfg   Config
	locks sync.Map // org -> *sync.Mutex: changes of one organization run one at a time (a change cuts its subordinates)
}

// New builds the service.
func New(cfg Config) *Service {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Service{cfg: cfg}
}

func (s *Service) org(ctx context.Context) string { return application.OrgFrom(ctx, s.cfg.OrgID) }

func (s *Service) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) lock(org string) func() {
	m, _ := s.locks.LoadOrStore(org, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *Service) audit(ctx context.Context, org string, a Actor, action, id string, d map[string]any) {
	if s.cfg.Audit != nil {
		s.cfg.Audit(ctx, org, a.ID, action, id, d)
	}
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "pos_" + hex.EncodeToString(b)
}

// ---- who sees and who manages ----

func (s *Service) model(ctx context.Context, org string) (*offices.Model, error) {
	if s.cfg.Model == nil {
		return nil, forbidden("office_unavailable", "departments are not available")
	}
	m, err := s.cfg.Model(ctx, org)
	if err != nil || m == nil {
		return nil, forbidden("office_unavailable", "the departments cannot be read right now; try again")
	}
	return m, nil
}

// office loads a department the actor may look at: General is not a department, and a private one answers as if it
// did not exist to whoever is not a member (administrators included, D3).
func (s *Service) office(ctx context.Context, org string, a Actor, officeID string) (*offices.Model, offices.Office, error) {
	m, err := s.model(ctx, org)
	if err != nil {
		return nil, offices.Office{}, err
	}
	o, ok := m.Office(officeID)
	if !ok || o.General {
		return nil, offices.Office{}, notFound("department not found")
	}
	if !a.Open && o.Visibility == offices.Private && !m.IsMember(a.ID, officeID) {
		return nil, offices.Office{}, notFound("department not found")
	}
	return m, o, nil
}

// canManage: administrators and the responsibles of the department (the demo: everybody).
func canManage(m *offices.Model, a Actor, officeID string) bool {
	return a.Open || a.Admin || m.IsLead(a.ID, officeID)
}

// person: only people change positions; an agent, a token or the platform never does.
func person(a Actor) error {
	if a.Open || application.PersonID(a.ID) != "" {
		return nil
	}
	return forbidden("humans_only", "only a person changes positions")
}

func (s *Service) manage(ctx context.Context, org string, a Actor, officeID string) (*offices.Model, offices.Office, error) {
	if err := person(a); err != nil {
		return nil, offices.Office{}, err
	}
	m, o, err := s.office(ctx, org, a, officeID)
	if err != nil {
		return nil, o, err
	}
	if !canManage(m, a, officeID) {
		return nil, o, forbidden("forbidden", "only an administrator or the responsible of the department changes its positions")
	}
	return m, o, nil
}

func (s *Service) bounds(ctx context.Context, org, officeID string) (Bounds, error) {
	if s.cfg.Folders == nil {
		return Bounds{Shared: "/" + drive.RootShared}, nil
	}
	b, err := s.cfg.Folders.Bounds(ctx, org, officeID)
	if err != nil {
		return Bounds{}, err
	}
	if b.Shared == "" {
		b.Shared = "/" + drive.RootShared
	}
	return b, nil
}

// ---- listing ----

// AgentRef is an agent of the department and the position it holds ("" none).
type AgentRef struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PositionID string `json:"position_id"`
}

// OfficeView is the "Puestos" screen of a department.
type OfficeView struct {
	Office    offices.Office
	CanManage bool
	Positions []Position
	Agents    []AgentRef
	Skills    []SkillInfo
	Bounds    Bounds
}

// agentsOf lists the agents of a department.
func agentsOf(m *offices.Model, officeID string) []offices.AgentRef {
	var out []offices.AgentRef
	for _, a := range m.Agents() {
		if m.Agent(a.ID).OfficeID == officeID {
			out = append(out, a)
		}
	}
	return out
}

// valid keeps the assignments still in force: the agent is in the department it got the position in.
func valid(m *offices.Model, as []Assignment, byID map[string]Position) map[string]string {
	out := map[string]string{}
	for _, x := range as {
		p, ok := byID[x.PositionID]
		if ok && m.Agent(x.AgentID).OfficeID == x.OfficeID && p.OfficeID == x.OfficeID {
			out[x.AgentID] = x.PositionID
		}
	}
	return out
}

func (s *Service) positionsOf(ctx context.Context, org string, m *offices.Model, officeID string) ([]Position, map[string]string, error) {
	ps, err := s.cfg.Store.ListPositions(ctx, org, officeID)
	if err != nil {
		return nil, nil, err
	}
	as, err := s.cfg.Store.ListAssignments(ctx, org)
	if err != nil {
		return nil, nil, err
	}
	byID := map[string]Position{}
	for _, p := range ps {
		byID[p.ID] = p
	}
	held := valid(m, as, byID)
	for i := range ps {
		ps[i].Occupants = []string{}
		for agent, pid := range held {
			if pid == ps[i].ID {
				ps[i].Occupants = append(ps[i].Occupants, agent)
			}
		}
		sort.Strings(ps[i].Occupants)
	}
	return ps, held, nil
}

// skillsFor lists the skills a position of the department may carry: the company's and the department's own.
func (s *Service) skillsFor(ctx context.Context, org, officeID string) ([]SkillInfo, error) {
	if s.cfg.Skills == nil {
		return []SkillInfo{}, nil
	}
	all, err := s.cfg.Skills.Skills(ctx, org)
	if err != nil {
		return nil, err
	}
	out := []SkillInfo{}
	for _, sk := range all {
		if sk.OfficeID == "" || sk.OfficeID == officeID {
			out = append(out, sk)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// List is the positions screen of a department.
func (s *Service) List(ctx context.Context, a Actor, officeID string) (OfficeView, error) {
	org := s.org(ctx)
	m, o, err := s.office(ctx, org, a, officeID)
	if err != nil {
		return OfficeView{}, err
	}
	ps, held, err := s.positionsOf(ctx, org, m, officeID)
	if err != nil {
		return OfficeView{}, err
	}
	v := OfficeView{Office: o, CanManage: canManage(m, a, officeID) && person(a) == nil, Positions: ps, Agents: []AgentRef{}}
	for _, ag := range agentsOf(m, officeID) {
		v.Agents = append(v.Agents, AgentRef{ID: ag.ID, Name: ag.Name, PositionID: held[ag.ID]})
	}
	if v.Skills, err = s.skillsFor(ctx, org, officeID); err != nil {
		return OfficeView{}, err
	}
	if v.Bounds, err = s.bounds(ctx, org, officeID); err != nil {
		return OfficeView{}, err
	}
	if v.Bounds.SharedIn == nil {
		v.Bounds.SharedIn = []string{}
	}
	return v, nil
}

// ---- create, change, delete ----

// Input is what a person writes on a position.
type Input struct {
	Name      string   `json:"name"`
	ReportsTo string   `json:"reports_to"`
	Skills    []string `json:"skills"`
	Library   []Folder `json:"library"`
	Template  string   `json:"template"`
}

// normalize validates an input against the department and returns the position to store (without ids or dates).
func (s *Service) normalize(ctx context.Context, org, officeID, selfID string, in Input, all []Position, b Bounds) (Position, error) {
	p := Position{OfficeID: officeID, Name: strings.TrimSpace(in.Name), ReportsTo: strings.TrimSpace(in.ReportsTo), Template: strings.TrimSpace(in.Template)}
	if p.Name == "" {
		return p, invalid("name_required", "a position needs a name")
	}
	if utf8.RuneCountInString(p.Name) > MaxName {
		return p, invalid("name_too_long", "the name is too long")
	}
	for _, x := range all {
		if x.ID != selfID && strings.EqualFold(x.Name, p.Name) {
			return p, invalid("name_taken", "the department already has a position with that name")
		}
	}
	if !validTemplate(p.Template) {
		return p, invalid("template_invalid", "unknown template")
	}
	// the superior: another position of the same department, never itself nor one of its own subordinates
	if p.ReportsTo != "" {
		byID := map[string]Position{}
		for _, x := range all {
			byID[x.ID] = x
		}
		if _, ok := byID[p.ReportsTo]; !ok || p.ReportsTo == selfID {
			return p, invalid("reports_to_invalid", "the superior must be another position of the same department")
		}
		for id, n := p.ReportsTo, 0; id != "" && n <= len(all); n++ {
			if id == selfID {
				return p, invalid("cycle", "a position cannot report to one of its own subordinates")
			}
			id = byID[id].ReportsTo
		}
	}
	// skills: the company's or this department's
	if len(in.Skills) > MaxSkills {
		return p, invalid("too_many_skills", "a position carries at most %d skills", MaxSkills)
	}
	p.Skills = []string{}
	if len(in.Skills) > 0 {
		allowed, err := s.skillsFor(ctx, org, officeID)
		if err != nil {
			return p, err
		}
		for _, id := range in.Skills {
			id = strings.TrimSpace(id)
			if slices.Contains(p.Skills, id) {
				continue
			}
			// a skill of another department answers like one that does not exist (D3: it is not revealed)
			if !slices.ContainsFunc(allowed, func(x SkillInfo) bool { return x.ID == id }) {
				return p, invalid("skill_invalid", "a skill of the position does not exist or belongs to another department")
			}
			p.Skills = append(p.Skills, id)
		}
		sort.Strings(p.Skills)
	}
	// library: inside the department, /Shared or what is shared with it (read only there)
	if len(in.Library) > MaxFolders {
		return p, invalid("too_many_folders", "a library has at most %d folders", MaxFolders)
	}
	p.Library = []Folder{}
	for _, f := range in.Library {
		c, err := drive.CleanPath(f.Path)
		if err != nil || c == "/" {
			return p, invalid("folder_invalid", "a folder of the library is not valid")
		}
		level := strings.ToLower(strings.TrimSpace(f.Level))
		if level != LevelNone && level != LevelRead && level != LevelEdit {
			return p, invalid("folder_invalid", "a folder level must be read, edit or none")
		}
		switch b.Within(c) {
		case "":
			return p, invalid("folder_outside", "the library can only hold folders of the department, /Shared or shared with the department")
		case kindSharedIn:
			if level == LevelEdit {
				return p, invalid("folder_level_invalid", "what another department shares can only be read")
			}
		}
		if slices.ContainsFunc(p.Library, func(x Folder) bool { return strings.EqualFold(x.Path, c) }) {
			return p, invalid("folder_invalid", "a folder appears twice in the library")
		}
		if s.cfg.Folders == nil {
			return p, invalid("folder_invalid", "folders are not available")
		}
		ok, err := s.cfg.Folders.FolderExists(ctx, org, c)
		if err != nil {
			return p, err
		}
		if !ok {
			return p, invalid("folder_invalid", "a folder of the library does not exist")
		}
		p.Library = append(p.Library, Folder{Path: c, Level: level})
	}
	sort.SliceStable(p.Library, func(i, j int) bool {
		if di, dj := depth(p.Library[i].Path), depth(p.Library[j].Path); di != dj {
			return di < dj
		}
		return strings.ToLower(p.Library[i].Path) < strings.ToLower(p.Library[j].Path)
	})
	return p, nil
}

func exceedsErr(ex Excess) error {
	return &Error{Code: "exceeds_superior", Kind: domain.ErrInvalid, Msg: "a position cannot have more than the position it reports to", Details: ex}
}

// cascade cuts the subordinates (and theirs) of a changed position to what it now has. It returns the positions that
// change and what each loses.
func cascade(all []Position, root Position, b Bounds) ([]Position, Impact) {
	byID := map[string]Position{root.ID: root}
	kids := map[string][]Position{}
	for _, p := range all {
		if p.ID != root.ID {
			byID[p.ID] = p
			kids[p.ReportsTo] = append(kids[p.ReportsTo], p)
		}
	}
	imp := Impact{Changed: []Change{}}
	var changed []Position
	queue := []Position{root}
	for n := 0; len(queue) > 0 && n <= len(all); n++ {
		sup := queue[0]
		queue = queue[1:]
		for _, c := range kids[sup.ID] {
			if Exceeds(sup.Package(), c.Package(), b).Empty() {
				continue
			}
			pkg, lostSkills, lostFolders := Clamp(sup.Package(), c.Package(), b)
			c.Skills, c.Library = pkg.Skills, pkg.Library
			changed = append(changed, c)
			imp.Changed = append(imp.Changed, Change{PositionID: c.ID, Name: c.Name, SkillsRemoved: lostSkills, Folders: lostFolders})
			queue = append(queue, c)
		}
	}
	return changed, imp
}

// Create adds a position to a department. A position that reports to another must fit inside it.
func (s *Service) Create(ctx context.Context, a Actor, officeID string, in Input) (Position, error) {
	org := s.org(ctx)
	defer s.lock(org)()
	if _, _, err := s.manage(ctx, org, a, officeID); err != nil {
		return Position{}, err
	}
	all, err := s.cfg.Store.ListPositions(ctx, org, officeID)
	if err != nil {
		return Position{}, err
	}
	if len(all) >= MaxPositionsPerOffice {
		return Position{}, invalid("too_many", "a department has at most %d positions", MaxPositionsPerOffice)
	}
	b, err := s.bounds(ctx, org, officeID)
	if err != nil {
		return Position{}, err
	}
	p, err := s.normalize(ctx, org, officeID, "", in, all, b)
	if err != nil {
		return Position{}, err
	}
	if p.ReportsTo != "" {
		sup := find(all, p.ReportsTo)
		if ex := Exceeds(sup.Package(), p.Package(), b); !ex.Empty() {
			return Position{}, exceedsErr(ex)
		}
	}
	now := s.now()
	p.ID, p.CreatedBy, p.CreatedAt, p.UpdatedBy, p.UpdatedAt = newID(), a.ID, now, a.ID, now
	if err := s.cfg.Store.SavePositions(ctx, org, []Position{p}); err != nil {
		return Position{}, err
	}
	s.audit(ctx, org, a, "position.created", p.ID, map[string]any{"office_id": officeID, "name": p.Name, "reports_to": p.ReportsTo,
		"template": p.Template, "skills": p.Skills, "library": p.Library})
	p.Occupants = []string{}
	return p, nil
}

func find(all []Position, id string) Position {
	for _, p := range all {
		if p.ID == id {
			return p
		}
	}
	return Position{}
}

// Update changes a position. It must still fit inside its superior; its subordinates are cut to what it now has
// (Impact says what each loses). With dryRun nothing is saved: the screen asks before cutting.
func (s *Service) Update(ctx context.Context, a Actor, officeID, id string, in Input, dryRun bool) (Position, Impact, error) {
	org := s.org(ctx)
	defer s.lock(org)()
	m, _, err := s.manage(ctx, org, a, officeID)
	if err != nil {
		return Position{}, Impact{}, err
	}
	all, _, err := s.positionsOf(ctx, org, m, officeID)
	if err != nil {
		return Position{}, Impact{}, err
	}
	cur := find(all, id)
	if cur.ID == "" {
		return Position{}, Impact{}, notFound("position not found")
	}
	b, err := s.bounds(ctx, org, officeID)
	if err != nil {
		return Position{}, Impact{}, err
	}
	p, err := s.normalize(ctx, org, officeID, id, in, all, b)
	if err != nil {
		return Position{}, Impact{}, err
	}
	if p.ReportsTo != "" {
		if ex := Exceeds(find(all, p.ReportsTo).Package(), p.Package(), b); !ex.Empty() {
			return Position{}, Impact{}, exceedsErr(ex)
		}
	}
	if in.Template == "" {
		p.Template = cur.Template // a template marks where a position came from; editing it keeps the mark
	}
	p.ID, p.CreatedBy, p.CreatedAt, p.UpdatedBy, p.UpdatedAt = cur.ID, cur.CreatedBy, cur.CreatedAt, a.ID, s.now()
	changed, imp := cascade(all, p, b)
	p.Occupants = cur.Occupants
	if dryRun {
		return p, imp, nil
	}
	save := append([]Position{p}, changed...)
	for i := range save {
		save[i].Occupants = nil
		if i > 0 {
			save[i].UpdatedBy, save[i].UpdatedAt = a.ID, p.UpdatedAt
		}
	}
	if err := s.cfg.Store.SavePositions(ctx, org, save); err != nil {
		return Position{}, Impact{}, err
	}
	s.audit(ctx, org, a, "position.updated", p.ID, map[string]any{"office_id": officeID, "name": p.Name, "reports_to": p.ReportsTo,
		"skills": p.Skills, "library": p.Library, "subordinates_cut": len(imp.Changed)})
	for _, c := range imp.Changed {
		s.audit(ctx, org, a, "position.cut", c.PositionID, map[string]any{"office_id": officeID, "name": c.Name, "because_of": p.ID,
			"skills_removed": c.SkillsRemoved, "folders": c.Folders})
	}
	return p, imp, nil
}

// Delete removes a position: its subordinates report to its superior (they already fit inside it) and the agents
// that held it hold nothing.
func (s *Service) Delete(ctx context.Context, a Actor, officeID, id string) error {
	org := s.org(ctx)
	defer s.lock(org)()
	m, _, err := s.manage(ctx, org, a, officeID)
	if err != nil {
		return err
	}
	all, _, err := s.positionsOf(ctx, org, m, officeID)
	if err != nil {
		return err
	}
	p := find(all, id)
	if p.ID == "" {
		return notFound("position not found")
	}
	if err := s.cfg.Store.DeletePosition(ctx, org, id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return notFound("position not found")
		}
		return err
	}
	s.audit(ctx, org, a, "position.deleted", id, map[string]any{"office_id": officeID, "name": p.Name, "occupants": p.Occupants})
	return nil
}

// Templates proposes the starter positions of a department; the subordinates' proposal is already cut to the
// department's manager (the first position made from the manager template, or the first one at the top).
func (s *Service) Templates(ctx context.Context, a Actor, officeID string) ([]Template, error) {
	org := s.org(ctx)
	if _, _, err := s.office(ctx, org, a, officeID); err != nil {
		return nil, err
	}
	all, err := s.cfg.Store.ListPositions(ctx, org, officeID)
	if err != nil {
		return nil, err
	}
	b, err := s.bounds(ctx, org, officeID)
	if err != nil {
		return nil, err
	}
	allowed, err := s.skillsFor(ctx, org, officeID)
	if err != nil {
		return nil, err
	}
	ids := func(names []string) []string {
		out := []string{}
		for _, n := range names {
			if i := slices.IndexFunc(allowed, func(x SkillInfo) bool { return x.Name == n }); i >= 0 {
				out = append(out, allowed[i].ID)
			}
		}
		sort.Strings(out)
		return out
	}
	var boss Position
	for _, p := range all {
		if p.Template == TemplateManager {
			boss = p
			break
		}
	}
	if boss.ID == "" {
		for _, p := range all {
			if p.ReportsTo == "" {
				boss = p
				break
			}
		}
	}
	out := []Template{}
	for _, k := range templateKeys {
		t := Template{Key: k, Proposal: starter(k, b, ids)}
		if k != TemplateManager && boss.ID != "" {
			t.ReportsTo = boss.ID
			if !Exceeds(boss.Package(), t.Proposal, b).Empty() {
				t.Proposal, _, _ = Clamp(boss.Package(), t.Proposal, b)
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// ---- who holds a position ----

// AgentView is "Su puesto" of an agent.
type AgentView struct {
	Position   *Position  `json:"position"`
	OfficeID   string     `json:"office_id"`
	OfficeName string     `json:"office_name"`
	CanManage  bool       `json:"can_manage"`
	Positions  []Position `json:"positions"`
}

// agent loads an agent the actor may look at: an agent of a private department answers as if it did not exist to
// whoever is not a member.
func (s *Service) agent(ctx context.Context, org string, a Actor, agentID string) (*offices.Model, string, error) {
	m, err := s.model(ctx, org)
	if err != nil {
		return nil, "", err
	}
	if !slices.ContainsFunc(m.Agents(), func(x offices.AgentRef) bool { return x.ID == agentID }) {
		return nil, "", notFound("agent not found")
	}
	if !a.Open && !m.CanSeeWork(offices.Viewer{UserID: a.ID, Admin: a.Admin}, agentID) {
		return nil, "", notFound("agent not found")
	}
	return m, m.Agent(agentID).OfficeID, nil
}

// Agent is the position of an agent and the positions of its department.
func (s *Service) Agent(ctx context.Context, a Actor, agentID string) (AgentView, error) {
	org := s.org(ctx)
	m, office, err := s.agent(ctx, org, a, agentID)
	if err != nil {
		return AgentView{}, err
	}
	v := AgentView{OfficeID: office, Positions: []Position{}}
	o, ok := m.Office(office)
	if !ok || o.General {
		return v, nil // General is not a department: no positions
	}
	v.OfficeName, v.CanManage = o.Name, canManage(m, a, office) && person(a) == nil
	ps, held, err := s.positionsOf(ctx, org, m, office)
	if err != nil {
		return AgentView{}, err
	}
	v.Positions = ps
	if pid := held[agentID]; pid != "" {
		p := find(ps, pid)
		v.Position = &p
	}
	return v, nil
}

// Assign gives an agent a position of its own department (one per agent: it replaces the one it had).
func (s *Service) Assign(ctx context.Context, a Actor, agentID, positionID string) error {
	org := s.org(ctx)
	defer s.lock(org)()
	_, office, err := s.agent(ctx, org, a, agentID)
	if err != nil {
		return err
	}
	p, err := s.cfg.Store.GetPosition(ctx, org, positionID)
	if errors.Is(err, domain.ErrNotFound) {
		return notFound("position not found")
	}
	if err != nil {
		return err
	}
	if _, _, err := s.manage(ctx, org, a, p.OfficeID); err != nil {
		return err
	}
	if p.OfficeID != office {
		return invalid("position_other_office", "an agent can only hold a position of its own department")
	}
	prev := ""
	if x, err := s.cfg.Store.AssignmentOf(ctx, org, agentID); err == nil {
		prev = x.PositionID
	}
	if err := s.cfg.Store.SetAssignment(ctx, org, Assignment{AgentID: agentID, PositionID: p.ID, OfficeID: office, AssignedBy: a.ID, AssignedAt: s.now()}); err != nil {
		return err
	}
	s.audit(ctx, org, a, "position.assigned", p.ID, map[string]any{"office_id": office, "agent_id": agentID, "name": p.Name, "previous": prev})
	return nil
}

// Unassign leaves an agent without a position (it keeps what its trade and its own rules give it).
func (s *Service) Unassign(ctx context.Context, a Actor, agentID string) error {
	org := s.org(ctx)
	defer s.lock(org)()
	_, office, err := s.agent(ctx, org, a, agentID)
	if err != nil {
		return err
	}
	x, err := s.cfg.Store.AssignmentOf(ctx, org, agentID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// whoever manages the department the agent is in now decides (an agent that moved already lost it at runtime)
	if _, _, err := s.manage(ctx, org, a, office); err != nil {
		return err
	}
	if err := s.cfg.Store.DeleteAssignment(ctx, org, agentID); err != nil {
		return err
	}
	s.audit(ctx, org, a, "position.unassigned", x.PositionID, map[string]any{"office_id": x.OfficeID, "agent_id": agentID})
	return nil
}

// ---- runtime: what the position gives the agent right now ----

// PositionOf is the position an agent holds right now. An agent moved to another department loses it here, at once
// (the assignment is removed and audited). Without departments (no model) nothing is held. An error means "unknown":
// callers fail closed.
func (s *Service) PositionOf(ctx context.Context, org, agentID string) (Position, bool, error) {
	x, err := s.cfg.Store.AssignmentOf(ctx, org, agentID)
	if errors.Is(err, domain.ErrNotFound) {
		return Position{}, false, nil
	}
	if err != nil {
		return Position{}, false, err
	}
	if s.cfg.Model == nil {
		return Position{}, false, nil
	}
	m, err := s.cfg.Model(ctx, org)
	if err != nil || m == nil {
		return Position{}, false, errors.Join(errors.New("positions: offices model unavailable"), err)
	}
	if m.Agent(agentID).OfficeID != x.OfficeID {
		if err := s.cfg.Store.DeleteAssignment(ctx, org, agentID); err == nil {
			s.audit(ctx, org, Actor{ID: "system"}, "position.unassigned", x.PositionID, map[string]any{"office_id": x.OfficeID, "agent_id": agentID, "reason": "moved_department"})
		}
		return Position{}, false, nil
	}
	p, err := s.cfg.Store.GetPosition(ctx, org, x.PositionID)
	if errors.Is(err, domain.ErrNotFound) {
		return Position{}, false, nil
	}
	if err != nil {
		return Position{}, false, err
	}
	if p.OfficeID != x.OfficeID {
		return Position{}, false, nil
	}
	return p, true, nil
}

// AgentLibrary implements drive.AgentLibraries: the library of the agent's position (none = no library).
func (s *Service) AgentLibrary(ctx context.Context, org, agentID string) (drive.Library, error) {
	p, ok, err := s.PositionOf(ctx, org, agentID)
	if err != nil || !ok {
		return drive.Library{}, err
	}
	lib := drive.Library{PositionID: p.ID, PositionName: p.Name}
	for _, f := range p.Library {
		lib.Rules = append(lib.Rules, drive.LibraryRule{Path: f.Path, Level: f.Level})
	}
	return lib, nil
}

// PositionSkills is the name and the skills of the agent's position (skills.Config.PositionSkills).
func (s *Service) PositionSkills(ctx context.Context, org, agentID string) (string, []string, error) {
	p, ok, err := s.PositionOf(ctx, org, agentID)
	if err != nil || !ok {
		return "", nil, err
	}
	return p.Name, p.Skills, nil
}
