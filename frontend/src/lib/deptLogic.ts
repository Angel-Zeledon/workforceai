/** Pure logic of the department drive (tests: deptLogic.test.mjs). Paths compare case-insensitively. */
import type { Approval } from "./types";
import type { OfficeView } from "./officesLogic";

/** Actions that create a file (backend canonical names: drive/agent.go, gwtool.go, pnl.go). */
const CREATES = new Set(["write", "write_document", "income_statement"]);
const norm = (p: string) => p.replace(/\/+$/, "").toLowerCase();
const parentOf = (p: string) => { const i = norm(p).lastIndexOf("/"); return i <= 0 ? "/" : norm(p).slice(0, i); };

/** Departments of the user: member, not General, with a drive folder. Order kept. */
export function myDepartments(offices: OfficeView[]): OfficeView[] {
  return offices.filter((o) => o.is_member && !o.general && !!o.folder_path);
}

/** Pending approvals whose context.path is under `under`, grouped by lowercase exact path. */
export function pendingByPath(approvals: Approval[], under: string): Map<string, Approval[]> {
  const out = new Map<string, Approval[]>();
  const root = norm(under);
  for (const a of approvals) {
    if (a.status !== "pending") continue;
    const p = a.context?.path;
    if (typeof p !== "string" || !p) continue;
    const key = norm(p);
    if (key !== root && !key.startsWith(root + "/")) continue;
    const list = out.get(key);
    if (list) list.push(a); else out.set(key, [a]);
  }
  return out;
}

export function filterRows<T extends { path: string; updated_at?: string }>(
  rows: T[], filter: "all" | "review" | "today", pending: Map<string, unknown>, now: Date,
): T[] {
  if (filter === "all") return rows;
  if (filter === "review") return rows.filter((r) => pending.has(norm(r.path)));
  const start = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  return rows.filter((r) => {
    const t = r.updated_at ? Date.parse(r.updated_at) : NaN;
    return !Number.isNaN(t) && t >= start;
  });
}

/** Files an agent proposes (pending write approval) directly in `folder` that do not exist yet. */
export function proposedRows(
  pending: Map<string, Approval[]>, folder: string, existing: Set<string>,
): { path: string; name: string; approval: Approval }[] {
  const dir = norm(folder);
  const have = new Set<string>();
  for (const e of existing) have.add(norm(e));
  const out: { path: string; name: string; approval: Approval }[] = [];
  for (const [key, list] of pending) {
    if (parentOf(key) !== dir || have.has(key)) continue;
    const a = list.find((x) => CREATES.has(String(x.context?.action)));
    if (!a) continue;
    const raw = String(a.context?.path ?? key);
    out.push({ path: raw, name: raw.replace(/\/+$/, "").split("/").pop() || raw, approval: a });
  }
  return out;
}

export function defaultAgent(office: OfficeView): string | undefined {
  return office.agents[0];
}

/** Breadcrumbs from the department root (included) down to `path`. */
export function crumbs(path: string, root: string): { name: string; path: string }[] {
  const r = root.replace(/\/+$/, "");
  const p = path.replace(/\/+$/, "");
  const out = [{ name: r.split("/").pop() || r, path: r }];
  if (norm(p) === norm(r) || !norm(p).startsWith(norm(r) + "/")) return out;
  let acc = r;
  for (const seg of p.slice(r.length + 1).split("/")) {
    acc += "/" + seg;
    out.push({ name: seg, path: acc });
  }
  return out;
}

/** The key `pendingByPath` uses for a path (lowercase, no trailing "/"). */
export const pathKey = norm;

/** How many distinct paths with something pending lie strictly under `folder` (any depth). */
export function pendingUnder(pending: Map<string, unknown>, folder: string): number {
  const root = norm(folder) + "/";
  let n = 0;
  for (const k of pending.keys()) if (k.startsWith(root)) n++;
  return n;
}

/** The department a URL segment names (by id, case-insensitive, URL-decoded); undefined when it is not one of mine. */
export function findDepartment(depts: OfficeView[], param: string): OfficeView | undefined {
  let id = param;
  try { id = decodeURIComponent(param); } catch { /* keep it raw */ }
  const k = id.toLowerCase();
  return depts.find((d) => d.id.toLowerCase() === k);
}

export type DeptFilter = "all" | "review" | "today";

/**
 * What the center list shows. All: everything. Para revisar: the files here with something pending, the proposed files
 * and the subfolders that hold something pending (to drill down). Cambiado hoy: only files changed today.
 * `reviewCount` = everything pending under the current folder, at any depth.
 */
export function deptView<F extends { path: string }, R extends { path: string; updated_at?: string }, P>(
  v: { folder: string; folders: F[]; files: R[]; proposed: P[]; pending: Map<string, unknown>; now: Date }, filter: DeptFilter,
): { folders: F[]; files: R[]; proposed: P[]; reviewCount: number } {
  const reviewCount = pendingUnder(v.pending, v.folder);
  const files = filterRows(v.files, filter, v.pending, v.now);
  if (filter === "all") return { folders: v.folders, files, proposed: v.proposed, reviewCount };
  if (filter === "review") return { folders: v.folders.filter((f) => pendingUnder(v.pending, f.path) > 0), files, proposed: v.proposed, reviewCount };
  return { folders: [], files, proposed: [], reviewCount };
}

/** "Último cambio": relative ("3h") under a day, a date after that (or when the date is unreadable). */
export function lastChange(iso: string, now: Date): { relative: boolean } {
  const t = Date.parse(iso);
  return { relative: !Number.isNaN(t) && now.getTime() - t < 24 * 3600_000 };
}

/**
 * The folder the URL names (`?carpeta=`, relative to the department root) as a full path. Anything that is not plainly
 * under the root (`..`, `.`, a backslash) falls back to the root.
 */
export function folderFromParam(root: string, param: string | null | undefined): string {
  const r = root.replace(/\/+$/, "");
  const segs = String(param ?? "").split("/").filter(Boolean);
  if (segs.some((s) => s === ".." || s === "." || s.includes("\\"))) return r;
  return segs.length ? `${r}/${segs.join("/")}` : r;
}

/** The `?carpeta=` value of a folder: its path under the root ("" for the root itself or anything outside it). */
export function folderParam(root: string, path: string): string {
  const r = norm(root);
  const p = path.replace(/\/+$/, "");
  return norm(p).startsWith(r + "/") ? p.slice(r.length + 1) : "";
}

/** The office's agents the person may use (the agents directory's `rights.use`); no directory (older backend): all. */
export function usableAgents(office: string[], directory: { id: string; rights: { use: boolean } }[] | null): string[] {
  if (!directory) return office;
  const ok = new Set(directory.filter((d) => d.rights.use).map((d) => d.id));
  return office.filter((id) => ok.has(id));
}

/** The text for a refused "Pedir": not allowed to use that agent, the folder is not available, or a generic failure. */
export function sendErrorKey(e: unknown): string {
  const m = /-> (\d{3})\b/.exec(e instanceof Error ? e.message : "");
  const code = m ? Number(m[1]) : 0;
  if (code === 403) return "dept.ask.err.forbidden";
  if (code === 400 || code === 404) return "dept.ask.err.folder";
  return "chat.sendError";
}

/** Milliseconds until the next local midnight ("Cambiado hoy" is recomputed then). */
export function msUntilMidnight(now: Date): number {
  const next = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1);
  return next.getTime() - now.getTime();
}

/** The URL of a department at a folder (`carpeta` = folderParam; "" = the root). */
export function deptHref(officeId: string, carpeta: string): string {
  return `/d/${encodeURIComponent(officeId)}${carpeta ? `?carpeta=${encodeURIComponent(carpeta)}` : ""}`;
}
