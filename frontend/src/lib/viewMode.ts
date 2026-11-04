"use client";
import { create } from "zustand";
import { resolveViewAll, viewAllKey } from "./viewModeLogic";

/**
 * "Ver todo": whether the advanced views open by default (see lib/viewModeLogic.ts for who starts simple).
 * The person's choice is stored per user on this device; the server default comes from GET /org/settings.
 */
interface ViewModeState {
  uid: string | null;
  serverDefault: string | null;
  viewAll: boolean;
  /** called by the shell with the signed-in user and the organization's default */
  init: (uid: string | null, serverDefault?: string | null) => void;
  setViewAll: (v: boolean) => void;
}

const read = (k: string) => { try { return localStorage.getItem(k); } catch { return null; } };

export const useViewMode = create<ViewModeState>((set, get) => ({
  uid: null,
  serverDefault: null,
  viewAll: true, // until we know, nothing is hidden (the same as before this change)
  init: (uid, serverDefault) => {
    const sd = serverDefault === undefined ? get().serverDefault : serverDefault;
    set({ uid, serverDefault: sd, viewAll: resolveViewAll(read(viewAllKey(uid)), sd) });
  },
  setViewAll: (v) => {
    try { localStorage.setItem(viewAllKey(get().uid), v ? "1" : "0"); } catch { /* private mode: only this session */ }
    set({ viewAll: v });
  },
}));

export const useViewAll = () => useViewMode((s) => s.viewAll);
