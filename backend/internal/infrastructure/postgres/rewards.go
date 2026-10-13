package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/rewards"
)

// Coin ledger, wallet and shop state (internal/rewards; migration 530_rewards.sql, RLS by org).
// The wallet row is locked (SELECT ... FOR UPDATE) for every earn and purchase
// of an organization, which serializes them: parallel purchases can never
// overspend and the ledger sequence has no gaps or duplicates.

var _ rewards.Store = (*Store)(nil)

const entryCols = `id, seq, kind, amount, reason, ref_kind, ref_id, title, agent_id, actor, detail, balance_after, idem_key, created_at`

func scanEntry(r scanner) (rewards.Entry, error) {
	var e rewards.Entry
	var detail []byte
	if err := r.Scan(&e.ID, &e.Seq, &e.Kind, &e.Amount, &e.Reason, &e.RefKind, &e.RefID, &e.Title, &e.AgentID, &e.Actor, &detail, &e.BalanceAfter, &e.IdemKey, &e.CreatedAt); err != nil {
		return e, err
	}
	if len(detail) > 0 {
		_ = json.Unmarshal(detail, &e.Detail)
	}
	return e, nil
}

// lockWallet makes sure the wallet row exists and locks it for the transaction.
func lockWallet(ctx context.Context, tx pgx.Tx, org string) (w rewards.Wallet, entries int64, err error) {
	if _, err = tx.Exec(ctx, `INSERT INTO coin_wallets (org_id) VALUES ($1) ON CONFLICT (org_id) DO NOTHING`, org); err != nil {
		return
	}
	err = tx.QueryRow(ctx, `SELECT balance, earned_total, spent_total, entries FROM coin_wallets WHERE org_id=$1 FOR UPDATE`, org).
		Scan(&w.Balance, &w.EarnedTotal, &w.SpentTotal, &entries)
	return
}

func entryByKey(ctx context.Context, tx pgx.Tx, org, key string) (rewards.Entry, bool, error) {
	e, err := scanEntry(tx.QueryRow(ctx, `SELECT `+entryCols+` FROM coin_ledger WHERE org_id=$1 AND idem_key=$2`, org, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return e, false, nil
	}
	return e, err == nil, err
}

func appendEntry(ctx context.Context, tx pgx.Tx, org string, e rewards.Entry, w rewards.Wallet, entries int64) (rewards.Entry, error) {
	e.ID, e.Seq, e.BalanceAfter = uuid.NewString(), entries+1, w.Balance
	detail, _ := json.Marshal(e.Detail)
	if len(detail) == 0 || string(detail) == "null" {
		detail = []byte("{}")
	}
	err := tx.QueryRow(ctx, `INSERT INTO coin_ledger (id, org_id, seq, kind, amount, reason, ref_kind, ref_id, title, agent_id, actor, detail, balance_after, idem_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING created_at`,
		e.ID, org, e.Seq, e.Kind, e.Amount, e.Reason, e.RefKind, e.RefID, e.Title, e.AgentID, e.Actor, detail, e.BalanceAfter, e.IdemKey).Scan(&e.CreatedAt)
	if err != nil {
		return e, err
	}
	_, err = tx.Exec(ctx, `UPDATE coin_wallets SET balance=$2, earned_total=$3, spent_total=$4, entries=$5, updated_at=now() WHERE org_id=$1`,
		org, w.Balance, w.EarnedTotal, w.SpentTotal, e.Seq)
	return e, err
}

// Earn implements rewards.Store.
func (s *Store) Earn(ctx context.Context, org string, e rewards.Entry) (out rewards.Entry, w rewards.Wallet, applied bool, err error) {
	err = s.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var n int64
		var err error
		if w, n, err = lockWallet(ctx, tx, org); err != nil {
			return err
		}
		if prev, ok, err := entryByKey(ctx, tx, org, e.IdemKey); err != nil || ok {
			out = prev
			return err
		}
		w.Balance += e.Amount
		w.EarnedTotal += e.Amount
		if out, err = appendEntry(ctx, tx, org, e, w, n); err != nil {
			return err
		}
		applied = true
		return nil
	})
	return out, w, applied && err == nil, err
}

// Purchase implements rewards.Store.
func (s *Store) Purchase(ctx context.Context, org string, e rewards.Entry, o rewards.Owned) (out rewards.Entry, w rewards.Wallet, replay bool, err error) {
	err = s.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var n int64
		var err error
		if w, n, err = lockWallet(ctx, tx, org); err != nil {
			return err
		}
		if prev, ok, err := entryByKey(ctx, tx, org, e.IdemKey); err != nil || ok {
			out, replay = prev, ok
			return err
		}
		var has bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM shop_inventory WHERE org_id=$1 AND item_id=$2)`, org, o.ItemID).Scan(&has); err != nil {
			return err
		}
		if has {
			return rewards.ErrAlreadyOwned
		}
		if w.Balance < e.Amount {
			return rewards.ErrInsufficient
		}
		w.Balance -= e.Amount
		w.SpentTotal += e.Amount
		if _, err := tx.Exec(ctx, `INSERT INTO shop_inventory (org_id, item_id, price_paid, purchased_by) VALUES ($1,$2,$3,$4)`, org, o.ItemID, o.PricePaid, o.PurchasedBy); err != nil {
			return err
		}
		out, err = appendEntry(ctx, tx, org, e, w, n)
		return err
	})
	if err != nil {
		w = rewards.Wallet{}
	}
	return out, w, replay, err
}

// Wallet implements rewards.Store.
func (s *Store) Wallet(ctx context.Context, org string) (rewards.Wallet, error) {
	var w rewards.Wallet
	err := s.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT balance, earned_total, spent_total FROM coin_wallets WHERE org_id=$1`, org).Scan(&w.Balance, &w.EarnedTotal, &w.SpentTotal)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	return w, err
}

// Ledger implements rewards.Store.
func (s *Store) Ledger(ctx context.Context, org string, limit int, before int64) ([]rewards.Entry, error) {
	return many(ctx, s, org, scanEntry, `SELECT `+entryCols+` FROM coin_ledger WHERE org_id=$1 AND ($3::bigint = 0 OR seq < $3) ORDER BY seq DESC LIMIT $2`, org, limit, before)
}

// EarnedByAgent implements rewards.Store.
func (s *Store) EarnedByAgent(ctx context.Context, org string) (map[string]int64, error) {
	out := map[string]int64{}
	err := s.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT agent_id, COALESCE(SUM(amount),0) FROM coin_ledger WHERE org_id=$1 AND kind='earn' AND agent_id <> '' GROUP BY agent_id`, org)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a string
			var n int64
			if err := rows.Scan(&a, &n); err != nil {
				return err
			}
			out[a] = n
		}
		return rows.Err()
	})
	return out, err
}

// Inventory implements rewards.Store.
func (s *Store) Inventory(ctx context.Context, org string) ([]rewards.Owned, error) {
	return many(ctx, s, org, func(r scanner) (rewards.Owned, error) {
		var o rewards.Owned
		err := r.Scan(&o.ItemID, &o.PricePaid, &o.PurchasedBy, &o.PurchasedAt)
		return o, err
	}, `SELECT item_id, price_paid, purchased_by, purchased_at FROM shop_inventory WHERE org_id=$1 ORDER BY item_id`, org)
}

// SetEquipped implements rewards.Store.
func (s *Store) SetEquipped(ctx context.Context, org, agent, slot, item string) error {
	if item == "" {
		return s.exec(ctx, org, `DELETE FROM shop_equipment WHERE org_id=$1 AND agent_id=$2 AND slot=$3`, org, agent, slot)
	}
	return s.exec(ctx, org, `INSERT INTO shop_equipment (org_id, agent_id, slot, item_id) VALUES ($1,$2,$3,$4)
		ON CONFLICT (org_id, agent_id, slot) DO UPDATE SET item_id=EXCLUDED.item_id`, org, agent, slot, item)
}

// Equipment implements rewards.Store.
func (s *Store) Equipment(ctx context.Context, org string) ([]rewards.Equipped, error) {
	return many(ctx, s, org, func(r scanner) (rewards.Equipped, error) {
		var e rewards.Equipped
		err := r.Scan(&e.AgentID, &e.Slot, &e.ItemID)
		return e, err
	}, `SELECT agent_id, slot, item_id FROM shop_equipment WHERE org_id=$1 ORDER BY agent_id, slot`, org)
}

// SetPlaced implements rewards.Store.
func (s *Store) SetPlaced(ctx context.Context, org, slot, item string) error {
	if item == "" {
		return s.exec(ctx, org, `DELETE FROM shop_office WHERE org_id=$1 AND slot=$2`, org, slot)
	}
	return s.exec(ctx, org, `INSERT INTO shop_office (org_id, slot, item_id) VALUES ($1,$2,$3)
		ON CONFLICT (org_id, slot) DO UPDATE SET item_id=EXCLUDED.item_id`, org, slot, item)
}

// Office implements rewards.Store.
func (s *Store) Office(ctx context.Context, org string) ([]rewards.Placed, error) {
	return many(ctx, s, org, func(r scanner) (rewards.Placed, error) {
		var p rewards.Placed
		err := r.Scan(&p.Slot, &p.ItemID)
		return p, err
	}, `SELECT slot, item_id FROM shop_office WHERE org_id=$1 ORDER BY slot`, org)
}

// GetLayout implements rewards.Store.
func (s *Store) GetLayout(ctx context.Context, org string) (json.RawMessage, int, error) {
	var doc []byte
	var v int
	err := s.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT doc, version FROM office_layout WHERE org_id=$1`, org).Scan(&doc, &v)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	return doc, v, err
}

// SaveLayout implements rewards.Store: a compare-and-set on the version, atomic under concurrency (the first
// save races on the primary key, later ones on the version predicate of the UPDATE).
func (s *Store) SaveLayout(ctx context.Context, org string, doc json.RawMessage, base int, actor string) (int, error) {
	var out int
	err := s.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var err error
		if base == 0 {
			err = tx.QueryRow(ctx, `INSERT INTO office_layout (org_id, doc, version, updated_by) VALUES ($1,$2,1,$3)
				ON CONFLICT (org_id) DO NOTHING RETURNING version`, org, []byte(doc), actor).Scan(&out)
		} else {
			err = tx.QueryRow(ctx, `UPDATE office_layout SET doc=$2, version=version+1, updated_at=now(), updated_by=$4
				WHERE org_id=$1 AND version=$3 RETURNING version`, org, []byte(doc), base, actor).Scan(&out)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		ce := &rewards.LayoutConflictError{}
		var cur []byte
		switch err := tx.QueryRow(ctx, `SELECT doc, version FROM office_layout WHERE org_id=$1`, org).Scan(&cur, &ce.Version); {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		default:
			ce.Layout = cur
		}
		return ce
	})
	return out, err
}
