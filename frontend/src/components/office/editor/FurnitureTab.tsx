"use client";
// The "Muebles" tab: the furniture you own and have not placed, what is placed, and the original rooms and decor, all as
// little windows with a picture drawn in the chosen office style (never lists of text).
import { useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import { go } from "@/lib/navBridge";
import { itemName, useRewards } from "@/lib/rewards";
import { pieceOf, useOfficeLayout, withPiece, withoutPiece } from "@/lib/officeLayout";
import { DECOR_IDS, FIXTURES, ROOM_PARTS, ZONE_NAMES, hideFixture, isRoomPart, resetFixture, showFixture, type FixtureId, type Layout, type ZoneName } from "@/lib/officeLayoutModel";
import { Btn } from "../../ui";
import { ItemThumb } from "../../shop/ItemThumb";
import { turnFixture } from "../editorWorld";
import { pieceInfo } from "../pieceInfo";
import { pixelDisplay } from "./thumbPost";
import { useFixtureThumb } from "./useFixtureThumb";
import { EmptyArt, FOCUS, Range, Section, Tile } from "./parts";

const NON_PIECE = new Set(["window", "desk", "chair"]);
const CAT_ORDER = ["plant", "lamp", "light", "rug", "sofa", "table", "cabinet", "divider", "whiteboard", "appliance", "coffee", "poster", "shelf", "aquarium", "arcade", "trophy"];

const zoneName = (t: (k: string) => string, z: ZoneName) => t(`oe.zone.${z}`);
/** "Plant 2", "Seat 3", "Coffee machine": the name of a built-in object for the cards and the selection card. */
function fixtureName(t: (k: string, v?: Record<string, string | number>) => string, id: FixtureId): string {
  if (!isRoomPart(id)) return id === "rack" ? t("oe.fx.rack") : `${t("oe.fx.plant")} ${DECOR_IDS.indexOf(id) + 1}`;
  const m = /^(.*)_(\d)$/.exec(id);
  return m ? t(`oe.part.${m[1]}`, { n: m[2] }) : t(`oe.part.${id}`);
}

/** The picture of a room or of a built-in object, at a size that stays sharp in Pixel. */
function Art({ k, size }: { k: Parameters<typeof useFixtureThumb>[0]; size: number }) {
  const { url, pixel } = useFixtureThumb(k);
  const side = pixel ? pixelDisplay(size) : size;
  if (!url) return <span className="block animate-pulse rounded-md bg-line/60" style={{ width: size, height: size }} aria-hidden />;
  // eslint-disable-next-line @next/next/no-img-element -- generated picture
  return <img src={url} alt="" width={side} height={side} draggable={false} style={{ width: side, height: side, imageRendering: pixel ? "pixelated" : "auto" }} className="object-contain" />;
}

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
    <div data-testid="oe-selected" className="space-y-3 rounded-[10px] border border-accent/40 bg-accent-soft/40 p-3">
      <div className="flex items-center gap-3">
        <div className="shrink-0 rounded-lg bg-panel"><ItemThumb item={item} size={64} /></div>
        <div className="min-w-0 flex-1"><div className="truncate text-[13px] font-semibold text-ink">{itemName(item, locale)}</div><div className="text-[12px] text-mute">{t("oe.f.selected")}</div></div>
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

/** One built-in object: select it, put it back when taken away, or send it back to its spot when moved. */
function FixtureTile({ id }: { id: FixtureId }) {
  const { t } = useT();
  const layout = useOfficeLayout((s) => s.draft) as Layout;
  const sel = useOfficeLayout((s) => s.selection);
  const mutate = useOfficeLayout((s) => s.mutate);
  const cfg = layout.fixtures[id];
  const hidden = !!cfg?.hidden;
  const active = sel?.kind === "fixture" && sel.id === id;
  return (
    <div className={`flex min-w-0 flex-col overflow-hidden rounded-[10px] border bg-panel ${active ? "border-accent ring-2 ring-accent/30" : "border-line"}`}>
      <button type="button" data-testid={`oe-fixture-${id}`} disabled={hidden} aria-pressed={active} onClick={() => useOfficeLayout.getState().select({ kind: "fixture", id })}
        className={`flex min-w-0 flex-1 flex-col text-left transition enabled:hover:bg-panel2/70 disabled:opacity-55 ${FOCUS}`}>
        <span className="flex items-center justify-center bg-panel2/70 py-1.5"><Art k={{ kind: "fixture", id }} size={64} /></span>
        <span className="block px-2 pt-1.5">
          <span className="line-clamp-2 min-h-[2.1rem] break-words text-[12px] font-medium leading-[1.05rem] text-ink">{fixtureName(t, id)}</span>
          <span className="block min-h-[14px] truncate text-[11px] leading-tight text-mute">{hidden ? t("oe.fx.hidden") : cfg ? t("oe.d.moved") : ""}</span>
        </span>
      </button>
      {(hidden || cfg) && (
        <button type="button" data-testid={hidden ? `oe-fixture-show-${id}` : undefined} onClick={() => mutate((l) => (hidden ? showFixture(l, id) : resetFixture(l, id)))}
          className={`mx-1.5 mb-1.5 mt-1 min-h-8 rounded-md px-2 text-[12px] font-medium text-accent transition hover:bg-accent-soft ${FOCUS}`}>{hidden ? t("oe.fx.show") : t("oe.z.reset")}</button>
      )}
    </div>
  );
}

/** Rooms (a picture of each, with its pieces one tap away) and the original plants and rack. */
function RoomsSection() {
  const { t } = useT();
  const layout = useOfficeLayout((s) => s.draft) as Layout;
  const sel = useOfficeLayout((s) => s.selection);
  const mutate = useOfficeLayout((s) => s.mutate);
  const select = useOfficeLayout((s) => s.select);
  const selRoom = sel?.kind === "zone" ? sel.id : sel?.kind === "fixture" ? FIXTURES[sel.id].room : undefined;
  const [open, setOpen] = useState<ZoneName | null>(null);
  return (
    <Section title={t("oe.f.rooms")}>
      <p className="text-[12px] leading-snug text-mute">{t("oe.z.hint")}</p>
      <ul className="space-y-2">
        {ZONE_NAMES.map((z) => {
          const moved = !!layout.zones[z];
          const expanded = open === z || selRoom === z;
          const changed = ROOM_PARTS[z].filter((id) => layout.fixtures[id]).length;
          const active = sel?.kind === "zone" && sel.id === z;
          const status = [moved ? t("oe.d.moved") : "", changed ? t("oe.z.changed", { count: changed }) : ""].filter(Boolean).join(" · ");
          return (
            <li key={z} className={`overflow-hidden rounded-[10px] border bg-panel ${active ? "border-accent ring-2 ring-accent/30" : "border-line"}`}>
              <button type="button" data-testid={`oe-zone-${z}`} aria-pressed={active} onClick={() => select({ kind: "zone", id: z })} className={`flex w-full items-center gap-3 p-2 text-left transition hover:bg-panel2/70 ${FOCUS}`}>
                <span className="flex h-[88px] w-[88px] shrink-0 items-center justify-center rounded-lg bg-panel2/70"><Art k={{ kind: "room", id: z }} size={80} /></span>
                <span className="min-w-0 flex-1">
                  <span className="block text-[13px] font-semibold text-ink">{zoneName(t, z)}</span>
                  <span className="mt-0.5 block text-[12px] text-mute">{status || t("oe.z.pieces", { count: ROOM_PARTS[z].length })}</span>
                </span>
              </button>
              <div className="flex items-center justify-between gap-2 border-t border-line px-2 py-1">
                <button type="button" aria-expanded={expanded} data-testid={`oe-zone-open-${z}`} onClick={() => setOpen(open === z ? null : z)} className={`min-h-9 rounded-md px-2 text-[12px] font-medium text-accent hover:bg-accent-soft ${FOCUS}`}>{expanded ? t("oe.z.hideParts") : t("oe.z.parts")}</button>
                {moved && <button type="button" onClick={() => mutate((l) => { const zs = { ...l.zones }; delete zs[z]; return { ...l, zones: zs }; })} className={`min-h-9 rounded-md px-2 text-[12px] font-medium text-accent hover:bg-accent-soft ${FOCUS}`}>{t("oe.z.reset")}</button>}
              </div>
              {expanded && <div className="grid grid-cols-3 gap-2 border-t border-line bg-panel2/40 p-2" data-testid={`oe-zone-parts-${z}`}>{ROOM_PARTS[z].map((id) => <FixtureTile key={id} id={id} />)}</div>}
            </li>
          );
        })}
      </ul>
      <div className="grid grid-cols-3 gap-2">{DECOR_IDS.map((id) => <FixtureTile key={id} id={id} />)}</div>
    </Section>
  );
}

/** Controls of the selected room or built-in object. */
function SelectedOther() {
  const { t } = useT();
  const sel = useOfficeLayout((s) => s.selection);
  const mutate = useOfficeLayout((s) => s.mutate);
  if (sel?.kind === "fixture") {
    const id = sel.id;
    const room = FIXTURES[id].room;
    return (
      <div data-testid="oe-selected" className="space-y-3 rounded-[10px] border border-accent/40 bg-accent-soft/40 p-3">
        <div className="flex items-center gap-3">
          <div className="shrink-0 rounded-lg bg-panel p-1"><Art k={{ kind: "fixture", id }} size={56} /></div>
          <div className="min-w-0">
            <div className="text-[13px] font-semibold text-ink">{fixtureName(t, id)}</div>
            {room && <div className="text-[12px] text-mute">{zoneName(t, room)} · {t(FIXTURES[id].surface ? "oe.part.carries" : "oe.part.hint")}</div>}
          </div>
        </div>
        <div className="flex gap-2">
          <Btn onClick={() => turnFixture(id)} data-testid="oe-rotate">{t("oe.f.rotate")} (R)</Btn>
          <Btn kind="danger" data-testid="oe-remove" onClick={() => { mutate((l) => hideFixture(l, id)); useOfficeLayout.getState().select(null); }}>{t("oe.f.remove")}</Btn>
        </div>
      </div>
    );
  }
  if (sel?.kind === "zone") return (
    <div data-testid="oe-selected" className="flex items-center gap-3 rounded-[10px] border border-accent/40 bg-accent-soft/40 p-3">
      <div className="shrink-0 rounded-lg bg-panel p-1"><Art k={{ kind: "room", id: sel.id }} size={56} /></div>
      <div className="min-w-0"><div className="text-[13px] font-semibold text-ink">{zoneName(t, sel.id)}</div><p className="text-[12px] leading-snug text-mute">{t("oe.z.hint")}</p></div>
    </div>
  );
  return null;
}

export function FurnitureTab() {
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
  const toShop = () => { useOfficeLayout.getState().cancel(); go("/shop"); };
  // what could be bought, shown (never bought) as the picture of the empty states
  const teaser = useMemo(() => catalog.filter((i) => i.kind === "office" && !NON_PIECE.has(i.category) && !owned[i.id]).slice(0, 3), [catalog, owned]);
  const teaserArt = <div className="flex h-full items-center justify-center gap-0.5 opacity-80">{teaser.map((it) => <ItemThumb key={it.id} item={it} size={48} />)}</div>;
  return (
    <div className="space-y-5">
      <SelectedPiece />
      <SelectedOther />
      <Section title={t("oe.f.inventory")}>
        {stock.length === 0 ? (
          <EmptyArt title={t("oe.f.emptyTitle")} body={t("oe.f.empty")} action={t("oe.f.shop")} onAction={toShop} art={teaserArt} />
        ) : (
          <>
            <div className="flex flex-wrap gap-1.5">
              {["all", ...cats].map((c) => (
                <button key={c} type="button" aria-pressed={cat === c} onClick={() => setCat(c)}
                  className={`min-h-8 rounded-full border px-3 py-1 text-[12px] font-medium transition ${FOCUS} ${cat === c ? "border-accent/40 bg-accent-soft text-accent" : "border-line bg-panel text-ink2 hover:bg-panel2"}`}>{c === "all" ? t("oe.f.all") : t(`shop.cat.${c}`)}</button>
              ))}
            </div>
            <div className="grid grid-cols-3 gap-2" data-testid="oe-stock">
              {shown.map((it) => (
                <Tile key={it.id} testid={`oe-stock-${it.id}`} selected={placing === it.id} name={itemName(it, locale)} onClick={() => useOfficeLayout.getState().startPlacing(placing === it.id ? null : it.id)}>
                  <ItemThumb item={it} size={80} />
                </Tile>
              ))}
            </div>
            <button type="button" onClick={toShop} className={`rounded text-[12px] font-medium text-accent hover:underline ${FOCUS}`}>{t("oe.f.shop")}</button>
          </>
        )}
      </Section>
      <Section title={t("oe.f.placed")}>
        {layout.pieces.length === 0 ? (
          <EmptyArt title={t("oe.f.noneTitle")} body={t("oe.f.none")} action={stock.length ? t("oe.f.placeOne") : t("oe.f.shop")}
            onAction={() => (stock.length ? useOfficeLayout.getState().startPlacing(stock[0].id) : toShop())} art={teaserArt} />
        ) : (
          <div className="grid grid-cols-3 gap-2" data-testid="oe-placed">
            {layout.pieces.map((p) => {
              const it = catalog.find((i) => i.id === p.item);
              if (!it) return null;
              return (
                <Tile key={p.uid} testid={`oe-placed-${p.uid}`} selected={sel?.kind === "piece" && sel.uid === p.uid} name={itemName(it, locale)} onClick={() => useOfficeLayout.getState().select({ kind: "piece", uid: p.uid })}>
                  <ItemThumb item={it} size={64} />
                </Tile>
              );
            })}
          </div>
        )}
      </Section>
      <RoomsSection />
    </div>
  );
}
