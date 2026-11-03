"use client";
import { useCallback, useEffect, useState } from "react";
import { fmtDateTime, useT } from "@/lib/i18n";
import { officesApi } from "@/lib/officesApi";
import { NOTE_MAX, noteIssue, type NoteScope, type TeamNote } from "@/lib/officesLogic";
import { Modal } from "../templates/Modal";
import { btnGhost, btnPrimary, inputCls } from "./shared";

/** The memory of an office or of the person (E2, equipos-colaboracion.md §2.9): what their agents keep in mind. The
 *  responsible of an office may promote a note to company knowledge. With onClose it is a dialog, else inline. */
export function TeamMemory({ scope, refId, title, onClose }: { scope: NoteScope; refId: string; title?: string; onClose?: () => void }) {
  const { t } = useT();
  const [notes, setNotes] = useState<TeamNote[] | null>(null);
  const [canWrite, setCanWrite] = useState(false);
  const [canPromote, setCanPromote] = useState(false);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);

  const load = useCallback(() => {
    officesApi.notes(scope, refId).then((r) => { setNotes(r.notes); setCanWrite(r.can_write); setCanPromote(r.can_promote); })
      .catch(() => setError(t("offices.memory.loadError")));
  }, [scope, refId, t]);
  useEffect(load, [load]);

  const act = async (fn: () => Promise<unknown>, ok?: string) => {
    setBusy(true); setError(null); setDone(null);
    try { await fn(); if (ok) setDone(ok); load(); } catch (e) {
      setError(t(String(e).includes("400") ? "offices.memory.unsafe" : "offices.memory.saveError"));
    } finally { setBusy(false); }
  };
  const issue = noteIssue(text);

  const body = (
    <div className="grid gap-3" data-testid={`team-memory-${scope}`}>
      {onClose && <p className="text-[12px] text-mute">{t("offices.memory.hint")}</p>}
      {canWrite && (
        <div className="grid gap-2">
          <textarea data-testid={`team-memory-text-${scope}`} value={text} onChange={(e) => setText(e.target.value)} rows={2} maxLength={NOTE_MAX + 50}
            placeholder={scope === "person" ? t("offices.memory.placeholderMine") : t("offices.memory.placeholder")} className={inputCls} />
          <div className="flex items-center justify-between gap-2">
            <span className="text-[11px] text-mute">{issue === "long" ? t("offices.memory.long") : ""}</span>
            <button type="button" data-testid={`team-memory-add-${scope}`} disabled={!!issue || busy} className={btnPrimary}
              onClick={() => void act(async () => { await officesApi.addNote(scope, refId, text.trim()); setText(""); })}>{t("offices.memory.add")}</button>
          </div>
        </div>
      )}
      {error && <div role="alert" className="text-[12px] font-semibold text-red-600">{error}</div>}
      {done && <div role="status" className="text-[12px] font-semibold text-emerald-700">{done}</div>}
      {!notes && !error && <div className="text-[12px] text-mute">{t("org.loading")}</div>}
      {notes && notes.length === 0 && <p className="text-[12px] text-mute">{t("offices.memory.empty")}</p>}
      <ul className="grid gap-1.5">
        {notes?.map((n) => (
          <li key={n.id} data-testid={`team-note-${n.id}`} className="rounded-lg border border-line bg-panel2/60 px-3 py-2">
            <p className="whitespace-pre-wrap text-[13px]">{n.text}</p>
            <div className="mt-1 flex flex-wrap items-center gap-x-3 text-[11px] text-mute">
              <span>{n.author_name || n.created_by}</span>
              <span>{fmtDateTime(n.created_at)}</span>
              {(n.promoted_from || n.promoted) && <span className="font-semibold text-accent">{t("offices.memory.promoted")}</span>}
              {canPromote && scope === "office" && !n.promoted && (
                <button type="button" disabled={busy} data-testid={`team-note-promote-${n.id}`} className="font-semibold text-accent hover:underline"
                  onClick={() => void act(() => officesApi.promote(n.id), t("offices.memory.promotedOk"))}>{t("offices.memory.promote")}</button>
              )}
              {n.can_delete && (
                <button type="button" disabled={busy} data-testid={`team-note-delete-${n.id}`} className="text-red-600 hover:underline"
                  onClick={() => void act(() => officesApi.deleteNote(n.id))}>{t("offices.memory.delete")}</button>
              )}
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
  if (!onClose) return body;
  return (
    <Modal title={title ?? t("offices.memory")} onClose={onClose} testid="team-memory-dialog">
      {body}
      <div className="mt-4 flex justify-end"><button type="button" onClick={onClose} className={btnGhost}>{t("offices.done")}</button></div>
    </Modal>
  );
}
