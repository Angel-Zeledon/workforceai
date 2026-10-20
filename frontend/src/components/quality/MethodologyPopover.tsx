"use client";
import { useEffect, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import type { Methodology } from "@/lib/qualityApi";
import { weightPct } from "@/lib/qualityLogic";

/** "How is this calculated?": the components, weights and minimum samples exactly as the API returned them. */
export function MethodologyPopover({ m }: { m: Methodology }) {
  const { t } = useT();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(false); };
    const onDown = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false); };
    document.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onDown);
    return () => { document.removeEventListener("keydown", onKey); document.removeEventListener("mousedown", onDown); };
  }, [open]);

  return (
    <div ref={ref} className="relative inline-block">
      <button type="button" data-testid="quality-methodology-open" aria-expanded={open} aria-haspopup="dialog" onClick={() => setOpen((v) => !v)}
        className="text-[11px] font-medium text-accent hover:underline">{t("quality.methodology.button")}</button>
      {open && (
        <div role="dialog" aria-label={t("quality.methodology.title")} data-testid="quality-methodology"
          className="absolute right-0 z-30 mt-1 w-[min(340px,86vw)] rounded-xl border border-line bg-panel p-3 text-left shadow-float">
          <div className="text-[12px] font-semibold text-ink">{t("quality.methodology.title")}</div>
          <p className="mt-1 text-[11px] leading-snug text-ink2">{t("quality.methodology.intro")}</p>
          <ul className="mt-2 space-y-2">
            {m.components.map((c) => (
              <li key={c.key} data-testid={`quality-method-${c.key}`} className="text-[11px] leading-snug">
                <div className="flex items-baseline justify-between gap-2">
                  <span className="font-semibold text-ink">{t(`quality.component.${c.key}`)}</span>
                  <span className="shrink-0 text-mute">{t("quality.methodology.weight", { pct: weightPct(c.weight) })} · {t("quality.methodology.min", { n: c.min_sample })}</span>
                </div>
                <div className="text-ink2">{t(`quality.methodology.${c.key}`)}</div>
              </li>
            ))}
          </ul>
          <ul className="mt-2 space-y-1 border-t border-line pt-2 text-[10.5px] text-mute">
            <li>{t("quality.methodology.minTotal", { n: m.min_total_sample })}</li>
            <li>{t("quality.methodology.window", { n: m.window_days })}</li>
            <li>{t("quality.methodology.trend", { n: m.trend_delta })}</li>
            <li>{t("quality.advisory")}</li>
          </ul>
        </div>
      )}
    </div>
  );
}
