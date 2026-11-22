import { ApiError } from "../api";
import {
  clampToSuperior, covers, descendantsOf, exceeds, levelOf,
  type Bounds, type Change, type Impact, type LibraryEntry, type Position, type Template,
} from "../positionsLogic";

/**
 * Positions in mock mode (NEXT_PUBLIC_MOCK=true): the demo's Contabilidad department with its agent, a handful of
 * skills and the same rules as the backend on the cases the screen can reach (name, cycle, the superior's ceiling,
 * dry-run impact, delete re-parenting). Other departments answer 404 like an unknown department.
 */

const OFFICE = { id: "contabilidad", name: "Contabilidad", folder_path: "/Offices/Contabilidad" };
const BOUNDS: Bounds = { office_folder: OFFICE.folder_path, shared: "/Shared", shared_in: [] };
const DEPT_AGENTS = ["accounting"];
const SKILLS = [
  { id: "sk-conciliar", name: "conciliar-bancos", description: "Compara el banco con los registros", office_id: OFFICE.id },
  { id: "sk-factura", name: "emitir-factura", description: "Prepara facturas a clientes", office_id: OFFICE.id },
  { id: "sk-iva", name: "declarar-iva", description: "Arma la declaración de IVA del mes", office_id: OFFICE.id },
  { id: "sk-cierre", name: "cierre-mensual", description: "Lista y pasos del cierre del mes", office_id: "" },
];

const fail = (status: number, code: string): never => { throw new ApiError(`mock -> ${status}`, status, code); };
const now = () => new Date().toISOString();

export class MockPositions {
  private rows: Position[] | null = null;
  private seq = 1;

  reset() { this.rows = null; this.seq = 1; }

  matches(seg: string[]) { return (seg[0] === "offices" && seg[2] === "positions") || (seg[0] === "agents" && seg[2] === "position"); }

  private list(): Position[] {
    if (!this.rows) {
      const mk = (id: string, name: string, reports_to: string, skills: string[], library: LibraryEntry[], template: Template, occupants: string[]): Position =>
        ({ id, office_id: OFFICE.id, name, reports_to, skills, library, template, occupants, created_by: "demo", created_at: now(), updated_by: "demo", updated_at: now() });
      this.rows = [
        mk("pos-gerente", "Gerente de Contabilidad", "", ["sk-conciliar", "sk-factura", "sk-iva", "sk-cierre"],
          [{ path: OFFICE.folder_path, level: "edit" }, { path: "/Shared", level: "read" }], "manager", ["accounting"]),
        mk("pos-asistente", "Asistente de gerente", "pos-gerente", ["sk-factura"], [{ path: OFFICE.folder_path, level: "read" }], "assistant", []),
      ];
    }
    return this.rows;
  }

  private get(id: string): Position { return this.list().find((p) => p.id === id) ?? fail(404, "not_found"); }
  private office(id: string) { if (id !== OFFICE.id) fail(404, "office_unavailable"); }

  private validate(body: Record<string, unknown>, self: Position | null): { name: string; reports_to: string; skills: string[]; library: LibraryEntry[] } {
    const name = String(body.name ?? "").trim();
    if (!name) fail(400, "name_required");
    if (name.length > 60) fail(400, "name_too_long");
    if (this.list().some((p) => p.id !== self?.id && p.name.toLowerCase() === name.toLowerCase())) fail(409, "name_taken");
    const reports_to = String(body.reports_to ?? "");
    let sup: Position | null = null;
    if (reports_to) {
      sup = this.list().find((p) => p.id === reports_to) ?? null;
      if (!sup) fail(400, "reports_to_invalid");
      if (self && (reports_to === self.id || descendantsOf(this.list(), self.id).includes(reports_to))) fail(400, "cycle");
    }
    const skills = Array.isArray(body.skills) ? body.skills.map(String) : [];
    if (skills.some((s) => !SKILLS.some((k) => k.id === s))) fail(400, "skill_invalid");
    const library = (Array.isArray(body.library) ? body.library : []) as LibraryEntry[];
    if (library.length > 50) fail(400, "too_many_folders");
    for (const e of library) {
      if (!e || typeof e.path !== "string" || !["read", "edit", "none"].includes(e.level)) fail(400, "folder_invalid");
      if (![BOUNDS.office_folder, BOUNDS.shared, ...BOUNDS.shared_in].some((r) => covers(r, e.path))) fail(400, "folder_outside");
    }
    if (sup) {
      const x = exceeds(sup, { skills, library }, BOUNDS);
      if (x.skills.length || x.folders.length) throw new ApiError("mock -> 409", 409, "exceeds_superior");
    }
    return { name, reports_to, skills, library };
  }

  /** What the subordinates of `id` lose when it becomes `next`; with `apply` they are trimmed (parents first). */
  private cascade(id: string, next: Position, apply: boolean): Impact {
    const changed: Change[] = [];
    const all = this.list().map((p) => (p.id === id ? next : p));
    const queue = [id];
    while (queue.length) {
      const sup = all.find((p) => p.id === queue.shift())!;
      for (const sub of all.filter((p) => p.reports_to === sup.id)) {
        const x = exceeds(sup, sub, BOUNDS);
        if (x.skills.length || x.folders.length) {
          changed.push({ position_id: sub.id, name: sub.name, skills_removed: x.skills,
            folders: x.folders.map((f) => ({ path: f.path, from: f.level, to: levelOf(sup.library, f.path, BOUNDS) })) });
          if (apply) { const c = clampToSuperior(sup, sub, BOUNDS); sub.skills = c.skills; sub.library = c.library; sub.updated_at = now(); }
        }
        queue.push(sub.id);
      }
    }
    return { changed };
  }

  private agentView(agentId: string) {
    const inDept = DEPT_AGENTS.includes(agentId);
    const position = inDept ? this.list().find((p) => p.occupants.includes(agentId)) ?? null : null;
    return {
      position, office_id: inDept ? OFFICE.id : "", office_name: inDept ? OFFICE.name : "", can_manage: inDept,
      positions: inDept ? this.list().map((p) => ({ id: p.id, name: p.name, reports_to: p.reports_to })) : [],
    };
  }

  private seat(agentId: string, positionId: string) {
    if (!DEPT_AGENTS.includes(agentId)) fail(404, "not_found");
    for (const p of this.list()) p.occupants = p.occupants.filter((a) => a !== agentId);
    if (positionId) { const p = this.list().find((x) => x.id === positionId) ?? fail(404, "not_found"); p.occupants.push(agentId); }
  }

  handle(method: string, seg: string[], body: Record<string, unknown> | undefined, agents: { id: string; name: string }[]): unknown {
    const b = body ?? {};
    if (seg[0] === "agents") {
      const id = seg[1];
      if (method === "PUT") this.seat(id, String(b.position_id ?? ""));
      else if (method === "DELETE") this.seat(id, "");
      return this.agentView(id);
    }
    this.office(seg[1]);
    if (seg[3] === "templates") {
      const gerente = this.list().find((p) => p.template === "manager" || !p.reports_to);
      const sup = gerente ? { skills: gerente.skills, library: gerente.library } : null;
      const manager = { reports_to: "", skills: SKILLS.map((s) => s.id), library: [{ path: OFFICE.folder_path, level: "edit" as const }, { path: "/Shared", level: "read" as const }] };
      const cut = (skills: string[]) => {
        const raw = { skills, library: [{ path: OFFICE.folder_path, level: "read" as const }] };
        return sup ? { reports_to: gerente!.id, ...clampToSuperior(sup, raw, BOUNDS) } : { reports_to: "", ...raw };
      };
      return { templates: [
        { key: "manager", proposal: manager },
        { key: "assistant", proposal: cut([]) },
        { key: "specialist", proposal: cut([]) },
      ] };
    }
    const pid = seg[3];
    if (!pid) {
      if (method === "POST") {
        const v = this.validate(b, null);
        const p: Position = { id: `pos-${++this.seq}-${Date.now() % 1000}`, office_id: OFFICE.id, ...v, template: (b.template as Template) ?? "", occupants: [], created_by: "demo", created_at: now(), updated_by: "demo", updated_at: now() };
        this.list().push(p);
        return p;
      }
      return {
        office: OFFICE, can_manage: true, positions: this.list(),
        agents: agents.filter((a) => DEPT_AGENTS.includes(a.id)).map((a) => ({ id: a.id, name: a.name, position_id: this.list().find((p) => p.occupants.includes(a.id))?.id ?? "" })),
        skills: SKILLS, bounds: BOUNDS,
      };
    }
    const cur = this.get(pid);
    if (method === "PUT") {
      const v = this.validate(b, cur);
      const next: Position = { ...cur, ...v, updated_by: "demo", updated_at: now() };
      const impact = this.cascade(cur.id, next, !b.dry_run);
      if (!b.dry_run) Object.assign(cur, next);
      return { position: b.dry_run ? cur : next, impact };
    }
    if (method === "DELETE") {
      for (const p of this.list()) if (p.reports_to === cur.id) p.reports_to = cur.reports_to;
      this.rows = this.list().filter((p) => p.id !== cur.id);
      return { ok: true };
    }
    return cur;
  }
}
