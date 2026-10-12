"use client";
import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { useT } from "@/lib/i18n";
import { drive, DriveError, type DiffResult, type DriveFile, type DriveVersion } from "@/lib/drive";
import { EDITABLE, TEXTUAL, fileKind, fmtBytes, useFileViewer } from "@/lib/fileView";
import { useStore } from "@/lib/store";
import { useDriveNav } from "@/lib/drive";
import { Icon } from "../icons";
import { useAgentName } from "../ui";
import { sendChat } from "../chat/ChatThread";
import { FileBadge } from "./FileCard";
import { Markdown } from "./markdown";
import { parseCsv } from "@/lib/csv";
import { CodeView, DiffView, ImageView, JsonView, PdfBytesView, TableView } from "./viewers";

type Load = { state: "loading" } | { state: "error"; code: string } | { state: "ok"; file: DriveFile; versions: DriveVersion[] };

/** Right side panel, like Claude artifacts / ChatGPT canvas: the file chosen in the chat, by type, with versions, edit and "ask for changes". */
export function FileViewerPanel() {
  const ref = useFileViewer((s) => s.ref);
  const close = useFileViewer((s) => s.close);
  const { t } = useT();
  const name = useAgentName();
  const [load, setLoad] = useState<Load>({ state: "loading" });
  const [version, setVersion] = useState(0); // 0 = latest
  const [showDiff, setShowDiff] = useState(false);
  const [editing, setEditing] = useState(false);
  // the agent that touched this path last (file.activity) refreshes the panel
  const touched = useStore((s) => (ref ? Object.values(s.files).find((f) => f.path === ref.path)?.ts : 0));

  const reload = useCallback(async () => {
    if (!ref) return;
    try {
      const file = ref.file_id ? await drive.file(ref.file_id) : await drive.byPath(ref.path);
      if (!file) { setLoad({ state: "error", code: "not_found" }); return; }
      const versions = await drive.versions(file.id).catch(() => [] as DriveVersion[]);
      setLoad({ state: "ok", file, versions });
    } catch (e) {
      setLoad({ state: "error", code: e instanceof DriveError ? e.code : "unavailable" });
    }
  }, [ref]);
  useEffect(() => { setLoad({ state: "loading" }); setVersion(0); setShowDiff(false); setEditing(false); reload(); }, [ref?.path, ref?.file_id, reload]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { if (touched) reload(); }, [touched, reload]);
  useEffect(() => {
    if (!ref) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape" && !editing) close(); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [ref, close, editing]);

  if (!ref) return null;
  const file = load.state === "ok" ? load.file : null;
  const kind = fileKind(file?.name ?? ref.name, file?.mime ?? ref.mime);
  const current = file?.current_version ?? 0;
  const shown = version || current;
  const author = file?.updated_by;

  return (
    <aside data-testid="file-viewer" data-kind={kind} data-version={shown} role="complementary" aria-label={t("fileviewer.panelLabel", { name: ref.name })}
      className="pointer-events-auto flex h-full w-[min(560px,100vw)] flex-col border-l border-line bg-panel shadow-float">
      <header className="flex items-start gap-2.5 border-b border-line p-3">
        <FileBadge kind={kind} />
        <div className="min-w-0 flex-1">
          <h2 data-testid="viewer-title" className="truncate text-[13.5px] font-semibold text-ink">{file?.name ?? ref.name}</h2>
          <div className="truncate text-[11px] text-mute">
            {t(`fileviewer.kind.${kind}`)}{file ? ` · ${fmtBytes(file.size)}` : ""}
            {author ? ` · ${author.kind === "agent" ? name(author.id) : author.kind === "human" ? t("drive.person") : t("drive.system")}` : ""}
          </div>
          <div className="truncate font-mono text-[10px] text-mute">{ref.path}</div>
        </div>
        <button type="button" data-testid="viewer-close" aria-label={t("fileviewer.close")} title={t("fileviewer.close")} onClick={close} className="rounded-md p-1.5 text-mute hover:bg-panel2 hover:text-ink">
          <Icon name="x" size={15} />
        </button>
      </header>

      {file && (
        <Toolbar
          file={file} kind={kind} version={version} shown={shown} showDiff={showDiff} editing={editing}
          onVersion={(v) => { setVersion(v === current ? 0 : v); setShowDiff(false); }}
          onDiff={() => setShowDiff((d) => !d)} onEdit={() => { setVersion(0); setShowDiff(false); setEditing(true); }}
        />
      )}

      <div className="min-h-0 flex-1 overflow-auto" data-testid="viewer-body">
        {load.state === "loading" && <div className="p-6 text-center text-xs text-mute">{t("drive.loading")}</div>}
        {load.state === "error" && <div role="alert" data-testid="viewer-error" className="p-6 text-center text-xs text-err">{load.code === "not_found" || load.code === "404" ? t("fileviewer.gone") : t("fileviewer.error", { code: load.code })}</div>}
        {file && (editing
          ? <Editor key={file.id} file={file} kind={kind} onDone={async (saved) => { setEditing(false); if (saved) await reload(); }} />
          : <Body file={file} kind={kind} version={shown} latest={current} showDiff={showDiff} />)}
      </div>

      {file && <AskBar file={file} versions={load.state === "ok" ? load.versions : []} />}
    </aside>
  );
}

function Toolbar({ file, kind, version, shown, showDiff, editing, onVersion, onDiff, onEdit }: {
  file: DriveFile; kind: ReturnType<typeof fileKind>; version: number; shown: number; showDiff: boolean; editing: boolean;
  onVersion: (v: number) => void; onDiff: () => void; onEdit: () => void;
}) {
  const { t } = useT();
  const show = useDriveNav((s) => s.show);
  const close = useFileViewer((s) => s.close);
  const textual = TEXTUAL.includes(kind);
  const nums = useMemo(() => Array.from({ length: file.current_version }, (_, i) => file.current_version - i), [file.current_version]);
  return (
    <div className="flex flex-wrap items-center gap-1.5 border-b border-line px-3 py-2 text-[11.5px]">
      <label className="flex items-center gap-1 text-mute">
        <span className="sr-only">{t("fileviewer.version")}</span>
        <select data-testid="viewer-version" value={shown} disabled={editing} onChange={(e) => onVersion(Number(e.target.value))}
          className="rounded-md border border-line bg-panel2 px-1.5 py-1 font-mono text-ink outline-none focus:border-accent">
          {nums.map((n) => <option key={n} value={n}>v{n}{n === file.current_version ? ` · ${t("fileviewer.latest")}` : ""}</option>)}
        </select>
      </label>
      {textual && shown > 1 && !editing && (
        <button type="button" data-testid="viewer-diff-toggle" aria-pressed={showDiff} onClick={onDiff}
          className={`rounded-md border px-2 py-1 font-medium ${showDiff ? "border-accent bg-accent/10 text-accent-hover" : "border-line text-ink2 hover:border-accent"}`}>
          {t("fileviewer.changes")}
        </button>
      )}
      {EDITABLE.includes(kind) && !editing && version === 0 && (
        <button type="button" data-testid="viewer-edit" onClick={onEdit} className="inline-flex items-center gap-1 rounded-md border border-line px-2 py-1 font-medium text-ink2 hover:border-accent">
          <Icon name="edit" size={12} />{t("fileviewer.edit")}
        </button>
      )}
      <span className="flex-1" />
      <button type="button" data-testid="viewer-download" onClick={() => drive.download(file).catch(() => {})} className="rounded-md border border-line px-2 py-1 font-medium text-ink2 hover:border-accent">{t("fileviewer.download")}</button>
      <button type="button" data-testid="viewer-open-files" onClick={() => { close(); show(file.path.split("/").slice(0, -1).join("/") || "/"); }}
        className="rounded-md border border-line px-2 py-1 font-medium text-ink2 hover:border-accent">{t("fileviewer.openInFiles")}</button>
      {version > 0 && version !== file.current_version && (
        <span className="w-full text-[11px] text-warn">{t("fileviewer.oldVersion", { v: version })}</span>
      )}
    </div>
  );
}

/** The content by type. Text comes from /text (what an agent would read); PDF and images from /content. */
function Body({ file, kind, version, latest, showDiff }: { file: DriveFile; kind: ReturnType<typeof fileKind>; version: number; latest: number; showDiff: boolean }) {
  const { t } = useT();
  const [text, setText] = useState<{ text: string; more: boolean } | null>(null);
  const [blob, setBlob] = useState<Blob | null>(null);
  const [diff, setDiff] = useState<DiffResult | null>(null);
  const [err, setErr] = useState("");
  const binary = kind === "pdf" || kind === "image";
  useEffect(() => {
    let dead = false;
    setText(null); setBlob(null); setErr("");
    const v = version === latest ? 0 : version;
    if (TEXTUAL.includes(kind)) drive.text(file.id, v).then((r) => !dead && setText({ text: r.text, more: r.more })).catch(() => !dead && setErr("load"));
    else if (binary) drive.blob(file.id, v).then((b) => !dead && setBlob(b)).catch(() => !dead && setErr("load"));
    return () => { dead = true; };
  }, [file.id, file.current_version, version, latest, kind, binary]);
  useEffect(() => {
    setDiff(null);
    if (!showDiff || version < 2) return;
    let dead = false;
    drive.diff(file.id, version - 1, version).then((d) => !dead && setDiff(d)).catch(() => !dead && setErr("load"));
    return () => { dead = true; };
  }, [showDiff, file.id, version, file.current_version]);

  if (err) return <div role="alert" className="p-6 text-center text-xs text-err">{t("fileviewer.error", { code: err })}</div>;
  if (showDiff) return <div className="p-3">{diff ? <DiffView lines={diff.lines} added={diff.added} removed={diff.removed} /> : <div className="text-xs text-mute">{t("drive.loading")}</div>}</div>;
  if (kind === "other") {
    return (
      <div data-testid="viewer-nopreview" className="flex h-full flex-col items-center justify-center gap-3 p-8 text-center">
        <FileBadge kind="other" size={52} />
        <p className="max-w-[300px] text-xs leading-relaxed text-mute">{t("fileviewer.noPreview")}</p>
        <button type="button" onClick={() => drive.download(file).catch(() => {})} className="rounded-md border border-accent bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:bg-accent-hover">{t("fileviewer.download")}</button>
      </div>
    );
  }
  if (binary) {
    if (!blob) return <div className="p-6 text-center text-xs text-mute">{t("drive.loading")}</div>;
    return kind === "pdf" ? <PdfBytesView blob={blob} /> : <ImageView blob={blob} name={file.name} />;
  }
  if (!text) return <div className="p-6 text-center text-xs text-mute">{t("drive.loading")}</div>;
  return (
    <div className="p-3">
      {text.more && <div className="mb-2 rounded-md bg-warn/10 px-2 py-1 text-[11px] text-warn">{t("fileviewer.truncated")}</div>}
      {kind === "markdown" ? <Markdown text={text.text} />
        : kind === "csv" ? <TableView rows={parseCsv(text.text)} />
        : kind === "sheet" ? <TableView rows={parseCsv(text.text.replace(/"/g, "'"))} testId="viewer-sheet" />
        : kind === "json" ? <JsonView text={text.text} />
        : <CodeView text={text.text} />}
    </div>
  );
}

/** Direct editing of text files: saves a new version with expected_version; a 409 offers to reload or overwrite. */
function Editor({ file, kind, onDone }: { file: DriveFile; kind: string; onDone: (saved: boolean) => void }) {
  const { t } = useT();
  const [text, setText] = useState<string | null>(null);
  const [more, setMore] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [conflict, setConflict] = useState<number | null>(null);
  const area = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    drive.text(file.id, 0).then((r) => { setText(r.text); setMore(r.more); }).catch(() => setErr("load"));
  }, [file.id]);
  useEffect(() => { if (text !== null) area.current?.focus(); }, [text === null]); // eslint-disable-line react-hooks/exhaustive-deps

  const save = async (expected: number) => {
    if (text === null) return;
    setBusy(true); setErr(""); setConflict(null);
    try {
      await drive.save(file.id, text, expected, t("fileviewer.editMessage"));
      onDone(true);
    } catch (e) {
      if (e instanceof DriveError && e.code === "version_conflict") setConflict(e.currentVersion ?? file.current_version);
      else setErr(e instanceof DriveError ? e.code : "unavailable");
    }
    setBusy(false);
  };
  const submit = (e: FormEvent) => { e.preventDefault(); save(file.current_version); };
  if (more) return <div className="p-4 text-xs text-warn" role="alert">{t("fileviewer.tooBigToEdit")}<div className="mt-2"><button type="button" className="rounded-md border border-line px-2 py-1" onClick={() => onDone(false)}>{t("fileviewer.cancel")}</button></div></div>;
  return (
    <form onSubmit={submit} data-testid="viewer-editor" className="flex h-full min-h-[280px] flex-col gap-2 p-3">
      {conflict !== null && (
        <div role="alert" data-testid="viewer-conflict" className="rounded-lg border border-warn/40 bg-warn/10 p-2.5 text-[11.5px] text-ink">
          <p className="font-semibold">{t("fileviewer.conflictTitle")}</p>
          <p className="mt-0.5 text-ink2">{t("fileviewer.conflictBody", { v: conflict })}</p>
          <div className="mt-2 flex gap-2">
            <button type="button" data-testid="viewer-conflict-overwrite" onClick={() => save(conflict)} className="rounded-md border border-warn px-2 py-1 font-semibold text-warn hover:bg-warn/10">{t("fileviewer.overwrite")}</button>
            <button type="button" data-testid="viewer-conflict-reload" onClick={() => onDone(true)} className="rounded-md border border-line px-2 py-1 font-semibold text-ink2 hover:bg-panel2">{t("fileviewer.discardMine")}</button>
          </div>
        </div>
      )}
      <textarea
        ref={area} data-testid="viewer-textarea" aria-label={t("fileviewer.editorLabel", { name: file.name })} value={text ?? ""} disabled={text === null || busy} spellCheck={kind === "markdown" || kind === "text"}
        onChange={(e) => setText(e.target.value)} onKeyDown={(e) => { if ((e.metaKey || e.ctrlKey) && e.key === "Enter") { e.preventDefault(); save(file.current_version); } }}
        className="min-h-[260px] flex-1 resize-none rounded-lg border border-line bg-panel2 p-2.5 font-mono text-[12px] leading-[1.55] text-ink outline-none focus:border-accent"
      />
      {err && <div role="alert" className="text-[11px] text-err">{t("fileviewer.saveError", { code: err })}</div>}
      <div className="flex items-center gap-2">
        <button type="submit" data-testid="viewer-save" disabled={busy || text === null} className="rounded-md border border-accent bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:bg-accent-hover disabled:opacity-40">
          {busy ? t("fileviewer.saving") : t("fileviewer.save")}
        </button>
        <button type="button" data-testid="viewer-cancel" disabled={busy} onClick={() => onDone(false)} className="rounded-md border border-line px-3 py-1.5 text-xs font-medium text-ink2 hover:bg-panel2">{t("fileviewer.cancel")}</button>
        <span className="ml-auto text-[10.5px] text-mute">{t("fileviewer.saveHint", { v: file.current_version + 1 })}</span>
      </div>
    </form>
  );
}

/** "Ask for changes" / comment: a chat message to the agent that made the file, with the file attached (the agent reads it with fs.read). */
function AskBar({ file, versions }: { file: DriveFile; versions: DriveVersion[] }) {
  const { t } = useT();
  const name = useAgentName();
  const agents = useStore((s) => s.agents);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState<{ to: string; kind: "change" | "comment" } | null>(null);
  const [err, setErr] = useState("");
  // the agent that wrote the file; else the office decides
  const owner = [...versions].sort((a, b) => b.version - a.version).find((v) => v.author.kind === "agent" && agents[v.author.id])?.author.id ?? null;
  const conv = owner ? `agent:${owner}` : "office";
  useEffect(() => { setSent(null); setText(""); setErr(""); }, [file.id]);

  const send = async (kind: "change" | "comment") => {
    const v = text.trim();
    if (!v || busy) return;
    setBusy(true); setErr("");
    const body = t(kind === "change" ? "fileviewer.askText" : "fileviewer.commentText", { name: file.name, path: file.path, v: file.current_version, text: v });
    try {
      await sendChat(conv, body, "user", [file.path]);
      setText(""); setSent({ to: owner ?? "all", kind });
    } catch { setErr(t("chat.sendError")); }
    setBusy(false);
  };
  return (
    <div className="border-t border-line bg-panel p-3">
      <form onSubmit={(e) => { e.preventDefault(); send("change"); }} className="flex flex-col gap-1.5">
        <label htmlFor="viewer-ask" className="text-[11px] font-semibold text-ink2">
          {owner ? t("fileviewer.askLabel", { name: name(owner).split(" ")[0] }) : t("fileviewer.askLabelTeam")}
        </label>
        <div className="flex gap-1.5">
          <input id="viewer-ask" data-testid="viewer-ask-input" value={text} onChange={(e) => setText(e.target.value)} placeholder={t("fileviewer.askPlaceholder")} autoComplete="off"
            className="min-w-0 flex-1 rounded-lg border border-line bg-panel2 px-2.5 py-1.5 text-[12.5px] text-ink outline-none placeholder:text-mute focus:border-accent" />
          <button type="submit" data-testid="viewer-ask-send" disabled={busy || !text.trim()} className="whitespace-nowrap rounded-md border border-accent bg-accent px-2.5 py-1.5 text-xs font-semibold text-white hover:bg-accent-hover disabled:opacity-40">{t("fileviewer.askSend")}</button>
          <button type="button" data-testid="viewer-comment-send" disabled={busy || !text.trim()} onClick={() => send("comment")} className="whitespace-nowrap rounded-md border border-line px-2.5 py-1.5 text-xs font-medium text-ink2 hover:border-accent disabled:opacity-40">{t("fileviewer.comment")}</button>
        </div>
      </form>
      {sent && <div role="status" data-testid="viewer-ask-sent" className="mt-1.5 text-[11px] text-ok">{t(sent.kind === "change" ? "fileviewer.askSent" : "fileviewer.commentSent", { name: sent.to === "all" ? t("common.everyone") : name(sent.to).split(" ")[0] })}</div>}
      {err && <div role="alert" className="mt-1.5 text-[11px] text-err">{err}</div>}
    </div>
  );
}
