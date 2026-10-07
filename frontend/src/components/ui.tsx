"use client";
import { useEffect, useState, type ReactNode } from "react";
import { useStore } from "@/lib/store";
import { fmtTime, stateMeta, taskStatusLabel, TASK_STATUS_COLOR } from "@/lib/meta";
import { tr, useT } from "@/lib/i18n";
import { resolveAgent, usePreferences } from "@/lib/preferences";

/** Resolves an agent id to its display name (honors user-customized names). */
export function useAgentName() {
  const agents = useStore((s) => s.agents);
  const prefs = usePreferences((s) => s.prefs.agents);
  const { t } = useT();
  return (id: string | null | undefined) => {
    if (!id) return t("common.system");
    if (id === "user") return t("common.you");
    if (id === "all") return t("common.everyone");
    if (id === "system") return t("common.system");
    return agents[id] ? resolveAgent(agents[id], prefs[id]).name : id;
  };
}
/** Resolves an agent id to its (possibly customized) accent color. */
export function useAgentColor() {
  const agents = useStore((s) => s.agents);
  const prefs = usePreferences((s) => s.prefs.agents);
  return (id: string | null | undefined) => resolveAgent(id ? agents[id] : undefined, id ? prefs[id] : undefined).color;
}

/** Oscurece un color (p. ej. el de un agente) hasta que el texto sobre blanco cumpla WCAG AA (>= 4.5:1). */
export function readable(hex: string): string {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex.trim());
  if (!m) return hex;
  const n = parseInt(m[1], 16);
  let rgb = [(n >> 16) & 255, (n >> 8) & 255, n & 255];
  const lum = (c: number[]) => {
    const f = c.map((v) => { const s = v / 255; return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4); });
    return 0.2126 * f[0] + 0.7152 * f[1] + 0.0722 * f[2];
  };
  for (let i = 0; i < 12 && 1.05 / (lum(rgb) + 0.05) < 4.6; i++) rgb = rgb.map((v, k) => Math.round(v * 0.88 + [21, 27, 38][k] * 0.12));
  return `#${rgb.map((v) => v.toString(16).padStart(2, "0")).join("")}`;
}

export function StateBadge({ state }: { state: string }) {
  useT();
  const m = stateMeta(state);
  return (
    <span className="inline-flex items-center gap-1 whitespace-nowrap rounded px-1.5 py-[2px] text-[10px] font-semibold uppercase tracking-wide" style={{ background: `${m.color}1a`, color: m.color }}>
      <span className="h-1.5 w-1.5 rounded-full" style={{ background: m.color }} />
      {m.label}
    </span>
  );
}
export function TaskBadge({ status }: { status: string }) {
  useT();
  const c = TASK_STATUS_COLOR[status] || "#7b8494";
  return (
    <span className="inline-flex items-center whitespace-nowrap rounded px-1.5 py-[2px] text-[10px] font-semibold uppercase tracking-wide" style={{ background: `${c}22`, color: c }}>
      {taskStatusLabel(status)}
    </span>
  );
}
export function RiskBadge({ risk }: { risk: string }) {
  const { t } = useT();
  const c = risk === "high" ? "#a63232" : risk === "medium" ? "#a86208" : "#2f7d55";
  const l = t(risk === "high" ? "risk.high" : risk === "medium" ? "risk.medium" : "risk.low");
  return <span className="rounded px-1.5 py-[2px] text-[10px] font-semibold uppercase tracking-wide" style={{ background: `${c}22`, color: c }}>{l}</span>;
}
export function Progress({ value, color = "#2b6cb0" }: { value: number; color?: string }) {
  return (
    <div className="h-1.5 w-full overflow-hidden rounded-full bg-mute/[0.16]">
      <div className="h-full rounded-full transition-all duration-500" style={{ width: `${Math.max(0, Math.min(100, value))}%`, background: color }} />
    </div>
  );
}
export function Dot({ color }: { color: string }) {
  return <span className="inline-block h-2 w-2 shrink-0 rounded-full" style={{ background: color }} />;
}
export function Card({ title, right, children, className = "" }: { title?: ReactNode; right?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={`rounded-xl border border-line bg-panel shadow-pop ${className}`}>
      {title !== undefined && (
        <header className="flex items-center justify-between border-b border-line px-4 py-3">
          <h3 className="text-[11px] font-semibold uppercase tracking-[0.08em] text-mute">{title}</h3>
          {right}
        </header>
      )}
      <div className="p-4">{children}</div>
    </section>
  );
}
export function Empty({ children }: { children: ReactNode }) {
  return <div className="rounded-lg border border-dashed border-line-strong px-3 py-6 text-center text-xs text-mute">{children}</div>;
}
export function Btn({ children, onClick, kind = "ghost", disabled, className = "", type = "button", "data-testid": testId }: {
  "data-testid"?: string; children: ReactNode; onClick?: () => void; kind?: "ghost" | "primary" | "ok" | "danger"; disabled?: boolean; className?: string; type?: "button" | "submit";
}) {
  const k = {
    ghost: "border-line-strong bg-panel text-ink hover:bg-panel2",
    primary: "border-accent bg-accent text-white hover:bg-accent-hover hover:border-accent-hover",
    ok: "border-ok/40 bg-ok/10 text-ok hover:bg-ok/15",
    danger: "border-err/40 bg-err/10 text-err hover:bg-err/15",
  }[kind];
  return (
    <button data-testid={testId} type={type} disabled={disabled} onClick={onClick} className={`rounded-md border px-3.5 py-1.5 text-xs font-medium transition disabled:cursor-not-allowed disabled:opacity-40 ${k} ${className}`}>
      {children}
    </button>
  );
}

export function timeAgo(iso: string) {
  const s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
  if (isNaN(s)) return "";
  if (s < 5) return tr("common.now");
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  return `${Math.floor(s / 3600)}h`;
}
export function useTicker(ms = 1000) {
  const [, set] = useState(0);
  useEffect(() => { const i = setInterval(() => set((x) => x + 1), ms); return () => clearInterval(i); }, [ms]);
}
export { fmtTime };
