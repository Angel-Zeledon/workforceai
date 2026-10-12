import { MOCK } from "./config";
import { call } from "./api";

/** Long-term memory of an agent (docs/architecture/memory.md, API in 08-api.md): what it produced and
 * remembers, with the source of each entry. The backend never returns what the agent may not read. */
export type MemoryKind = "task" | "report" | "file" | "note";
export interface MemoryEntry {
  id?: string; ref: string; kind: MemoryKind; title: string; snippet: string; agent_id?: string;
  project?: string; source_ref?: string; path?: string; at?: string; deletable: boolean;
}

export const memoryApi = {
  /** Without q: what the agent produced and remembers. With q: what it would recall for that query. */
  async list(agentId: string, q = ""): Promise<MemoryEntry[]> {
    if (MOCK) return [];
    const r = await call<MemoryEntry[]>("GET", `/agents/${encodeURIComponent(agentId)}/memory${q.trim() ? `?q=${encodeURIComponent(q.trim())}` : ""}`);
    return Array.isArray(r) ? r : [];
  },
  async remove(agentId: string, id: string): Promise<void> {
    if (MOCK) return;
    await call("DELETE", `/agents/${encodeURIComponent(agentId)}/memory/${encodeURIComponent(id)}`);
  },
};

/** Where a ref points: files open in the drive; the rest is shown as a label. */
export function refPath(ref: string): string | null {
  return ref.startsWith("file:/") ? ref.slice(5) : null;
}
