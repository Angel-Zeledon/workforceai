import { test, expect } from "@playwright/test";
import { SCENARIO_TEXT } from "../support/env";
import { api, approveUntilDone, resetDemo } from "../support/api";

/** C1: coins are earned by finished work, spent in the shop and shown on the characters. */

interface Wallet { wallet: { balance: number; earned_total: number } }
interface Item { id: string; kind: string; price: number; slot: string }
interface ShopState { inventory: { item_id: string }[]; equipment: { agent_id: string; slot: string; item_id: string }[] }

test.beforeEach(async () => {
  await resetDemo();
});

test("the $50,000 request earns coins; a purchase is equipped on an agent", async ({ page }) => {
  const before = (await api.get<Wallet>("/rewards/wallet")).wallet;
  const { request_id } = await api.post<{ request_id: string }>("/requests", { text: SCENARIO_TEXT });
  expect((await approveUntilDone(request_id)).status).toBe("done");

  // coins are paid in the background right after each task: wait for them
  await expect.poll(async () => (await api.get<Wallet>("/rewards/wallet")).wallet.earned_total, { timeout: 30_000 }).toBeGreaterThan(before.earned_total);
  const wallet = (await api.get<Wallet>("/rewards/wallet")).wallet;
  const ledger = await api.get<{ kind: string; ref_kind: string; agent_id: string; amount: number }[]>("/rewards/ledger?limit=50");
  expect(ledger.filter((e) => e.kind === "earn" && e.ref_kind === "task").length).toBeGreaterThanOrEqual(3);

  await page.goto("/");
  await expect(page.getByTestId("hud-coins")).toHaveAttribute("data-balance", String(wallet.balance));
  await expect(page.getByTestId("hud-cost")).toBeVisible(); // the dollar cost stays, separate from coins

  // pick an affordable character item the organization does not own yet
  const catalog = await api.get<{ items: Item[] }>("/shop/catalog");
  const state = await api.get<ShopState>("/shop/state");
  const owned = new Set(state.inventory.map((o) => o.item_id));
  const item = catalog.items.filter((i) => i.kind === "character" && !owned.has(i.id) && i.price <= wallet.balance).sort((a, b) => a.price - b.price)[0];
  test.skip(!item, "nothing affordable left in this organization (coins accumulate across runs)");

  await page.getByTestId("nav-shop").click();
  await expect(page.getByTestId("shop-view")).toBeVisible();
  await page.getByTestId(`shop-buy-${item.id}`).click();
  await expect(page.getByTestId("hud-coins")).toHaveAttribute("data-balance", String(wallet.balance - item.price));
  await page.getByTestId(`shop-equip-${item.id}`).click();
  await expect(page.getByTestId(`shop-worn-${item.id}`)).toBeVisible();

  const after = await api.get<ShopState>("/shop/state");
  expect(after.equipment.some((e) => e.item_id === item.id && e.slot === item.slot)).toBe(true);
  await page.getByTestId("shop-tab-history").click();
  await expect(page.getByTestId("shop-ledger-row").first()).toBeVisible();
});

test("there is no way to credit coins from the API", async () => {
  for (const path of ["/rewards/grant", "/rewards/topup", "/rewards/wallet", "/shop/coins"]) {
    const res = await fetch(`${process.env.API_URL ?? "http://localhost:8080/api/v1"}${path}`, {
      method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ amount: 1000 }),
    });
    expect([404, 405]).toContain(res.status);
  }
});
