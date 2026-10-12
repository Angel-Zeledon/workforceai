// Package rewards implements the coin system: coins are EARNED ONLY BY REAL
// WORK (a finished task, a finished project) and spent on cosmetic items for
// the characters and the office. Rules that never change:
//
//   - there is no way to obtain coins other than the earning rules below: no
//     endpoint credits coins, none sells them, and nothing speeds work up;
//   - the ledger is append-only and is the source of truth; the wallet is the
//     running balance and can never go negative;
//   - a task (or project) pays once: the ledger key is unique per
//     (organization, idempotency key), so retries, restarts and replays of the
//     same event are harmless;
//   - the dollar cost of the AI is a separate concern and is never mixed in.
//
// docs/architecture/rewards.md has the full description.
package rewards

import (
	"fmt"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Ledger entry kinds.
const (
	KindEarn  = "earn"
	KindSpend = "spend"
)

// Reasons (codes; the UI translates them).
const (
	ReasonTaskDone      = "task_done"       // base pay
	ReasonTaskFirstTry  = "task_first_try"  // base + bonus: passed the quality review on the first try
	ReasonTaskRework    = "task_rework"     // reduced pay: the output needed rework
	ReasonProjectDone   = "project_done"    // project completion bonus
	ReasonPurchase      = "purchase"        // spent on a shop item
	RefTask, RefProject = "task", "project" // ref kinds of earn entries
	RefItem             = "item"            // ref kind of a purchase
)

// Entry is one line of the append-only coin ledger. Amount is always positive;
// Kind says whether it was earned or spent.
type Entry struct {
	ID      string `json:"id"`
	Seq     int64  `json:"seq"` // 1-based, per organization
	Kind    string `json:"kind"`
	Amount  int64  `json:"amount"`
	Reason  string `json:"reason"`
	RefKind string `json:"ref_kind"`
	RefID   string `json:"ref_id"`
	// Title is a short human label of what the entry is about (task title,
	// project name, item id). Never content of the work.
	Title   string `json:"title,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
	Actor   string `json:"actor"`
	// Detail explains the amount (base, bonus, complexity, reworks...).
	Detail       map[string]any `json:"detail,omitempty"`
	BalanceAfter int64          `json:"balance_after"`
	CreatedAt    time.Time      `json:"created_at"`
	// IdemKey is the unique key of the entry per organization.
	IdemKey string `json:"-"`
}

// Wallet is the coin balance of an organization.
type Wallet struct {
	Balance     int64 `json:"balance"`
	EarnedTotal int64 `json:"earned_total"`
	SpentTotal  int64 `json:"spent_total"`
}

// Owned is an item an organization bought (each item is owned once).
type Owned struct {
	ItemID      string    `json:"item_id"`
	PricePaid   int64     `json:"price_paid"`
	PurchasedBy string    `json:"purchased_by"`
	PurchasedAt time.Time `json:"purchased_at"`
}

// Equipped says which character item an agent wears in a slot.
type Equipped struct {
	AgentID string `json:"agent_id"`
	Slot    string `json:"slot"`
	ItemID  string `json:"item_id"`
}

// Placed says which office item stands in an office slot.
type Placed struct {
	Slot   string `json:"slot"`
	ItemID string `json:"item_id"`
}

// Errors (all map to HTTP 409 through domain.ErrConflict, 404/400 as noted).
var (
	ErrInsufficient = fmt.Errorf("%w: not enough coins", domain.ErrConflict)
	ErrAlreadyOwned = fmt.Errorf("%w: item already owned", domain.ErrConflict)
	ErrNotOwned     = fmt.Errorf("%w: item not owned", domain.ErrConflict)
)

// Error codes of the purchase endpoint (stable, the UI translates them).
const (
	CodeInsufficient = "insufficient_coins"
	CodeAlreadyOwned = "already_owned"
	CodeNotOwned     = "not_owned"
)
