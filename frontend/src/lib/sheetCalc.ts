/**
 * Tiny spreadsheet formula engine for the aiw.sheet/1 content (no external dependency).
 * Supports + - * / ^, parentheses, comparisons, cell refs, ranges and a whitelist of functions.
 * AIW_REF("alias","Sheet!A1") resolves through the sheet `imports` using a caller-provided resolver.
 */
export interface SheetCell { v?: string | number | boolean | null; f?: string; fmt?: string }
export interface SheetData {
  id: string; name: string; rows?: number; cols?: number;
  cells: Record<string, SheetCell>;
  colw?: Record<string, number>;
}
export interface SheetImport { alias: string; artifact_id: string; pinned_version?: number | null }
export interface SheetContent { schema: "aiw.sheet/1"; sheets: SheetData[]; imports?: SheetImport[] }

export type CellValue = string | number | boolean | null;
/** Resolves a reference to another artifact: (artifactId, "Sheet!A1" or "Sheet!A1:B3") -> value or matrix. */
export type RefResolver = (artifactId: string, ref: string) => CellValue | CellValue[][] | undefined;

export const colName = (i: number) => {
  let s = ""; let n = i + 1;
  while (n > 0) { const r = (n - 1) % 26; s = String.fromCharCode(65 + r) + s; n = Math.floor((n - 1) / 26); }
  return s;
};
export const colIndex = (name: string) => name.split("").reduce((a, c) => a * 26 + c.charCodeAt(0) - 64, 0) - 1;
export function parseAddr(a: string): { c: number; r: number } | null {
  const m = /^\$?([A-Za-z]{1,3})\$?(\d{1,6})$/.exec(a.trim());
  return m ? { c: colIndex(m[1].toUpperCase()), r: Number(m[2]) - 1 } : null;
}
export const addr = (c: number, r: number) => `${colName(c)}${r + 1}`;

type Tok = { t: "num" | "str" | "id" | "op" | "lp" | "rp" | "comma"; v: string };
function tokenize(src: string): Tok[] {
  const out: Tok[] = []; let i = 0;
  while (i < src.length) {
    const ch = src[i];
    if (/\s/.test(ch)) { i++; continue; }
    if (/[0-9.]/.test(ch)) { let j = i; while (j < src.length && /[0-9.]/.test(src[j])) j++; out.push({ t: "num", v: src.slice(i, j) }); i = j; continue; }
    if (ch === '"') { let j = i + 1; let s = ""; while (j < src.length && src[j] !== '"') s += src[j++]; out.push({ t: "str", v: s }); i = j + 1; continue; }
    if (/[A-Za-z_$]/.test(ch)) { let j = i; while (j < src.length && /[A-Za-z0-9_$!:.]/.test(src[j])) j++; out.push({ t: "id", v: src.slice(i, j) }); i = j; continue; }
    if (ch === "(") { out.push({ t: "lp", v: ch }); i++; continue; }
    if (ch === ")") { out.push({ t: "rp", v: ch }); i++; continue; }
    if (ch === "," || ch === ";") { out.push({ t: "comma", v: "," }); i++; continue; }
    const two = src.slice(i, i + 2);
    if (["<=", ">=", "<>"].includes(two)) { out.push({ t: "op", v: two }); i += 2; continue; }
    if ("+-*/^<>=&%".includes(ch)) { out.push({ t: "op", v: ch }); i++; continue; }
    throw new Error("#ERR!");
  }
  return out;
}

const num = (v: unknown): number => {
  if (typeof v === "number") return v;
  if (typeof v === "boolean") return v ? 1 : 0;
  if (v === null || v === undefined || v === "") return 0;
  const n = Number(v);
  if (Number.isNaN(n)) throw new Error("#VALUE!");
  return n;
};
const flat = (args: unknown[]): CellValue[] => args.flatMap((a) => (Array.isArray(a) ? (a as CellValue[][]).flat() : [a as CellValue]));
const nums = (args: unknown[]) => flat(args).filter((v) => typeof v === "number") as number[];

export interface CalcCtx {
  sheets: SheetData[];
  sheet: SheetData;
  imports?: SheetImport[];
  resolve?: RefResolver;
  depth?: number;
  stack?: Set<string>;
}

function getSheet(ctx: CalcCtx, name?: string): SheetData | undefined {
  if (!name) return ctx.sheet;
  return ctx.sheets.find((s) => s.name === name || s.id === name);
}

/** Evaluates the cell at `a` (e.g. "B3") of ctx.sheet. */
export function cellValue(ctx: CalcCtx, a: string): CellValue {
  const sheet = ctx.sheet;
  const cell = sheet.cells[a.toUpperCase().replace(/\$/g, "")];
  if (!cell) return null;
  if (!cell.f) return cell.v ?? null;
  const key = `${sheet.id}!${a}`;
  const stack = ctx.stack ?? new Set<string>();
  if (stack.has(key)) return "#CIRC!";
  if ((ctx.depth ?? 0) > 64) return "#CIRC!";
  stack.add(key);
  try {
    const res = evalFormula(cell.f, { ...ctx, stack, depth: (ctx.depth ?? 0) + 1 });
    return Array.isArray(res) ? (res[0]?.[0] ?? null) : res;
  } catch (e) {
    // fall back to the cached value when the source artifact is not loaded
    if (e instanceof Error && e.message === "#REF!" && cell.v !== undefined) return cell.v;
    return e instanceof Error && e.message.startsWith("#") ? e.message : "#ERR!";
  } finally { stack.delete(key); }
}

function rangeValues(ctx: CalcCtx, ref: string): CellValue[][] | CellValue {
  let sheetName: string | undefined; let r = ref;
  if (r.includes("!")) { const i = r.indexOf("!"); sheetName = r.slice(0, i).replace(/^'|'$/g, ""); r = r.slice(i + 1); }
  const sheet = getSheet(ctx, sheetName);
  if (!sheet) throw new Error("#REF!");
  const sub = { ...ctx, sheet };
  const [a, b] = r.split(":");
  const pa = parseAddr(a);
  if (!pa) throw new Error("#REF!");
  if (!b) return cellValue(sub, addr(pa.c, pa.r));
  const pb = parseAddr(b);
  if (!pb) throw new Error("#REF!");
  const out: CellValue[][] = [];
  for (let r2 = Math.min(pa.r, pb.r); r2 <= Math.max(pa.r, pb.r); r2++) {
    const row: CellValue[] = [];
    for (let c = Math.min(pa.c, pb.c); c <= Math.max(pa.c, pb.c); c++) row.push(cellValue(sub, addr(c, r2)));
    out.push(row);
  }
  return out;
}

export function evalFormula(formula: string, ctx: CalcCtx): CellValue | CellValue[][] {
  const toks = tokenize(formula.replace(/^=/, ""));
  let p = 0;
  const peek = () => toks[p];
  const eat = () => toks[p++];

  const parseCompare = (): any => {
    let l = parseAdd();
    while (peek() && peek().t === "op" && ["=", "<", ">", "<=", ">=", "<>"].includes(peek().v)) {
      const op = eat().v; const r = parseAdd();
      const a = l as any, b = r as any;
      l = op === "=" ? a === b : op === "<>" ? a !== b : op === "<" ? a < b : op === ">" ? a > b : op === "<=" ? a <= b : a >= b;
    }
    return l;
  };
  const parseAdd = (): any => {
    let l = parseMul();
    while (peek() && peek().t === "op" && ["+", "-", "&"].includes(peek().v)) {
      const op = eat().v; const r = parseMul();
      l = op === "&" ? `${l ?? ""}${r ?? ""}` : op === "+" ? num(l) + num(r) : num(l) - num(r);
    }
    return l;
  };
  const parseMul = (): any => {
    let l = parsePow();
    while (peek() && peek().t === "op" && ["*", "/"].includes(peek().v)) {
      const op = eat().v; const r = parsePow();
      if (op === "/") { if (num(r) === 0) throw new Error("#DIV/0!"); l = num(l) / num(r); } else l = num(l) * num(r);
    }
    return l;
  };
  const parsePow = (): any => {
    const l = parseUnary();
    if (peek() && peek().t === "op" && peek().v === "^") { eat(); return Math.pow(num(l), num(parsePow())); }
    return l;
  };
  const parseUnary = (): any => {
    if (peek() && peek().t === "op" && (peek().v === "-" || peek().v === "+")) {
      const op = eat().v; const v = num(parseUnary()); return op === "-" ? -v : v;
    }
    let v = parsePrimary();
    while (peek() && peek().t === "op" && peek().v === "%") { eat(); v = num(v) / 100; }
    return v;
  };
  const parsePrimary = (): any => {
    const t = eat();
    if (!t) throw new Error("#ERR!");
    if (t.t === "num") return Number(t.v);
    if (t.t === "str") return t.v;
    if (t.t === "lp") { const v = parseCompare(); if (eat()?.t !== "rp") throw new Error("#ERR!"); return v; }
    if (t.t === "id") {
      if (peek()?.t === "lp") {
        eat();
        const raw: Tok[][] = []; // keep argument token ranges for AIW_REF static alias check
        const args: any[] = [];
        if (peek()?.t !== "rp") {
          for (;;) {
            const start = p; args.push(parseCompare()); raw.push(toks.slice(start, p));
            if (peek()?.t === "comma") { eat(); continue; }
            break;
          }
        }
        if (eat()?.t !== "rp") throw new Error("#ERR!");
        return callFn(t.v.toUpperCase(), args, ctx);
      }
      const u = t.v.toUpperCase();
      if (u === "TRUE") return true;
      if (u === "FALSE") return false;
      return rangeValues(ctx, t.v);
    }
    throw new Error("#ERR!");
  };

  const out = parseCompare();
  if (p < toks.length) throw new Error("#ERR!");
  return out;
}

function callFn(name: string, args: any[], ctx: CalcCtx): CellValue | CellValue[][] {
  switch (name) {
    case "SUM": return nums(args).reduce((a, b) => a + b, 0);
    case "AVERAGE": { const n = nums(args); return n.length ? n.reduce((a, b) => a + b, 0) / n.length : "#DIV/0!"; }
    case "MIN": { const n = nums(args); return n.length ? Math.min(...n) : 0; }
    case "MAX": { const n = nums(args); return n.length ? Math.max(...n) : 0; }
    case "COUNT": return nums(args).length;
    case "ABS": return Math.abs(num(args[0]));
    case "ROUND": { const d = Math.pow(10, args[1] === undefined ? 0 : num(args[1])); return Math.round(num(args[0]) * d) / d; }
    case "IF": return args[0] ? args[1] ?? true : args[2] ?? false;
    case "AIW_REF": {
      const alias = String(args[0]); const ref = String(args[1]);
      const imp = ctx.imports?.find((i) => i.alias === alias);
      if (!imp || !ctx.resolve) throw new Error("#REF!");
      const v = ctx.resolve(imp.artifact_id, ref);
      if (v === undefined) throw new Error("#REF!");
      return v;
    }
    default: throw new Error("#NAME?");
  }
}

/** Does the formula use AIW_REF (for the "linked" chip)? Returns the alias or null. */
export function refAlias(f?: string): string | null {
  if (!f) return null;
  const m = /AIW_REF\(\s*"([^"]+)"/i.exec(f);
  return m ? m[1] : null;
}

export function formatValue(v: CellValue, fmt?: string): string {
  if (v === null || v === undefined || v === "") return "";
  if (typeof v === "boolean") return v ? "TRUE" : "FALSE";
  if (typeof v !== "number") return String(v);
  if (!fmt) return Number.isInteger(v) ? String(v) : String(Math.round(v * 1e6) / 1e6);
  const dec = (fmt.split(".")[1] || "").replace(/[^0#]/g, "").length;
  if (fmt.includes("%")) return `${(v * 100).toFixed(dec)}%`;
  const s = Math.abs(v).toLocaleString("en-US", { minimumFractionDigits: dec, maximumFractionDigits: dec });
  return `${v < 0 ? "-" : ""}${fmt.startsWith("$") ? "$" : ""}${s}`;
}

/** Re-evaluates every formula and stores the result in `v` (cached value, as the spec requires). */
export function recalcSheets(content: SheetContent, resolve?: RefResolver): SheetContent {
  const sheets = content.sheets.map((s) => ({ ...s, cells: { ...s.cells } }));
  for (const s of sheets) {
    for (const [a, cell] of Object.entries(s.cells)) {
      if (!cell.f) continue;
      const val = cellValue({ sheets, sheet: s, imports: content.imports, resolve }, a);
      if (typeof val === "string" && val.startsWith("#") && cell.v !== undefined) continue; // keep cache on unresolved refs
      s.cells[a] = { ...cell, v: val };
    }
  }
  return { ...content, sheets };
}
