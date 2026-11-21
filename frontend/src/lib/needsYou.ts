"use client";
import { create } from "zustand";
import { call } from "./api";
import { withoutItem, type NeedsYouResult } from "./needsYouLogic";

/**
 * "Te necesitan" (docs/architecture/needs-you.md): the list and the badge of the menu. Both come from the server, which
 * applies every rule (departments, who decides). The store reloads them when a live event that matters arrives
 * (store.ts calls refreshNeedsYou; needsYouLogic.isNeedsYouFrame), and every couple of minutes as a safety net.
 */
export const needsYouApi = {
  list: (limit = 200) => call<NeedsYouResult>("GET", `/needs-you?limit=${limit}`),
  count: () => call<NeedsYouResult>("GET", "/needs-you/count"),
};

interface NeedsYouState {
  count: number;
  result: NeedsYouResult | null;
  loading: boolean;
  error: boolean;
  /** how many screens show the list (the page): only then a live event reloads the whole list */
  watchers: number;
  loadCount: () => Promise<void>;
  load: () => Promise<void>;
  /** the person resolved an item here: it leaves at once, the server confirms on the next load */
  drop: (kind: string, id: string) => void;
  watch: () => () => void;
}

export const useNeedsYou = create<NeedsYouState>((set, get) => ({
  count: 0, result: null, loading: false, error: false, watchers: 0,
  loadCount: async () => {
    try { const r = await needsYouApi.count(); set({ count: r.total }); } catch { /* keep the last number */ }
  },
  load: async () => {
    set({ loading: true });
    try { const r = await needsYouApi.list(); set({ result: r, count: r.total, error: false, loading: false }); }
    catch { set({ error: true, loading: false }); }
  },
  drop: (kind, id) => set((s) => {
    if (!s.result) return { count: Math.max(0, s.count - 1) };
    const result = withoutItem(s.result, kind, id);
    return { result, count: result.total };
  }),
  watch: () => {
    set((s) => ({ watchers: s.watchers + 1 }));
    void get().load();
    return () => set((s) => ({ watchers: Math.max(0, s.watchers - 1) }));
  },
}));

let timer: ReturnType<typeof setTimeout> | null = null;

/** A live event changed what waits for somebody: reload once the burst settles (several events come together). */
export function refreshNeedsYou(delay = 500) {
  if (timer) clearTimeout(timer);
  timer = setTimeout(() => {
    timer = null;
    const s = useNeedsYou.getState();
    void (s.watchers > 0 ? s.load() : s.loadCount());
  }, delay);
}
