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
