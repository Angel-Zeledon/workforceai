"use client";
// The "Puestos" tab: one window per person; open it to choose the desk and the chair from little pictures drawn in the
// chosen office style.
import { useT } from "@/lib/i18n";
import { go } from "@/lib/navBridge";
import { useStore } from "@/lib/store";
import { itemName, useRewards, type ShopItem } from "@/lib/rewards";
import { useOfficeLayout } from "@/lib/officeLayout";
import type { Layout } from "@/lib/officeLayoutModel";
import { roleMeta } from "@/lib/meta";
import { Btn, useAgentName } from "../../ui";
import { Icon } from "../../icons";
import { ItemThumb } from "../../shop/ItemThumb";
import { EmptyArt, FOCUS, Tile } from "./parts";

/** The pieces to pick from (the original first), as little windows. */
/** The original desk and chair of the office, as stand-in pieces so the "Clásico" window shows a picture too. */
const ORIGINAL: Record<"desk" | "chair", ShopItem> = {
  desk: { id: "desk_original", kind: "office", category: "desk", slot: "desk", price: 0, rarity: "common", name: { es: "Escritorio", en: "Desk" }, description: { es: "", en: "" }, color: "#d9c4a3" },
  chair: { id: "chair_original", kind: "office", category: "chair", slot: "chair", price: 0, rarity: "common", name: { es: "Silla", en: "Chair" }, description: { es: "", en: "" }, color: "#59616d" },
};

function Picker({ items, value, none, onPick, testid, kind }: { items: ShopItem[]; value: string | undefined; none: string; onPick: (id: string | undefined) => void; testid: string; kind: "desk" | "chair" }) {
  const { locale } = useT();
  return (
    <div className="grid grid-cols-3 gap-2" data-testid={testid}>
      <Tile selected={!value} name={none} onClick={() => onPick(undefined)}>
        <ItemThumb item={ORIGINAL[kind]} size={64} />
      </Tile>
      {items.map((it) => (
        <Tile key={it.id} testid={`oe-pick-${it.id}`} selected={value === it.id} name={itemName(it, locale)} onClick={() => onPick(it.id)}>
          <ItemThumb item={it} size={64} />
        </Tile>
      ))}
    </div>
  );
}

export function DesksTab() {
  const { t } = useT();
  const name = useAgentName();
  const layout = useOfficeLayout((s) => s.draft) as Layout;
  const sel = useOfficeLayout((s) => s.selection);
  const mutate = useOfficeLayout((s) => s.mutate);
  const order = useStore((s) => s.agentOrder);
  const agents = useStore((s) => s.agents);
  const catalog = useRewards((s) => s.catalog);
  const owned = useRewards((s) => s.owned);
  const desks = catalog.filter((i) => i.category === "desk" && owned[i.id]);
  const chairs = catalog.filter((i) => i.category === "chair" && owned[i.id]);
  const byId = (id?: string) => (id ? catalog.find((i) => i.id === id) : undefined);
  const active = sel?.kind === "desk" ? sel.id : null;
  const setCfg = (id: string, patch: { desk?: string; chair?: string; x?: number; z?: number }, drop?: ("desk" | "chair" | "pos")[]) =>
    mutate((l) => {
      const cur = { ...(l.desks[id] || {}), ...patch };
      for (const d of drop || []) { if (d === "pos") { delete cur.x; delete cur.z; } else delete cur[d]; }
      const desks2 = { ...l.desks };
      if (Object.keys(cur).length) desks2[id] = cur; else delete desks2[id];
      return { ...l, desks: desks2 };
    });
  const all = (kind: "desk" | "chair", id: string | undefined) =>
    mutate((l) => {
      const out = { ...l.desks };
      for (const a of order) { const cur = { ...(out[a] || {}) }; if (id) cur[kind] = id; else delete cur[kind]; if (Object.keys(cur).length) out[a] = cur; else delete out[a]; }
      return { ...l, desks: out };
    });
  const toShop = () => { useOfficeLayout.getState().cancel(); go("/shop"); };
  // what could be bought, shown (never bought) as the picture of the empty state
  const teaser = catalog.filter((i) => (i.category === "desk" || i.category === "chair") && !owned[i.id]).slice(0, 3);
  return (
    <div className="space-y-4">
      <p className="text-[12px] leading-snug text-mute">{t("oe.d.hint")}</p>
      {desks.length + chairs.length === 0 && (
        <EmptyArt title={t("oe.d.emptyTitle")} body={t("oe.d.none")} action={t("oe.f.shop")} onAction={toShop}
          art={<div className="flex h-full items-center justify-center gap-0.5 opacity-80">{teaser.map((it) => <ItemThumb key={it.id} item={it} size={48} />)}</div>} />
      )}
      <ul className="space-y-2">
        {order.map((id) => {
          const a = agents[id];
          const cfg = layout.desks[id] || {};
          const open = active === id;
          const color = roleMeta(a?.role).color;
          const dk = byId(cfg.desk), ch = byId(cfg.chair);
          return (
            <li key={id} className={`rounded-[10px] border p-2 ${open ? "border-accent bg-accent-soft/30" : "border-line bg-panel"}`}>
              <button type="button" data-testid={`oe-desk-${id}`} aria-expanded={open} onClick={() => useOfficeLayout.getState().select(open ? null : { kind: "desk", id })} className={`flex min-h-10 w-full items-center gap-2 rounded-md text-left ${FOCUS}`}>
                <i className="h-2.5 w-2.5 shrink-0 rounded-full" style={{ background: color }} />
                <span className="min-w-0 flex-1 truncate text-[13px] font-semibold text-ink">{name(id)}</span>
                {!open && (dk || ch) && <span className="flex shrink-0 items-center" aria-hidden>{dk && <ItemThumb item={dk} size={32} />}{ch && <ItemThumb item={ch} size={32} />}</span>}
                {cfg.x !== undefined && <span className="text-[11px] text-mute">{t("oe.d.moved")}</span>}
                <Icon name="chevron" size={14} className={`shrink-0 text-mute transition-transform ${open ? "rotate-90" : ""}`} />
              </button>
              {open && (
                <div className="mt-3 space-y-4">
                  {desks.length > 0 && (
                    <div className="space-y-2">
                      <div className="flex items-center justify-between text-[12px] text-mute"><span className="font-medium text-ink2">{t("oe.d.desk")}</span><button type="button" onClick={() => all("desk", cfg.desk)} className={`inline-flex min-h-10 items-center rounded px-1 font-medium text-accent hover:underline ${FOCUS}`}>{t("oe.d.all")}</button></div>
                      <Picker kind="desk" testid={`oe-desks-${id}`} items={desks} value={cfg.desk} none={t("oe.d.default")} onPick={(v) => (v ? setCfg(id, { desk: v }) : setCfg(id, {}, ["desk"]))} />
                    </div>
                  )}
                  {chairs.length > 0 && (
                    <div className="space-y-2">
                      <div className="flex items-center justify-between text-[12px] text-mute"><span className="font-medium text-ink2">{t("oe.d.chair")}</span><button type="button" onClick={() => all("chair", cfg.chair)} className={`inline-flex min-h-10 items-center rounded px-1 font-medium text-accent hover:underline ${FOCUS}`}>{t("oe.d.all")}</button></div>
                      <Picker kind="chair" testid={`oe-chairs-${id}`} items={chairs} value={cfg.chair} none={t("oe.d.default")} onPick={(v) => (v ? setCfg(id, { chair: v }) : setCfg(id, {}, ["chair"]))} />
                    </div>
                  )}
                  {cfg.x !== undefined && <Btn onClick={() => setCfg(id, {}, ["pos"])}>{t("oe.d.reset")}</Btn>}
                </div>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
