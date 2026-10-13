"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { drive, DriveError, type DiffResult } from "@/lib/drive";
import { canUndoEdit, editCounts } from "@/lib/fileEdit";
import { fileKind, useFileViewer, TEXTUAL } from "@/lib/fileView";
import type { FileRef } from "@/lib/types";
import { FileBadge } from "./FileCard";
import { DiffView } from "./viewers";

type State = "pending" | "kept" | "undone";

/**
 * An agent edited a file (fs.edit): file name, version N -> N+1, lines added and removed, a collapsible diff and two buttons.
 * Keep dismisses (the edit already is the newest version). Undo restores the previous version through the existing
 * restore-version API (a NEW version, history is kept), and only while the edit is still the latest version.
 */
export function FileEditCard({ f }: { f: FileRef }) {
  const { t } = useT();
  const open = useFileViewer((s) => s.open);
  const touch = useFileViewer((s) => s.touch);
  const [state, setState] = useState<State>("pending");
  const [expanded, setExpanded] = useState(false);
  const [diff, setDiff] = useState<DiffResult | null>(null);
  const [diffErr, setDiffErr] = useState("");
  const [current, setCurrent] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [restored, setRestored] = useState(0);
  const kind = fileKind(f.name, f.mime);
  const from = f.from_version ?? 0;
  const to = f.version ?? 0;

  // the newest version of the file: Undo is offered only while this edit is still it
  useEffect(() => {
    if (state !== "pending") return;
    let dead = false;
    (f.file_id ? drive.file(f.file_id) : drive.byPath(f.path))
      .then((file) => !dead && setCurrent(file ? file.current_version : null))
      .catch(() => !dead && setCurrent(null));
    return () => { dead = true; };
  }, [f.file_id, f.path, state]);

  useEffect(() => {
    if (!expanded || diff || diffErr || !f.file_id) return;
    if (!TEXTUAL.includes(kind) || kind === "sheet") { setDiffErr("none"); return; }
    let dead = false;
    drive.diff(f.file_id, from, to).then((d) => !dead && setDiff(d)).catch((e) => !dead && setDiffErr(e instanceof DriveError ? e.code : "unavailable"));
    return () => { dead = true; };
  }, [expanded, diff, diffErr, f.file_id, from, to, kind]);

  const latest = current !== null && canUndoEdit({ version: to, from_version: from }, current);
  const stale = current !== null && !latest;

  const undo = async () => {
    if (!f.file_id || busy) return;
    setBusy(true); setErr("");
    try {
      const file = await drive.file(f.file_id); // check again: someone may have saved since the card was drawn
      if (!canUndoEdit({ version: to, from_version: from }, file.current_version)) { setCurrent(file.current_version); setBusy(false); return; }
      await drive.restoreVersion(f.file_id, from);
      setRestored(from); setState("undone"); touch();
    } catch (e) { setErr(t("edit.undoError", { code: e instanceof DriveError ? e.code : "unavailable" })); }
    setBusy(false);
  };

  return (
    <div data-testid="file-edit-card" data-state={state} data-path={f.path}
      className="mt-1 w-full min-w-[240px] max-w-[360px] rounded-xl border border-line-strong bg-panel p-2.5 shadow-pop">
      <div className="flex items-center gap-2.5">
        <FileBadge kind={kind} />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-[12.5px] font-semibold text-ink">{t("edit.title", { name: f.name })}</span>
          <span className="block truncate text-[10.5px] text-mute">
            <span data-testid="edit-versions" className="font-mono">{t("edit.versions", { from, to })}</span>
            {" · "}
            <span data-testid="edit-counts" className="font-mono"><span className="text-ok">+{f.added ?? 0}</span> <span className="text-err">-{f.removed ?? 0}</span></span>
          </span>
        </span>
        <button type="button" data-testid="edit-open" onClick={() => open({ ...f, action: "attach", version: undefined })} className="shrink-0 rounded-md border border-line px-2 py-1 text-[11px] font-semibold text-accent-hover hover:border-accent">{t("edit.open")}</button>
      </div>

      <button type="button" data-testid="edit-toggle" aria-expanded={expanded} onClick={() => setExpanded((e) => !e)}
        className="mt-2 text-[11px] font-medium text-ink2 underline-offset-2 hover:underline">
        {expanded ? t("edit.hideChanges") : t("edit.showChanges")} <span className="font-mono text-mute">({editCounts(f.added, f.removed)})</span>
      </button>
      {expanded && (
        <div data-testid="edit-diff" className="mt-1.5 max-h-64 overflow-auto rounded-lg border border-line">
          {diff ? <DiffView lines={diff.lines} added={diff.added} removed={diff.removed} />
            : diffErr ? <p className="p-2 text-[11px] text-mute">{diffErr === "none" || diffErr === "not_text" ? t("edit.noDiff") : t("edit.diffError", { code: diffErr })}</p>
            : <p className="p-2 text-[11px] text-mute">{t("drive.loading")}</p>}
        </div>
      )}

      {state === "pending" && (
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <button type="button" data-testid="edit-keep" onClick={() => setState("kept")}
            className="rounded-md border border-accent bg-accent px-3 py-1 text-[11.5px] font-semibold text-white hover:bg-accent-hover">{t("edit.keep")}</button>
          {latest && (
            <button type="button" data-testid="edit-undo" disabled={busy} onClick={undo}
              className="rounded-md border border-line px-3 py-1 text-[11.5px] font-semibold text-ink2 hover:border-accent disabled:opacity-40">{busy ? t("edit.undoing") : t("edit.undo")}</button>
          )}
          {stale && <span data-testid="edit-stale" className="text-[10.5px] text-mute">{t("edit.stale")}</span>}
        </div>
      )}
      {state === "kept" && <p role="status" data-testid="edit-kept" className="mt-2 text-[11px] text-ok">{t("edit.kept")}</p>}
      {state === "undone" && <p role="status" data-testid="edit-undone" className="mt-2 text-[11px] text-ok">{t("edit.undone", { v: restored })}</p>}
      {err && <p role="alert" className="mt-1.5 text-[11px] text-err">{err}</p>}
    </div>
  );
}
