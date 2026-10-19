"use client";
import { create } from "zustand";
import { MOCK } from "@/lib/config";
import { call } from "@/lib/api";
import { useSession } from "@/lib/session";
import { SPAWN, clampToFloor, isStatus, type PStatus, type Pt } from "./logic";

/** A person online in the office, as the server announces them (see backend/internal/presence). */
export interface Person {
  user_id: string; name: string; status: PStatus; x: number; z: number; zone?: string; since?: string; auto_away?: boolean;
}

interface PresenceState {
  /** People online, keyed by user id, self included. */
  people: Record<string, Person>;
  selfId: string | null;
  /** True once the server answered GET /presence (live presence available). */
  live: boolean;
  /** Where the avatar of each OTHER person is heading (smoothed by the scene); self is driven locally. */
  load: () => Promise<void>;
  apply: (type: string, payload: any) => void;
  /** Optimistic local update of my own position (the scene moves me immediately; the server confirms by echo). */
  moveSelf: (p: Pt, zone?: string) => void;
  setMyStatus: (s: PStatus) => Promise<void>;
  reset: () => void;
}

const SELF_LOCAL = "local";

export const usePresence = create<PresenceState>((set, get) => ({
  people: {}, selfId: null, live: false,
  load: async () => {
    if (MOCK) {
      // no backend in the mock: the visitor is alone in the office, so the avatar and the status menu still work
      const u = useSession.getState().user;
      set({ selfId: SELF_LOCAL, live: false, people: { [SELF_LOCAL]: { user_id: SELF_LOCAL, name: u?.name || "You", status: "available", x: SPAWN.x, z: SPAWN.z } } });
      return;
    }
    try {
      const r = await call<{ people: Person[]; self: string }>("GET", "/presence");
      const people: Record<string, Person> = {};
      for (const p of r.people ?? []) people[p.user_id] = p;
      set({ people, selfId: r.self, live: true });
    } catch { set({ live: false }); }
  },
  apply: (type, p) => {
    if (!p) return;
    const id: string | undefined = p.user_id;
    switch (type) {
      case "presence.joined": if (id) set((s) => ({ people: { ...s.people, [id]: p as Person } })); break;
      case "presence.left": if (id) set((s) => { const { [id]: _gone, ...rest } = s.people; return { people: rest }; }); break;
      case "presence.moved":
        // my own echo is ignored: I am the source of truth for where I stand
        if (id && id !== get().selfId) set((s) => (s.people[id] ? { people: { ...s.people, [id]: { ...s.people[id], x: p.x, z: p.z, zone: p.zone } } } : s));
        break;
      case "presence.status":
        if (id && isStatus(p.status)) set((s) => (s.people[id] ? { people: { ...s.people, [id]: { ...s.people[id], status: p.status, auto_away: !!p.auto_away } } } : s));
        break;
    }
  },
  moveSelf: (pt, zone) => {
    const me = get().selfId;
    if (!me) return;
    const c = clampToFloor(pt);
    set((s) => (s.people[me] ? { people: { ...s.people, [me]: { ...s.people[me], x: c.x, z: c.z, zone: zone ?? s.people[me].zone } } } : s));
  },
  setMyStatus: async (st) => {
    const me = get().selfId;
    if (!me) return;
    const prev = get().people[me]?.status;
    set((s) => (s.people[me] ? { people: { ...s.people, [me]: { ...s.people[me], status: st, auto_away: false } } } : s));
    if (MOCK) return;
    try { await call("PUT", "/presence/status", { status: st }); }
    catch { if (prev) set((s) => (s.people[me] ? { people: { ...s.people, [me]: { ...s.people[me], status: prev } } } : s)); }
  },
  reset: () => set({ people: {}, selfId: null, live: false }),
}));

/** Other people (not me), ordered by name. */
export const othersOf = (s: { people: Record<string, Person>; selfId: string | null }) =>
  Object.values(s.people).filter((p) => p.user_id !== s.selfId).sort((a, b) => a.name.localeCompare(b.name));
