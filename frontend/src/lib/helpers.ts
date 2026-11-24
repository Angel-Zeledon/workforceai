import { create } from "zustand";
import { call } from "./api";
import { MOCK } from "./config";
import { mergeGroup } from "./helpersLogic";
import type { HelperGroup, WsFrame } from "./types";

/**
 * Temporary helpers: the groups by id and the `helpers_enabled` flag. With the flag off or absent nothing changes in the
 * interface (the server keeps it off until it is tested); the demo has it on.
 */
interface HelpersState {
  enabled: boolean;
  groups: Record<string, HelperGroup>;
  /** The composer is aimed at "Ayudantes (sin puesto)": the next message to the office asks for helpers. */
  free: boolean;
  setFree: (v: boolean) => void;
  setEnabled: (v: boolean) => void;
  upsert: (g: HelperGroup | undefined | null) => void;
  applyFrame: (f: WsFrame) => void;
  /** GET /requests/{id} carries `helper_group`: fills the group of a request the page did not hear about live. */
  loadFor: (requestId: string) => Promise<void>;
}

export const useHelpers = create<HelpersState>((set, get) => ({
  enabled: MOCK,
  groups: {},
  free: false,
  setFree: (free) => set({ free }),
  setEnabled: (enabled) => set({ enabled }),
  upsert: (g) => { if (g?.id) set((s) => ({ groups: { ...s.groups, [g.id]: mergeGroup(s.groups[g.id], g) } })); },
  applyFrame: (f) => {
    const p = f.payload ?? {};
    if (f.type === "hello") {
      if (typeof p.helpers_enabled === "boolean") get().setEnabled(p.helpers_enabled);
      const list: HelperGroup[] = Array.isArray(p.helper_groups) ? p.helper_groups : [];
      for (const g of list) get().upsert(g);
      return;
    }
    if (f.type === "helpers.proposed" || f.type === "helpers.updated") get().upsert(p.group);
  },
  loadFor: async (requestId) => {
    try { const r = await call<{ helper_group?: HelperGroup | null }>("GET", `/requests/${encodeURIComponent(requestId)}`); get().upsert(r?.helper_group); } catch { /* the backend may not offer helpers */ }
  },
}));

export const isHelpersFrame = (type: string) => type === "helpers.proposed" || type === "helpers.updated";

/** The calls of the contract (docs/.../ayudantes-temporales-design.md §6). The group comes back through helpers.updated. */
export const helpersApi = {
  launch: (requestId: string, count: number, capUsd: number) =>
    call<{ group?: HelperGroup }>("POST", `/requests/${encodeURIComponent(requestId)}/helpers`, { count, cap_usd: capUsd }),
  decline: (requestId: string) => call<{ group?: HelperGroup }>("POST", `/requests/${encodeURIComponent(requestId)}/helpers/decline`, {}),
  /** `part` stops one part; without it, everything that has not finished. */
  stop: (requestId: string, part?: number) =>
    call<{ group?: HelperGroup }>("POST", `/requests/${encodeURIComponent(requestId)}/helpers/stop`, part == null ? {} : { part }),
  retry: (requestId: string, part: number) => call<{ group?: HelperGroup }>("POST", `/requests/${encodeURIComponent(requestId)}/helpers/retry`, { part }),
  /** No position: the office coordinator (role assistant) owns the helpers. */
  askWithoutPosition: (text: string, attachments?: string[]) =>
    call<{ message_id: string; turn_id: string }>("POST", "/messages", { conversation: "office", text, helpers: true, ...(attachments?.length ? { attachments } : {}) }),
};
