/**
 * Pure logic of the people chat (no React, no imports): unread / mention counters driven by realtime frames, and the
 * @mention helpers. Mirrors backend/internal/peoplechat (handles, unread = last_seq - last_read_seq).
 * Unit tested with `node --test` (peopleLogic.test.mjs).
 */
export interface PMessage {
  id: string; channel_id: string; seq: number; author_kind: "user" | "agent"; author_id: string; body: string;
  mentions: { kind: "user" | "agent"; id: string }[]; created_at: string;
}

export interface Counter { unread: number; mentions: number; lastSeq: number }
export type Counters = Record<string, Counter>;
const ZERO: Counter = { unread: 0, mentions: 0, lastSeq: 0 };

/**
 * A message arrived over the socket. Frames are at-least-once, so a sequence already seen is ignored. My own messages
 * never count; a message in the channel I am looking at (window focused) is read on arrival.
 */
export function applyIncoming(state: Counters, msg: PMessage, me: string, opts: { activeId: string | null; focused: boolean }): Counters {
  const cur = state[msg.channel_id] ?? ZERO;
  if (msg.seq <= cur.lastSeq) return state;
  const mine = msg.author_kind === "user" && msg.author_id === me;
  const seen = mine || (opts.focused && opts.activeId === msg.channel_id);
  if (seen) return { ...state, [msg.channel_id]: { unread: 0, mentions: 0, lastSeq: msg.seq } };
  const mentioned = msg.mentions.some((m) => m.kind === "user" && m.id === me);
  return { ...state, [msg.channel_id]: { unread: cur.unread + 1, mentions: cur.mentions + (mentioned ? 1 : 0), lastSeq: msg.seq } };
}

/** The person read the channel (here or in another tab). */
export function applyRead(state: Counters, channelId: string, seq: number): Counters {
  const cur = state[channelId];
  if (!cur) return state;
  const last = Math.max(cur.lastSeq, seq);
  if (seq >= last) return { ...state, [channelId]: { unread: 0, mentions: 0, lastSeq: last } };
  return { ...state, [channelId]: { ...cur, unread: Math.min(cur.unread, last - seq) } };
}

/** Total unread across channels (the badge of the chats entry). */
export const totalUnread = (state: Counters) => Object.values(state).reduce((n, c) => n + c.unread, 0);
export const totalMentions = (state: Counters) => Object.values(state).reduce((n, c) => n + c.mentions, 0);

/** "3", or "99+" past 99; empty for zero. */
export const badge = (n: number) => (n <= 0 ? "" : n > 99 ? "99+" : String(n));

/** Seeds the counters from the server list (source of truth on load and reconnect). */
export function fromChannels(rows: { id: string; last_seq: number; unread: number; mentions_unread: number }[]): Counters {
  const out: Counters = {};
  for (const r of rows) out[r.id] = { unread: r.unread, mentions: r.mentions_unread, lastSeq: r.last_seq };
  return out;
}

// ---- @mentions ----------------------------------------------------------------------------------------------

export interface Mentionable { kind: "user" | "agent"; id: string; name: string }

const ACCENTS: Record<string, string> = { á: "a", à: "a", â: "a", ã: "a", ä: "a", é: "e", è: "e", ê: "e", ë: "e", í: "i", ì: "i", î: "i", ï: "i", ó: "o", ò: "o", ô: "o", õ: "o", ö: "o", ú: "u", ù: "u", û: "u", ü: "u", ñ: "n", ç: "c" };
const fold = (s: string) => s.toLowerCase().replace(/[^\u0000-\u007f]/g, (c) => ACCENTS[c] ?? c);

/** The dotted handle the backend resolves: "Ana García" -> "ana.garcia". */
export function handleOf(name: string): string {
  return fold(name.trim()).replace(/[^\p{L}\p{N}]+/gu, ".").replace(/^\.+|\.+$/g, "");
}

/** The @query being typed at the caret, or null: `{ start, query }` where start is the index of the "@". */
export function mentionQuery(text: string, caret: number): { start: number; query: string } | null {
  const upto = text.slice(0, caret);
  const at = upto.lastIndexOf("@");
  if (at < 0) return null;
  if (at > 0 && /[\p{L}\p{N}._-]/u.test(upto[at - 1])) return null; // an e-mail address, not a mention
  const query = upto.slice(at + 1);
  if (/[\s@]/.test(query) || query.length > 40) return null;
  return { start: at, query };
}

/** Candidates whose name or handle starts with / contains the query; prefix matches first, at most `limit`. */
export function suggest(query: string, all: Mentionable[], limit = 6): Mentionable[] {
  const q = fold(query);
  const scored: [number, Mentionable][] = [];
  for (const m of all) {
    const h = handleOf(m.name), n = fold(m.name);
    const rank = !q ? 2 : h.startsWith(q) || n.startsWith(q) ? 0 : n.split(/\s+/).some((w) => w.startsWith(q)) ? 1 : h.includes(q) ? 2 : -1;
    if (rank >= 0) scored.push([rank, m]);
  }
  scored.sort((a, b) => a[0] - b[0] || a[1].name.localeCompare(b[1].name));
  return scored.slice(0, limit).map((x) => x[1]);
}

/** Replaces the @query at `start..caret` with the chosen handle plus a space; returns the new text and caret. */
export function insertMention(text: string, caret: number, start: number, m: Mentionable): { text: string; caret: number } {
  const ins = `@${handleOf(m.name)} `;
  return { text: text.slice(0, start) + ins + text.slice(caret), caret: start + ins.length };
}

export interface Piece { text: string; mention?: Mentionable }

/** Splits a message body into plain text and resolved mentions (to highlight them); unknown @words stay plain. */
export function splitMentions(body: string, all: Mentionable[]): Piece[] {
  const byHandle = new Map<string, Mentionable[]>();
  for (const m of all) { const h = handleOf(m.name); byHandle.set(h, [...(byHandle.get(h) ?? []), m]); }
  const out: Piece[] = [];
  let last = 0;
  for (const mt of body.matchAll(/(^|[^\p{L}\p{N}._-])@([\p{L}\p{N}._-]+)/gu)) {
    const raw = mt[2].replace(/[._-]+$/, "");
    const hit = byHandle.get(fold(raw));
    if (!hit || hit.length !== 1) continue;
    const start = (mt.index ?? 0) + mt[1].length;
    if (start > last) out.push({ text: body.slice(last, start) });
    out.push({ text: "@" + raw, mention: hit[0] });
    last = start + 1 + raw.length;
  }
  if (last < body.length) out.push({ text: body.slice(last) });
  return out;
}
