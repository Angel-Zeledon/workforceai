/**
 * Edit by chat (docs/architecture/agent-edit.md): pure helpers, no React and no network, so they can be unit tested.
 */
import type { FileRef } from "./types";

/** The agent shown first in "Ask for a change": the one that last edited the file, else the most relevant role, else the first. */
export function pickEditAgent(
  versions: { version: number; author: { kind: string; id: string } }[],
  agents: Record<string, { id: string; role?: string; title?: string }>,
  order: string[],
  kind: string,
): string | null {
  const ids = order.filter((id) => agents[id]);
  if (!ids.length) return null;
  const last = [...versions].sort((a, b) => b.version - a.version).find((v) => v.author.kind === "agent" && agents[v.author.id]);
  if (last) return last.author.id;
  const roles: Record<string, RegExp> = {
    sheet: /financ|analy|data|account|contab|report/i,
    csv: /financ|analy|data|account|contab|report/i,
    doc: /writ|content|legal|market|redact|contenido|doc/i,
    markdown: /writ|content|legal|market|redact|contenido|doc/i,
  };
  const re = roles[kind];
  if (re) {
    const hit = ids.find((id) => re.test(`${agents[id].role ?? ""} ${agents[id].title ?? ""}`));
    if (hit) return hit;
  }
  return ids[0];
}

/** Undo is offered only while the edit is still the latest version: restoring would otherwise drop later work. */
export function canUndoEdit(f: { version?: number; from_version?: number }, currentVersion: number): boolean {
  return !!f.from_version && f.from_version >= 1 && !!f.version && f.version === currentVersion && currentVersion > f.from_version;
}

/** "+3 -1" style counts. */
export const editCounts = (added = 0, removed = 0) => `+${added} -${removed}`;

/**
 * The open file as context of a chat message ("Edit this file in chat"): the file is added to the attachments
 * (path + version) and the edit hint is appended to the text. Returns the same inputs when there is nothing to add.
 */
export function withOpenFile(
  text: string,
  refs: FileRef[],
  open: FileRef | null,
  hint: string,
): { text: string; refs: FileRef[] } {
  if (!open || !open.path) return { text, refs };
  const has = refs.some((r) => r.path === open.path);
  const nextRefs = has ? refs : [...refs, { ...open, action: "attach" }];
  const t = text.trim();
  return { text: t ? `${t}\n\n${hint}` : hint, refs: nextRefs };
}
