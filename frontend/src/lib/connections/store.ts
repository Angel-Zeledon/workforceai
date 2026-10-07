"use client";
import { create } from "zustand";
import type { WsFrame } from "../types";
import { connApi, ctlApi } from "./api";
import type {
  AgentControl, Connection, EmailDraft, Grant, OrgControls, PlanReview, ProviderDef, SpendLimitStatus, UsageRow,
} from "./types";

export type SecurityView = null | "connections" | "security";
export type ConnTab = "connections" | "matrix" | "usage" | "limits" | "capabilities";

/** Frames handled here (routed from store.ts the same way artifact.* frames are). */
export const isConnectionsFrame = (type: string) =>
  type.startsWith("connection.") || type.startsWith("control.") || type === "agent.control_changed"
  || type.startsWith("tool_call.") || type === "plan.review_requested" || type === "plan.approved";

// This store only ever holds metadata and state. Secrets are sent from the form straight to the API and never enter it.
interface State {
  view: SecurityView;
  tab: ConnTab;
  providers: ProviderDef[];
  connections: Record<string, Connection>;
  grants: Record<string, Grant[]>; // by connection id
  usage: UsageRow[];
  controls: OrgControls | null;
  agentControls: Record<string, AgentControl>;
  spend: SpendLimitStatus[];
  drafts: EmailDraft[];
  plans: PlanReview[];
  loaded: boolean;
  setView: (v: SecurityView) => void;
  setTab: (t: ConnTab) => void;
  load: () => Promise<void>;
  loadControls: () => Promise<void>;
  loadConnections: () => Promise<void>;
  loadUsage: () => Promise<void>;
  loadOutbox: () => Promise<void>;
  loadPlans: () => Promise<void>;
  applyFrame: (f: WsFrame) => void;
}

const safe = async <T,>(p: Promise<T>, d: T): Promise<T> => { try { return await p; } catch { return d; } };
const byId = <T extends { id: string }>(a: T[]) => Object.fromEntries(a.map((x) => [x.id, x]));

export const useConnections = create<State>((set, get) => ({
  view: null, tab: "connections", providers: [], connections: {}, grants: {}, usage: [], controls: null, agentControls: {}, spend: [], drafts: [], plans: [], loaded: false,
  setView: (view) => set({ view }),
  setTab: (tab) => set({ tab }),
  loadControls: async () => {
    const [controls, ac, spend] = await Promise.all([
      safe(ctlApi.controls(), null as OrgControls | null), safe(ctlApi.agentControls(), [] as AgentControl[]), safe(ctlApi.spend(), [] as SpendLimitStatus[]),
    ]);
    // Ignore responses that are not a real controls object (e.g. {} or an error body), so the UI never renders a bogus state.
    const valid = controls && typeof controls.kill_switch_level === "string" ? controls : null;
    set((s) => ({ controls: valid ?? s.controls, agentControls: Object.fromEntries(ac.map((a) => [a.agent_id, a])), spend }));
  },
  loadConnections: async () => {
    const [providers, list] = await Promise.all([safe(connApi.providers(), [] as ProviderDef[]), safe(connApi.list(), [] as Connection[])]);
    const grantLists = await Promise.all(list.map((c) => safe(connApi.grants(c.id), [] as Grant[])));
    set({ providers, connections: byId(list), grants: Object.fromEntries(list.map((c, i) => [c.id, grantLists[i]])) });
  },
  loadUsage: async () => {
    const list = Object.values(get().connections);
    const rows = (await Promise.all(list.map((c) => safe(connApi.usage(c.id), [] as UsageRow[])))).flat();
    set({ usage: rows.sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at)) });
  },
  loadOutbox: async () => set({ drafts: await safe(ctlApi.outbox(), [] as EmailDraft[]) }),
  loadPlans: async () => set({ plans: await safe(ctlApi.plans(), [] as PlanReview[]) }),
  load: async () => {
    await Promise.all([get().loadControls(), get().loadConnections(), get().loadOutbox(), get().loadPlans()]);
    await get().loadUsage();
    set({ loaded: true });
  },
  applyFrame: (f) => {
    const p = f.payload || {};
    switch (f.type) {
      case "hello": void get().load(); break; // demo reset or reconnect
      case "control.changed": if (p.controls) set({ controls: p.controls as OrgControls }); void get().loadControls(); break;
      case "agent.control_changed": void get().loadControls(); break;
      case "connection.created": case "connection.updated":
        if (p.connection) set((s) => ({ connections: { ...s.connections, [p.connection.id]: p.connection } }));
        void get().loadConnections(); break;
      case "connection.revoked": case "connection.grant_changed": void get().loadConnections(); void get().loadPlans(); break;
      case "connection.usage": void get().loadUsage(); break;
      case "tool_call.hold_changed":
        if (p.tool_call) {
          set((s) => ({ drafts: s.drafts.some((d) => d.id === p.tool_call.id) ? s.drafts.map((d) => (d.id === p.tool_call.id ? p.tool_call : d)) : [p.tool_call, ...s.drafts] }));
        }
        break;
      case "plan.review_requested": case "plan.approved": void get().loadPlans(); break;
    }
  },
}));
