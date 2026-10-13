"use client";
import { useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import { go } from "@/lib/navBridge";
import { useStore } from "@/lib/store";
import { useCan } from "@/lib/session";
import { itemName, useRewards, type ShopItem } from "@/lib/rewards";
import { effective, pieceOf, useOfficeLayout, withPiece, withoutPiece } from "@/lib/officeLayout";
import { DEFAULT_LAYOUT, FIXTURES, FIXTURE_IDS, FLOOR_KINDS, FLOOR_TINTS, MOODS, TRIM_COLORS, WALL_COLORS, ZONE_NAMES, isHex, type FixtureId, type FloorKind, type Layout, type Mood, type ZoneName } from "@/lib/officeLayoutModel";
import { Btn, useAgentName } from "../ui";
import { Icon } from "../icons";
import { ErrorLine } from "../connections/shared";
import dynamic from "next/dynamic";
import { ItemThumb } from "../shop/ItemThumb";
import { ItemPreview } from "../shop/ItemPreview";

const ThumbStudio = dynamic(() => import("../shop/ThumbStudio").then((m) => m.ThumbStudio), { ssr: false });
import { pieceInfo } from "./pieceInfo";
import { roleMeta } from "@/lib/meta";

/** Entry point shown in the office HUD: only people who can spend coins can redecorate. */
export function EditOfficeButton() {
  const { t } = useT();
  const editing = useOfficeLayout((s) => s.editing);
  const can = useCan("shop:buy");
  if (editing || !can) return null;
  return (
    <button type="button" data-testid="office-edit" onClick={() => useOfficeLayout.getState().startEdit()}
      className="pointer-events-auto flex items-center gap-2 rounded-lg border border-line bg-panel/95 px-3 py-2 text-xs font-semibold text-ink shadow-soft backdrop-blur transition hover:bg-panel2">
      <Icon name="edit" size={15} />{t("oe.edit")}
    </button>
  );
}

const NON_PIECE = new Set(["window", "desk", "chair"]);
const CAT_ORDER = ["plant", "lamp", "light", "rug", "sofa", "table", "cabinet", "divider", "whiteboard", "appliance", "coffee", "poster", "shelf", "aquarium", "arcade", "trophy"];

function Swatches({ colors, value, onPick, label }: { colors: string[]; value: string; onPick: (c: string) => void; label: (c: string) => string }) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {colors.map((c) => (
        <button key={c} type="button" aria-label={label(c)} aria-pressed={value.toLowerCase() === c.toLowerCase()} onClick={() => onPick(c)}
          className={`h-7 w-7 rounded-full border transition hover:scale-110 ${value.toLowerCase() === c.toLowerCase() ? "border-accent ring-2 ring-accent/40" : "border-line-strong"}`} style={{ background: c }} />
      ))}
    </div>
  );
}

function ColorField({ value, onPick, label }: { value: string; onPick: (c: string) => void; label: string }) {
  const [text, setText] = useState("");
  return (
    <div className="mt-2 flex items-center gap-2 text-[11.5px] text-mute">
      <label className="relative flex h-7 w-7 cursor-pointer items-center justify-center overflow-hidden rounded-full border border-line-strong" style={{ background: value }} title={label}>
        <input type="color" value={isHex(value) && value.length === 7 ? value : "#ffffff"} onChange={(e) => onPick(e.target.value)} className="absolute inset-0 h-full w-full cursor-pointer opacity-0" aria-label={label} />
      </label>
      <input value={text} onChange={(e) => { setText(e.target.value); if (isHex(e.target.value)) onPick(e.target.value); }} placeholder={value}
        className="w-24 rounded-md border border-line bg-panel2 px-2 py-1 font-mono text-[11px] text-ink outline-none focus:border-accent" aria-label={label} />
      <span>{label}</span>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return <section className="space-y-2"><h3 className="text-[12px] font-semibold text-ink">{title}</h3>{children}</section>;
}

function Range({ label, value, min, max, step, onChange, left, right, testid }: { label: string; value: number; min: number; max: number; step: number; onChange: (v: number) => void; left?: string; right?: string; testid?: string }) {
  return (
    <label className="block text-[11.5px] text-mute">
      <span className="mb-1 flex justify-between"><span>{label}</span><span className="font-mono text-ink2">{value.toFixed(2)}</span></span>
      <input data-testid={testid} type="range" min={min} max={max} step={step} value={value} onChange={(e) => onChange(Number(e.target.value))} className="w-full accent-accent" />
      {(left || right) && <span className="flex justify-between text-[10.5px]"><span>{left}</span><span>{right}</span></span>}
    </label>
  );
}

// ------------------------------------------------------------------ ambience

const FLOOR_SWATCH: Record<FloorKind, string> = {
  concrete: "linear-gradient(135deg,#e7eaee,#cfd5dc)",
  wood: "repeating-linear-gradient(0deg,#c99a63 0 6px,#b98a55 6px 12px)",
  parquet: "repeating-linear-gradient(45deg,#c99a63 0 5px,#a97a48 5px 10px)",
  carpet: "radial-gradient(#c3c8cf 1px,#aeb4bc 1.5px) 0 0/4px 4px",
  tile: "linear-gradient(#e9edf1,#e9edf1) padding-box, repeating-linear-gradient(0deg,#9aa6b4 0 1px,transparent 1px 14px), repeating-linear-gradient(90deg,#9aa6b4 0 1px,transparent 1px 14px)",
  marble: "linear-gradient(115deg,#f1f2f5 40%,#c8ced8 41% 42%,#f1f2f5 43% 70%,#b6bdc9 71% 72%,#f1f2f5 73%)",
};
/** The temperature each preset starts at, so the bar shows where it sits (warm to the left, cool to the right). */
const MOOD_TEMP: Record<Mood, number> = { natural: 0, warm: -0.35, cool: 0.35, sunset: -0.25, night: 0 };
const MOOD_ICON: Record<Mood, "sun" | "moon" | "contrast" | "flag" | "pulse"> = { natural: "sun", warm: "flag", cool: "pulse", sunset: "contrast", night: "moon" };

/** Light mood presets plus two bars: temperature (warm to cool) and brightness. Edits the draft in the editor, saves right away outside it. */
export function LightControls() {
  const { t } = useT();
  const light = useOfficeLayout((s) => effective(s).light);
  const setLight = useOfficeLayout((s) => s.setLight);
  return (
    <div className="space-y-3" data-testid="light-controls">
      <div className="grid grid-cols-5 gap-1.5">
        {MOODS.map((m) => (
          <button key={m} type="button" data-testid={`oe-mood-${m}`} aria-pressed={light.mood === m} onClick={() => setLight({ mood: m, temp: MOOD_TEMP[m] })}
            className={`flex flex-col items-center gap-1 rounded-lg border px-1 py-2 text-[10.5px] font-medium transition ${light.mood === m ? "border-accent bg-accent-soft text-accent" : "border-line text-ink2 hover:bg-panel2"}`}>
            <Icon name={MOOD_ICON[m]} size={16} />{t(`oe.mood.${m}`)}
          </button>
        ))}
      </div>
      <Range testid="oe-temp" label={t("oe.a.temp")} value={light.temp} min={-1} max={1} step={0.05} left={t("oe.a.warm")} right={t("oe.a.cool")} onChange={(v) => setLight({ temp: v })} />
      <Range testid="oe-brightness" label={t("oe.a.brightness")} value={light.brightness} min={0.5} max={1.5} step={0.05} left={t("oe.light.dim")} right={t("oe.light.bright")} onChange={(v) => setLight({ brightness: v })} />
    </div>
  );
}

/** HUD button of the office: the room's light, one click away (replaces the old ambient / lights on / night switch). */
export function LightButton() {
  const { t } = useT();
  const can = useCan("shop:buy");
  const [open, setOpen] = useState(false);
  if (!can) return null;
  return (
    <div className="pointer-events-auto relative">
      <button type="button" data-testid="office-light" aria-expanded={open} onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-2 rounded-lg border border-line bg-panel/95 px-3 py-2 text-xs font-semibold text-ink shadow-soft backdrop-blur transition hover:bg-panel2">
        <Icon name="sun" size={15} />{t("oe.light.title")}
      </button>
      {open && (
        <div className="absolute right-0 top-full z-50 mt-2 w-[300px] rounded-xl border border-line bg-panel p-4 shadow-pop">
          <div className="mb-3 flex items-center justify-between"><h3 className="text-[13px] font-semibold text-ink">{t("oe.a.light")}</h3>
            <button type="button" onClick={() => setOpen(false)} aria-label={t("oe.cancel")} className="rounded-md p-1 text-mute hover:bg-panel2 hover:text-ink"><Icon name="x" size={14} /></button></div>
          <LightControls />
        </div>
      )}
    </div>
  );
}

function AmbienceTab() {
  const { t, locale } = useT();
  const layout = useOfficeLayout((s) => s.draft) as Layout;
  const mutate = useOfficeLayout((s) => s.mutate);
  const catalog = useRewards((s) => s.catalog);
  const owned = useRewards((s) => s.owned);
  const office = useRewards((s) => s.office);
  const views = catalog.filter((i) => i.kind === "office" && i.category === "window" && owned[i.id]);
  const set = (fn: (l: Layout) => Layout, key?: string) => mutate(fn, key);
  return (
    <div className="space-y-5">
      <Section title={t("oe.a.walls")}>
        <Swatches colors={WALL_COLORS} value={layout.walls.color} label={(c) => t("oe.a.swatch", { c })} onPick={(c) => set((l) => ({ ...l, walls: { ...l.walls, color: c } }), "wall")} />
        <ColorField value={layout.walls.color} label={t("oe.a.custom")} onPick={(c) => set((l) => ({ ...l, walls: { ...l.walls, color: c } }), "wall")} />
        <button type="button" data-testid="oe-windows" role="switch" aria-checked={layout.walls.windows} onClick={() => set((l) => ({ ...l, walls: { ...l.walls, windows: !l.walls.windows } }))}
          className="flex w-full items-center justify-between rounded-lg border border-line px-3 py-2 text-[12px] font-medium text-ink2 transition hover:bg-panel2">
          <span>{t("oe.a.windows")}</span>
          <span className={`flex h-5 w-9 items-center rounded-full px-0.5 transition ${layout.walls.windows ? "bg-accent" : "bg-line-strong"}`}><i className={`h-4 w-4 rounded-full bg-white shadow transition-transform ${layout.walls.windows ? "translate-x-4" : ""}`} /></span>
        </button>
        <div className="pt-1 text-[11.5px] text-mute">{t("oe.a.trim")}</div>
        <Swatches colors={TRIM_COLORS} value={layout.walls.trim} label={(c) => t("oe.a.swatch", { c })} onPick={(c) => set((l) => ({ ...l, walls: { ...l.walls, trim: c } }), "trim")} />
      </Section>

      <Section title={t("oe.a.floor")}>
        <div className="grid grid-cols-3 gap-2">
          {FLOOR_KINDS.map((k) => (
            <button key={k} type="button" data-testid={`oe-floor-${k}`} aria-pressed={layout.floor.kind === k} onClick={() => set((l) => ({ ...l, floor: { ...l.floor, kind: k } }))}
              className={`overflow-hidden rounded-lg border text-left text-[11.5px] transition ${layout.floor.kind === k ? "border-accent ring-2 ring-accent/30" : "border-line hover:border-line-strong"}`}>
              <span className="block h-9 w-full" style={{ background: FLOOR_SWATCH[k], backgroundColor: "#d9dde3" }} />
              <span className="block px-2 py-1 font-medium text-ink">{t(`oe.floor.${k}`)}</span>
            </button>
          ))}
        </div>
        <div className="text-[11.5px] text-mute">{t("oe.a.tint")}</div>
        <Swatches colors={FLOOR_TINTS} value={layout.floor.tint} label={(c) => t("oe.a.swatch", { c })} onPick={(c) => set((l) => ({ ...l, floor: { ...l.floor, tint: c } }), "tint")} />
      </Section>

      <Section title={t("oe.a.light")}><LightControls /></Section>

      {views.length > 0 && (
        <Section title={t("oe.a.view")}>
          <div className="flex flex-wrap gap-2">
            <button type="button" onClick={() => void useRewards.getState().remove("window_1")} aria-pressed={!office.window_1}
              className={`rounded-lg border px-2.5 py-1.5 text-[11.5px] font-medium ${!office.window_1 ? "border-accent bg-accent-soft text-accent" : "border-line text-ink2 hover:bg-panel2"}`}>{t("oe.a.noView")}</button>
            {views.map((v) => (
              <button key={v.id} type="button" title={itemName(v, locale)} onClick={() => void useRewards.getState().place(v.id)} aria-pressed={office.window_1 === v.id}
                className={`rounded-lg border p-1 ${office.window_1 === v.id ? "border-accent ring-2 ring-accent/30" : "border-line hover:bg-panel2"}`}><ItemPreview item={v} size={44} /></button>
            ))}
          </div>
        </Section>
      )}

      <Btn onClick={() => set((l) => ({ ...l, walls: DEFAULT_LAYOUT.walls, floor: DEFAULT_LAYOUT.floor, light: DEFAULT_LAYOUT.light }))}>{t("oe.a.reset")}</Btn>
    </div>
  );
}

// ------------------------------------------------------------------ furniture

function SelectedPiece() {
  const { t, locale } = useT();
  const layout = useOfficeLayout((s) => s.draft) as Layout;
  const sel = useOfficeLayout((s) => s.selection);
  const catalog = useRewards((s) => s.catalog);
  const mutate = useOfficeLayout((s) => s.mutate);
  if (sel?.kind !== "piece") return null;
  const p = pieceOf(layout, sel.uid);
  const item = p && catalog.find((i) => i.id === p.item);
  if (!p || !item) return null;
  const info = pieceInfo(item);
  return (
    <div data-testid="oe-selected" className="space-y-3 rounded-xl border border-accent/40 bg-accent-soft/40 p-3">
      <div className="flex items-center gap-3">
        <ItemThumb item={item} size={56} />
        <div className="min-w-0 flex-1"><div className="truncate text-[13px] font-semibold text-ink">{itemName(item, locale)}</div><div className="text-[11px] text-mute">{t("oe.f.selected")}</div></div>
      </div>
      <Range label={t("oe.f.scale")} value={p.s} min={0.6} max={1.6} step={0.05} onChange={(v) => mutate((l) => withPiece(l, p.uid, { s: v }), `s:${p.uid}`)} />
      {info.mount !== "floor" && <Range label={t("oe.f.height")} value={p.y ?? info.y} min={0.2} max={info.mount === "ceiling" ? 2.9 : 2.6} step={0.05} onChange={(v) => mutate((l) => withPiece(l, p.uid, { y: v }), `y:${p.uid}`)} />}
      <div className="flex gap-2">
        <Btn onClick={() => mutate((l) => withPiece(l, p.uid, { rot: p.rot + Math.PI / 2 }))} data-testid="oe-rotate">{t("oe.f.rotate")} (R)</Btn>
        <Btn kind="danger" onClick={() => { mutate((l) => withoutPiece(l, p.uid)); useOfficeLayout.getState().select(null); }} data-testid="oe-remove">{t("oe.f.remove")}</Btn>
      </div>
    </div>
  );
}

const zoneName = (t: (k: string) => string, z: ZoneName) => t(`oe.zone.${z}`);
const fixtureName = (t: (k: string) => string, id: FixtureId) => t(id === "rack" ? "oe.fx.rack" : "oe.fx.plant");

/** Rooms and the original plants / rack: selected here or in the 3D view, dragged to move. */
function RoomsSection() {
  const { t } = useT();
  const layout = useOfficeLayout((s) => s.draft) as Layout;
  const sel = useOfficeLayout((s) => s.selection);
  const mutate = useOfficeLayout((s) => s.mutate);
  const select = useOfficeLayout((s) => s.select);
  const row = (active: boolean) => `flex w-full items-center justify-between gap-2 rounded-lg border px-2.5 py-1.5 text-left text-[12px] transition ${active ? "border-accent bg-accent-soft text-accent" : "border-transparent text-ink2 hover:bg-panel2"}`;
  return (
    <Section title={t("oe.f.rooms")}>
      <p className="text-[11.5px] leading-snug text-mute">{t("oe.z.hint")}</p>
      <ul className="space-y-0.5">
        {ZONE_NAMES.map((z) => {
          const moved = !!layout.zones[z];
          return (
            <li key={z} className="flex items-center gap-1">
              <button type="button" data-testid={`oe-zone-${z}`} onClick={() => select({ kind: "zone", id: z })} className={row(sel?.kind === "zone" && sel.id === z)}><span>{zoneName(t, z)}</span>{moved && <span className="text-[10.5px] text-mute">{t("oe.d.moved")}</span>}</button>
              {moved && <button type="button" onClick={() => mutate((l) => { const zs = { ...l.zones }; delete zs[z]; return { ...l, zones: zs }; })} className="shrink-0 rounded-md px-2 py-1 text-[11px] font-medium text-accent hover:bg-accent-soft">{t("oe.z.reset")}</button>}
            </li>
          );
        })}
        {FIXTURE_IDS.map((id, i) => {
          const hidden = !!layout.fixtures[id]?.hidden;
          return (
            <li key={id} className="flex items-center gap-1">
              <button type="button" data-testid={`oe-fixture-${id}`} disabled={hidden} onClick={() => select({ kind: "fixture", id })} className={`${row(sel?.kind === "fixture" && sel.id === id)} disabled:opacity-50`}>
                <span>{fixtureName(t, id)}{id !== "rack" ? ` ${i + 1}` : ""}</span>{hidden && <span className="text-[10.5px] text-mute">{t("oe.fx.hidden")}</span>}
              </button>
              {hidden && <button type="button" onClick={() => mutate((l) => { const fx = { ...l.fixtures }; delete fx[id]; return { ...l, fixtures: fx }; })} className="shrink-0 rounded-md px-2 py-1 text-[11px] font-medium text-accent hover:bg-accent-soft">{t("oe.fx.show")}</button>}
            </li>
          );
        })}
      </ul>
    </Section>
  );
}

/** Controls of the selected room or original decoration. */
function SelectedOther() {
  const { t } = useT();
  const sel = useOfficeLayout((s) => s.selection);
  const mutate = useOfficeLayout((s) => s.mutate);
  if (sel?.kind === "fixture") {
    const id = sel.id;
    const rot = () => mutate((l) => ({ ...l, fixtures: { ...l.fixtures, [id]: { ...(l.fixtures[id] ?? { x: FIXTURES[id].x, z: FIXTURES[id].z }), rot: (l.fixtures[id]?.rot ?? FIXTURES[id].rot) + Math.PI / 2 } } }), `rot:${id}`);
    return (
      <div data-testid="oe-selected" className="space-y-3 rounded-xl border border-accent/40 bg-accent-soft/40 p-3">
        <div className="text-[13px] font-semibold text-ink">{fixtureName(t, id)}</div>
        <div className="flex gap-2">
          <Btn onClick={rot}>{t("oe.f.rotate")} (R)</Btn>
          <Btn kind="danger" onClick={() => { mutate((l) => ({ ...l, fixtures: { ...l.fixtures, [id]: { ...(l.fixtures[id] ?? { x: FIXTURES[id].x, z: FIXTURES[id].z }), hidden: true } } })); useOfficeLayout.getState().select(null); }}>{t("oe.f.remove")}</Btn>
        </div>
      </div>
    );
  }
  if (sel?.kind === "zone") return (
    <div data-testid="oe-selected" className="space-y-1 rounded-xl border border-accent/40 bg-accent-soft/40 p-3">
      <div className="text-[13px] font-semibold text-ink">{zoneName(t, sel.id)}</div>
      <p className="text-[11.5px] text-mute">{t("oe.z.hint")}</p>
    </div>
  );
  return null;
}

function FurnitureTab() {
  const { t, locale } = useT();
  const layout = useOfficeLayout((s) => s.draft) as Layout;
  const catalog = useRewards((s) => s.catalog);
  const owned = useRewards((s) => s.owned);
  const placing = useOfficeLayout((s) => s.placing);
  const sel = useOfficeLayout((s) => s.selection);
  const [cat, setCat] = useState("all");
  const placedIds = useMemo(() => new Set(layout.pieces.map((p) => p.item)), [layout.pieces]);
  const stock = useMemo(() => catalog.filter((i) => i.kind === "office" && owned[i.id] && !NON_PIECE.has(i.category) && !placedIds.has(i.id)), [catalog, owned, placedIds]);
  const cats = useMemo(() => CAT_ORDER.filter((c) => stock.some((i) => i.category === c)), [stock]);
  const shown = stock.filter((i) => cat === "all" || i.category === cat).sort((a, b) => CAT_ORDER.indexOf(a.category) - CAT_ORDER.indexOf(b.category) || a.price - b.price);
  return (
    <div className="space-y-5">
      <SelectedPiece />
      <SelectedOther />
      <Section title={t("oe.f.inventory")}>
        {stock.length === 0 ? (
          <div className="space-y-2 rounded-xl border border-dashed border-line-strong p-4 text-center text-[12px] text-mute">
            <p>{t("oe.f.empty")}</p>
            <Btn kind="primary" onClick={() => { useOfficeLayout.getState().cancel(); go("/shop"); }}>{t("oe.f.shop")}</Btn>
          </div>
        ) : (
          <>
            <div className="flex flex-wrap gap-1.5">
              {["all", ...cats].map((c) => (
                <button key={c} type="button" aria-pressed={cat === c} onClick={() => setCat(c)}
                  className={`rounded-full border px-2.5 py-1 text-[11px] font-medium transition ${cat === c ? "border-accent/40 bg-accent-soft text-accent" : "border-line bg-panel text-ink2 hover:bg-panel2"}`}>{c === "all" ? t("oe.f.all") : t(`shop.cat.${c}`)}</button>
              ))}
            </div>
            <div className="grid grid-cols-3 gap-2">
              {shown.map((it) => (
                <button key={it.id} type="button" data-testid={`oe-stock-${it.id}`} aria-pressed={placing === it.id} onClick={() => useOfficeLayout.getState().startPlacing(placing === it.id ? null : it.id)}
                  className={`group flex flex-col items-center gap-1 rounded-xl border p-1.5 text-center transition ${placing === it.id ? "border-accent bg-accent-soft ring-2 ring-accent/30" : "border-line bg-panel hover:-translate-y-0.5 hover:border-line-strong hover:shadow-soft"}`}>
                  <ItemThumb item={it} size={72} />
                  <span className="line-clamp-2 text-[10.5px] font-medium leading-tight text-ink2">{itemName(it, locale)}</span>
                </button>
              ))}
            </div>
            <button type="button" onClick={() => { useOfficeLayout.getState().cancel(); go("/shop"); }} className="text-[11.5px] font-medium text-accent hover:underline">{t("oe.f.shop")}</button>
          </>
        )}
      </Section>
      <Section title={t("oe.f.placed")}>
        {layout.pieces.length === 0 ? <p className="text-[11.5px] text-mute">{t("oe.f.none")}</p> : (
          <ul className="space-y-1">
            {layout.pieces.map((p) => {
              const it = catalog.find((i) => i.id === p.item);
              if (!it) return null;
              const active = sel?.kind === "piece" && sel.uid === p.uid;
              return (
                <li key={p.uid}>
                  <button type="button" onClick={() => useOfficeLayout.getState().select({ kind: "piece", uid: p.uid })}
                    className={`flex w-full items-center gap-2 rounded-lg border px-2 py-1 text-left text-[12px] transition ${active ? "border-accent bg-accent-soft text-accent" : "border-transparent text-ink2 hover:bg-panel2"}`}>
                    <ItemPreview item={it} size={28} /><span className="truncate">{itemName(it, locale)}</span>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </Section>
      <RoomsSection />
    </div>
  );
}

// ------------------------------------------------------------------ workstations

function StylePicker({ items, value, none, onPick }: { items: ShopItem[]; value: string | undefined; none: string; onPick: (id: string | undefined) => void }) {
  const { locale } = useT();
  return (
    <div className="flex flex-wrap gap-1.5">
      <button type="button" aria-pressed={!value} onClick={() => onPick(undefined)} className={`rounded-lg border px-2 py-1.5 text-[11px] font-medium ${!value ? "border-accent bg-accent-soft text-accent" : "border-line text-ink2 hover:bg-panel2"}`}>{none}</button>
      {items.map((it) => (
        <button key={it.id} type="button" title={itemName(it, locale)} aria-pressed={value === it.id} onClick={() => onPick(it.id)}
          className={`rounded-lg border p-0.5 ${value === it.id ? "border-accent ring-2 ring-accent/30" : "border-line hover:bg-panel2"}`}><ItemPreview item={it} size={40} /></button>
      ))}
    </div>
  );
}

function DesksTab() {
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
  return (
    <div className="space-y-4">
      <p className="text-[12px] leading-snug text-mute">{t("oe.d.hint")}</p>
      {desks.length + chairs.length === 0 && (
        <div className="space-y-2 rounded-xl border border-dashed border-line-strong p-3 text-center text-[12px] text-mute">
          <p>{t("oe.d.none")}</p>
          <Btn kind="primary" onClick={() => { useOfficeLayout.getState().cancel(); go("/shop"); }}>{t("oe.f.shop")}</Btn>
        </div>
      )}
      <ul className="space-y-2">
        {order.map((id) => {
          const a = agents[id];
          const cfg = layout.desks[id] || {};
          const open = active === id;
          const color = roleMeta(a?.role).color;
          return (
            <li key={id} className={`rounded-xl border p-2 ${open ? "border-accent bg-accent-soft/30" : "border-line bg-panel"}`}>
              <button type="button" data-testid={`oe-desk-${id}`} onClick={() => useOfficeLayout.getState().select(open ? null : { kind: "desk", id })} className="flex w-full items-center gap-2 text-left">
                <i className="h-2.5 w-2.5 shrink-0 rounded-full" style={{ background: color }} />
                <span className="min-w-0 flex-1 truncate text-[12.5px] font-semibold text-ink">{name(id)}</span>
                {cfg.x !== undefined && <span className="text-[10.5px] text-mute">{t("oe.d.moved")}</span>}
                <Icon name="chevron" size={14} className={`shrink-0 text-mute transition-transform ${open ? "rotate-90" : ""}`} />
              </button>
              {open && (
                <div className="mt-3 space-y-3">
                  {desks.length > 0 && (
                    <div className="space-y-1.5">
                      <div className="flex items-center justify-between text-[11.5px] text-mute"><span>{t("oe.d.desk")}</span><button type="button" onClick={() => all("desk", cfg.desk)} className="font-medium text-accent hover:underline">{t("oe.d.all")}</button></div>
                      <StylePicker items={desks} value={cfg.desk} none={t("oe.d.default")} onPick={(v) => (v ? setCfg(id, { desk: v }) : setCfg(id, {}, ["desk"]))} />
                    </div>
                  )}
                  {chairs.length > 0 && (
                    <div className="space-y-1.5">
                      <div className="flex items-center justify-between text-[11.5px] text-mute"><span>{t("oe.d.chair")}</span><button type="button" onClick={() => all("chair", cfg.chair)} className="font-medium text-accent hover:underline">{t("oe.d.all")}</button></div>
                      <StylePicker items={chairs} value={cfg.chair} none={t("oe.d.default")} onPick={(v) => (v ? setCfg(id, { chair: v }) : setCfg(id, {}, ["chair"]))} />
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

// ------------------------------------------------------------------ the panel

const TABS = ["furniture", "desks", "ambience"] as const;

export function OfficeEditor() {
  const { t } = useT();
  const editing = useOfficeLayout((s) => s.editing);
  const tab = useOfficeLayout((s) => s.tab);
  const saving = useOfficeLayout((s) => s.saving);
  const error = useOfficeLayout((s) => s.error);
  const canUndo = useOfficeLayout((s) => s.past.length > 0);
  const canRedo = useOfficeLayout((s) => s.future.length > 0);
  const grid = useOfficeLayout((s) => s.grid);
  const placing = useOfficeLayout((s) => s.placing);
  const dragging = useOfficeLayout((s) => s.dragging);
  const sel = useOfficeLayout((s) => s.selection);
  const dirty = useOfficeLayout((s) => !!s.draft && JSON.stringify(s.draft) !== JSON.stringify(s.layout));
  if (!editing) return null;
  const st = useOfficeLayout.getState;
  const hint = placing ? t("oe.hint.placing") : sel ? t("oe.hint.drag") : t("oe.hint.idle");
  return (
    <>
      <ThumbStudio />
      <div data-testid="office-editor" className="pointer-events-auto absolute bottom-4 left-4 top-4 z-40 flex w-[348px] max-w-[calc(100vw-32px)] flex-col overflow-hidden rounded-2xl border border-line bg-panel shadow-pop">
        <header className="border-b border-line px-4 pb-0 pt-3">
          <div className="flex items-center justify-between">
            <h2 className="font-display text-[15px] font-semibold text-ink">{t("oe.title")}</h2>
            <button type="button" onClick={() => st().cancel()} aria-label={t("oe.cancel")} className="rounded-md p-1 text-mute hover:bg-panel2 hover:text-ink"><Icon name="x" size={16} /></button>
          </div>
          <div role="tablist" className="mt-2 flex gap-1">
            {TABS.map((k) => (
              <button key={k} type="button" role="tab" aria-selected={tab === k} data-testid={`oe-tab-${k}`} onClick={() => st().setTab(k)}
                className={`-mb-px border-b-2 px-3 py-2 text-[12.5px] font-medium transition ${tab === k ? "border-accent text-accent" : "border-transparent text-mute hover:text-ink"}`}>{t(`oe.tab.${k}`)}</button>
            ))}
          </div>
        </header>
        <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4">
          {tab === "furniture" && <FurnitureTab />}
          {tab === "desks" && <DesksTab />}
          {tab === "ambience" && <AmbienceTab />}
        </div>
        <footer className="space-y-2 border-t border-line bg-panel2/60 px-4 py-3">
          {error && (
            <div className="space-y-1.5">
              <ErrorLine msg={t(`oe.err.${error}`)} />
              {error === "conflict" && <Btn onClick={() => { st().cancel(); void st().load(); }}>{t("oe.reload")}</Btn>}
            </div>
          )}
          <div className="flex items-center gap-1.5">
            <button type="button" disabled={!canUndo} onClick={() => st().undo()} aria-label={t("oe.undo")} title={`${t("oe.undo")} (Ctrl+Z)`} data-testid="oe-undo" className="rounded-md border border-line-strong bg-panel p-1.5 text-ink2 transition hover:bg-panel2 disabled:opacity-40"><Icon name="back" size={15} /></button>
            <button type="button" disabled={!canRedo} onClick={() => st().redo()} aria-label={t("oe.redo")} title={`${t("oe.redo")} (Ctrl+Y)`} data-testid="oe-redo" className="rounded-md border border-line-strong bg-panel p-1.5 text-ink2 transition hover:bg-panel2 disabled:opacity-40"><Icon name="back" size={15} className="-scale-x-100" /></button>
            <button type="button" aria-pressed={grid} onClick={() => st().toggleGrid()} aria-label={t("oe.grid")} title={t("oe.grid")} className={`rounded-md border p-1.5 transition ${grid ? "border-accent/40 bg-accent-soft text-accent" : "border-line-strong bg-panel text-ink2 hover:bg-panel2"}`}><Icon name="grid" size={15} /></button>
            <span className="flex-1" />
            <Btn onClick={() => st().cancel()} data-testid="oe-cancel">{t("oe.cancel")}</Btn>
            <Btn kind="primary" disabled={saving} onClick={() => void st().save()} data-testid="oe-save">{saving ? t("oe.saving") : dirty ? t("oe.save") : t("oe.done")}</Btn>
          </div>
        </footer>
      </div>
      <div className="pointer-events-none absolute left-[380px] top-4 z-30 hidden rounded-full border border-line bg-panel/95 px-3.5 py-1.5 text-[11.5px] font-medium text-ink2 shadow-soft backdrop-blur md:block">
        {dragging ? t("oe.hint.moving") : hint}
      </div>
    </>
  );
}
