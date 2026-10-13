package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/rewards"
)

// Coins and shop API (docs/architecture/rewards.md, 08-api.md). There is no
// endpoint that credits coins: they are earned only by finished work, inside
// the backend. Reading is open to every role; buying, equipping and placing
// need shop:buy (member and above), and every one of them is audited.

func (s *server) mountRewards(r chi.Router) {
	if s.Rewards == nil {
		return
	}
	read, buy := s.can(auth.PermShopRead), s.can(auth.PermShopBuy)
	r.With(read).Get("/rewards/wallet", s.rewardsWallet)
	r.With(read).Get("/rewards/ledger", s.rewardsLedger)
	r.With(read).Get("/rewards/rules", s.rewardsRules)
	r.With(read).Get("/shop/catalog", s.shopCatalog)
	r.With(read).Get("/shop/state", s.shopState)
	r.With(buy).Post("/shop/purchases", s.shopBuy)
	r.With(buy).Put("/agents/{id}/equipment/{slot}", s.shopEquip)
	r.With(buy).Delete("/agents/{id}/equipment/{slot}", s.shopUnequip)
	r.With(buy).Put("/office/items/{slot}", s.shopPlace)
	r.With(buy).Delete("/office/items/{slot}", s.shopRemove)
	r.With(read).Get("/office/layout", s.officeLayout)
	r.With(buy).Put("/office/layout", s.officeLayoutSave)
}

func (s *server) officeLayout(w http.ResponseWriter, r *http.Request) {
	d, err := s.Rewards.GetLayout(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, d)
}

// officeLayoutSave replaces the layout if base_version is the stored version; a stale one answers 409
// layout_conflict with the current document. Validation errors are 400 with code "invalid".
func (s *server) officeLayoutSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Layout      json.RawMessage `json:"layout"`
		BaseVersion int             `json:"base_version"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	d, err := s.Rewards.SaveLayout(r.Context(), s.org(r), s.actor(r), body.Layout, body.BaseVersion)
	if err != nil {
		if ce, ok := rewards.AsConflict(err); ok {
			writeJSON(w, http.StatusConflict, map[string]any{"error": ce.Error(), "code": rewards.CodeLayoutConflict, "version": ce.Version, "layout": ce.Layout})
			return
		}
		if errors.Is(err, domain.ErrInvalid) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "code": rewards.CodeInvalidLayout})
			return
		}
		s.rewardsFail(w, err)
		return
	}
	writeJSON(w, 200, d)
}

// rewardsFail maps rewards errors; the stable code travels in the body.
func (s *server) rewardsFail(w http.ResponseWriter, err error) {
	code := ""
	switch {
	case errors.Is(err, rewards.ErrInsufficient):
		code = rewards.CodeInsufficient
	case errors.Is(err, rewards.ErrAlreadyOwned):
		code = rewards.CodeAlreadyOwned
	case errors.Is(err, rewards.ErrNotOwned):
		code = rewards.CodeNotOwned
	}
	if code == "" {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "code": code})
}

func (s *server) rewardsWallet(w http.ResponseWriter, r *http.Request) {
	st, err := s.Rewards.State(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"wallet": st.Wallet, "earned_by_agent": st.EarnedBy})
}

func (s *server) rewardsLedger(w http.ResponseWriter, r *http.Request) {
	limit, before := 50, int64(0)
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, 200)
	}
	if n, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64); err == nil && n > 0 {
		before = n
	}
	v, err := s.Rewards.Store.Ledger(r.Context(), s.org(r), limit, before)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, list(v))
}

func (s *server) rewardsRules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Rewards.Rules)
}

func (s *server) shopCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"version": s.Rewards.Catalog.Version, "items": s.Rewards.Catalog.Items,
		"character_slots": s.Rewards.Catalog.Slots(rewards.KindCharacter), "office_slots": s.Rewards.Catalog.Slots(rewards.KindOffice)})
}

func (s *server) shopState(w http.ResponseWriter, r *http.Request) {
	st, err := s.Rewards.State(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	st.Inventory, st.Equipment, st.Office = list(st.Inventory), list(st.Equipment), list(st.Office)
	writeJSON(w, 200, st)
}

func (s *server) shopBuy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ItemID         string `json:"item_id"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	key := body.IdempotencyKey
	if h := r.Header.Get("Idempotency-Key"); h != "" {
		key = h
	}
	res, err := s.Rewards.Buy(r.Context(), s.org(r), s.actor(r), body.ItemID, key)
	if err != nil {
		s.rewardsFail(w, err)
		return
	}
	status := http.StatusCreated
	if res.Replay {
		status = http.StatusOK
	}
	writeJSON(w, status, res)
}

func (s *server) shopEquip(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ItemID string `json:"item_id"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	it, ok := s.Rewards.Catalog.Item(body.ItemID)
	if !ok || it.Slot != chi.URLParam(r, "slot") {
		s.fail(w, errors.Join(domain.ErrInvalid, errors.New("item does not fit that slot")))
		return
	}
	eq, err := s.Rewards.Equip(r.Context(), s.org(r), s.actor(r), chi.URLParam(r, "id"), body.ItemID)
	if err != nil {
		s.rewardsFail(w, err)
		return
	}
	writeJSON(w, 200, eq)
}

func (s *server) shopUnequip(w http.ResponseWriter, r *http.Request) {
	agent, slot := chi.URLParam(r, "id"), chi.URLParam(r, "slot")
	if err := s.Rewards.Unequip(r.Context(), s.org(r), s.actor(r), agent, slot); err != nil {
		s.rewardsFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) shopPlace(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ItemID string `json:"item_id"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	it, ok := s.Rewards.Catalog.Item(body.ItemID)
	if !ok || it.Slot != chi.URLParam(r, "slot") {
		s.fail(w, errors.Join(domain.ErrInvalid, errors.New("item does not fit that slot")))
		return
	}
	p, err := s.Rewards.Place(r.Context(), s.org(r), s.actor(r), body.ItemID)
	if err != nil {
		s.rewardsFail(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *server) shopRemove(w http.ResponseWriter, r *http.Request) {
	slot := chi.URLParam(r, "slot")
	if err := s.Rewards.Remove(r.Context(), s.org(r), s.actor(r), slot); err != nil {
		s.rewardsFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
