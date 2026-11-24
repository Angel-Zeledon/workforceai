"use client";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useCallback, useEffect, useMemo, useRef, useState, type DragEvent, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { intlLocale, useT } from "@/lib/i18n";
import { drive, driveShares, parentOf, type DriveFile, type DriveFolder, type DriveShare, type Hit, type Listing, type Principal } from "@/lib/drive";
import { officesApi } from "@/lib/officesApi";
import type { DirectoryEntry, OfficeView, OfficesResponse } from "@/lib/officesLogic";
import {
  agentUnreviewed, defaultAgent, deptView, fileStatus, findDepartment, folderFromParam, folderParam, lastChange, msUntilMidnight,
  onlySharedFile, pathKey, pendingByPath, pendingUnder, personNames, proposedRows, rowMenuItems, shareFromParam, sharedHref, sharedRoot,
  usableAgents, withReviewed, withStatus, type DeptFilter, type RowMenuItem,
} from "@/lib/deptLogic";
import {
  agentDesks, canCreateHere, dayFolders, folderHref, folderLabel, groupAttachments, humanCrumbs, isAttachmentsFolder, isHiddenPath,
  isProtectedFolder, joinPath, moveError, navDepartments, officeOfPath, placeRoot, readOnlyReason, sectionHelp, sectionOf, visibleFolders,
  type Desk, type NameCtx,
} from "@/lib/filesLogic";
import { folderAccess } from "@/lib/folderAccess";
import { docSearch, type Passage, type PassageResult } from "@/lib/docsearch";
import { useFileViewer } from "@/lib/fileView";
import { useStore } from "@/lib/store";
import { useSession } from "@/lib/session";
import { toast } from "@/lib/toast";
import { errorText } from "@/lib/errors";
import type { Approval } from "@/lib/types";
import { Empty, Loading, timeAgo, useAgentName } from "../ui";
import { EmptyState, PageSkeleton } from "../nav/ui2";
import { Icon, type IconName } from "../icons";
import { Avatar } from "../chat/ChatThread";
import { Author, FilePane } from "../drive/DriveView";
import { FolderAgentsPanel } from "../drive/FolderAgentsPanel";
import { IndexStatusChip, PassageResults } from "../drive/PassageSearch";
import { ApprovalPending } from "../approvals/PendingCard";
import { DetailPanel, dropDetailUrl, useDetailUrl, useIsPhone } from "../ui/DetailPanel";
import { DeptTree } from "../dept/DeptTree";
import { DeptComposer, type AskSeed } from "../dept/DeptComposer";
import { ShareDialog } from "../dept/ShareDialog";
import { ShareRequests } from "../dept/ShareRequests";
import { ShareAccess } from "../dept/ShareAccess";
import { FilesNav } from "./FilesNav";
import { MoveDialog, NameDialog, type PickRoot } from "./FolderDialogs";
import { useDriveUpload } from "./useDriveUpload";

/** The offices of the person (undefined while loading; an unreachable backend counts as "no departments"). */
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
        action={<div className="flex flex-wrap justify-center gap-2">
          <Link href="/files" prefetch={false} className="inline-flex min-h-10 items-center rounded-lg border border-line-strong px-4 text-[13px] font-medium text-ink hover:bg-panel2 sm:min-h-9">{t("files.title")}</Link>
          {admin && <Link href="/offices" prefetch={false} data-testid="dept-create"
            className="inline-flex min-h-10 items-center rounded-lg bg-accent px-4 text-[13px] font-semibold text-white hover:bg-accent-hover sm:min-h-9">{t("dept.create")}</Link>}
        </div>} />
    </div>
  );
}

/** `/d`: straight to the first department of the person, or the empty state. */
export function FilesDepartmentHome() {
  const r = useOffices();
  const router = useRouter();
  const first = r ? navDepartments(r.offices).mine[0] : undefined;
  useEffect(() => { if (first) router.replace(`/d/${encodeURIComponent(first.id)}`); }, [first, router]);
  if (r === undefined || first) return <PageSkeleton />;
  return <NoDepartment admin={!!r?.me.admin} />;
}

/**
 * The one Files screen. `/files?path=` browses any place (the home lists the sections); `/d/<office>?carpeta=` is the
 * direct link of a department (its own folders, what other departments share with it, the composer, Para revisar).
 */
export function FilesScreen({ officeId }: { officeId?: string }) {
  const r = useOffices();
  if (r === undefined) return <PageSkeleton />;
  const offices = r?.offices ?? [];
  const nav = navDepartments(offices);
  let dept: OfficeView | undefined;
  if (officeId) {
    dept = findDepartment([...nav.mine, ...nav.others], officeId);
    if (!dept) return <NoDepartment admin={!!r?.me.admin} unavailable />;
  }
  return <Suspense fallback={<PageSkeleton />}><Screen key={dept?.id ?? "files"} dept={dept} offices={offices} mine={nav.mine} others={nav.others} /></Suspense>;
}

type Proposed = ReturnType<typeof proposedRows>[number];
type Selected = { kind: "file"; file: DriveFile } | { kind: "proposed"; row: Proposed };
type MenuItem = RowMenuItem | "rename" | "move" | "trash";
type Target = { kind: "file"; file: DriveFile } | { kind: "folder"; folder: DriveFolder };
type DragItem = { kind: "file" | "folder"; id: string; path: string; name: string };

/** The URL parameter of the open file. Not "archivo": that one belongs to the global file viewer. */
const DOC_PARAM = "doc";
/** From this width the panel is a column beside the list; below it covers the composer. */
const WIDE = "(min-width: 1280px)";
const DRAG_TYPE = "application/x-wf-drive";

const chip = "inline-flex items-center whitespace-nowrap rounded-[6px] px-1.5 py-[1px] text-[10.5px] font-semibold";
const REVIEW = `${chip} bg-warn/10 text-warn`;
const PROPOSED = `${chip} bg-accent-soft text-accent`;
const SENT = `${chip} bg-ok/10 text-ok`;
const DRAFT = `${chip} border border-line bg-panel2 text-ink2`;
const MENU_ITEM_H = 40;
const MENU_LABEL: Record<MenuItem, string> = {
  open: "dept.menu.open", ask: "dept.menu.ask", download: "dept.menu.download", share: "dept.menu.share",
  markSent: "dept.menu.markSent", markDraft: "dept.menu.markDraft", clearStatus: "dept.menu.clearStatus",
  rename: "files.rename", move: "files.move", trash: "files.toTrash",
};
const toolBtn = "inline-flex min-h-10 items-center gap-1.5 rounded-lg border px-3 text-[12.5px] font-medium transition disabled:cursor-not-allowed disabled:opacity-50 sm:min-h-9";
const TOOL = `${toolBtn} border-line bg-panel text-ink2 hover:bg-panel2 hover:text-ink`;
const TOOL_PRIMARY = `${toolBtn} border-accent bg-accent font-semibold text-white hover:bg-accent-hover`;

const isTyping = (el: EventTarget | null) => {
  const n = el as HTMLElement | null;
  return !!n && (n.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(n.tagName));
};

function Screen({ dept, offices, mine, others }: { dept?: OfficeView; offices: OfficeView[]; mine: OfficeView[]; others: OfficeView[] }) {
  const { t } = useT();
  const router = useRouter();
  const phone = useIsPhone();
  const params = useSearchParams();
  const member = !!dept?.is_member;
  const ownRoot = (dept?.folder_path ?? "").replace(/\/+$/, "");

  // D4: what other departments share with this one ("Compartido conmigo") and the requests that leave it (members only)
  const [incoming, setIncoming] = useState<DriveShare[] | null>(member ? null : []);
  const [outgoing, setOutgoing] = useState<DriveShare[]>([]);
  const [sharesTick, setSharesTick] = useState(0);
  useEffect(() => {
    if (!dept || !member) return;
    driveShares.inbox(dept.id).then(setIncoming).catch(() => setIncoming([]));
    driveShares.out(dept.id).then(setOutgoing).catch(() => setOutgoing([]));
  }, [dept, member, sharesTick]);
  const comp = dept ? params.get("comp") : null;
  const share = shareFromParam(incoming ?? [], comp);
  const waiting = !!comp && incoming === null;
  const root = dept ? (share ? sharedRoot(share) : ownRoot) : placeRoot(params.get("path") || "/", offices);
  const path = dept ? folderFromParam(root, params.get("carpeta")) : (params.get("path") || "/").replace(/(.)\/+$/, "$1");
  const nameOfOffice = (id: string) => offices.find((o) => o.id === id)?.name ?? id;

  // a department's folder opened from /files gets its direct link (one URL per place)
  useEffect(() => {
    if (dept || sectionOf(path) !== "dept" || !officeOfPath(path, [...mine, ...others])) return;
    const doc = params.get(DOC_PARAM);
    const href = folderHref(path, [...mine, ...others]);
    router.replace(doc ? `${href}${href.includes("?") ? "&" : "?"}${DOC_PARAM}=${encodeURIComponent(doc)}` : href);
  }, [dept, path, mine, others, params, router]);

  // names: agents by name (the directory knows the ones the store does not), departments by name
  const agentNameRaw = useAgentName();
  const [dir, setDir] = useState<DirectoryEntry[] | null | undefined>(undefined);
  useEffect(() => { officesApi.directory().then((r) => setDir(r.agents)).catch(() => setDir(null)); }, []);
  const ctx: NameCtx = useMemo(() => ({
    offices, t, locale: intlLocale(),
    agentName: (id: string) => { const n = agentNameRaw(id); if (n && n !== id) return n; return dir?.find((d) => d.id === id)?.name || undefined; },
  }), [offices, t, agentNameRaw, dir]);
  const agents = useMemo(() => (!dept || dir === undefined ? null : usableAgents(dept.agents, dir)), [dir, dept]);
  const firstAgent = dept && agents && (agents.includes(defaultAgent(dept) ?? "") ? defaultAgent(dept) : agents[0]);

  // the agents' desks for the side bar (by name)
  const [deskFolders, setDeskFolders] = useState<DriveFolder[] | null>(null);
  useEffect(() => { drive.list("/Agents").then((l) => setDeskFolders(l.folders)).catch(() => setDeskFolders([])); }, []);
  const desks: Desk[] | null = useMemo(() => (deskFolders ? agentDesks(deskFolders, ctx) : null), [deskFolders, ctx]);

  // "Cambiado hoy" follows the calendar
  const [day, setDay] = useState(() => new Date().toDateString());
  useEffect(() => {
    const bump = () => setDay(new Date().toDateString());
    const timer = setTimeout(bump, msUntilMidnight(new Date()) + 1000);
    window.addEventListener("focus", bump); document.addEventListener("visibilitychange", bump);
    return () => { clearTimeout(timer); window.removeEventListener("focus", bump); document.removeEventListener("visibilitychange", bump); };
  }, [day]);

  const [listing, setListing] = useState<Listing | null>(null);
  const [error, setError] = useState("");
  const [filterPick, setFilter] = useState<DeptFilter>("all");
  const reviewable = !!dept && member && !share;
  const filter: DeptFilter = reviewable ? filterPick : "all";
  const [selected, setSelected] = useState<Selected | null>(null);
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<Hit[] | null>(null);
  const [meaning, setMeaning] = useState(true);
  const [passages, setPassages] = useState<PassageResult | null>(null);
  const [focus, setFocus] = useState<{ start: number; page?: number } | null>(null);
  const [seed, setSeed] = useState<AskSeed | null>(null);
  const [navOpen, setNavOpen] = useState(false);
  const [menu, setMenu] = useState<{ target: Target; id: string; items: MenuItem[]; top: number; right: number; trigger: HTMLButtonElement } | null>(null);
  const menuBox = useRef<HTMLDivElement>(null);
  const [marking, setMarking] = useState(false);
  const [markErr, setMarkErr] = useState(false);
  const [actionErr, setActionErr] = useState("");
  const [sharing, setSharing] = useState<{ name: string; path: string } | null>(null);
  const [naming, setNaming] = useState<{ mode: "new" } | { mode: "rename"; target: Target } | null>(null);
  const [moving, setMoving] = useState<Target | null>(null);
  const [whoSees, setWhoSees] = useState(false);
  const [trash, setTrash] = useState<DriveFile[] | null>(null);
  const [dropOn, setDropOn] = useState<string | null>(null);
  const [osDrag, setOsDrag] = useState(false);
  const [urlDoc, setUrlDoc] = useState<string | null>(() => params.get(DOC_PARAM));
  const [wantDoc, setWantDoc] = useState<string | null>(null);

  const me = useSession((s) => s.user);
  const role = useSession((s) => s.role);
  const perms = useSession((s) => s.permissions);
  const canDelete = perms.includes("files:delete") || role === "owner" || role === "admin";
  const names = useMemo(() => (dept ? personNames(dept, me) : new Map<string, string>()), [dept, me]);
  const authorText = (p: Principal) => (p.kind === "agent" ? agentNameRaw(p.id) : p.kind === "human" ? names.get(p.id) ?? t("drive.person")
    : p.kind === "system" ? t("drive.system") : p.id);
  const who = (p: Principal, copy?: boolean) => (p.kind === "human" && names.has(p.id)
    ? <span data-testid={copy ? undefined : "dept-author-person"}>{names.get(p.id)}</span> : <Author p={p} copy={copy} />);

  // pending approvals of fs actions under this place (they carry context.path)
  const approvals = useStore((s) => s.approvals);
  const pending = useMemo(() => pendingByPath(share || root === "/" ? [] : (Object.values(approvals) as Approval[]), root), [approvals, root, share]);
  const pendingSig = useMemo(() => [...pending.keys()].sort().join("|"), [pending]);
  const rootKey = root === "/" ? "/" : pathKey(root) + "/";
  const touched = useStore((s) => Object.values(s.files).filter((f) => pathKey(f.path).startsWith(rootKey)).map((f) => f.ts).join(","));

  const home = path === "/";
  const attach = isAttachmentsFolder(path);
  const sharedFileId = share?.kind === "file" ? share.file_id : undefined;
  const load = useCallback(async () => {
    if (path === "/") { setListing({ folder: { id: "", name: "", path: "/" }, folders: [], files: [], access: "read" }); return; }
    try {
      setError("");
      if (sharedFileId) {
        const f = await drive.file(sharedFileId);
        setListing({ folder: { id: "", name: "", path }, folders: [], files: [f], access: "read" });
      } else setListing(await drive.list(path));
    } catch (e) { setError(errorText(e, { fallbackKey: "files.err.load" })); }
  }, [path, sharedFileId]);
  useEffect(() => { void load(); }, [load, touched, pendingSig]);

  // the chat's attachments: the files of every day folder, grouped by date in the list
  const [attachFiles, setAttachFiles] = useState<DriveFile[] | null>(null);
  const days = useMemo(() => (attach && listing ? dayFolders(listing.folders) : null), [attach, listing]);
  useEffect(() => {
    if (!days) { setAttachFiles(null); return; }
    let live = true;
    Promise.all(days.days.map((d) => drive.list(d.path).then((l) => l.files).catch(() => [] as DriveFile[])))
      .then((all) => { if (live) setAttachFiles(all.flat()); });
    return () => { live = false; };
  }, [days]);

  const access = share ? "read" : listing?.access ?? (dept && !member ? "read" : "edit");
  const canEdit = access === "edit" && !share;
  const canCreate = canCreateHere(path, access) && !share;
  const roReason = readOnlyReason(path, access, ctx, share ? nameOfOffice(share.owner_office_id) : undefined);

  const files = useMemo(() => {
    const own = share ? onlySharedFile(listing?.files ?? [], share) : listing?.files ?? [];
    return attach ? [...own, ...(attachFiles ?? [])] : own;
  }, [listing, share, attach, attachFiles]);
  const folders: DriveFolder[] = useMemo(() => {
    if (share?.kind === "file") return [];
    const all = visibleFolders(listing?.folders ?? []);
    return attach && days ? all.filter((f) => !days.days.some((d) => d.path === f.path)) : all;
  }, [listing, share, attach, days]);
  const proposed = useMemo(() => proposedRows(pending, path, new Set(files.map((f) => f.path))), [pending, path, files]);
  const view = useMemo(() => deptView({ folder: path, folders, files, proposed, pending, now: new Date() }, filter), [path, folders, files, proposed, pending, filter, day]); // eslint-disable-line react-hooks/exhaustive-deps
  const groups = useMemo(() => (attach ? groupAttachments(view.files, new Date()) : null), [attach, view.files, day]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (selected?.kind === "proposed" && !proposed.some((p) => p.approval.id === selected.row.approval.id)) setSelected(null);
    if (selected?.kind === "file") { const f = files.find((x) => x.id === selected.file.id); if (f && f !== selected.file) setSelected({ kind: "file", file: f }); }
  }, [proposed, files]); // eslint-disable-line react-hooks/exhaustive-deps

  // the open file is in the URL (?doc=): Back closes the panel, Forward opens it again
  const selPath = selected ? (selected.kind === "file" ? selected.file.path : selected.row.path) : null;
  useDetailUrl(DOC_PARAM, selPath ?? urlDoc, (v) => { setSelected(null); setUrlDoc(v); });
  useEffect(() => {
    const target = urlDoc ?? wantDoc;
    if (!target || !listing) return;
    const key = pathKey(target);
    if (root !== "/" && !key.startsWith(rootKey)) { setUrlDoc(null); setWantDoc(null); return; }
    if (wantDoc && pathKey(parentOf(target)) !== pathKey(path) && !attach) return; // the hit's folder is still loading
    let live = true;
    const done = (sel: Selected | null) => { if (!live) return; setSelected(sel); setUrlDoc(null); setWantDoc(null); };
    const prop = proposed.find((x) => pathKey(x.path) === key);
    if (prop) { done({ kind: "proposed", row: prop }); return; }
    const f = files.find((x) => pathKey(x.path) === key);
    if (f) { done({ kind: "file", file: f }); return; }
    drive.byPath(target).then((x) => done(x ? { kind: "file", file: x } : null)).catch(() => done(null));
    return () => { live = false; };
  }, [urlDoc, wantDoc, listing, proposed, files, path, rootKey, root, attach]);

  useEffect(() => { setMarkErr(false); }, [selPath]);
  useEffect(() => { setActionErr(""); setHits(null); setPassages(null); setNavOpen(false); setMenu(null); setTrash(null); }, [path, comp]);

  const setStatus = async (f: DriveFile, status: "sent" | "draft" | null) => {
    setMenu(null); setActionErr("");
    try { await drive.setTags(f.id, withStatus(f.tags, status)); await load(); } catch { setActionErr(t("dept.status.err")); }
  };
  const markReviewed = async (f: DriveFile) => {
    if (marking) return;
    setMarking(true); setMarkErr(false);
    try { await drive.setTags(f.id, withReviewed(f.tags)); await load(); } catch { setMarkErr(true); }
    setMarking(false);
  };

  // ------------------------------------------------------------------ navigation

  /** Go to a folder (a new history entry). Inside a shared item it stays in the share unless `own`. */
  const openFolder = (p: string, own = false) => {
    setNavOpen(false); setHits(null); setPassages(null);
    if (!(own && share) && pathKey(p) === pathKey(path)) return;
    dropDetailUrl(DOC_PARAM);
    setSelected(null); setUrlDoc(null); setWantDoc(null); setWhoSees(false);
    if (dept && share && !own && pathKey(p).startsWith(pathKey(root))) { router.push(sharedHref(dept.id, share.path, folderParam(root, p)), { scroll: false }); return; }
    router.push(folderHref(p, [...mine, ...others]), { scroll: false });
  };
  const openShared = (s: DriveShare) => {
    if (!dept) return;
    setNavOpen(false); setHits(null);
    dropDetailUrl(DOC_PARAM);
    setSelected(null); setUrlDoc(null); setWantDoc(null);
    router.push(sharedHref(dept.id, s.path, ""), { scroll: false });
  };

  // ------------------------------------------------------------------ the row menu

  const closeMenu = useCallback((focusBack: boolean) => {
    setMenu((m) => { if (m && focusBack) m.trigger.focus(); return null; });
  }, []);
  useEffect(() => {
    if (!menu) return;
    menuBox.current?.querySelector<HTMLElement>("[role=menuitem]")?.focus({ preventScroll: true });
    const close = () => closeMenu(false);
    const key = (e: KeyboardEvent) => { if (e.key === "Escape") { e.preventDefault(); closeMenu(true); } };
    window.addEventListener("mousedown", close); window.addEventListener("keydown", key); window.addEventListener("resize", close);
    window.addEventListener("scroll", close, true);
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
  const movable = (target: Target) => canEdit && (target.kind === "file" || !isProtectedFolder(target.folder));
  const menuItems = (target: Target): MenuItem[] => {
    const base: MenuItem[] = reviewable
      ? rowMenuItems({ kind: target.kind, shared: false, tags: target.kind === "file" ? target.file.tags : undefined })
      : target.kind === "file" ? ["open", "download"] : ["open"];
    if (!movable(target)) return base;
    const at = base.indexOf(target.kind === "file" ? "download" : "open") + 1;
    const out = [...base.slice(0, at), "rename", "move", ...base.slice(at)] as MenuItem[];
    if (target.kind === "file" && canDelete) out.push("trash");
    return out;
  };
  const openMenu = (e: { currentTarget: HTMLButtonElement }, target: Target) => {
    const id = target.kind === "file" ? target.file.id : target.folder.id || target.folder.path;
    const items = menuItems(target);
    const r = e.currentTarget.getBoundingClientRect();
    const h = items.length * MENU_ITEM_H + 8;
    const below = r.bottom + h < window.innerHeight;
    setMenu(menu?.id === id ? null : { target, id, items, top: below ? r.bottom + 4 : Math.max(8, r.top - h), right: window.innerWidth - r.right, trigger: e.currentTarget });
  };
  const runMenu = (it: MenuItem, target: Target) => {
    const file = target.kind === "file" ? target.file : null;
    if (it === "open") {
      if (file) setSelected({ kind: "file", file }); else if (target.kind === "folder") openFolder(target.folder.path);
      closeMenu(false);
    } else if (it === "ask" && file) ask(file);
    else if (it === "download" && file) { void drive.download(file).catch((e) => setActionErr(errorText(e))); closeMenu(true); }
    else if (it === "share") {
      setSharing(target.kind === "file" ? { name: target.file.name, path: target.file.path } : { name: target.folder.name, path: target.folder.path });
      closeMenu(false);
    } else if (it === "rename") { setNaming({ mode: "rename", target }); closeMenu(false); }
    else if (it === "move") { setMoving(target); closeMenu(false); }
    else if (it === "trash" && file) { closeMenu(false); void toTrash(file); }
    else if (file && it === "markSent") void setStatus(file, "sent");
    else if (file && it === "markDraft") void setStatus(file, "draft");
    else if (file && it === "clearStatus") void setStatus(file, null);
  };
  const ask = (f: DriveFile) => {
    setMenu(null);
    if (!window.matchMedia(WIDE).matches) setSelected(null);
    setSeed({ text: t("dept.ask.about", { name: f.name }), path: f.path, name: f.name, n: Date.now() });
  };

  // ------------------------------------------------------------------ create, rename, move, trash

  const siblings = useMemo(() => [...(listing?.folders ?? []), ...(listing?.files ?? [])].map((x) => x.name), [listing]);
  const here = home ? t("files.title") : folderLabel(path, ctx);
  const createFolder = async (name: string) => {
    await drive.mkdir(joinPath(path, name));
    toast(t("files.newFolder.done", { name }));
    await load();
  };
  const rename = async (target: Target, name: string) => {
    const p = target.kind === "file" ? target.file.path : target.folder.path;
    const dest = joinPath(parentOf(p), name);
    if (target.kind === "file") await drive.moveFile(target.file.id, dest); else await drive.moveFolder(target.folder.id || `fld:${p}`, dest);
    if (selected?.kind === "file" && target.kind === "file" && selected.file.id === target.file.id) { dropDetailUrl(DOC_PARAM); setSelected(null); }
    toast(t("files.rename.done"));
    await load();
  };
  const moveTo = async (target: Target | DragItem, dest: string) => {
    const item: DragItem = "file" in target ? { kind: "file", id: target.file.id, path: target.file.path, name: target.file.name }
      : "folder" in target ? { kind: "folder", id: target.folder.id || `fld:${target.folder.path}`, path: target.folder.path, name: target.folder.name } : target;
    const to = joinPath(dest, item.name);
    if (item.kind === "file") await drive.moveFile(item.id, to); else await drive.moveFolder(item.id, to);
    if (selPath && pathKey(selPath) === pathKey(item.path)) { dropDetailUrl(DOC_PARAM); setSelected(null); }
    toast(t("files.move.done", { place: folderLabel(dest, ctx) }));
    await load();
  };
  const toTrash = async (f: DriveFile) => {
    setActionErr("");
    try {
      await drive.remove(f.id);
      if (selected?.kind === "file" && selected.file.id === f.id) { dropDetailUrl(DOC_PARAM); setSelected(null); }
      toast(t("files.toTrash.done")); await load();
    } catch (e) { setActionErr(errorText(e)); }
  };
  const showTrash = async () => {
    if (trash) { setTrash(null); return; }
    const all = (await drive.trash().catch(() => ({ files: [] as DriveFile[] }))).files;
    setTrash(root === "/" ? all : all.filter((f) => pathKey(f.path).startsWith(rootKey)));
  };
  const restore = async (f: DriveFile) => {
    try { await drive.restore(f.id); setTrash((x) => x?.filter((y) => y.id !== f.id) ?? null); await load(); }
    catch (e) { setActionErr(errorText(e)); }
  };

  // "N": a new folder where the person may create one
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() !== "n" || e.ctrlKey || e.metaKey || e.altKey || isTyping(e.target) || naming || moving || sharing || menu) return;
      if (!canCreate || document.querySelector("[role=dialog][aria-modal=true]")) return;
      e.preventDefault(); setNaming({ mode: "new" });
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [canCreate, naming, moving, sharing, menu]);

  // ------------------------------------------------------------------ uploads and drag and drop

  const up = useDriveUpload(path, () => void load());
  const fileInput = useRef<HTMLInputElement>(null);
  const dragData = (e: DragEvent): DragItem | null => {
    try { const raw = e.dataTransfer.getData(DRAG_TYPE); return raw ? JSON.parse(raw) as DragItem : null; } catch { return null; }
  };
  const isInternal = (e: DragEvent) => Array.from(e.dataTransfer.types).includes(DRAG_TYPE);
  const dragProps = (target: Target) => (movable(target) && !phone ? {
    draggable: true,
    onDragStart: (e: DragEvent) => {
      const it: DragItem = target.kind === "file" ? { kind: "file", id: target.file.id, path: target.file.path, name: target.file.name }
        : { kind: "folder", id: target.folder.id || `fld:${target.folder.path}`, path: target.folder.path, name: target.folder.name };
      e.dataTransfer.setData(DRAG_TYPE, JSON.stringify(it)); e.dataTransfer.effectAllowed = "move";
    },
  } : {});
  /** A folder row (or a breadcrumb) that receives a dragged row: the move is checked first, as the move dialog does. */
  const dropProps = (dest: string) => (canEdit ? {
    onDragOver: (e: DragEvent) => { if (isInternal(e)) { e.preventDefault(); e.stopPropagation(); e.dataTransfer.dropEffect = "move"; setDropOn(dest); } },
    onDragLeave: () => setDropOn((d) => (d === dest ? null : d)),
    onDrop: (e: DragEvent) => {
      if (!isInternal(e)) return;
      e.preventDefault(); e.stopPropagation(); setDropOn(null);
      const it = dragData(e);
      if (!it) return;
      const problem = moveError(it, dest);
      if (problem) { if (problem !== "files.move.same") setActionErr(t(problem)); return; }
      setActionErr("");
      moveTo(it, dest).catch((err) => setActionErr(errorText(err)));
    },
  } : {});

  // ------------------------------------------------------------------ search

  const search = async () => {
    const q = query.trim();
    if (!q) { setHits(null); setPassages(null); return; }
    const scope = root === "/" ? "" : root;
    if (meaning) {
      const r = await docSearch.passages(q, scope).catch(() => null);
      if (r?.enabled) { setHits(null); setPassages(r); return; }
    }
    setPassages(null);
    setHits((await drive.search(q, scope).catch(() => [])).filter((h) => !isHiddenPath(h.path)));
  };
  const openHit = (p: string) => {
    setQuery(""); setPassages(null); setHits(null);
    const parent = parentOf(p);
    if (pathKey(parent) === pathKey(path)) { setWantDoc(p); return; }
    openFolder(isAttachmentsFolder(parentOf(parent)) ? parentOf(parent) : parent);
    setWantDoc(p);
  };
  const openPassage = async (p: Passage) => {
    openHit(p.path);
    setFocus({ start: p.start, page: p.page });
  };

  // ------------------------------------------------------------------ "N agentes pueden consultar esto"

  const [seers, setSeers] = useState<number | null>(null);
  const showSeers = !home && pathKey(path) !== "/offices" && pathKey(path) !== "/agents" && !share;
  useEffect(() => {
    if (!showSeers) { setSeers(null); return; }
    let live = true;
    folderAccess.folder(path).then((a) => live && setSeers(a.filter((x) => x.level !== "none").length)).catch(() => live && setSeers(null));
    return () => { live = false; };
  }, [path, showSeers]);

  const when = (iso: string) => (lastChange(iso, new Date()).relative ? timeAgo(iso)
    : new Date(iso).toLocaleDateString(intlLocale(), { day: "numeric", month: "short" }));
  const trail = share ? [{ label: share.name, path: root }, ...humanCrumbs(path, ctx).filter((c) => pathKey(c.path).startsWith(pathKey(root) + "/"))] : humanCrumbs(path, ctx);
  const title = share && pathKey(path) === pathKey(root) ? share.name : here;
  const help = sectionHelp(path, ctx);
  const selWhere = selPath ? humanCrumbs(parentOf(selPath), ctx).map((c) => c.label).join(" › ") : "";
  const selPending = selPath ? pending.get(pathKey(selPath)) ?? [] : [];
  const empty = !view.folders.length && !view.files.length && !view.proposed.length;
  const showStatus = !!dept;

  if (waiting) return <PageSkeleton />;

  // the place's folder tree in the side bar (a department, a section, an agent's desk)
  const navRoot = dept ? ownRoot : root;
  const treeName = dept ? dept.name : navRoot === "/" ? "" : folderLabel(navRoot, ctx);
  const deskId = sectionOf(navRoot) === "agents" && navRoot.split("/").filter(Boolean).length === 2 ? navRoot.split("/").pop() : undefined;
  const tree = navRoot === "/" || pathKey(navRoot) === "/agents" ? null : (
    <DeptTree root={navRoot} rootName={treeName} current={share ? ownRoot : path} pending={pending} onOpen={(p) => openFolder(p, true)}
      rootIcon={deskId ? <Avatar id={deskId} size={16} /> : undefined}
      refresh={touched + pendingSig} label={(f) => folderLabel(f.path, ctx)} hide={isHiddenPath} leaf={isAttachmentsFolder}
      {...(dept && member ? { shared: incoming ?? [], sharedCurrent: share?.path ?? null, ownerName: nameOfOffice, onOpenShared: openShared } : {})} />
  );
  const side = <FilesNav root={navRoot} depts={mine} others={others} desks={desks} tree={tree} onOpen={(p) => openFolder(p, true)} />;

  // where "Mover a…" can go: my departments, Compartido con todos, Proyectos and Clientes
  const pickRoots: PickRoot[] = [
    ...mine.map((o) => ({ path: o.folder_path!.replace(/\/+$/, ""), label: o.name })),
    { path: "/Shared", label: t("files.section.shared") }, { path: "/Projects", label: t("files.section.projects") }, { path: "/Clients", label: t("files.section.clients") },
  ];

  const folderRow = (f: DriveFolder) => {
    const target: Target = { kind: "folder", folder: f };
    const n = pendingUnder(pending, f.path);
    const label = folderLabel(f.path, ctx);
    const desk = pathKey(parentOf(f.path)) === "/agents";
    const icon = desk ? <Avatar id={f.name} size={16} /> : <Icon name="folder" size={15} className="shrink-0 text-mute" />;
    return (
      <tr key={f.id || f.path} data-testid={`dept-folder-${label}`} onClick={() => openFolder(f.path)} {...dragProps(target)} {...dropProps(f.path)}
        className={`cursor-pointer border-t border-line first:border-t-0 ${dropOn === f.path ? "bg-accent-soft outline outline-1 outline-accent" : "hover:bg-panel2"}`}>
        <td className="py-2.5 pl-3">
          <span className="flex min-w-0 items-center gap-2 font-medium text-ink">{icon}
            <button type="button" onClick={(e) => { e.stopPropagation(); openFolder(f.path); }} aria-label={t("files.open", { name: label })}
              className="min-w-0 break-words rounded text-left [overflow-wrap:anywhere] hover:underline">{label}</button>
            {dropOn === f.path && <span className="shrink-0 text-[11px] font-normal text-accent">{t("files.move.drop", { name: label })}</span>}
          </span>
        </td>
        <td className="hidden sm:table-cell" />
        {showStatus && <td>{n > 0 && <span className={REVIEW} title={t("dept.reviewUnder", { n })}>{t("dept.reviewUnder", { n })}</span>}</td>}
        <td className="pr-1 text-right" onClick={(e) => e.stopPropagation()}>
          {(reviewable || movable(target)) && (
            <button type="button" data-testid={`dept-menu-${label}`} aria-label={t("dept.menu.more")} title={t("dept.menu.more")} aria-haspopup="menu"
              aria-expanded={menu?.id === (f.id || f.path)} aria-controls={menu?.id === (f.id || f.path) ? "dept-row-menu" : undefined}
              onMouseDown={(e) => e.stopPropagation()} onClick={(e) => openMenu(e, target)}
              className="inline-flex h-9 w-9 items-center justify-center rounded-md text-[16px] font-semibold leading-none text-mute hover:bg-panel2 hover:text-ink">⋯</button>
          )}
        </td>
      </tr>
    );
  };
  const fileRow = (f: DriveFile) => {
    const target: Target = { kind: "file", file: f };
    const sel = selected?.kind === "file" && selected.file.id === f.id;
    const status = share ? null : fileStatus(f, pending);
    return (
      <tr key={f.id} data-testid={`dept-file-${f.name}`} data-selected={sel} onClick={() => { setSelected({ kind: "file", file: f }); setFocus(null); }} {...dragProps(target)}
        className={`cursor-pointer border-t border-line first:border-t-0 ${sel ? "bg-accent-soft" : "hover:bg-panel2"}`}>
        <td className="py-2.5 pl-3">
          <span className="flex min-w-0 items-start gap-2 text-ink"><Icon name="doc" size={15} className="mt-[2px] shrink-0 text-mute" />
            <button type="button" data-testid={`dept-open-${f.name}`} onClick={(e) => { e.stopPropagation(); setSelected({ kind: "file", file: f }); setFocus(null); }}
              className="min-w-0 break-words rounded text-left [overflow-wrap:anywhere] hover:underline">{f.name}</button></span>
          <span className="mt-0.5 flex flex-wrap gap-x-1.5 pl-[23px] text-[11.5px] text-mute sm:hidden">{who(f.updated_by, true)}<span aria-hidden>·</span><span>{when(f.updated_at)}</span></span>
        </td>
        <td className="hidden py-2 pr-2 text-[12px] text-ink2 sm:table-cell">
          <div data-testid="dept-changed" className="truncate whitespace-nowrap" title={`${authorText(f.updated_by)} · ${when(f.updated_at)}`}>
            {who(f.updated_by)} <span className="text-mute">·&nbsp;{when(f.updated_at)}</span>
          </div>
        </td>
        {showStatus && <td>
          {status === "review" && <span data-testid="dept-chip-review" className={REVIEW}>{t("dept.review")}</span>}
          {status === "sent" && <span data-testid="dept-chip-sent" className={SENT}>{t("dept.status.sent")}</span>}
          {status === "draft" && <span data-testid="dept-chip-draft" className={DRAFT}>{t("dept.status.draft")}</span>}
        </td>}
        <td className="pr-1 text-right" onClick={(e) => e.stopPropagation()}>
          <button type="button" data-testid={`dept-menu-${f.name}`} aria-label={t("dept.menu.more")} title={t("dept.menu.more")} aria-haspopup="menu"
            aria-expanded={menu?.id === f.id} aria-controls={menu?.id === f.id ? "dept-row-menu" : undefined}
            onMouseDown={(e) => e.stopPropagation()} onClick={(e) => openMenu(e, target)}
            className="inline-flex h-9 w-9 items-center justify-center rounded-md text-[16px] font-semibold leading-none text-mute hover:bg-panel2 hover:text-ink">⋯</button>
        </td>
      </tr>
    );
  };
  const cols = showStatus ? 4 : 3;

  return (
    <div data-testid="dept-drive" className="relative flex h-full min-h-0 bg-bg">
      {/* left: the sections and the folders of the place (desktop) */}
      <aside className="hidden w-[248px] shrink-0 overflow-y-auto border-r border-line bg-panel px-2 py-3 md:block">{side}</aside>

      <section className="flex min-w-0 flex-1 flex-col"
        onDragOver={(e) => { if (canCreate && !isInternal(e) && Array.from(e.dataTransfer.types).includes("Files")) { e.preventDefault(); setOsDrag(true); } }}
        onDragLeave={(e) => { if (e.currentTarget === e.target) setOsDrag(false); }}
        onDrop={(e) => { if (isInternal(e)) return; e.preventDefault(); setOsDrag(false); if (canCreate && e.dataTransfer.files.length) void up.upload(e.dataTransfer.files); }}>
        <header data-tour="files.header" className="shrink-0 border-b border-line bg-panel px-3 pb-2.5 pt-3 sm:px-5">
          {/* the trail, in plain words, on its own line */}
          {trail.length > 1 && (
            <nav aria-label={t("files.where")} data-tour="files.crumbs" data-testid="files-crumbs" className="-ml-1.5 mb-1 flex min-w-0 flex-wrap items-center gap-0.5 text-[12.5px]">
              {trail.map((c, i) => {
                const last = i === trail.length - 1;
                return (
                  <span key={c.path} className="flex min-w-0 max-w-full items-center gap-0.5">
                    {i > 0 && <Icon name="chevron" size={11} className="shrink-0 text-mute" />}
                    <button type="button" onClick={() => openFolder(c.path)} aria-current={last ? "page" : undefined} {...(last ? {} : dropProps(c.path))}
                      className={`min-h-8 max-w-[60vw] truncate rounded px-1.5 sm:max-w-[280px] ${dropOn === c.path ? "bg-accent-soft text-accent" : last ? "font-semibold text-ink" : "text-mute hover:bg-panel2 hover:text-ink"}`}>{c.label}</button>
                  </span>
                );
              })}
            </nav>
          )}
          <div className="flex flex-wrap items-start gap-x-3 gap-y-2">
            <div className="min-w-0 flex-1 basis-[220px]">
              <h1 data-testid="dept-title" className="min-w-0 break-words font-display text-[18px] font-semibold leading-tight tracking-tight text-ink [overflow-wrap:anywhere] sm:text-xl">{title}</h1>
              <p data-testid="files-help" className="mt-0.5 text-[12.5px] leading-snug text-mute">{home ? t("files.desc") : t(help.key, help.vars)}</p>
              {dept && (
                <Link href={`/d/${encodeURIComponent(dept.id)}/puestos`} data-testid="dept-positions-link"
                  className="mt-1.5 inline-flex min-h-8 items-center rounded-[10px] border border-line px-2.5 text-[12px] font-medium text-accent hover:bg-accent-soft">{t("pos.link")}</Link>
              )}
            </div>
            {!home && (
              <div className="flex w-full flex-wrap items-center gap-1.5 sm:w-auto">
                <button type="button" data-testid="files-new-folder" onClick={() => setNaming({ mode: "new" })} disabled={!canCreate}
                  title={canCreate ? t("files.newFolder.hint") : roReason ? t(roReason.key, roReason.vars) : undefined} aria-keyshortcuts="N" className={TOOL}>
                  <Icon name="folder" size={14} />{t("files.newFolder")}</button>
                <input ref={fileInput} type="file" multiple className="hidden" onChange={(e) => { if (e.target.files?.length) void up.upload(e.target.files); e.target.value = ""; }} />
                <span data-tour="files.upload" className="inline-flex">
                  <button type="button" data-testid="drive-upload" onClick={() => fileInput.current?.click()} disabled={!canCreate || up.busy}
                    title={!canCreate && roReason ? t(roReason.key, roReason.vars) : undefined} className={TOOL_PRIMARY}>
                    <Icon name="plus" size={14} />{up.busy ? `${t("files.uploading")}${up.progress ? ` ${Math.floor(up.progress * 100)} %` : ""}` : t("files.upload")}</button>
                </span>
                <span data-tour="files.trash" className="inline-flex">
                  <button type="button" data-testid="drive-trash" aria-pressed={!!trash} onClick={() => void showTrash()} aria-label={t("files.trash")} title={t("files.trash")}
                    className={`${TOOL} px-2.5 ${trash ? "border-accent text-accent" : ""}`}><Icon name="trash" size={14} /><span className="sm:sr-only">{t("files.trash")}</span></button>
                </span>
              </div>
            )}
          </div>
          {/* who can use it, and why something is off */}
          {(!home && (showSeers && seers !== null || roReason)) && (
            <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1">
              {showSeers && seers !== null && (
                <button type="button" data-testid="files-agents-see" onClick={() => { setWhoSees(!whoSees); setSelected(null); }} aria-pressed={whoSees}
                  className="inline-flex min-h-8 items-center gap-1.5 rounded-md px-1.5 text-[12px] font-medium text-accent hover:bg-accent-soft">
                  <Icon name="bot" size={13} />{seers === 0 ? t("files.agentsSee.none") : seers === 1 ? t("files.agentsSee.one") : t("files.agentsSee.many", { n: seers })}
                </button>
              )}
              {roReason && <p role="note" data-testid="files-readonly" className="inline-flex items-center gap-1.5 text-[12px] text-ink2"><Icon name="lock" size={12} className="shrink-0 text-mute" />{t(roReason.key, roReason.vars)}</p>}
              <IndexStatusChip path={path} refresh={touched} />
            </div>
          )}
          {/* search (by meaning or by name, inside this place; everywhere at the home) and Para revisar */}
            <div className="mt-2 flex flex-wrap items-center gap-1.5">
              <form role="search" className="relative min-w-0 flex-1 basis-[200px] sm:max-w-sm" onSubmit={(e) => { e.preventDefault(); void search(); }}>
                <Icon name="search" size={14} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-mute" />
                <input data-tour="files.search" data-testid="dept-search" type="search" value={query} onChange={(e) => { setQuery(e.target.value); if (!e.target.value) { setHits(null); setPassages(null); } }}
                  placeholder={meaning ? t("drive.meaning.placeholder") : t("dept.search")} aria-label={t("dept.search")}
                  className="min-h-10 w-full rounded-lg border border-line bg-panel2 pl-8 pr-3 text-base text-ink outline-none focus:border-accent sm:min-h-9 sm:text-[13px]" />
              </form>
              <button type="button" data-testid="drive-meaning" aria-pressed={meaning} title={t("drive.meaning.help")} onClick={() => setMeaning(!meaning)}
                className={`min-h-10 rounded-lg border px-2.5 text-[12px] sm:min-h-9 ${meaning ? "border-accent bg-accent-soft text-accent" : "border-line text-mute hover:text-ink"}`}>{t("drive.meaning.toggle")}</button>
              {reviewable && (["all", "review", "today"] as const).map((f) => (
                <button key={f} type="button" data-testid={`dept-filter-${f}`} aria-pressed={filter === f} onClick={() => { setFilter(f); setHits(null); }}
                  className={`inline-flex min-h-10 items-center gap-1.5 rounded-lg border px-3 text-[12.5px] font-medium transition sm:min-h-9 ${filter === f
                    ? "border-accent bg-accent-soft text-accent" : "border-line bg-panel text-ink2 hover:bg-panel2 hover:text-ink"}`}>
                  {f === "all" ? t("dept.filter.all") : f === "review" ? t("dept.review") : t("dept.filter.today")}
                  {f === "review" && view.reviewCount > 0 && (
                    <span data-testid="dept-review-count" className={`rounded-[6px] px-1.5 text-[11px] font-semibold ${filter === f ? "bg-accent text-white" : "bg-warn/10 text-warn"}`}>{view.reviewCount}</span>
                  )}
                </button>
              ))}
            </div>
          {/* phone: the sections fold under one button */}
          <div className="mt-2 md:hidden">
            <button type="button" data-testid="dept-tree-toggle" aria-expanded={navOpen} onClick={() => setNavOpen(!navOpen)}
              className="flex min-h-10 w-full items-center gap-2 rounded-lg border border-line bg-panel px-3 text-left text-[13px] text-ink">
              <Icon name="folder" size={14} className="text-accent" />
              <span className="text-mute">{t("files.where")}:</span>
              <span className="min-w-0 flex-1 truncate font-semibold">{title}</span>
              <Icon name="down" size={14} className={`text-mute transition-transform ${navOpen ? "rotate-180" : ""}`} />
            </button>
            {navOpen && <div className="mt-1.5 max-h-[55dvh] overflow-y-auto rounded-lg border border-line bg-panel p-1.5">{side}</div>}
          </div>
        </header>
        {up.progress !== null && (
          <div role="progressbar" aria-label={t("files.uploading")} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.floor(up.progress * 100)} data-testid="drive-upload-progress"
            className="h-[3px] w-full shrink-0 overflow-hidden bg-accent/15">
            <div className="h-full bg-accent transition-[width] duration-200" style={{ width: `${Math.floor(up.progress * 100)}%` }} />
          </div>
        )}

        <div data-tour="files.list" className="min-h-0 flex-1 overflow-y-auto px-3 py-3 sm:px-5">
          {share && (
            <div data-testid="dept-shared-banner" className="mb-3 flex items-center gap-2 rounded-[10px] border border-line bg-panel2 px-3 py-2 text-[12.5px] text-ink2">
              <Icon name="folder" size={14} className="shrink-0 text-accent" />{t("dept.shared.banner", { office: nameOfOffice(share.owner_office_id) })}
            </div>
          )}
          {reviewable && !hits && !passages && <ShareRequests shares={outgoing} meId={me?.id} nameOfPerson={(id) => names.get(id) ?? t("drive.person")} nameOfOffice={nameOfOffice}
            onChanged={() => setSharesTick((n) => n + 1)} />}
          {(actionErr || up.error) && (
            <div role="alert" data-testid="drive-error" className="mb-3 flex items-start gap-2 rounded-lg border border-err/30 bg-err/5 px-3 py-2 text-[12.5px] text-err">
              <span className="min-w-0 flex-1">{actionErr || up.error}</span>
              <button type="button" onClick={() => { setActionErr(""); up.clearError(); }} aria-label={t("files.close")} className="shrink-0 rounded p-0.5 hover:bg-err/10"><Icon name="x" size={13} /></button>
            </div>
          )}
          {osDrag && <div className="mb-3 rounded-[10px] border-2 border-dashed border-accent px-4 py-6 text-center text-[13px] text-accent">{t("files.dropHere", { place: here })}</div>}
          {passages ? (
            <PassageResults result={passages} onOpen={(p) => void openPassage(p)} onClose={() => setPassages(null)} />
          ) : hits ? (
            <div data-testid="dept-search-results" className="space-y-2">
              <div className="flex items-center justify-between gap-2">
                <h2 className="text-[13px] font-semibold text-ink">{t("drive.results", { n: hits.length })}</h2>
                <button type="button" onClick={() => setHits(null)} className="min-h-9 rounded-lg px-2 text-[12.5px] font-medium text-accent hover:bg-accent-soft">{t("drive.close")}</button>
              </div>
              {!hits.length && <Empty>{t("drive.noResults")}</Empty>}
              {hits.map((h) => (
                <button type="button" key={h.file_id} onClick={() => openHit(h.path)}
                  className="block w-full rounded-[10px] border border-line bg-panel px-3 py-2 text-left hover:border-accent">
                  <div className="text-[13px] font-medium text-ink">{h.name}</div>
                  <div className="truncate text-[11px] text-mute">{humanCrumbs(parentOf(h.path), ctx).map((c) => c.label).join(" › ")}</div>
                  {h.snippet && <div className="mt-1 line-clamp-2 text-[12px] text-ink2">{h.snippet.replace(/[«»]/g, "")}</div>}
                </button>
              ))}
            </div>
          ) : home ? (
            <HomeSections mine={mine} others={others} desks={desks} onOpen={openFolder} />
          ) : trash ? (
            <div data-testid="drive-trash-list" className="space-y-2">
              <div className="flex items-center justify-between gap-2">
                <h2 className="text-[13px] font-semibold text-ink">{t("files.trash.title")}</h2>
                <button type="button" onClick={() => setTrash(null)} className="min-h-9 rounded-lg px-2 text-[12.5px] font-medium text-accent hover:bg-accent-soft">{t("drive.close")}</button>
              </div>
              {!trash.length && <Empty>{t("files.trash.empty")}</Empty>}
              {trash.map((f) => (
                <div key={f.id} className="flex items-center gap-2 rounded-[10px] border border-line bg-panel px-3 py-2 text-[13px]">
                  <span className="min-w-0 flex-1"><span className="block truncate font-medium text-ink">{f.name}</span>
                    <span className="block truncate text-[11px] text-mute">{humanCrumbs(parentOf(f.path), ctx).map((c) => c.label).join(" › ")}</span></span>
                  {canEdit && <button type="button" onClick={() => void restore(f)} className="min-h-9 shrink-0 rounded-lg border border-line px-2.5 text-[12px] font-medium text-ink hover:bg-panel2">{t("files.trash.restore")}</button>}
                </div>
              ))}
            </div>
          ) : error ? (
            <Empty testid="files-error">{error}</Empty>
          ) : !listing || (attach && attachFiles === null) ? (
            <Loading rows={6} />
          ) : empty ? (
            <EmptyState testid="dept-empty" icon="folder" compact
              title={filter !== "all" ? undefined : attach ? undefined : t("files.empty.title")}
              body={filter !== "all" ? t("dept.emptyFilter") : attach ? t("files.attachments.empty") : canCreate ? t("files.empty.edit") : t("files.empty.read")} />
          ) : (
            <div className="overflow-hidden rounded-[10px] border border-line bg-panel">
              <table data-testid="dept-table" className="w-full table-fixed text-left text-[13px]">
                <thead className="border-b border-line bg-panel2/60 text-[11px] uppercase tracking-wide text-mute">
                  <tr>
                    <th className="py-2 pl-3 font-semibold">{t("dept.col.name")}</th>
                    <th className="hidden w-[30%] font-semibold sm:table-cell">{t("dept.col.changed")}</th>
                    {showStatus && <th className="w-[100px] font-semibold max-sm:w-[96px]">{t("dept.col.status")}</th>}
                    <th className="w-10"><span className="sr-only">{t("dept.menu.more")}</span></th>
                  </tr>
                </thead>
                <tbody>
                  {view.proposed.map((p) => {
                    const sel = selected?.kind === "proposed" && selected.row.approval.id === p.approval.id;
                    return (
                      <tr key={`p:${p.approval.id}`} data-testid={`dept-proposed-${p.name}`} data-selected={sel} onClick={() => setSelected({ kind: "proposed", row: p })}
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
                          <div className="truncate whitespace-nowrap"><Author p={{ kind: "agent", id: p.approval.agent_id }} /> <span className="text-mute">·&nbsp;{when(p.approval.created_at)}</span></div>
                        </td>
                        {showStatus && <td><span data-testid="dept-chip-proposed" className={`${PROPOSED} border border-accent/25`}>{t("dept.proposed")}</span></td>}
                        <td />
                      </tr>
                    );
                  })}
                  {view.folders.map(folderRow)}
                  {groups ? groups.map((g) => (
                    <GroupRows key={g.group} label={t(`files.group.${g.group}`)} cols={cols}>{g.files.map(fileRow)}</GroupRows>
                  )) : view.files.map(fileRow)}
                </tbody>
              </table>
            </div>
          )}
        </div>

        {reviewable && <DeptComposer agents={agents} initialAgent={firstAgent ?? undefined} folder={path} seed={seed} />}
      </section>

      {menu && (
        <div ref={menuBox} id="dept-row-menu" role="menu" aria-label={menu.target.kind === "file" ? menu.target.file.name : folderLabel(menu.target.folder.path, ctx)} data-testid="dept-row-menu" onKeyDown={menuKeys}
          onMouseDown={(e) => e.stopPropagation()} style={{ top: menu.top, right: menu.right }}
          className="fixed z-50 w-60 rounded-[8px] border border-line bg-panel py-1 shadow-float">
          {menu.items.map((it) => (
            <button key={it} type="button" role="menuitem" tabIndex={-1} data-testid={`dept-menu-${it}`} onClick={() => runMenu(it, menu.target)}
              className={`flex min-h-10 w-full items-center px-3 text-left text-[13px] outline-none hover:bg-panel2 focus:bg-accent-soft focus:text-accent sm:min-h-9 ${it === "trash" ? "text-err" : "text-ink"}`}>{t(MENU_LABEL[it])}</button>
          ))}
        </div>
      )}

      {/* "Qué agentes la ven": a column on a desktop, the whole screen on a phone (never a sideways scroll) */}
      {whoSees && !selected && showSeers && (
        <div className="flex max-md:fixed max-md:inset-0 max-md:z-50 max-md:[&>aside]:w-full">
          <FolderAgentsPanel path={path} label={trail.map((c) => c.label).join(" › ")} onClose={() => setWhoSees(false)} />
        </div>
      )}

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
              {reviewable && agentUnreviewed(selected.file) && (
                <button type="button" data-testid="dept-panel-reviewed" disabled={marking} onClick={() => void markReviewed(selected.file)}
                  className="inline-flex min-h-10 items-center gap-1 rounded-lg bg-accent px-3 text-[12.5px] font-semibold text-white hover:bg-accent-hover disabled:opacity-50 sm:min-h-8">
                  <Icon name="check" size={13} />{t("dept.markReviewed")}</button>
              )}
              <button type="button" data-testid="files-panel-viewer" onClick={() => useFileViewer.getState().open({ path: selected.file.path, name: selected.file.name, file_id: selected.file.id })}
                className="min-h-10 rounded-lg border border-line-strong bg-panel px-3 text-[12.5px] font-medium text-ink hover:bg-panel2 sm:min-h-8">{t("files.openViewer")}</button>
              {reviewable && (
                <button type="button" data-testid="dept-panel-ask" onClick={() => ask(selected.file)}
                  className="min-h-10 rounded-lg border border-line-strong bg-panel px-3 text-[12.5px] font-medium text-ink hover:bg-panel2 sm:min-h-8">{t("dept.menu.ask")}</button>
              )}
            </>
          ) : undefined}
          onClose={() => setSelected(null)}
          className="xl:w-[380px] 2xl:w-[440px] max-md:fixed max-md:inset-x-0 max-md:bottom-0 max-md:top-[14dvh] max-md:z-50 max-md:rounded-t-[10px] max-md:border-t max-md:border-line max-md:shadow-float">
          {markErr && <div role="alert" data-testid="dept-panel-reviewed-err" className="shrink-0 border-b border-line bg-err/5 px-4 py-2 text-[12px] text-err">{t("dept.markReviewed.err")}</div>}
          {reviewable && dept && selected.kind === "file" && (
            <ShareAccess path={selected.file.path} ownerName={dept.name} nameOfOffice={nameOfOffice} tick={sharesTick} />
          )}
          {selPending.length > 0 && (
            <div data-testid="dept-panel-pending" className="max-h-[50%] shrink-0 space-y-2 overflow-y-auto border-b border-line bg-panel2/50 p-3">
              {selPending.map((a) => <ApprovalPending key={a.id} a={a} variant="compact" />)}
            </div>
          )}
          {selected.kind === "file" ? (
            <FilePane key={selected.file.id} file={selected.file} embedded readOnly={!canEdit} focus={focus} onClearFocus={() => setFocus(null)} onClose={() => setSelected(null)}
              onChanged={() => void load()} onDeleted={() => { setSelected(null); void load(); }} />
          ) : (
            <div className="min-h-0 flex-1 overflow-y-auto p-4 text-[12.5px] leading-relaxed text-mute">{t("dept.proposedBody")}</div>
          )}
          <details data-testid="files-details" className="shrink-0 border-t border-line px-4 py-2 text-[11.5px] text-mute">
            <summary className="cursor-pointer select-none py-1 font-medium text-ink2">{t("files.details")}</summary>
            <div className="pb-1">{t("files.details.where")}: <code className="break-all font-mono text-[11px]">{selPath}</code></div>
          </details>
        </DetailPanel>
      )}

      {sharing && dept && <ShareDialog name={sharing.name} path={sharing.path} owner={dept} offices={offices} onClose={() => setSharing(null)}
        onShared={() => { setSharesTick((n) => n + 1); void load(); }} />}
      {naming?.mode === "new" && (
        <NameDialog title={t("files.newFolder.title", { place: here })} label={t("files.newFolder.label")} cta={t("files.newFolder.create")}
          siblings={siblings} submit={createFolder} onClose={() => setNaming(null)} />
      )}
      {naming?.mode === "rename" && (() => {
        const tg = naming.target;
        const name = tg.kind === "file" ? tg.file.name : tg.folder.name;
        return <NameDialog title={t("files.rename.title", { name })} label={t("files.rename.label")} cta={t("files.rename.save")} initial={name} original={name}
          file={tg.kind === "file"} siblings={siblings} submit={(n) => rename(tg, n)} onClose={() => setNaming(null)} />;
      })()}
      {moving && (
        <MoveDialog item={moving.kind === "file" ? { kind: "file", path: moving.file.path, name: moving.file.name } : { kind: "folder", path: moving.folder.path, name: moving.folder.name }}
          roots={pickRoots} ctx={ctx} onMove={(dest) => moveTo(moving, dest)} onClose={() => setMoving(null)} />
      )}
    </div>
  );
}

/** A date heading ("Hoy", "Esta semana"...) and its rows, inside the table of the chat's attachments. */
function GroupRows({ label, cols, children }: { label: string; cols: number; children: ReactNode }) {
  return (
    <>
      <tr data-testid={`files-group-${label}`} className="border-t border-line first:border-t-0 bg-panel2/50">
        <th scope="rowgroup" colSpan={cols} className="py-1.5 pl-3 text-left text-[11px] font-semibold uppercase tracking-wide text-mute">{label}</th>
      </tr>
      {children}
    </>
  );
}

/** The home of Files: every section, in plain words, with what goes there. */
function HomeSections({ mine, others, desks, onOpen }: { mine: OfficeView[]; others: OfficeView[]; desks: Desk[] | null; onOpen: (p: string) => void }) {
  const { t } = useT();
  const card = (path: string, title: string, body: string, icon: IconName | ReactNode, extra?: ReactNode, testid?: string) => (
    <li key={path}>
      <button type="button" data-testid={testid} onClick={() => onOpen(path)}
        className="flex h-full w-full items-start gap-3 rounded-[10px] border border-line bg-panel p-3 text-left transition hover:border-accent focus-visible:border-accent">
        <span aria-hidden className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-accent-soft text-accent">
          {typeof icon === "string" ? <Icon name={icon as IconName} size={16} /> : icon}
        </span>
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-1.5 text-[13.5px] font-semibold text-ink"><span className="min-w-0 truncate">{title}</span>{extra}</span>
          <span className="mt-0.5 block text-[12px] leading-snug text-mute">{body}</span>
        </span>
      </button>
    </li>
  );
  const grid = "grid gap-2 sm:grid-cols-2 xl:grid-cols-3";
  const h2 = "mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-mute";
  return (
    <div data-testid="files-home" className="space-y-5">
      <section>
        <h2 className={h2}>{t("files.section.depts")}</h2>
        {mine.length || others.length ? (
          <ul className={grid}>
            {mine.map((o) => card(o.folder_path!.replace(/\/+$/, ""), o.name, t(o.visibility === "private" ? "files.help.deptPrivate" : "files.help.dept", { name: o.name }), "folder",
              o.visibility === "private" ? <Icon name="lock" size={12} className="shrink-0 text-mute" /> : undefined, `files-home-dept-${o.name}`))}
            {others.map((o) => card(o.folder_path!.replace(/\/+$/, ""), o.name, t("files.help.deptReader", { name: o.name }), "folder",
              <span className="shrink-0 text-[10.5px] font-normal text-mute">{t("files.readOnlyTag")}</span>, `files-home-dept-${o.name}`))}
          </ul>
        ) : <p className="text-[12.5px] text-mute">{t("files.depts.none")}</p>}
      </section>
      <section>
        <h2 className={h2}>{t("files.title")}</h2>
        <ul className={grid}>
          {card("/Shared", t("files.section.shared"), t("files.help.shared"), "users", undefined, "files-home-shared")}
          {card("/Projects", t("files.section.projects"), t("files.help.projects"), "layers", undefined, "files-home-projects")}
          {card("/Clients", t("files.section.clients"), t("files.help.clients"), "briefcase", undefined, "files-home-clients")}
          {card("/Agents", t("files.section.agents"), t("files.help.agents"), "bot", desks && desks.length > 0 ? (
            <span className="ml-auto flex shrink-0 -space-x-1">{desks.slice(0, 4).map((d) => <Avatar key={d.id} id={d.id} size={16} />)}</span>) : undefined, "files-home-agents")}
        </ul>
      </section>
    </div>
  );
}
