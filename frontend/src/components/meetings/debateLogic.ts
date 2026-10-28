// Pure helpers of the debates panel (B5). No React, no app imports: unit tested with node (debateLogic.test.mjs).

/** One option per line (or separated by ";"), trimmed, without duplicates. */
export function parseOptions(text: string): string[] {
  const out: string[] = [];
  for (const raw of (text || "").split(/[\n;]+/)) {
    const o = raw.trim().replace(/\s+/g, " ").slice(0, 160);
    if (o && !out.includes(o)) out.push(o);
  }
  return out;
}

const STATUS_CODE: Record<string, string> = { "400": "invalid", "402": "budget_exceeded", "403": "forbidden", "423": "blocked", "503": "unavailable" };

/** Stable error code of a failed POST /debates (the api client reports "<METHOD> <path> -> <status>"). */
export function debateErrorCode(e: unknown): string {
  const m = /->\s*(\d{3})/.exec(e instanceof Error ? e.message : String(e ?? ""));
  return (m && STATUS_CODE[m[1]]) || "generic";
}
