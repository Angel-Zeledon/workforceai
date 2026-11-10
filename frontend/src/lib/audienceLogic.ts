import type { Audience } from "./teamsLogic";

/** The audience the menu uses: only the demo (MOCK) is "company" on purpose; otherwise what /me/welcome says, or null (today's menu). */
export function audienceOf(welcome: { audience?: Audience | "" } | null | undefined, mock: boolean): Audience | null {
  if (mock) return "company";
  return welcome?.audience || null;
}
