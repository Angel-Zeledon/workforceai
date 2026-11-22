import type {
  OfficeTemplateCard, OfficeTemplateExport, OfficeTemplateFile, OfficeTemplateInstall, OfficeTemplatePreview,
} from "../officeTemplatesLogic";

/** Demo of the office templates (F3): an export of an example accounting department and the imported templates of
 *  this browser session. Nothing is hired in the demo: installing only marks the template as installed. */
const sample: OfficeTemplateFile = {
  format: "aiw.office-template", format_version: 1, kind: "department", name: "Contabilidad",
  for: "Despachos que cierran el mes de varios clientes", solves: "El cierre contable de cada mes, igual en cada cliente",
  departments: [{ key: "d1", name: "Contabilidad" }],
  agents: [
    { department: "d1", role: "accounting", name: "Ana Torres", instructions: "Usa siempre el catálogo de cuentas del cliente.", skills: ["cierre-mensual"] },
    { department: "d1", role: "analyst", name: "Luis Mora" },
  ],
  skills: [{ name: "cierre-mensual" }], workflows: [{ name: "Cierre del cliente" }], schedules: [{ template: "budget_planning" }],
  goals: [{ title: "Cerrar cada mes antes del día 5" }],
};

const store: { id: string; file: OfficeTemplateFile; installed: boolean }[] = [];

function preview(id: string): OfficeTemplatePreview {
  const s = store.find((x) => x.id === id);
  if (!s) throw new Error("not found");
  const st = s.installed ? "existing" : "new";
  return {
    id, name: s.file.name, kind: s.file.kind, for: s.file.for ?? "", solves: s.file.solves ?? "", format_version: 1, office_id: "general",
    departments: s.file.departments.map((d) => ({
      key: d.key, name: d.name, destination: { office_id: "general", office_name: "General", matched: false },
      agents: s.file.agents.filter((a) => a.department === d.key).map((a) => ({
        role: a.role, title: a.role, does: a.does ?? "", status: st, name: s.installed ? "" : a.name ?? a.role, autonomy: "approve_each", skills: [],
      })),
      new_count: s.installed ? 0 : s.file.agents.length, existing_count: s.installed ? s.file.agents.length : 0, skills: [],
    })),
    skills: s.file.skills.map((x) => ({ name: x.name, status: st })),
    workflows: s.file.workflows.map((x) => ({ name: x.name, status: st })),
    schedules: s.file.schedules.map((x) => ({ name: x.template, status: st, detail: "08:30" })),
    goals: s.file.goals.map((x) => ({ name: x.title, status: s.installed ? "existing" : "active" })),
    new_count: s.installed ? 0 : s.file.agents.length, existing_count: s.installed ? s.file.agents.length : 0,
    can_install: !s.installed, blocked_reason: s.installed ? "nothing_new" : "",
  };
}

export const officeTemplatesMock = {
  exportFile: async (scope: { officeId?: string; company?: boolean }): Promise<OfficeTemplateExport> => ({
    file_name: scope.company ? "empresa.aiw-template.json" : "contabilidad.aiw-template.json",
    template: scope.company ? { ...sample, kind: "company", name: "Empresa" } : sample, left_out: [],
  }),
  importFile: async (file: OfficeTemplateFile): Promise<OfficeTemplatePreview> => {
    const id = `otpl-demo-${store.length + 1}`;
    store.unshift({ id, file, installed: false });
    return preview(id);
  },
  list: async (): Promise<OfficeTemplateCard[]> => store.map((s) => ({
    id: s.id, name: s.file.name, kind: s.file.kind, for: s.file.for ?? "", solves: s.file.solves ?? "",
    departments: s.file.departments.map((d) => d.name), agents: s.file.agents.length, skills: s.file.skills.length,
    workflows: s.file.workflows.length, goals: s.file.goals.length,
    new_count: s.installed ? 0 : s.file.agents.length, existing_count: s.installed ? s.file.agents.length : 0,
  })),
  get: async (id: string) => preview(id),
  install: async (id: string): Promise<OfficeTemplateInstall> => {
    const s = store.find((x) => x.id === id);
    if (!s) throw new Error("not found");
    const first = !s.installed;
    s.installed = true;
    return {
      created: first ? s.file.agents.map((a, i) => ({ id: `demo-${i}`, name: a.name ?? a.role, role: a.role })) : [],
      skills: first ? s.file.skills.map((x) => x.name) : [], workflows: first ? s.file.workflows.map((x) => x.name) : [],
      schedules: first ? s.file.schedules.length : 0, goals: first ? s.file.goals.map((g) => ({ name: g.title, status: "active" as const })) : [],
      skipped: [], preview: preview(id),
    };
  },
  remove: async (id: string) => {
    const i = store.findIndex((x) => x.id === id);
    if (i >= 0) store.splice(i, 1);
  },
};
