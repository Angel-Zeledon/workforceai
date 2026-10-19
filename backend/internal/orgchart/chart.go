package orgchart

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/domain"
)

// ---- the org chart view ----

// Member is a person or an agent inside a department box of the chart.
type Member struct {
	Kind          Kind       `json:"kind"`
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Title         string     `json:"title"`
	Role          string     `json:"role"`
	Head          bool       `json:"head"`
	ReportsTo     Ref        `json:"reports_to"`
	ReportsToName string     `json:"reports_to_name"`
	Escalation    Ref        `json:"escalation"`
	State         string     `json:"state,omitempty"`
	Open          *bool      `json:"open,omitempty"` // agents with a schedule: may act right now
	NextOpen      *time.Time `json:"next_open,omitempty"`
	BudgetUSD     *float64   `json:"budget_usd,omitempty"`
}

// DeptNode is a department with its members and sub-departments.
type DeptNode struct {
	Department
	HeadName string     `json:"head_name"`
	Members  []Member   `json:"members"`
	Children []DeptNode `json:"children"`
	People   int        `json:"people"`
	Agents   int        `json:"agents"`
}

// Chart is the whole org chart of an organization.
type Chart struct {
	Roots      []DeptNode `json:"roots"`
	Unassigned []Member   `json:"unassigned"`
	People     int        `json:"people"`
	Agents     int        `json:"agents"`
	Applied    []Applied  `json:"applied"`
	Now        time.Time  `json:"now"`
}

// BuildChart assembles the chart from plain data (pure, so it is tested without stores).
func BuildChart(deps []Department, agents []domain.Agent, jobs []Job, people []Person, places []Placement, applied []Applied, now time.Time) Chart {
	jobOf := map[string]Job{}
	for _, j := range jobs {
		jobOf[j.AgentID] = j
	}
	placeOf := map[string]Placement{}
	for _, p := range places {
		placeOf[p.UserID] = p
	}
	names := map[string]string{}
	var owner string
	for _, p := range people {
		names[Ref{Kind: KindPerson, ID: p.UserID}.Key()] = p.Name
		if p.Role == "owner" && owner == "" {
			owner = p.Name
		}
	}
	if owner == "" {
		owner = "Owner"
	}
	names[Ref{Kind: KindPerson, ID: OwnerID}.Key()] = owner
	for _, a := range agents {
		names[Ref{Kind: KindAgent, ID: a.ID}.Key()] = a.Name
	}
	nameOf := func(r Ref) string {
		if r.IsZero() {
			return ""
		}
		return names[r.Key()]
	}
	heads := map[string]string{} // member key -> department id it heads
	for _, d := range deps {
		if !d.Head.IsZero() {
			heads[d.Head.Key()] = d.ID
		}
	}
	byDept := map[string][]Member{}
	var unassigned []Member
	place := func(deptID string, m Member) {
		if _, ok := heads[m.Ref().Key()]; ok && heads[m.Ref().Key()] == deptID {
			m.Head = true
		}
		valid := false
		for _, d := range deps {
			if d.ID == deptID {
				valid = true
			}
		}
		if deptID == "" || !valid {
			unassigned = append(unassigned, m)
			return
		}
		byDept[deptID] = append(byDept[deptID], m)
	}
	for _, a := range agents {
		j, has := jobOf[a.ID]
		m := Member{Kind: KindAgent, ID: a.ID, Name: a.Name, Title: a.Title, Role: a.Role, State: string(a.State)}
		if has {
			m.ReportsTo, m.Escalation, m.BudgetUSD = j.ReportsTo, j.Escalation, j.BudgetUSD
			if j.Schedule.Enabled {
				av := j.Schedule.AvailabilityAt(now)
				m.Open, m.NextOpen = &av.Open, av.NextOpen
			}
		}
		m.ReportsToName = nameOf(m.ReportsTo)
		place(j.DepartmentID, m)
	}
	for _, p := range people {
		pl := placeOf[p.UserID]
		m := Member{Kind: KindPerson, ID: p.UserID, Name: p.Name, Title: pl.Title, Role: p.Role, ReportsTo: pl.ReportsTo}
		m.ReportsToName = nameOf(m.ReportsTo)
		place(pl.DepartmentID, m)
	}
	kids := map[string][]Department{}
	for _, d := range deps {
		kids[d.ParentID] = append(kids[d.ParentID], d)
	}
	var build func(d Department) DeptNode
	build = func(d Department) DeptNode {
		n := DeptNode{Department: d, HeadName: nameOf(d.Head), Members: byDept[d.ID], Children: []DeptNode{}}
		if n.Members == nil {
			n.Members = []Member{}
		}
		sort.SliceStable(n.Members, func(i, j int) bool {
			a, b := n.Members[i], n.Members[j]
			if a.Head != b.Head {
				return a.Head
			}
			if a.Kind != b.Kind {
				return a.Kind == KindPerson
			}
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		})
		for _, m := range n.Members {
			if m.Kind == KindPerson {
				n.People++
			} else {
				n.Agents++
			}
		}
		for _, k := range kids[d.ID] {
			c := build(k)
			n.Children = append(n.Children, c)
		}
		return n
	}
	c := Chart{Roots: []DeptNode{}, Unassigned: unassigned, People: len(people), Agents: len(agents), Applied: applied, Now: now}
	if c.Unassigned == nil {
		c.Unassigned = []Member{}
	}
	if c.Applied == nil {
		c.Applied = []Applied{}
	}
	for _, d := range kids[""] {
		c.Roots = append(c.Roots, build(d))
	}
	return c
}

// Ref returns the reference of a member.
func (m Member) Ref() Ref { return Ref{Kind: m.Kind, ID: m.ID} }

// Chart returns the org chart of the organization at the current time.
func (s *Service) Chart(ctx context.Context) (Chart, error) {
	org := s.org(ctx)
	deps, err := s.Store.ListDepartments(ctx, org)
	if err != nil {
		return Chart{}, err
	}
	_, agents, err := s.agentMap(ctx)
	if err != nil {
		return Chart{}, err
	}
	jobs, err := s.Store.ListJobs(ctx, org)
	if err != nil {
		return Chart{}, err
	}
	places, err := s.Store.ListPlacements(ctx, org)
	if err != nil {
		return Chart{}, err
	}
	applied, err := s.Store.ListApplied(ctx, org)
	if err != nil {
		return Chart{}, err
	}
	return BuildChart(deps, agents, jobs, s.people(ctx), places, applied, s.now()), nil
}

// ---- delegation ----

// RuleInput is the body to create a delegation rule.
type RuleInput struct {
	From            Selector `json:"from"`
	To              Selector `json:"to"`
	Effect          string   `json:"effect"`
	RequireApproval bool     `json:"require_approval"`
	Approver        Ref      `json:"approver"`
	Note            string   `json:"note"`
}

const maxRules = 200

func (s *Service) Rules(ctx context.Context) ([]Rule, error) {
	r, err := s.Store.ListRules(ctx, s.org(ctx))
	if r == nil {
		r = []Rule{}
	}
	return r, err
}

// CreateRule validates and adds a delegation rule.
func (s *Service) CreateRule(ctx context.Context, in RuleInput) (Rule, error) {
	org := s.org(ctx)
	r := Rule{ID: "rule_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12], From: in.From, To: in.To, Effect: in.Effect,
		RequireApproval: in.RequireApproval, Approver: in.Approver, Note: strings.TrimSpace(in.Note), CreatedBy: s.actor(ctx), CreatedAt: s.now()}
	if r.Effect == "" {
		r.Effect = EffectAllow
	}
	if err := ValidateRule(r); err != nil {
		return Rule{}, invalid("%v", err)
	}
	all, err := s.Store.ListRules(ctx, org)
	if err != nil {
		return Rule{}, err
	}
	if len(all) >= maxRules {
		return Rule{}, fmt.Errorf("%w: at most %d delegation rules", domain.ErrConflict, maxRules)
	}
	agents, _, err := s.agentMap(ctx)
	if err != nil {
		return Rule{}, err
	}
	deps, err := s.Store.ListDepartments(ctx, org)
	if err != nil {
		return Rule{}, err
	}
	for _, sel := range []Selector{r.From, r.To} {
		switch sel.Type {
		case SelAgent:
			if _, ok := agents[sel.ID]; !ok {
				return Rule{}, invalid("unknown agent %q", sel.ID)
			}
		case SelPerson:
			if !s.personExists(ctx, sel.ID) {
				return Rule{}, invalid("unknown person %q", sel.ID)
			}
		case SelDepartment:
			found := false
			for _, d := range deps {
				found = found || d.ID == sel.ID
			}
			if !found {
				return Rule{}, invalid("unknown department %q", sel.ID)
			}
		}
	}
	if err := s.checkRef(ctx, r.Approver, "approver", agents); err != nil {
		return Rule{}, err
	}
	if err := s.Store.PutRule(ctx, org, r); err != nil {
		return Rule{}, err
	}
	s.invalidate(org)
	s.audit(ctx, "delegation.rule_created", "delegation_rule", r.ID, map[string]any{"from": r.From, "to": r.To, "effect": r.Effect, "approval": r.RequireApproval})
	return r, nil
}

// DeleteRule removes a delegation rule.
func (s *Service) DeleteRule(ctx context.Context, id string) error {
	if err := s.Store.DeleteRule(ctx, s.org(ctx), id); err != nil {
		return err
	}
	s.invalidate(s.org(ctx))
	s.audit(ctx, "delegation.rule_deleted", "delegation_rule", id, nil)
	return nil
}

// directory builds the lookups delegation checks need and resolves an actor's department.
func (s *Service) directory(ctx context.Context) (Directory, map[string]string, error) {
	org := s.org(ctx)
	deps, err := s.Store.ListDepartments(ctx, org)
	if err != nil {
		return Directory{}, nil, err
	}
	jobs, err := s.Store.ListJobs(ctx, org)
	if err != nil {
		return Directory{}, nil, err
	}
	places, err := s.Store.ListPlacements(ctx, org)
	if err != nil {
		return Directory{}, nil, err
	}
	d := Directory{Departments: deps, ReportsTo: map[string]Ref{}}
	deptOf := map[string]string{}
	for _, j := range jobs {
		k := Ref{Kind: KindAgent, ID: j.AgentID}.Key()
		d.ReportsTo[k], deptOf[k] = j.ReportsTo, j.DepartmentID
	}
	for _, p := range places {
		k := Ref{Kind: KindPerson, ID: p.UserID}.Key()
		d.ReportsTo[k], deptOf[k] = p.ReportsTo, p.DepartmentID
	}
	return d, deptOf, nil
}

// CheckDelegation decides whether `from` may assign work to `to` under the organization's rules.
func (s *Service) CheckDelegation(ctx context.Context, from, to Ref) (Decision, error) {
	for _, r := range []Ref{from, to} {
		if err := validateRef(r, "actor"); err != nil || r.IsZero() {
			return Decision{}, invalid("from and to must each be a person or an agent")
		}
	}
	rules, err := s.Store.ListRules(ctx, s.org(ctx))
	if err != nil {
		return Decision{}, err
	}
	dir, deptOf, err := s.directory(ctx)
	if err != nil {
		return Decision{}, err
	}
	return Check(rules, Actor{Kind: from.Kind, ID: from.ID, DepartmentID: deptOf[from.Key()]},
		Actor{Kind: to.Kind, ID: to.ID, DepartmentID: deptOf[to.Key()]}, dir), nil
}
