"use client";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useT, fmtNumber } from "@/lib/i18n";
import { drive, DriveError, isTextual, parentOf, useUploadLimit, type DriveFile, type DriveVersion, type Hit, type Listing, type UploadResult } from "@/lib/drive";
import { useStore } from "@/lib/store";
import { toast } from "@/lib/toast";
import { Btn, Empty, Loading } from "../ui";
import { FolderAgentsPanel } from "./FolderAgentsPanel";
import { EmptyState } from "../nav/ui2";
import { ConceptHelp } from "../concepts/ConceptHelp";
import { Icon } from "../icons";
import { useAgentName } from "../ui";
import { docSearch, type Passage, type PassageResult } from "@/lib/docsearch";
import { officesApi } from "@/lib/officesApi";
import { privateOfficeFolders, rootLabel } from "@/lib/officeFoldersLogic";
import { IndexStatusChip, PassageResults } from "./PassageSearch";


const fmtSize = (n: number) => (n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(1)} KB` : `${(n / (1 << 20)).toFixed(1)} MB`);

/** "Files" screen: the office drive (folders, files, versions, trash, search). */
export function DriveView({ initialPath = "/" }: { initialPath?: string }) {
  const { t } = useT();
  const [path, setPath] = useState(initialPath);
  const [listing, setListing] = useState<Listing | null>(null);
  const [selected, setSelected] = useState<DriveFile | null>(null);
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<Hit[] | null>(null);
  // search by meaning (passages with page and snippet); off = the classic search by name and text
  const [meaning, setMeaning] = useState(true);
  const [passages, setPassages] = useState<PassageResult | null>(null);
  const [focus, setFocus] = useState<{ start: number; page?: number } | null>(null);
  const [trash, setTrash] = useState<DriveFile[] | null>(null);
  const [whoSees, setWhoSees] = useState(false); // "Qué agentes la ven" for the current folder
  const [error, setError] = useState("");
  // folders of private offices (they show a lock); an older backend without offices just shows none
  const [lockedFolders, setLockedFolders] = useState<Set<string>>(new Set());
  useEffect(() => { officesApi.list().then((r) => setLockedFolders(privateOfficeFolders(r.offices))).catch(() => { /* no offices */ }); }, []);
  const [busy, setBusy] = useState(false);
  // share of the current upload already sent (0..1); null when nothing is uploading
  const [progress, setProgress] = useState<number | null>(null);
  const maxBytes = useUploadLimit();
  const [drag, setDrag] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  // refresh when an agent touches a file under the folder we are looking at
  const touched = useStore((s) => Object.values(s.files).filter((f) => parentOf(f.path) === path).map((f) => f.ts).join(","));

  const load = useCallback(async () => {
    try {
      setError("");
      setListing(await drive.list(path));
    } catch (e) {
      setError(e instanceof DriveError ? t("drive.error", { code: e.code }) : t("drive.unavailable"));
    }
  }, [path, t]);
  useEffect(() => { load(); }, [load, touched]);

  /** Streams the files to the server in multipart batches, with one progress bar for the whole upload. */
  const upload = async (files: FileList | File[]) => {
    const all = Array.from(files);
    const tooBig = all.filter((f) => f.size > maxBytes);
    const list = all.filter((f) => f.size <= maxBytes);
    setBusy(true); setError("");
    const total = list.reduce((n, f) => n + f.size, 0) || 1;
    let done = 0;
    const failed: UploadResult[] = [];
    try {
      setProgress(0);
      // batches of at most 20 files / ~200 MB: a failed request only repeats its own batch
      for (let i = 0; i < list.length;) {
        const batch: File[] = [];
        let bytes = 0;
        while (i < list.length && batch.length < 20 && (batch.length === 0 || bytes + list[i].size <= 200 << 20)) { bytes += list[i].size; batch.push(list[i++]); }
        const res = await drive.uploadMany(path, batch.map((f) => ({ name: f.name, file: f })), (loaded) => setProgress(Math.min(1, (done + loaded) / total)));
        done += bytes;
        failed.push(...res.filter((r) => r.status >= 300));
      }
      await load();
      if (list.length > failed.length) toast(t("toast.uploaded"));
    } catch (e) {
      setError(e instanceof DriveError ? (e.code === "plan_limit" ? t("drive.planLimit") : t("drive.error", { code: e.code })) : String(e));
    } finally {
      setBusy(false); setProgress(null);
    }
    const first = failed[0];
    if (tooBig.length) setError(`${tooBig.map((f) => f.name).join(", ")}: ${t("att.err.file_too_large", { mb: Math.round(maxBytes / (1 << 20)) })}`);
    else if (first) setError(`${failed.map((f) => f.name).join(", ")}: ${first.code === "plan_limit" ? t("drive.planLimit") : t("drive.error", { code: first.code ?? String(first.status) })}`);
  };
  const search = async () => {
    if (!query.trim()) { setHits(null); setPassages(null); return; }
    if (meaning) {
      const r = await docSearch.passages(query.trim()).catch(() => null);
      if (r?.enabled) { setHits(null); setPassages(r); return; } // index off or unreachable: classic search below
    }
    setPassages(null);
    setHits(await drive.search(query.trim()).catch(() => []));
  };
  const openPassage = async (p: Passage) => {
    setPassages(null);
    setPath(parentOf(p.path));
    const f = await drive.file(p.file_id).catch(() => null);
    if (f) { setSelected(f); setFocus({ start: p.start, page: p.page }); }
  };
  const crumbs = path === "/" ? [] : path.split("/").filter(Boolean);
  const canUpload = path !== "/";

  return (
    <div data-testid="drive-view" className="flex h-full min-h-0 bg-bg"
      onDragOver={(e) => { if (canUpload) { e.preventDefault(); setDrag(true); } }} onDragLeave={() => setDrag(false)}
      onDrop={(e) => { e.preventDefault(); setDrag(false); if (canUpload && e.dataTransfer.files.length) upload(e.dataTransfer.files); }}>
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex flex-wrap items-center gap-2 border-b border-line bg-panel px-3 py-3 sm:px-5">
          <div data-tour="files.header" className="w-full sm:mr-3 sm:w-auto"><div className="flex items-center gap-1.5"><h1 className="font-display text-[18px] sm:text-xl font-semibold text-ink">{t("drive.title")}</h1><ConceptHelp concept="file" /></div><p className="text-[12px] text-mute">{t("drive.page.desc")}</p></div>
          <nav data-tour="files.crumbs" data-testid="drive-crumbs" className="flex flex-wrap items-center gap-1 text-sm">
            <button type="button" className="min-h-9 rounded px-1.5 py-0.5 text-mute hover:bg-panel2 hover:text-ink sm:min-h-0" onClick={() => { setPath("/"); setSelected(null); setTrash(null); }}>{t("drive.root")}</button>
            {crumbs.map((c, i) => (
              <span key={i} className="flex items-center gap-1">
                <span className="text-mute">/</span>
                <button type="button" className="min-h-9 rounded px-1.5 py-0.5 text-ink hover:bg-panel2 sm:min-h-0" onClick={() => { setPath("/" + crumbs.slice(0, i + 1).join("/")); setSelected(null); setTrash(null); }}>
                  {rootLabel(c, t)}
                </button>
              </span>
            ))}
          </nav>
          <IndexStatusChip path={path} refresh={touched} />
          <div className="flex w-full flex-wrap items-center gap-2 sm:ml-auto sm:w-auto">
            <button type="button" data-testid="drive-meaning" aria-pressed={meaning} title={t("drive.meaning.help")} onClick={() => setMeaning(!meaning)}
              className={`min-h-10 rounded-lg border px-2 py-1.5 text-[11px] sm:min-h-0 ${meaning ? "border-accent bg-accent/10 text-accent" : "border-line text-mute hover:text-ink"}`}>{t("drive.meaning.toggle")}</button>
            <input data-tour="files.search" data-testid="drive-search" value={query} onChange={(e) => setQuery(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") search(); }}
              placeholder={meaning ? t("drive.meaning.placeholder") : t("drive.searchPlaceholder")} className="order-first min-h-10 w-full min-w-0 basis-full rounded-lg border border-line bg-panel2 px-3 py-1.5 text-base outline-none focus:border-accent sm:order-none sm:min-h-0 sm:w-56 sm:basis-auto sm:text-xs" />
            {path !== "/" && <Btn data-testid="drive-who-sees" className="inline-flex items-center gap-1" onClick={() => { setWhoSees(!whoSees); setSelected(null); }}><Icon name="users" size={13} />{t("access.folder.button")}</Btn>}
            <span data-tour="files.trash" className="inline-flex"><Btn data-testid="drive-trash" onClick={async () => setTrash(trash ? null : (await drive.trash().catch(() => ({ files: [] }))).files)}>{t("drive.trash")}</Btn></span>
            <input ref={fileInput} type="file" multiple className="hidden" onChange={(e) => { if (e.target.files?.length) upload(e.target.files); e.target.value = ""; }} />
            <span data-tour="files.upload" className="inline-flex"><Btn kind="primary" data-testid="drive-upload" className="inline-flex items-center gap-1" disabled={!canUpload || busy} onClick={() => fileInput.current?.click()}>
              <Icon name="plus" size={13} />{busy ? `${t("drive.uploading")}${progress ? ` ${Math.floor(progress * 100)} %` : ""}` : t("drive.upload")}
            </Btn></span>
          </div>
        </div>
        {progress !== null && (
          <div role="progressbar" aria-label={t("drive.uploading")} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.floor(progress * 100)} data-testid="drive-upload-progress"
            className="h-[3px] w-full overflow-hidden bg-accent/15">
            <div className="h-full bg-accent transition-[width] duration-200" style={{ width: `${Math.floor(progress * 100)}%` }} />
          </div>
        )}
        {error && <div data-testid="drive-error" className="mx-3 mt-3 sm:mx-5 rounded-lg bg-red-500/10 px-3 py-2 text-xs text-red-700">{error}</div>}
        {drag && <div className="mx-3 mt-3 sm:mx-5 rounded-xl border-2 border-dashed border-accent px-4 py-6 text-center text-sm text-accent">{t("drive.dropHere", { path })}</div>}
        <div data-tour="files.list" className="min-h-0 flex-1 overflow-y-auto p-3 sm:p-5">
          {passages ? (
            <PassageResults result={passages} onOpen={openPassage} onClose={() => setPassages(null)} />
          ) : hits ? (
            <SearchResults hits={hits} onOpen={async (h) => { setHits(null); setPath(parentOf(h.path)); }} onClose={() => setHits(null)} />
          ) : trash ? (
            <TrashList files={trash} onRestore={async (f) => { await drive.restore(f.id); setTrash((await drive.trash()).files); load(); }} />
          ) : !listing ? (
            <Loading rows={5} />
          ) : (
            <FileTable listing={listing} path={path} selected={selected?.id ?? null}
              onFolder={(p) => { setPath(p); setSelected(null); }} onFile={(f) => { setSelected(f); setFocus(null); }} canUpload={canUpload} onUpload={() => fileInput.current?.click()} lockedFolders={lockedFolders} />
          )}
        </div>
      </div>
      {whoSees && !selected && path !== "/" && <FolderAgentsPanel path={path} label={"/" + crumbs.map((c) => rootLabel(c, t)).join("/")} onClose={() => setWhoSees(false)} />}
      {selected && <FilePane file={selected} focus={focus} onClearFocus={() => setFocus(null)} onClose={() => { setSelected(null); setFocus(null); }} onChanged={() => { load(); }} onDeleted={() => { setSelected(null); load(); }} />}
    </div>
  );
}

/** `copy`: the second rendering (phone line under the name) carries no test id, so ids stay unique. */
export function Author({ p, copy = false }: { p: DriveFile["updated_by"]; copy?: boolean }) {
  const { t } = useT();
  const name = useAgentName();
  if (p.kind === "agent") return <span className="inline-flex items-center gap-1"><span className="rounded bg-accent/10 px-1 text-[10px] font-semibold uppercase text-accent">{t("drive.agent")}</span>{name(p.id)}</span>;
  if (p.kind === "system") return <span className="text-mute">{t("drive.system")}</span>;
  if (p.kind === "inbox") return <span data-testid={copy ? undefined : "drive-author-inbox"} title={t("dataInbox.authorHint")} className="inline-flex items-center gap-1"><span className="rounded bg-amber-500/15 px-1 text-[10px] font-semibold uppercase text-amber-700">{t("dataInbox.author")}</span>{p.id}</span>;
  return <span>{t("drive.person")}</span>;
}

function FileTable({ listing, path, selected, onFolder, onFile, canUpload, onUpload, lockedFolders }: {
  listing: Listing; path: string; selected: string | null; onFolder: (p: string) => void; onFile: (f: DriveFile) => void; canUpload: boolean; onUpload: () => void; lockedFolders: Set<string>;
}) {
  const { t } = useT();
  if (!listing.folders.length && !listing.files.length) return canUpload
    ? <EmptyState testid="drive-empty" concept="file" title={t("drive.emptyTitle")} body={t("drive.emptyFolder")} action={<Btn kind="primary" onClick={onUpload}>{t("drive.upload")}</Btn>} />
    : <Empty>{t("drive.empty")}</Empty>;
  return (
    <table data-testid="drive-table" className="w-full text-left text-sm">
      <thead className="text-[11px] uppercase tracking-wide text-mute">
        <tr><th className="py-2 font-semibold">{t("drive.col.name")}</th><th className="hidden font-semibold sm:table-cell">{t("drive.col.by")}</th><th className="hidden font-semibold sm:table-cell">{t("drive.col.version")}</th><th className="hidden font-semibold sm:table-cell">{t("drive.col.size")}</th><th className="hidden font-semibold sm:table-cell">{t("drive.col.updated")}</th></tr>
      </thead>
      <tbody>
        {listing.folders.map((f) => (
          <tr key={f.id} data-testid={`drive-folder-${f.name}`} className="cursor-pointer border-t border-line hover:bg-panel2" onClick={() => onFolder(f.path)}>
            <td className="py-2"><span className="flex items-center gap-2 font-medium text-ink"><Icon name="folder" size={15} />{path === "/" ? rootLabel(f.name, t) : f.name}{lockedFolders.has(f.path) && <span data-testid={`drive-lock-${f.name}`} title={t("drive.officePrivate")} className="text-mute"><Icon name="lock" size={13} /></span>}</span></td>
            <td className="hidden sm:table-cell" /><td className="hidden sm:table-cell" /><td className="hidden sm:table-cell" /><td className="hidden sm:table-cell" />
          </tr>
        ))}
        {listing.files.map((f) => (
          <tr key={f.id} data-testid={`drive-file-${f.name}`} data-selected={selected === f.id}
            className={`cursor-pointer border-t border-line hover:bg-panel2 ${selected === f.id ? "bg-accent/5" : ""}`} onClick={() => onFile(f)}>
            <td className="py-2">
              <span className="flex min-w-0 items-center gap-2 text-ink"><Icon name="doc" size={15} className="shrink-0" /><span className="min-w-0 break-words [overflow-wrap:anywhere]">{f.name}</span></span>
              {/* phones: the other columns as one line under the name */}
              <span className="mt-0.5 flex flex-wrap gap-x-2 pl-[23px] text-[11.5px] text-mute sm:hidden"><Author p={f.updated_by} copy /><span>v{f.current_version}</span><span>{fmtSize(f.size)}</span><span>{new Date(f.updated_at).toLocaleDateString()}</span></span>
            </td>
            <td className="hidden text-xs sm:table-cell"><Author p={f.updated_by} /></td>
            <td className="hidden text-xs text-mute sm:table-cell">v{f.current_version}</td>
            <td className="hidden text-xs text-mute sm:table-cell">{fmtSize(f.size)}</td>
            <td className="hidden text-xs text-mute sm:table-cell">{new Date(f.updated_at).toLocaleString()}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function SearchResults({ hits, onOpen, onClose }: { hits: Hit[]; onOpen: (h: Hit) => void; onClose: () => void }) {
  const { t } = useT();
  return (
    <div data-testid="drive-search-results" className="space-y-2">
      <div className="flex items-center justify-between"><h2 className="text-sm font-semibold text-ink">{t("drive.results", { n: hits.length })}</h2><Btn onClick={onClose}>{t("drive.close")}</Btn></div>
      {!hits.length && <Empty>{t("drive.noResults")}</Empty>}
      {hits.map((h) => (
        <button type="button" key={h.file_id} onClick={() => onOpen(h)} className="block w-full rounded-lg border border-line bg-panel px-3 py-2 text-left hover:border-accent">
          <div className="text-sm font-medium text-ink">{h.name}</div>
          <div className="text-[11px] text-mute">{h.path}</div>
          {h.snippet && <div className="mt-1 line-clamp-2 text-xs text-ink/80">{h.snippet.replace(/[«»]/g, "")}</div>}
        </button>
      ))}
    </div>
  );
}

function TrashList({ files, onRestore }: { files: DriveFile[]; onRestore: (f: DriveFile) => void }) {
  const { t } = useT();
  return (
    <div data-testid="drive-trash-list" className="space-y-2">
      <h2 className="text-sm font-semibold text-ink">{t("drive.trashTitle")}</h2>
      {!files.length && <Empty>{t("drive.trashEmpty")}</Empty>}
      {files.map((f) => (
        <div key={f.id} className="flex items-center justify-between rounded-lg border border-line bg-panel px-3 py-2 text-sm">
          <span>{f.path}</span><Btn onClick={() => onRestore(f)}>{t("drive.restoreFile")}</Btn>
        </div>
      ))}
    </div>
  );
}

/** CSV preview as a small table (first rows); everything else as text. Never rendered as HTML. */
function Preview({ file, text }: { file: DriveFile; text: string }) {
  const rows = useMemo(() => (file.name.endsWith(".csv") ? text.trim().split(/\r?\n/).slice(0, 60).map((l) => l.split(",")) : null), [file.name, text]);
  if (rows && rows.length) {
    return (
      <div className="wf-scroll-x"><table data-testid="drive-preview-table" className="w-full text-xs">
        <tbody>{rows.map((r, i) => <tr key={i} className={i ? "border-t border-line" : "font-semibold"}>{r.map((c, j) => <td key={j} className="px-1.5 py-1">{c}</td>)}</tr>)}</tbody>
      </table></div>
    );
  }
  return <pre data-testid="drive-preview-text" className="whitespace-pre-wrap break-words font-mono text-[11.5px] leading-relaxed text-ink">{text}</pre>;
}

/** `embedded`: inside a host panel that already shows the name and the close button (the department's Drive):
 * no header and no placement of its own, it fills the host. Default: the Files screen's side pane, as always. */
export function FilePane({ file, focus, onClearFocus, onClose, onChanged, onDeleted, embedded = false }: {
  file: DriveFile; focus?: { start: number; page?: number } | null; onClearFocus?: () => void; onClose: () => void; onChanged: () => void; onDeleted: () => void; embedded?: boolean;
}) {
  const { t } = useT();
  const [text, setText] = useState<string | null>(null);
  // opened from a passage: the extracted text around it (also for docx/xlsx/pdf), from just before the hit
  const [focusText, setFocusText] = useState<string | null>(null);
  useEffect(() => {
    if (!focus) { setFocusText(null); return; }
    docSearch.textAt(file.id, Math.max(0, focus.start - 200)).then(setFocusText).catch(() => setFocusText(null));
  }, [file.id, focus]);
  const [versions, setVersions] = useState<DriveVersion[]>([]);
  const [shown, setShown] = useState(0);
  const textual = isTextual(file);
  useEffect(() => {
    setShown(0);
    drive.versions(file.id).then(setVersions).catch(() => setVersions([]));
  }, [file.id, file.current_version]);
  useEffect(() => {
    if (!textual) { setText(null); return; }
    drive.text(file.id, shown).then((r) => setText(r.text)).catch(() => setText(null));
  }, [file.id, file.current_version, shown, textual]);
  return (
    <aside data-tour={embedded ? undefined : "files.pane"} data-testid="drive-file-pane" className={embedded ? "flex min-h-0 flex-1 flex-col bg-panel" : "flex flex-col border-l border-line bg-panel max-md:fixed max-md:inset-0 max-md:z-40 max-md:h-dvh max-md:w-full max-md:border-l-0 md:w-[420px] md:min-w-[320px]"}>
      {!embedded && <div className="flex items-start gap-2 border-b border-line p-4">
        <div className="min-w-0 flex-1">
          <div className="truncate font-medium text-ink">{file.name}</div>
          <div className="truncate text-[11px] text-mute">{file.path}</div>
          <div className="mt-1 text-[11px] text-mute"><Author p={file.updated_by} /> · v{file.current_version} · {fmtSize(file.size)}</div>
        </div>
        <button type="button" aria-label={t("drive.close")} onClick={onClose} className="rounded p-1 text-mute hover:bg-panel2"><Icon name="x" size={14} /></button>
      </div>}
      <div className="flex gap-2 border-b border-line px-4 py-2">
        <Btn data-testid="drive-download" onClick={() => drive.download(file)}>{t("drive.download")}</Btn>
        <Btn kind="danger" data-testid="drive-delete" onClick={async () => { try { await drive.remove(file.id); onDeleted(); } catch (e) { alertCode(e); } }}>{t("drive.delete")}</Btn>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        {shown > 0 && <div className="mb-2 rounded bg-amber-500/10 px-2 py-1 text-[11px] text-amber-800">{t("drive.viewingVersion", { v: shown })}</div>}
        {focus && focusText !== null ? (
          <div data-testid="drive-focus">
            <div className="mb-2 flex items-center justify-between gap-2 rounded bg-accent/10 px-2 py-1 text-[11px] text-accent">
              <span>{focus.page ? t("drive.meaning.fromPage", { n: focus.page }) : t("drive.meaning.fromPassage")}</span>
              <button type="button" className="underline" onClick={onClearFocus}>{t("drive.meaning.wholeFile")}</button>
            </div>
            <pre className="whitespace-pre-wrap break-words font-mono text-[11.5px] leading-relaxed text-ink">{focusText}</pre>
          </div>
        ) : textual ? (text === null ? <Empty>{t("drive.loading")}</Empty> : <Preview file={file} text={text} />) : <Empty>{t("drive.noPreview")}</Empty>}
      </div>
      <div className="max-h-[38%] overflow-y-auto border-t border-line p-4">
        <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("drive.history")}</h3>
        <ul data-tour="files.versions" data-testid="drive-versions" className="space-y-1.5">
          {versions.map((v) => (
            <li key={v.version} className="flex items-center gap-2 text-xs">
              <button type="button" className={`rounded px-1.5 py-0.5 font-mono ${shown === v.version || (!shown && v.version === file.current_version) ? "bg-accent/10 text-accent" : "text-mute hover:bg-panel2"}`}
                onClick={() => setShown(v.version === file.current_version ? 0 : v.version)}>v{v.version}</button>
              <span className="min-w-0 flex-1 truncate"><Author p={v.author} />{v.message ? ` · ${v.message}` : ""}</span>
              <span className="text-mute">{new Date(v.created_at).toLocaleString()}</span>
              {v.version !== file.current_version && (
                <button type="button" data-testid={`drive-restore-v${v.version}`} className="text-accent hover:underline"
                  onClick={async () => { await drive.restoreVersion(file.id, v.version); onChanged(); }}>{t("drive.restore")}</button>
              )}
            </li>
          ))}
        </ul>
        <p className="mt-2 text-[10.5px] text-mute">{t("drive.historyNote", { n: fmtNumber(versions.length) })}</p>
      </div>
    </aside>
  );
}

function alertCode(e: unknown) {
  // surfaced inline by the caller's next load; kept minimal here (no browser dialogs in the app)
  console.warn("drive:", e instanceof DriveError ? e.code : e);
}
