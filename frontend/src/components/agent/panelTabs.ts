/**
 * The agent panel in 5 visible tabs (docs/superpowers/plans/2026-11-04-ux-tanda-2.md, task B; spec 5.3).
 * Pure (no React): AgentPanel uses it to place every old section of the wheel in its new tab, and to translate old
 * links and focus requests (`?section=thoughts`, the shield's "open the thought audit") to the new tab + the section to
 * scroll to + the folded block to open.
 */

export const AGENT_TABS = ["chat", "work", "can", "knows", "profile"] as const;
export type AgentTab = (typeof AGENT_TABS)[number];

/** The 11 sections of the old wheel (their ids are the old `?section=` values and the `panel-tab-*` test ids). */
export const OLD_SECTIONS = ["chat", "state", "tasks", "can", "chats", "memory", "reports", "files", "activity", "profile", "thoughts"] as const;

/** Blocks of "Trabajo" that start folded. */
export const FOLDED = ["thoughts", "chats"] as const;
export type Folded = (typeof FOLDED)[number];

/** What each tab holds, in order (the jump row of Trabajo and Qué sabe lists these). */
export const TAB_SECTIONS: Record<AgentTab, readonly string[]> = {
  chat: [],
  work: ["state", "tasks", "reports", "files", "activity", "thoughts", "chats"],
  can: ["controls", "permissions", "folders", "tools"],
  knows: ["skills", "memory"],
  profile: ["about", "job", "look", "trust", "quality"],
};

// Old names and handy aliases -> section inside a tab. The new tab ids also work as a `section` value.
const ALIAS: Record<string, string> = {
  status: "state", estado: "state", task: "tasks", report: "reports", archivos: "files", artifacts: "files",
  thought: "thoughts", audit: "thoughts", team: "chats", conversations: "chats",
  approvals: "permissions", perm: "permissions", access: "folders", pause: "controls", rehearsal: "controls",
  connections: "tools", skill: "skills", appearance: "look", customizer: "look", knowledge: "knows",
};

export interface PanelTarget { tab: AgentTab; section: string | null; unfold: Folded | null }

/**
 * Translates a section name (old wheel id, new tab id or alias) to where it lives now. `null` when unknown, so the
 * caller keeps what is open. The old tabs "can" and "profile" are also the new tabs: they open at the top.
 */
export function resolvePanelTarget(raw: string | null | undefined): PanelTarget | null {
  const k0 = String(raw ?? "").trim().toLowerCase();
  if (!k0) return null;
  const k = ALIAS[k0] ?? k0;
  if ((AGENT_TABS as readonly string[]).includes(k)) return { tab: k as AgentTab, section: null, unfold: null };
  for (const tab of AGENT_TABS) {
    if (!TAB_SECTIONS[tab].includes(k)) continue;
    const unfold = (FOLDED as readonly string[]).includes(k) ? (k as Folded) : null;
    return { tab, section: k, unfold };
  }
  return null;
}
