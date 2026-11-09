import { call } from "./api";
import { API_URL, MOCK } from "./config";
import { bootstrapAuth } from "./session";
import type { Profession, Welcome } from "./teamsLogic";

/** Contract: backend/internal/auth/teams.go (sign-up check) and backend/internal/api/teams.go (welcome, teams). */

export interface CreatedTeam { team: { id: string; name: string }; profession: string; agents: string[] }
export interface TeamInvite { email: string; role: string; department_id?: string; profession?: string; lead?: boolean }

export const teamsApi = {
  /** Public: can this address enter its company (verified domain with auto-join on)? Never throws. */
  async signupCheck(email: string): Promise<{ join_org_name?: string }> {
    if (MOCK || !email.includes("@")) return {};
    try {
      const res = await fetch(`${API_URL}/auth/signup/check`, {
        method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ email }), cache: "no-store",
      });
      return res.ok ? await res.json() : {};
    } catch { return {}; }
  },
  /** Which welcome this person sees. Null when the backend has no teams (open demo, older backend). */
  async welcome(): Promise<Welcome | null> {
    if (MOCK) return null;
    // the open demo (no sign-in) has no teams: don't ask (it would only answer 404)
    if ((await bootstrapAuth()) !== "authenticated") return null;
    try { return await call<Welcome>("GET", "/me/welcome"); } catch { return null; }
  },
  pickTeam: (b: { department_id?: string; profession?: Profession }) => call<Welcome>("POST", "/me/team", b),
  createTeam: (b: { name?: string; profession: Profession; head_me?: boolean }) => call<CreatedTeam>("POST", "/teams", b),
  setupDone: () => call<Record<string, never>>("POST", "/teams/setup/done", {}),
  invite: (b: TeamInvite) => call<{ token: string; email: string }>("POST", "/invitations", b),
};

export const inviteLink = (token: string) => `${window.location.origin}/invite?token=${encodeURIComponent(token)}`;
