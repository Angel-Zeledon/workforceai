"use client";
import type { ReactNode } from "react";
import { useT, fmtNumber } from "@/lib/i18n";
import { displayTitle } from "@/lib/projects/calc";
import type { Light, NodeState, ProjectNode, ProjectStatus } from "@/lib/projects/types";
import { useAgentColor, useAgentName } from "../ui";

export const NODE_STATE_COLOR: Record<NodeState, string> = {
  draft: "#aab3c0", pending: "#66707f", ready: "#5b8fb5", running: "#2b6cb0", awaiting_approval: "#a86208", waiting: "#7c6bb0",
  paused: "#8b94a3", blocked: "#b4443c", done: "#2f7d55", skipped: "#aab3c0", failed: "#a63232", cancelled: "#8b94a3",
};
export const RETRY_COLOR = "#b8651f";
export const CRITICAL_COLOR = "#3451b2";
export const LIGHT_COLOR: Record<Light, string> = { green: "#2f7d55", amber: "#a86208", red: "#a63232" };
export const STATUS_COLOR: Record<ProjectStatus, string> = {
  draft: "#aab3c0", planning: "#7c6bb0", ready: "#5b8fb5", running: "#2b6cb0", paused: "#8b94a3", waiting_human: "#a86208",
  done: "#2f7d55", failed: "#a63232", cancelled: "#8b94a3",
};

/** USD with enough decimals for the sub-dollar amounts LLM tasks cost. */
export const fmtMoney = (n: number) => `$${fmtNumber(n || 0, Math.abs(n) < 1 ? 3 : 2)}`;

export function nodeColor(n: ProjectNode) {
  return n.state === "ready" && n.attempt > 0 ? RETRY_COLOR : NODE_STATE_COLOR[n.state];
}

export function useNodeTitle() {
  useT();
  return (n: { title: string; title_key?: string; title_params?: Record<string, string> }) => displayTitle(n);
}

export function StatePill({ state, retrying }: { state: NodeState; retrying?: boolean }) {
  const { t } = useT();
  const c = retrying ? RETRY_COLOR : NODE_STATE_COLOR[state];
  return (
    <span className="inline-flex items-center gap-1 whitespace-nowrap rounded px-1.5 py-[2px] text-[10px] font-semibold uppercase tracking-wide" style={{ background: `${c}22`, color: c }}>
      <span className="h-1.5 w-1.5 rounded-full" style={{ background: c }} />
      {retrying ? t("pv.state.retrying") : t(`pv.state.${state}`)}
    </span>
  );
}

export function StatusPill({ status }: { status: ProjectStatus }) {
  const { t } = useT();
  const c = STATUS_COLOR[status];
  return (
    <span className="inline-flex items-center gap-1 whitespace-nowrap rounded-full px-2 py-[3px] text-[10px] font-semibold uppercase tracking-wide" style={{ background: `${c}26`, color: c }}>
      <span className="h-1.5 w-1.5 rounded-full" style={{ background: c }} />
      {t(`pv.status.${status}`)}
    </span>
  );
}

export function LightDot({ light, testId }: { light: Light; testId?: string }) {
  const { t } = useT();
  return <span data-testid={testId} data-light={light} title={t(`pv.light.${light}`)} className="inline-block h-3 w-3 shrink-0 rounded-full border border-black/10" style={{ background: LIGHT_COLOR[light] }} />;
}

export function AgentChip({ id, small }: { id: string | null; small?: boolean }) {
  const name = useAgentName();
  const color = useAgentColor();
  const { t } = useT();
  if (!id) return <span className="text-[11px] text-mute">{t("pv.human")}</span>;
  return (
    <span className={`inline-flex items-center gap-1 ${small ? "text-[10px]" : "text-[11px]"} font-semibold text-ink`}>
      <span className="h-2 w-2 rounded-full" style={{ background: color(id) }} />{name(id).split(" ")[0]}
    </span>
  );
}

export function Modal({ children, onClose, testId, wide }: { children: ReactNode; onClose: () => void; testId?: string; wide?: boolean }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-ink/30 p-4" onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}>
      <div data-testid={testId} className={`ac-pop max-h-[90vh] w-full overflow-y-auto rounded-xl border border-line bg-panel p-5 shadow-soft ${wide ? "max-w-[720px]" : "max-w-[480px]"}`}>{children}</div>
    </div>
  );
}

export function Kpi({ label, value, sub, color = "#151b26", testId, attrs }: { label: string; value: ReactNode; sub?: ReactNode; color?: string; testId?: string; attrs?: Record<string, string | number> }) {
  return (
    <div data-testid={testId} {...attrs} className="rounded-xl border border-line bg-panel p-4 shadow-pop">
      <div className="text-[10px] font-semibold uppercase tracking-[0.08em] text-mute">{label}</div>
      <div className="mt-1 font-mono text-xl font-semibold" style={{ color }}>{value}</div>
      {sub && <div className="mt-0.5 text-[11px] text-mute">{sub}</div>}
    </div>
  );
}

/** Segmented bar by node state (used on cards and workflow rows). */
export function StateBar({ nodes }: { nodes: ProjectNode[] }) {
  const total = nodes.length || 1;
  const order: NodeState[] = ["done", "running", "awaiting_approval", "ready", "waiting", "pending", "blocked", "failed"];
  return (
    <div className="flex h-1.5 w-full overflow-hidden rounded-full bg-mute/[0.14]">
      {order.map((s) => {
        const c = nodes.filter((n) => n.state === s).length;
        return c ? <div key={s} style={{ width: `${(c / total) * 100}%`, background: NODE_STATE_COLOR[s] }} /> : null;
      })}
    </div>
  );
}
