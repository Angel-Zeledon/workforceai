export type AgentState =
  | "idle" | "thinking" | "working" | "waiting" | "talking"
  | "reviewing" | "blocked" | "awaiting_approval" | "completed" | "error";

export interface AgentMetrics { tasks_completed: number; tasks_pending: number; avg_seconds: number; cost_usd: number }
export interface Agent {
  id: string; name: string; role: string; title: string; description: string;
  state: AgentState; activity: string; current_task_id: string | null; progress: number;
  tools: string[]; permissions: string[];
  autonomy: "suggest" | "approve_each" | "rules" | "autonomous";
  metrics: AgentMetrics;
}
export interface StructuredOutput {
  summary: string; findings: string[]; metrics: Record<string, string | number>;
  hypotheses: string[]; evidence: string[]; recommendations: string[];
  confidence: number; suggested_tasks: string[];
}
export type TaskStatus = "pending" | "running" | "blocked" | "awaiting_approval" | "done" | "failed";
export interface Task {
  id: string; request_id: string; workflow_id: string | null; title: string; description: string;
  agent_id: string; status: TaskStatus; depends_on: string[]; parent_task_id: string | null;
  created_at: string; started_at: string | null; finished_at: string | null; output: StructuredOutput | null;
  /** plain-language reason why this agent got the task (task.created.assigned_reason) */
  assigned_reason?: string | null;
}
export type MessageKind = "chat" | "delegation" | "consult" | "answer" | "system";
export interface Message {
  id: string; conversation_id: string; from: string; to: string; kind: MessageKind;
  text: string; task_id: string | null; ts: string;
}
export interface Conversation { id: string; title: string; participants: string[]; request_id: string | null; last_message_at: string }
export interface Approval {
  id: string; task_id: string; agent_id: string; action: string; title: string; details: string;
  risk: "low" | "medium" | "high"; status: "pending" | "approved" | "rejected";
  created_at: string; resolved_at: string | null;
  /** Governance (optional: older backends omit them). */
  required_approvals?: number; required_role?: string; requested_by?: string;
  decisions?: { by: string; role?: string; note?: string; ts: string }[];
  /** Standing approvals: set by the server only for internal, reversible actions of a project. */
  tool?: string; project_id?: string; standing_eligible?: boolean;
}

/** "Always allow this kind of action in this project" (docs/architecture/06-permisos-autonomia.md). */
export interface StandingApproval {
  id: string; project_id: string; agent_id: string; tool: string; action: string; approval_id?: string;
  created_by: string; created_at: string; expires_at: string; revoked_at?: string; revoked_by?: string;
  use_count: number; last_used_at?: string; status: "active" | "revoked" | "expired";
}
export interface Report {
  id: string; request_id: string; title: string; summary: string;
  sections: { heading: string; body: string }[]; contributors: string[]; cost_usd: number; created_at: string;
}
export interface ActivityItem { id: string; ts: string; agent_id: string | null; kind: string; text: string }
export type RequestStatus =
  | "planning" | "running" | "awaiting_approval" | "awaiting_confirmation" | "paused" | "done" | "failed";
export interface Request { id: string; text: string; status: RequestStatus; created_at: string; report_id: string | null; cost_usd: number }
export interface Metrics {
  tasks_active: number; tasks_done: number; approvals_pending: number;
  cost_usd: number; budget_usd: number; errors: number; requests_total: number;
}
export interface PlanTask { id: string; title: string; agent_id: string; depends_on: string[] }
export interface MemoryItem { key: string; value: string; scope: string }
export interface AgentDetail {
  agent: Agent; tasks: Task[]; conversations: Conversation[]; memory: MemoryItem[]; recent_activity: ActivityItem[];
}
export interface WsFrame {
  id: string; type: string; ts: string; org_id?: string; agent_id?: string; payload: any;
}
export interface ErrorItem { id: string; ts: string; agent_id: string | null; message: string }

/** Chat routing contract (docs/architecture/chat-routing.md): conversation is "office" or "agent:<id>". */
export type ChatConversationId = string;
/** A drive file a chat message points to (never content): what an agent produced or the user attached. */
export interface FileRef {
  file_id?: string; path: string; name: string; mime?: string; size?: number; version?: number;
  /** write: produced by an agent; attach: attached by the user */
  action?: "write" | "attach" | string;
}
export interface ChatMessage {
  id: string; conversation: ChatConversationId; turn_id: string | null; from: string; to: string;
  kind: MessageKind; text: string; reply_to: string | null; ts: string;
  /** optional: the agent redirects the topic to this colleague (offers a quick action) */
  handoff_to?: string | null;
  /** drive files of this message (cards in the chat) */
  files?: FileRef[];
}
export type TurnIntent = "smalltalk" | "question" | "task";
export interface RouteResponder { agent_id: string; role: "primary" | "contributor"; reason: string }
export interface RouteDecision {
  turn_id: string; intent: TurnIntent; topic: string; responders: RouteResponder[]; ts: string;
  /** llm | rules | local | handoff */ source?: string;
  /** topic of another area: who it would be handed to (questions) / reassigned to (tasks) */
  consult?: { agent_id: string; reason?: string } | null;
}
