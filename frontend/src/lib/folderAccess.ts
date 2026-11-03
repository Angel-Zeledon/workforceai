import { MOCK } from "./config";
import { call } from "./api";
import type { AccessEntry, AccessLevel, FolderAgent } from "./folderAccessLogic";

export type { AccessEntry, AccessLevel, FolderAgent } from "./folderAccessLogic";

/** Folder access of agents (API in docs/architecture/08-api.md, "Accesos por agente"). Everyone reads; admins and the
 * head of the agent's team change (can_manage). Mock mode keeps the defaults in memory. */
const mockRules: Record<string, AccessEntry[]> = {};

function mockEntries(agentId: string): AccessEntry[] {
  return [
    { path: `/Agents/${agentId}`, level: "edit", source: "default", fixed: true },
    ...(mockRules[agentId]?.some((r) => r.path === "/Shared") ? [] : [{ path: "/Shared", level: "read", source: "default" } as AccessEntry]),
    ...(mockRules[agentId] ?? []),
  ];
}

export const folderAccess = {
  async agent(agentId: string): Promise<{ entries: AccessEntry[]; can_manage: boolean }> {
    if (MOCK) return { entries: mockEntries(agentId), can_manage: true };
    const r = await call<{ entries: AccessEntry[]; can_manage: boolean }>("GET", `/agents/${encodeURIComponent(agentId)}/folder-access`);
    return { entries: Array.isArray(r?.entries) ? r.entries : [], can_manage: !!r?.can_manage };
  },
  async set(agentId: string, path: string, level: AccessLevel | "default"): Promise<{ entries: AccessEntry[] }> {
    if (MOCK) {
      const rest = (mockRules[agentId] ?? []).filter((r) => r.path !== path);
      mockRules[agentId] = level === "default" ? rest : [...rest, { path, level, source: "manual" }];
      return { entries: mockEntries(agentId) };
    }
    return call<{ entries: AccessEntry[] }>("PUT", `/agents/${encodeURIComponent(agentId)}/folder-access`, { path, level });
  },
  async folder(path: string): Promise<FolderAgent[]> {
    if (MOCK) return [];
    const r = await call<{ agents: FolderAgent[] }>("GET", `/drive/folder-access?path=${encodeURIComponent(path)}`);
    return Array.isArray(r?.agents) ? r.agents : [];
  },
};
