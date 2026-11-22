"use client";
import { useEffect, useMemo, useState } from "react";
import { useT, fmtNumber } from "@/lib/i18n";
import { drive, DriveError, isTextual, type DriveFile, type DriveVersion } from "@/lib/drive";
import { Btn, Empty } from "../ui";
import { Icon } from "../icons";
import { useAgentName } from "../ui";
import { docSearch } from "@/lib/docsearch";


const fmtSize = (n: number) => (n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(1)} KB` : `${(n / (1 << 20)).toFixed(1)} MB`);

/** `copy`: the second rendering (phone line under the name) carries no test id, so ids stay unique. */
export function Author({ p, copy = false }: { p: DriveFile["updated_by"]; copy?: boolean }) {
  const { t } = useT();
  const name = useAgentName();
  if (p.kind === "agent") return <span className="inline-flex items-center gap-1"><span className="rounded bg-accent/10 px-1 text-[10px] font-semibold uppercase text-accent">{t("drive.agent")}</span>{name(p.id)}</span>;
  if (p.kind === "system") return <span className="text-mute">{t("drive.system")}</span>;
  if (p.kind === "inbox") return <span data-testid={copy ? undefined : "drive-author-inbox"} title={t("dataInbox.authorHint")} className="inline-flex items-center gap-1"><span className="rounded bg-amber-500/15 px-1 text-[10px] font-semibold uppercase text-amber-700">{t("dataInbox.author")}</span>{p.id}</span>;
  return <span>{t("drive.person")}</span>;
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

/** `embedded`: inside a host panel that already shows the name and the close button (the Files screen):
 * no header, no "Mover a la papelera" and no placement of its own, it fills the host. Default: the Files screen's
 * side pane, as always. */
export function FilePane({ file, focus, onClearFocus, onClose, onChanged, onDeleted, embedded = false, readOnly = false }: {
  file: DriveFile; focus?: { start: number; page?: number } | null; onClearFocus?: () => void; onClose: () => void; onChanged: () => void; onDeleted: () => void; embedded?: boolean;
  /** No way to change the file (shared with me): the versions cannot be restored. */
  readOnly?: boolean;
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
    <aside data-tour="files.pane" data-testid="drive-file-pane" className={embedded ? "flex min-h-0 flex-1 flex-col bg-panel" : "flex flex-col border-l border-line bg-panel max-md:fixed max-md:inset-0 max-md:z-40 max-md:h-dvh max-md:w-full max-md:border-l-0 md:w-[420px] md:min-w-[320px]"}>
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
        {!embedded && <Btn kind="danger" data-testid="drive-delete" onClick={async () => { try { await drive.remove(file.id); onDeleted(); } catch (e) { alertCode(e); } }}>{t("drive.delete")}</Btn>}
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
              {!readOnly && v.version !== file.current_version && (
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
