"use client";
import type { ReactNode } from "react";
import { useT, fmtNumber } from "@/lib/i18n";
import { displayTitle } from "@/lib/projects/calc";
import type { Light, NodeState, ProjectNode, ProjectStatus } from "@/lib/projects/types";
import { useAgentColor, useAgentName } from "../ui";
import { KIND_COLOR, colorTable, kindOf } from "@/lib/statusLogic";
import { StatusIcon } from "../ui/Status";

// One palette for the whole app (lib/statusLogic.ts): "Te necesita" amber, "Listo" one green, "Por empezar" gray.
export const NODE_STATE_COLOR: Record<NodeState, string> = colorTable("node", ["draft", "pending", "ready", "running", "awaiting_approval", "waiting", "needs_input", "paused", "blocked", "done", "skipped", "failed", "cancelled"] as const);
export const RETRY_COLOR = "#b8651f";
export const CRITICAL_COLOR = "#0A66C2";
export const LIGHT_COLOR: Record<Light, string> = { green: KIND_COLOR.done, amber: KIND_COLOR.needs, red: KIND_COLOR.problem };
export const STATUS_COLOR: Record<ProjectStatus, string> = colorTable("project", ["draft", "planning", "ready", "running", "paused", "waiting_human", "done", "partial", "failed", "cancelled"] as const);

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
  const c = retrying ? NODE_STATE_COLOR.running : NODE_STATE_COLOR[state];
  const kind = kindOf("node", retrying ? "running" : state);
  return (
    <span data-kind={kind} className="inline-flex items-center gap-1 whitespace-nowrap rounded px-1.5 py-[2px] text-[10px] font-semibold uppercase tracking-wide" style={{ background: `${c}22`, color: c }}>
      <StatusIcon kind={kind} size={10} />
      {retrying ? t("pv.state.retrying") : t(`pv.state.${state}`)}
    </span>
  );
}

export function StatusPill({ status }: { status: ProjectStatus }) {
  const { t } = useT();
  const c = STATUS_COLOR[status];
  const kind = kindOf("project", status);
  return (
    <span data-kind={kind} className="inline-flex items-center gap-1 whitespace-nowrap rounded-full px-2 py-[3px] text-[10px] font-semibold uppercase tracking-wide" style={{ background: `${c}26`, color: c }}>
      <StatusIcon kind={kind} size={10} />
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
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-ink/30 p-0 sm:items-center sm:p-4" onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}>
      <div data-testid={testId} className={`ac-pop max-h-[92dvh] w-full overflow-y-auto overflow-x-hidden rounded-t-2xl border border-line bg-panel px-4 pb-[max(1rem,env(safe-area-inset-bottom))] pt-4 shadow-soft sm:max-h-[90dvh] sm:rounded-xl sm:p-5 ${wide ? "max-w-[720px]" : "max-w-[480px]"}`}>{children}</div>
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
  const order: NodeState[] = ["done", "running", "needs_input", "awaiting_approval", "ready", "waiting", "pending", "blocked", "failed"];
  return (
    <div className="flex h-1.5 w-full overflow-hidden rounded-full bg-mute/[0.14]">
      {order.map((s) => {
        const c = nodes.filter((n) => n.state === s).length;
        return c ? <div key={s} style={{ width: `${(c / total) * 100}%`, background: NODE_STATE_COLOR[s] }} /> : null;
      })}
    </div>
  );
}
