"use client";
import { create } from "zustand";
import { call } from "./api";
import { alertFromAudit, alertFromFrame, mergeAlerts, type ShieldAlert } from "./shieldLogic";
import type { WsFrame } from "./types";

/**
 * Visible shield (B10, docs/architecture/shield.md). A security.alert of kind prompt_injection_suspected means content an
 * agent read (an email, a page, a file) looked like it was giving the agent orders. The backend already treated it as
 * data and made every write of that task wait for a human; this store makes it visible: a shield over the agent's
 * desk (office/ShieldFloats), a notice with a link to the task's thought audit (security/ShieldAlerts) and the
 * security log (security/ShieldLogCard), read from the audit trail. Ids only: the third-party text never gets here.
 */

export const isShieldFrame = (type: string) => type === "security.alert";

/** How long the shield stays over the desk and the notice stays on screen. */
export const SHIELD_FLASH_MS = 6500;
export const SHIELD_NOTICE_MS = 14000;

export type ShieldLogState = "idle" | "loading" | "ok" | "restricted" | "error";

interface State {
  /** notices on screen (newest first, at most 3) */
  notices: ShieldAlert[];
  /** shields over desks: alert id -> agent and start time */
  flashes: { id: string; agent_id: string; at: number }[];
  /** the security log (audit trail, newest first) */
  log: ShieldAlert[];
  logState: ShieldLogState;
  applyFrame: (f: WsFrame) => void;
  dismiss: (id: string) => void;
  loadLog: () => Promise<void>;
}

export const useShield = create<State>((set, get) => ({
  notices: [], flashes: [], log: [], logState: "idle",
  applyFrame: (f) => {
    const a = alertFromFrame(f);
    if (!a) return;
    if (get().notices.some((n) => n.id === a.id)) return;
    const at = Date.now();
    set((s) => ({
      notices: [a, ...s.notices].slice(0, 3),
      flashes: [...s.flashes.filter((x) => at - x.at < SHIELD_FLASH_MS), { id: a.id, agent_id: a.agent_id, at }],
      log: mergeAlerts(s.log, [a]),
    }));
    setTimeout(() => set((s) => ({ flashes: s.flashes.filter((x) => x.id !== a.id) })), SHIELD_FLASH_MS + 200);
    setTimeout(() => get().dismiss(a.id), SHIELD_NOTICE_MS);
  },
  dismiss: (id) => set((s) => ({ notices: s.notices.filter((n) => n.id !== id) })),
  loadLog: async () => {
    set({ logState: "loading" });
    try {
      const r = await call<{ items?: unknown[] }>("GET", "/audit?type=security.injection_suspected&limit=50");
      const items = (Array.isArray(r?.items) ? r.items : []).map((x) => alertFromAudit(x as any)).filter((x): x is ShieldAlert => !!x);
      // the audit trail is the record; live alerts not yet listed (other replica, race) stay until it catches up
      set((s) => ({ log: mergeAlerts(s.log.filter((l) => !items.some((i) => i.task_id === l.task_id && i.agent_id === l.agent_id)), items), logState: "ok" }));
    } catch (e) {
      set({ logState: /-> 403$/.test(String((e as Error)?.message ?? "")) ? "restricted" : "error" });
    }
  },
}));

/** Demo only (NEXT_PUBLIC_MOCK=true): the mock backend plays a read whose content tries to give an agent orders. */
export const simulateInjection = () => call("POST", "/demo/security-alert", {});
