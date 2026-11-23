"use client";
// The room's light: mood presets, temperature and brightness, the automatic (real time) switch and the HUD button.
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { effective, useOfficeLayout } from "@/lib/officeLayout";
import { MOODS, dayPhase, localHour, type Mood } from "@/lib/officeLayoutModel";
import { usePreferences } from "@/lib/preferences";
import { Icon } from "../../icons";
import { FOCUS, Range } from "./parts";

/** The temperature each preset starts at, so the bar shows where it sits (warm to the left, cool to the right). */
const MOOD_TEMP: Record<Mood, number> = { natural: 0, warm: -0.35, cool: 0.35, sunset: -0.25, night: 0 };
const MOOD_ICON: Record<Mood, "sun" | "moon" | "contrast" | "flag" | "pulse"> = { natural: "sun", warm: "flag", cool: "pulse", sunset: "contrast", night: "moon" };

/** Light mood presets plus two bars: temperature (warm to cool) and brightness. Edits the draft in the editor, saves right away outside it. */
export function LightControls() {
  const { t } = useT();
  const light = useOfficeLayout((s) => effective(s).light);
  const setLight = useOfficeLayout((s) => s.setLight);
  const auto = usePreferences((s) => s.prefs.lightAuto);
  return (
    <div className="space-y-3" data-testid="light-controls">
      <div className="grid grid-cols-5 gap-1.5">
        {MOODS.map((m) => (
          <button key={m} type="button" data-testid={`oe-mood-${m}`} aria-pressed={!auto && light.mood === m}
            onClick={() => { if (auto) usePreferences.getState().set({ lightAuto: false }); setLight({ mood: m, temp: MOOD_TEMP[m] }); }}
            className={`flex flex-col items-center gap-1 rounded-[10px] border px-1 py-2 text-[11px] font-medium transition ${FOCUS} ${!auto && light.mood === m ? "border-accent bg-accent-soft text-accent" : "border-line text-ink2 hover:bg-panel2"}`}>
            <Icon name={MOOD_ICON[m]} size={16} />{t(`oe.mood.${m}`)}
          </button>
        ))}
      </div>
      <Range testid="oe-temp" label={t("oe.a.temp")} value={light.temp} min={-1} max={1} step={0.05} left={t("oe.a.warm")} right={t("oe.a.cool")} onChange={(v) => setLight({ temp: v })} />
      <Range testid="oe-brightness" label={t("oe.a.brightness")} value={light.brightness} min={0.5} max={1.5} step={0.05} left={t("oe.light.dim")} right={t("oe.light.bright")} onChange={(v) => setLight({ brightness: v })} />
    </div>
  );
}

/**
 * "Automatic (real time)": a per-viewer preference (B5). On, the room follows the viewer's local hour (dawn, day,
 * dusk, night); off (the default), it keeps the organization's mood exactly as before. Picking a mood turns it off.
 */
export function AutoLightToggle() {
  const { t } = useT();
  const auto = usePreferences((s) => s.prefs.lightAuto);
  const set = usePreferences((s) => s.set);
  const phase = dayPhase(localHour(new Date()));
  return (
    <label className="flex cursor-pointer items-start gap-2 rounded-[10px] border border-line px-2.5 py-2 text-[12px] text-ink2 hover:bg-panel2">
      <input type="checkbox" data-testid="oe-light-auto" checked={auto} onChange={(e) => set({ lightAuto: e.target.checked })} className="mt-0.5 accent-accent" />
      <span>
        <span className="block font-semibold text-ink">{t("oe.light.auto")}</span>
        <span className="block text-mute">{auto ? t("oe.light.autoNow", { phase: t(`oe.light.phase.${phase}`) }) : t("oe.light.autoHint")}</span>
      </span>
    </label>
  );
}

/** HUD button of the office: the room's light, one click away (replaces the old ambient / lights on / night switch). */
export function LightButton() {
  const { t } = useT();
  const can = useCan("shop:buy");
  const [open, setOpen] = useState(false);
  return (
    <div className="pointer-events-auto relative">
      <button type="button" data-testid="office-light" aria-expanded={open} onClick={() => setOpen((v) => !v)}
        className="flex min-h-10 items-center gap-2 rounded-lg border border-line bg-panel/95 px-3 py-2 text-xs font-semibold text-ink shadow-soft backdrop-blur transition hover:bg-panel2">
        <Icon name="sun" size={15} />{t("oe.light.title")}
      </button>
      {open && (
        <div className="absolute right-0 top-full z-50 mt-2 max-h-[calc(100dvh-6rem)] w-[300px] max-w-[calc(100vw-2rem)] overflow-y-auto rounded-xl border border-line bg-panel p-4 shadow-pop">
          <div className="mb-3 flex items-center justify-between"><h3 className="text-[13px] font-semibold text-ink">{t("oe.a.light")}</h3>
            <button type="button" onClick={() => setOpen(false)} aria-label={t("oe.cancel")} className="-m-1.5 rounded-md p-2.5 text-mute hover:bg-panel2 hover:text-ink"><Icon name="x" size={14} /></button></div>
          <div className="space-y-3">
            <AutoLightToggle />
            {can && <LightControls />}
          </div>
        </div>
      )}
    </div>
  );
}
