/**
 * Client of the comments anchored to a passage of a file (F7, docs/architecture/08-api.md sec. 52).
 * In mock mode comments live in memory; a passage is found again by looking for its quote in the text of the version.
 */
import { API_URL, MOCK } from "./config";
import { authFetch } from "./session";
import { drive, DriveError } from "./drive";
import type { FileComment } from "./anchoredComments";

export interface CommentsList { comments: FileComment[]; version: number; current_version: number }
export interface NewComment { body: string; quote?: string; version?: number; parent_id?: string }

export interface DriveCommentsApi {
  list: (fileId: string, version?: number) => Promise<CommentsList>;
  add: (fileId: string, input: NewComment) => Promise<FileComment>;
  resolve: (fileId: string, id: string, resolved: boolean) => Promise<FileComment>;
  /** "Ask {agent} to make the change": a chat request to that agent with the file attached. */
  requestChange: (fileId: string, id: string, agentId: string, lang: string) => Promise<FileComment>;
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await authFetch(`${API_URL}${path}`, {
    method, cache: "no-store",
    ...(body === undefined ? {} : { body: JSON.stringify(body), headers: { "Content-Type": "application/json" } }),
  });
  const text = await res.text();
  const data = text ? (() => { try { return JSON.parse(text); } catch { return text; } })() : {};
  if (!res.ok) throw new DriveError(res.status, data?.code ?? String(res.status), data?.error ?? res.statusText);
  return data as T;
}

const base = (id: string) => `/drive/files/${encodeURIComponent(id)}/comments`;

export const driveComments: DriveCommentsApi = MOCK ? mockComments() : {
  list: (id, version = 0) => call<CommentsList>("GET", `${base(id)}${version ? `?version=${version}` : ""}`).then((r) => ({ ...r, comments: r.comments ?? [] })),
  add: (id, input) => call<FileComment>("POST", base(id), input),
  resolve: (id, cid, resolved) => call<FileComment>("PATCH", `${base(id)}/${encodeURIComponent(cid)}`, { resolved }),
  requestChange: (id, cid, agent_id, lang) => call<FileComment>("POST", `${base(id)}/${encodeURIComponent(cid)}/request-change`, { agent_id, lang }),
};

function mockComments(): DriveCommentsApi {
  const all = new Map<string, FileComment[]>();
  let n = 0;
  const flat = (s: string) => s.replace(/[\s*_`#>~|\\[\]]+/g, "");
  const locate = async (fileId: string, c: FileComment, version: number): Promise<FileComment> => {
    if (!c.anchor) return c;
    const text = (await drive.text(fileId, version).catch(() => ({ text: "" }))).text;
    const found = flat(text).includes(flat(c.anchor.quote));
    return { ...c, location: found ? { status: "intact", start: c.anchor.start, end: c.anchor.end } : { status: "lost", start: -1, end: -1 } };
  };
  return {
    async list(fileId, version = 0) {
      const f = await drive.file(fileId);
      const v = version || f.current_version;
      const comments = await Promise.all((all.get(fileId) ?? []).map((c) => locate(fileId, c, v)));
      return { comments, version: v, current_version: f.current_version };
    },
    async add(fileId, input) {
      const f = await drive.file(fileId);
      const v = input.version || f.current_version;
      const c: FileComment = { id: `fcm_mock${++n}`, file_id: fileId, parent_id: input.parent_id ?? null, author: { kind: "person", id: "you" }, body: input.body.trim(),
        resolved: false, created_at: new Date().toISOString() };
      if (input.quote && !input.parent_id) {
        const text = (await drive.text(fileId, v)).text;
        if (!flat(text).includes(flat(input.quote))) throw new DriveError(400, "anchor_not_in_text", "the quote is not in the text of the version");
        c.anchor = { quote: input.quote, prefix: "", suffix: "", version: v, start: 0, end: 0 };
      }
      all.set(fileId, [...(all.get(fileId) ?? []), c]);
      return c;
    },
    async resolve(fileId, id, resolved) {
      const list = all.get(fileId) ?? [];
      const c = list.find((x) => x.id === id);
      if (!c) throw new DriveError(404, "not_found", "comment not found");
      c.resolved = resolved; c.resolved_by = resolved ? "person:you" : null; c.resolved_version = null;
      return c;
    },
    async requestChange(fileId, id, agentId) {
      const f = await drive.file(fileId);
      const c = (all.get(fileId) ?? []).find((x) => x.id === id);
      if (!c) throw new DriveError(404, "not_found", "comment not found");
      c.change = { agent_id: agentId, base_version: f.current_version, requested_by: "person:you", requested_at: new Date().toISOString() };
      return c;
    },
  };
}
