/**
 * Drive client (docs/architecture/drive.md, API in docs/architecture/08-api.md sec. 19).
 * People browse and change files here; agents only reach the drive through the fs tool.
 * In mock mode an in-memory drive is used: /Shared starts with a few files and each
 * agent's desk shows a draft for every task it finished (what the simulated runtime
 * does against the real backend).
 */
import { go } from "@/lib/navBridge";
import { useEffect, useState } from "react";
import { create } from "zustand";
import { API_URL, MOCK } from "./config";
import { authFetch, authUpload, type UploadProgress } from "./session";
import { useStore } from "./store";
import { draftSlug } from "./mock/draft";

/** "inbox" = a data inbox: software outside the platform pushed the content (lib/dataInboxLogic.ts). */
export interface Principal { kind: "human" | "agent" | "system" | "inbox"; id: string }
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
/** Storage of the organization; max_upload_bytes is the cap of one uploaded file (bigger with S3). */
export interface DriveUsage { used_bytes: number; quota_bytes: number; max_file_bytes: number; max_upload_bytes?: number }
/** One file of a multipart upload (POST /drive/upload): each file succeeds or fails on its own. */
export interface UploadResult { name: string; path?: string; status: number; code?: string; error?: string; created?: boolean; result?: { file: DriveFile } }
export type { UploadProgress };

/** A drive error with the backend's stable code (version_conflict, read_only_mode, too_large...). */
export class DriveError extends Error {
  constructor(public status: number, public code: string, message: string, public currentVersion?: number) { super(message); }
}

async function req<T>(method: string, path: string, body?: BodyInit | null, headers?: Record<string, string>): Promise<T> {
  return parse<T>(await authFetch(`${API_URL}${path}`, { method, body: body ?? undefined, headers, cache: "no-store" }));
}
async function parse<T>(res: Response): Promise<T> {
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
  /** One file, streamed by the server straight to storage; onProgress reports the bytes sent. */
  upload: async (path: string, file: Blob, expected?: number, onProgress?: UploadProgress, signal?: AbortSignal) =>
    parse<{ file: DriveFile; created: boolean }>(await authUpload(`${API_URL}/drive/upload?path=${q(path)}${expected ? `&expected_version=${expected}` : ""}`,
      { method: "PUT", body: file, headers: { "Content-Type": file.type || "application/octet-stream" }, signal }, onProgress)),
  /**
   * Several files in one request (multipart): `name` may carry a relative path ("sub/a.txt", folder uploads).
   * Resolves with one result per file; a file that fails does not undo the others.
   */
  uploadMany: async (folder: string, files: { name: string; file: Blob }[], onProgress?: UploadProgress, signal?: AbortSignal) => {
    const fd = new FormData();
    for (const f of files) fd.append("file", f.file, f.name);
    const r = await parse<{ results: UploadResult[] }>(await authUpload(`${API_URL}/drive/upload?folder=${q(folder)}`, { method: "POST", body: fd, signal }, onProgress));
    return r.results ?? [];
  },
  usage: () => req<DriveUsage>("GET", "/drive/usage"),
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
  /** `at`: when it happened (demo data in the past); now by default. */
  const put = (path: string, text: string, by: Principal, message = "", at = now()) => {
    const cur = [...files.values()].find((m) => m.file.path === path && !m.file.deleted_at);
    if (cur) {
      if (cur.versions[cur.versions.length - 1].text === text) return cur.file;
      const n = cur.file.current_version + 1;
      cur.versions.push({ v: { file_id: cur.file.id, version: n, size: text.length, author: by, message, created_at: at }, text });
      Object.assign(cur.file, { current_version: n, size: text.length, updated_by: by, updated_at: at });
      return cur.file;
    }
    const id = `fil_mock_${++seq}`;
    const name = path.split("/").pop() || "file";
    const mime = name.endsWith(".csv") ? "text/csv" : name.endsWith(".md") ? "text/markdown" : name.endsWith(".json") ? "application/json" : "text/plain";
    const file: DriveFile = { id, name, path, mime, size: text.length, current_version: 1, updated_by: by, updated_at: at, tags: [] };
    files.set(id, { file, versions: [{ v: { file_id: id, version: 1, size: text.length, author: by, message, created_at: at }, text }] });
    return file;
  };
  const ago = (hours: number) => new Date(Date.now() - hours * 3600_000).toISOString();
  put("/Shared/Politicas/politica-de-descuentos.md", "# Política de descuentos\n\nHasta 10% sin aprobación; más requiere Dirección.\n", human);
  put("/Shared/Finanzas/saldos-bancarios.csv", "cuenta,banco,saldo\nOperativa,BAC,128400\nNómina,BN,56300\nUSD,BCR,40200\n", human);
  put("/Clients/Grupo Alfa/brief.md", "# Brief Grupo Alfa\n\nRediseño completo, presupuesto USD 50,000, entrega en 8 semanas.\n", human);
  // Office folders demo: the private Contabilidad office has its folder with a client's files
  put("/Offices/Contabilidad/Clientes/Grupo Alfa/estado-de-cuenta.csv", "concepto,monto\nServicios octubre,12500\nAnticipo,-5000\n", human);
  put("/Offices/Contabilidad/Clientes/Grupo Alfa/notas-de-cierre.md", "# Cierre de octubre\n\nPendiente conciliar el anticipo.\n", human);
  // D2 demo: the department's Drive. Contabilidad has the accounting template (Clientes, Proveedores, Bancos y
  // conciliaciones, Impuestos, Cierres mensuales, Políticas) with files by people and by its agent (Tomás Vidal).
  const acc: Principal = { kind: "agent", id: "accounting" };
  const C = "/Offices/Contabilidad";
  put(`${C}/resumen-semanal.md`, "# Resumen semanal de Contabilidad\n\n- Cobros de la semana registrados.\n- Conciliación de octubre en revisión.\n- Declaración de IVA lista para presentar.\n", acc, "resumen de la semana", ago(0.7));
  put(`${C}/plan-de-cuentas.csv`, "cuenta,nombre,tipo\n1101,Caja,activo\n1102,Bancos,activo\n2101,Proveedores,pasivo\n4101,Ventas,ingreso\n5101,Costo de ventas,gasto\n", human, "", ago(24 * 15));
  put(`${C}/Clientes/Distribuidora Norte/contrato-servicios.md`, "# Contrato de servicios — Distribuidora Norte\n\nVigencia: 12 meses. Facturación mensual a 30 días.\n", human, "", ago(24 * 3));
  put(`${C}/Proveedores/lista-de-proveedores.csv`, "proveedor,plazo,contacto\nPapelería Central,30 días,ventas@papeleria.example\nTransportes Ruta 1,15 días,cobros@ruta1.example\n", human, "", ago(24 * 6));
  put(`${C}/Proveedores/facturas-por-pagar-noviembre.csv`, "proveedor,factura,vence,monto\nPapelería Central,F-2201,2026-11-20,850\nTransportes Ruta 1,TR-993,2026-11-14,1240\n", acc, "facturas de noviembre", ago(2));
  put(`${C}/Bancos y conciliaciones/extracto-bac-octubre.csv`, "fecha,concepto,monto\n2026-10-03,Depósito Grupo Alfa,5000\n2026-10-15,Pago planilla,-18200\n2026-10-28,Comisión bancaria,-12\n", human, "", ago(26));
  put(`${C}/Bancos y conciliaciones/conciliacion-septiembre.md`, "# Conciliación bancaria — septiembre\n\nSaldo según banco y según libros coinciden. Sin partidas abiertas.\n", acc, "conciliación de septiembre", ago(24 * 12));
  put(`${C}/Impuestos/declaracion-iva-octubre.md`, "# Declaración de IVA — octubre\n\nDébito fiscal: 4.180\nCrédito fiscal: 2.960\nA pagar: 1.220\n", acc, "borrador de la declaración", ago(24 * 4));
  put(`${C}/Cierres mensuales/cierre-septiembre.md`, "# Cierre de septiembre\n\nCerrado el 5 de octubre. Ajustes: depreciación y provisión de vacaciones.\n", acc, "cierre de septiembre", ago(24 * 9));
  put(`${C}/Cierres mensuales/checklist-de-cierre.md`, "# Checklist de cierre mensual\n\n1. Conciliar bancos\n2. Registrar provisiones\n3. Revisar cuentas por cobrar\n4. Emitir estados financieros\n", human, "", ago(24 * 20));
  put(`${C}/Políticas/politica-de-reembolsos.md`, "# Política de reembolsos de gastos\n\nSe reembolsan gastos con factura a nombre de la empresa, presentados en 30 días.\n", human, "", ago(24 * 30));
  // B6 demo: the capacity cost the accountant cites is in version 2 of this file (version 1 had an older figure)
  put("/Shared/Finanzas/costo-capacidad.csv", "concepto,mensual\nPerfil senior ops,3600\nPerfil senior ops,3600\n", human);
  put("/Shared/Finanzas/costo-capacidad.csv", "concepto,mensual\nPerfil senior ops (cargado),3900\nPerfil senior ops (cargado),3900\nTotal capacidad adicional,7800\n", human, "costo cargado");
  // Data inboxes demo: a file an outside system pushed (two versions), authored by the inbox (lib/mock/data-inboxes-mock.ts).
  const erp: Principal = { kind: "inbox", id: "ibx_demo_erp" };
  put("/Shared/Imports/pedidos-erp/orders-2026-10-26.json", '[{"order_id":1040,"total":120.5},{"order_id":1041,"total":88}]\n', erp, "pushed to the data inbox Pedidos ERP (diario)");
  put("/Shared/Imports/pedidos-erp/orders-2026-10-26.json", '[{"order_id":1040,"total":120.5},{"order_id":1041,"total":88},{"order_id":1042,"total":250.75}]\n', erp, "pushed to the data inbox Pedidos ERP (diario)");

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
    if (path === "/") ["Agents", "Projects", "Clients", "Shared", "Offices"].forEach((r) => sub.add(r));
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
    upload: async (path: string, file: Blob, _expected?: number, onProgress?: UploadProgress) => {
      const created = !live().some((m) => m.file.path === path);
      onProgress?.(file.size, file.size);
      return { file: put(path, await file.text(), human, "upload"), created };
    },
    uploadMany: async (folder: string, list: { name: string; file: Blob }[], onProgress?: UploadProgress): Promise<UploadResult[]> => {
      const total = list.reduce((n, f) => n + f.file.size, 0);
      const out: UploadResult[] = [];
      for (const f of list) {
        const path = `${folder === "/" ? "" : folder}/${f.name}`;
        const created = !live().some((m) => m.file.path === path);
        out.push({ name: f.name.split("/").pop() || f.name, path, status: created ? 201 : 200, created, result: { file: put(path, await f.file.text(), human, "upload") } });
      }
      onProgress?.(total, total);
      return out;
    },
    usage: async (): Promise<DriveUsage> => ({ used_bytes: 0, quota_bytes: 5 << 30, max_file_bytes: 25 << 20, max_upload_bytes: 25 << 20 }),
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
  show: (path) => { set({ open: true, path }); go(path === "/" ? "/files" : `/files?path=${encodeURIComponent(path)}`); },
  close: () => set({ open: false }),
  toggle: () => go(useDriveNav.getState().open ? "/" : "/files"),
}));

/** Upload cap until the server says otherwise (GET /drive/usage max_upload_bytes: 500 MB with S3, 25 MB without). */
export const DEFAULT_UPLOAD_BYTES = 25 << 20;
let uploadLimit: Promise<number> | null = null;
/** The server's per-file upload cap, asked once per page load. */
export function useUploadLimit(): number {
  const [n, setN] = useState(DEFAULT_UPLOAD_BYTES);
  useEffect(() => {
    uploadLimit ??= drive.usage().then((u) => u.max_upload_bytes || u.max_file_bytes || DEFAULT_UPLOAD_BYTES)
      .catch(() => { uploadLimit = null; return DEFAULT_UPLOAD_BYTES; });
    let live = true;
    void uploadLimit.then((v) => { if (live) setN(v); });
    return () => { live = false; };
  }, []);
  return n;
}
