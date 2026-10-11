"use client";
import { fmtUsd, useT } from "@/lib/i18n";
import { useEnergy } from "./officeHooks";

const COLOR = { ok: "#2f9e6b", low: "#d9890b", empty: "#d14343" } as const;

/** Remaining budget of an agent as an "energy" bar. The dollar amount is ALWAYS shown next to it (no abstract points). */
export function EnergyBar({ id, compact = false }: { id: string; compact?: boolean }) {
  const { t } = useT();
  const e = useEnergy(id);
  if (!e) return null;
  const c = COLOR[e.level];
  const label = t("office.energy.left", { amount: fmtUsd(e.remaining) });
  return (
    <div data-testid={`office-energy-${id}`} data-level={e.level} data-paused={e.paused ? "1" : "0"} title={`${t("office.energy")}: ${label} / ${fmtUsd(e.capUsd)}`}
      className={`flex items-center gap-1.5 ${compact ? "" : "mt-1.5"}`}>
      <div className={`${compact ? "h-1 w-8" : "h-1.5 flex-1"} overflow-hidden rounded-full bg-mute/15`}>
        <div className="h-full rounded-full transition-all duration-500" style={{ width: `${Math.max(3, Math.round(e.fraction * 100))}%`, background: c }} />
      </div>
      <span className={`shrink-0 font-mono ${compact ? "text-[9px]" : "text-[10.5px]"} font-semibold`} style={{ color: c }}>{compact ? fmtUsd(e.remaining) : label}</span>
    </div>
  );
}
