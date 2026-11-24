"use client";
import { useEffect, useMemo } from "react";
import { create } from "zustand";
import { officesApi } from "./officesApi";
import type { OfficeView } from "./officesLogic";
import { useOfficeLayout } from "./officeLayout";
import { useSession } from "./session";
import { useAgentOffices } from "./useAgentOffices";
import { useStore } from "./store";
import { ALL_OFFICES, currentOfficeKey, filterAgentIds, officeChoices, officeOf, resolveCurrent, type AlsoIn } from "./currentOfficeLogic";

/** The office the person is in, per person and device. Pure rules: currentOfficeLogic.ts. */
interface State {
  stored: string; offices: OfficeView[] | null; bases: Record<string, string>; userId: string | null;
  boot: (userId: string | null) => void; refresh: () => Promise<void>; choose: (id: string) => void;
}

const read = (k: string): string => { try { return window.localStorage.getItem(k) ?? ""; } catch { return ""; } };
const write = (k: string, v: string) => { try { window.localStorage.setItem(k, v); } catch { /* private mode */ } };

/** Points the 3D (and its editor rights) at an office. "all" shows General's room. */
function pointLayout(id: string, offices: OfficeView[] | null) {
  const target = id === ALL_OFFICES ? "general" : id;
  const o = offices?.find((x) => x.id === target);
  useOfficeLayout.setState({ officeEditable: !o || o.general || o.can_manage });
  void useOfficeLayout.getState().setOffice(target);
}

export const useCurrentOfficeStore = create<State>((set, get) => ({
  stored: ALL_OFFICES, offices: null, bases: {}, userId: null,
  boot: (userId) => {
    if (get().userId === userId && get().offices) return;
    set({ userId, stored: read(currentOfficeKey(userId)) || ALL_OFFICES });
    void get().refresh();
  },
  refresh: async () => {
    try {
      const [list, dir] = await Promise.all([officesApi.list(), officesApi.directory().catch(() => ({ agents: [] }))]);
      const before = resolveCurrent(get().stored, get().offices);
      const bases: Record<string, string> = {};
      for (const a of dir.agents) bases[a.id] = officeOf(a.office_id);
      set({ offices: list.offices, bases });
      const now = resolveCurrent(get().stored, list.offices);
      // the office vanished or the person lost access: back to "all", and the 3D follows
      if (now !== before || (now !== ALL_OFFICES && useOfficeLayout.getState().officeId !== (now === "general" ? "" : now))) pointLayout(now, list.offices);
    } catch { /* an older backend or offline: one office, no selector */ }
  },
  choose: (id) => {
    const next = resolveCurrent(id, get().offices);
    set({ stored: next });
    write(currentOfficeKey(get().userId), next);
    pointLayout(next, get().offices);
  },
}));

/** Loads the offices once the session is known; mount it once in the shell. */
export function useCurrentOfficeBoot() {
  const uid = useSession((s) => s.user?.id ?? null);
  useEffect(() => {
    useCurrentOfficeStore.getState().boot(uid);
    const onVisible = () => { if (document.visibilityState === "visible") void useCurrentOfficeStore.getState().refresh(); };
    document.addEventListener("visibilitychange", onVisible);
    return () => document.removeEventListener("visibilitychange", onVisible);
  }, [uid]);
}

/** The current office (`"all"` or an id), the list of offices and what belongs to it. */
export function useCurrentOffice() {
  const stored = useCurrentOfficeStore((s) => s.stored);
  const offices = useCurrentOfficeStore((s) => s.offices);
  const bases = useCurrentOfficeStore((s) => s.bases);
  const choose = useCurrentOfficeStore((s) => s.choose);
  const current = useMemo(() => resolveCurrent(stored, offices), [stored, offices]);
  const choices = useMemo(() => officeChoices(offices), [offices]);
  const office = current === ALL_OFFICES ? null : offices?.find((o) => o.id === current) ?? null;
  return { current, offices, choices, office, bases, choose, hasSelector: !!choices, all: current === ALL_OFFICES };
}

/** Agents of the current office (base or accepted "also works in"), in the order of the office. Everything in "all". */
export function useOfficeAgentIds(): { ids: string[]; alsoIn: Record<string, AlsoIn[]>; current: string } {
  const { current, bases, hasSelector } = useCurrentOffice();
  const order = useStore((s) => s.agentOrder);
  const scoped = hasSelector && current !== ALL_OFFICES;
  const { enabled, views } = useAgentOffices(scoped ? order : []);
  const alsoIn = useMemo(() => {
    const out: Record<string, AlsoIn[]> = {};
    if (enabled) for (const [id, v] of Object.entries(views)) out[id] = v.offices.map((r) => ({ id: r.office_id, pending: r.status === "pending" }));
    return out;
  }, [enabled, views]);
  const ids = useMemo(() => (scoped ? filterAgentIds(order, current, (id) => bases[id], alsoIn) : order), [scoped, order, current, bases, alsoIn]);
  return { ids, alsoIn, current };
}
