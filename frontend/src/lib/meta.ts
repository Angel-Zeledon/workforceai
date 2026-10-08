import type { AgentState } from "./types";
import { tr, hasKey } from "./i18n-core";

export interface RoleMeta {
  color: string; label: string; skin: string; hair: string;
  hairStyle: "long" | "short" | "bun" | "bob" | "ponytail" | "bald";
  accessory: "none" | "glasses" | "roundglasses" | "headphones" | "hardhat" | "headset" | "lanyard" | "beanie";
  tie: boolean; female: boolean;
  desk: [number, number];
}
// Desk positions; characters sit on the +z side and face -z (toward the monitor).
export const ROLE_META: Record<string, RoleMeta> = {
  sales:      { color: "#f4895a", label: "Sales",       skin: "#f2c09a", hair: "#7a4020", hairStyle: "long",     accessory: "none",         tie: false, female: true,  desk: [-8, -4.5] },
  analyst:    { color: "#45b8cc", label: "Analyst",     skin: "#c98f68", hair: "#1c1410", hairStyle: "bob",      accessory: "headphones",   tie: false, female: true,  desk: [-3.5, -4.5] },
  accounting: { color: "#4cb987", label: "Accounting", skin: "#f0c8a4", hair: "#8a8f98", hairStyle: "short",    accessory: "roundglasses", tie: true,  female: false, desk: [1, -4.5] },
  hr:         { color: "#ee7aa8", label: "HR",      skin: "#a8734f", hair: "#16110d", hairStyle: "short",    accessory: "lanyard",      tie: false, female: false, desk: [-8, 1.5] },
  legal:      { color: "#9577d6", label: "Legal",        skin: "#edc29b", hair: "#2a1a12", hairStyle: "bun",      accessory: "glasses",      tie: false, female: true,  desk: [-3.5, 1.5] },
  operations: { color: "#e8b53a", label: "Operations",  skin: "#d9a07a", hair: "#2b2b2b", hairStyle: "short",    accessory: "hardhat",      tie: false, female: false, desk: [1, 1.5] },
  assistant:  { color: "#5a95e8", label: "Assistant",    skin: "#e8b995", hair: "#7a4a2a", hairStyle: "ponytail", accessory: "headset",      tie: false, female: true,  desk: [5.5, 1.5] },
};
export const DEFAULT_ROLE: RoleMeta = { color: "#94a3b8", label: "Agent", skin: "#d9a07a", hair: "#222", hairStyle: "short", accessory: "none", tie: false, female: false, desk: [0, 6] };
/** Display data of roles that have no hardcoded entry above, fed from GET /role-templates (see registerRoleTemplates). */
const TEMPLATE_META: Record<string, Partial<RoleMeta>> = {};
export interface RoleTemplateDisplay {
  id: string; title?: string;
  display?: { color?: string; appearance?: { skin?: string; hair?: string; hair_style?: string; accessory?: string; tie?: boolean; female?: boolean } };
}
const HAIR_STYLES = ["long", "short", "bun", "bob", "ponytail", "bald"];
const ACCESSORIES = ["none", "glasses", "roundglasses", "headphones", "hardhat", "headset", "lanyard", "beanie"];
/** Registers the look of template-based roles. The 7 hardcoded seed roles always win, so the demo never changes. */
export function registerRoleTemplates(items: RoleTemplateDisplay[]) {
  for (const it of items) {
    if (ROLE_META[it.id]) continue;
    const d = it.display, a = d?.appearance;
    const m: Partial<RoleMeta> = {};
    if (it.title) m.label = it.title;
    if (d?.color) m.color = d.color;
    if (a?.skin) m.skin = a.skin;
    if (a?.hair) m.hair = a.hair;
    if (a?.hair_style && HAIR_STYLES.includes(a.hair_style)) m.hairStyle = a.hair_style as RoleMeta["hairStyle"];
    if (a?.accessory && ACCESSORIES.includes(a.accessory)) m.accessory = a.accessory as RoleMeta["accessory"];
    if (typeof a?.tie === "boolean") m.tie = a.tie;
    if (typeof a?.female === "boolean") m.female = a.female;
    TEMPLATE_META[it.id] = m;
  }
}
const baseRoleMeta = (role?: string): RoleMeta => {
  if (!role) return DEFAULT_ROLE;
  return ROLE_META[role] ?? (TEMPLATE_META[role] ? { ...DEFAULT_ROLE, ...TEMPLATE_META[role] } : DEFAULT_ROLE);
};
/** Role metadata with the label localized for the current locale (role.<id> keys, then the template title). */
export const roleMeta = (role?: string): RoleMeta => {
  const m = baseRoleMeta(role);
  const label = role && hasKey(`role.${role}`) ? tr(`role.${role}`) : role && TEMPLATE_META[role]?.label ? TEMPLATE_META[role].label! : tr("role.default");
  return { ...m, label };
};

/** Desk slots for agents whose role has no fixed desk (or whose fixed desk is already taken). Clear of the 7 seed desks and of the meeting/approval zones. */
export const FREE_DESKS: [number, number][] = [
  [5.5, -4.5], [-8, 6.5], [-3.5, 6.5], [1, 6.5], [5.5, 6.5], [-12.5, -4.5], [-12.5, 1.5], [-12.5, 6.5],
];
/** Assigns every agent a desk: its role's fixed desk when free, otherwise the next free slot (stacked in extra rows when all are used). */
export function assignDesks(agents: { id: string; role: string }[]): Record<string, [number, number]> {
  const out: Record<string, [number, number]> = {};
  const used = new Set<string>();
  const key = (d: [number, number]) => `${d[0]},${d[1]}`;
  for (const a of agents) {
    const fixed = ROLE_META[a.role]?.desk;
    if (fixed && !used.has(key(fixed))) { out[a.id] = fixed; used.add(key(fixed)); }
  }
  let n = 0;
  for (const a of agents) {
    if (out[a.id]) continue;
    let d = FREE_DESKS.find((f) => !used.has(key(f)));
    if (!d) { const k = n++; d = [-12.5 + (k % 7) * 4.5, -8 + Math.floor(k / 7) * 1.2]; }
    out[a.id] = d; used.add(key(d));
  }
  return out;
}

// Where the "user" (CEO) lives in the office: approvals zone.
export const USER_POS: [number, number, number] = [9.5, 1.6, 5.4];
export const MEETING_CENTER: [number, number] = [9.5, -6.2];

export const STATE_META: Record<AgentState, { color: string }> = {
  idle: { color: "#66707f" },
  thinking: { color: "#6d5bb5" },
  working: { color: "#2b6cb0" },
  waiting: { color: "#7b8494" },
  talking: { color: "#2f8f6b" },
  reviewing: { color: "#4f5fb8" },
  blocked: { color: "#b4443c" },
  awaiting_approval: { color: "#a86208" },
  completed: { color: "#2f7d55" },
  error: { color: "#a63232" },
};
export const stateMeta = (s: string) => {
  const key = (s in STATE_META ? s : "idle") as AgentState;
  return { color: STATE_META[key].color, label: tr(`state.${key}`) };
};

export { fmtUsd, fmtTime } from "./i18n-core";
export const taskStatusLabel = (st: string) => (hasKey(`task.status.${st}`) ? tr(`task.status.${st}`) : st);
export const TASK_STATUS_COLOR: Record<string, string> = {
  pending: "#66707f", running: "#2b6cb0", blocked: "#b4443c", awaiting_approval: "#a86208", done: "#2f7d55", failed: "#a63232",
};
