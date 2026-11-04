/**
 * One table of states for the whole app (docs/superpowers/specs/2026-11-03-ux-simplificacion-propuesta.md, 5.4).
 * Every internal state of a pedido, project, project task, batch row, agent task, agent or customer falls into one of a
 * few kinds; each kind has ONE color, so "Te necesita" is amber and "Listo" is the same green everywhere.
 * Pure (no React, no i18n): the words live in the i18n catalogs under the existing keys (pv.state.*, task.status.*…).
 */

export type StatusKind = "draft" | "todo" | "free" | "running" | "needs" | "paused" | "done" | "partial" | "problem" | "cancelled";

/** The single palette. needs = the app's warn (amber), done = ok (green), problem = err (red), running = blue. */
export const KIND_COLOR: Record<StatusKind, string> = {
  draft: "#aab3c0",
  todo: "#7b8494",
  free: "#7b8494",
  running: "#2b6cb0",
  needs: "#a86208",
  paused: "#66707f",
  done: "#287c48",
  partial: "#287c48",
  problem: "#b4443c",
  cancelled: "#8b94a3",
};

/** Tailwind tone of a kind, for the places that color with classes instead of inline styles. */
export type KindTone = "running" | "warn" | "ok" | "err" | "mute";
export const KIND_TONE: Record<StatusKind, KindTone> = {
  draft: "mute", todo: "mute", free: "mute", running: "running", needs: "warn", paused: "mute", done: "ok", partial: "ok", problem: "err", cancelled: "mute",
};

export type StatusDomain = "node" | "project" | "agent" | "task" | "pedido" | "batchRow" | "batch" | "customer";

const RUNNING = ["running", "thinking", "working", "reviewing", "talking", "on_track"];
const NEEDS = ["needs_help", "needs_input", "needs_approval", "awaiting_approval", "waiting_human", "waiting_approval"];
const TODO = ["pending", "ready", "queued", "waiting", "planning", "unknown"];
const PROBLEM = ["failed", "blocked", "error"];

/** Kind of an internal state. Unknown values from a newer server fall back to "todo" (gray), never break the view. */
export function kindOf(domain: StatusDomain, state: string | null | undefined): StatusKind {
  const s = String(state ?? "");
  if (s === "draft") return "draft";
  if (s === "idle") return domain === "agent" || domain === "customer" ? "free" : "todo";
  if (s === "paused") return "paused";
  if (s === "done" || s === "completed") return "done";
  if (s === "partial") return "partial";
  if (s === "cancelled" || s === "skipped") return "cancelled";
  if (RUNNING.includes(s) || s === "retrying") return "running";
  if (NEEDS.includes(s)) return "needs";
  if (PROBLEM.includes(s)) return "problem";
  if (TODO.includes(s)) return "todo";
  return "todo";
}

export const statusColor = (domain: StatusDomain, state: string | null | undefined) => KIND_COLOR[kindOf(domain, state)];
export const statusTone = (domain: StatusDomain, state: string | null | undefined) => KIND_TONE[kindOf(domain, state)];

/** Builds a color table for a list of states (the old per-screen tables now derive from the single palette). */
export function colorTable<S extends string>(domain: StatusDomain, states: readonly S[]): Record<S, string> {
  return Object.fromEntries(states.map((s) => [s, statusColor(domain, s)])) as Record<S, string>;
}

/** i18n prefix of the label of each domain (the keys already exist; only their words were unified). */
export const LABEL_PREFIX: Record<StatusDomain, string> = {
  node: "pv.state.", project: "pv.status.", agent: "state.", task: "task.status.", pedido: "pedido.status.",
  batchRow: "batch.row.", batch: "batch.status.", customer: "customers.status.",
};
