/**
 * Drive client (docs/architecture/drive.md, API in docs/architecture/08-api.md sec. 19).
 * People browse and change files here; agents only reach the drive through the fs tool.
 * In mock mode an in-memory drive is used: /Shared starts with a few files and each
 * agent's desk shows a draft for every task it finished (what the simulated runtime
 * does against the real backend).
 */
import { create } from "zustand";
import { API_URL, MOCK } from "./config";
import { authFetch } from "./session";
import { useStore } from "./store";
import { draftSlug } from "./mock/draft";

export interface Principal { kind: "human" | "agent" | "system"; id: string }
export interface DriveFolder { id: string; name: string; path: string; root_kind?: string }
export interface DriveFile {
  id: string; name: string; path: string; mime: string; size: number; current_version: number;
  updated_by: Principal; updated_at: string; tags: string[]; deleted_at?: string | null;
}
export interface DriveVersion { file_id: string; version: number; size: number; author: Principal; message: string; created_at: string }
export interface Listing { folder: DriveFolder; folders: DriveFolder[]; files: DriveFile[] }
export interface TextResult { text: string; more: boolean; offset: number; returned: number; info?: { kind?: string; note?: string } }
export interface Hit { file_id: string; path: string; name: string; snippet: string; size: number }
export interface DiffLine { op: string; text: string }
export interface DiffResult { from: number; to: number; added: number; removed: number; lines: DiffLine[]; truncated: boolean }
/** Server-read preview of an office file (xlsx sheets, docx blocks): the browser never parses the package. */
export interface PreviewSheet { name: string; rows: string[][]; truncated?: boolean }
export interface PreviewBlock { type: "heading" | "paragraph" | "bullets" | "table"; level?: number; text?: string; items?: string[]; rows?: string[][] }
export interface Preview { kind: "sheets" | "doc" | "none"; sheets?: PreviewSheet[]; blocks?: PreviewBlock[]; truncated: boolean; note?: string }
export interface Trash { folders: DriveFolder[]; files: DriveFile[] }

/** A drive error with the backend's stable code (version_conflict, read_only_mode, too_large...). */
export class DriveError extends Error {
  constructor(public status: number, public code: string, message: string, public currentVersion?: number) { super(message); }
}

async function req<T>(method: string, path: string, body?: BodyInit | null, headers?: Record<string, string>): Promise<T> {
  const res = await authFetch(`${API_URL}${path}`, { method, body: body ?? undefined, headers, cache: "no-store" });
  const text = await res.text();
  const data = text ? (() => { try { return JSON.parse(text); } catch { return text; } })() : {};
  if (!res.ok) throw new DriveError(res.status, data?.code ?? String(res.status), data?.error ?? res.statusText, data?.current_version);
  return data as T;
}
const q = (p: string) => encodeURIComponent(p);
const json = (v: unknown) => ({ body: JSON.stringify(v), headers: { "Content-Type": "application/json" } });

export const isTextual = (f: { mime: string; name: string }) =>
  /^text\//.test(f.mime) || /json|csv|markdown|xml|yaml/.test(f.mime) || /\.(md|txt|csv|json|ya?ml|log)$/i.test(f.name);

export function contentURL(id: string, version = 0, download = false): string {
  if (MOCK) return "#";
  return `${API_URL}/drive/files/${id}/content?version=${version}${download ? "&download=1" : ""}`;
}

export const drive = MOCK ? mockDrive() : {
  list: (path: string) => req<Listing>("GET", `/drive/list?path=${q(path)}`),
  search: (query: string, path = "") => req<{ hits: Hit[] }>("GET", `/drive/search?q=${q(query)}${path ? `&path=${q(path)}` : ""}`).then((r) => r.hits ?? []),
  text: (id: string, version = 0) => req<TextResult>("GET", `/drive/files/${id}/text?version=${version}&max_chars=20000`),
  versions: (id: string) => req<{ versions: DriveVersion[] }>("GET", `/drive/files/${id}/versions`).then((r) => r.versions ?? []),
  restoreVersion: (id: string, v: number) => req("POST", `/drive/files/${id}/versions/${v}/restore`),
  preview: (id: string, version = 0) => req<{ preview: Preview }>("GET", `/drive/files/${id}/preview?version=${version}`).then((r) => r.preview),
  file: (id: string) => req<DriveFile>("GET", `/drive/files/${id}`),
  /** The file at a path (lists its folder): chat cards may only know the path. */
  byPath: async (path: string): Promise<DriveFile | null> => {
    const l = await req<Listing>("GET", `/drive/list?path=${q(parentOf(path))}`);
    return l.files.find((f) => f.path === path) ?? null;
  },
  diff: (id: string, from: number, to: number) => req<DiffResult>("GET", `/drive/files/${id}/diff?from=${from}&to=${to}`),
  /** New version of a text file; 409 version_conflict when someone saved in between. */
  save: (id: string, text: string, expected: number, message = "") =>
    req<{ file: DriveFile }>("PUT", `/drive/files/${id}/content?expected_version=${expected}${message ? `&message=${q(message)}` : ""}`, text,
      { "Content-Type": "text/plain; charset=utf-8" }),
  /** Raw bytes through the session (images, PDF). */
  blob: async (id: string, version = 0): Promise<Blob> => {
    const res = await authFetch(contentURL(id, version), { cache: "no-store" });
    if (!res.ok) throw new DriveError(res.status, String(res.status), res.statusText);
    return res.blob();
  },
  upload: (path: string, file: Blob, expected?: number) =>
    req<{ file: DriveFile; created: boolean }>("PUT", `/drive/upload?path=${q(path)}${expected ? `&expected_version=${expected}` : ""}`, file,
      { "Content-Type": file.type || "application/octet-stream" }),
  mkdir: (path: string) => { const j = json({ path }); return req<DriveFolder>("POST", "/drive/folders", j.body, j.headers); },
  remove: (id: string) => req("DELETE", `/drive/files/${id}`),
  restore: (id: string) => req("POST", `/drive/files/${id}/restore`),
  trash: () => req<Trash>("GET", "/drive/trash"),
  /** Downloads through the session (the token is in memory, not in a cookie). */
  download: async (f: DriveFile) => {
    const res = await authFetch(contentURL(f.id, 0, true), { cache: "no-store" });
    if (!res.ok) throw new DriveError(res.status, String(res.status), res.statusText);
    const url = URL.createObjectURL(await res.blob());
    const a = document.createElement("a");
    a.href = url; a.download = f.name; a.click();
    setTimeout(() => URL.revokeObjectURL(url), 5000);
  },
};

// ------------------------------------------------------------------------------------------
// Mock (NEXT_PUBLIC_MOCK=true): an in-memory drive good enough for demos and screenshots.
// ------------------------------------------------------------------------------------------
function mockDrive() {
  type Mem = { file: DriveFile; versions: { v: DriveVersion; text: string }[] };
  const files = new Map<string, Mem>();
  const now = () => new Date().toISOString();
  const human: Principal = { kind: "human", id: "user" };
  let seq = 0;
  const put = (path: string, text: string, by: Principal, message = "") => {
    const cur = [...files.values()].find((m) => m.file.path === path && !m.file.deleted_at);
    if (cur) {
      if (cur.versions[cur.versions.length - 1].text === text) return cur.file;
      const n = cur.file.current_version + 1;
      cur.versions.push({ v: { file_id: cur.file.id, version: n, size: text.length, author: by, message, created_at: now() }, text });
      Object.assign(cur.file, { current_version: n, size: text.length, updated_by: by, updated_at: now() });
      return cur.file;
    }
    const id = `fil_mock_${++seq}`;
    const name = path.split("/").pop() || "file";
    const mime = name.endsWith(".csv") ? "text/csv" : name.endsWith(".md") ? "text/markdown" : name.endsWith(".json") ? "application/json" : "text/plain";
    const file: DriveFile = { id, name, path, mime, size: text.length, current_version: 1, updated_by: by, updated_at: now(), tags: [] };
    files.set(id, { file, versions: [{ v: { file_id: id, version: 1, size: text.length, author: by, message, created_at: now() }, text }] });
    return file;
  };
  put("/Shared/Politicas/politica-de-descuentos.md", "# Política de descuentos\n\nHasta 10% sin aprobación; más requiere Dirección.\n", human);
  put("/Shared/Finanzas/saldos-bancarios.csv", "cuenta,banco,saldo\nOperativa,BAC,128400\nNómina,BN,56300\nUSD,BCR,40200\n", human);
  put("/Clients/Grupo Alfa/brief.md", "# Brief Grupo Alfa\n\nRediseño completo, presupuesto USD 50,000, entrega en 8 semanas.\n", human);

  /** Each finished task leaves its draft on the agent's desk, as the simulated runtime does. */
  const drafted = new Set<string>(); // one draft per task: later edits by people are not overwritten
  const syncDesks = () => {
    const s = useStore.getState();
    for (const t of Object.values(s.tasks)) {
      if (t.status !== "done" || !t.agent_id || !t.output || drafted.has(t.id)) continue;
      drafted.add(t.id);
      const slug = draftSlug(t.title);
      const body = [`# ${t.title}`, "", t.output.summary ?? "", ...(t.output.findings?.length ? ["", "## Hallazgos", ...t.output.findings.map((x) => `- ${x}`)] : [])].join("\n");
      put(`/Agents/${t.agent_id}/${slug}.md`, body + "\n", { kind: "agent", id: t.agent_id }, `borrador de ${t.id}`);
    }
  };
  const live = () => [...files.values()].filter((m) => !m.file.deleted_at);
  const folderOf = (p: string) => p.split("/").slice(0, -1).join("/") || "/";
  const list = async (path: string): Promise<Listing> => {
    syncDesks();
    const base = path === "/" ? "" : path.replace(/\/$/, "");
    const sub = new Set<string>();
    const out: DriveFile[] = [];
    for (const m of live()) {
      if (!m.file.path.startsWith(base + "/")) continue;
      const rest = m.file.path.slice(base.length + 1).split("/");
      if (rest.length === 1) out.push(m.file); else sub.add(rest[0]);
    }
    if (path === "/") ["Agents", "Projects", "Clients", "Shared"].forEach((r) => sub.add(r));
    const name = base.split("/").pop() || "/";
    return {
      folder: { id: `fld:${path}`, name, path: path || "/" },
      folders: [...sub].sort().map((n) => ({ id: `fld:${base}/${n}`, name: n, path: `${base}/${n}` })),
      files: out.sort((a, b) => a.name.localeCompare(b.name)),
    };
  };
  const byId = (id: string) => {
    const m = files.get(id);
    if (!m) throw new DriveError(404, "not_found", "file not found");
    return m;
  };
  return {
    list,
    search: async (query: string, path = "") => {
      syncDesks();
      const qq = query.toLowerCase();
      return live().filter((m) => (!path || m.file.path.startsWith(path)) &&
        (m.file.name.toLowerCase().includes(qq) || m.versions[m.versions.length - 1].text.toLowerCase().includes(qq)))
        .map((m) => ({ file_id: m.file.id, path: m.file.path, name: m.file.name, size: m.file.size, snippet: m.versions[m.versions.length - 1].text.slice(0, 120) }));
    },
    text: async (id: string, version = 0) => {
      const m = byId(id);
      const v = version ? m.versions.find((x) => x.v.version === version) : m.versions[m.versions.length - 1];
      return { text: v?.text ?? "", more: false, offset: 0, returned: v?.text.length ?? 0 } as TextResult;
    },
    versions: async (id: string) => byId(id).versions.map((x) => x.v).reverse(),
    preview: async (): Promise<Preview> => ({ kind: "none", truncated: false, note: "mock" }),
    file: async (id: string) => byId(id).file,
    byPath: async (path: string) => { syncDesks(); return live().find((m) => m.file.path === path)?.file ?? null; },
    diff: async (id: string, from: number, to: number): Promise<DiffResult> => {
      const m = byId(id);
      const a = (m.versions.find((x) => x.v.version === from)?.text ?? "").split("\n"), b = (m.versions.find((x) => x.v.version === to)?.text ?? "").split("\n");
      const lines: DiffLine[] = [];
      let added = 0, removed = 0;
      // line diff good enough for a demo: keep common lines, mark the rest
      const bs = new Set(b), as = new Set(a);
      for (const l of a) { if (bs.has(l)) lines.push({ op: " ", text: l }); else { lines.push({ op: "-", text: l }); removed++; } }
      for (const l of b) if (!as.has(l)) { lines.push({ op: "+", text: l }); added++; }
      return { from, to, added, removed, lines, truncated: false };
    },
    save: async (id: string, text: string, expected: number, message = "") => {
      const m = byId(id);
      if (m.file.current_version !== expected) throw new DriveError(409, "version_conflict", "version conflict", m.file.current_version);
      return { file: put(m.file.path, text, human, message || "edit") };
    },
    blob: async (id: string, version = 0) => {
      const m = byId(id);
      const v = version ? m.versions.find((x) => x.v.version === version) : m.versions[m.versions.length - 1];
      return new Blob([v?.text ?? ""], { type: m.file.mime });
    },
    restoreVersion: async (id: string, v: number) => {
      const m = byId(id);
      const old = m.versions.find((x) => x.v.version === v);
      if (old) put(m.file.path, old.text, human, `restored v${v}`);
      return {};
    },
    upload: async (path: string, file: Blob) => {
      const created = !live().some((m) => m.file.path === path);
      return { file: put(path, await file.text(), human, "upload"), created };
    },
    mkdir: async (path: string) => ({ id: `fld:${path}`, name: path.split("/").pop() || "", path }),
    remove: async (id: string) => { byId(id).file.deleted_at = now(); return {}; },
    restore: async (id: string) => { byId(id).file.deleted_at = null; return {}; },
    trash: async () => ({ folders: [], files: [...files.values()].filter((m) => m.file.deleted_at).map((m) => m.file) }),
    download: async (f: DriveFile) => {
      const url = URL.createObjectURL(new Blob([byId(f.id).versions.slice(-1)[0].text], { type: f.mime }));
      const a = document.createElement("a");
      a.href = url; a.download = f.name; a.click();
      setTimeout(() => URL.revokeObjectURL(url), 5000);
    },
    folderOf,
  };
}

/** Parent folder of a path ("/Shared/a.md" -> "/Shared"). */
export const parentOf = (p: string) => p.split("/").slice(0, -1).join("/") || "/";

/** The "Files" screen: open/closed and the folder to show (set from an agent's desk, a project...). */
export const useDriveNav = create<{ open: boolean; path: string; show: (path: string) => void; close: () => void; toggle: () => void }>((set) => ({
  open: false, path: "/",
  show: (path) => set({ open: true, path }),
  close: () => set({ open: false }),
  toggle: () => set((s) => ({ open: !s.open })),
}));
