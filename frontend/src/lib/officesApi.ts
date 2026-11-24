import { call } from "./api";
import { MOCK } from "./config";
import type { AccessInput, AccessView, DirectoryEntry, NoteScope, OfficeView, OfficeVisibility, OfficesResponse, TeamNote } from "./officesLogic";

/** Contract: backend/internal/api/offices.go, offices_agents.go and offices_notes.go (E2 "Oficinas", /api/v1). */

/** Demo: the offices and the agents' base offices come from lib/mock/agent-offices-mock.ts. */
async function mockList(): Promise<OfficesResponse> {
  const [{ mockBackend }, { mockOfficeWorld }] = await Promise.all([import("./mock/engine"), import("./mock/agent-offices-mock")]);
  return mockOfficeWorld.offices(mockBackend.agentList().map((a) => a.id));
}
async function mockDirectory(): Promise<DirectoryEntry[]> {
  const [{ mockBackend }, { mockOfficeWorld }] = await Promise.all([import("./mock/engine"), import("./mock/agent-offices-mock")]);
  return mockOfficeWorld.directory(mockBackend.agentList());
}

async function mockAccess(agent: string): Promise<AccessView> {
  const e = (await mockDirectory()).find((x) => x.id === agent);
  if (!e) throw new Error(`GET /agents/${agent}/access -> 404`);
  return e;
}
async function mockSetAccess(agent: string, b: AccessInput): Promise<AccessView> {
  const { mockOfficeWorld } = await import("./mock/agent-offices-mock");
  const cur = await mockAccess(agent);
  const office = b.owner_kind === "office" && b.owner_id ? b.owner_id : cur.office_id;
  mockOfficeWorld.setBase(agent, office, b.visibility ?? cur.visibility, b.shares ?? cur.shares);
  return mockAccess(agent);
}

export const officesApi = {
  list: (): Promise<OfficesResponse> => (MOCK ? mockList() : call<OfficesResponse>("GET", "/offices")),
  enable: (department_id: string, visibility: OfficeVisibility) => call<OfficeView>("POST", "/offices", { department_id, visibility }),
  setVisibility: (id: string, visibility: OfficeVisibility) => call<OfficeView>("PUT", `/offices/${encodeURIComponent(id)}`, { visibility }),
  disable: (id: string) => call<void>("DELETE", `/offices/${encodeURIComponent(id)}`),
  setMember: (id: string, user: string, role: "lead" | "member") => call<OfficeView>("PUT", `/offices/${encodeURIComponent(id)}/members/${encodeURIComponent(user)}`, { role }),
  removeMember: (id: string, user: string) => call<OfficeView>("DELETE", `/offices/${encodeURIComponent(id)}/members/${encodeURIComponent(user)}`),

  directory: (): Promise<{ agents: DirectoryEntry[]; restricted: boolean }> =>
    (MOCK ? mockDirectory().then((agents) => ({ agents, restricted: false })) : call("GET", "/agents/directory")),
  access: (agent: string) => (MOCK ? mockAccess(agent) : call<AccessView>("GET", `/agents/${encodeURIComponent(agent)}/access`)),
  setAccess: (agent: string, b: AccessInput) => (MOCK ? mockSetAccess(agent, b) : call<AccessView>("PUT", `/agents/${encodeURIComponent(agent)}/access`, b)),

  notes: (scope: NoteScope, ref = ""): Promise<{ notes: TeamNote[]; can_write: boolean; can_promote: boolean }> =>
    (MOCK ? Promise.resolve({ notes: [], can_write: true, can_promote: false })
      : call("GET", `/memory/notes?scope=${scope}&ref=${encodeURIComponent(ref)}`)),
  addNote: (scope: NoteScope, ref: string, text: string) => call<TeamNote>("POST", "/memory/notes", { scope, ref, text }),
  deleteNote: (id: string) => call<void>("DELETE", `/memory/notes/${encodeURIComponent(id)}`),
  promote: (id: string) => call<TeamNote>("POST", `/memory/notes/${encodeURIComponent(id)}/promote`, {}),
};
