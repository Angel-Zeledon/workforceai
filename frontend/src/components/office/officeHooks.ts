"use client";
import { useEffect, useMemo, useState } from "react";
import { useStore } from "@/lib/store";
import { useCost, type AgentBudget } from "@/lib/cost";
import { useProjects } from "@/lib/projects/store";
import { energyOf, taskEta, type EtaInfo, type Energy } from "./officeState";

const BUSY = new Set(["working", "thinking", "reviewing", "awaiting_approval"]);

/** Re-renders every `ms` while `on` (keeps the ETA fresh without a global timer). */
function useNow(on: boolean, ms: number) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!on) return;
    setNow(Date.now());
    const i = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(i);
  }, [on, ms]);
  return now;
}

export interface TaskProgress { eta: EtaInfo; /** seconds to the end of the whole project (health.schedule.eta_p50), when known */ projectEtaS: number | null }

type StoreState = ReturnType<typeof useStore.getState>;
type ProjectsState = ReturnType<typeof useProjects.getState>;

/** The pure core of useTaskProgress (the 3D tags use the hook; the flat office reads it from the stores each second). */
export function progressOf(agent: StoreState["agents"][string] | undefined, task: StoreState["tasks"][string] | undefined, detail: ProjectsState["detail"], health: ProjectsState["health"], now: number): TaskProgress | null {
  const active = !!agent && BUSY.has(agent.state) && !!task && task.status === "running";
  if (!active || !task || !agent) return null;
  const node = detail ? detail.nodes.find((n) => n.task_id === task.id) : undefined;
  const startedMs = task.started_at ? Date.parse(task.started_at) : null;
  const eta = taskEta({
    progress: agent.progress, startedMs: startedMs !== null && !Number.isNaN(startedMs) ? startedMs : null, now,
    estSeconds: node?.est_seconds ?? null, calibration: health?.schedule.calibration_factor ?? null,
  });
  if (!eta) return null;
  const pe = node && health?.schedule.eta_p50 ? (Date.parse(health.schedule.eta_p50) - now) / 1000 : null;
  return { eta, projectEtaS: pe !== null && pe > 0 && Number.isFinite(pe) ? pe : null };
}

/**
 * Progress and ETA of the agent's current task from real data: the agent's reported progress, the task start,
 * the project node estimate (matched by task_id) and the project calibration. Null when not applicable.
 */
export function useTaskProgress(id: string): TaskProgress | null {
  const agent = useStore((s) => s.agents[id]);
  const task = useStore((s) => (agent?.current_task_id ? s.tasks[agent.current_task_id] : undefined));
  const detail = useProjects((s) => s.detail);
  const health = useProjects((s) => s.health);
  const active = !!agent && BUSY.has(agent.state) && !!task && task.status === "running";
  const now = useNow(active, 4000);
  return useMemo(() => progressOf(agent, task, detail, health, now), [agent, task, detail, health, now]);
}

export type AgentEnergy = Energy & { capUsd: number; paused: boolean };

/** The pure core of useEnergy: the agent's budget line and whether an agent-scope alert paused it. */
export function energyFor(ab: AgentBudget | undefined, alertPaused: boolean): AgentEnergy | null {
  if (!ab) return null;
  const e = energyOf(ab.cap_usd, ab.spent_usd, ab.reserved_usd);
  return e ? { ...e, capUsd: ab.cap_usd, paused: ab.paused || alertPaused } : null;
}

/** True while an agent-scope budget alert that can be resumed holds this agent. */
export function agentAlertPaused(alerts: ReturnType<typeof useCost.getState>["alerts"], id: string): boolean {
  return Object.values(alerts).some((a) => a.resumable && a.scope === "agent" && (a.agent_id || a.scope_id) === id);
}

/** Remaining budget ("energy") of an agent from GET /budget; null when it has no cap. `paused` mirrors the real pause. */
export function useEnergy(id: string): AgentEnergy | null {
  const ab = useCost((s) => s.status?.agents.find((a) => a.agent_id === id));
  const alertPaused = useCost((s) => agentAlertPaused(s.alerts, id));
  return energyFor(ab, alertPaused);
}
