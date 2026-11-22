/**
 * Comments anchored to a passage of a document (F7, docs/architecture/anchored-comments.md): pure helpers, no React and
 * no network, so they can be unit tested. The server keeps the anchor (quote, context, version) and says whether the
 * quote is still in the text; here the quote is found again in the rendered page to highlight it.
 */
import type { FileKind } from "./fileView";

export interface CommentAnchor { quote: string; prefix: string; suffix: string; version: number; start: number; end: number }
export interface CommentLocation { status: "intact" | "moved" | "lost"; start: number; end: number }
export interface CommentChange { agent_id: string; base_version: number; requested_by: string; requested_at: string; turn_id?: string }
export interface FileComment {
  id: string; file_id: string; parent_id?: string | null; anchor?: CommentAnchor | null;
  author: { kind: string; id: string }; body: string;
  resolved: boolean; resolved_by?: string | null; resolved_version?: number | null; resolved_at?: string | null;
  change?: CommentChange | null; created_at: string; location?: CommentLocation | null;
}
export interface Thread { root: FileComment; replies: FileComment[] }

/** The kinds a person can select text in (the viewer renders them as text). Others get notes on the whole file at most. */
export const ANCHORABLE: FileKind[] = ["markdown", "text"];
export const MAX_QUOTE = 2000;
export const MAX_BODY = 4000;

/** What the person selected, ready to send: line breaks normalized, trimmed. null when empty or too long. */
export function cleanQuote(raw: string): string | null {
  const q = raw.replace(/\r\n?/g, "\n").replace(/[ \t]+/g, " ").replace(/\n{3,}/g, "\n\n").trim();
  if (!q || [...q].length > MAX_QUOTE) return null;
  return q;
}

/** Roots with their replies, oldest first. Open threads first, resolved ones after (each group by creation). */
export function buildThreads(comments: FileComment[]): Thread[] {
  const byParent = new Map<string, FileComment[]>();
  const roots: FileComment[] = [];
  for (const c of comments) {
    if (c.parent_id) byParent.set(c.parent_id, [...(byParent.get(c.parent_id) ?? []), c]);
    else roots.push(c);
  }
  const by = (a: FileComment, b: FileComment) => a.created_at.localeCompare(b.created_at);
  const threads = roots.sort(by).map((root) => ({ root, replies: (byParent.get(root.id) ?? []).sort(by) }));
  return [...threads.filter((t) => !t.root.resolved), ...threads.filter((t) => t.root.resolved)];
}

/** "found" = the quote is in the text; "changed" = it was rewritten ("the text changed"); "general" = a note on the whole file. */
export function anchorState(c: FileComment): "found" | "changed" | "general" {
  if (!c.anchor) return "general";
  return c.location?.status === "lost" ? "changed" : "found";
}

export function openCount(comments: FileComment[]): number {
  return comments.filter((c) => !c.parent_id && !c.resolved).length;
}

/** Whether "Ask {agent} to change it" can be offered: a comment on a passage that still exists, not resolved, not asked yet. */
export function canAskChange(c: FileComment): boolean {
  return !c.parent_id && !!c.anchor && !c.resolved && !c.change && anchorState(c) === "found";
}

export interface NodeRange { startNode: number; startOffset: number; endNode: number; endOffset: number }

/**
 * Finds `quote` in the text nodes of the rendered page. Whitespace is ignored on both sides (a selection across two
 * paragraphs has line breaks that the DOM text does not), and among several equal passages the one whose
 * neighbourhood matches the prefix and suffix best wins.
 */
export function findQuote(nodes: string[], quote: string, prefix = "", suffix = ""): NodeRange | null {
  const flat: string[] = [];
  const owner: { n: number; o: number }[] = [];
  nodes.forEach((t, n) => {
    for (let o = 0; o < t.length; o++) {
      if (!/\s/.test(t[o])) { flat.push(t[o]); owner.push({ n, o }); }
    }
  });
  const hay = flat.join("");
  const needle = quote.replace(/\s+/g, "");
  if (!needle) return null;
  const pre = prefix.replace(/\s+/g, ""), suf = suffix.replace(/\s+/g, "");
  let best = -1, bestScore = -1;
  for (let at = hay.indexOf(needle); at >= 0; at = hay.indexOf(needle, at + 1)) {
    let a = 0;
    while (a < pre.length && at - 1 - a >= 0 && hay[at - 1 - a] === pre[pre.length - 1 - a]) a++;
    let b = 0;
    const end = at + needle.length;
    while (b < suf.length && end + b < hay.length && hay[end + b] === suf[b]) b++;
    if (a + b > bestScore) { best = at; bestScore = a + b; }
  }
  if (best < 0) return null;
  const s = owner[best], e = owner[best + needle.length - 1];
  return { startNode: s.n, startOffset: s.o, endNode: e.n, endOffset: e.o + 1 };
}

/** Short form of a quote for lists. */
export function shortQuote(q: string, max = 140): string {
  const one = q.replace(/\s+/g, " ").trim();
  const chars = [...one];
  return chars.length <= max ? one : `${chars.slice(0, max - 1).join("")}…`;
}
