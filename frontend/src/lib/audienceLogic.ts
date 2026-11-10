import type { Audience } from "./teamsLogic";

/** The audience the menu uses: only the demo (MOCK) is "company" on purpose; otherwise what /me/welcome says, or null (today's menu). */
export function audienceOf(welcome: { audience?: Audience | "" } | null | undefined, mock: boolean): Audience | null {
  if (mock) return "company";
  return welcome?.audience || null;
}

export const audienceKey = (orgId: string) => `wf.audience.${orgId}`;

/** First value to show for an organization: the demo is company; otherwise the last one saved for that org if it is valid, else null (today's menu). */
export function initialAudience(stored: string | null | undefined, mock: boolean): Audience | null {
  if (mock) return "company";
  return stored === "solo" || stored === "team" || stored === "company" ? stored : null;
}
