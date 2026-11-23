import catalogJson from "./shopCatalog.json" with { type: "json" };
import type { Catalog, LedgerEntry, ShopItem, Wallet } from "../rewards";

type Emit = (type: string, payload: unknown, agentId?: string) => void;

/**
 * Mock of the coin system (same contract as backend/internal/rewards, docs/architecture/08-api.md section 20).
 * Coins are earned ONLY when a mock task finishes (once per task, like the real ledger) and spent in the shop;
 * there is no way to credit coins from the UI.
 */
const BASE: Record<string, number> = { S: 5, M: 10, L: 20, XL: 40 };

export interface LayoutStorage { getItem(k: string): string | null; setItem(k: string, v: string): void }
const LAYOUT_KEY = "aiw.mock.office.layout.v1";
const defaultStorage = (): LayoutStorage | null => { try { return typeof localStorage === "undefined" ? null : localStorage; } catch { return null; } };

// What backend/internal/rewards/layout.go does to the style of a saved layout (the rest of the document is kept as sent).
const STYLES = ["classic", "studio", "flat", "clay", "pixel"];
const LEGACY_STYLES: Record<string, string> = { bricks: "pixel", paper: "flat", real: "studio", toon: "classic" };
/** Saved-layout rules for the style: an old name becomes its replacement, "classic" is stored as no style, an unknown one is a 400. */
export function normalizeLayoutDoc(layout: unknown): unknown {
  if (!layout || typeof layout !== "object") return layout ?? null;
  const l = { ...(layout as Record<string, unknown>) };
  let style = typeof l.style === "string" ? l.style : "";
  if (style && LEGACY_STYLES[style]) style = LEGACY_STYLES[style];
  if (style === "classic") style = "";
  if (style && !STYLES.includes(style)) throw new Error("PUT /office/layout -> 400");
  if (style) l.style = style; else delete l.style;
  return l;
}

export class MockRewards {
  private catalog = catalogJson as unknown as Catalog;
  private wallet: Wallet = { balance: 0, earned_total: 0, spent_total: 0 };
  private ledger: LedgerEntry[] = [];
  private keys = new Set<string>();
  private owned = new Map<string, { item_id: string; price_paid: number; purchased_by: string; purchased_at: string }>();
  private equip = new Map<string, string>(); // "agent|slot" -> item
  private office = new Map<string, string>();
  private earnedBy: Record<string, number> = {};
  /** The office layout document (docs/architecture/office-editor.md). The demo keeps it in the browser's localStorage so a
   *  reload keeps the saved style and furniture, as the backend would (the backend stores it in the database). */
  private layout: unknown = null;
  private layoutVersion = 0;
  private store: LayoutStorage | null;
  private seq = 0;

  private emit: Emit;
  /** The demo office starts with `startCoins` (same as the backend's REWARDS_DEMO_COINS welcome grant). */
  constructor(emit: Emit, startCoins = 1000, store: LayoutStorage | null = defaultStorage()) {
    this.emit = emit;
    this.store = store;
    try {
      const saved = JSON.parse(store?.getItem(LAYOUT_KEY) ?? "null") as { layout: unknown; version: number } | null;
      if (saved && typeof saved.version === "number") { this.layout = saved.layout ?? null; this.layoutVersion = saved.version; }
    } catch { /* private mode or a broken value: start from the built-in look */ }
    if (startCoins > 0) {
      this.keys.add("grant:welcome");
      this.wallet = { balance: startCoins, earned_total: startCoins, spent_total: 0 };
      this.push({ kind: "earn", amount: startCoins, reason: "welcome_grant", ref_kind: "grant", ref_id: "welcome", title: "welcome", actor: "system" });
    }
  }

  matches(seg: string[]) {
    const [r0, r1, r2] = seg;
    return r0 === "rewards" || r0 === "shop" || (r0 === "agents" && r2 === "equipment") || (r0 === "office" && (r1 === "items" || r1 === "layout"));
  }

  private push(e: Omit<LedgerEntry, "id" | "seq" | "balance_after" | "created_at">): LedgerEntry {
    const entry: LedgerEntry = { ...e, id: `coin-${++this.seq}-${Math.random().toString(36).slice(2, 7)}`, seq: this.seq, balance_after: this.wallet.balance, created_at: new Date().toISOString() };
    this.ledger.unshift(entry);
    return entry;
  }

  /** A mock task finished: pays once per task (idempotent), by its complexity (default M). */
  onTaskDone(task: { id: string; title: string; agent_id: string }, complexity = "M") {
    const key = `earn:task:${task.id}`;
    if (this.keys.has(key)) return;
    this.keys.add(key);
    const amount = BASE[complexity] ?? BASE.M;
    this.wallet = { ...this.wallet, balance: this.wallet.balance + amount, earned_total: this.wallet.earned_total + amount };
    this.earnedBy[task.agent_id] = (this.earnedBy[task.agent_id] || 0) + amount;
    const entry = this.push({ kind: "earn", amount, reason: "task_done", ref_kind: "task", ref_id: task.id, title: task.title, agent_id: task.agent_id, actor: "system", detail: { complexity, base: amount, bonus: 0 } });
    this.emit("coins.earned", { entry, wallet: this.wallet, amount, agent_id: task.agent_id, ref_kind: "task", ref_id: task.id }, task.agent_id);
  }

  private state() {
    return {
      wallet: this.wallet, earned_by_agent: this.earnedBy,
      inventory: [...this.owned.values()],
      equipment: [...this.equip].map(([k, item_id]) => { const [agent_id, slot] = k.split("|"); return { agent_id, slot, item_id }; }),
      office: [...this.office].map(([slot, item_id]) => ({ slot, item_id })),
    };
  }
  private item(id: string): ShopItem | undefined { return this.catalog.items.find((i) => i.id === id); }

  async handle(method: string, seg: string[], q: URLSearchParams, body: any): Promise<unknown> {
    const [r0, r1, r2, r3] = seg;
    if (method === "GET") {
      if (r0 === "shop" && r1 === "catalog") return this.catalog;
      if (r0 === "shop" && r1 === "state") return this.state();
      if (r0 === "rewards" && r1 === "wallet") return { wallet: this.wallet, earned_by_agent: this.earnedBy };
      if (r0 === "rewards" && r1 === "ledger") return this.ledger.slice(0, Number(q.get("limit") || 50));
    }
    if (method === "POST" && r0 === "shop" && r1 === "purchases") {
      const it = this.item(String(body?.item_id));
      if (!it) throw new Error("POST /shop/purchases -> 404");
      if (this.owned.has(it.id) || this.wallet.balance < it.price) throw new Error("POST /shop/purchases -> 409");
      this.wallet = { ...this.wallet, balance: this.wallet.balance - it.price, spent_total: this.wallet.spent_total + it.price };
      this.owned.set(it.id, { item_id: it.id, price_paid: it.price, purchased_by: "user", purchased_at: new Date().toISOString() });
      const entry = this.push({ kind: "spend", amount: it.price, reason: "purchase", ref_kind: "item", ref_id: it.id, title: it.id, actor: "user" });
      const out = { item: it, entry, wallet: this.wallet, replay: false };
      this.emit("shop.purchased", out);
      return out;
    }
    if (r0 === "agents" && r2 === "equipment") {
      if (method === "PUT") {
        const it = this.item(String(body?.item_id));
        if (!it || it.kind !== "character" || it.slot !== r3) throw new Error("PUT equipment -> 400");
        if (!this.owned.has(it.id)) throw new Error("PUT equipment -> 409");
        this.equip.set(`${r1}|${it.slot}`, it.id);
        this.emit("item.equipped", { agent_id: r1, slot: it.slot, item_id: it.id, item: it }, r1);
        return { agent_id: r1, slot: it.slot, item_id: it.id };
      }
      if (method === "DELETE") {
        this.equip.delete(`${r1}|${r3}`);
        this.emit("item.unequipped", { agent_id: r1, slot: r3 }, r1);
        return {};
      }
    }
    if (r0 === "office" && r1 === "layout") {
      if (method === "GET") return { layout: this.layout, version: this.layoutVersion };
      if (method === "PUT") {
        if (Number(body?.base_version) !== this.layoutVersion) throw new Error("PUT /office/layout -> 409");
        this.layout = normalizeLayoutDoc(body?.layout);
        this.layoutVersion += 1;
        try { this.store?.setItem(LAYOUT_KEY, JSON.stringify({ layout: this.layout, version: this.layoutVersion })); } catch { /* quota / private mode */ }
        this.emit("office.layout_updated", { version: this.layoutVersion, actor: "user" });
        return { layout: this.layout, version: this.layoutVersion };
      }
    }
    if (r0 === "office" && r1 === "items") {
      if (method === "PUT") {
        const it = this.item(String(body?.item_id));
        if (!it || it.kind !== "office" || it.slot !== r2) throw new Error("PUT office -> 400");
        if (!this.owned.has(it.id)) throw new Error("PUT office -> 409");
        this.office.set(it.slot, it.id);
        this.emit("office.item_placed", { slot: it.slot, item_id: it.id, item: it });
        return { slot: it.slot, item_id: it.id };
      }
      if (method === "DELETE") {
        this.office.delete(r2);
        this.emit("office.item_removed", { slot: r2 });
        return {};
      }
    }
    throw new Error(`mock: ${method} /${seg.join("/")} not implemented`);
  }
}
