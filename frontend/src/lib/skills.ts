/**
 * Skill library client (docs/architecture/skills.md, API in docs/architecture/08-api.md sec. 20).
 * Skills are reusable know-how in the open Agent Skills format. People create, import, export and
 * review them here; agents only receive name + description and ask for the text with skill.load.
 * In mock mode (NEXT_PUBLIC_MOCK=true) an in-memory library with the starter skills is used.
 */
import { create } from "zustand";
import { API_URL, MOCK } from "./config";
import { authFetch } from "./session";
import { getLocale } from "./i18n-core";
import type { SkillDraft, SkillFileEntry, SkillStatus } from "./skillsLogic";

export type { SkillStatus } from "./skillsLogic";
export type SkillSource = "created" | "imported" | "from_work";
export interface Skill {
  id: string; name: string; description: string; status: SkillStatus; source: SkillSource; builtin: boolean;
  current_version: number; active_version: number; active_description?: string;
  created_by: string; created_at: string; updated_at: string; reviewed_by?: string; reviewed_at?: string | null; review_note?: string; agents?: number;
}
export interface SkillWarning { code: "injection" | "script" | "allowed_tools" | "import_note"; path?: string; detail?: string; hits?: string[] }
export interface SkillFileMeta { path: string; size: number; sha256: string; script?: boolean }
export interface SkillVersion {
  skill_id: string; version: number; author: string; created_at: string; changelog: string; description: string;
  allowed_tools: string[]; files: SkillFileMeta[]; warnings: SkillWarning[]; source: string; restored_from?: number; bytes: number;
}
export interface SkillDetail extends Skill {
  version: SkillVersion; instructions: string; skill_md: string; license?: string; metadata?: Record<string, string>;
  allowed_tools: string[]; files: SkillFileEntry[]; assignments: { agent_id: string; assigned_by: string }[];
}
export interface AgentSkill extends Skill { via: "agent" | "role"; assigned_by?: string; servable: boolean }
export interface SkillPayload { name: string; description: string; instructions: string; allowed_tools: string[]; files: SkillFileEntry[]; changelog?: string; source?: string }

/** A skills error with the backend's stable code (name_taken, path_traversal, zip_bomb, stale_review...). */
export class SkillError extends Error {
  constructor(public status: number, public code: string, message: string) { super(message); }
}

async function req<T>(method: string, path: string, body?: BodyInit | null, headers?: Record<string, string>): Promise<T> {
  const res = await authFetch(`${API_URL}${path}`, { method, body: body ?? undefined, headers, cache: "no-store" });
  const text = await res.text();
  const data = text ? (() => { try { return JSON.parse(text); } catch { return text; } })() : {};
  if (!res.ok) throw new SkillError(res.status, data?.code ?? String(res.status), data?.error ?? res.statusText);
  return data as T;
}
const json = (v: unknown) => ({ body: JSON.stringify(v), headers: { "Content-Type": "application/json" } });
const toPayload = (d: SkillDraft, files: SkillFileEntry[]): SkillPayload => ({
  name: d.name, description: d.description, instructions: d.instructions, allowed_tools: d.allowed_tools, files, changelog: d.changelog || undefined, source: d.source,
});
export { toPayload as skillPayload };

function save(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url; a.download = filename; a.click();
  setTimeout(() => URL.revokeObjectURL(url), 5000);
}

export type ImportResult = SkillDetail & { notes: string[] };

export const skillsApi = MOCK ? mockSkills() : {
  list: () => req<Skill[]>("GET", "/skills"),
  get: (id: string, version = 0) => req<SkillDetail>("GET", `/skills/${id}${version ? `?version=${version}` : ""}`),
  create: (p: SkillPayload) => { const j = json(p); return req<SkillDetail>("POST", "/skills", j.body, j.headers); },
  update: (id: string, p: SkillPayload) => { const j = json(p); return req<SkillDetail & { changed: boolean }>("PUT", `/skills/${id}`, j.body, j.headers); },
  versions: (id: string) => req<SkillVersion[]>("GET", `/skills/${id}/versions`),
  restore: (id: string, v: number) => req<SkillDetail>("POST", `/skills/${id}/versions/${v}/restore`),
  submit: (id: string) => req<Skill>("POST", `/skills/${id}/submit`),
  review: (id: string, decision: "approve" | "reject", version: number, note = "") => { const j = json({ decision, version, note }); return req<Skill>("POST", `/skills/${id}/review`, j.body, j.headers); },
  disable: (id: string) => req<Skill>("POST", `/skills/${id}/disable`),
  remove: (id: string) => req<unknown>("DELETE", `/skills/${id}`),
  importFile: (file: Blob, filename: string, name = "") => {
    const fd = new FormData();
    fd.append("file", file, filename);
    if (name) fd.append("name", name);
    return req<ImportResult>("POST", "/skills/import", fd);
  },
  /** Downloads through the session (the token is in memory, not in a cookie). */
  download: async (s: { id: string; name: string }, format: "zip" | "md", version = 0) => {
    const res = await authFetch(`${API_URL}/skills/${s.id}/export?format=${format}${version ? `&version=${version}` : ""}`, { cache: "no-store" });
    if (!res.ok) throw new SkillError(res.status, String(res.status), res.statusText);
    save(await res.blob(), format === "zip" ? `${s.name}.zip` : "SKILL.md");
  },
  agentSkills: (agentId: string) => req<AgentSkill[]>("GET", `/agents/${agentId}/skills`),
  assign: (agentId: string, skillId: string) => req<AgentSkill[]>("PUT", `/agents/${agentId}/skills/${skillId}`),
  unassign: (agentId: string, skillId: string) => req<AgentSkill[]>("DELETE", `/agents/${agentId}/skills/${skillId}`),
};

/** The library screen: open/closed, the skill shown and a prefilled draft ("save as skill"). */
export const useSkillsNav = create<{
  open: boolean; selected: string | "new" | null; prefill: SkillDraft | null; tab: "content" | "review" | "versions" | "agents";
  show: (id?: string | "new" | null, tab?: "content" | "review" | "versions" | "agents") => void; select: (id: string | "new" | null, tab?: "content" | "review" | "versions" | "agents") => void;
  openNew: (prefill: SkillDraft | null) => void; close: () => void; toggle: () => void;
}>((set) => ({
  open: false, selected: null, prefill: null, tab: "content",
  show: (id = null, tab = "content") => set({ open: true, selected: id, tab }),
  select: (id, tab = "content") => set({ selected: id, tab, prefill: null }),
  openNew: (prefill) => set({ open: true, selected: "new", prefill, tab: "content" }),
  close: () => set({ open: false }),
  toggle: () => set((s) => ({ open: !s.open })),
}));

// ------------------------------------------------------------------------------------------
// Mock (NEXT_PUBLIC_MOCK=true): an in-memory library with the lifecycle of the real one.
// ------------------------------------------------------------------------------------------
function mockSkills() {
  type V = Omit<SkillVersion, "files"> & { md: string; instructions: string; files: SkillFileEntry[]; fileMeta: SkillFileMeta[] };
  const es = getLocale() !== "en";
  const store = new Map<string, { skill: Skill; versions: V[]; agents: Set<string> }>();
  const roleDefaults: Record<string, string[]> = {
    accounting: ["bank-reconciliation", "month-end-close", "variance-analysis"], sales: ["b2b-proposal", "client-email"],
    legal: ["service-contract-review"], assistant: ["client-email", "project-plan"], operations: ["project-plan"], analyst: ["variance-analysis"],
  };
  let seq = 0;
  const now = () => new Date().toISOString();
  const sha = (s: string) => { let h = 0; for (const c of s) h = (h * 31 + c.charCodeAt(0)) | 0; return (h >>> 0).toString(16).padStart(8, "0"); };
  const scan = (instructions: string, files: SkillFileEntry[], tools: string[]): SkillWarning[] => {
    const w: SkillWarning[] = [];
    for (const f of [{ path: "SKILL.md", content: instructions }, ...files]) if (/ignore (all )?previous instructions|ignora (todas )?las instrucciones/i.test(f.content)) w.push({ code: "injection", path: f.path, hits: ["ignore_instructions"] });
    for (const f of files) if (/\.(py|js|sh)$/i.test(f.path)) w.push({ code: "script", path: f.path });
    if (tools.length) w.push({ code: "allowed_tools", detail: tools.join(" ") });
    return w;
  };
  const mdOf = (name: string, description: string, instructions: string, tools: string[]) =>
    `---\nname: ${name}\ndescription: ${JSON.stringify(description)}\n${tools.length ? `allowed-tools: ${tools.join(" ")}\n` : ""}---\n\n${instructions.trim()}\n`;
  const addVersion = (id: string, by: string, p: SkillPayload, source: string, changelog: string, restoredFrom = 0): V => {
    const m = store.get(id)!;
    const n = m.skill.current_version + 1;
    const md = mdOf(p.name, p.description, p.instructions, p.allowed_tools);
    const fileMeta = [{ path: "SKILL.md", size: md.length, sha256: sha(md) }, ...p.files.map((f) => ({ path: f.path, size: f.content.length, sha256: sha(f.content), script: /\.(py|js|sh)$/i.test(f.path) }))];
    const v: V = { skill_id: id, version: n, author: by, created_at: now(), changelog, description: p.description, allowed_tools: p.allowed_tools, files: p.files, fileMeta,
      warnings: scan(p.instructions, p.files, p.allowed_tools), source, restored_from: restoredFrom || undefined, bytes: md.length, md, instructions: p.instructions };
    m.versions.push(v);
    m.skill.current_version = n; m.skill.description = p.description; m.skill.updated_at = now();
    return v;
  };
  const make = (p: SkillPayload, o: { status: SkillStatus; source: SkillSource; builtin?: boolean; by: string }) => {
    const id = `skl_mock_${++seq}`;
    const skill: Skill = { id, name: p.name, description: p.description, status: o.status, source: o.source, builtin: !!o.builtin, current_version: 0, active_version: 0,
      created_by: o.by, created_at: now(), updated_at: now() };
    store.set(id, { skill, versions: [], agents: new Set() });
    addVersion(id, o.by, p, o.source, o.builtin ? "Starter skill" : o.source === "imported" ? "Imported" : "Created");
    if (o.status === "active") { skill.active_version = 1; skill.active_description = p.description; skill.reviewed_by = o.by; }
    return skill;
  };
  const S = (name: string, d: [string, string], body: [string, string], files: SkillFileEntry[] = []) =>
    make({ name, description: es ? d[0] : d[1], instructions: es ? body[0] : body[1], allowed_tools: [], files }, { status: "active", source: "created", builtin: true, by: "system" });
  S("bank-reconciliation", ["Concilia un extracto bancario contra el libro mayor y deja las diferencias explicadas.", "Reconcile a bank statement against the ledger and leave the differences explained."],
    ["# Conciliación bancaria\n\n1. Normaliza ambos lados.\n2. Empareja por referencia, luego por importe y fecha.\n3. Clasifica lo que no empareja.\n4. Entrega la tabla de diferencias.\n", "# Bank reconciliation\n\n1. Normalize both sides.\n2. Match by reference, then by amount and date.\n3. Classify what does not match.\n4. Deliver the difference table.\n"],
    [{ path: "templates/conciliacion.csv", content: "fecha,concepto,importe_banco,importe_libro,diferencia,causa\n" }]);
  S("b2b-proposal", ["Redacta una propuesta comercial B2B clara: alcance, entregables, precio y siguientes pasos.", "Write a clear B2B commercial proposal: scope, deliverables, price and next steps."], ["# Propuesta B2B\n\n1. Resumen ejecutivo.\n2. Alcance.\n3. Inversión.\n", "# B2B proposal\n\n1. Executive summary.\n2. Scope.\n3. Investment.\n"]);
  S("service-contract-review", ["Revisa un contrato de servicios y señala cláusulas de riesgo con prioridad.", "Review a services agreement and flag risky clauses by priority."], ["# Revisión de contrato\n\n1. Partes y vigencia.\n2. Checklist de cláusulas.\n3. Riesgo y recomendación.\n", "# Contract review\n\n1. Parties and term.\n2. Clause checklist.\n3. Risk and recommendation.\n"]);
  S("month-end-close", ["Guía el cierre contable mensual con una checklist y evidencias.", "Guide the monthly accounting close with a checklist and evidence."], ["# Cierre mensual\n\n1. Conciliaciones.\n2. Provisiones.\n3. Estados financieros.\n", "# Month-end close\n\n1. Reconciliations.\n2. Accruals.\n3. Statements.\n"]);
  S("variance-analysis", ["Analiza variaciones real contra presupuesto y explica las causas con cifras.", "Analyze actual vs budget variances and explain causes with figures."], ["# Análisis de variaciones\n\n1. Base de comparación.\n2. Precio, volumen y mezcla.\n", "# Variance analysis\n\n1. Comparison base.\n2. Price, volume and mix.\n"]);
  S("client-email", ["Redacta un correo claro a un cliente con un único siguiente paso.", "Write a clear email to a client with a single next step."], ["# Correo a cliente\n\n1. Objetivo.\n2. Asunto específico.\n3. Un siguiente paso.\n", "# Client email\n\n1. Goal.\n2. Specific subject.\n3. One next step.\n"]);
  S("project-plan", ["Convierte un objetivo en un plan de proyecto con hitos, responsables y riesgos.", "Turn a goal into a project plan with milestones, owners and risks."], ["# Plan de proyecto\n\n1. Objetivo.\n2. Hitos y tareas.\n3. Riesgos.\n", "# Project plan\n\n1. Goal.\n2. Milestones and tasks.\n3. Risks.\n"]);

  const err = (status: number, code: string, msg = code) => new SkillError(status, code, msg);
  const get = (id: string) => store.get(id) ?? (() => { throw err(404, "not_found"); })();
  const detail = (id: string, version = 0): SkillDetail => {
    const m = get(id);
    const v = m.versions.find((x) => x.version === (version || m.skill.current_version))!;
    return { ...m.skill, agents: m.agents.size, version: { ...v, files: v.fileMeta } as SkillVersion, instructions: v.instructions, skill_md: v.md, allowed_tools: v.allowed_tools,
      files: v.files, assignments: [...m.agents].map((a) => ({ agent_id: a, assigned_by: "user" })) };
  };
  const servable = (s: Skill) => s.active_version > 0 && (s.status === "active" || s.status === "in_review");
  const agentSkills = (agentId: string, role: string): AgentSkill[] => {
    const out: AgentSkill[] = [];
    for (const m of store.values()) {
      if (m.agents.has(agentId)) out.push({ ...m.skill, via: "agent", servable: servable(m.skill) });
      else if ((roleDefaults[role] ?? []).includes(m.skill.name)) out.push({ ...m.skill, via: "role", servable: servable(m.skill) });
    }
    return out.sort((a, b) => a.name.localeCompare(b.name));
  };
  const roleOf = (agentId: string) => agentId;
  const parseMd = (text: string): SkillPayload => {
    const m = text.replace(/\r\n/g, "\n").match(/^---\n([\s\S]*?)\n---\n?([\s\S]*)$/);
    if (!m) throw err(400, "frontmatter_missing");
    const field = (k: string) => (m[1].match(new RegExp(`^${k}:\\s*(.*)$`, "m"))?.[1] ?? "").trim().replace(/^["']|["']$/g, "");
    const name = field("name"), description = field("description");
    if (!/^[a-z0-9-]{1,64}$/.test(name)) throw err(400, "name_invalid");
    if (!description) throw err(400, "description_required");
    if (!m[2].trim()) throw err(400, "instructions_required");
    return { name, description, instructions: m[2].trim() + "\n", allowed_tools: field("allowed-tools").split(/\s+/).filter(Boolean), files: [] };
  };
  const live = () => [...store.values()].filter((m) => !(m as { deleted?: boolean }).deleted);
  return {
    list: async () => live().map((m) => ({ ...m.skill, agents: m.agents.size })).sort((a, b) => a.name.localeCompare(b.name)),
    get: async (id: string, version = 0) => detail(id, version),
    create: async (p: SkillPayload) => {
      if (live().some((m) => m.skill.name === p.name)) throw err(409, "name_taken");
      const sk = make(p, { status: "draft", source: p.source === "from_work" ? "from_work" : "created", by: "user" });
      return detail(sk.id);
    },
    update: async (id: string, p: SkillPayload) => {
      const m = get(id);
      const cur = m.versions[m.versions.length - 1];
      if (mdOf(p.name, p.description, p.instructions, p.allowed_tools) === cur.md && JSON.stringify(p.files) === JSON.stringify(cur.files)) return { ...detail(id), changed: false };
      addVersion(id, "user", { ...p, name: m.skill.name }, m.skill.source, p.changelog || "Edited");
      if (m.skill.status === "active") { m.skill.active_version = m.skill.current_version; m.skill.active_description = p.description; }
      return { ...detail(id), changed: true };
    },
    versions: async (id: string) => [...get(id).versions].reverse().map((v) => ({ ...v, files: v.fileMeta }) as SkillVersion),
    restore: async (id: string, n: number) => {
      const m = get(id); const old = m.versions.find((v) => v.version === n); if (!old) throw err(404, "not_found");
      addVersion(id, "user", { name: m.skill.name, description: old.description, instructions: old.instructions, allowed_tools: old.allowed_tools, files: old.files }, m.skill.source, `Restored from v${n}`, n);
      return detail(id);
    },
    submit: async (id: string) => { const m = get(id); if (m.skill.status !== "draft") throw err(409, "invalid_state"); m.skill.status = "in_review"; return m.skill; },
    review: async (id: string, decision: "approve" | "reject", version: number, note = "") => {
      const m = get(id);
      if (version !== m.skill.current_version) throw err(409, "stale_review");
      if (decision === "approve") { m.skill.status = "active"; m.skill.active_version = version; m.skill.active_description = m.skill.description; m.skill.review_note = ""; }
      else { m.skill.status = m.skill.active_version > 0 ? "active" : "draft"; m.skill.review_note = note; }
      m.skill.reviewed_by = "user"; m.skill.reviewed_at = now();
      return m.skill;
    },
    disable: async (id: string) => { const m = get(id); m.skill.status = "disabled"; return m.skill; },
    remove: async (id: string) => { (get(id) as { deleted?: boolean }).deleted = true; store.delete(id); return {}; },
    importFile: async (file: Blob, filename: string, name = ""): Promise<ImportResult> => {
      if (!/\.md$/i.test(filename)) throw err(400, "zip_invalid", "mock mode imports only SKILL.md");
      const p = parseMd(await file.text());
      if (name) p.name = name;
      if (live().some((m) => m.skill.name === p.name)) throw err(409, "name_taken");
      const sk = make(p, { status: "in_review", source: "imported", by: "user" });
      return { ...detail(sk.id), notes: [] };
    },
    download: async (s: { id: string; name: string }, _format: "zip" | "md") => {
      const url = URL.createObjectURL(new Blob([get(s.id).versions.slice(-1)[0].md], { type: "text/markdown" }));
      const a = document.createElement("a");
      a.href = url; a.download = "SKILL.md"; a.click();
      setTimeout(() => URL.revokeObjectURL(url), 5000);
    },
    agentSkills: async (agentId: string) => agentSkills(agentId, roleOf(agentId)),
    assign: async (agentId: string, skillId: string) => { get(skillId).agents.add(agentId); return agentSkills(agentId, roleOf(agentId)); },
    unassign: async (agentId: string, skillId: string) => { get(skillId).agents.delete(agentId); return agentSkills(agentId, roleOf(agentId)); },
  };
}
