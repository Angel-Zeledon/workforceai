"use client";
import { useEffect } from "react";
import { create } from "zustand";
import { MOCK } from "./config";
import { teamsApi } from "./teamsApi";
import type { Audience } from "./teamsLogic";

/**
 * The organization's audience ("solo" | "team" | "company"), loaded once per session from GET /me/welcome.
 * null while loading or if it fails: the menu of today. The demo (MOCK) has no welcome, so it shows the company menu.
 */
interface AudienceState { audience: Audience | null; started: boolean; load: () => void }

export const useAudienceStore = create<AudienceState>((set, get) => ({
  audience: MOCK ? "company" : null,
  started: MOCK,
  load: () => {
    if (get().started) return;
    set({ started: true });
    teamsApi.welcome().then((w) => { if (w && w.audience) set({ audience: w.audience }); }).catch(() => { /* keep today's menu */ });
  },
}));

export function useAudience(): Audience | null {
  const audience = useAudienceStore((s) => s.audience);
  const load = useAudienceStore((s) => s.load);
  useEffect(() => { load(); }, [load]);
  return audience;
}
