"use client";
import { useEffect } from "react";
import { create } from "zustand";
import { MOCK } from "./config";
import { teamsApi } from "./teamsApi";
import type { Audience } from "./teamsLogic";
import { audienceOf } from "./audienceLogic";

/**
 * The organization's audience ("solo" | "team" | "company"), loaded once per session from GET /me/welcome.
 * null while loading or if it fails: the menu of today. Only NEXT_PUBLIC_MOCK=true shows the company menu on its own (see audienceLogic.ts).
 */
interface AudienceState { audience: Audience | null; started: boolean; load: () => void }

export const useAudienceStore = create<AudienceState>((set, get) => ({
  audience: audienceOf(null, MOCK),
  started: MOCK,
  load: () => {
    if (get().started) return;
    set({ started: true });
    teamsApi.welcome().then((w) => set({ audience: audienceOf(w, MOCK) })).catch(() => { /* keep today's menu */ });
  },
}));

export function useAudience(): Audience | null {
  const audience = useAudienceStore((s) => s.audience);
  const load = useAudienceStore((s) => s.load);
  useEffect(() => { load(); }, [load]);
  return audience;
}
