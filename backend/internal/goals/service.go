package goals

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/offices"
)

// Config wires the service.
type Config struct {
	Store Store
	// Offices returns the departments model; nil = no departments (the open demo): every goal is the company's.
	Offices func(ctx context.Context, org string) (*offices.Model, error)
	// Audit records a change (metadata only, never the texts).
	Audit func(ctx context.Context, org, actor, action, id string, details map[string]any)
	// Emit publishes the live event of a goal to audience (nil = the whole organization).
	Emit func(ctx context.Context, org, typ string, g Goal, audience []string)
	// Clean makes a text people wrote safe to hand an agent (the cleaner of the memory notes). nil = as is.
	Clean func(string) string
	// Accept refuses a text that reads like instructions for the agents (the injection check of the memory notes):
	// false refuses the goal. nil = everything is accepted.
	Accept func(string) bool
	Now    func() time.Time
}

// Service holds the rules of the goals.
type Service struct {
	cfg Config
	// mu serializes the active-goal cap within one process (with several replicas the cap may be crossed by a race).
	mu sync.Mutex
}

var _ application.GoalSource = (*Service)(nil)

func New(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{cfg: cfg}
}

func invalid(msg string) error   { return fmt.Errorf("%w: %s", domain.ErrInvalid, msg) }
func forbidden(msg string) error { return fmt.Errorf("%w: %s", domain.ErrForbidden, msg) }
func notFound() error            { return fmt.Errorf("%w: goal", domain.ErrNotFound) }

// view is what the service knows of the departments for one call: the model, nothing (no departments, nothing is
// filtered) or a failure (the departments' goals stay closed).
type view struct {
	m      *offices.Model
	failed bool
}

func (s *Service) model(ctx context.Context, org string) view {
	if s.cfg.Offices == nil {
		return view{}
	}
	m, err := s.cfg.Offices(ctx, org)
	if err != nil || m == nil {
		return view{failed: true}
	}
	return view{m: m}
}

// canSee: the actor may know the goal exists (D3: a private department's goal only its members).
func (v view) canSee(a Actor, g Goal) bool {
	if g.OfficeID == "" || a.Open {
		return true
	}
	if v.failed {
		return false
	}
	if v.m == nil {
		return true
	}
	o, ok := v.m.Office(g.OfficeID)
	if !ok {
		return a.Admin // the department was closed: only administrators still find its goals
	}
	return o.Visibility == offices.Open || v.m.IsMember(a.ID, g.OfficeID)
}

// canWrite: a company goal the administrators; a department's goal its responsibles or an administrator.
func (v view) canWrite(a Actor, office string) bool {
	switch {
	case a.Open:
		return true
	case office == "":
		return a.Admin
	case v.failed || v.m == nil:
		return false
	}
	if _, ok := v.m.Office(office); !ok {
		return a.Admin
	}
	return a.Admin || v.m.IsLead(a.ID, office)
}

// audience is who receives the live event of a goal: nil (everybody) unless it is a private department's, then its
// members. ok=false: nobody may be told (the departments cannot be read, or the private office has no members).
func (v view) audience(g Goal) ([]string, bool) {
	if g.OfficeID == "" || v.m == nil && !v.failed {
		return nil, true
	}
	if v.failed {
		return nil, false
	}
	o, ok := v.m.Office(g.OfficeID)
	if !ok {
		return nil, false
	}
	if o.Visibility != offices.Private {
		return nil, true
	}
	return o.Members, len(o.Members) > 0
}

func (v view) withName(g Goal) Goal {
	if g.OfficeID != "" && v.m != nil {
		if o, ok := v.m.Office(g.OfficeID); ok {
			g.OfficeName = o.Name
		}
	}
	return g
}

// List returns the goals the actor sees, newest first; archived ones only with includeArchived.
func (s *Service) List(ctx context.Context, org string, a Actor, includeArchived bool) ([]Goal, error) {
	all, err := s.cfg.Store.ListGoals(ctx, org)
	if err != nil {
		return nil, err
	}
	v := s.model(ctx, org)
	out := make([]Goal, 0, len(all))
	for _, g := range all {
		if g.Status == StatusArchived && !includeArchived || !v.canSee(a, g) {
			continue
		}
		out = append(out, v.withName(g))
	}
	return out, nil
}

// Get returns one goal; a goal the actor may not see answers as missing.
func (s *Service) Get(ctx context.Context, org string, a Actor, id string) (Goal, error) {
	g, err := s.cfg.Store.GetGoal(ctx, org, id)
	if err != nil {
		return Goal{}, err
	}
	v := s.model(ctx, org)
	if !v.canSee(a, g) {
		return Goal{}, notFound()
	}
	return v.withName(g), nil
}

// Visible returns the goal when the actor sees it (ok=false: missing or hidden). For the "contributes to" of a
// report or request.
func (s *Service) Visible(ctx context.Context, org string, a Actor, id string) (Goal, bool) {
	if id == "" {
		return Goal{}, false
	}
	g, err := s.Get(ctx, org, a, id)
	return g, err == nil
}

func clip(s string) string { return strings.Join(strings.Fields(s), " ") }

// texts validates and normalizes the texts of a goal.
func texts(title, desc, period string) (string, string, string, error) {
	title, desc, period = clip(title), strings.TrimSpace(desc), clip(period)
	switch {
	case title == "":
		return "", "", "", invalid("title is required")
	case len([]rune(title)) > MaxTitle:
		return "", "", "", invalid(fmt.Sprintf("title is longer than %d characters", MaxTitle))
	case len([]rune(desc)) > MaxDescription:
		return "", "", "", invalid(fmt.Sprintf("description is longer than %d characters", MaxDescription))
	case len([]rune(period)) > MaxPeriod:
		return "", "", "", invalid(fmt.Sprintf("period is longer than %d characters", MaxPeriod))
	}
	return title, desc, period, nil
}

// texts also refuses a goal that reads like instructions for the agents: a goal is context, never an order.
func (s *Service) texts(title, desc, period string) (string, string, string, error) {
	title, desc, period, err := texts(title, desc, period)
	if err == nil && s.cfg.Accept != nil {
		for _, t := range []string{title, desc, period} {
			if t != "" && !s.cfg.Accept(t) {
				return "", "", "", invalid("the goal reads like instructions for the agents; write what the company wants to achieve")
			}
		}
	}
	return title, desc, period, err
}

func dueDate(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return nil, invalid("due_date must be YYYY-MM-DD")
	}
	return &d, nil
}

func validStatus(st string) bool {
	return st == StatusActive || st == StatusAchieved || st == StatusArchived
}

// activeCount counts the active goals of the organization (every department: the cap is the company's).
func (s *Service) activeCount(ctx context.Context, org string) (int, error) {
	all, err := s.cfg.Store.ListGoals(ctx, org)
	n := 0
	for _, g := range all {
		if g.Status == StatusActive {
			n++
		}
	}
	return n, err
}

// officeFor validates the department of a new goal ("" and General = the company).
func (v view) officeFor(a Actor, office string) (string, error) {
	office = strings.TrimSpace(office)
	if office == "" || office == offices.GeneralID {
		return "", nil
	}
	if v.failed {
		return "", forbidden("the departments cannot be read right now; try again")
	}
	if v.m == nil {
		return "", invalid("this organization has no departments")
	}
	if _, ok := v.m.Office(office); !ok {
		return "", invalid("unknown department")
	}
	if !v.canSee(a, Goal{OfficeID: office}) {
		return "", forbidden("you cannot add goals to a department you cannot see")
	}
	return office, nil
}

// Create adds a goal (active).
func (s *Service) Create(ctx context.Context, org string, a Actor, in Input) (Goal, error) {
	title, desc, period, err := s.texts(in.Title, in.Description, in.Period)
	if err != nil {
		return Goal{}, err
	}
	due, err := dueDate(in.DueDate)
	if err != nil {
		return Goal{}, err
	}
	v := s.model(ctx, org)
	office, err := v.officeFor(a, in.OfficeID)
	if err != nil {
		return Goal{}, err
	}
	if !v.canWrite(a, office) {
		if office == "" {
			return Goal{}, forbidden("only an administrator can set the company's goals")
		}
		return Goal{}, forbidden("only the responsible of the department or an administrator can set its goals")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, err := s.activeCount(ctx, org); err != nil {
		return Goal{}, err
	} else if n >= MaxActive {
		return Goal{}, fmt.Errorf("%w: at most %d active goals; mark one achieved or archive it first", domain.ErrConflict, MaxActive)
	}
	now := s.cfg.Now().UTC()
	g := Goal{ID: "goal-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16], OfficeID: office, Title: title, Description: desc,
		Status: StatusActive, Period: period, DueDate: due, CreatedBy: a.ID, CreatedAt: now, UpdatedAt: now}
	if err := s.cfg.Store.CreateGoal(ctx, org, g); err != nil {
		return Goal{}, err
	}
	g = v.withName(g)
	s.changed(ctx, org, a, v, "goal.created", g, map[string]any{"office_id": office, "status": g.Status})
	return g, nil
}

// Update changes the texts, deadline or status of a goal.
func (s *Service) Update(ctx context.Context, org string, a Actor, id string, p Patch) (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, err := s.cfg.Store.GetGoal(ctx, org, id)
	if err != nil {
		return Goal{}, err
	}
	v := s.model(ctx, org)
	if !v.canSee(a, g) {
		return Goal{}, notFound()
	}
	if !v.canWrite(a, g.OfficeID) {
		return Goal{}, forbidden("only the responsible of the department or an administrator can change this goal")
	}
	title, desc, period := g.Title, g.Description, g.Period
	var changed []string
	if p.Title != nil {
		title, changed = *p.Title, append(changed, "title")
	}
	if p.Description != nil {
		desc, changed = *p.Description, append(changed, "description")
	}
	if p.Period != nil {
		period, changed = *p.Period, append(changed, "period")
	}
	if g.Title, g.Description, g.Period, err = s.texts(title, desc, period); err != nil {
		return Goal{}, err
	}
	if p.DueDate != nil {
		if g.DueDate, err = dueDate(*p.DueDate); err != nil {
			return Goal{}, err
		}
		changed = append(changed, "due_date")
	}
	from := g.Status
	if p.Status != nil {
		st := strings.TrimSpace(*p.Status)
		if !validStatus(st) {
			return Goal{}, invalid("status must be active, achieved or archived")
		}
		if st == StatusActive && from != StatusActive {
			if n, err := s.activeCount(ctx, org); err != nil {
				return Goal{}, err
			} else if n >= MaxActive {
				return Goal{}, fmt.Errorf("%w: at most %d active goals", domain.ErrConflict, MaxActive)
			}
		}
		g.Status = st
		if st != from {
			changed = append(changed, "status")
		}
	}
	g.UpdatedAt = s.cfg.Now().UTC()
	if err := s.cfg.Store.UpdateGoal(ctx, org, g); err != nil {
		return Goal{}, err
	}
	g = v.withName(g)
	s.changed(ctx, org, a, v, "goal.updated", g, map[string]any{"fields": changed, "status_from": from, "status": g.Status})
	return g, nil
}

// changed audits a change and tells the people who see the goal.
func (s *Service) changed(ctx context.Context, org string, a Actor, v view, action string, g Goal, details map[string]any) {
	if s.cfg.Audit != nil {
		actor := a.ID
		if actor == "" {
			actor = "user"
		}
		s.cfg.Audit(ctx, org, actor, action, g.ID, details)
	}
	if s.cfg.Emit == nil {
		return
	}
	if aud, ok := v.audience(g); ok {
		s.cfg.Emit(ctx, org, EventChanged, g, aud)
	}
}

// CheckLink implements application.GoalSource: new work may point to the goal when it is of this organization,
// active and visible to the person asking (userID "" = no person: only the seal of departments applies as for
// someone outside them). A hidden goal answers exactly like a missing one.
func (s *Service) CheckLink(ctx context.Context, org, userID, goalID string) error {
	g, err := s.cfg.Store.GetGoal(ctx, org, goalID)
	if err != nil {
		return invalid("unknown goal")
	}
	if !s.model(ctx, org).canSee(Actor{ID: userID}, g) {
		return invalid("unknown goal")
	}
	if g.Status != StatusActive {
		return invalid("the goal is not active")
	}
	return nil
}

// AgentNote implements application.GoalSource: the goal as untrusted context for an agent (agentID "" = the
// planner). A company or open department's goal reaches every task of the work and the planner; a private
// department's goal only the tasks of that department's agents (the planner's plan can be read by whoever sees the
// request). Anything unreadable gives no note (fail closed).
func (s *Service) AgentNote(ctx context.Context, org, goalID, agentID string) (application.RecalledMemory, bool) {
	if goalID == "" {
		return application.RecalledMemory{}, false
	}
	g, err := s.cfg.Store.GetGoal(ctx, org, goalID)
	if err != nil {
		return application.RecalledMemory{}, false
	}
	if g.OfficeID != "" {
		v := s.model(ctx, org)
		if v.failed || v.m == nil {
			return application.RecalledMemory{}, false
		}
		o, ok := v.m.Office(g.OfficeID)
		if !ok {
			return application.RecalledMemory{}, false
		}
		if o.Visibility == offices.Private && (agentID == "" || v.m.Agent(agentID).OfficeID != g.OfficeID) {
			return application.RecalledMemory{}, false
		}
	}
	clean := s.cfg.Clean
	if clean == nil {
		clean = func(x string) string { return x }
	}
	parts := []string{}
	if g.Description != "" {
		parts = append(parts, g.Description)
	}
	if g.Period != "" {
		parts = append(parts, "Period: "+g.Period)
	}
	if g.DueDate != nil {
		parts = append(parts, "Due: "+g.DueDate.Format(time.DateOnly))
	}
	return application.RecalledMemory{Ref: "goal:" + g.ID, Kind: "goal", Title: clean(g.Title), Snippet: clean(strings.Join(parts, ". ")),
		At: g.CreatedAt}, true
}
