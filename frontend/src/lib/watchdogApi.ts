import { call } from "./api";

/** Stalled-work watchdog (F6). Contract: backend/internal/api/watchdog.go, docs/architecture/watchdog.md. */
export type StallKind = "no_progress" | "no_worker";

export interface StalledTask {
  task_id: string; request_id: string; agent_id: string; title: string; status: string;
  kind: StallKind; project: boolean; last_progress: string; minutes: number; episode: string;
}

export interface StallCandidate { id: string; name: string; role: string; title: string }
export interface CascadeTask { id: string; title: string; agent_id: string; status: string }

export interface StallDetail {
  task: { id: string; title: string; agent_id: string; status: string; request_id: string };
  stalled: boolean;
  stall?: StalledTask;
  reassign_candidates: StallCandidate[];
  /** Active dependents a cancellation also stops: the person confirms this exact list. */
  cancel_cascade: CascadeTask[];
  /** Dependents of departments the person does not see: a cancellation is then refused. */
  hidden_cascade: number;
}

export type StallAction = "retry" | "reassign" | "cancel";

export interface StallResult { action: StallAction; status: "applied" | "requested" | "resumed" | "reopened" | "finished"; cancelled?: string[] }

const t = (id: string) => `/tasks/${encodeURIComponent(id)}/stall`;

export const watchdogApi = {
  list: () => call<StalledTask[]>("GET", "/stalled-work"),
  detail: (taskId: string) => call<StallDetail>("GET", t(taskId)),
  retry: (taskId: string) => call<StallResult>("POST", `${t(taskId)}/retry`, {}),
  reassign: (taskId: string, agentId: string) => call<StallResult>("POST", `${t(taskId)}/reassign`, { agent_id: agentId }),
  cancel: (taskId: string, confirmTaskIds: string[]) => call<StallResult>("POST", `${t(taskId)}/cancel`, { confirm_task_ids: confirmTaskIds }),
};
