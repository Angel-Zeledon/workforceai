import { create } from "zustand";
import { api, toChatMessage } from "./api";
import { registerRoleTemplates } from "./meta";
import { getLocale } from "./i18n-core";
import { useArtifacts } from "./artifacts";
import { isCostFrame, useCost } from "./cost";
import { isConnectionsFrame, useConnections } from "./connections/store";
import type {
  ActivityItem, Agent, Approval, ChatMessage, Conversation, ErrorItem, Message, Metrics, PlanTask,
  Report, Request, RouteDecision, Task, WsFrame,
} from "./types";

export interface Link { id: string; from: string; to: string; kind: string; text: string; ts: number; /** from chat.message: bubble only, no 3D line */ chat?: boolean }

interface State {
  connected: boolean;
  loaded: boolean;
  mode: "office" | "dashboard";
  selectedAgentId: string | null;
  agents: Record<string, Agent>;
  agentOrder: string[];
  tasks: Record<string, Task>;
  requests: Record<string, Request>;
  plans: Record<string, PlanTask[]>;
  conversations: Record<string, Conversation>;
  messages: Record<string, Message[]>;
  /** 1:1 and office chat, keyed by "office" | "agent:<id>" */
  chat: Record<string, ChatMessage[]>;
  /** routing decisions by turn_id (who answers and why) */
  routes: Record<string, RouteDecision>;
  lastTurnId: string | null;
  /** text prefilled in a chat input (e.g. when jumping to a colleague's 1:1 chat with context) */
  drafts: Record<string, string>;
  setDraft: (conv: string, text: string) => void;
  /** who is typing per conversation: agent_id -> start ms (auto-expires) */
  typing: Record<string, Record<string, number>>;
  approvals: Record<string, Approval>;
  reports: Record<string, Report>;
  activity: ActivityItem[];
  errors: ErrorItem[];
  metrics: Metrics | null;
  /** recent messages (agent<->agent, user<->agent) used by the 3D scene to draw lines/bubbles and to trigger consult walks */
  links: Link[];
  /** bumps on events relevant to an agent, used to refresh its detail panel */
  agentTick: Record<string, number>;
  setMode: (m: "office" | "dashboard") => void;
  select: (id: string | null) => void;
  setConnected: (c: boolean) => void;
  loadAll: () => Promise<void>;
  loadMessages: (convId: string) => Promise<void>;
  loadChat: (conv: string) => Promise<void>;
  /** optimistic local echo of the user's own message until the backend echoes it */
  addLocalChat: (m: ChatMessage) => void;
  loadConversations: () => Promise<void>;
  apply: (f: WsFrame) => void;
}

function byId<T extends { id: string }>(arr: T[]): Record<string, T> {
  return Object.fromEntries(arr.map((x) => [x.id, x]));
}
const TERMINAL = ["done", "failed", "blocked"];

function deriveRequest(requestId: string, tasks: Record<string, Task>, reqs: Record<string, Request>) {
  const r = reqs[requestId];
  if (!r || r.status === "done") return reqs;
  const ts = Object.values(tasks).filter((t) => t.request_id === requestId);
  if (!ts.length) return reqs;
  let status = r.status;
  if (ts.some((t) => t.status === "awaiting_approval")) status = "awaiting_approval";
  else if (ts.every((t) => TERMINAL.includes(t.status)) && ts.some((t) => t.status === "failed" || t.status === "blocked")) status = "failed";
  else if (status === "planning" || status === "awaiting_approval") status = "running";
  return status === r.status ? reqs : { ...reqs, [requestId]: { ...r, status } };
}

const TYPING_TTL = 20000;
/** Merges chat messages by id, then drops the local optimistic echo once the real one (same text, same sender) arrived. */
function mergeChat(a: ChatMessage[], b: ChatMessage[]): ChatMessage[] {
  const map = new Map<string, ChatMessage>();
  [...a, ...b].forEach((m) => map.set(m.id, m));
  const all = Array.from(map.values()).sort((x, y) => +new Date(x.ts) - +new Date(y.ts));
  return all.filter((m) => !(m.id.startsWith("local:") && all.some((o) => o !== m && !o.id.startsWith("local:") && o.from === m.from && o.text === m.text && o.turn_id)));
}

let convTimer: ReturnType<typeof setTimeout> | null = null;

export const useStore = create<State>((set, get) => ({
  connected: false, loaded: false, mode: "office", selectedAgentId: null,
  agents: {}, agentOrder: [], tasks: {}, requests: {}, plans: {}, conversations: {}, messages: {}, chat: {}, routes: {}, lastTurnId: null, typing: {}, drafts: {},
  setDraft: (conv, text) => set((s) => ({ drafts: { ...s.drafts, [conv]: text } })),
  approvals: {}, reports: {}, activity: [], errors: [], metrics: null, links: [], agentTick: {},

  setMode: (mode) => set({ mode }),
  select: (selectedAgentId) => set({ selectedAgentId }),
  setConnected: (connected) => set({ connected }),

  loadAll: async () => {
    async function safe<T>(p: Promise<T>, d: T): Promise<T> {
      try { return await p; } catch { return d; }
    }
    // Look of template-based roles (best effort) must be known before their agents render.
    await safe(api.roleTemplates(getLocale()).then(registerRoleTemplates), undefined);
    const [agents, tasks, requests, conversations, approvals, reports, activity, metrics] = await Promise.all([
      safe(api.agents(), [] as Agent[]), safe(api.tasks(), [] as Task[]), safe(api.requests(), [] as Request[]),
      safe(api.conversations(), [] as Conversation[]), safe(api.approvals(), [] as Approval[]),
      safe(api.reports(), [] as Report[]), safe(api.activity(), [] as ActivityItem[]), safe(api.metrics(), null as Metrics | null),
    ]);
    set((s) => ({
      loaded: true,
      agents: agents.length ? byId(agents) : s.agents,
      agentOrder: agents.length ? agents.map((a) => a.id) : s.agentOrder,
      tasks: { ...s.tasks, ...byId(tasks) },
      requests: { ...s.requests, ...byId(requests) },
      conversations: { ...s.conversations, ...byId(conversations) },
      approvals: { ...s.approvals, ...byId(approvals) },
      reports: { ...s.reports, ...byId(reports) },
      activity: activity.length ? [...activity].sort((a, b) => +new Date(b.ts) - +new Date(a.ts)) : s.activity,
      metrics: metrics ?? s.metrics,
    }));
  },
  loadConversations: async () => {
    try { const c = await api.conversations(); set((s) => ({ conversations: { ...s.conversations, ...byId(c) } })); } catch { /* ignore */ }
  },
  loadMessages: async (convId) => {
    try {
      const m = await api.messages(convId);
      set((s) => {
        const merged = byId([...m, ...(s.messages[convId] || [])]);
        return { messages: { ...s.messages, [convId]: Object.values(merged).sort((a, b) => +new Date(a.ts) - +new Date(b.ts)) } };
      });
    } catch { /* ignore */ }
  },

  loadChat: async (conv) => {
    try {
      const m = await api.chatHistory(conv);
      set((s) => ({ chat: { ...s.chat, [conv]: mergeChat(s.chat[conv] || [], m) } }));
    } catch { /* ignore */ }
  },
  addLocalChat: (m) => set((s) => ({ chat: { ...s.chat, [m.conversation]: mergeChat(s.chat[m.conversation] || [], [m]) } })),

  apply: (f) => {
    if (f.type.startsWith("artifact.")) { useArtifacts.getState().applyFrame(f); return; }
    if (isConnectionsFrame(f.type)) { useConnections.getState().applyFrame(f); return; }
    if (f.type === "hello") { useArtifacts.getState().load(); useConnections.getState().applyFrame(f); }
    if (isCostFrame(f.type)) useCost.getState().applyFrame(f);
    const p = f.payload || {};
    const tick = (s: State, ids: (string | null | undefined)[]) => {
      const t = { ...s.agentTick };
      ids.forEach((i) => { if (i) t[i] = (t[i] || 0) + 1; });
      return t;
    };
    switch (f.type) {
      case "hello": {
        const agents: Agent[] = p.agents || [];
        set({ agents: byId(agents), agentOrder: agents.map((a) => a.id) });
        break;
      }
      case "request.received":
        set((s) => ({
          requests: {
            ...s.requests,
            [p.request_id]: s.requests[p.request_id] || { id: p.request_id, text: p.text, status: "planning", created_at: f.ts, report_id: null, cost_usd: 0 },
          },
        }));
        break;
      case "request.completed":
        set((s) => {
          const r = s.requests[p.request_id];
          return r ? { requests: { ...s.requests, [p.request_id]: { ...r, status: "done" as const, report_id: p.report_id ?? r.report_id } } } : {};
        });
        break;
      case "request.status_changed":
        // cost control: awaiting_confirmation / paused and the way back to running
        set((s) => {
          const r = s.requests[p.request_id];
          return r && p.status ? { requests: { ...s.requests, [p.request_id]: { ...r, status: p.status } } } : {};
        });
        break;
      case "plan.created":
        set((s) => {
          const reqs = s.requests[p.request_id]
            ? { ...s.requests, [p.request_id]: { ...s.requests[p.request_id], status: "running" as const } }
            : s.requests;
          return { plans: { ...s.plans, [p.request_id]: p.tasks || [] }, requests: reqs };
        });
        break;
      case "agent.state_changed": {
        const id: string = p.agent_id || f.agent_id || "";
        set((s) => {
          const a = s.agents[id];
          if (!a) return {};
          return {
            agents: { ...s.agents, [id]: { ...a, state: p.state, activity: p.activity ?? "", current_task_id: p.task_id ?? null, progress: p.progress ?? 0 } },
            agentTick: tick(s, [id]),
          };
        });
        break;
      }
      case "task.created": case "task.started": case "task.completed": case "task.failed": case "task.blocked": {
        const t0: Task = p.task;
        if (!t0) break;
        set((s) => {
          const reason = t0.assigned_reason ?? (f.type === "task.created" ? p.assigned_reason : undefined) ?? s.tasks[t0.id]?.assigned_reason;
          const t: Task = reason ? { ...t0, assigned_reason: reason } : t0;
          const tasks = { ...s.tasks, [t.id]: t };
          return { tasks, requests: deriveRequest(t.request_id, tasks, s.requests), agentTick: tick(s, [t.agent_id]) };
        });
        break;
      }
      case "message.sent": {
        const m: Message = p.message;
        if (!m) break;
        const known = !!get().conversations[m.conversation_id];
        // the user's own request text is already visible in the office chat: no duplicate bubble
        const echoed = m.from === "user" && (get().chat.office || []).some((c) => c.from === "user" && c.text === m.text);
        set((s) => {
          const conv = s.conversations[m.conversation_id] || {
            id: m.conversation_id, title: "Conversación", request_id: null, last_message_at: m.ts,
            participants: [m.from, m.to].filter((x) => x !== "user" && x !== "all" && x !== "system"),
          };
          const list = s.messages[m.conversation_id] || [];
          const now = Date.now();
          const links = echoed ? s.links : [...s.links.filter((l) => now - l.ts < 15000), { id: m.id, from: m.from, to: m.to, kind: m.kind, text: m.text, ts: now }];
          return {
            conversations: { ...s.conversations, [conv.id]: { ...conv, last_message_at: m.ts } },
            messages: list.some((x) => x.id === m.id) ? s.messages : { ...s.messages, [m.conversation_id]: [...list, m] },
            links,
            agentTick: tick(s, [m.from, m.to]),
          };
        });
        if (!known && !convTimer) {
          convTimer = setTimeout(() => { convTimer = null; get().loadConversations(); }, 600);
        }
        break;
      }
      case "chat.message": {
        const m = toChatMessage({ ...p, id: p.id ?? p.message_id ?? f.id, ts: p.ts ?? f.ts }, p.conversation ?? "office");
        const typingOff = (s: State) => {
          const cur = s.typing[m.conversation];
          if (!cur || !(m.from in cur)) return s.typing;
          const { [m.from]: _gone, ...rest } = cur;
          return { ...s.typing, [m.conversation]: rest };
        };
        set((s) => {
          const speaker = !["user", "all", "system"].includes(m.from);
          const now = Date.now();
          const links = speaker
            ? [...s.links.filter((l) => now - l.ts < 15000), { id: m.id, from: m.from, to: m.to, kind: m.kind === "system" ? "chat" : m.kind, text: m.text, ts: now, chat: true }]
            : s.links;
          return {
            chat: { ...s.chat, [m.conversation]: mergeChat(s.chat[m.conversation] || [], [m]) },
            typing: typingOff(s), links, agentTick: tick(s, [m.from, m.to]),
          };
        });
        break;
      }
      case "chat.typing":
        set((s) => {
          const conv: string = p.conversation, id: string = p.agent_id;
          if (!conv || !id) return {};
          const cur = { ...(s.typing[conv] || {}) };
          if (p.on) cur[id] = Date.now(); else delete cur[id];
          return { typing: { ...s.typing, [conv]: cur } };
        });
        break;
      case "route.decided": {
        const r: RouteDecision = { turn_id: p.turn_id, intent: p.intent, topic: p.topic ?? "", responders: p.responders || [], ts: f.ts, source: p.source, consult: p.consult ?? null };
        if (r.turn_id) set((s) => ({ routes: { ...s.routes, [r.turn_id]: r }, lastTurnId: r.turn_id }));
        break;
      }
      case "approval.requested": case "approval.resolved": {
        const a: Approval = p.approval;
        if (!a) break;
        set((s) => {
          const approvals = { ...s.approvals, [a.id]: a };
          const task = s.tasks[a.task_id];
          const requests = task ? deriveRequest(task.request_id, s.tasks, s.requests) : s.requests;
          return { approvals, requests, agentTick: tick(s, [a.agent_id]) };
        });
        break;
      }
      case "report.created": {
        const r: Report = p.report;
        if (r) set((s) => ({ reports: { ...s.reports, [r.id]: r } }));
        break;
      }
      case "activity.logged": {
        const it: ActivityItem = p.item;
        if (it) set((s) => (s.activity.some((x) => x.id === it.id) ? {} : { activity: [it, ...s.activity].slice(0, 300), agentTick: tick(s, [it.agent_id]) }));
        break;
      }
      case "error":
        set((s) => ({
          errors: [{ id: f.id, ts: f.ts, agent_id: p.agent_id ?? f.agent_id ?? null, message: p.message || "Error" }, ...s.errors].slice(0, 100),
        }));
        break;
      case "metrics.updated":
        if (p.metrics) set({ metrics: p.metrics });
        break;
    }
  },
}));

/** Selector helper: agents currently typing in a conversation (ignores stale entries). */
export function typingIn(typing: Record<string, Record<string, number>>, conv: string): string[] {
  const now = Date.now();
  return Object.entries(typing[conv] || {}).filter(([, ts]) => now - ts < TYPING_TTL).map(([id]) => id);
}
