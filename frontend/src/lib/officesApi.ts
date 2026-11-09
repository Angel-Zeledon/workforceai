import { call } from "./api";
import { MOCK } from "./config";
import type { AccessInput, AccessView, DirectoryEntry, NoteScope, OfficeView, OfficeVisibility, OfficesResponse, TeamNote } from "./officesLogic";

/** Contract: backend/internal/api/offices.go, offices_agents.go and offices_notes.go (E2 "Oficinas", /api/v1). */

const mockOffices: OfficesResponse = {
  offices: [{ id: "general", name: "General", visibility: "open", general: true, leads: [{ user_id: "demo", name: "Demo" }], members: [{ user_id: "demo", name: "Demo" }],
    member_count: 1, agents: [], can_enter: true, can_manage: true, is_member: true, is_lead: true }],
  departments: [], restricted: false, me: { user_id: "demo", admin: true, offices: ["general"] },
};

/** Demo: the directory lists the demo's own agents (owned by the General office), so the Agents tab is not empty. */
async function mockDirectory(): Promise<DirectoryEntry[]> {
  const { mockBackend } = await import("./mock/engine");
  return mockBackend.agentList().map((a) => ({
    id: a.id, name: a.name, role: a.role, title: a.title,
    owner_kind: "office" as const, owner_id: "general", owner_name: "General", office_id: "general", office_name: "General",
    visibility: "office" as const, shares: [], explicit: false, rights: { see: true, use: true, edit: true },
  }));
}

export const officesApi = {
  list: (): Promise<OfficesResponse> => (MOCK ? Promise.resolve(mockOffices) : call<OfficesResponse>("GET", "/offices")),
  enable: (department_id: string, visibility: OfficeVisibility) => call<OfficeView>("POST", "/offices", { department_id, visibility }),
  setVisibility: (id: string, visibility: OfficeVisibility) => call<OfficeView>("PUT", `/offices/${encodeURIComponent(id)}`, { visibility }),
  disable: (id: string) => call<void>("DELETE", `/offices/${encodeURIComponent(id)}`),
  setMember: (id: string, user: string, role: "lead" | "member") => call<OfficeView>("PUT", `/offices/${encodeURIComponent(id)}/members/${encodeURIComponent(user)}`, { role }),
  removeMember: (id: string, user: string) => call<OfficeView>("DELETE", `/offices/${encodeURIComponent(id)}/members/${encodeURIComponent(user)}`),

  directory: (): Promise<{ agents: DirectoryEntry[]; restricted: boolean }> =>
    (MOCK ? mockDirectory().then((agents) => ({ agents, restricted: false })) : call("GET", "/agents/directory")),
  access: (agent: string) => call<AccessView>("GET", `/agents/${encodeURIComponent(agent)}/access`),
  setAccess: (agent: string, b: AccessInput) => call<AccessView>("PUT", `/agents/${encodeURIComponent(agent)}/access`, b),

  notes: (scope: NoteScope, ref = ""): Promise<{ notes: TeamNote[]; can_write: boolean; can_promote: boolean }> =>
    (MOCK ? Promise.resolve({ notes: [], can_write: true, can_promote: false })
      : call("GET", `/memory/notes?scope=${scope}&ref=${encodeURIComponent(ref)}`)),
  addNote: (scope: NoteScope, ref: string, text: string) => call<TeamNote>("POST", "/memory/notes", { scope, ref, text }),
  deleteNote: (id: string) => call<void>("DELETE", `/memory/notes/${encodeURIComponent(id)}`),
  promote: (id: string) => call<TeamNote>("POST", `/memory/notes/${encodeURIComponent(id)}/promote`, {}),
};
