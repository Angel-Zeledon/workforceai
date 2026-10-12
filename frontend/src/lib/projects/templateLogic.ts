/**
 * Pure logic of customizable work templates (no React, no imports but erased types; unit-tested with `node --test`):
 * key and placeholder generation, dependency encoding ("key" / "key^list"), builder state <-> TemplateInput conversion,
 * tree edits with the backend limits, and a small expander that mirrors the backend rules (repeat blocks, cartesian
 * products, fan-in, "previous item" chains) for the mock mode and for tests.
 */
import type { Complexity, Risk, TemplateIssue, TemplateInput, TemplateInputNode, TemplateParamType, TemplatePreview } from "./types";

export const TPL_LIMITS = { phases: 30, workflows: 400, tasks: 1000, params: 12, listItems: 50, defaultListItems: 20, mockNodes: 2000 } as const;
export const TEMPLATE_CATEGORIES = ["finance", "operations", "legal", "sales", "marketing", "hr", "it", "compliance", "general"] as const;
export const TASK_KINDS = ["task", "gate", "milestone", "wait"] as const;
export const COMPLEXITIES: Complexity[] = ["S", "M", "L", "XL"];
export const RISKS: Risk[] = ["low", "medium", "high"];
/** issue codes the UI translates (tpl.issue.<code>) */
export const ISSUE_CODES = [
  "invalid_key", "duplicate_key", "unknown_dependency", "self_dependency", "cycle", "unknown_agent", "no_agent", "empty_title", "no_nodes",
  "too_large", "invalid_param", "invalid_repeat", "invalid_approval", "invalid_name", "invalid_complexity", "invalid_kind",
  "max_nodes_per_project", "max_list_items", "max_template_edges",
] as const;
export const DEFAULT_ACTION = "approve_step";

const KEY_RE = /^[A-Za-z][A-Za-z0-9]{0,31}$/;
const PARAM_KEY_RE = /^[a-z][a-zA-Z0-9]{0,23}$/;
export const isValidKey = (k: string) => KEY_RE.test(k);
export const isValidParamKey = (k: string) => PARAM_KEY_RE.test(k) && !k.startsWith("item") && !k.startsWith("index") && k !== "goal";

// ---- builder state ----------------------------------------------------------------------------------------------
export interface BParam { key: string; label: string; type: TemplateParamType; default: string; itemName: string; min: string; max: string; keyEdited: boolean }
export interface BTask {
  key: string; kind: "task" | "gate" | "milestone" | "wait"; title: string; description: string; agent: string; complexity: Complexity;
  /** one criterion per line */
  acceptance: string;
  /** task keys; "key^list" = same block, previous item of that list */
  deps: string[];
  approval: { on: boolean; action: string; risk: Risk };
}
export interface BWorkflow { key: string; title: string; repeat: string[]; tasks: BTask[] }
export interface BPhase { key: string; title: string; workflows: BWorkflow[] }
export interface BuilderState { name: string; description: string; category: string; shared: boolean; params: BParam[]; phases: BPhase[] }

// ---- keys -------------------------------------------------------------------------------------------------------
export function usedKeys(s: BuilderState): Set<string> {
  const used = new Set<string>();
  for (const p of s.phases) { used.add(p.key); for (const w of p.workflows) { used.add(w.key); for (const t of w.tasks) used.add(t.key); } }
  return used;
}
/** first free key `<prefix><n>` (o1, w1, t1...) */
export function nextKey(prefix: string, used: Set<string>): string {
  let n = 1;
  while (used.has(`${prefix}${n}`)) n++;
  const k = `${prefix}${n}`;
  used.add(k);
  return k;
}
/** a valid, unique param key derived from a human label ("Business units" -> "businessUnits") */
export function slugParamKey(label: string, taken: string[]): string {
  const words = label.normalize("NFD").replace(/[̀-ͯ]/g, "").replace(/[^A-Za-z0-9]+/g, " ").trim().split(" ").filter(Boolean);
  let base = words.map((w, i) => (i === 0 ? w.toLowerCase() : w[0].toUpperCase() + w.slice(1).toLowerCase())).join("");
  base = base.replace(/^[0-9]+/, "");
  if (!base) base = "param";
  base = base.slice(0, 22);
  if (base.startsWith("item") || base.startsWith("index") || base === "goal") base = `p${base[0].toUpperCase()}${base.slice(1)}`.slice(0, 22);
  let k = base, n = 2;
  while (taken.includes(k) || !isValidParamKey(k)) { k = `${base}${n++}`; if (n > 99) break; }
  return k;
}

// ---- dependency encoding ----------------------------------------------------------------------------------------
export const encodeDep = (key: string, prevList?: string) => (prevList ? `${key}^${prevList}` : key);
export function decodeDep(dep: string): { key: string; prev?: string } {
  const i = dep.indexOf("^");
  return i < 0 ? { key: dep } : { key: dep.slice(0, i), prev: dep.slice(i + 1) };
}

// ---- lists -------------------------------------------------------------------------------------------------------
/** items of a list value: comma, semicolon or line separated; blanks dropped */
export function splitList(v: string): string[] {
  return (v || "").split(/[,;\n\r]/).map((x) => x.trim()).filter(Boolean);
}
export const listMax = (p: { max?: number }) => (p.max && p.max > 0 ? Math.min(p.max, TPL_LIMITS.listItems) : TPL_LIMITS.defaultListItems);

/** placeholder chips available in a text: every param, plus the item names of the lists the block repeats over */
export function placeholders(s: BuilderState, repeat?: string[]): string[] {
  const out = s.params.filter((p) => p.key).map((p) => `{${p.key}}`);
  for (const k of repeat ?? []) {
    const p = s.params.find((x) => x.key === k);
    if (p && p.type === "list" && p.itemName) out.push(`{${p.itemName}}`);
  }
  return out;
}

// ---- construction -----------------------------------------------------------------------------------------------
export function newTask(used: Set<string>, over: Partial<BTask> = {}): BTask {
  return { key: nextKey("t", used), kind: "task", title: "", description: "", agent: "", complexity: "M", acceptance: "", deps: [], approval: { on: false, action: DEFAULT_ACTION, risk: "medium" }, ...over };
}
export const newWorkflow = (used: Set<string>): BWorkflow => ({ key: nextKey("w", used), title: "", repeat: [], tasks: [newTask(used)] });
export const newPhase = (used: Set<string>): BPhase => ({ key: nextKey("o", used), title: "", workflows: [newWorkflow(used)] });
export function newParam(taken: string[], type: TemplateParamType = "text"): BParam {
  return { key: "", label: "", type, default: "", itemName: "", min: "", max: "", keyEdited: false };
}
export function blankState(): BuilderState {
  return { name: "", description: "", category: "general", shared: true, params: [], phases: [newPhase(new Set())] };
}

export const countOf = (s: BuilderState) => ({
  phases: s.phases.length,
  workflows: s.phases.reduce((n, p) => n + p.workflows.length, 0),
  tasks: s.phases.reduce((n, p) => n + p.workflows.reduce((m, w) => m + w.tasks.length, 0), 0),
  params: s.params.length,
});
export const canAddPhase = (s: BuilderState) => { const c = countOf(s); return c.phases < TPL_LIMITS.phases && c.workflows < TPL_LIMITS.workflows && c.tasks < TPL_LIMITS.tasks; };
export const canAddWorkflow = (s: BuilderState) => { const c = countOf(s); return c.workflows < TPL_LIMITS.workflows && c.tasks < TPL_LIMITS.tasks; };
export const canAddTask = (s: BuilderState) => countOf(s).tasks < TPL_LIMITS.tasks;
export const canAddParam = (s: BuilderState) => s.params.length < TPL_LIMITS.params;

function move<T>(a: T[], i: number, dir: -1 | 1): T[] {
  const j = i + dir;
  if (i < 0 || j < 0 || i >= a.length || j >= a.length) return a;
  const c = a.slice(); [c[i], c[j]] = [c[j], c[i]];
  return c;
}

/** drops dependencies on tasks that no longer exist and repeats over lists that no longer exist */
export function prune(s: BuilderState): BuilderState {
  const keys = new Set<string>();
  for (const p of s.phases) for (const w of p.workflows) for (const t of w.tasks) keys.add(t.key);
  const lists = new Set(s.params.filter((p) => p.type === "list" && p.key).map((p) => p.key));
  return {
    ...s,
    phases: s.phases.map((p) => ({
      ...p,
      workflows: p.workflows.map((w) => {
        const repeat = w.repeat.filter((r) => lists.has(r));
        return {
          ...w, repeat,
          tasks: w.tasks.map((t) => ({ ...t, deps: t.deps.filter((d) => { const x = decodeDep(d); return keys.has(x.key) && x.key !== t.key && (!x.prev || repeat.includes(x.prev)); }) })),
        };
      }),
    })),
  };
}

export const addPhase = (s: BuilderState): BuilderState => (canAddPhase(s) ? { ...s, phases: [...s.phases, newPhase(usedKeys(s))] } : s);
export const removePhase = (s: BuilderState, i: number): BuilderState => prune({ ...s, phases: s.phases.filter((_, x) => x !== i) });
export const movePhase = (s: BuilderState, i: number, dir: -1 | 1): BuilderState => ({ ...s, phases: move(s.phases, i, dir) });

const withPhase = (s: BuilderState, i: number, f: (p: BPhase) => BPhase): BuilderState => ({ ...s, phases: s.phases.map((p, x) => (x === i ? f(p) : p)) });
const withWf = (s: BuilderState, i: number, j: number, f: (w: BWorkflow) => BWorkflow): BuilderState => withPhase(s, i, (p) => ({ ...p, workflows: p.workflows.map((w, x) => (x === j ? f(w) : w)) }));

export const addWorkflow = (s: BuilderState, i: number): BuilderState => (canAddWorkflow(s) ? withPhase(s, i, (p) => ({ ...p, workflows: [...p.workflows, newWorkflow(usedKeys(s))] })) : s);
export const removeWorkflow = (s: BuilderState, i: number, j: number): BuilderState => prune(withPhase(s, i, (p) => ({ ...p, workflows: p.workflows.filter((_, x) => x !== j) })));
export const moveWorkflow = (s: BuilderState, i: number, j: number, dir: -1 | 1): BuilderState => withPhase(s, i, (p) => ({ ...p, workflows: move(p.workflows, j, dir) }));
export const addTask = (s: BuilderState, i: number, j: number, over: Partial<BTask> = {}): BuilderState =>
  (canAddTask(s) ? withWf(s, i, j, (w) => ({ ...w, tasks: [...w.tasks, newTask(usedKeys(s), over)] })) : s);
export const removeTask = (s: BuilderState, i: number, j: number, k: number): BuilderState => prune(withWf(s, i, j, (w) => ({ ...w, tasks: w.tasks.filter((_, x) => x !== k) })));
export const moveTask = (s: BuilderState, i: number, j: number, k: number, dir: -1 | 1): BuilderState => withWf(s, i, j, (w) => ({ ...w, tasks: move(w.tasks, k, dir) }));
export const patchPhase = (s: BuilderState, i: number, patch: Partial<BPhase>): BuilderState => withPhase(s, i, (p) => ({ ...p, ...patch }));
export const patchWorkflow = (s: BuilderState, i: number, j: number, patch: Partial<BWorkflow>): BuilderState => prune(withWf(s, i, j, (w) => ({ ...w, ...patch })));
export const patchTask = (s: BuilderState, i: number, j: number, k: number, patch: Partial<BTask>): BuilderState =>
  withWf(s, i, j, (w) => ({ ...w, tasks: w.tasks.map((t, x) => (x === k ? { ...t, ...patch } : t)) }));

/** a gate gets its approval on by default; leaving gate/task keeps what the user chose */
export function setKind(t: BTask, kind: BTask["kind"]): BTask {
  const approvable = kind === "gate" || kind === "task";
  return { ...t, kind, approval: { ...t.approval, on: approvable ? (kind === "gate" ? true : t.approval.on) : false } };
}

/** params edit: the key follows the label until the user edits the key; removing/renaming prunes the repeats and deps */
export function patchParam(s: BuilderState, i: number, patch: Partial<BParam>): BuilderState {
  const params = s.params.map((p, x) => {
    if (x !== i) return p;
    const n = { ...p, ...patch };
    if (patch.label !== undefined && !n.keyEdited && patch.key === undefined) n.key = n.label.trim() ? slugParamKey(n.label, s.params.filter((_, y) => y !== i).map((q) => q.key)) : "";
    if (patch.key !== undefined) n.keyEdited = true;
    if (n.type === "list" && !n.itemName) n.itemName = defaultItemName(s.params, i);
    return n;
  });
  // keep repeat references when the key of a list param changes
  const old = s.params[i];
  let out: BuilderState = { ...s, params };
  if (old && params[i].key !== old.key && old.key) {
    const to = params[i].key, from = old.key;
    out = { ...out, phases: out.phases.map((p) => ({ ...p, workflows: p.workflows.map((w) => ({
      ...w, repeat: w.repeat.map((r) => (r === from ? to : r)),
      tasks: w.tasks.map((t) => ({ ...t, deps: t.deps.map((d) => { const x = decodeDep(d); return x.prev === from ? encodeDep(x.key, to) : d; }) })),
    })) })) };
  }
  return prune(out);
}
function defaultItemName(params: BParam[], i: number): string {
  const taken = new Set(params.filter((_, x) => x !== i).map((p) => p.itemName));
  let n = 1, name = "item";
  while (taken.has(name)) name = `item${++n}`;
  return name;
}
export const addParam = (s: BuilderState): BuilderState => (canAddParam(s) ? { ...s, params: [...s.params, newParam(s.params.map((p) => p.key))] } : s);
export const removeParam = (s: BuilderState, i: number): BuilderState => prune({ ...s, params: s.params.filter((_, x) => x !== i) });

// ---- conversion -------------------------------------------------------------------------------------------------
/** structural subset of a stored template that the builder can load (built-in or organization) */
export interface LoadableTemplate {
  name: string; name_key?: string; description: string; description_key?: string; category?: string; shared?: boolean;
  params: { key: string; label?: string; label_key?: string; default: string; default_key?: string; type?: string; item_name?: string; min?: number; max?: number }[];
  objectives: { key: string; title?: string; title_key?: string; workflows: { key: string; title?: string; title_key?: string; repeat?: string[]; nodes: {
    key: string; kind?: string; title?: string; title_key?: string; description?: string; agent?: string; complexity?: string; acceptance?: string[]; deps?: string[]; approval?: { action: string; risk: string };
  }[] }[] }[];
}
export function fromTemplate(tpl: LoadableTemplate, tt: (key: string) => string = (k) => k): BuilderState {
  const txt = (lit: string | undefined, key: string | undefined) => lit || (key ? tt(key) : "");
  return {
    name: txt(tpl.name, tpl.name_key), description: txt(tpl.description, tpl.description_key), category: tpl.category || "general", shared: tpl.shared !== false,
    params: tpl.params.map((p) => ({
      key: p.key, label: txt(p.label, p.label_key), type: (p.type === "number" || p.type === "list" ? p.type : "text") as TemplateParamType,
      default: p.default || (p.default_key ? tt(p.default_key) : ""), itemName: p.item_name || "", min: p.min ? String(p.min) : "", max: p.max ? String(p.max) : "", keyEdited: true,
    })),
    phases: tpl.objectives.map((o) => ({
      key: o.key, title: txt(o.title, o.title_key),
      workflows: o.workflows.map((w) => ({
        key: w.key, title: txt(w.title, w.title_key), repeat: w.repeat ? w.repeat.slice() : [],
        tasks: w.nodes.map((n) => {
          const kind = (n.kind === "gate" || n.kind === "milestone" || n.kind === "wait" ? n.kind : "task") as BTask["kind"];
          return {
            key: n.key, kind, title: txt(n.title, n.title_key), description: n.description || "", agent: n.agent || "", complexity: (n.complexity as Complexity) || "M",
            acceptance: (n.acceptance ?? []).join("\n"), deps: (n.deps ?? []).slice(),
            approval: { on: !!n.approval, action: n.approval?.action || DEFAULT_ACTION, risk: (n.approval?.risk as Risk) || (kind === "gate" ? "high" : "medium") },
          };
        }),
      })),
    })),
  };
}

export function toInput(s: BuilderState, extra: { baseVersion?: number; note?: string } = {}): TemplateInput {
  const num = (v: string) => { const n = parseInt(v, 10); return Number.isFinite(n) ? n : undefined; };
  const out: TemplateInput = {
    name: s.name.trim(), description: s.description.trim(), category: s.category || "general", shared: s.shared,
    params: s.params.filter((p) => p.key).map((p) => {
      const o: TemplateInput["params"][number] = { key: p.key, label: p.label.trim() || p.key, type: p.type, default: p.default };
      if (p.type === "list") { if (p.itemName) o.item_name = p.itemName; const m = num(p.max); if (m) o.max = m; }
      if (p.type === "number") { const lo = num(p.min), hi = num(p.max); if (lo !== undefined) o.min = lo; if (hi !== undefined) o.max = hi; }
      return o;
    }),
    objectives: s.phases.map((p) => ({
      key: p.key, title: p.title.trim(),
      workflows: p.workflows.map((w) => ({
        key: w.key, title: w.title.trim(), ...(w.repeat.length ? { repeat: w.repeat.slice() } : {}),
        nodes: w.tasks.map((t): TemplateInputNode => {
          const n: TemplateInputNode = { key: t.key, kind: t.kind, title: t.title.trim() };
          if (t.description.trim()) n.description = t.description.trim();
          if (t.kind === "task" && t.agent.trim()) n.agent = t.agent.trim();
          if (t.kind === "task") n.complexity = t.complexity;
          const acc = t.acceptance.split("\n").map((x) => x.trim()).filter(Boolean);
          if (acc.length) n.acceptance = acc;
          if (t.deps.length) n.deps = t.deps.slice();
          if ((t.kind === "gate" || t.kind === "task") && t.approval.on) n.approval = { action: t.approval.action || DEFAULT_ACTION, risk: t.approval.risk };
          return n;
        }),
      })),
    })),
  };
  if (extra.baseVersion) out.base_version = extra.baseVersion;
  if (extra.note?.trim()) out.note = extra.note.trim();
  return out;
}

/** "invalid template: unknown_agent t3, cycle" -> [{code:"unknown_agent", node_id:"t3"}, {code:"cycle"}] */
export function issuesFromError(detail: string): TemplateIssue[] {
  const body = detail.replace(/^.*?invalid template:\s*/i, "");
  if (body === detail && !/invalid template/i.test(detail)) return [];
  const out: TemplateIssue[] = [];
  for (const part of body.split(",")) {
    const [code, node] = part.trim().split(/\s+/);
    if (code && /^[a-z_]+$/.test(code)) out.push({ severity: "error", code, ...(node ? { node_id: node } : {}) });
  }
  return out;
}
/** code of a "limit_exceeded: max_list_items (limit 12): ..." message, with its limit */
export function limitFromDetail(detail: string): { code: string; limit?: number } | null {
  const m = /limit_exceeded: (\w+)(?: \(limit (\d+)\))?/.exec(detail);
  return m ? { code: m[1], ...(m[2] ? { limit: Number(m[2]) } : {}) } : null;
}

/** tasks whose dependencies (or titles) the builder can offer, with the workflow they belong to */
export function taskIndex(s: BuilderState): { key: string; title: string; repeat: string[] }[] {
  return s.phases.flatMap((p) => p.workflows.flatMap((w) => w.tasks.map((t) => ({ key: t.key, title: t.title, repeat: w.repeat }))));
}

/** local checks the builder can do without the server (the server validates the rest) */
export function localIssues(s: BuilderState): TemplateIssue[] {
  const out: TemplateIssue[] = [];
  if (!s.name.trim()) out.push({ severity: "error", code: "invalid_name" });
  for (const p of s.phases) {
    if (!p.title.trim()) out.push({ severity: "error", node_id: p.key, code: "empty_title" });
    for (const w of p.workflows) {
      if (!w.title.trim()) out.push({ severity: "error", node_id: w.key, code: "empty_title" });
      for (const t of w.tasks) {
        if (!t.title.trim()) out.push({ severity: "error", node_id: t.key, code: "empty_title" });
        if (t.kind === "task" && !t.agent.trim()) out.push({ severity: "error", node_id: t.key, code: "no_agent" });
      }
    }
  }
  return out;
}

// ---- expansion (mirror of the backend rules; used by the mock and the tests) ----------------------------------------
export interface LikeNode { key: string; kind?: string; title?: string; title_key?: string; agent?: string; complexity?: string; deps?: string[]; approval?: unknown }
export interface LikeTemplate {
  name?: string;
  params: { key: string; default: string; default_key?: string; type?: string; item_name?: string; min?: number; max?: number }[];
  objectives: { key: string; title?: string; title_key?: string; workflows: { key: string; title?: string; title_key?: string; repeat?: string[]; nodes: LikeNode[] }[] }[];
}
export interface ExpandedNode { key: string; kind: string; title: string; title_key?: string; agent: string; complexity: string; deps: string[]; approval?: unknown }
export interface Expanded {
  objectives: { key: string; title: string; workflows: { key: string; title: string; nodes: ExpandedNode[] }[] }[];
  values: Record<string, string>;
}
export interface ExpandError { code: string; limit?: number; param?: string }
export type TitleFn = (key: string, vars: Record<string, string>) => string;

/** resolves defaults, clamps numbers and splits lists; an oversized or empty list is an error */
export function resolveParams(params: LikeTemplate["params"], raw: Record<string, string> = {}, tt: TitleFn = (k) => k): { values: Record<string, string>; lists: Record<string, string[]>; error?: ExpandError } {
  const values: Record<string, string> = {}, lists: Record<string, string[]> = {};
  for (const p of params) {
    const v = (raw[p.key] ?? "").trim();
    const def = p.default || (p.default_key ? tt(p.default_key, {}) : "");
    if (p.type === "number") {
      const lo = p.min ?? 0, hi = p.max && p.max > 0 ? p.max : 1000;
      let n = parseInt(v || def, 10);
      if (!Number.isFinite(n)) n = Math.min(Math.max(parseInt(def, 10) || 0, lo), hi);
      values[p.key] = String(Math.min(Math.max(n, lo), hi));
    } else if (p.type === "list") {
      const items = splitList(v || def);
      const max = listMax(p);
      if (!items.length) return { values, lists, error: { code: "invalid_param", param: p.key } };
      if (items.length > max) return { values, lists, error: { code: "max_list_items", limit: max, param: p.key } };
      lists[p.key] = items; values[p.key] = items.join(", ");
    } else values[p.key] = v || def;
  }
  return { values, lists };
}

const subst = (s: string, vars: Record<string, string>) => s.replace(/\{([A-Za-z][A-Za-z0-9_]*)\}/g, (m, k) => (k in vars ? vars[k] : m));

/** the flat plan a template produces for some parameters: one block instance per item (cartesian for two lists), dependencies resolved */
export function expandTemplate(tpl: LikeTemplate, raw: Record<string, string> = {}, tt: TitleFn = (k) => k, maxNodes: number = TPL_LIMITS.mockNodes): { expanded?: Expanded; error?: ExpandError } {
  const { values, lists, error } = resolveParams(tpl.params, raw, tt);
  if (error) return { error };
  const itemOf: Record<string, string> = {};
  let ln = 0;
  for (const p of tpl.params) if (p.type === "list") { ln++; itemOf[p.key] = p.item_name || (ln > 1 ? `item${ln}` : "item"); }

  type Inst = { idx: Record<string, number>; ord: number };
  const wfInfo = new Map<string, { repeat: string[]; insts: Inst[] }>();
  const nodeWf = new Map<string, string>();
  let total = 0;
  for (const o of tpl.objectives) for (const w of o.workflows) {
    const repeat = (w.repeat ?? []).filter((r) => lists[r]);
    let insts: Inst[] = [{ idx: {}, ord: 1 }];
    for (const r of repeat) {
      const next: Inst[] = [];
      for (const b of insts) lists[r].forEach((_, i) => next.push({ idx: { ...b.idx, [r]: i }, ord: 0 }));
      insts = next;
    }
    insts.forEach((x, i) => { x.ord = i + 1; });
    total += insts.length * w.nodes.length;
    if (total > maxNodes) return { error: { code: "max_nodes_per_project", limit: maxNodes } };
    wfInfo.set(w.key, { repeat, insts });
    for (const n of w.nodes) nodeWf.set(n.key, w.key);
  }
  const nodeKey = (k: string, repeated: boolean, ord: number) => (repeated ? `${k}_${ord}` : k);
  const out: Expanded = { objectives: [], values };
  for (const o of tpl.objectives) {
    const eo = { key: o.key, title: subst(o.title ?? (o.title_key ? tt(o.title_key, values) : ""), values), workflows: [] as Expanded["objectives"][number]["workflows"] };
    for (const w of o.workflows) {
      const me = wfInfo.get(w.key)!;
      me.insts.forEach((inst) => {
        const vars = { ...values };
        for (const r of me.repeat) vars[itemOf[r]] = lists[r][inst.idx[r]];
        const ew = { key: nodeKey(w.key, me.repeat.length > 0, inst.ord), title: subst(w.title ?? (w.title_key ? tt(w.title_key, vars) : ""), vars), nodes: [] as ExpandedNode[] };
        for (const n of w.nodes) {
          const deps: string[] = [];
          for (const d of n.deps ?? []) {
            const { key: dk, prev } = decodeDep(d);
            const twf = nodeWf.get(dk);
            if (!twf) continue;
            const t = wfInfo.get(twf)!;
            for (const ti of t.insts) {
              let ok = true;
              for (const r of t.repeat) {
                if (r === prev && me.repeat.includes(r)) { if (ti.idx[r] !== inst.idx[r] - 1) ok = false; }
                else if (me.repeat.includes(r)) { if (ti.idx[r] !== inst.idx[r]) ok = false; }
                if (!ok) break;
              }
              if (ok) deps.push(nodeKey(dk, t.repeat.length > 0, ti.ord));
            }
          }
          ew.nodes.push({
            key: nodeKey(n.key, me.repeat.length > 0, inst.ord), kind: n.kind || "task",
            title: subst(n.title ?? (n.title_key ? tt(n.title_key, vars) : ""), vars), ...(n.title_key && !n.title ? { title_key: n.title_key } : {}),
            agent: n.agent || "", complexity: n.complexity || "M", deps, ...(n.approval ? { approval: n.approval } : {}),
          });
        }
        eo.workflows.push(ew);
      });
    }
    out.objectives.push(eo);
  }
  return { expanded: out };
}

const PRIOR_P50: Record<string, number> = { S: 0.02, M: 0.05, L: 0.12, XL: 0.25 };

/** counts, longest chain, widest level, agents and a rough cost of an expanded plan (the mock "preview") */
export function summarize(name: string, ex: Expanded): TemplatePreview {
  const nodes = ex.objectives.flatMap((o) => o.workflows.flatMap((w) => w.nodes));
  const byKey = new Map(nodes.map((n) => [n.key, n]));
  const depth = new Map<string, number>();
  const visiting = new Set<string>();
  const level = (k: string): number => {
    const known = depth.get(k);
    if (known !== undefined) return known;
    if (visiting.has(k)) return 1;
    visiting.add(k);
    let d = 1;
    for (const dep of byKey.get(k)?.deps ?? []) d = Math.max(d, level(dep) + 1);
    visiting.delete(k);
    depth.set(k, d);
    return d;
  };
  // iterative warm-up (deep chains) in creation order, which is mostly topological
  for (const n of nodes) level(n.key);
  const widths = new Map<number, number>();
  let levels = 0;
  for (const n of nodes) { const d = depth.get(n.key) ?? 1; levels = Math.max(levels, d); widths.set(d, (widths.get(d) ?? 0) + 1); }
  const agents = [...new Set(nodes.map((n) => n.agent).filter(Boolean))].sort();
  let p50 = 0;
  for (const n of nodes) if (n.kind === "task" || !n.kind) p50 += PRIOR_P50[n.complexity] ?? PRIOR_P50.M;
  return {
    name, tasks: nodes.length, gates: nodes.filter((n) => n.kind === "gate").length,
    workflows: ex.objectives.reduce((n, o) => n + o.workflows.length, 0), objectives: ex.objectives.length, nodes: nodes.length + ex.objectives.length,
    levels, parallel: Math.max(0, ...widths.values()), agents,
    estimate: { total: { p50_usd: Math.round(p50 * 1000) / 1000, p90_usd: Math.round(p50 * 2.1 * 1000) / 1000, calls: nodes.length } },
    issues: [], sample: nodes.slice(0, 6).map((n) => n.title),
  };
}

/** stats of a template at default / max parameters (all list params filled to their max with numbered items) */
export function statsOf(tpl: LikeTemplate, tt: TitleFn = (k) => k): { objectives: number; tasks: number; tasks_max: number; gates: number } | null {
  const def = expandTemplate(tpl, {}, tt);
  if (!def.expanded) return null;
  const maxRaw: Record<string, string> = {};
  for (const p of tpl.params) {
    if (p.type === "list") maxRaw[p.key] = Array.from({ length: listMax(p) }, (_, i) => `${i + 1}`).join(",");
    else if (p.type === "number" && p.max) maxRaw[p.key] = String(p.max);
  }
  const mx = expandTemplate(tpl, maxRaw, tt, 1e6);
  const count = (e: Expanded) => e.objectives.reduce((n, o) => n + o.workflows.reduce((m, w) => m + w.nodes.length, 0), 0);
  return {
    objectives: tpl.objectives.length, tasks: count(def.expanded), tasks_max: mx.expanded ? count(mx.expanded) : count(def.expanded),
    gates: def.expanded.objectives.reduce((n, o) => n + o.workflows.reduce((m, w) => m + w.nodes.filter((x) => x.kind === "gate").length, 0), 0),
  };
}

/** structural validation used by the mock `validate` (the real backend has its own, with the same issue codes) */
export function validateLike(tpl: LikeTemplate & { name?: string }, agents: string[]): TemplateIssue[] {
  const out: TemplateIssue[] = [];
  const add = (severity: "error" | "warn", node_id: string | undefined, code: string) => out.push({ severity, ...(node_id ? { node_id } : {}), code });
  if (!tpl.name?.trim()) add("error", undefined, "invalid_name");
  if (!tpl.objectives.length) add("error", undefined, "no_nodes");
  const seen = new Set<string>(), nodeKeys = new Set<string>();
  const check = (k: string) => { if (!isValidKey(k)) add("error", k, "invalid_key"); else if (seen.has(k)) add("error", k, "duplicate_key"); seen.add(k); };
  for (const o of tpl.objectives) { check(o.key); for (const w of o.workflows) { check(w.key); for (const n of w.nodes) { check(n.key); nodeKeys.add(n.key); } } }
  const lists = new Set(tpl.params.filter((p) => p.type === "list").map((p) => p.key));
  for (const p of tpl.params) if (!isValidParamKey(p.key)) add("error", p.key, "invalid_param");
  const edges = new Map<string, string[]>();
  for (const o of tpl.objectives) for (const w of o.workflows) {
    if ((w.repeat ?? []).some((r) => !lists.has(r)) || (w.repeat ?? []).length > 2) add("error", w.key, "invalid_repeat");
    if (!(w.title ?? w.title_key)?.trim()) add("error", w.key, "empty_title");
    for (const n of w.nodes) {
      if (!(n.title ?? n.title_key)?.trim()) add("error", n.key, "empty_title");
      if ((n.kind ?? "task") === "task") { if (!n.agent) add("error", n.key, "no_agent"); else if (agents.length && !agents.includes(n.agent)) add("error", n.key, "unknown_agent"); }
      const ds: string[] = [];
      for (const d of n.deps ?? []) {
        const { key, prev } = decodeDep(d);
        if (key === n.key) add("error", n.key, "self_dependency");
        else if (!nodeKeys.has(key)) add("error", n.key, "unknown_dependency");
        else if (!prev) ds.push(key); // "key^list" (the previous item) chains the items: it is not a cycle
        if (prev && !(w.repeat ?? []).includes(prev)) add("error", n.key, "invalid_repeat");
      }
      edges.set(n.key, [...(edges.get(n.key) ?? []), ...ds]);
    }
  }
  // cycle: DFS colouring
  const color = new Map<string, number>();
  let cyc = false;
  const dfs = (k: string) => { color.set(k, 1); for (const d of edges.get(k) ?? []) { const c = color.get(d); if (c === 1) cyc = true; else if (!c) dfs(d); } color.set(k, 2); };
  for (const k of edges.keys()) if (!color.get(k)) dfs(k);
  if (cyc) add("error", undefined, "cycle");
  return out;
}
