import { call, api, type RoleTemplate } from "./api";
import { MOCK } from "./config";
import type { AgentOfficesView } from "./agentOfficesLogic";
import type { AgentVisibility, Share } from "./officesLogic";
import type { Agent } from "./types";

/** Contract: docs/architecture/08-api.md §57 (agents in several offices, hiring with an office). */

const enc = encodeURIComponent;
const world = () => import("./mock/agent-offices-mock").then((m) => m.mockOfficeWorld);

export interface SuggestedSkill { id: string; name: string; description: string; office_id: string; via: "position" | "role"; position_name?: string; servable: boolean }
export interface HireBody {
  template_id: string; locale: string; name?: string;
  office_id?: string; position_id?: string; skills?: string[]; visibility?: AgentVisibility; shares?: Share[];
}

async function mockSuggested(template: string, office: string, position: string): Promise<SuggestedSkill[]> {
  const role = (id: string, name: string, description: string): SuggestedSkill => ({ id, name, description, office_id: "", via: "role", servable: true });
  const out = [role(`sk-${template}-informe`, "redactar-informe", "Resume hallazgos en un informe corto"), role(`sk-${template}-revision`, "revisar-documento", "Revisa un documento y marca dudas")];
  if (position) out.push({ id: "sk-cierre", name: "cierre-mensual", description: "Lista y pasos del cierre del mes", office_id: office, via: "position", position_name: "Puesto", servable: true });
  return out;
}

export const agentOfficesApi = {
  get: (agent: string): Promise<AgentOfficesView> => (MOCK ? world().then((w) => w.view(agent)) : call("GET", `/agents/${enc(agent)}/offices`)),
  grant: (agent: string, office_id: string): Promise<AgentOfficesView> =>
    (MOCK ? world().then((w) => w.grant(agent, office_id)) : call("POST", `/agents/${enc(agent)}/offices`, { office_id })),
  accept: (agent: string, office: string): Promise<AgentOfficesView> =>
    (MOCK ? world().then((w) => w.accept(agent, office)) : call("POST", `/agents/${enc(agent)}/offices/${enc(office)}/accept`, {})),
  remove: (agent: string, office: string): Promise<AgentOfficesView> =>
    (MOCK ? world().then((w) => w.remove(agent, office)) : call("DELETE", `/agents/${enc(agent)}/offices/${enc(office)}`)),

  suggestedSkills: async (template: string, office: string, position = ""): Promise<SuggestedSkill[]> => {
    if (MOCK) return mockSuggested(template, office, position);
    const q = new URLSearchParams({ ...(office ? { office_id: office } : {}), ...(position ? { position_id: position } : {}) }).toString();
    const r = await call<{ skills?: SuggestedSkill[] }>("GET", `/role-templates/${enc(template)}/suggested-skills${q ? `?${q}` : ""}`);
    return Array.isArray(r?.skills) ? r.skills : [];
  },

  /** POST /agents/from-template with the office, position, skills and who may ask (all optional: nothing = General, as before). */
  hire: async (b: HireBody): Promise<{ agent: Agent; template_id: string }> => {
    const body = { template_id: b.template_id, locale: b.locale, ...(b.name?.trim() ? { name: b.name.trim() } : {}),
      ...(b.office_id && b.office_id !== "general" ? { office_id: b.office_id } : {}), ...(b.position_id ? { position_id: b.position_id } : {}),
      ...(b.skills?.length ? { skills: b.skills } : {}), ...(b.visibility ? { visibility: b.visibility } : {}), ...(b.shares?.length ? { shares: b.shares } : {}) };
    if (MOCK) {
      const res = await api.hireFromTemplate(b.template_id, b.locale, b.name);
      (await world()).setBase(res.agent.id, b.office_id || "general", b.visibility ?? "office", b.shares ?? []);
      return res;
    }
    return call("POST", "/agents/from-template", body);
  },

  /** The multi-office switch lives in each agent's view (`enabled`); one view tells it for the whole company. */
  switchOn: async (probeAgent: string): Promise<boolean> => {
    try { return !!(await agentOfficesApi.get(probeAgent)).enabled; } catch { return false; }
  },
};

export type { RoleTemplate };
