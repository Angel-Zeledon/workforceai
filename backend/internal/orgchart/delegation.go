package orgchart

import (
	"fmt"
	"strings"
)

// Delegation rules answer "may A assign work to B, and who approves?". The most SPECIFIC matching
// rule wins; at equal specificity deny beats approval beats allow. With no matching rule the answer
// is "allowed, no approval" so an organization that never configures rules behaves exactly as before.
// A rule from "any" to "any" is the organization default (for example deny, to make the model strict).

// Actor is who delegates or receives work, with the department it belongs to.
type Actor struct {
	Kind         Kind
	ID           string
	DepartmentID string
}

// Directory resolves the structure rules need: the department tree and the boss / head lookups.
type Directory struct {
	Departments []Department
	// ReportsTo maps "kind:id" to the Ref of the boss (job description / placement).
	ReportsTo map[string]Ref
}

// Decision is the result of a delegation check.
type Decision struct {
	Allowed       bool   `json:"allowed"`
	NeedsApproval bool   `json:"needs_approval"`
	Approver      Ref    `json:"approver"`
	RuleID        string `json:"rule_id"`
	Reason        string `json:"reason"`
}

func (a Actor) ref() Ref { return Ref{Kind: a.Kind, ID: a.ID} }

// ValidateRule checks the shape of a rule.
func ValidateRule(r Rule) error {
	for _, s := range []Selector{r.From, r.To} {
		switch s.Type {
		case SelAny, SelAnyPerson, SelAnyAgent:
			if s.ID != "" {
				return fmt.Errorf("selector %q takes no id", s.Type)
			}
		case SelPerson, SelAgent, SelDepartment:
			if strings.TrimSpace(s.ID) == "" || len(s.ID) > 64 {
				return fmt.Errorf("selector %q needs an id", s.Type)
			}
		default:
			return fmt.Errorf("unknown selector type %q", s.Type)
		}
	}
	if r.Effect != EffectAllow && r.Effect != EffectDeny {
		return fmt.Errorf("effect must be allow or deny")
	}
	if r.Effect == EffectDeny && (r.RequireApproval || !r.Approver.IsZero()) {
		return fmt.Errorf("a deny rule has no approval")
	}
	if err := validateRef(r.Approver, "approver"); err != nil {
		return err
	}
	if !r.Approver.IsZero() && !r.RequireApproval {
		return fmt.Errorf("an approver only makes sense when approval is required")
	}
	if len(r.Note) > 200 {
		return fmt.Errorf("note is too long (max 200)")
	}
	return nil
}

func (s Selector) matches(a Actor, d Directory) bool {
	switch s.Type {
	case SelAny:
		return true
	case SelAnyPerson:
		return a.Kind == KindPerson
	case SelAnyAgent:
		return a.Kind == KindAgent
	case SelPerson:
		return a.Kind == KindPerson && a.ID == s.ID
	case SelAgent:
		return a.Kind == KindAgent && a.ID == s.ID
	case SelDepartment:
		return a.DepartmentID != "" && Descendants(d.Departments, s.ID)[a.DepartmentID]
	}
	return false
}

func (s Selector) specificity() int {
	switch s.Type {
	case SelPerson, SelAgent:
		return 3
	case SelDepartment:
		return 2
	case SelAnyPerson, SelAnyAgent:
		return 1
	}
	return 0
}

func rank(r Rule) int { // tie-break: deny > approval > allow
	switch {
	case r.Effect == EffectDeny:
		return 2
	case r.RequireApproval:
		return 1
	}
	return 0
}

// Check decides whether from may assign work to to.
func Check(rules []Rule, from, to Actor, d Directory) Decision {
	if from.Kind == to.Kind && from.ID == to.ID {
		return Decision{Allowed: true, Reason: "self"} // working for yourself is never a delegation
	}
	var best *Rule
	bestScore := -1
	for i := range rules {
		r := rules[i]
		if !r.From.matches(from, d) || !r.To.matches(to, d) {
			continue
		}
		score := (r.From.specificity()+r.To.specificity())*10 + rank(r)
		if score > bestScore || (score == bestScore && r.ID < best.ID) {
			best, bestScore = &rules[i], score
		}
	}
	if best == nil {
		return Decision{Allowed: true, Reason: "no_rule"}
	}
	if best.Effect == EffectDeny {
		return Decision{Allowed: false, RuleID: best.ID, Reason: "denied_by_rule"}
	}
	dec := Decision{Allowed: true, RuleID: best.ID, Reason: "allowed_by_rule"}
	if best.RequireApproval {
		dec.NeedsApproval, dec.Reason = true, "approval_required"
		dec.Approver = approverFor(*best, to, d)
	}
	return dec
}

// approverFor: the rule's approver; else the head of the target's department; else the target's boss;
// else the owner of the organization.
func approverFor(r Rule, to Actor, d Directory) Ref {
	if !r.Approver.IsZero() {
		return r.Approver
	}
	for _, dep := range d.Departments {
		if dep.ID == to.DepartmentID && !dep.Head.IsZero() && dep.Head != to.ref() {
			return dep.Head
		}
	}
	if b, ok := d.ReportsTo[to.ref().Key()]; ok && !b.IsZero() {
		return b
	}
	return Ref{Kind: KindPerson, ID: OwnerID}
}
