package application

import (
	"sort"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Approvals in one minute (B17, docs/architecture/approvals-inbox.md): a cross-project inbox built from what
// already exists. Nothing here decides anything. Deciding goes through POST /approvals/{id}/decision or
// POST /approvals/batch, with every existing rule (roles, double approval, "agents never approve", audit).
//
// The "learns when to interrupt" part is a plain, explainable suggestion: when people in the organization approved
// more than 95 % of a kind of action in the last 30 days (at least 20 decisions, never a kind that reached high
// risk), the inbox SUGGESTS raising the notification threshold or a standing approval. It never changes a setting.

// Inbox rules, also returned to the client so the UI can explain them.
const (
	InboxSuggestWindowDays   = 30
	InboxSuggestMinDecisions = 20
	InboxSuggestRate         = 0.95
	inboxCounterMax          = 280
)

// InboxItem is one pending approval with what the card needs.
type InboxItem struct {
	domain.Approval
	TaskTitle   string  `json:"task_title,omitempty"`
	RequestID   string  `json:"request_id,omitempty"`
	TaskCostUSD float64 `json:"task_cost_usd"`
	// ProjectID (embedded) is filled from the request's project when the approval itself does not carry it.
	ProjectName string `json:"project_name,omitempty"`
	// GroupKey is set when the item belongs to a group of identical pending actions of one project.
	GroupKey string `json:"group_key,omitempty"`
	// Counterargument is the devil's advocate summary when one was already generated (never generated here).
	Counterargument *InboxCounter `json:"counterargument,omitempty"`
	HighRisk        bool          `json:"high_risk"`
}

// InboxCounter is the cached devil's advocate review, shortened for the card.
type InboxCounter struct {
	Summary string   `json:"summary"`
	Risks   []string `json:"risks"`
}

// InboxGroup is every pending approval of one action in one project. Batchable means POST /approvals/batch may
// decide it in one step: at least two, all low risk (double approval counts as high risk).
type InboxGroup struct {
	Key         string   `json:"key"`
	ProjectID   string   `json:"project_id"`
	ProjectName string   `json:"project_name,omitempty"`
	Action      string   `json:"action"`
	Count       int      `json:"count"`
	ApprovalIDs []string `json:"approval_ids"`
	Batchable   bool     `json:"batchable"`
	// Reason says why a group of two or more is not batchable: "risk".
	Reason string `json:"reason,omitempty"`
}

// InboxSuggestion is advisory only.
type InboxSuggestion struct {
	Kind       string  `json:"kind"` // raise_notify_threshold
	Action     string  `json:"action"`
	Decided    int     `json:"decided"`
	Approved   int     `json:"approved"`
	Rate       float64 `json:"rate"`
	WindowDays int     `json:"window_days"`
	MaxRisk    string  `json:"max_risk"`
}

// InboxRules travel with the response so the UI can explain itself.
type InboxRules struct {
	SuggestWindowDays   int     `json:"suggest_window_days"`
	SuggestMinDecisions int     `json:"suggest_min_decisions"`
	SuggestRate         float64 `json:"suggest_rate"`
	// Undo is "client_side": the API has no undo, so the UI holds a decision for a few seconds before sending it.
	Undo string `json:"undo"`
}

// ApprovalInbox is GET /approvals/inbox.
type ApprovalInbox struct {
	GeneratedAt time.Time         `json:"generated_at"`
	Items       []InboxItem       `json:"items"`
	Groups      []InboxGroup      `json:"groups"`
	Suggestions []InboxSuggestion `json:"suggestions"`
	Rules       InboxRules        `json:"rules"`
}

// InboxInput is what BuildApprovalInbox needs; the lookups may be nil.
type InboxInput struct {
	Pending     []domain.Approval
	History     []domain.Approval
	Tasks       map[string]domain.Task
	ProjectOf   func(requestID string) string
	ProjectName func(projectID string) string
	Counter     func(approvalID string) (domain.Counterargument, bool)
	Now         time.Time
}

var riskRank = map[string]int{"low": 0, "medium": 1, "high": 2}

// BuildApprovalInbox assembles the inbox (pure; tested on its own).
func BuildApprovalInbox(in InboxInput) ApprovalInbox {
	out := ApprovalInbox{GeneratedAt: in.Now, Items: []InboxItem{}, Groups: []InboxGroup{}, Suggestions: []InboxSuggestion{},
		Rules: InboxRules{SuggestWindowDays: InboxSuggestWindowDays, SuggestMinDecisions: InboxSuggestMinDecisions, SuggestRate: InboxSuggestRate, Undo: "client_side"}}
	groups := map[string]*InboxGroup{}
	for _, ap := range in.Pending {
		if ap.Status != domain.ApprovalPending {
			continue
		}
		it := InboxItem{Approval: ap, HighRisk: IsHighRiskApproval(ap)}
		if t, ok := in.Tasks[ap.TaskID]; ok {
			it.TaskTitle, it.RequestID, it.TaskCostUSD = t.Title, t.RequestID, round6(t.CostUSD)
			if it.ProjectID == "" && in.ProjectOf != nil && t.RequestID != "" {
				it.ProjectID = in.ProjectOf(t.RequestID)
			}
		}
		if it.ProjectID != "" && in.ProjectName != nil {
			it.ProjectName = in.ProjectName(it.ProjectID)
		}
		if in.Counter != nil {
			if c, ok := in.Counter(ap.ID); ok {
				it.Counterargument = &InboxCounter{Summary: truncate(c.Counterargument, inboxCounterMax), Risks: boundStrings(c.Risks, 3)}
			}
		}
		if it.ProjectID != "" && ap.Action != "" {
			key := it.ProjectID + "|" + ap.Action
			g := groups[key]
			if g == nil {
				g = &InboxGroup{Key: key, ProjectID: it.ProjectID, ProjectName: it.ProjectName, Action: ap.Action, Batchable: true}
				groups[key] = g
			}
			g.Count++
			g.ApprovalIDs = append(g.ApprovalIDs, ap.ID)
			// High risk includes double approval (IsHighRiskApproval): never batched.
			if it.HighRisk || ap.Risk != "low" {
				g.Batchable, g.Reason = false, "risk"
			}
			it.GroupKey = key
		}
		out.Items = append(out.Items, it)
	}
	for _, g := range groups {
		if g.Count < 2 {
			g.Batchable, g.Reason = false, ""
			continue
		}
		out.Groups = append(out.Groups, *g)
	}
	// Groups of one are not groups: drop the key from their single item.
	inGroup := map[string]bool{}
	for _, g := range out.Groups {
		inGroup[g.Key] = true
	}
	for i := range out.Items {
		if !inGroup[out.Items[i].GroupKey] {
			out.Items[i].GroupKey = ""
		}
	}
	// Riskiest first; within a risk, oldest first (it has waited longest).
	sort.SliceStable(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if riskRank[a.Risk] != riskRank[b.Risk] {
			return riskRank[a.Risk] > riskRank[b.Risk]
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	sort.Slice(out.Groups, func(i, j int) bool { return out.Groups[i].Key < out.Groups[j].Key })
	out.Suggestions = inboxSuggestions(in.History, in.Now)
	return out
}

// inboxSuggestions: kinds of action people approved more than InboxSuggestRate of the time in the window.
// Standing approvals (decided automatically) are not human decisions and are left out; a kind that ever reached
// high risk is never suggested. Rejections do not record who rejected, so the count is the organization's.
func inboxSuggestions(history []domain.Approval, now time.Time) []InboxSuggestion {
	since := now.AddDate(0, 0, -InboxSuggestWindowDays)
	type agg struct {
		decided, approved int
		maxRisk           string
	}
	by := map[string]*agg{}
	for _, ap := range history {
		if ap.Status == domain.ApprovalPending || ap.ResolvedAt == nil || ap.ResolvedAt.Before(since) || ap.Action == "" {
			continue
		}
		if len(ap.Decisions) > 0 && strings.HasPrefix(ap.Decisions[0].By, "standing:") {
			continue
		}
		a := by[ap.Action]
		if a == nil {
			a = &agg{maxRisk: "low"}
			by[ap.Action] = a
		}
		a.decided++
		if ap.Status == domain.ApprovalApproved {
			a.approved++
		}
		if IsHighRiskApproval(ap) {
			a.maxRisk = "high"
		} else if riskRank[ap.Risk] > riskRank[a.maxRisk] {
			a.maxRisk = ap.Risk
		}
	}
	out := []InboxSuggestion{}
	for action, a := range by {
		rate := float64(a.approved) / float64(a.decided)
		if a.decided < InboxSuggestMinDecisions || rate <= InboxSuggestRate || a.maxRisk == "high" {
			continue
		}
		out = append(out, InboxSuggestion{Kind: "raise_notify_threshold", Action: action, Decided: a.decided, Approved: a.approved,
			Rate: float64(int(rate*1000+0.5)) / 1000, WindowDays: InboxSuggestWindowDays, MaxRisk: a.maxRisk})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Decided != out[j].Decided {
			return out[i].Decided > out[j].Decided
		}
		return out[i].Action < out[j].Action
	})
	return out
}
