"use client";
import { go } from "@/lib/navBridge";
import { create } from "zustand";
import { call } from "./api";
import type { WsFrame } from "./types";

// ---- contract (docs/architecture/08-api.md section 20) ----
export type ItemKind = "character" | "office";
export type Rarity = "common" | "rare" | "epic";
export interface ShopItem {
  id: string; kind: ItemKind; category: string; slot: string; price: number; rarity: Rarity;
  name: { es: string; en: string }; description: { es: string; en: string };
  color?: string; color2?: string;
}
export interface Wallet { balance: number; earned_total: number; spent_total: number }
export interface LedgerEntry {
  id: string; seq: number; kind: "earn" | "spend"; amount: number; reason: string; ref_kind: string; ref_id: string;
  title?: string; agent_id?: string; actor: string; detail?: Record<string, unknown>; balance_after: number; created_at: string;
}
export interface Catalog { version: number; items: ShopItem[]; character_slots: string[]; office_slots: string[] }
interface ShopStateDoc {
  wallet: Wallet; earned_by_agent: Record<string, number>;
  inventory: { item_id: string }[]; equipment: { agent_id: string; slot: string; item_id: string }[]; office: { slot: string; item_id: string }[];
}
export interface CoinFloat { id: string; agent_id: string; amount: number; at: number }

/** Why a purchase or equip request was refused (the stable code of the 409 body). */
export class ShopError extends Error {
  constructor(public code: string) { super(code); }
}

export const SHOP_FRAMES = ["coins.earned", "shop.purchased", "item.equipped", "item.unequipped", "office.item_placed", "office.item_removed"];
export const isRewardsFrame = (type: string) => SHOP_FRAMES.includes(type);

const EMPTY_WALLET: Wallet = { balance: 0, earned_total: 0, spent_total: 0 };
const FLOAT_MS = 2600;
const LEDGER_MAX = 200;

interface RewardsState {
  loaded: boolean;
  available: boolean;
  wallet: Wallet;
  earnedBy: Record<string, number>;
  catalog: ShopItem[];
  owned: Record<string, true>;
  /** agent id -> slot -> item id */
  equip: Record<string, Record<string, string>>;
  /** office slot -> item id */
  office: Record<string, string>;
  ledger: LedgerEntry[];
  floats: CoinFloat[];
  /** bumps whenever coins are earned, so the counter can pulse */
  earnTick: number;
  load: () => Promise<void>;
  loadLedger: () => Promise<void>;
  applyFrame: (f: WsFrame) => void;
  buy: (itemId: string) => Promise<void>;
  equipItem: (agentId: string, itemId: string) => Promise<void>;
  unequip: (agentId: string, slot: string) => Promise<void>;
  place: (itemId: string) => Promise<void>;
  remove: (slot: string) => Promise<void>;
}

function groupEquip(rows: { agent_id: string; slot: string; item_id: string }[]) {
  const out: Record<string, Record<string, string>> = {};
  for (const r of rows) (out[r.agent_id] ||= {})[r.slot] = r.item_id;
  return out;
}

async function guarded<T>(p: Promise<T>): Promise<T> {
  try { return await p; } catch (e) {
    const m = /->\s*(\d+)/.exec(String((e as Error)?.message));
    throw new ShopError(m ? `http_${m[1]}` : "network");
  }
}

export const useRewards = create<RewardsState>((set, get) => ({
  loaded: false, available: true, wallet: EMPTY_WALLET, earnedBy: {}, catalog: [], owned: {}, equip: {}, office: {}, ledger: [], floats: [], earnTick: 0,

  load: async () => {
    try {
      const [cat, st] = await Promise.all([call<Catalog>("GET", "/shop/catalog"), call<ShopStateDoc>("GET", "/shop/state")]);
      if (!cat || !Array.isArray(cat.items) || !st) return;
      set({
        loaded: true, available: true, catalog: cat.items, wallet: st.wallet, earnedBy: st.earned_by_agent || {},
        owned: Object.fromEntries((st.inventory || []).map((o) => [o.item_id, true as const])),
        equip: groupEquip(st.equipment || []), office: Object.fromEntries((st.office || []).map((o) => [o.slot, o.item_id])),
      });
    } catch {
      set({ loaded: true, available: false }); // a backend without coins: the UI hides itself
    }
  },
  loadLedger: async () => {
    try {
      const rows = await call<LedgerEntry[]>("GET", `/rewards/ledger?limit=${LEDGER_MAX}`);
      if (Array.isArray(rows)) set({ ledger: rows });
    } catch { /* ignore */ }
  },

  applyFrame: (f) => {
    const p = f.payload || {};
    switch (f.type) {
      case "coins.earned": {
        const e = p.entry as LedgerEntry | undefined;
        const agent = String(p.agent_id ?? f.agent_id ?? "");
        const amount = Number(p.amount) || 0;
        set((s) => {
          const seen = e && s.ledger.some((x) => x.id === e.id);
          const float: CoinFloat | null = agent && amount > 0 && !seen ? { id: e?.id || f.id, agent_id: agent, amount, at: Date.now() } : null;
          return {
            wallet: p.wallet || s.wallet,
            earnedBy: agent && !seen ? { ...s.earnedBy, [agent]: (s.earnedBy[agent] || 0) + amount } : s.earnedBy,
            ledger: e && !seen ? [e, ...s.ledger].slice(0, LEDGER_MAX) : s.ledger,
            floats: float ? [...s.floats, float] : s.floats,
            earnTick: s.earnTick + (seen ? 0 : 1),
          };
        });
        const id = String(e?.id || f.id);
        setTimeout(() => set((s) => (s.floats.some((x) => x.id === id) ? { floats: s.floats.filter((x) => x.id !== id) } : {})), FLOAT_MS + 200);
        break;
      }
      case "shop.purchased": {
        const it = p.item as ShopItem | undefined;
        const e = p.entry as LedgerEntry | undefined;
        set((s) => ({
          wallet: p.wallet || s.wallet,
          owned: it ? { ...s.owned, [it.id]: true } : s.owned,
          ledger: e && !s.ledger.some((x) => x.id === e.id) ? [e, ...s.ledger].slice(0, LEDGER_MAX) : s.ledger,
        }));
        break;
      }
      case "item.equipped": {
        const a = String(p.agent_id ?? f.agent_id ?? ""), slot = String(p.slot ?? ""), item = String(p.item_id ?? "");
        if (a && slot && item) set((s) => ({ equip: { ...s.equip, [a]: { ...(s.equip[a] || {}), [slot]: item } } }));
        break;
      }
      case "item.unequipped": {
        const a = String(p.agent_id ?? f.agent_id ?? ""), slot = String(p.slot ?? "");
        if (a && slot) set((s) => {
          const { [slot]: _gone, ...rest } = s.equip[a] || {};
          return { equip: { ...s.equip, [a]: rest } };
        });
        break;
      }
      case "office.item_placed": {
        const slot = String(p.slot ?? ""), item = String(p.item_id ?? "");
        if (slot && item) set((s) => ({ office: { ...s.office, [slot]: item } }));
        break;
      }
      case "office.item_removed": {
        const slot = String(p.slot ?? "");
        if (slot) set((s) => { const { [slot]: _gone, ...rest } = s.office; return { office: rest }; });
        break;
      }
    }
  },

  buy: async (itemId) => {
    try {
      const r = await call<{ item: ShopItem; entry: LedgerEntry; wallet: Wallet }>("POST", "/shop/purchases", { item_id: itemId, idempotency_key: `ui-${itemId}-${Date.now().toString(36)}` });
      get().applyFrame({ id: r.entry.id, type: "shop.purchased", ts: r.entry.created_at, org_id: "", payload: r });
    } catch (e) {
      const m = String((e as Error)?.message);
      throw new ShopError(/-> 409/.test(m) ? "conflict" : "network");
    }
  },
  equipItem: async (agentId, itemId) => {
    const it = get().catalog.find((x) => x.id === itemId);
    if (!it) return;
    await guarded(call("PUT", `/agents/${agentId}/equipment/${it.slot}`, { item_id: itemId }));
    get().applyFrame({ id: "local", type: "item.equipped", ts: "", org_id: "", payload: { agent_id: agentId, slot: it.slot, item_id: itemId } });
  },
  unequip: async (agentId, slot) => {
    await guarded(call("DELETE", `/agents/${agentId}/equipment/${slot}`));
    get().applyFrame({ id: "local", type: "item.unequipped", ts: "", org_id: "", payload: { agent_id: agentId, slot } });
  },
  place: async (itemId) => {
    const it = get().catalog.find((x) => x.id === itemId);
    if (!it) return;
    await guarded(call("PUT", `/office/items/${it.slot}`, { item_id: itemId }));
    get().applyFrame({ id: "local", type: "office.item_placed", ts: "", org_id: "", payload: { slot: it.slot, item_id: itemId } });
  },
  remove: async (slot) => {
    await guarded(call("DELETE", `/office/items/${slot}`));
    get().applyFrame({ id: "local", type: "office.item_removed", ts: "", org_id: "", payload: { slot } });
  },
}));

/** Name / description of an item in the given locale (the catalog carries es and en). */
export const itemName = (it: ShopItem, locale: string) => (locale === "en" ? it.name.en : it.name.es);
export const itemDesc = (it: ShopItem, locale: string) => (locale === "en" ? it.description.en : it.description.es);

/** Navigation of the Shop screen (full screen like Files and Projects). */
export const useShopNav = create<{ open: boolean; show: () => void; close: () => void; toggle: () => void }>((set) => ({
  open: false, show: () => { set({ open: true }); go("/shop"); }, close: () => set({ open: false }), toggle: () => go(useShopNav.getState().open ? "/" : "/shop"),
}));

/** Category order of the Shop screen and its i18n label keys (listed explicitly in es.json/en.json). */
export const CHARACTER_CATEGORIES = ["outfit", "hat", "glasses", "headphones", "scarf", "badge", "buddy"] as const;
export const OFFICE_CATEGORIES = ["plant", "lamp", "rug", "coffee", "poster", "shelf", "aquarium", "arcade", "trophy", "window"] as const;
