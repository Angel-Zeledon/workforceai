import type { AgentOfficeRow, AgentOfficesView } from "../agentOfficesLogic";
import { GENERAL_ID, isGeneral } from "../agentOfficesLogic";
import type { AgentVisibility, DirectoryEntry, OfficeView, OfficesResponse, Share } from "../officesLogic";

/**
 * The demo's offices and who works where (NEXT_PUBLIC_MOCK=true): General, Contabilidad (private, the person is in),
 * Ventas and Legal (open), Dirección (private, closed to the person). One agent also works in a second office
 * (Nadia, base General, accepted in Contabilidad) and one invitation waits for an answer (Iván in Ventas).
 * The multi-office switch is on in the demo. Same shapes as backend/internal/api/offices*.go (docs/architecture/08-api.md §57).
 */

const me = { user_id: "demo", name: "Demo" };
const office = (o: Partial<OfficeView> & Pick<OfficeView, "id" | "name">): OfficeView => ({
  visibility: "open", general: false, leads: [me], members: [me], member_count: 1, agents: [], can_enter: true, can_manage: true, is_member: true, is_lead: true, folder_path: `/Offices/${o.name}`, ...o,
});

const BASE: Record<string, string> = { sales: "ventas", legal: "legal", accounting: "contabilidad" };

export class MockOfficeWorld {
  enabled = true;
  private base: Record<string, string> = { ...BASE };
  private access: Record<string, { visibility: AgentVisibility; shares: Share[] }> = {};
  private others: Record<string, AgentOfficeRow[]> = this.seedOthers();

  private seedOthers(): Record<string, AgentOfficeRow[]> {
    return {
      analyst: [{ office_id: "contabilidad", name: "Contabilidad", status: "accepted", granted_by: "demo", accepted_by: "demo", can_accept: false, can_remove: true }],
      operations: [{ office_id: "ventas", name: "Ventas", status: "pending", granted_by: "demo", can_accept: true, can_remove: true }],
    };
  }
  reset() { this.base = { ...BASE }; this.access = {}; this.others = this.seedOthers(); }

  /** `agentIds` = every agent of the demo (the ones never placed are in General). */
  offices(agentIds: string[] = Object.keys(BASE)): OfficesResponse {
    const agentsOf = (id: string) => agentIds.filter((a) => this.baseOf(a) === id);
    const list: OfficeView[] = [
      office({ id: GENERAL_ID, name: "General", general: true, folder_path: undefined }),
      office({ id: "contabilidad", name: "Contabilidad", visibility: "private", members: [me, { user_id: "u_laura", name: "Laura Méndez" }], member_count: 2 }),
      office({ id: "ventas", name: "Ventas", leads: [{ user_id: "u_marta", name: "Marta Soto" }], members: [me, { user_id: "u_marta", name: "Marta Soto" }], member_count: 2, is_lead: false }),
      office({ id: "legal", name: "Legal", leads: [{ user_id: "u_legal", name: "Rodrigo Pineda" }], members: [me, { user_id: "u_legal", name: "Rodrigo Pineda" }], member_count: 2, is_lead: false }),
      office({ id: "direccion", name: "Dirección", visibility: "private", leads: [{ user_id: "u_dir", name: "Carmen Ibarra" }], members: [], member_count: 3, can_enter: false, can_manage: false, is_member: false, is_lead: false }),
    ];
    for (const o of list) o.agents = o.can_enter ? agentsOf(o.id) : [];
    return { offices: list, departments: [], restricted: false, me: { user_id: "demo", admin: true, offices: ["general", "contabilidad", "ventas", "legal"] } };
  }

  /** The base office of an agent, "general" when it was never placed. */
  baseOf(agent: string): string { return this.base[agent] ?? GENERAL_ID; }
  setBase(agent: string, officeId: string, visibility: AgentVisibility = "office", shares: Share[] = []) {
    this.base[agent] = isGeneral(officeId) ? GENERAL_ID : officeId;
    this.access[agent] = { visibility, shares };
  }

  directory(agents: readonly { id: string; name: string; role: string; title: string }[]): DirectoryEntry[] {
    const names = new Map(this.offices().offices.map((o) => [o.id, o.name]));
    return agents.map((a) => {
      const oid = this.baseOf(a.id);
      const acc = this.access[a.id] ?? { visibility: "office" as const, shares: [] };
      return {
        id: a.id, name: a.name, role: a.role, title: a.title, owner_kind: "office" as const, owner_id: oid, owner_name: names.get(oid) ?? "General",
        office_id: oid, office_name: names.get(oid) ?? "General", visibility: acc.visibility, shares: acc.shares, explicit: false, rights: { see: true, use: true, edit: true },
      };
    });
  }

  view(agent: string): AgentOfficesView {
    const oid = this.baseOf(agent);
    const name = this.offices().offices.find((o) => o.id === oid)?.name ?? "General";
    return { agent_id: agent, base_office: { id: oid, name }, offices: this.others[agent] ?? [], enabled: this.enabled };
  }
  grant(agent: string, officeId: string): AgentOfficesView {
    const o = this.offices().offices.find((x) => x.id === officeId);
    if (!o) throw new Error(`POST /agents/${agent}/offices -> 404`);
    if (!this.enabled) throw new Error(`POST /agents/${agent}/offices -> 403`);
    if (officeId === this.baseOf(agent)) throw new Error(`POST /agents/${agent}/offices -> 400`);
    const list = (this.others[agent] ??= []);
    if (!list.some((r) => r.office_id === officeId)) list.push({ office_id: officeId, name: o.name, status: o.is_lead ? "accepted" : "pending", granted_by: "demo", can_accept: !o.is_lead, can_remove: true });
    return this.view(agent);
  }
  accept(agent: string, officeId: string): AgentOfficesView {
    const r = (this.others[agent] ?? []).find((x) => x.office_id === officeId);
    if (!r) throw new Error(`POST /agents/${agent}/offices/${officeId}/accept -> 404`);
    r.status = "accepted"; r.can_accept = false; r.accepted_by = "demo";
    return this.view(agent);
  }
  remove(agent: string, officeId: string): AgentOfficesView {
    this.others[agent] = (this.others[agent] ?? []).filter((x) => x.office_id !== officeId);
    return this.view(agent);
  }
}

export const mockOfficeWorld = new MockOfficeWorld();
