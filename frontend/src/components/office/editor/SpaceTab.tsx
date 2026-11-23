"use client";
// The "Espacio" tab: walls, floor, light and the view from the window. The floor samples and the views are drawn in the
// office style that is chosen, so what you pick looks like what you will get.
import { useMemo } from "react";
import { useT } from "@/lib/i18n";
import { itemName, useRewards } from "@/lib/rewards";
import { useOfficeLayout, useOfficeStyle } from "@/lib/officeLayout";
import { DEFAULT_LAYOUT, FLOOR_KINDS, FLOOR_TINTS, SPACE_EFFECTS, TRIM_COLORS, WALL_COLORS, normalizeStyle, type Layout } from "@/lib/officeLayoutModel";
import { Btn } from "../../ui";
import { ItemThumb } from "../../shop/ItemThumb";
import { LightControls } from "./LightPanel";
import { ColorField, FOCUS, Section, Swatches, Tile } from "./parts";
import { SWATCH_H, SWATCH_W, floorSwatch } from "./floorSwatch";

export function SpaceTab() {
  const { t, locale } = useT();
  const layout = useOfficeLayout((s) => s.draft) as Layout;
  const mutate = useOfficeLayout((s) => s.mutate);
  const style = useOfficeStyle();
  const catalog = useRewards((s) => s.catalog);
  const owned = useRewards((s) => s.owned);
  const office = useRewards((s) => s.office);
  const views = catalog.filter((i) => i.kind === "office" && i.category === "window" && owned[i.id]);
  const set = (fn: (l: Layout) => Layout, key?: string) => mutate(fn, key);
  const fx = SPACE_EFFECTS[normalizeStyle(layout.style)];
  const pixel = style === "pixel";
  // the samples only change with the style, the kind and the tone
  const floors = useMemo(() => FLOOR_KINDS.map((k) => ({ k, url: floorSwatch(style, k, layout.floor.tint) })), [style, layout.floor.tint]);
  return (
    <div className="space-y-5">
      <p className="text-[12px] leading-snug text-mute">{t("oe.space.hint")}</p>
      <Section title={t("oe.a.walls")}>
        <Swatches colors={WALL_COLORS} value={layout.walls.color} label={(c) => t("oe.a.swatch", { c })} onPick={(c) => set((l) => ({ ...l, walls: { ...l.walls, color: c } }), "wall")} />
        <ColorField value={layout.walls.color} label={t("oe.a.custom")} onPick={(c) => set((l) => ({ ...l, walls: { ...l.walls, color: c } }), "wall")} />
        {fx.windows && <button type="button" data-testid="oe-windows" role="switch" aria-checked={layout.walls.windows} onClick={() => set((l) => ({ ...l, walls: { ...l.walls, windows: !l.walls.windows } }))}
          className={`flex w-full items-center justify-between rounded-[10px] border border-line px-3 py-2.5 text-[12.5px] font-medium text-ink2 transition hover:bg-panel2 ${FOCUS}`}>
          <span>{t("oe.a.windows")}</span>
          <span className={`flex h-5 w-9 items-center rounded-full px-0.5 transition ${layout.walls.windows ? "bg-accent" : "bg-line-strong"}`}><i className={`h-4 w-4 rounded-full bg-white shadow transition-transform ${layout.walls.windows ? "translate-x-4" : ""}`} /></span>
        </button>}
        {fx.trim && <>
          <div className="pt-1 text-[12px] text-mute">{t("oe.a.trim")}</div>
          <Swatches colors={TRIM_COLORS} value={layout.walls.trim} label={(c) => t("oe.a.swatch", { c })} onPick={(c) => set((l) => ({ ...l, walls: { ...l.walls, trim: c } }), "trim")} />
        </>}
      </Section>

      <Section title={t("oe.a.floor")}>
        <div className="grid grid-cols-3 gap-2" data-testid="oe-floors">
          {floors.map(({ k, url }) => (
            <Tile key={k} testid={`oe-floor-${k}`} selected={layout.floor.kind === k} name={t(`oe.floor.${k}`)} onClick={() => set((l) => ({ ...l, floor: { ...l.floor, kind: k } }))}>
              {/* eslint-disable-next-line @next/next/no-img-element -- a tiny generated sample */}
              <img src={url} alt="" width={SWATCH_W} height={SWATCH_H} draggable={false} style={{ imageRendering: pixel ? "pixelated" : "auto", width: SWATCH_W, height: SWATCH_H }} className="max-w-full object-contain" />
            </Tile>
          ))}
        </div>
        <div className="text-[12px] text-mute">{t("oe.a.tint")}</div>
        <Swatches colors={FLOOR_TINTS} value={layout.floor.tint} label={(c) => t("oe.a.swatch", { c })} onPick={(c) => set((l) => ({ ...l, floor: { ...l.floor, tint: c } }), "tint")} />
      </Section>

      {fx.light && <Section title={t("oe.a.light")}><LightControls /></Section>}

      {fx.view && views.length > 0 && (
        <Section title={t("oe.a.view")}>
          <div className="grid grid-cols-3 gap-2" data-testid="oe-views">
            <Tile selected={!office.window_1} name={t("oe.a.noView")} onClick={() => void useRewards.getState().remove("window_1")}>
              <span className="flex h-16 w-16 items-center justify-center rounded-md border border-dashed border-line-strong text-[11px] text-mute" aria-hidden>—</span>
            </Tile>
            {views.map((v) => (
              <Tile key={v.id} selected={office.window_1 === v.id} name={itemName(v, locale)} onClick={() => void useRewards.getState().place(v.id)}>
                <ItemThumb item={v} size={64} />
              </Tile>
            ))}
          </div>
        </Section>
      )}

      <Btn onClick={() => set((l) => ({ ...l, walls: DEFAULT_LAYOUT.walls, floor: DEFAULT_LAYOUT.floor, light: DEFAULT_LAYOUT.light }))}>{t("oe.a.reset")}</Btn>
    </div>
  );
}
