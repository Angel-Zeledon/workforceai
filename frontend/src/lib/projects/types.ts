/** Project / workflow visualization contracts (docs/architecture/workflow-visualization.md). Additive: legacy Task/Request are untouched. */

export type NodeKind = "task" | "subtask" | "consult" | "group" | "milestone" | "gate" | "wait" | "review";
export type NodeState =
  | "draft" | "pending" | "ready" | "running" | "awaiting_approval" | "waiting"
  | "paused" | "blocked" | "done" | "skipped" | "failed" | "cancelled";
export type ProjectStatus = "draft" | "planning" | "ready" | "running" | "paused" | "waiting_human" | "done" | "failed" | "cancelled";
export type ProjectControl = "active" | "paused" | "cancelled";
export type Complexity = "S" | "M" | "L" | "XL";
export type Light = "green" | "amber" | "red";
export type Risk = "low" | "medium" | "high";

export interface ProjectNode {
  id: string;
  project_id: string;
  objective_id: string;
  /** workflow / group node id (null for workflow groups themselves) */
  parent_id: string | null;
  kind: NodeKind;
  /** resolved text (project locale at creation); `title_key` re-localizes it until the user edits the title */
  title: string;
  title_key?: string;
  title_params?: Record<string, string>;
  agent_id: string | null;
  state: NodeState;
  attempt: number;
  max_attempts: number;
  progress: number;
  depends_on: string[];
  delegation_depth: number;
  delegation_chain: string[];
  /** topological layer: layout only, never capped */
  dag_level: number;
  wbs_path: string;
  complexity: Complexity;
  est_cost_usd: number;
  est_seconds: number;
  cost_usd: number;
  /** offsets in ms from project start (planned schedule, computed at launch) */
  plan_start_ms: number | null;
  plan_end_ms: number | null;
  started_at: string | null;
  finished_at: string | null;
  rev: number;
  approval_action?: string;
  /** a human skipped this failed node (W2) */
  skipped?: boolean;
  skip_reason?: string;
  error?: string | null;
}

export interface ProjectObjective { id: string; title: string; title_key?: string; title_params?: Record<string, string>; position: number }

export interface ProjectApproval {
  id: string; project_id: string; node_id: string; agent_id: string | null;
  action: string; title: string; details: string; risk: Risk;
  status: "pending" | "approved" | "rejected"; created_at: string; resolved_at: string | null;
}

export interface BudgetPolicy { mode: "hard" | "soft"; warn_at: number[]; on_hard: "pause_and_ask" | "fail"; reserve_pct: number }

export interface ProjectEstimate {
  basis: "priors" | "calibrated";
  model: string;
  confidence: "low" | "medium" | "high";
  total: { p50_usd: number; p90_usd: number; calls: number };
  duration: { p50_seconds: number; p90_seconds: number; human_wait_seconds: number };
  by_objective: { id: string; p50_usd: number; p90_usd: number; nodes: number }[];
  by_agent: { agent_id: string; p50_usd: number; calls: number }[];
  warnings: { key: string; params?: Record<string, string | number> }[];
}

export interface ProjectSummary {
  id: string; name: string; name_key?: string; goal: string;
  status: ProjectStatus; control: ProjectControl;
  template_id: string | null;
  budget_usd: number; spent_usd: number;
  tasks_done: number; tasks_total: number; running: number; awaiting: number; failed: number;
  light: Light;
  created_at: string; started_at: string | null; finished_at: string | null; deadline_at: string | null;
  objectives_count: number;
}

export interface ProjectDetail {
  project: ProjectSummary;
  objectives: ProjectObjective[];
  nodes: ProjectNode[];
  approvals: ProjectApproval[];
  budget: BudgetPolicy;
  estimate: ProjectEstimate | null;
  structure_version: number;
  max_parallel: number;
  /** while the hierarchical planner is still producing nodes */
  planning: { done: number; total: number } | null;
}

export interface TemplateParam { key: string; label_key: string; default: string }
export interface TemplateNode {
  key: string; kind?: NodeKind; title_key: string; title?: string; agent?: string; complexity?: Complexity;
  deps?: string[]; secs?: number;
  approval?: { action: string; risk: Risk };
  /** the agent delegates a subtask to another agent at runtime (fork/join, delegation_depth+1) */
  delegate?: { agent: string; title_key: string; complexity?: Complexity };
  fail_once?: boolean;
}
export interface TemplateWorkflow { key: string; title_key: string; title?: string; nodes: TemplateNode[] }
export interface TemplateObjective { key: string; title_key: string; title?: string; workflows: TemplateWorkflow[] }
export interface ProjectTemplate {
  id: string; key: string; version: number; name_key?: string; name: string; description_key?: string; description: string;
  params: TemplateParam[]; objectives: TemplateObjective[]; builtin: boolean;
  budget_hint?: { p50_usd: number; p90_usd: number; basis: "priors" | "calibrated" };
}

export interface ProjectHealth {
  project_id: string; computed_at: string; light: Light;
  progress: { tasks_done: number; tasks_total: number; weighted_pct: number };
  schedule: { deadline_at: string | null; eta_p50: string | null; eta_p90: string | null; slip_seconds_p50: number | null };
  critical_path: { length_seconds: number; node_ids: string[]; blocked_on_human: number };
  budget: { limit_usd: number; spent_usd: number; burn_usd_per_h: number; forecast_at_completion_usd: number; forecast_p90_usd: number; warning: "none" | "p90_near_limit" | "over" };
  waiting_human: { count: number; oldest_age_seconds: number; blocking_critical: number };
  risks: { kind: "retry" | "agent_saturated" | "failed"; node_ids: string[]; agent_id?: string; count: number }[];
  by_objective: { id: string; pct: number; state: NodeState | "running" | "done"; spent_usd: number }[];
}

/** WS-like frames for the projects channel (project.created / status_changed / delta / plan.draft_progress). */
export interface ProjectFrame {
  type: "project.created" | "project.status_changed" | "project.delta" | "plan.draft_progress";
  payload: {
    project: ProjectSummary;
    nodes?: ProjectNode[];
    objectives?: ProjectObjective[];
    approvals?: ProjectApproval[];
    planning?: { done: number; total: number } | null;
    structure_version?: number;
    estimate?: ProjectEstimate | null;
    removed_node_ids?: string[];
  };
}

export type PlanOp =
  | { op: "update"; id: string; fields: { title?: string; agent_id?: string } };

export interface NewProjectBody { goal: string; template_id?: string; params?: Record<string, string>; budget_usd?: number }
export interface LaunchBody { approved_budget_usd: number; acknowledge_underbudget?: boolean; max_parallel?: number }
export type ControlAction = "pause" | "resume" | "cancel";
