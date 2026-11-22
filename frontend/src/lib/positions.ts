import { call } from "./api";
import { errorKey, type Bounds, type Impact, type LibraryEntry, type Position, type Template } from "./positionsLogic";

export type { Bounds, Change, FolderLoss, Impact, Level, LibraryEntry, Position, Template } from "./positionsLogic";

/** Positions inside a department. Contract: docs/architecture/08-api.md (positions) — /api/v1/offices/{id}/positions
 * and /agents/{id}/position. Errors carry `code` (ApiError.code): show them with positionsLogic.errorKey, never raw. */

export interface PositionsView {
  office: { id: string; name: string; folder_path: string };
  can_manage: boolean;
  positions: Position[];
  /** the agents of the department; position_id "" = none */
  agents: { id: string; name: string; position_id: string }[];
  /** the skills a position of this department may carry */
  skills: { id: string; name: string; description: string; office_id: string }[];
  bounds: Bounds;
}
export interface TemplateProposal { key: Exclude<Template, "">; proposal: { reports_to: string; skills: string[]; library: LibraryEntry[] } }
export interface PositionBody { name: string; reports_to?: string; skills?: string[]; library?: LibraryEntry[]; template?: Template }
export interface AgentPosition {
  position: Position | null; office_id: string; office_name: string; can_manage: boolean;
  positions: { id: string; name: string; reports_to: string }[];
}

const enc = encodeURIComponent;
const base = (office: string) => `/offices/${enc(office)}/positions`;

export const positionsApi = {
  list: (office: string) => call<PositionsView>("GET", base(office)),
  templates: async (office: string) => {
    const r = await call<{ templates: TemplateProposal[] }>("GET", `${base(office)}/templates`);
    return Array.isArray(r?.templates) ? r.templates : [];
  },
  create: (office: string, b: PositionBody) => call<Position>("POST", base(office), b),
  /** `dry_run: true` saves nothing: it only says what the subordinates would lose. */
  update: (office: string, id: string, b: { name: string; reports_to: string; skills: string[]; library: LibraryEntry[]; dry_run?: boolean }) =>
    call<{ position: Position; impact: Impact }>("PUT", `${base(office)}/${enc(id)}`, b),
  remove: (office: string, id: string) => call<{ ok: boolean }>("DELETE", `${base(office)}/${enc(id)}`),
};

export const agentPositionApi = {
  get: (agent: string) => call<AgentPosition>("GET", `/agents/${enc(agent)}/position`),
  set: (agent: string, position_id: string) => call<AgentPosition>("PUT", `/agents/${enc(agent)}/position`, { position_id }),
  clear: (agent: string) => call<AgentPosition>("DELETE", `/agents/${enc(agent)}/position`),
};


/** The sentence to show for a refused call (never the raw code or message). `t` is the screen's translator. */
export function positionError(e: unknown, t: (key: string) => string): string {
  const x = e as { code?: string; status?: number } | null;
  return t(errorKey(x?.code, x?.status));
}
