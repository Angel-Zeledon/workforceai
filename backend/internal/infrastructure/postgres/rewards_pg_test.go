package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/rewards"
)

func earnE(task, agent string, n int64) rewards.Entry {
	return rewards.Entry{Kind: rewards.KindEarn, Amount: n, Reason: rewards.ReasonTaskDone, RefKind: rewards.RefTask, RefID: task, AgentID: agent,
		Actor: "system", Title: "T " + task, Detail: map[string]any{"complexity": "M"}, IdemKey: "earn:task:" + task}
}

func spendE(item string, n int64, key string) rewards.Entry {
	return rewards.Entry{Kind: rewards.KindSpend, Amount: n, Reason: rewards.ReasonPurchase, RefKind: rewards.RefItem, RefID: item, Actor: "u1", IdemKey: key}
}

// Coin ledger (migration 530): idempotent earning, atomic purchases under
// concurrency, tenant isolation under RLS and the append-only ledger.
func TestRewardsLedgerWalletAndIsolation(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	seedOrg(t, st, "org-b")

	// Earning is idempotent per task.
	for i := 0; i < 3; i++ {
		e, w, applied, err := st.Earn(ctx, "org-a", earnE("t1", "analyst", 10))
		if err != nil || applied != (i == 0) || w.Balance != 10 || e.Seq != 1 || e.Detail["complexity"] != "M" {
			t.Fatalf("earn #%d: %+v %+v applied=%v %v", i, e, w, applied, err)
		}
	}
	if _, _, _, err := st.Earn(ctx, "org-a", earnE("t2", "sales", 5)); err != nil {
		t.Fatal(err)
	}
	by, _ := st.EarnedByAgent(ctx, "org-a")
	if by["analyst"] != 10 || by["sales"] != 5 {
		t.Fatalf("per agent = %v", by)
	}

	// Purchases: insufficient, ok, replay, owned.
	if _, _, _, err := st.Purchase(ctx, "org-a", spendE("lamp_lava", 260, "buy:lamp_lava"), rewards.Owned{ItemID: "lamp_lava", PricePaid: 260}); !errors.Is(err, rewards.ErrInsufficient) {
		t.Fatalf("overspend: %v", err)
	}
	e, w, replay, err := st.Purchase(ctx, "org-a", spendE("badge_star", 12, "buy:badge_star"), rewards.Owned{ItemID: "badge_star", PricePaid: 12, PurchasedBy: "u1"})
	if err != nil || replay || w.Balance != 3 || w.SpentTotal != 12 || e.Seq != 3 || e.BalanceAfter != 3 {
		t.Fatalf("purchase: %+v %+v replay=%v %v", e, w, replay, err)
	}
	if e2, w2, replay, err := st.Purchase(ctx, "org-a", spendE("badge_star", 12, "buy:badge_star"), rewards.Owned{ItemID: "badge_star"}); err != nil || !replay || e2.ID != e.ID || w2.Balance != 3 {
		t.Fatalf("replay: %+v %+v %v %v", e2, w2, replay, err)
	}
	if _, _, _, err := st.Purchase(ctx, "org-a", spendE("badge_star", 12, "buy:key:other"), rewards.Owned{ItemID: "badge_star"}); !errors.Is(err, rewards.ErrAlreadyOwned) {
		t.Fatalf("second copy: %v", err)
	}
	if inv, _ := st.Inventory(ctx, "org-a"); len(inv) != 1 || inv[0].ItemID != "badge_star" {
		t.Fatalf("inventory = %+v", inv)
	}

	// Equipment and office.
	if err := st.SetEquipped(ctx, "org-a", "analyst", "badge", "badge_star"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetEquipped(ctx, "org-a", "analyst", "badge", "badge_heart"); err != nil { // replaces
		t.Fatal(err)
	}
	if eq, _ := st.Equipment(ctx, "org-a"); len(eq) != 1 || eq[0].ItemID != "badge_heart" {
		t.Fatalf("equipment = %+v", eq)
	}
	if err := st.SetEquipped(ctx, "org-a", "analyst", "badge", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPlaced(ctx, "org-a", "plant_1", "plant_cactus"); err != nil {
		t.Fatal(err)
	}
	if of, _ := st.Office(ctx, "org-a"); len(of) != 1 || of[0].Slot != "plant_1" {
		t.Fatalf("office = %+v", of)
	}

	// Ledger paging, newest first.
	led, _ := st.Ledger(ctx, "org-a", 2, 0)
	if len(led) != 2 || led[0].Seq != 3 || led[1].Seq != 2 {
		t.Fatalf("ledger = %+v", led)
	}
	if led, _ = st.Ledger(ctx, "org-a", 5, 2); len(led) != 1 || led[0].Seq != 1 || led[0].Kind != rewards.KindEarn {
		t.Fatalf("page 2 = %+v", led)
	}

	// Tenant isolation: org-b sees nothing, cannot write rows of org-a.
	if w, _ := st.Wallet(ctx, "org-b"); w.Balance != 0 {
		t.Fatalf("org-b wallet = %+v", w)
	}
	if inv, _ := st.Inventory(ctx, "org-b"); len(inv) != 0 {
		t.Fatal("org-b sees org-a inventory")
	}
	if led, _ := st.Ledger(ctx, "org-b", 10, 0); len(led) != 0 {
		t.Fatal("org-b sees org-a ledger")
	}
	for _, tbl := range []string{"coin_wallets", "coin_ledger", "shop_inventory", "shop_equipment", "shop_office"} {
		err := st.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
			var n int
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+tbl).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				t.Errorf("org-b transaction sees %d rows of %s", n, tbl)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := st.exec(ctx, "org-b", `INSERT INTO coin_wallets (org_id, balance) VALUES ('org-a', 99999)`); err == nil {
		t.Error("org-b wrote a wallet of org-a")
	}
	// Coins cannot be edited or erased by the application role: the ledger is append-only.
	if err := st.exec(ctx, "org-a", `UPDATE coin_ledger SET amount = amount + 1000`); err == nil {
		t.Error("the app role could rewrite the ledger")
	}
	if err := st.exec(ctx, "org-a", `DELETE FROM coin_ledger`); err == nil {
		t.Error("the app role could erase the ledger")
	}
	// The balance can never go negative, even by a direct write.
	if err := st.exec(ctx, "org-a", `UPDATE coin_wallets SET balance = -1`); err == nil {
		t.Error("negative balance accepted")
	}

	down, err := os.ReadFile("../../../migrations/down/530_rewards_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	owner := env.owner(t)
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	var reg *string
	_ = owner.QueryRow(ctx, `SELECT to_regclass('coin_ledger')::text`).Scan(&reg)
	if reg != nil {
		t.Fatalf("down left the table: %s", *reg)
	}
	up, err := migrationsFS.ReadFile("migrations/530_rewards.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := owner.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up after down (%d): %v", i, err)
		}
	}
}

// Parallel purchases and replayed earnings of one organization, many
// connections at once: no overspending, no double payment, gapless sequence.
func TestRewardsConcurrentPurchasesPostgres(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-c")

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ { // 4 distinct tasks, each replayed 4 times in parallel
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, _, err := st.Earn(ctx, "org-c", earnE(fmt.Sprintf("t%d", i%4), "a", 10)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if w, _ := st.Wallet(ctx, "org-c"); w.Balance != 40 || w.EarnedTotal != 40 {
		t.Fatalf("after parallel earn: %+v", w)
	}

	var ok atomic.Int64
	for i := 0; i < 24; i++ { // 12 items of 7 coins, each requested twice; only 5 fit in 40 coins... (5*7=35)
		wg.Add(1)
		go func() {
			defer wg.Done()
			item := fmt.Sprintf("item_%d", i%12)
			_, _, replay, err := st.Purchase(ctx, "org-c", spendE(item, 7, "buy:"+item), rewards.Owned{ItemID: item, PricePaid: 7})
			if err == nil && !replay {
				ok.Add(1)
			} else if err != nil && !errors.Is(err, rewards.ErrInsufficient) {
				t.Errorf("unexpected: %v", err)
			}
		}()
	}
	wg.Wait()
	w, _ := st.Wallet(ctx, "org-c")
	if ok.Load() != 5 || w.Balance != 5 || w.SpentTotal != 35 || w.Balance < 0 {
		t.Fatalf("bought %d items; wallet %+v", ok.Load(), w)
	}
	inv, _ := st.Inventory(ctx, "org-c")
	if len(inv) != 5 {
		t.Fatalf("inventory has %d items", len(inv))
	}
	led, _ := st.Ledger(ctx, "org-c", 100, 0)
	if len(led) != 9 { // 4 earns + 5 spends
		t.Fatalf("ledger has %d entries", len(led))
	}
	for i, e := range led {
		if e.Seq != int64(len(led)-i) {
			t.Fatalf("sequence has gaps: %+v", led)
		}
	}
}
