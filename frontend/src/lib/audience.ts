"use client";
import { useEffect } from "react";
import { create } from "zustand";
import { MOCK } from "./config";
import { teamsApi } from "./teamsApi";
import type { Audience } from "./teamsLogic";
import { audienceKey, audienceOf, initialAudience, storedAudience } from "./audienceLogic";
import { useSession } from "./session";

/**
 * The organization's audience ("solo" | "team" | "company") from GET /me/welcome, pinned to the current user + organization:
 * it reloads when either changes (sign-out/in, switch org). The last known value is kept per organization in localStorage so
 * the menu does not flicker on reload; /me/welcome confirms or corrects it. null (no value / failure) = today's menu.
 * Only NEXT_PUBLIC_MOCK=true shows the company menu on its own (see audienceLogic.ts).
 */
interface AudienceState { audience: Audience | null; key: string | null; bind: (uid: string | null, orgId: string | null) => void }

const read = (k: string) => { try { return localStorage.getItem(k); } catch { return null; } };
const write = (k: string, v: string | null) => {
  try { if (v) localStorage.setItem(k, v); else localStorage.removeItem(k); } catch { /* private mode */ }
};

export const useAudienceStore = create<AudienceState>((set, get) => ({
  audience: initialAudience(null, MOCK),
  key: null,
  bind: (uid, orgId) => {
    const key = uid && orgId ? `${uid}|${orgId}` : null;
    if (key === get().key) return;
    set({ key, audience: key ? initialAudience(read(audienceKey(orgId!)), MOCK) : initialAudience(null, MOCK) });
    if (!key || MOCK) return;
    teamsApi.welcome().then((w) => {
      if (get().key !== key) return; // the person changed org/user meanwhile
      const a = audienceOf(w, MOCK);
      set({ audience: a });
      write(audienceKey(orgId!), storedAudience(a)); // no audience any more: the old saved value goes too
    }).catch(() => { /* keep what we had */ });
  },
}));

export function useAudience(): Audience | null {
  const audience = useAudienceStore((s) => s.audience);
  const uid = useSession((s) => s.user?.id ?? null);
  const orgId = useSession((s) => s.orgId);
  useEffect(() => { useAudienceStore.getState().bind(uid, orgId); }, [uid, orgId]);
  return audience;
}
