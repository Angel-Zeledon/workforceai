package policy

import (
	"fmt"
	"strings"
	"time"
)

// Approval permissions per agent (docs/superpowers/specs/2026-11-03-permisos-de-aprobacion-design.md and
// docs/architecture/06-permisos-autonomia.md section 11).
//
// They are not a second engine: PermissionRules turns what a person chose (a level in plain words, exceptions per
// kind of action, the safety floor) into grants and limits for the existing Engine, built for the call being
// decided. An agent without an entry in Permissions.Agents keeps the behaviour it always had.

// Kinds of action. The six categories are what a person can make an exception for; "internal" (read, search,
// draft, create and edit internal files, analyze) and "other" (anything not recognized) are not categories.
const (
	KindInternal  = "internal"
	KindMessages  = "messages"  // send emails and messages (also share and invite)
	KindPublish   = "publish"   // publish
	KindPayments  = "payments"  // pay and invoice
	KindDelete    = "delete"    // delete
	KindContracts = "contracts" // contracts and proposals
	KindSpend     = "spend"     // expenses with an amount
	KindOther     = "other"
)

// PermissionCategories lists the categories a person can make exceptions for, in display order.
var PermissionCategories = []string{KindMessages, KindPublish, KindPayments, KindDelete, KindContracts, KindSpend}

// FloorCategories always ask, also when the agent works alone: only the account owner can lift them, per agent and
// category (invariant 1 of section 3: an explicit rule signed by the highest role).
var FloorCategories = []string{KindPayments, KindContracts, KindDelete}

// IsFloor reports whether a category belongs to the safety floor.
func IsFloor(cat string) bool { return inList(FloorCategories, cat) }

// ValidCategory reports whether cat is one of PermissionCategories.
func ValidCategory(cat string) bool { return inList(PermissionCategories, cat) }

func inList(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// Exception modes.
const (
	ModeAsk   = "ask"    // always ask
	ModeNever = "never"  // never ask (the floor still asks unless the owner lifted it)
	ModeAskIf = "ask_if" // ask only when one of the conditions holds
)

// AskIf are the conditions of an "ask if..." exception. Any condition that holds asks.
type AskIf struct {
	PerDayOver   int      `json:"per_day_over,omitempty"`  // more than N per day (24 h sliding window)
	AmountOver   *float64 `json:"amount_over,omitempty"`   // an amount above X (or an amount that cannot be read)
	NewRecipient bool     `json:"new_recipient,omitempty"` // a recipient that is not a known contact
}

// Exception changes how one category is treated for one agent.
type Exception struct {
	Category  string    `json:"category"`
	Mode      string    `json:"mode"`
	If        *AskIf    `json:"if,omitempty"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// Source says where it was created: "panel", "settings" or "approval:<id>".
	Source string `json:"source,omitempty"`
}

// MaxPerDay caps the "more than N per day" condition.
const MaxPerDay = 10000

// Validate checks an exception.
func (e Exception) Validate() error {
	if !ValidCategory(e.Category) {
		return fmt.Errorf("permissions: unknown category %q", e.Category)
	}
	switch e.Mode {
	case ModeAsk, ModeNever:
	case ModeAskIf:
		c := e.If
		if c == nil || (c.PerDayOver == 0 && c.AmountOver == nil && !c.NewRecipient) {
			return fmt.Errorf("permissions: ask_if needs per_day_over, amount_over or new_recipient")
		}
		if c.PerDayOver < 0 || c.PerDayOver > MaxPerDay {
			return fmt.Errorf("permissions: per_day_over must be between 1 and %d", MaxPerDay)
		}
		if c.AmountOver != nil && *c.AmountOver < 0 {
			return fmt.Errorf("permissions: amount_over must be >= 0")
		}
	default:
		return fmt.Errorf("permissions: unknown mode %q", e.Mode)
	}
	return nil
}

// FloorLift records that the account owner lifted the safety floor of one category for one agent.
type FloorLift struct {
	Category string    `json:"category"`
	By       string    `json:"by"`
	At       time.Time `json:"at"`
}

// AgentPermissions are the choices made for one agent. Its presence means "the agent uses the factory rules": its
// level (agents.autonomy, the single source of truth) is read in plain words and the floor applies.
type AgentPermissions struct {
	Exceptions []Exception `json:"exceptions,omitempty"`
	FloorLifts []FloorLift `json:"floor_lifts,omitempty"`
	Since      time.Time   `json:"since"`
	UpdatedBy  string      `json:"updated_by,omitempty"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

// Lifted reports whether the owner lifted the floor of cat for this agent.
func (a AgentPermissions) Lifted(cat string) bool {
	for _, l := range a.FloorLifts {
		if l.Category == cat {
			return true
		}
	}
	return false
}

// Exception returns the exception for cat, if any.
func (a AgentPermissions) Exception(cat string) (Exception, bool) {
	for _, e := range a.Exceptions {
		if e.Category == cat {
			return e, true
		}
	}
	return Exception{}, false
}

// Clone returns a deep copy.
func (a AgentPermissions) Clone() AgentPermissions {
	out := a
	out.Exceptions = make([]Exception, len(a.Exceptions))
	for i, e := range a.Exceptions {
		if e.If != nil {
			c := *e.If
			if c.AmountOver != nil {
				v := *c.AmountOver
				c.AmountOver = &v
			}
			e.If = &c
		}
		out.Exceptions[i] = e
	}
	out.FloorLifts = append([]FloorLift(nil), a.FloorLifts...)
	return out
}

// Permissions are the approval permissions of an organization (org_settings.rules.permissions).
type Permissions struct {
	// DefaultLevel is the autonomy level new agents start at ("" = the role template's default), always capped by
	// the role's ceiling.
	DefaultLevel string                      `json:"default_level,omitempty"`
	Agents       map[string]AgentPermissions `json:"agents,omitempty"`
	// Scopes are "in this project / batch, do not ask for <category>" (section 10 extended to categories and batches).
	Scopes []ScopeGrant `json:"scopes,omitempty"`
}

// Scope kinds of a ScopeGrant.
const (
	ScopeProject = "project"
	ScopeBatch   = "batch"
)

// ScopeGrant lets the agents of one project (or of every project of one batch) do one category without asking. It
// never covers the safety floor (unless the owner lifted it for the agent), nor what the organization rules or the
// gateway require, and it ends with the project or at ExpiresAt.
type ScopeGrant struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`               // project | batch
	ScopeID    string    `json:"scope_id"`           // the project or batch id
	AgentID    string    `json:"agent_id,omitempty"` // "" = any agent of the project or batch
	Category   string    `json:"category"`
	ApprovalID string    `json:"approval_id,omitempty"`
	CreatedBy  string    `json:"created_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// ScopeAllows returns the live scope grant that lets agentID do cat in this project or batch, if any.
func (p *Permissions) ScopeAllows(agentID, projectID, batchID, cat string, now time.Time) (ScopeGrant, bool) {
	if p == nil || (projectID == "" && batchID == "") {
		return ScopeGrant{}, false
	}
	for _, g := range p.Scopes {
		if g.Category != cat || (g.AgentID != "" && g.AgentID != agentID) || !now.Before(g.ExpiresAt) {
			continue
		}
		if (g.Kind == ScopeProject && projectID != "" && g.ScopeID == projectID) || (g.Kind == ScopeBatch && batchID != "" && g.ScopeID == batchID) {
			return g, true
		}
	}
	return ScopeGrant{}, false
}

// WithNever returns a copy of a whose exception for cat is "never ask" (a scope grant applies). The floor still asks.
func (a AgentPermissions) WithNever(cat string) AgentPermissions {
	out := a.Clone()
	kept := out.Exceptions[:0]
	for _, e := range out.Exceptions {
		if e.Category != cat {
			kept = append(kept, e)
		}
	}
	out.Exceptions = append(kept, Exception{Category: cat, Mode: ModeNever})
	return out
}

// For returns the permissions of an agent (ok=false: the agent keeps its old behaviour).
func (p *Permissions) For(agentID string) (AgentPermissions, bool) {
	if p == nil || p.Agents == nil {
		return AgentPermissions{}, false
	}
	a, ok := p.Agents[agentID]
	return a, ok
}

// Clone returns a deep copy (nil stays nil).
func (p *Permissions) Clone() *Permissions {
	if p == nil {
		return nil
	}
	out := &Permissions{DefaultLevel: p.DefaultLevel, Agents: make(map[string]AgentPermissions, len(p.Agents)),
		Scopes: append([]ScopeGrant(nil), p.Scopes...)}
	for k, v := range p.Agents {
		out.Agents[k] = v.Clone()
	}
	return out
}

// verb tokens per kind; matched against the "_"-separated words of the action (and of tool.action).
var (
	contractWords = []string{"contract", "contracts", "proposal", "proposals", "offer", "binding", "sign", "signature", "quote"}
	paymentWords  = []string{"pay", "payment", "payments", "payout", "transfer", "wire", "refund", "charge", "invoice", "invoices", "bill", "billing", "financials", "payroll"}
	deleteWords   = []string{"delete", "remove", "destroy", "purge", "erase", "trash", "drop", "wipe", "unlink"}
	publishWords  = []string{"publish", "tweet", "broadcast", "post", "unpublish"}
	messageWords  = []string{"send", "reply", "forward", "email", "mail", "message", "messages", "notify", "invite", "share", "sms", "dm", "text"}
	spendWords    = []string{"buy", "purchase", "order", "expense", "spend", "subscribe", "book", "rent"}
	internalTools = []string{"documents", "docs", "document", "spreadsheet", "sheets", "fs", "drive", "files", "notes", "memory",
		"artifacts", "reports", "calculator", "workspace", "tasks"}
	internalWords = []string{"create", "update", "edit", "write", "append", "move", "rename", "save", "copy", "set", "add", "log",
		"annotate", "comment", "recalculate", "format", "convert", "tag", "organize", "duplicate", "fill", "document", "income"}
)

func hasWord(words []string, list []string) bool {
	for _, w := range words {
		if inList(list, w) {
			return true
		}
	}
	return false
}

// ActionKind classifies a tool action into the kinds above. Read-only verbs (read, search, draft, analyze...) are
// internal first, so a draft of a contract is internal and sending it is a contract. Unknown actions are "other"
// (they ask under "Only what matters").
func ActionKind(tool, action string, args map[string]any) string {
	tool, action = NormalizeName(tool), NormalizeName(action)
	if IsReadOnly(action) {
		return KindInternal
	}
	words := strings.Split(action, "_")
	switch {
	case hasWord(words, contractWords):
		return KindContracts
	case hasWord(words, paymentWords) || tool == "payments" || tool == "billing":
		return KindPayments
	case hasWord(words, deleteWords):
		return KindDelete
	case hasWord(words, publishWords) && !inList([]string{"email", "gmail", "slack", "chat", "teams", "sms", "whatsapp"}, tool):
		return KindPublish
	case hasWord(words, messageWords) || inList([]string{"email", "gmail", "outlook", "slack", "chat", "teams", "sms", "whatsapp"}, tool):
		return KindMessages
	}
	f := analyze(args)
	if _, ok := f.maxAmount(); ok || f.amountInvalid || hasWord(words, spendWords) {
		return KindSpend
	}
	if inList(internalTools, tool) && hasWord(words, internalWords) || (action == "log_note") {
		return KindInternal
	}
	return KindOther
}

// PermissionRules returns the grants and limits that the choices of one agent add for a call of the given kind.
// They only add supervision on top of an allow grant (the engine lets the strictest rule win):
//
//   - floor categories ask unless the owner lifted them for this agent;
//   - an exception decides its category (ask, never, or ask when a condition holds);
//   - otherwise the level decides: at "rules" (Only what matters) everything but internal work asks; at
//     "autonomous" (Works alone) nothing else asks; at approve_each the engine already asks for everything.
func PermissionRules(agentID string, level Autonomy, a AgentPermissions, kind string) ([]Grant, []Limit) {
	ask := func(id, why string, w Conditions) Grant {
		return Grant{ID: id, Agent: agentID, Tool: "*", Action: "*", Effect: EffectRequireApproval, When: w, Description: why}
	}
	if kind == KindInternal {
		return nil, nil
	}
	if IsFloor(kind) && !a.Lifted(kind) {
		return []Grant{ask("perm.floor:"+kind, "safety floor: "+kind+" always asks", Conditions{})}, nil
	}
	if e, ok := a.Exception(kind); ok && ValidCategory(kind) {
		switch e.Mode {
		case ModeAsk:
			return []Grant{ask("perm.ask:"+kind, "exception: always ask for "+kind, Conditions{})}, nil
		case ModeNever:
			return nil, nil
		case ModeAskIf:
			var gs []Grant
			var ls []Limit
			if e.If == nil {
				return nil, nil
			}
			if e.If.AmountOver != nil {
				v := *e.If.AmountOver
				gs = append(gs, ask("perm.if_amount:"+kind, fmt.Sprintf("exception: ask for %s above %g", kind, v), Conditions{AmountAbove: &v}))
			}
			if e.If.NewRecipient {
				gs = append(gs, ask("perm.if_new_recipient:"+kind, "exception: ask for "+kind+" to a new recipient", Conditions{Recipient: RecipientNew}))
			}
			if e.If.PerDayOver > 0 {
				ls = append(ls, Limit{ID: "perm.per_day:" + kind, Agent: agentID, Tool: "*", Action: "*", Window: 24 * time.Hour,
					MaxCalls: e.If.PerDayOver, OnExceed: EffectRequireApproval})
			}
			return gs, ls
		}
	}
	if level == AutonomyRules {
		return []Grant{ask("perm.important:"+kind, "only what matters: "+kind+" leaves the company or cannot be undone", Conditions{})}, nil
	}
	return nil, nil
}
