import type { LinkImport, LinkImportInput, LinkRun, LinkSchedule } from "../linkImportsApi";

/** Demo/mock backend of "import from a link" (NEXT_PUBLIC_MOCK=true). Nothing is downloaded: a run is simulated from
 *  the link itself so the screen can be tried end to end. A link with "login" answers a web page (error), one with
 *  "live" changes on every run (a new version), any other link lands once and then stays unchanged. Private
 *  addresses and plain http are refused like the server does. The header value is never kept, only its last 4. */

const INTERVAL: Record<LinkSchedule, number> = { once: 0, hourly: 3600e3, daily: 86400e3, weekly: 7 * 86400e3 };

function slugify(name: string): string {
  const s = name.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 40);
  return s || "import";
}

function fail(code: string): never {
  throw new Error(code);
}

function checkLink(raw: string): string {
  let u: URL;
  try { u = new URL(raw.trim()); } catch { return fail("url_invalid"); }
  if (u.protocol !== "https:") fail("url_scheme_not_allowed");
  const h = u.hostname.toLowerCase();
  if (h === "localhost" || h.endsWith(".local") || h.endsWith(".internal") || /^(10\.|127\.|192\.168\.|169\.254\.|172\.(1[6-9]|2\d|3[01])\.)/.test(h) || h.startsWith("[")) {
    fail("address_not_allowed");
  }
  return u.toString();
}

class LinkImportsMock {
  private imports = new Map<string, LinkImport>();
  private runs = new Map<string, LinkRun[]>();
  private versions = new Map<string, number>();
  private seq = 0;

  constructor() {
    const now = Date.now();
    const li = this.make({ name: "Ventas publicadas (CSV)", url: "https://sheets.example.com/pub?output=csv", schedule: "daily" }, now - 3 * 86400e3);
    this.imports.set(li.id, li);
    this.record(li, "schedule", now - 2 * 86400e3);
    this.record(li, "schedule", now - 86400e3);
  }

  private nid(p: string) { this.seq += 1; return `${p}${Date.now().toString(16)}${this.seq}`; }

  private make(in_: LinkImportInput, at = Date.now()): LinkImport {
    const name = (in_.name ?? "").trim();
    if (!name) fail("url_invalid");
    const url = checkLink(in_.url ?? "");
    const slug = slugify(name);
    const schedule = in_.schedule ?? "once";
    const iso = new Date(at).toISOString();
    const customer = (in_.customer_id ?? "").trim();
    const folder = customer ? `Imports/Links/${slug}` : (in_.folder?.trim() || `/Shared/Imports/Links/${slug}`);
    const header = in_.header?.name && in_.header.value ? in_.header : null;
    return {
      id: this.nid("lnk_"), name, slug, url, host: new URL(url).host, folder, folder_path: customer ? `/Clients/${customer}/${folder}` : folder,
      customer_id: customer, file_stem: (in_.file_stem ?? "").trim(), schedule, enabled: in_.enabled ?? true,
      header_name: header?.name ?? "", header_set: !!header, header_hint: header?.value && header.value.length > 4 ? header.value.slice(-4) : "",
      created_by: "you", created_at: iso, updated_at: iso, next_run_at: schedule === "once" ? null : iso, last_run_at: null, last_status: "",
      last_code: "", last_hash: "", last_file_id: "", last_path: "", last_version: 0, last_data_at: null, run_count: 0,
    };
  }

  private record(li: LinkImport, trigger: "manual" | "schedule", at = Date.now()): LinkRun {
    const iso = new Date(at).toISOString();
    const ext = /json/i.test(li.url) ? ".json" : /xlsx/i.test(li.url) ? ".xlsx" : ".csv";
    const path = `${li.folder_path}/${li.file_stem || li.slug}${ext}`;
    const run: LinkRun = { id: this.nid("lnr_"), import_id: li.id, trigger, actor: trigger === "manual" ? "you" : "", status: "ok", code: "",
      started_at: iso, duration_ms: 420, size: 0, mime: "", hash: "", file_id: "", path: "", version: 0 };
    const version = this.versions.get(li.id) ?? 0;
    if (/login/i.test(li.url)) {
      run.status = "error"; run.code = "web_page_not_data";
    } else if (version > 0 && !/live/i.test(li.url)) {
      Object.assign(run, { status: "unchanged", size: 1840, mime: "text/csv", hash: li.last_hash, file_id: li.last_file_id, path, version });
    } else {
      const v = version + 1;
      this.versions.set(li.id, v);
      Object.assign(run, { status: "ok", size: 1840 + v * 37, mime: ext === ".json" ? "application/json" : "text/csv", hash: `sim${v}`.padEnd(64, "0"),
        file_id: li.last_file_id || this.nid("fil_"), path, version: v });
    }
    li.last_run_at = iso; li.last_status = run.status; li.last_code = run.code; li.run_count += 1;
    if (run.status !== "error") Object.assign(li, { last_hash: run.hash, last_file_id: run.file_id, last_path: run.path, last_version: run.version });
    if (run.status === "ok") li.last_data_at = iso;
    if (trigger === "schedule" && INTERVAL[li.schedule]) li.next_run_at = new Date(at + INTERVAL[li.schedule]).toISOString();
    const list = this.runs.get(li.id) ?? [];
    list.unshift(run);
    this.runs.set(li.id, list.slice(0, 200));
    return run;
  }

  private get(lid: string): LinkImport {
    const li = this.imports.get(lid);
    if (!li) fail("not_found");
    return li;
  }

  async handle(method: string, path: string, body?: unknown): Promise<unknown> {
    const [p, q] = path.split("?");
    const parts = p.split("/").filter(Boolean).map(decodeURIComponent); // ["link-imports", id?, "run"|"runs"?]
    const lid = parts[1];
    const in_ = (body ?? {}) as LinkImportInput;
    if (parts[0] !== "link-imports") fail("not_found");
    if (!lid && method === "GET") {
      const imports = [...this.imports.values()].sort((a, b) => b.created_at.localeCompare(a.created_at));
      return { imports: imports.map((x) => ({ ...x })), schedules: ["once", "hourly", "daily", "weekly"], types: [".csv", ".tsv", ".json", ".xlsx", ".txt", ".pdf"] };
    }
    if (!lid && method === "POST") {
      const li = this.make(in_);
      this.imports.set(li.id, li);
      return { ...li };
    }
    const li = this.get(lid);
    if (parts[2] === "run" && method === "POST") {
      const run = this.record(li, "manual");
      return { run, import: { ...li } };
    }
    if (parts[2] === "runs" && method === "GET") {
      const limit = Number(new URLSearchParams(q ?? "").get("limit")) || 20;
      return (this.runs.get(lid) ?? []).slice(0, limit);
    }
    if (method === "GET") return { ...li };
    if (method === "DELETE") { this.imports.delete(lid); this.runs.delete(lid); return {}; }
    if (method === "PUT") {
      if (in_.name?.trim()) li.name = in_.name.trim();
      if (in_.url?.trim()) { li.url = checkLink(in_.url); li.host = new URL(li.url).host; }
      if (in_.file_stem?.trim()) li.file_stem = in_.file_stem.trim();
      if (in_.schedule) li.schedule = in_.schedule;
      if (typeof in_.enabled === "boolean") li.enabled = in_.enabled;
      if (in_.header?.clear) Object.assign(li, { header_name: "", header_set: false, header_hint: "" });
      else if (in_.header?.name) {
        li.header_name = in_.header.name;
        if (in_.header.value) Object.assign(li, { header_set: true, header_hint: in_.header.value.length > 4 ? in_.header.value.slice(-4) : "" });
      }
      li.next_run_at = li.schedule === "once" ? null : (li.next_run_at ?? new Date().toISOString());
      li.updated_at = new Date().toISOString();
      return { ...li };
    }
    return fail("not_found");
  }
}

export const linkImportsMock = new LinkImportsMock();
