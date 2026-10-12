"use client";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useT, fmtNumber } from "@/lib/i18n";
import { drive, DriveError, isTextual, parentOf, type DriveFile, type DriveVersion, type Hit, type Listing } from "@/lib/drive";
import { useStore } from "@/lib/store";
import { toast } from "@/lib/toast";
import { Btn, Empty, Loading } from "../ui";
import { EmptyState } from "../nav/ui2";
import { Icon } from "../icons";
import { useAgentName } from "../ui";

const ROOTS = new Set(["Agents", "Projects", "Clients", "Shared"]);
/** The four well-known roots are translated; every other folder keeps its own name. */
const rootLabel = (t: (k: string) => string, name: string) => (ROOTS.has(name) ? t(`drive.rootName.${name}`) : name);

const fmtSize = (n: number) => (n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(1)} KB` : `${(n / (1 << 20)).toFixed(1)} MB`);

/** "Files" screen: the office drive (folders, files, versions, trash, search). */
export function DriveView({ initialPath = "/" }: { initialPath?: string }) {
  const { t } = useT();
  const [path, setPath] = useState(initialPath);
  const [listing, setListing] = useState<Listing | null>(null);
  const [selected, setSelected] = useState<DriveFile | null>(null);
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<Hit[] | null>(null);
  const [trash, setTrash] = useState<DriveFile[] | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
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

  const upload = async (files: FileList | File[]) => {
    setBusy(true);
    try {
      for (const f of Array.from(files)) await drive.upload(`${path === "/" ? "" : path}/${f.name}`, f);
      await load();
      toast(t("toast.uploaded"));
    } catch (e) {
      setError(e instanceof DriveError ? t("drive.error", { code: e.code }) : String(e));
    } finally {
      setBusy(false);
    }
  };
  const search = async () => {
    if (!query.trim()) { setHits(null); return; }
    setHits(await drive.search(query.trim()).catch(() => []));
  };
  const crumbs = path === "/" ? [] : path.split("/").filter(Boolean);
  const canUpload = path !== "/";

  return (
    <div data-testid="drive-view" className="flex h-full min-h-0 bg-bg"
      onDragOver={(e) => { if (canUpload) { e.preventDefault(); setDrag(true); } }} onDragLeave={() => setDrag(false)}
      onDrop={(e) => { e.preventDefault(); setDrag(false); if (canUpload && e.dataTransfer.files.length) upload(e.dataTransfer.files); }}>
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex flex-wrap items-center gap-2 border-b border-line bg-panel px-5 py-3">
          <div className="mr-3"><h1 className="font-display text-xl font-semibold text-ink">{t("drive.title")}</h1><p className="text-[12px] text-mute">{t("drive.page.desc")}</p></div>
          <nav data-testid="drive-crumbs" className="flex flex-wrap items-center gap-1 text-sm">
            <button type="button" className="rounded px-1.5 py-0.5 text-mute hover:bg-panel2 hover:text-ink" onClick={() => { setPath("/"); setSelected(null); setTrash(null); }}>{t("drive.root")}</button>
            {crumbs.map((c, i) => (
              <span key={i} className="flex items-center gap-1">
                <span className="text-mute">/</span>
                <button type="button" className="rounded px-1.5 py-0.5 text-ink hover:bg-panel2" onClick={() => { setPath("/" + crumbs.slice(0, i + 1).join("/")); setSelected(null); setTrash(null); }}>
                  {rootLabel(t, c)}
                </button>
              </span>
            ))}
          </nav>
          <div className="ml-auto flex items-center gap-2">
            <input data-testid="drive-search" value={query} onChange={(e) => setQuery(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") search(); }}
              placeholder={t("drive.searchPlaceholder")} className="w-56 rounded-lg border border-line bg-panel2 px-3 py-1.5 text-xs outline-none focus:border-accent" />
            <Btn data-testid="drive-trash" onClick={async () => setTrash(trash ? null : (await drive.trash().catch(() => ({ files: [] }))).files)}>{t("drive.trash")}</Btn>
            <input ref={fileInput} type="file" multiple className="hidden" onChange={(e) => { if (e.target.files?.length) upload(e.target.files); e.target.value = ""; }} />
            <Btn kind="primary" data-testid="drive-upload" className="inline-flex items-center gap-1" disabled={!canUpload || busy} onClick={() => fileInput.current?.click()}>
              <Icon name="plus" size={13} />{busy ? t("drive.uploading") : t("drive.upload")}
            </Btn>
          </div>
        </div>
        {error && <div data-testid="drive-error" className="mx-5 mt-3 rounded-lg bg-red-500/10 px-3 py-2 text-xs text-red-700">{error}</div>}
        {drag && <div className="mx-5 mt-3 rounded-xl border-2 border-dashed border-accent px-4 py-6 text-center text-sm text-accent">{t("drive.dropHere", { path })}</div>}
        <div className="min-h-0 flex-1 overflow-y-auto p-5">
          {hits ? (
            <SearchResults hits={hits} onOpen={async (h) => { setHits(null); setPath(parentOf(h.path)); }} onClose={() => setHits(null)} />
          ) : trash ? (
            <TrashList files={trash} onRestore={async (f) => { await drive.restore(f.id); setTrash((await drive.trash()).files); load(); }} />
          ) : !listing ? (
            <Loading rows={5} />
          ) : (
            <FileTable listing={listing} path={path} selected={selected?.id ?? null}
              onFolder={(p) => { setPath(p); setSelected(null); }} onFile={setSelected} canUpload={canUpload} onUpload={() => fileInput.current?.click()} />
          )}
        </div>
      </div>
      {selected && <FilePane file={selected} onClose={() => setSelected(null)} onChanged={() => { load(); }} onDeleted={() => { setSelected(null); load(); }} />}
    </div>
  );
}

function Author({ p }: { p: DriveFile["updated_by"] }) {
  const { t } = useT();
  const name = useAgentName();
  if (p.kind === "agent") return <span className="inline-flex items-center gap-1"><span className="rounded bg-accent/10 px-1 text-[10px] font-semibold uppercase text-accent">{t("drive.agent")}</span>{name(p.id)}</span>;
  if (p.kind === "system") return <span className="text-mute">{t("drive.system")}</span>;
  return <span>{t("drive.person")}</span>;
}

function FileTable({ listing, path, selected, onFolder, onFile, canUpload, onUpload }: {
  listing: Listing; path: string; selected: string | null; onFolder: (p: string) => void; onFile: (f: DriveFile) => void; canUpload: boolean; onUpload: () => void;
}) {
  const { t } = useT();
  if (!listing.folders.length && !listing.files.length) return canUpload
    ? <EmptyState testid="drive-empty" icon="doc" title={t("drive.emptyTitle")} body={t("drive.emptyFolder")} action={<Btn kind="primary" onClick={onUpload}>{t("drive.upload")}</Btn>} />
    : <Empty>{t("drive.empty")}</Empty>;
  return (
    <table data-testid="drive-table" className="w-full text-left text-sm">
      <thead className="text-[11px] uppercase tracking-wide text-mute">
        <tr><th className="py-2 font-semibold">{t("drive.col.name")}</th><th className="font-semibold">{t("drive.col.by")}</th><th className="font-semibold">{t("drive.col.version")}</th><th className="font-semibold">{t("drive.col.size")}</th><th className="font-semibold">{t("drive.col.updated")}</th></tr>
      </thead>
      <tbody>
        {listing.folders.map((f) => (
          <tr key={f.id} data-testid={`drive-folder-${f.name}`} className="cursor-pointer border-t border-line hover:bg-panel2" onClick={() => onFolder(f.path)}>
            <td className="py-2"><span className="flex items-center gap-2 font-medium text-ink"><Icon name="folder" size={15} />{path === "/" ? rootLabel(t, f.name) : f.name}</span></td>
            <td /><td /><td /><td />
          </tr>
        ))}
        {listing.files.map((f) => (
          <tr key={f.id} data-testid={`drive-file-${f.name}`} data-selected={selected === f.id}
            className={`cursor-pointer border-t border-line hover:bg-panel2 ${selected === f.id ? "bg-accent/5" : ""}`} onClick={() => onFile(f)}>
            <td className="py-2"><span className="flex items-center gap-2 text-ink"><Icon name="doc" size={15} />{f.name}</span></td>
            <td className="text-xs"><Author p={f.updated_by} /></td>
            <td className="text-xs text-mute">v{f.current_version}</td>
            <td className="text-xs text-mute">{fmtSize(f.size)}</td>
            <td className="text-xs text-mute">{new Date(f.updated_at).toLocaleString()}</td>
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
      <table data-testid="drive-preview-table" className="w-full text-xs">
        <tbody>{rows.map((r, i) => <tr key={i} className={i ? "border-t border-line" : "font-semibold"}>{r.map((c, j) => <td key={j} className="px-1.5 py-1">{c}</td>)}</tr>)}</tbody>
      </table>
    );
  }
  return <pre data-testid="drive-preview-text" className="whitespace-pre-wrap break-words font-mono text-[11.5px] leading-relaxed text-ink">{text}</pre>;
}

function FilePane({ file, onClose, onChanged, onDeleted }: { file: DriveFile; onClose: () => void; onChanged: () => void; onDeleted: () => void }) {
  const { t } = useT();
  const [text, setText] = useState<string | null>(null);
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
    <aside data-testid="drive-file-pane" className="flex w-[420px] min-w-[320px] flex-col border-l border-line bg-panel">
      <div className="flex items-start gap-2 border-b border-line p-4">
        <div className="min-w-0 flex-1">
          <div className="truncate font-medium text-ink">{file.name}</div>
          <div className="truncate text-[11px] text-mute">{file.path}</div>
          <div className="mt-1 text-[11px] text-mute"><Author p={file.updated_by} /> · v{file.current_version} · {fmtSize(file.size)}</div>
        </div>
        <button type="button" aria-label={t("drive.close")} onClick={onClose} className="rounded p-1 text-mute hover:bg-panel2"><Icon name="x" size={14} /></button>
      </div>
      <div className="flex gap-2 border-b border-line px-4 py-2">
        <Btn data-testid="drive-download" onClick={() => drive.download(file)}>{t("drive.download")}</Btn>
        <Btn kind="danger" data-testid="drive-delete" onClick={async () => { try { await drive.remove(file.id); onDeleted(); } catch (e) { alertCode(e); } }}>{t("drive.delete")}</Btn>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        {shown > 0 && <div className="mb-2 rounded bg-amber-500/10 px-2 py-1 text-[11px] text-amber-800">{t("drive.viewingVersion", { v: shown })}</div>}
        {textual ? (text === null ? <Empty>{t("drive.loading")}</Empty> : <Preview file={file} text={text} />) : <Empty>{t("drive.noPreview")}</Empty>}
      </div>
      <div className="max-h-[38%] overflow-y-auto border-t border-line p-4">
        <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("drive.history")}</h3>
        <ul data-testid="drive-versions" className="space-y-1.5">
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
