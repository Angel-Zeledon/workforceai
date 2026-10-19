package rewards

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Event types (additive WS contract, docs/architecture/03-eventos-realtime.md).
const (
	EvCoinsEarned    = "coins.earned"
	EvShopPurchased  = "shop.purchased"
	EvItemEquipped   = "item.equipped"
	EvItemUnequipped = "item.unequipped"
	EvOfficePlaced   = "office.item_placed"
	EvOfficeRemoved  = "office.item_removed"
)

// TaskClassifier is the optional capability of the projects layer that says
// what a finished task is: its complexity and whether it was real work (a
// milestone or a human gate is not). Plain strings keep the packages apart.
type TaskClassifier interface {
	RewardInfo(ctx context.Context, org string, t domain.Task) (complexity string, work bool, projectID string)
}

// Rejections tells whether a human rejected an approval of the task.
type Rejections func(ctx context.Context, org, taskID, requestID string) bool

// Service is the coin economy of the organizations.
type Service struct {
	Store      Store
	Catalog    *Catalog
	Rules      Rules
	Rec        *application.Recorder
	Agents     func(ctx context.Context, org, id string) bool // does the agent exist
	Classifier TaskClassifier
	Rejected   Rejections
	Log        *slog.Logger

	// WelcomeCoins is granted once to every organization the first time it opens the shop or office (0 = no grant).
	WelcomeCoins int64
	welcomed     sync.Map // org id -> struct{}: organizations already checked in this process

	// Daily turns the daily goal on (REWARDS_DAILY_GOAL, default on in main); off, nothing changes in the economy.
	Daily bool
	// Clock (optional) replaces time.Now for the daily goal (tests).
	Clock func() time.Time
}

// ---- earning ----

// OnTaskCompleted pays a finished task once. Safe to call any number of times
// for the same task: the ledger key is unique per task. Errors are logged by
// the caller; earning never affects the task itself.
func (s *Service) OnTaskCompleted(ctx context.Context, org string, t domain.Task) {
	if !s.Rules.Enabled || t.Status != domain.TaskDone {
		return
	}
	f := TaskFacts{Work: true}
	if s.Classifier != nil {
		var cx string
		cx, f.Work, _ = s.Classifier.RewardInfo(ctx, org, t)
		f.Complexity = cx
	}
	if !f.Work {
		return
	}
	if s.Rejected != nil && s.Rejected(ctx, org, t.ID, t.RequestID) {
		f.Rejected = true
	}
	if t.Output != nil && t.Output.Review != nil {
		r := t.Output.Review
		f.Reviewed, f.Verdict, f.Reworks, f.Exhausted = true, r.Verdict, r.Reworks, r.Exhausted
	}
	p := s.Rules.ForTask(f)
	if p.Amount <= 0 {
		return
	}
	detail := map[string]any{"complexity": p.Complexity, "base": p.Base, "bonus": p.Bonus, "reviewed": f.Reviewed, "reworks": f.Reworks}
	s.earn(ctx, org, Entry{
		Kind: KindEarn, Amount: p.Amount, Reason: p.Reason, RefKind: RefTask, RefID: t.ID,
		Title: clip(t.Title, 140), AgentID: t.AgentID, Actor: "system", Detail: detail, IdemKey: "earn:task:" + t.ID,
	}, t.RequestID)
	s.checkDaily(ctx, org) // the daily goal counts finished work only: it can only have been reached by this payment
}

// OnProjectDone pays the completion bonus of a project once.
func (s *Service) OnProjectDone(ctx context.Context, org, projectID, name string, tasksDone int) {
	amount := s.Rules.ForProject(tasksDone)
	if amount <= 0 {
		return
	}
	s.earn(ctx, org, Entry{
		Kind: KindEarn, Amount: amount, Reason: ReasonProjectDone, RefKind: RefProject, RefID: projectID,
		Title: clip(name, 140), Actor: "system", Detail: map[string]any{"tasks_done": tasksDone}, IdemKey: "earn:project:" + projectID,
	}, "")
}

// GrantWelcome gives an organization its starting coins once (idempotent: a restart or a replay pays nothing more).
func (s *Service) GrantWelcome(ctx context.Context, org string, amount int64) {
	if amount <= 0 || !s.Rules.Enabled {
		return
	}
	s.earn(ctx, org, Entry{
		Kind: KindEarn, Amount: amount, Reason: ReasonWelcome, RefKind: RefGrant, RefID: "welcome",
		Title: "welcome", Actor: "system", IdemKey: "grant:welcome",
	}, "")
}

// ensureWelcome grants the welcome coins the first time an organization is seen. The ledger key makes the grant
// idempotent across restarts and replicas; the map only avoids a database round trip on every request.
func (s *Service) ensureWelcome(ctx context.Context, org string) {
	if s.WelcomeCoins <= 0 || !s.Rules.Enabled {
		return
	}
	if _, done := s.welcomed.Load(org); done {
		return
	}
	s.GrantWelcome(ctx, org, s.WelcomeCoins)
	s.welcomed.Store(org, struct{}{})
}

func (s *Service) earn(ctx context.Context, org string, e Entry, requestID string) {
	ctx = application.WithOrg(context.WithoutCancel(ctx), org)
	stored, w, applied, err := s.Store.Earn(ctx, org, e)
	if err != nil {
		s.log().Warn("rewards: earn", "ref", e.RefID, "err", err)
		return
	}
	if !applied {
		return // already paid: replay, retry or restart
	}
	s.emit(ctx, application.Action{Type: EvCoinsEarned, AgentID: stored.AgentID, Entity: "coins", EntityID: stored.RefID, SkipAudit: true,
		Payload: map[string]any{"entry": stored, "wallet": w, "amount": stored.Amount, "agent_id": stored.AgentID, "ref_kind": stored.RefKind, "ref_id": stored.RefID, "request_id": requestID}})
}

// ---- shop ----

// State is everything the Shop screen and the office need in one document.
type State struct {
	Wallet      Wallet           `json:"wallet"`
	EarnedBy    map[string]int64 `json:"earned_by_agent"`
	Inventory   []Owned          `json:"inventory"`
	Equipment   []Equipped       `json:"equipment"`
	Office      []Placed         `json:"office"`
	RulesActive Rules            `json:"rules"`
	// Layout is the office layout document (null when none was saved) and its version.
	Layout        json.RawMessage `json:"layout"`
	LayoutVersion int             `json:"layout_version"`
}

// State returns the wallet, the inventory and the equipped/placed items.
func (s *Service) State(ctx context.Context, org string) (State, error) {
	var st State
	var err error
	s.ensureWelcome(ctx, org)
	if st.Wallet, err = s.Store.Wallet(ctx, org); err != nil {
		return st, err
	}
	if st.EarnedBy, err = s.Store.EarnedByAgent(ctx, org); err != nil {
		return st, err
	}
	if st.Inventory, err = s.Store.Inventory(ctx, org); err != nil {
		return st, err
	}
	if st.Equipment, err = s.Store.Equipment(ctx, org); err != nil {
		return st, err
	}
	if st.Office, err = s.Store.Office(ctx, org); err != nil {
		return st, err
	}
	if st.Layout, st.LayoutVersion, err = s.Store.GetLayout(ctx, org); err != nil {
		return st, err
	}
	st.RulesActive = s.Rules
	return st, nil
}

// PurchaseResult is what POST /shop/purchases returns.
type PurchaseResult struct {
	Item   Item   `json:"item"`
	Entry  Entry  `json:"entry"`
	Wallet Wallet `json:"wallet"`
	Replay bool   `json:"replay"`
}

// Buy spends coins on an item. key makes the request idempotent: the same key
// returns the original result without charging again; without a key the item
// itself is the key (an item can only be owned once).
func (s *Service) Buy(ctx context.Context, org, actor, itemID, key string) (PurchaseResult, error) {
	it, ok := s.Catalog.Item(itemID)
	if !ok {
		return PurchaseResult{}, fmt.Errorf("%w: unknown item", domain.ErrNotFound)
	}
	idem := "buy:" + itemID
	if k := strings.TrimSpace(key); k != "" {
		if len(k) > 120 {
			return PurchaseResult{}, fmt.Errorf("%w: idempotency key too long", domain.ErrInvalid)
		}
		idem = "buy:key:" + k
	}
	e := Entry{Kind: KindSpend, Amount: it.Price, Reason: ReasonPurchase, RefKind: RefItem, RefID: it.ID, Title: it.ID, Actor: actor, IdemKey: idem}
	stored, w, replay, err := s.Store.Purchase(ctx, org, e, Owned{ItemID: it.ID, PricePaid: it.Price, PurchasedBy: actor})
	if err != nil {
		return PurchaseResult{}, err
	}
	if stored.RefID != it.ID { // the key was used for another item
		return PurchaseResult{}, fmt.Errorf("%w: idempotency key already used for another item", domain.ErrConflict)
	}
	if !replay {
		s.audit(ctx, actor, EvShopPurchased, "shop_item", it.ID, map[string]any{"price": it.Price, "balance_after": w.Balance})
		s.emit(ctx, application.Action{Type: EvShopPurchased, SkipAudit: true, Entity: "shop_item", EntityID: it.ID,
			Payload: map[string]any{"item": it, "entry": stored, "wallet": w, "actor": actor}})
	}
	return PurchaseResult{Item: it, Entry: stored, Wallet: w, Replay: replay}, nil
}

func (s *Service) owned(ctx context.Context, org, itemID string) (bool, error) {
	inv, err := s.Store.Inventory(ctx, org)
	if err != nil {
		return false, err
	}
	for _, o := range inv {
		if o.ItemID == itemID {
			return true, nil
		}
	}
	return false, nil
}

// Equip puts an owned character item on an agent, replacing what the agent
// wore in that slot.
func (s *Service) Equip(ctx context.Context, org, actor, agentID, itemID string) (Equipped, error) {
	it, ok := s.Catalog.Item(itemID)
	if !ok || it.Kind != KindCharacter {
		return Equipped{}, fmt.Errorf("%w: not a character item", domain.ErrInvalid)
	}
	if s.Agents != nil && !s.Agents(ctx, org, agentID) {
		return Equipped{}, fmt.Errorf("%w: agent", domain.ErrNotFound)
	}
	if has, err := s.owned(ctx, org, itemID); err != nil {
		return Equipped{}, err
	} else if !has {
		return Equipped{}, ErrNotOwned
	}
	if err := s.Store.SetEquipped(ctx, org, agentID, it.Slot, itemID); err != nil {
		return Equipped{}, err
	}
	eq := Equipped{AgentID: agentID, Slot: it.Slot, ItemID: itemID}
	s.audit(ctx, actor, EvItemEquipped, "agent", agentID, map[string]any{"slot": it.Slot, "item_id": itemID})
	s.emit(ctx, application.Action{Type: EvItemEquipped, SkipAudit: true, AgentID: agentID, Entity: "agent", EntityID: agentID,
		Payload: map[string]any{"agent_id": agentID, "slot": it.Slot, "item_id": itemID, "item": it, "actor": actor}})
	return eq, nil
}

// Unequip empties a character slot of an agent.
func (s *Service) Unequip(ctx context.Context, org, actor, agentID, slot string) error {
	if !validSlot(s.Catalog, KindCharacter, slot) {
		return fmt.Errorf("%w: unknown slot", domain.ErrInvalid)
	}
	if s.Agents != nil && !s.Agents(ctx, org, agentID) {
		return fmt.Errorf("%w: agent", domain.ErrNotFound)
	}
	if err := s.Store.SetEquipped(ctx, org, agentID, slot, ""); err != nil {
		return err
	}
	s.audit(ctx, actor, EvItemUnequipped, "agent", agentID, map[string]any{"slot": slot})
	s.emit(ctx, application.Action{Type: EvItemUnequipped, SkipAudit: true, AgentID: agentID, Entity: "agent", EntityID: agentID,
		Payload: map[string]any{"agent_id": agentID, "slot": slot, "actor": actor}})
	return nil
}

// Place puts an owned office item in its office slot.
func (s *Service) Place(ctx context.Context, org, actor, itemID string) (Placed, error) {
	it, ok := s.Catalog.Item(itemID)
	if !ok || it.Kind != KindOffice {
		return Placed{}, fmt.Errorf("%w: not an office item", domain.ErrInvalid)
	}
	if layoutSlot(it.Slot) {
		return Placed{}, fmt.Errorf("%w: %s items are used through the office layout, not placed in a slot", domain.ErrInvalid, it.Slot)
	}
	if has, err := s.owned(ctx, org, itemID); err != nil {
		return Placed{}, err
	} else if !has {
		return Placed{}, ErrNotOwned
	}
	if err := s.Store.SetPlaced(ctx, org, it.Slot, itemID); err != nil {
		return Placed{}, err
	}
	s.emit(ctx, application.Action{Type: EvOfficePlaced, SkipAudit: true, Entity: "office", EntityID: it.Slot,
		Payload: map[string]any{"slot": it.Slot, "item_id": itemID, "item": it, "actor": actor}})
	s.audit(ctx, actor, EvOfficePlaced, "office", it.Slot, map[string]any{"item_id": itemID})
	return Placed{Slot: it.Slot, ItemID: itemID}, nil
}

// Remove empties an office slot.
func (s *Service) Remove(ctx context.Context, org, actor, slot string) error {
	if layoutSlot(slot) || !validSlot(s.Catalog, KindOffice, slot) {
		return fmt.Errorf("%w: unknown slot", domain.ErrInvalid)
	}
	if err := s.Store.SetPlaced(ctx, org, slot, ""); err != nil {
		return err
	}
	s.audit(ctx, actor, EvOfficeRemoved, "office", slot, nil)
	s.emit(ctx, application.Action{Type: EvOfficeRemoved, SkipAudit: true, Entity: "office", EntityID: slot,
		Payload: map[string]any{"slot": slot, "actor": actor}})
	return nil
}

func validSlot(c *Catalog, kind, slot string) bool {
	for _, s := range c.Slots(kind) {
		if s == slot {
			return true
		}
	}
	return false
}

func (s *Service) emit(ctx context.Context, a application.Action) {
	if s.Rec != nil {
		s.Rec.Emit(ctx, a)
	}
}

func (s *Service) audit(ctx context.Context, actor, action, entity, id string, details map[string]any) {
	if s.Rec != nil {
		s.Rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: action, Entity: entity, EntityID: id, Details: details})
	}
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func clip(v string, n int) string {
	r := []rune(strings.TrimSpace(v))
	if len(r) > n {
		return string(r[:n])
	}
	return string(r)
}
