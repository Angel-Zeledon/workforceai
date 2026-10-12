// Pure logic of the slash commands (no React, no path aliases: unit-testable with node --test).
// Commands are accepted in Spanish AND English whatever the UI locale; the reply language follows the alias typed.
import type { Command, CmdArg, Lang } from "./registry";

export const GROUP_ORDER = ["control", "office", "org"] as const;

/** lowercase, accent-free, for matching */
export function norm(s: string): string {
  return s.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase().trim();
}

/** Every word that invokes the command: id, en and es aliases. */
export function allNames(c: Command): string[] {
  return [...new Set([c.id, ...c.aliases.en, ...c.aliases.es].map(norm))];
}

/** The alias to display: first alias of the UI locale, falling back to the id. */
export function primaryAlias(c: Command, locale: Lang): string {
  return c.aliases[locale][0] ?? (locale === "es" ? c.aliases.es[0] : undefined) ?? c.id;
}

export interface Parsed { slash: boolean; name: string; rest: string; hasSpace: boolean }
export function parseInput(text: string): Parsed {
  if (!text.startsWith("/")) return { slash: false, name: "", rest: "", hasSpace: false };
  const m = /^\/(\S*)(\s?)([\s\S]*)$/.exec(text)!;
  return { slash: true, name: m[1], hasSpace: m[2] !== "", rest: m[3] };
}

/** Exact resolution of the typed name (any language). */
export function resolveCommand(cmds: Command[], name: string): Command | undefined {
  const n = norm(name);
  if (!n) return undefined;
  return cmds.find((c) => allNames(c).includes(n));
}

/**
 * Language of the reply: the alias the user typed decides. Names that do not tell the language
 * (present in both lists, or the id of a command with no aliases at all, like /excel) use the UI locale.
 */
export function detectLang(c: Command, typed: string, uiLocale: Lang): Lang {
  const n = norm(typed);
  const es = c.aliases.es.map(norm);
  const en = [c.id, ...c.aliases.en].map(norm);
  const inEs = es.includes(n), inEn = en.includes(n);
  if (inEs && inEn) return uiLocale;
  if (inEs) return "es";
  if (inEn && c.aliases.es.length === 0 && c.aliases.en.length === 0) return uiLocale;
  if (inEn) return "en";
  return uiLocale;
}

export interface Match { cmd: Command; score: number; alias: string; matched: string }
/** Search-as-you-type over ids and aliases of both languages; the current-locale alias is the one displayed. */
export function matchCommands(cmds: Command[], query: string, locale: Lang): Match[] {
  const q = norm(query);
  const out: Match[] = [];
  for (const cmd of cmds) {
    let best = -1, matched = "";
    for (const nm of allNames(cmd)) {
      let s = -1;
      if (!q) s = 1;
      else if (nm === q) s = 100;
      else if (nm.startsWith(q)) s = 80 - Math.min(20, nm.length - q.length);
      else if (nm.includes(q)) s = 40;
      if (s > best) { best = s; matched = nm; }
    }
    if (best < 0 && q.length >= 3) {
      const hay = norm(`${cmd.title.es} ${cmd.title.en} ${cmd.description.es} ${cmd.description.en}`);
      if (hay.includes(q)) { best = 10; matched = cmd.id; }
    }
    if (best < 0) continue;
    out.push({ cmd, score: best, alias: primaryAlias(cmd, locale), matched });
  }
  const gi = (g: string) => GROUP_ORDER.indexOf(g as (typeof GROUP_ORDER)[number]);
  return out.sort((a, b) => (q ? b.score - a.score : 0) || gi(a.cmd.group) - gi(b.cmd.group));
}

export interface Tok { text: string; start: number; end: number; quoted: boolean }
/** Splits on whitespace honoring "double quotes"; keeps offsets so a token can be replaced in place. */
export function tokenize(s: string): Tok[] {
  const out: Tok[] = [];
  const re = /"([^"]*)"?|(\S+)/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(s))) out.push({ text: m[1] !== undefined ? m[1] : m[2], start: m.index, end: m.index + m[0].length, quoted: m[1] !== undefined });
  return out;
}

const stripAt = (s: string) => (s.startsWith("@") ? s.slice(1) : s);

export interface ArgState {
  values: Record<string, string>;
  missing: string[];
  /** index in cmd.args of the argument being typed */
  current: number;
  /** token being typed (null when starting a new one) */
  currentTok: Tok | null;
  mentions: string[];
}
export function parseArgs(cmd: Command, rest: string): ArgState {
  const toks = tokenize(rest);
  const endsSpace = /\s$/.test(rest);
  const values: Record<string, string> = {};
  const mentions: string[] = [];
  let ti = 0, current = 0;
  let currentTok: Tok | null = null;
  for (let ai = 0; ai < cmd.args.length; ai++) {
    const a = cmd.args[ai];
    current = ai;
    if (ti >= toks.length) break;
    // A list of agents followed by more arguments (/reunion ventas legal: tema): the agents are the
    // @mentions at the start, or everything before a ":". Plain words are left for the next argument.
    if (a.variadic && a.kind === "agent" && ai < cmd.args.length - 1) {
      const colon = rest.indexOf(":");
      let names: Tok[];
      if (colon >= 0) names = toks.filter((t) => t.start < colon);
      else { names = []; for (let k = ti; k < toks.length && toks[k].text.startsWith("@"); k++) names.push(toks[k]); }
      if (names.length) {
        values[a.name] = names.map((t) => stripAt(t.text.replace(/:$/, ""))).filter(Boolean).join(" ");
        for (const t of names) mentions.push(stripAt(t.text.replace(/:$/, "")));
      }
      ti += names.length;
      if (colon >= 0) {
        const after = rest.slice(colon + 1);
        // the remaining arguments read what follows the colon
        const sub = parseArgs({ ...cmd, args: cmd.args.slice(ai + 1) } as Command, after);
        Object.assign(values, sub.values); mentions.push(...sub.mentions);
        currentTok = sub.currentTok; current = ai + 1 + sub.current;
        ti = toks.length;
        break;
      }
      continue;
    }
    if (a.variadic) {
      const slice = toks.slice(ti);
      values[a.name] = rest.slice(slice[0].start).trim();
      for (const t of slice) if (t.text.startsWith("@")) mentions.push(stripAt(t.text));
      currentTok = endsSpace ? null : slice[slice.length - 1];
      ti = toks.length;
      break;
    }
    values[a.name] = a.kind === "agent" ? stripAt(toks[ti].text) : toks[ti].text;
    if (a.kind === "agent" || toks[ti].text.startsWith("@")) mentions.push(stripAt(toks[ti].text));
    const last = ti === toks.length - 1;
    ti++;
    if (last && !endsSpace) { currentTok = toks[ti - 1]; break; }
    current = Math.min(ai + 1, cmd.args.length - 1);
  }
  const missing = cmd.args.filter((a) => a.required && !values[a.name]).map((a) => a.name);
  return { values, missing, current, currentTok, mentions };
}

/** The @mention being typed at the caret, if any. */
export function mentionAt(text: string, caret: number): { query: string; start: number; end: number } | null {
  const before = text.slice(0, caret);
  const m = /(^|\s)@([\p{L}\p{N}_-]*)$/u.exec(before);
  if (!m) return null;
  const start = caret - m[2].length - 1;
  const after = /^[\p{L}\p{N}_-]*/u.exec(text.slice(caret))![0];
  return { query: m[2], start, end: caret + after.length };
}

/** Text with [start,end) replaced by value (quoted when it has spaces) plus a trailing space. */
export function replaceRange(text: string, start: number, end: number, value: string): { text: string; caret: number } {
  const v = /\s/.test(value) ? `"${value}"` : value;
  const next = text.slice(0, start) + v + " " + text.slice(end).replace(/^\s+/, "");
  return { text: next, caret: start + v.length + 1 };
}

/** Shape of the `args` sent to POST /commands/run: { argName: value }. */
export function buildArgs(cmd: Command, st: ArgState): Record<string, string> {
  const out: Record<string, string> = {};
  for (const a of cmd.args) if (st.values[a.name] !== undefined) out[a.name] = st.values[a.name];
  return out;
}

export const argLabel = (a: CmdArg) => (a.variadic ? `${a.name}…` : a.name);

/** Surface of a chat input from its conversation id. */
export function surfaceOf(conv: string, general = false): "office" | "general" | "agent" {
  if (conv.startsWith("agent:")) return "agent";
  return general ? "general" : "office";
}

/** Sort rows of a small table by column index, numeric when both look numeric. */
export function sortRows(rows: unknown[][], col: number, dir: 1 | -1): unknown[][] {
  const num = (v: unknown) => (typeof v === "number" ? v : Number(String(v).replace(/[^0-9.\-]/g, "")));
  return [...rows].sort((a, b) => {
    const x = a[col], y = b[col];
    const nx = num(x), ny = num(y);
    if (!Number.isNaN(nx) && !Number.isNaN(ny) && String(x).trim() !== "" && String(y).trim() !== "") return (nx - ny) * dir;
    return String(x ?? "").localeCompare(String(y ?? "")) * dir;
  });
}
