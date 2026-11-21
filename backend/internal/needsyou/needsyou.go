// Package needsyou builds "Te necesitan" (docs/architecture/needs-you.md): one list of everything that waits for the
// person who looks and that they may resolve (approvals they may decide, questions for them, pending suggestions,
// trust proposals they may decide). The package only merges and orders: every Source applies the rule its own screen
// already applies (who sees it, who decides it), so a new source is one more function in the list and can never
// widen what a person sees.
package needsyou

import (
	"context"
	"sort"
	"strings"
	"time"
)

// Kinds of items. A new source picks a new kind (the UI shows unknown kinds with a generic label).
const (
	KindApproval   = "approval"
	KindQuestion   = "question"
	KindSuggestion = "suggestion"
	KindTrust      = "trust"
)

// Urgency of an item: high first, then normal, then low.
const (
	UrgencyHigh   = "high"
	UrgencyNormal = "normal"
	UrgencyLow    = "low"
)

// StaleAfter is how long blocked work (an approval, a question) waits before it counts as urgent: the agents'
// questions get their first reminder at 24 h too.
const StaleAfter = 24 * time.Hour

// Item is one thing waiting for the person. Kind and Urgency are set by Collect.
type Item struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Title string `json:"title"`
	// Detail is a second line when the source has one (the task of an approval or a question).
	Detail     string    `json:"detail,omitempty"`
	AgentID    string    `json:"agent_id,omitempty"`
	AgentName  string    `json:"agent_name,omitempty"`
	OfficeID   string    `json:"office_id,omitempty"`
	OfficeName string    `json:"office_name,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	// Risk is the approval's risk (low|medium|high); Double a double approval (two people decide).
	Risk    string `json:"risk,omitempty"`
	Double  bool   `json:"double,omitempty"`
	Urgency string `json:"urgency"`
	// Target is the route of the UI where the item is resolved.
	Target string `json:"target"`
	// Direct says the item may be resolved from the list in one step (low-risk approval, a suggestion); otherwise
	// the list only links to Target.
	Direct bool `json:"direct"`

	// Per kind (all optional).
	Action       string            `json:"action,omitempty"`        // approval: the action
	ProjectName  string            `json:"project_name,omitempty"`  // approval, question
	Signal       string            `json:"signal,omitempty"`        // suggestion: the signal behind it
	Params       map[string]string `json:"params,omitempty"`        // suggestion: the values behind the text
	Options      []string          `json:"options,omitempty"`       // question: the answers the agent offered
	ProposalKind string            `json:"proposal_kind,omitempty"` // trust: promotion | demotion
	FromLevel    string            `json:"from_level,omitempty"`    // trust
	ToLevel      string            `json:"to_level,omitempty"`      // trust
}

// Source yields the items of one kind for the person of the request. Collect gets full=false for the counter:
// it may then skip what only the list shows (titles of tasks, names), never a rule.
type Source struct {
	Kind    string
	Collect func(ctx context.Context, full bool) ([]Item, error)
}

// Options of one collection.
type Options struct {
	Full      bool // fill what only the list shows
	CountOnly bool // return the counts and no items
	Limit     int  // at most this many items (0: all)
	Now       time.Time
}

// Result is GET /needs-you (and, without items, GET /needs-you/count).
type Result struct {
	Items  []Item         `json:"items"`
	Total  int            `json:"total"`
	Counts map[string]int `json:"counts"`
	// Truncated says Items was cut at the limit (Total and Counts are not).
	Truncated bool `json:"truncated,omitempty"`
	// Unavailable names the sources that could not be read: their items are left out, never guessed.
	Unavailable []string `json:"unavailable,omitempty"`
}

// Urgency of an item at now: blocked work (an approval, a question) is urgent when it is high risk, a double
// approval or has waited StaleAfter; otherwise normal. Suggestions and trust proposals block nothing: low.
func Urgency(it Item, now time.Time) string {
	switch it.Kind {
	case KindApproval, KindQuestion:
		if strings.EqualFold(it.Risk, "high") || it.Double || !it.CreatedAt.IsZero() && now.Sub(it.CreatedAt) >= StaleAfter {
			return UrgencyHigh
		}
		return UrgencyNormal
	default:
		return UrgencyLow
	}
}

func rank(u string) int {
	switch u {
	case UrgencyHigh:
		return 2
	case UrgencyNormal:
		return 1
	}
	return 0
}

// Collect reads every source and returns the merged list: most urgent first, then the oldest. A source that fails is
// left out entirely (named in Unavailable): what it returned with the error is not shown.
func Collect(ctx context.Context, o Options, sources ...Source) Result {
	if o.Now.IsZero() {
		o.Now = time.Now().UTC()
	}
	res := Result{Items: []Item{}, Counts: map[string]int{}}
	var all []Item
	for _, s := range sources {
		if s.Kind == "" || s.Collect == nil {
			continue
		}
		items, err := s.Collect(ctx, o.Full && !o.CountOnly)
		if err != nil {
			res.Unavailable = append(res.Unavailable, s.Kind)
			continue
		}
		for _, it := range items {
			it.Kind = s.Kind
			it.Urgency = Urgency(it, o.Now)
			all = append(all, it)
			res.Counts[s.Kind]++
		}
	}
	res.Total = len(all)
	if o.CountOnly {
		return res
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if ra, rb := rank(a.Urgency), rank(b.Urgency); ra != rb {
			return ra > rb
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})
	if o.Limit > 0 && len(all) > o.Limit {
		all, res.Truncated = all[:o.Limit], true
	}
	if len(all) > 0 {
		res.Items = all
	}
	return res
}
