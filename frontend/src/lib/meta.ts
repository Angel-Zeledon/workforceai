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
const baseRoleMeta = (role?: string): RoleMeta => (role && ROLE_META[role]) || DEFAULT_ROLE;
/** Role metadata with the label localized for the current locale (role.<id> keys). */
export const roleMeta = (role?: string): RoleMeta => {
  const m = baseRoleMeta(role);
  return { ...m, label: role && hasKey(`role.${role}`) ? tr(`role.${role}`) : tr("role.default") };
};

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
