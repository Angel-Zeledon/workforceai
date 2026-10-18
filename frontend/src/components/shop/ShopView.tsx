"use client";
import { useEffect, useMemo, useState } from "react";
import { useT, fmtNumber, fmtDateTime } from "@/lib/i18n";
import {
  CHARACTER_CATEGORIES, OFFICE_CATEGORIES, itemDesc, itemName, useRewards, type LedgerEntry, type ShopItem,
} from "@/lib/rewards";
import { useStore } from "@/lib/store";
import { Btn, Empty, useAgentName } from "../ui";
import { toast } from "@/lib/toast";
import { CoinIcon } from "./Coin";
import dynamic from "next/dynamic";
import { ItemThumb } from "./ItemThumb";
import { lighten, mix } from "../office/itemStyle";

const ThumbStudio = dynamic(() => import("./ThumbStudio").then((m) => m.ThumbStudio), { ssr: false });

/** The stage an item stands on: a soft spotlight in the item's own color, deeper for rarer items. */
function stageStyle(item: ShopItem): React.CSSProperties {
  const c = item.color || "#8f78c9";
  const edge = item.rarity === "epic" ? mix(c, "#7c4dff", 0.4) : item.rarity === "rare" ? mix(c, "#3b82f6", 0.3) : c;
  return { background: `radial-gradient(ellipse 85% 70% at 50% 34%, ${lighten(c, 0.9)} 0%, ${lighten(c, 0.68)} 48%, ${mix(edge, "#7d8aa6", 0.3)} 135%)` };
}
import { go } from "@/lib/navBridge";
import { useOfficeLayout } from "@/lib/officeLayout";

type Tab = "shop" | "mine" | "history";
type Group = "character" | "office";

/** Rarity shows as a quiet dot + word, and as the card's edge: epic items get a soft violet glow. */
const RARITY_DOT: Record<string, string> = { common: "bg-slate-400", rare: "bg-sky-500", epic: "bg-violet-500" };
const RARITY_CARD: Record<string, string> = {
  common: "border-line",
  rare: "border-sky-300/60",
  epic: "border-violet-400/60 shadow-[0_0_0_1px_rgba(139,92,246,.18),0_6px_22px_-8px_rgba(139,92,246,.45)]",
};

function Price({ n }: { n: number }) {
  return <span className="inline-flex items-center gap-1 font-mono text-xs font-semibold text-amber-800"><CoinIcon size={13} />{fmtNumber(n, 0)}</span>;
}

/** One catalog item. In the Shop it can only be bought (a purchase lands in the
 *  inventory, nothing is applied); in the Inventory the owner chooses who wears it
 *  or places it in the office. */
function ItemCard({ item, mode, onOpenInventory }: { item: ShopItem; mode: "shop" | "inventory"; onOpenInventory: () => void }) {
  const { t, locale } = useT();
  const name = useAgentName();
  const balance = useRewards((s) => s.wallet.balance);
  const owned = useRewards((s) => !!s.owned[item.id]);
  const equip = useRewards((s) => s.equip);
  const placed = useRewards((s) => s.office[item.slot] === item.id);
  const agents = useStore((s) => s.agents);
  const order = useStore((s) => s.agentOrder);
  const [agent, setAgent] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState(false);
  const wearers = order.filter((id) => equip[id]?.[item.slot] === item.id);
  const target = agent || order[0] || "";
  const affordable = balance >= item.price;

  const run = async (fn: () => Promise<void>, okMsg?: string) => {
    setBusy(true); setErr(false);
    try { await fn(); if (okMsg) toast(okMsg); } catch { setErr(true); void useRewards.getState().load(); } finally { setBusy(false); }
  };
  const rw = useRewards.getState;

  return (
    <article data-testid={`shop-item-${item.id}`} data-owned={owned} data-category={item.category} className={`group relative flex flex-col gap-2.5 rounded-2xl border bg-panel p-3 shadow-soft transition duration-150 hover:-translate-y-0.5 hover:shadow-pop ${RARITY_CARD[item.rarity] ?? RARITY_CARD.common}`}>
      <div className={`relative mx-auto flex w-full items-center justify-center overflow-hidden rounded-xl py-3 ring-1 ring-inset ring-black/5 ${item.rarity === "epic" ? "shop-sheen" : ""}`} style={stageStyle(item)}>
        <div className="transition-transform duration-300 group-hover:-translate-y-0.5 group-hover:scale-[1.06]"><ItemThumb item={item} size={148} /></div>
        {owned && <span className="absolute right-1.5 top-1.5 rounded-full bg-ok/90 px-2 py-0.5 text-[10px] font-semibold text-white shadow-soft">{t("shop.inInventory")}</span>}
      </div>
      <div className="min-w-0">
        <div className="flex items-start justify-between gap-2">
          <h3 className="text-[13.5px] font-semibold leading-tight text-ink">{itemName(item, locale)}</h3>
          <Price n={item.price} />
        </div>
        <span className="mt-1 inline-flex items-center gap-1.5 text-[11px] font-medium text-mute"><i className={`h-1.5 w-1.5 rounded-full ${RARITY_DOT[item.rarity] ?? RARITY_DOT.common}`} />{t(`shop.rarity.${item.rarity}`)}</span>
      </div>
      <p className="text-[11.5px] leading-snug text-mute">{itemDesc(item, locale)}</p>
      <div className="mt-auto flex flex-col gap-1.5">
        {item.kind === "character" && wearers.length > 0 && (
          <div data-testid={`shop-worn-${item.id}`} className="text-[11px] font-medium text-ok">{t("shop.wornBy", { names: wearers.map((id) => name(id).split(" ")[0]).join(", ") })}</div>
        )}
        {item.kind === "office" && placed && <div data-testid={`shop-placed-${item.id}`} className="text-[11px] font-medium text-ok">{t("shop.placed")}</div>}
        {!owned ? (
          <>
            <Btn kind="primary" data-testid={`shop-buy-${item.id}`} disabled={busy || !affordable} onClick={() => run(() => rw().buy(item.id), t("toast.bought"))}>
              {t("shop.buy")}
            </Btn>
            {!affordable && <div className="text-[10.5px] text-mute">{t("shop.missing", { count: item.price - balance })}</div>}
          </>
        ) : mode === "shop" ? (
          <div className="flex items-center justify-between gap-2">
            <span data-testid={`shop-in-inventory-${item.id}`} className="text-[11px] font-medium text-ok">{t("shop.inInventory")}</span>
            <Btn data-testid={`shop-goto-inventory-${item.id}`} onClick={onOpenInventory}>{t("shop.toInventory")}</Btn>
          </div>
        ) : item.kind === "character" ? (
          <div className="flex flex-wrap items-center gap-1.5">
            <select aria-label={t("shop.forAgent")} data-testid={`shop-agent-${item.id}`} value={target} onChange={(e) => setAgent(e.target.value)}
              className="min-h-10 min-w-0 flex-1 rounded-md border border-line bg-panel2 px-2 py-1.5 text-xs outline-none focus:border-accent">
              {order.map((id) => <option key={id} value={id}>{name(id)}</option>)}
            </select>
            {target && equip[target]?.[item.slot] === item.id ? (
              <Btn data-testid={`shop-unequip-${item.id}`} disabled={busy} onClick={() => run(() => rw().unequip(target, item.slot), t("toast.unequipped"))}>{t("shop.unequip")}</Btn>
            ) : (
              <Btn kind="ok" data-testid={`shop-equip-${item.id}`} disabled={busy || !agents[target]} onClick={() => run(() => rw().equipItem(target, item.id), t("toast.equipped"))}>{t("shop.equip")}</Btn>
            )}
          </div>
        ) : item.slot === "piece" || item.slot === "desk" || item.slot === "chair" ? (
          // furniture of the office editor: it is placed and styled there, not in a fixed slot
          <Btn kind="ok" data-testid={`shop-edit-${item.id}`} onClick={() => {
            const ed = useOfficeLayout.getState();
            go("/");
            ed.startEdit();
            if (item.slot === "piece") ed.startPlacing(item.id); else ed.setTab("desks");
          }}>{t("shop.placeEditor")}</Btn>
        ) : placed ? (
          <Btn data-testid={`shop-remove-${item.id}`} disabled={busy} onClick={() => run(() => rw().remove(item.slot))}>{t("shop.remove")}</Btn>
        ) : (
          <Btn kind="ok" data-testid={`shop-place-${item.id}`} disabled={busy} onClick={() => run(() => rw().place(item.id))}>{t("shop.place")}</Btn>
        )}
        {err && <div role="alert" className="text-[10.5px] font-medium text-err">{t("shop.error")}</div>}
      </div>
    </article>
  );
}

function reasonText(t: (k: string, v?: Record<string, string | number>) => string, e: LedgerEntry, itemLabel: (id: string) => string) {
  if (e.reason === "purchase") return t("shop.reason.purchase", { item: itemLabel(e.ref_id) });
  const key = `shop.reason.${e.reason}`;
  return t(key, { title: e.title || e.ref_id });
}

function History() {
  const { t, locale } = useT();
  const name = useAgentName();
  const ledger = useRewards((s) => s.ledger);
  const earnedBy = useRewards((s) => s.earnedBy);
  const catalog = useRewards((s) => s.catalog);
  useEffect(() => { void useRewards.getState().loadLedger(); }, []);
  const itemLabel = (id: string) => { const it = catalog.find((c) => c.id === id); return it ? itemName(it, locale) : id; };
  const top = Object.entries(earnedBy).sort((a, b) => b[1] - a[1]);
  return (
    <div data-testid="shop-history" className="flex flex-col gap-4">
      {top.length > 0 && (
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-[11px] font-semibold uppercase tracking-wide text-mute">{t("shop.history.byAgent")}</span>
          {top.map(([id, n]) => (
            <span key={id} className="inline-flex items-center gap-1.5 rounded-full border border-line bg-panel px-2.5 py-1 text-xs font-medium text-ink2">
              {name(id)}<Price n={n} />
            </span>
          ))}
        </div>
      )}
      {ledger.length === 0 ? <Empty>{t("shop.history.empty")}</Empty> : (
        <div className="overflow-x-auto rounded-xl border border-line bg-panel">
          <table className="w-full text-left text-xs">
            <thead className="bg-panel2 text-[10.5px] uppercase tracking-wide text-mute">
              <tr><th className="px-3 py-2 font-semibold">{t("shop.history.when")}</th><th className="px-3 py-2 font-semibold">{t("shop.history.what")}</th><th className="px-3 py-2 font-semibold">{t("shop.history.agent")}</th><th className="px-3 py-2 text-right font-semibold">{t("shop.history.amount")}</th><th className="px-3 py-2 text-right font-semibold">{t("shop.history.balance")}</th></tr>
            </thead>
            <tbody>
              {ledger.map((e) => (
                <tr key={e.id} data-testid="shop-ledger-row" data-kind={e.kind} className="border-t border-line">
                  <td className="whitespace-nowrap px-3 py-2 text-mute">{fmtDateTime(e.created_at)}</td>
                  <td className="px-3 py-2 text-ink">{reasonText(t, e, itemLabel)}</td>
                  <td className="px-3 py-2 text-ink2">{e.agent_id ? name(e.agent_id) : "—"}</td>
                  <td className={`px-3 py-2 text-right font-mono font-semibold tabular-nums ${e.kind === "earn" ? "text-ok" : "text-ink2"}`}>{e.kind === "earn" ? "+" : "−"}{fmtNumber(e.amount, 0)}</td>
                  <td className="px-3 py-2 text-right font-mono tabular-nums text-mute">{fmtNumber(e.balance_after, 0)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

const PAGE = 48; // cards rendered at once: the catalog has hundreds of items

/** "Shop" screen: spend the coins that finished work earned on cosmetic items for the characters and the office. */
export function ShopView() {
  const { t } = useT();
  const wallet = useRewards((s) => s.wallet);
  const catalog = useRewards((s) => s.catalog);
  const owned = useRewards((s) => s.owned);
  const [tab, setTab] = useState<Tab>("shop");
  const [group, setGroup] = useState<Group>("character");
  const [cat, setCat] = useState<string>("all");
  const [limit, setLimit] = useState(PAGE);
  useEffect(() => { void useRewards.getState().load(); }, []);
  useEffect(() => { setLimit(PAGE); }, [group, cat, tab]);

  const cats: readonly string[] = group === "character" ? CHARACTER_CATEGORIES : OFFICE_CATEGORIES;
  const items = useMemo(() => catalog
    .filter((i) => i.kind === group && (cat === "all" || i.category === cat) && (tab === "shop" || owned[i.id]))
    .sort((a, b) => a.price - b.price || a.id.localeCompare(b.id)), [catalog, group, cat, tab, owned]);

  return (
    <div data-testid="shop-view" className="flex h-full min-h-0 flex-col bg-bg">
      <ThumbStudio />
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-line bg-panel px-4 py-3 sm:px-5">
        <h1 className="font-display text-xl font-semibold text-ink">{t("shop.title")}</h1>
        <div data-tour="shop.wallet" data-testid="shop-wallet" data-balance={wallet.balance} className="flex items-center gap-2 rounded-full border border-amber-300 bg-amber-100/70 px-3 py-1">
          <CoinIcon size={18} />
          <b className="font-mono text-base font-semibold tabular-nums text-amber-800">{fmtNumber(wallet.balance, 0)}</b>
          <span className="text-[11px] text-amber-800">{t("shop.coins")}</span>
        </div>
        <div className="hidden text-[11px] text-mute md:block">
          {t("shop.earnedTotal")} <b className="font-mono text-ink2">{fmtNumber(wallet.earned_total, 0)}</b> · {t("shop.spentTotal")} <b className="font-mono text-ink2">{fmtNumber(wallet.spent_total, 0)}</b>
        </div>
        <div data-tour="shop.tabs" role="tablist" className="flex w-full rounded-lg border border-line bg-panel2 p-0.5 text-xs font-medium sm:ml-auto sm:w-auto">
          {(["shop", "mine", "history"] as const).map((k) => (
            <button key={k} type="button" role="tab" aria-selected={tab === k} data-testid={`shop-tab-${k}`} onClick={() => setTab(k)}
              className={`min-h-10 flex-1 rounded-md px-3 py-1.5 transition sm:flex-none ${tab === k ? "bg-panel text-ink shadow-pop ring-1 ring-line" : "text-mute hover:text-ink"}`}>{t(`shop.tab.${k}`)}</button>
          ))}
        </div>
      </div>
      <p data-tour="shop.rule" data-testid="shop-rule" className="border-b border-line bg-amber-100/40 px-4 py-1.5 sm:px-5 text-[11.5px] text-amber-800">{t("shop.rule")}</p>

      <div data-tour="shop.content" className="min-h-0 flex-1 overflow-y-auto px-4 py-4 pb-[max(1rem,env(safe-area-inset-bottom))] sm:px-5">
        {tab === "history" ? <History /> : (
          <div className="flex flex-col gap-4">
            <div className="flex flex-wrap items-center gap-2">
              <div data-tour="shop.groups" className="flex rounded-lg border border-line bg-panel p-0.5 text-xs font-medium">
                {(["character", "office"] as const).map((g) => (
                  <button key={g} type="button" data-testid={`shop-group-${g}`} aria-pressed={group === g} onClick={() => { setGroup(g); setCat("all"); }}
                    className={`min-h-10 rounded-md px-3 py-1.5 transition ${group === g ? "bg-accent-soft text-accent" : "text-mute hover:text-ink"}`}>{t(`shop.group.${g}`)}</button>
                ))}
              </div>
              <div data-tour="shop.categories" className="flex flex-wrap gap-1.5">
                {["all", ...cats].map((c) => (
                  <button key={c} type="button" data-testid={`shop-cat-${c}`} aria-pressed={cat === c} onClick={() => setCat(c)}
                    className={`min-h-10 rounded-full border px-3 py-1 text-[11.5px] font-medium transition ${cat === c ? "border-accent/40 bg-accent-soft text-accent" : "border-line bg-panel text-ink2 hover:bg-panel2"}`}>
                    {c === "all" ? t("shop.cat.all") : t(`shop.cat.${c}`)}
                  </button>
                ))}
              </div>
            </div>
            {items.length === 0 ? <Empty>{tab === "mine" ? t("shop.mineEmpty") : t("shop.empty")}</Empty> : (
              <div data-tour="shop.grid" data-testid="shop-grid" className="ac-stagger grid grid-cols-[repeat(auto-fill,minmax(min(210px,100%),1fr))] gap-3.5">
                {items.slice(0, limit).map((it) => <ItemCard key={it.id} item={it} mode={tab === "mine" ? "inventory" : "shop"} onOpenInventory={() => setTab("mine")} />)}
              </div>
            )}
            {items.length > limit && (
              <div className="mt-4 flex justify-center">
                <button type="button" data-testid="shop-more" onClick={() => setLimit((n) => n + PAGE)} className="min-h-10 rounded-lg border border-line bg-panel px-4 py-2 text-xs font-semibold text-ink hover:bg-panel2">
                  {t("shop.more", { n: items.length - limit })}
                </button>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
