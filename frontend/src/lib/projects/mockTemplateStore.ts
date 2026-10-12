/**
 * Mock of the template library endpoints (NEXT_PUBLIC_MOCK=true): built-in templates plus an in-memory organization store with
 * versions, duplicate, validate and preview. Same shapes and error semantics as backend/internal/projects/templates_service.go.
 */
import { tr } from "../i18n-core";
import { useStore } from "../store";
import { BUILTIN_TEMPLATES } from "./templates";
import { GIANT_TEMPLATES } from "./mockGiants";
import {
  expandTemplate, fromTemplate, statsOf, summarize, toInput, validateLike, TEMPLATE_CATEGORIES,
  type Expanded, type TitleFn,
} from "./templateLogic";
import type {
  ProjectTemplate, SaveTemplateOpts, TemplateInput, TemplateObjective, TemplatePreview, TemplatePreviewRequest, TemplateValidation, TemplateVersionInfo,
} from "./types";

const ACTOR = "demo";
const tt: TitleFn = (key, vars) => tr(key, vars);

/** mock error that carries what ApiError carries (status + server message) */
export function mockHttpError(status: number, detail: string) {
  return Object.assign(new Error(`${status} ${detail}`), { status, detail });
}

const CATEGORY_OF: Record<string, string> = { financial_close: "finance", branch_opening: "operations", generic: "general" };

/** built-in templates that exist in the backend but are not modeled step by step in the mock: they reuse the generic plan */
function standIn(id: string, key: string, prefix: string, category: string): ProjectTemplate {
  const generic = BUILTIN_TEMPLATES.find((t) => t.key === "generic")!;
  return { ...generic, id, key, name_key: `${prefix}.name`, description_key: `${prefix}.desc`, category, params: [] };
}

function builtins(): ProjectTemplate[] {
  const base = BUILTIN_TEMPLATES.map((t) => ({ ...t, category: CATEGORY_OF[t.key] ?? "general", shared: true }));
  const extra = [
    standIn("tpl-quarterly-cash-close", "quarterly_cash_close", "proj.cc", "finance"),
    standIn("tpl-group-consolidated-close", "group_consolidated_close", "proj.gc", "finance"),
    standIn("tpl-market-expansion", "market_expansion", "proj.me", "operations"),
    standIn("tpl-acquisition-due-diligence", "acquisition_due_diligence", "proj.dd", "legal"),
  ].map((t) => ({ ...t, shared: true }));
  return [...base, ...GIANT_TEMPLATES, ...extra].map((t) => ({ ...t, stats: t.stats ?? statsOf(t, tt) ?? undefined }));
}

interface OrgEntry { tpl: ProjectTemplate; versions: { info: TemplateVersionInfo; snapshot: ProjectTemplate }[] }

let seq = 0;
const nowIso = () => new Date().toISOString();

/** input -> stored template (literal titles, no i18n keys) */
function fromInput(input: TemplateInput): Pick<ProjectTemplate, "name" | "description" | "category" | "params" | "objectives"> {
  return {
    name: input.name.trim(), description: input.description.trim(), category: input.category || "general",
    params: input.params.map((p) => ({ key: p.key, label: p.label, label_key: "", default: p.default, type: p.type, item_name: p.item_name, min: p.min, max: p.max })),
    objectives: input.objectives.map((o): TemplateObjective => ({
      key: o.key, title_key: "", title: o.title,
      workflows: o.workflows.map((w) => ({
        key: w.key, title_key: "", title: w.title, ...(w.repeat?.length ? { repeat: w.repeat } : {}),
        nodes: w.nodes.map((n) => ({ key: n.key, kind: n.kind, title_key: "", title: n.title, description: n.description, agent: n.agent, complexity: n.complexity, acceptance: n.acceptance, deps: n.deps, approval: n.approval })),
      })),
    })),
  };
}

export class MockTemplates {
  private org = new Map<string, OrgEntry>();
  private builtinList = builtins();

  private agents(): string[] { try { return useStore.getState().agentOrder; } catch { return []; } }

  list(): ProjectTemplate[] {
    const org = [...this.org.values()].map((e) => ({ ...e.tpl }));
    return [...this.builtinList, ...org].map((t) => ({
      ...t, name: t.name_key ? tr(t.name_key) : t.name, description: t.description_key ? tr(t.description_key) : t.description,
    }));
  }
  find(id: string): ProjectTemplate | undefined { return this.builtinList.find((t) => t.id === id) ?? this.org.get(id)?.tpl; }
  get(id: string): ProjectTemplate {
    const t = this.list().find((x) => x.id === id);
    if (!t) throw mockHttpError(404, "template not found");
    return t;
  }

  private check(input: TemplateInput) {
    const issues = validateLike({ ...fromInput(input), name: input.name }, this.agents()).filter((i) => i.severity === "error");
    if (issues.length) throw mockHttpError(400, `invalid template: ${issues.map((i) => (i.node_id ? `${i.code} ${i.node_id}` : i.code)).join(", ")}`);
    const lists = input.params.filter((p) => p.type === "list");
    if (lists.some((p) => (p.max ?? 20) > 50)) throw mockHttpError(400, "invalid template: invalid_param");
    if (input.params.length > 12) throw mockHttpError(400, "limit_exceeded: max_params (limit 12): too many parameters");
    const t = { ...fromInput(input) } as ProjectTemplate;
    for (const raw of [{}, Object.fromEntries(lists.map((p) => [p.key, Array.from({ length: p.max ?? 20 }, (_, i) => i + 1).join(",")]))]) {
      const r = expandTemplate(t, raw, tt, 1e6);
      if (r.error?.code === "invalid_param") throw mockHttpError(400, "invalid template: invalid_param");
    }
  }

  private snapshot(entry: OrgEntry, note?: string) {
    entry.versions.unshift({ info: { template_id: entry.tpl.id, version: entry.tpl.version, name: entry.tpl.name, saved_by: ACTOR, saved_at: nowIso(), ...(note ? { note } : {}) }, snapshot: JSON.parse(JSON.stringify(entry.tpl)) });
  }

  create(input: TemplateInput, forkedFrom?: string): ProjectTemplate {
    this.check(input);
    const id = `tpl-org-${Date.now().toString(36)}${(seq++).toString(36)}`;
    const body = fromInput(input);
    const tpl: ProjectTemplate = {
      id, key: `custom_${seq}`, version: 1, builtin: false, ...body, shared: input.shared !== false, ...(forkedFrom ? { forked_from: forkedFrom } : {}),
      created_by: ACTOR, updated_by: ACTOR, created_at: nowIso(), updated_at: nowIso(),
    };
    tpl.stats = statsOf(tpl, tt) ?? undefined;
    const entry: OrgEntry = { tpl, versions: [] };
    this.snapshot(entry, input.note);
    this.org.set(id, entry);
    return { ...tpl };
  }

  update(id: string, input: TemplateInput): ProjectTemplate {
    const e = this.org.get(id);
    if (!e) throw mockHttpError(this.builtinList.some((t) => t.id === id) ? 400 : 404, "a built-in template cannot be edited; duplicate it");
    if (input.base_version && input.base_version !== e.tpl.version) throw mockHttpError(409, "template_conflict: the template changed since you opened it");
    this.check(input);
    e.tpl = { ...e.tpl, ...fromInput(input), shared: input.shared !== false, version: e.tpl.version + 1, updated_by: ACTOR, updated_at: nowIso() };
    e.tpl.stats = statsOf(e.tpl, tt) ?? undefined;
    this.snapshot(e, input.note);
    return { ...e.tpl };
  }

  remove(id: string) {
    if (!this.org.delete(id)) throw mockHttpError(404, "template not found");
  }

  duplicate(id: string, name?: string): ProjectTemplate {
    const src = this.find(id);
    if (!src) throw mockHttpError(404, "template not found");
    const input = toInput(fromTemplate(src, (k) => tr(k)));
    input.name = (name ?? `${input.name} ${tr("tpl.lib.copySuffix")}`).trim();
    input.shared = true;
    // a stand-in (mock only) keeps its own category
    input.category = src.category || "general";
    return this.create(input, src.id);
  }

  versions(id: string): TemplateVersionInfo[] {
    const e = this.org.get(id);
    if (!e) throw mockHttpError(404, "template not found");
    return e.versions.map((v) => v.info);
  }

  restore(id: string, n: number): ProjectTemplate {
    const e = this.org.get(id);
    const v = e?.versions.find((x) => x.info.version === n);
    if (!e || !v) throw mockHttpError(404, "version not found");
    e.tpl = { ...v.snapshot, version: e.tpl.version + 1, updated_by: ACTOR, updated_at: nowIso() };
    this.snapshot(e, `restore v${n}`);
    return { ...e.tpl };
  }

  validate(input: TemplateInput): TemplateValidation {
    const issues = validateLike({ ...fromInput(input), name: input.name }, this.agents());
    return { ok: !issues.some((i) => i.severity === "error"), issues };
  }

  preview(req: TemplatePreviewRequest): TemplatePreview {
    let tpl: ProjectTemplate | undefined; let issues: TemplatePreview["issues"] = [];
    if (req.template) {
      tpl = { id: "", key: "draft", version: 0, builtin: false, ...fromInput(req.template) } as ProjectTemplate;
      issues = validateLike({ ...fromInput(req.template), name: req.template.name }, this.agents());
    } else if (req.template_id) tpl = this.find(req.template_id);
    if (!tpl) throw mockHttpError(404, "template not found");
    const r = expandTemplate(tpl, req.params ?? {}, tt, 2000);
    if (r.error) {
      if (r.error.code === "max_list_items") throw mockHttpError(400, `limit_exceeded: max_list_items (limit ${r.error.limit}): parameter "${r.error.param}"`);
      if (r.error.code === "max_nodes_per_project") throw mockHttpError(400, `limit_exceeded: max_nodes_per_project (limit ${r.error.limit}): the template expands to more nodes`);
      throw mockHttpError(400, `invalid template: ${r.error.code}`);
    }
    const name = tpl.name_key ? tr(tpl.name_key, r.expanded!.values) : tpl.name;
    return { ...summarize(name, r.expanded as Expanded), issues };
  }

  /** expands a template for a project when it has lists/repeats or is an organization template; plain built-ins stay as they are */
  flatten(id: string | undefined, params: Record<string, string>): { tpl: ProjectTemplate; params: Record<string, string> } | null {
    const tpl = id ? this.find(id) : undefined;
    if (!tpl) return null;
    const needs = !tpl.builtin || tpl.params.some((p) => p.type === "list" || p.type === "number") || tpl.objectives.some((o) => o.workflows.some((w) => w.repeat?.length));
    if (!needs) return null;
    const r = expandTemplate(tpl, params, tt, 2000);
    if (r.error || !r.expanded) {
      if (r.error?.code === "max_list_items") throw mockHttpError(400, `limit_exceeded: max_list_items (limit ${r.error.limit}): parameter "${r.error.param}"`);
      throw mockHttpError(400, `limit_exceeded: max_nodes_per_project (limit ${r.error?.limit ?? 0}): the template expands to more nodes`);
    }
    const flat: ProjectTemplate = {
      ...tpl, params: [],
      objectives: r.expanded.objectives.map((o) => ({
        key: o.key, title_key: "", title: o.title,
        workflows: o.workflows.map((w) => ({ key: w.key, title_key: "", title: w.title, nodes: w.nodes.map((n) => ({
          key: n.key, kind: n.kind as never, title_key: "", title: n.title, agent: n.agent || undefined, complexity: n.complexity as never, deps: n.deps,
          approval: n.approval as never,
        })) })),
      })),
    };
    return { tpl: flat, params: r.expanded.values };
  }

  /** "save a project as a template" with options */
  addFromProject(tpl: ProjectTemplate, opts: SaveTemplateOpts = {}): ProjectTemplate {
    const category = opts.category && (TEMPLATE_CATEGORIES as readonly string[]).includes(opts.category) ? opts.category : "general";
    const full: ProjectTemplate = {
      ...tpl, name: opts.name?.trim() || tpl.name, description: opts.description ?? tpl.description, category, shared: opts.shared !== false,
      created_by: ACTOR, updated_by: ACTOR, created_at: nowIso(), updated_at: nowIso(),
    };
    full.stats = statsOf(full, tt) ?? undefined;
    const entry: OrgEntry = { tpl: full, versions: [] };
    this.snapshot(entry);
    this.org.set(full.id, entry);
    return { ...full };
  }
}
