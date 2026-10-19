"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { Btn, Loading } from "../ui";
import { Icon } from "../icons";
import { MeetingsError, meetingsApi, type ActionItem, type Meeting, type Minutes } from "./api";
import { isNotesFile } from "./logic";
import { ErrorBar, errText, inputCls, Section } from "./shared";

/**
 * Minutes of a meeting: paste notes, upload a transcript (.txt .md .vtt .srt) or point at a drive file; an agent
 * writes the summary, decisions and action items and saves them to the drive. Nothing becomes a task until a person
 * ticks the action items and presses "Create tasks".
 */
export function MinutesView({ meeting, onChanged }: { meeting: Meeting; onChanged: () => void }) {
  const { t, locale } = useT();
  const canRequests = useCan("requests:create");
  const canFiles = useCan("files:write");
  const canWrite = canRequests && canFiles;
  const [min, setMin] = useState<Minutes | null | undefined>(undefined);
  const [notes, setNotes] = useState("");
  const [source, setSource] = useState<"notes" | "transcript" | "upload">("notes");
  const [drivePath, setDrivePath] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [msg, setMsg] = useState("");
  const [edit, setEdit] = useState(false);
  const [draft, setDraft] = useState<{ summary: string; decisions: string; items: ActionItem[] }>({ summary: "", decisions: "", items: [] });
  const [sel, setSel] = useState<number[]>([]);
  const fileInput = useRef<HTMLInputElement>(null);

  const load = useCallback(async () => {
    try { setMin(await meetingsApi.minutes(meeting.id)); } catch (e) {
      if (e instanceof MeetingsError && e.code === "not_found") setMin(null);
      else { setMin(null); setErr(errText(e, t)); }
    }
  }, [meeting.id, t]);
  useEffect(() => { void load(); }, [load]);
  useEffect(() => { if (min) setSel(min.action_items.map((it, i) => (it.task_key ? -1 : i)).filter((i) => i >= 0)); }, [min]);

  const canWriteNow = canWrite && (meeting.status === "live" || meeting.status === "ended");

  const onFile = async (f: File) => {
    setErr("");
    if (!isNotesFile(f.name)) { setErr(t("meetings.minutes.fileKind")); return; }
    if (f.size > 2_000_000) { setErr(t("meetings.error.too_large")); return; }
    setNotes(await f.text());
    setSource("upload");
    setDrivePath("");
  };

  const generate = async () => {
    setBusy(true); setErr(""); setMsg("");
    try {
      const m = await meetingsApi.generateMinutes(meeting.id, drivePath.trim() ? { file_path: drivePath.trim(), lang: locale } : { notes, source, lang: locale });
      setMin(m); setNotes(""); onChanged();
    } catch (e) { setErr(errText(e, t)); } finally { setBusy(false); }
  };

  const startEdit = () => {
    if (!min) return;
    setDraft({ summary: min.summary, decisions: min.decisions.join("\n"), items: min.action_items.map((x) => ({ ...x })) });
    setEdit(true);
  };
  const saveEdit = async () => {
    setBusy(true); setErr("");
    try {
      const m = await meetingsApi.editMinutes(meeting.id, { summary: draft.summary, decisions: draft.decisions.split("\n").map((s) => s.trim()).filter(Boolean),
        action_items: draft.items.filter((x) => x.text.trim()).map((x) => ({ text: x.text, owner: x.owner, due: x.due })) });
      setMin(m); setEdit(false);
    } catch (e) { setErr(errText(e, t)); } finally { setBusy(false); }
  };

  const createTasks = async () => {
    setBusy(true); setErr(""); setMsg("");
    try {
      const r = await meetingsApi.createTasks(meeting.id, sel, locale);
      setMin(r.minutes); setMsg(t("meetings.minutes.tasksCreated", { n: sel.length })); onChanged();
    } catch (e) { setErr(errText(e, t)); } finally { setBusy(false); }
  };

  if (min === undefined) return <Loading rows={2} />;

  if (min === null) {
    return (
      <div data-testid="minutes-empty" className="space-y-3">
        <ErrorBar msg={err} />
        {!canWriteNow ? (
          <p className="rounded-lg border border-dashed border-line-strong px-4 py-6 text-center text-[13px] text-mute">
            {meeting.status === "scheduled" ? t("meetings.minutes.notYet") : meeting.status === "cancelled" ? t("meetings.minutes.cancelled") : t("meetings.minutes.noPerm")}
          </p>
        ) : (
          <Section title={t("meetings.minutes.generate")}>
            <p className="mb-2 text-[12px] text-mute">{t("meetings.minutes.generateHint")}</p>
            <textarea data-testid="minutes-notes" value={notes} rows={10} maxLength={60000} onChange={(e) => { setNotes(e.target.value); if (source === "upload") setSource("notes"); }}
              placeholder={t("meetings.minutes.notesPh")} className={`${inputCls} font-mono`} />
            <div className="mt-2 flex flex-wrap items-center gap-2">
              <input ref={fileInput} type="file" accept=".txt,.md,.markdown,.vtt,.srt,.csv,.log,text/plain,text/markdown,text/vtt" className="hidden" data-testid="minutes-file"
                onChange={(e) => { const f = e.target.files?.[0]; if (f) void onFile(f); e.target.value = ""; }} />
              <Btn onClick={() => fileInput.current?.click()}>{t("meetings.minutes.upload")}</Btn>
              <span className="text-[11px] text-mute">{t("meetings.minutes.uploadHint")}</span>
            </div>
            <div className="mt-2 flex flex-wrap items-center gap-2">
              <input data-testid="minutes-drive-path" value={drivePath} onChange={(e) => setDrivePath(e.target.value)} placeholder={t("meetings.minutes.drivePh")} className={`${inputCls} max-w-sm`} />
              <span className="text-[11px] text-mute">{t("meetings.minutes.driveHint")}</span>
            </div>
            <div className="mt-3 flex justify-end">
              <Btn kind="primary" data-testid="minutes-generate" disabled={busy || (!notes.trim() && !drivePath.trim())} onClick={generate}>{busy ? t("meetings.minutes.writing") : t("meetings.minutes.write")}</Btn>
            </div>
          </Section>
        )}
      </div>
    );
  }

  const locked = !!min.request_id;
  const pending = min.action_items.filter((x) => !x.task_key).length;
  return (
    <div data-testid="minutes-view" className="space-y-3">
      <ErrorBar msg={err} />
      {msg && <div data-testid="minutes-msg" className="rounded-lg bg-ok/10 px-3 py-2 text-xs text-ok">{msg}</div>}
      <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-mute">
        <span>{t(min.summary_source === "runtime" ? "meetings.minutes.byAgent" : "meetings.minutes.byRules")}{min.truncated ? ` · ${t("meetings.minutes.truncated")}` : ""}</span>
        <span className="flex items-center gap-3">
          {min.file_path && <Link data-testid="minutes-file-link" href={`/files?path=${encodeURIComponent(min.file_path)}`} className="inline-flex items-center gap-1 font-medium text-accent hover:underline"><Icon name="doc" size={12} />{t("meetings.minutes.inDrive")}</Link>}
          {canWrite && !locked && !edit && <button type="button" data-testid="minutes-edit" onClick={startEdit} className="inline-flex items-center gap-1 font-medium text-accent hover:underline"><Icon name="edit" size={12} />{t("meetings.action.edit")}</button>}
        </span>
      </div>

      {edit ? (
        <div className="space-y-3" data-testid="minutes-editor">
          <Section title={t("meetings.minutes.summary")}><textarea rows={5} value={draft.summary} onChange={(e) => setDraft({ ...draft, summary: e.target.value })} className={inputCls} /></Section>
          <Section title={t("meetings.minutes.decisions")}>
            <textarea rows={4} value={draft.decisions} onChange={(e) => setDraft({ ...draft, decisions: e.target.value })} placeholder={t("meetings.minutes.onePerLine")} className={inputCls} />
          </Section>
          <Section title={t("meetings.minutes.actions")} right={<button type="button" onClick={() => setDraft({ ...draft, items: [...draft.items, { text: "" }] })} className="text-[11px] font-medium text-accent hover:underline">{t("meetings.action.addItem")}</button>}>
            <div className="space-y-1.5">
              {draft.items.map((it, i) => (
                <div key={i} className="flex flex-wrap items-center gap-1.5">
                  <input value={it.text} onChange={(e) => setDraft({ ...draft, items: draft.items.map((x, k) => (k === i ? { ...x, text: e.target.value } : x)) })} className={`${inputCls} min-w-[12rem] flex-1`} placeholder={t("meetings.minutes.itemPh")} />
                  <input value={it.owner ?? ""} onChange={(e) => setDraft({ ...draft, items: draft.items.map((x, k) => (k === i ? { ...x, owner: e.target.value } : x)) })} className={`${inputCls} w-32`} placeholder={t("meetings.field.owner")} />
                  <input type="date" value={it.due ?? ""} onChange={(e) => setDraft({ ...draft, items: draft.items.map((x, k) => (k === i ? { ...x, due: e.target.value } : x)) })} className={`${inputCls} w-36`} aria-label={t("meetings.minutes.due")} />
                  <button type="button" aria-label={t("meetings.action.remove")} onClick={() => setDraft({ ...draft, items: draft.items.filter((_, k) => k !== i) })} className="text-mute hover:text-err"><Icon name="x" size={13} /></button>
                </div>
              ))}
            </div>
          </Section>
          <div className="flex justify-end gap-2"><Btn onClick={() => setEdit(false)}>{t("meetings.action.cancel")}</Btn><Btn kind="primary" data-testid="minutes-save" disabled={busy} onClick={saveEdit}>{t("meetings.action.save")}</Btn></div>
        </div>
      ) : (
        <>
          <Section title={t("meetings.minutes.summary")}><p data-testid="minutes-summary" className="whitespace-pre-wrap text-[13px] leading-relaxed text-ink">{min.summary}</p></Section>
          <Section title={t("meetings.minutes.decisions")}>
            {min.decisions.length === 0 ? <p className="text-xs text-mute">{t("meetings.minutes.none")}</p> : (
              <ul data-testid="minutes-decisions" className="list-disc space-y-1 pl-5 text-[13px] text-ink">{min.decisions.map((d, i) => <li key={i}>{d}</li>)}</ul>
            )}
          </Section>
          <Section title={t("meetings.minutes.actions")} right={min.request_id ? <span className="text-[11px] font-medium text-ok">{t("meetings.minutes.tasksDone")}</span> : undefined}>
            {min.action_items.length === 0 ? <p className="text-xs text-mute">{t("meetings.minutes.none")}</p> : (
              <ul data-testid="minutes-actions" className="space-y-1.5">
                {min.action_items.map((it, i) => (
                  <li key={i} data-testid="minutes-action" className="flex items-start gap-2 text-[13px]">
                    <input type="checkbox" className="mt-1" disabled={!!it.task_key || !canWrite} checked={!!it.task_key || sel.includes(i)} aria-label={it.text}
                      onChange={(e) => setSel((s) => (e.target.checked ? [...s, i] : s.filter((x) => x !== i)))} />
                    <span className="min-w-0 flex-1">
                      <span className="text-ink">{it.text}</span>
                      <span className="ml-2 inline-flex flex-wrap items-center gap-1.5 align-middle text-[11px] text-mute">
                        {it.owner && <span className="rounded bg-panel2 px-1.5 py-[1px]">{it.owner}{it.owner_kind === "agent" ? ` · ${t("meetings.attendee.ai")}` : ""}</span>}
                        {it.due && <span className="rounded bg-panel2 px-1.5 py-[1px]">{t("meetings.minutes.due")}: {it.due}</span>}
                        {it.task_key && <span className="rounded bg-ok/10 px-1.5 py-[1px] font-semibold text-ok">{t("meetings.minutes.taskCreated")}</span>}
                      </span>
                    </span>
                  </li>
                ))}
              </ul>
            )}
            {canWrite && pending > 0 && (
              <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-line pt-3">
                <p className="text-[11px] text-mute">{t("meetings.minutes.tasksHint")}</p>
                <Btn kind="primary" data-testid="minutes-create-tasks" disabled={busy || sel.length === 0} onClick={createTasks}>{t("meetings.minutes.createTasks", { n: sel.length })}</Btn>
              </div>
            )}
          </Section>
          {canWrite && !locked && (
            <details className="rounded-xl border border-line bg-panel p-3 text-xs">
              <summary className="cursor-pointer font-medium text-ink2">{t("meetings.minutes.regenerate")}</summary>
              <p className="mt-2 text-[11px] text-mute">{t("meetings.minutes.regenerateHint")}</p>
              <textarea value={notes} rows={6} onChange={(e) => setNotes(e.target.value)} placeholder={t("meetings.minutes.notesPh")} className={`${inputCls} mt-2 font-mono`} />
              <div className="mt-2 flex justify-end"><Btn disabled={busy || !notes.trim()} onClick={generate}>{t("meetings.minutes.write")}</Btn></div>
            </details>
          )}
        </>
      )}
    </div>
  );
}
