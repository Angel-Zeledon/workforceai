"use client";
import { useT } from "@/lib/i18n";
import { useViewMode } from "@/lib/viewMode";
import { Icon } from "../icons";

/**
 * The visible "Ver todo" switch (simple mode / everything open). Remembered per person. `tone="dark"` for the navy
 * sidebar, `light` for screens.
 */
export function ViewAllSwitch({ tone = "light", compact = false }: { tone?: "light" | "dark"; compact?: boolean }) {
  const { t } = useT();
  const viewAll = useViewMode((s) => s.viewAll);
  const setViewAll = useViewMode((s) => s.setViewAll);
  const dark = tone === "dark";
  return (
    <button type="button" role="switch" aria-checked={viewAll} data-testid="view-all-toggle" data-on={viewAll ? "1" : "0"} onClick={() => setViewAll(!viewAll)}
      title={t(viewAll ? "ux.viewAll.onHint" : "ux.viewAll.offHint")}
      className={`flex min-h-9 w-full items-center gap-3 rounded-md px-3 py-1.5 text-left text-[12px] transition ${dark ? "text-white/60 hover:bg-white/5 hover:text-white" : "text-ink2 hover:bg-panel2"} ${compact ? "justify-center px-0" : ""}`}>
      <span aria-hidden className={`relative inline-flex h-[18px] w-8 shrink-0 items-center rounded-full transition-colors ${viewAll ? "bg-[#70b5f9]/80" : dark ? "bg-white/20" : "bg-line-strong"}`}>
        <span className={`absolute h-3.5 w-3.5 rounded-full bg-white shadow transition-transform ${viewAll ? "translate-x-[15px]" : "translate-x-[2px]"}`} />
      </span>
      {!compact && <span className="min-w-0 flex-1 truncate">{t("ux.viewAll.label")}</span>}
    </button>
  );
}

/** "Más vistas (N)": opens the views simple mode keeps folded, only here and only until the screen closes. */
export function MoreButton({ count, open, onToggle, testid }: { count: number; open: boolean; onToggle: () => void; testid: string }) {
  const { t } = useT();
  if (!count) return null;
  return (
    <button type="button" data-testid={testid} aria-expanded={open} onClick={onToggle}
      className="inline-flex items-center gap-1 rounded-md border border-dashed border-line-strong px-3 py-1.5 text-xs font-semibold text-ink2 transition hover:border-accent hover:text-accent">
      {open ? t("ux.more.less") : t("ux.more.open", { count })}
      <Icon name="down" size={12} className={`transition-transform ${open ? "rotate-180" : ""}`} />
    </button>
  );
}
