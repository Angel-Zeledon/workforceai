"use client";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { intlLocale, useT } from "@/lib/i18n";
import { drive, parentOf, type DriveFile, type DriveFolder, type Hit, type Listing, type Principal } from "@/lib/drive";
import { officesApi } from "@/lib/officesApi";
import type { DirectoryEntry, OfficeView, OfficesResponse } from "@/lib/officesLogic";
import {
  agentUnreviewed, crumbs, defaultAgent, deptHref, deptView, findDepartment, folderFromParam, folderParam, lastChange, msUntilMidnight, myDepartments,
  needsReview, pathKey, pendingByPath, pendingUnder, personNames, proposedRows, usableAgents, withReviewed, type DeptFilter,
} from "@/lib/deptLogic";
import { useStore } from "@/lib/store";
import { useSession } from "@/lib/session";
import type { Approval } from "@/lib/types";
import { Empty, Loading, timeAgo, useAgentName } from "../ui";
import { EmptyState, PageSkeleton } from "../nav/ui2";
import { Icon } from "../icons";
import { Author, FilePane } from "../drive/DriveView";
import { ApprovalPending } from "../approvals/PendingCard";
import { DetailPanel, dropDetailUrl, useDetailUrl, useIsPhone } from "../ui/DetailPanel";
import { DeptTree } from "./DeptTree";
import { DeptComposer, type AskSeed } from "./DeptComposer";

/** The offices of the person (null while loading; an unreachable backend counts as "no departments"). */
function useOffices(): OfficesResponse | null | undefined {
  const [r, setR] = useState<OfficesResponse | null | undefined>(undefined);
  useEffect(() => { officesApi.list().then(setR).catch(() => setR(null)); }, []);
  return r;
}

function NoDepartment({ admin, unavailable }: { admin: boolean; unavailable?: boolean }) {
  const { t } = useT();
  return (
    <div className="flex h-full items-start justify-center overflow-y-auto bg-bg p-4 sm:items-center sm:p-6">
      <EmptyState testid={unavailable ? "dept-unavailable" : "dept-none"} icon="folder"
        title={unavailable ? t("dept.unavailable") : t("dept.none.title")} body={unavailable ? t("dept.unavailable.body") : t("dept.none.body")}
        action={admin ? <Link href="/org?tab=offices" prefetch={false} data-testid="dept-create"
          className="inline-flex min-h-10 items-center rounded-lg bg-accent px-4 text-[13px] font-semibold text-white hover:bg-accent-hover sm:min-h-9">{t("dept.create")}</Link> : undefined} />
    </div>
  );
}

/** `/d`: straight to the first department of the person, or the empty state. */
export function DepartmentHome() {
  const r = useOffices();
  const router = useRouter();
  const first = r ? myDepartments(r.offices)[0] : undefined;
  useEffect(() => { if (first) router.replace(`/d/${encodeURIComponent(first.id)}`); }, [first, router]);
  if (r === undefined || first) return <PageSkeleton />;
  return <NoDepartment admin={!!r?.me.admin} />;
}

/** `/d/<office>`: the department's Drive, when the department is one of the person's. */
export function DepartmentDrive({ officeId }: { officeId: string }) {
  const r = useOffices();
  if (r === undefined) return <PageSkeleton />;
  const depts = r ? myDepartments(r.offices) : [];
  const dept = findDepartment(depts, officeId);
  if (!dept) return <NoDepartment admin={!!r?.me.admin} unavailable />;
  return <Suspense fallback={<PageSkeleton />}><DeptScreen key={dept.id} dept={dept} depts={depts} /></Suspense>;
}

type Proposed = ReturnType<typeof proposedRows>[number];
type Selected = { kind: "file"; file: DriveFile } | { kind: "proposed"; row: Proposed };

/** The URL parameter of the open file (the folder is `?carpeta=`). Not "archivo": that one belongs to the global file viewer. */
const DOC_PARAM = "doc";
/** From this width the panel is a column beside the list; below it covers the composer. */
const WIDE = "(min-width: 1280px)";

const chip = "inline-flex items-center whitespace-nowrap rounded-[6px] px-1.5 py-[1px] text-[10.5px] font-semibold";
const REVIEW = `${chip} bg-warn/10 text-warn`;
const PROPOSED = `${chip} bg-accent-soft text-accent`;

function DeptScreen({ dept, depts }: { dept: OfficeView; depts: OfficeView[] }) {
  const { t } = useT();
  const router = useRouter();
  const phone = useIsPhone();
  const root = (dept.folder_path ?? "").replace(/\/+$/, "");
  // the folder lives in the URL (?carpeta=, relative to the root): Back returns to the previous folder, a reload keeps it
  const params = useSearchParams();
  const path = folderFromParam(root, params.get("carpeta"));
  // a file named by the URL (?doc=) or by a search hit, until it is found and opened
  const [urlDoc, setUrlDoc] = useState<string | null>(() => params.get(DOC_PARAM));
  const [wantDoc, setWantDoc] = useState<string | null>(null);
  // the office's agents this person may use (null while the directory loads)
  const [dir, setDir] = useState<DirectoryEntry[] | null | undefined>(undefined);
  useEffect(() => { officesApi.directory().then((r) => setDir(r.agents)).catch(() => setDir(null)); }, []);
  const agents = useMemo(() => (dir === undefined ? null : usableAgents(dept.agents, dir)), [dir, dept.agents]);
  const firstAgent = agents && (agents.includes(defaultAgent(dept) ?? "") ? defaultAgent(dept) : agents[0]);
  // "Cambiado hoy" follows the calendar: recomputed at midnight and whenever the window comes back
  const [day, setDay] = useState(() => new Date().toDateString());
  useEffect(() => {
    const bump = () => setDay(new Date().toDateString());
    const timer = setTimeout(bump, msUntilMidnight(new Date()) + 1000);
    window.addEventListener("focus", bump); document.addEventListener("visibilitychange", bump);
    return () => { clearTimeout(timer); window.removeEventListener("focus", bump); document.removeEventListener("visibilitychange", bump); };
  }, [day]);
  const [listing, setListing] = useState<Listing | null>(null);
  const [error, setError] = useState(false);
  const [filter, setFilter] = useState<DeptFilter>("all");
  const [selected, setSelected] = useState<Selected | null>(null);
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<Hit[] | null>(null);
  const [seed, setSeed] = useState<AskSeed | null>(null);
  const [treeOpen, setTreeOpen] = useState(false);
  const [menu, setMenu] = useState<{ file: DriveFile; top: number; right: number; trigger: HTMLButtonElement } | null>(null);
  const menuBox = useRef<HTMLDivElement>(null);
  // "Marcar como revisado": a mark in flight and a refusal (a folder the person may only read)
  const [marking, setMarking] = useState(false);
  const [markErr, setMarkErr] = useState(false);
  // "Último cambio" names the person (the department's leads and members, and whoever is reading)
  const me = useSession((s) => s.user);
  const names = useMemo(() => personNames(dept, me), [dept, me]);
  const agentName = useAgentName();
  const authorText = (p: Principal) => (p.kind === "agent" ? agentName(p.id) : p.kind === "human" ? names.get(p.id) ?? t("drive.person")
    : p.kind === "system" ? t("drive.system") : p.id);
  const who = (p: Principal, copy?: boolean) => (p.kind === "human" && names.has(p.id)
    ? <span data-testid={copy ? undefined : "dept-author-person"}>{names.get(p.id)}</span> : <Author p={p} copy={copy} />);

  // pending approvals of fs actions under the department (they carry context.path)
  const approvals = useStore((s) => s.approvals);
  const pending = useMemo(() => pendingByPath(Object.values(approvals) as Approval[], root), [approvals, root]);
  const pendingSig = useMemo(() => [...pending.keys()].sort().join("|"), [pending]);
  // an agent touched a file of the department: list again
  const rootKey = pathKey(root) + "/";
  const touched = useStore((s) => Object.values(s.files).filter((f) => pathKey(f.path).startsWith(rootKey)).map((f) => f.ts).join(","));

  const load = useCallback(async () => {
    try { setError(false); setListing(await drive.list(path)); } catch { setError(true); }
  }, [path]);
  useEffect(() => { void load(); }, [load, touched, pendingSig]);

  const files = useMemo(() => listing?.files ?? [], [listing]);
  const folders: DriveFolder[] = useMemo(() => listing?.folders ?? [], [listing]);
  const proposed = useMemo(() => proposedRows(pending, path, new Set(files.map((f) => f.path))), [pending, path, files]);
  const view = useMemo(() => deptView({ folder: path, folders, files, proposed, pending, now: new Date() }, filter), [path, folders, files, proposed, pending, filter, day]); // eslint-disable-line react-hooks/exhaustive-deps

  // a proposal that was decided closes its panel; a file that changed shows its new version
  useEffect(() => {
    if (selected?.kind === "proposed" && !proposed.some((p) => p.approval.id === selected.row.approval.id)) setSelected(null);
    if (selected?.kind === "file") { const f = files.find((x) => x.id === selected.file.id); if (f && f !== selected.file) setSelected({ kind: "file", file: f }); }
  }, [proposed, files]); // eslint-disable-line react-hooks/exhaustive-deps

  // the open file is in the URL (?doc=): Back closes the panel (the sheet on a phone), Forward opens it again
  const selPath = selected ? (selected.kind === "file" ? selected.file.path : selected.row.path) : null;
  useDetailUrl(DOC_PARAM, selPath ?? urlDoc, (v) => { setSelected(null); setUrlDoc(v); });
  // find the file the URL or a search hit names: a proposal of this folder, a file of the listing, or the drive by path
  useEffect(() => {
    const target = urlDoc ?? wantDoc;
    if (!target || !listing) return;
    const key = pathKey(target);
    if (!key.startsWith(rootKey)) { setUrlDoc(null); setWantDoc(null); return; }
    if (wantDoc && pathKey(parentOf(target)) !== pathKey(path)) return; // the hit's folder is still loading
    let live = true;
    const done = (sel: Selected | null) => { if (!live) return; setSelected(sel); setUrlDoc(null); setWantDoc(null); };
    const prop = proposed.find((x) => pathKey(x.path) === key);
    if (prop) { done({ kind: "proposed", row: prop }); return; }
    const f = files.find((x) => pathKey(x.path) === key);
    if (f) { done({ kind: "file", file: f }); return; }
    drive.byPath(target).then((x) => done(x ? { kind: "file", file: x } : null)).catch(() => done(null));
    return () => { live = false; };
  }, [urlDoc, wantDoc, listing, proposed, files, path, rootKey]);

  useEffect(() => { setMarkErr(false); }, [selPath]);
  const markReviewed = async (f: DriveFile) => {
    if (marking) return;
    setMarking(true); setMarkErr(false);
    try { await drive.setTags(f.id, withReviewed(f.tags)); await load(); } catch { setMarkErr(true); }
    setMarking(false);
  };

  // a new folder: nothing of the previous one stays open
  useEffect(() => { setHits(null); setTreeOpen(false); setMenu(null); }, [path]);

  const closeMenu = useCallback((focusBack: boolean) => {
    setMenu((m) => { if (m && focusBack) m.trigger.focus(); return null; });
  }, []);
  useEffect(() => {
    if (!menu) return;
    menuBox.current?.querySelector<HTMLElement>("[role=menuitem]")?.focus({ preventScroll: true });
    const close = () => closeMenu(false);
    const key = (e: KeyboardEvent) => { if (e.key === "Escape") { e.preventDefault(); closeMenu(true); } };
    window.addEventListener("mousedown", close); window.addEventListener("keydown", key); window.addEventListener("resize", close);
    window.addEventListener("scroll", close, true); // any scroll (the list too) closes it
    return () => {
      window.removeEventListener("mousedown", close); window.removeEventListener("keydown", key); window.removeEventListener("resize", close);
      window.removeEventListener("scroll", close, true);
    };
  }, [menu, closeMenu]);
  const menuKeys = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    const items = [...(menuBox.current?.querySelectorAll<HTMLElement>("[role=menuitem]") ?? [])];
    const i = items.indexOf(document.activeElement as HTMLElement);
    const go = (n: number) => { e.preventDefault(); items[(n + items.length) % items.length]?.focus(); };
    if (e.key === "ArrowDown") go(i + 1);
    else if (e.key === "ArrowUp") go(i - 1);
    else if (e.key === "Home") go(0);
    else if (e.key === "End") go(items.length - 1);
    else if (e.key === "Tab") closeMenu(false);
  };

  /** Go to a folder of the department (a new history entry, so Back returns here). */
  const openFolder = (p: string) => {
    setTreeOpen(false); setHits(null);
    if (pathKey(p) === pathKey(path)) return;
    dropDetailUrl(DOC_PARAM); // the open file belongs to this folder: it closes without its own history step
    setSelected(null); setUrlDoc(null); setWantDoc(null);
    router.push(deptHref(dept.id, folderParam(root, p)), { scroll: false });
  };
  // the panel covers the composer below the wide layout (a sheet on a phone, floating on a tablet): it closes
  const ask = (f: DriveFile) => {
    setMenu(null);
    if (!window.matchMedia(WIDE).matches) setSelected(null);
    setSeed({ text: t("dept.ask.about", { name: f.name }), path: f.path, name: f.name, n: Date.now() });
  };
  const search = async () => {
    const q = query.trim();
    if (!q) { setHits(null); return; }
    setHits(await drive.search(q, root).catch(() => []));
  };
  const openHit = (h: Hit) => {
    setQuery("");
    openFolder(parentOf(h.path));
    setWantDoc(h.path); // opened once its folder is listed
  };
  const when = (iso: string) => (lastChange(iso, new Date()).relative ? timeAgo(iso)
    : new Date(iso).toLocaleDateString(intlLocale(), { day: "numeric", month: "short" }));

  const trail = crumbs(path, root);
  const here = trail[trail.length - 1]?.name ?? dept.name;
  // the panel's subtitle: where the file is, from the department down ("Contabilidad / Bancos y conciliaciones")
  const selWhere = selPath ? crumbs(parentOf(selPath), root).map((c) => c.name).join(" / ") : "";
  const selPending = selPath ? pending.get(pathKey(selPath)) ?? [] : [];
  const empty = !view.folders.length && !view.files.length && !view.proposed.length;

  const tree = <DeptTree root={root} rootName={dept.name} current={path} pending={pending} onOpen={openFolder} refresh={touched + pendingSig} />;

  return (
    <div data-testid="dept-drive" className="relative flex h-full min-h-0 bg-bg">
      {/* left: the folders (desktop) */}
      <aside className="hidden w-[240px] shrink-0 overflow-y-auto border-r border-line bg-panel px-2 py-3 md:block">
        <div className="px-2 pb-1.5 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute">{t("dept.folders")}</div>
        {tree}
      </aside>

      {/* center: header, filters, the list and the composer */}
      <section className="flex min-w-0 flex-1 flex-col">
        <header className="shrink-0 border-b border-line bg-panel px-3 pb-2.5 pt-3 sm:px-5">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            <h1 data-testid="dept-title" className="min-w-0 truncate font-display text-[18px] font-semibold leading-tight tracking-tight text-ink sm:text-xl">{dept.name}</h1>
            {depts.length > 1 && (
              <select aria-label={t("dept.switch")} data-testid="dept-switch" value={dept.id} onChange={(e) => router.push(`/d/${encodeURIComponent(e.target.value)}`)}
                className="min-h-9 rounded-lg border border-line bg-panel px-2 text-[13px] text-ink outline-none focus:border-accent">
                {depts.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
              </select>
            )}
            <form role="search" className="relative w-full sm:ml-auto sm:w-64" onSubmit={(e) => { e.preventDefault(); void search(); }}>
              <Icon name="search" size={14} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-mute" />
              <input data-testid="dept-search" type="search" value={query} onChange={(e) => { setQuery(e.target.value); if (!e.target.value) setHits(null); }}
                placeholder={t("dept.search")} aria-label={t("dept.search")}
                className="min-h-10 w-full rounded-lg border border-line bg-panel2 pl-8 pr-3 text-base text-ink outline-none focus:border-accent sm:min-h-9 sm:text-[13px]" />
            </form>
          </div>
          {trail.length > 1 && <nav aria-label={t("dept.folders")} data-testid="dept-crumbs" className="mt-1.5 hidden min-w-0 md:flex flex-wrap items-center gap-0.5 text-[12.5px]">
            {trail.map((c, i) => (
              <span key={c.path} className="flex min-w-0 items-center gap-0.5">
                {i > 0 && <Icon name="chevron" size={11} className="shrink-0 text-mute" />}
                <button type="button" onClick={() => openFolder(c.path)} aria-current={i === trail.length - 1 ? "page" : undefined}
                  className={`min-h-8 max-w-[40vw] truncate rounded px-1.5 sm:max-w-none ${i === trail.length - 1 ? "font-semibold text-ink" : "text-mute hover:bg-panel2 hover:text-ink"}`}>{c.name}</button>
              </span>
            ))}
          </nav>}
          {/* phone: the folders fold under one button */}
          <div className="mt-1.5 md:hidden">
            <button type="button" data-testid="dept-tree-toggle" aria-expanded={treeOpen} onClick={() => setTreeOpen(!treeOpen)}
              className="flex min-h-10 w-full items-center gap-2 rounded-lg border border-line bg-panel px-3 text-left text-[13px] text-ink">
              <Icon name="folder" size={14} className="text-accent" />
              <span className="text-mute">{t("dept.folders")}:</span>
              <span className="min-w-0 flex-1 truncate font-semibold">{here}</span>
              <Icon name="down" size={14} className={`text-mute transition-transform ${treeOpen ? "rotate-180" : ""}`} />
            </button>
            {treeOpen && <div className="mt-1.5 max-h-[45dvh] overflow-y-auto rounded-lg border border-line bg-panel p-1.5">{tree}</div>}
          </div>
          <div role="group" aria-label={t("dept.filters")} className="mt-2 flex flex-wrap gap-1.5">
            {(["all", "review", "today"] as const).map((f) => (
              <button key={f} type="button" data-testid={`dept-filter-${f}`} aria-pressed={filter === f} onClick={() => { setFilter(f); setHits(null); }}
                className={`inline-flex min-h-9 items-center gap-1.5 rounded-lg border px-3 text-[12.5px] font-medium transition sm:min-h-8 ${filter === f
                  ? "border-accent bg-accent-soft text-accent" : "border-line bg-panel text-ink2 hover:bg-panel2 hover:text-ink"}`}>
                {f === "all" ? t("dept.filter.all") : f === "review" ? t("dept.review") : t("dept.filter.today")}
                {f === "review" && view.reviewCount > 0 && (
                  <span data-testid="dept-review-count" className={`rounded-[6px] px-1.5 text-[11px] font-semibold ${filter === f ? "bg-accent text-white" : "bg-warn/10 text-warn"}`}>{view.reviewCount}</span>
                )}
              </button>
            ))}
          </div>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3 sm:px-5">
          {hits ? (
            <div data-testid="dept-search-results" className="space-y-2">
              <div className="flex items-center justify-between gap-2">
                <h2 className="text-[13px] font-semibold text-ink">{t("drive.results", { n: hits.length })}</h2>
                <button type="button" onClick={() => setHits(null)} className="min-h-9 rounded-lg px-2 text-[12.5px] font-medium text-accent hover:bg-accent-soft">{t("drive.close")}</button>
              </div>
              {!hits.length && <Empty>{t("drive.noResults")}</Empty>}
              {hits.map((h) => (
                <button type="button" key={h.file_id} onClick={() => void openHit(h)}
                  className="block w-full rounded-[10px] border border-line bg-panel px-3 py-2 text-left hover:border-accent">
                  <div className="text-[13px] font-medium text-ink">{h.name}</div>
                  <div className="truncate text-[11px] text-mute">{h.path.slice(root.length + 1) || h.path}</div>
                  {h.snippet && <div className="mt-1 line-clamp-2 text-[12px] text-ink2">{h.snippet.replace(/[«»]/g, "")}</div>}
                </button>
              ))}
            </div>
          ) : error ? (
            <Empty>{t("drive.unavailable")}</Empty>
          ) : !listing ? (
            <Loading rows={6} />
          ) : empty ? (
            <Empty testid="dept-empty" icon="folder">{filter === "all" ? t("dept.empty") : t("dept.emptyFilter")}</Empty>
          ) : (
            <div className="overflow-hidden rounded-[10px] border border-line bg-panel">
              <table data-testid="dept-table" className="w-full table-fixed text-left text-[13px]">
                <thead className="border-b border-line bg-panel2/60 text-[11px] uppercase tracking-wide text-mute">
                  <tr>
                    <th className="py-2 pl-3 font-semibold">{t("dept.col.name")}</th>
                    <th className="hidden w-[30%] font-semibold sm:table-cell">{t("dept.col.changed")}</th>
                    <th className="w-[100px] font-semibold max-sm:w-[96px]">{t("dept.col.status")}</th>
                    <th className="w-10"><span className="sr-only">{t("dept.menu.more")}</span></th>
                  </tr>
                </thead>
                <tbody>
                  {view.proposed.map((p) => {
                    const sel = selected?.kind === "proposed" && selected.row.approval.id === p.approval.id;
                    return (
                      <tr key={`p:${p.approval.id}`} data-testid={`dept-proposed-${p.name}`} data-selected={sel}
                        onClick={() => setSelected({ kind: "proposed", row: p })}
                        className={`cursor-pointer border-t border-line first:border-t-0 ${sel ? "bg-accent-soft" : "bg-accent-soft/40 hover:bg-accent-soft"}`}>
                        <td className="py-2.5 pl-3">
                          <span className="flex min-w-0 items-start gap-2 text-ink">
                            <Icon name="doc" size={15} className="mt-[2px] shrink-0 text-accent" />
                            <button type="button" data-testid={`dept-open-${p.name}`} onClick={(e) => { e.stopPropagation(); setSelected({ kind: "proposed", row: p }); }}
                              className="min-w-0 break-words rounded text-left font-medium [overflow-wrap:anywhere] hover:underline">{p.name}</button>
                          </span>
                          <span className="mt-0.5 flex flex-wrap gap-x-1.5 pl-[23px] text-[11.5px] text-mute sm:hidden"><Author p={{ kind: "agent", id: p.approval.agent_id }} copy /><span aria-hidden>·</span><span>{when(p.approval.created_at)}</span></span>
                        </td>
                        <td className="hidden py-2 pr-2 text-[12px] text-ink2 sm:table-cell">
                          <div className="truncate whitespace-nowrap" title={`${agentName(p.approval.agent_id)} · ${when(p.approval.created_at)}`}>
                            <Author p={{ kind: "agent", id: p.approval.agent_id }} /> <span className="text-mute">·&nbsp;{when(p.approval.created_at)}</span>
                          </div>
                        </td>
                        <td><span data-testid="dept-chip-proposed" className={`${PROPOSED} border border-accent/25`}>{t("dept.proposed")}</span></td>
                        <td />
                      </tr>
                    );
                  })}
                  {view.folders.map((f) => {
                    const n = pendingUnder(pending, f.path);
                    return (
                      <tr key={f.id} data-testid={`dept-folder-${f.name}`} onClick={() => openFolder(f.path)} className="cursor-pointer border-t border-line first:border-t-0 hover:bg-panel2">
                        <td className="py-2.5 pl-3"><span className="flex min-w-0 items-center gap-2 font-medium text-ink"><Icon name="folder" size={15} className="shrink-0 text-mute" /><button type="button" onClick={(e) => { e.stopPropagation(); openFolder(f.path); }} className="min-w-0 break-words rounded text-left [overflow-wrap:anywhere] hover:underline">{f.name}</button></span></td>
                        <td className="hidden sm:table-cell" />
                        <td>{n > 0 && <span className={REVIEW} title={t("dept.reviewUnder", { n })}>{t("dept.reviewUnder", { n })}</span>}</td>
                        <td />
                      </tr>
                    );
                  })}
                  {view.files.map((f) => {
                    const sel = selected?.kind === "file" && selected.file.id === f.id;
                    const rev = needsReview(f, pending);
                    return (
                      <tr key={f.id} data-testid={`dept-file-${f.name}`} data-selected={sel} onClick={() => setSelected({ kind: "file", file: f })}
                        className={`cursor-pointer border-t border-line first:border-t-0 ${sel ? "bg-accent-soft" : "hover:bg-panel2"}`}>
                        <td className="py-2.5 pl-3">
                          <span className="flex min-w-0 items-start gap-2 text-ink"><Icon name="doc" size={15} className="mt-[2px] shrink-0 text-mute" /><button type="button" data-testid={`dept-open-${f.name}`} onClick={(e) => { e.stopPropagation(); setSelected({ kind: "file", file: f }); }}
                            className="min-w-0 break-words rounded text-left [overflow-wrap:anywhere] hover:underline">{f.name}</button></span>
                          <span className="mt-0.5 flex flex-wrap gap-x-1.5 pl-[23px] text-[11.5px] text-mute sm:hidden">{who(f.updated_by, true)}<span aria-hidden>·</span><span>{when(f.updated_at)}</span></span>
                        </td>
                        <td className="hidden py-2 pr-2 text-[12px] text-ink2 sm:table-cell">
                          <div data-testid="dept-changed" className="truncate whitespace-nowrap" title={`${authorText(f.updated_by)} · ${when(f.updated_at)}`}>
                            {who(f.updated_by)} <span className="text-mute">·&nbsp;{when(f.updated_at)}</span>
                          </div>
                        </td>
                        <td>{rev && <span data-testid="dept-chip-review" className={REVIEW}>{t("dept.review")}</span>}</td>
                        <td className="pr-1 text-right" onClick={(e) => e.stopPropagation()}>
                          <button type="button" data-testid={`dept-menu-${f.name}`} aria-label={t("dept.menu.more")} title={t("dept.menu.more")} aria-haspopup="menu" aria-expanded={menu?.file.id === f.id} aria-controls={menu?.file.id === f.id ? "dept-row-menu" : undefined}
                            onMouseDown={(e) => e.stopPropagation()}
                            onClick={(e) => {
                              const r = e.currentTarget.getBoundingClientRect();
                              const below = r.bottom + 132 < window.innerHeight;
                              const trigger = e.currentTarget;
                              setMenu(menu?.file.id === f.id ? null : { file: f, top: below ? r.bottom + 4 : r.top - 132, right: window.innerWidth - r.right, trigger });
                            }}
                            className="inline-flex h-9 w-9 items-center justify-center rounded-md text-[16px] font-semibold leading-none text-mute hover:bg-panel2 hover:text-ink">⋯</button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </div>

        <DeptComposer agents={agents} initialAgent={firstAgent ?? undefined} folder={path} seed={seed} />
      </section>

      {/* the row menu (fixed, so the scrolling list never clips it) */}
      {menu && (
        <div ref={menuBox} id="dept-row-menu" role="menu" aria-label={menu.file.name} data-testid="dept-row-menu" onKeyDown={menuKeys}
          onMouseDown={(e) => e.stopPropagation()} style={{ top: menu.top, right: menu.right }}
          className="fixed z-50 w-52 rounded-[8px] border border-line bg-panel py-1 shadow-float">
          {[
            { id: "open", label: t("dept.menu.open"), run: () => { setSelected({ kind: "file", file: menu.file }); closeMenu(false); } },
            { id: "ask", label: t("dept.menu.ask"), run: () => ask(menu.file) },
            { id: "download", label: t("dept.menu.download"), run: () => { void drive.download(menu.file); closeMenu(true); } },
          ].map((it) => (
            <button key={it.id} type="button" role="menuitem" tabIndex={-1} data-testid={`dept-menu-${it.id}`} onClick={it.run}
              className="flex min-h-10 w-full items-center px-3 text-left text-[13px] text-ink outline-none hover:bg-panel2 focus:bg-accent-soft focus:text-accent sm:min-h-9">{it.label}</button>
          ))}
        </div>
      )}

      {/* right: the selected file (a column on a desktop, a sheet from the bottom on a phone) */}
      {selected && phone && <div aria-hidden className="fixed inset-0 z-40 bg-ink/30" onClick={() => setSelected(null)} />}
      {selected && (
        <DetailPanel testId="dept-panel" label={t("dept.file")} concept="file" ariaLabel={selected.kind === "file" ? selected.file.name : selected.row.name}
          title={selected.kind === "file" ? selected.file.name : selected.row.name}
          subtitle={selWhere}
          meta={selected.kind === "file"
            ? <>{who(selected.file.updated_by)}<span aria-hidden>·</span><span>{when(selected.file.updated_at)}</span><span aria-hidden>·</span><span>v{selected.file.current_version}</span></>
            : <><span className={PROPOSED}>{t("dept.proposed")}</span><Author p={{ kind: "agent", id: selected.row.approval.agent_id }} /><span aria-hidden>·</span><span>{when(selected.row.approval.created_at)}</span></>}
          actions={selected.kind === "file" ? (
            <>
              {agentUnreviewed(selected.file) && (
                <button type="button" data-testid="dept-panel-reviewed" disabled={marking} onClick={() => void markReviewed(selected.file)}
                  className="inline-flex min-h-10 items-center gap-1 rounded-lg bg-accent px-3 text-[12.5px] font-semibold text-white hover:bg-accent-hover disabled:opacity-50 sm:min-h-8">
                  <Icon name="check" size={13} />{t("dept.markReviewed")}</button>
              )}
              <button type="button" data-testid="dept-panel-ask" onClick={() => ask(selected.file)}
                className="min-h-10 rounded-lg border border-line-strong bg-panel px-3 text-[12.5px] font-medium text-ink hover:bg-panel2 sm:min-h-8">{t("dept.menu.ask")}</button>
            </>
          ) : undefined}
          onClose={() => setSelected(null)}
          className="xl:w-[380px] 2xl:w-[440px] max-md:fixed max-md:inset-x-0 max-md:bottom-0 max-md:top-[14dvh] max-md:z-50 max-md:rounded-t-[10px] max-md:border-t max-md:border-line max-md:shadow-float">
          {markErr && <div role="alert" data-testid="dept-panel-reviewed-err" className="shrink-0 border-b border-line bg-err/5 px-4 py-2 text-[12px] text-err">{t("dept.markReviewed.err")}</div>}
          {selPending.length > 0 && (
            <div data-testid="dept-panel-pending" className="max-h-[50%] shrink-0 space-y-2 overflow-y-auto border-b border-line bg-panel2/50 p-3">
              {selPending.map((a) => <ApprovalPending key={a.id} a={a} variant="compact" />)}
            </div>
          )}
          {selected.kind === "file" ? (
            <FilePane key={selected.file.id} file={selected.file} embedded onClose={() => setSelected(null)} onChanged={() => void load()}
              onDeleted={() => { setSelected(null); void load(); }} />
          ) : (
            <div className="min-h-0 flex-1 overflow-y-auto p-4 text-[12.5px] leading-relaxed text-mute">{t("dept.proposedBody")}</div>
          )}
        </DetailPanel>
      )}
    </div>
  );
}
