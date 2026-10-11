"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { agentV2Api, type Intervention } from "@/lib/agentV2Api";
import { canIntervene, canSendApplyNow, errorStatus, fmtEstimate, steerNoteError, STEER_NOTE_MAX } from "@/lib/agentV2Logic";
import { useCan } from "@/lib/session";
import { Btn, timeAgo } from "../ui";

/** "Intervene" control of a task: steer note (next call or apply now with confirmation) and reassign. Admin/owner (tasks:manage). */
export function TaskIntervention({ taskId, status }: { taskId: string; status: string }) {
  const { t } = useT();
  const canManage = useCan("tasks:manage");
  const [open, setOpen] = useState(false);
  const [info, setInfo] = useState<Intervention | null>(null);
  const [note, setNote] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [target, setTarget] = useState("");
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try { setInfo(await agentV2Api.intervention(taskId)); setErr(""); } catch (e) { setErr(errorStatus(e) === 403 ? t("intervene.forbidden") : t("intervene.loadError")); }
  }, [taskId, t]);
  useEffect(() => { if (open) load(); }, [open, load]);

  if (!canIntervene(status) || !canManage) return null;
  const fail = (e: unknown) => setErr(errorStatus(e) === 403 ? t("intervene.forbidden") : errorStatus(e) === 409 ? t("intervene.conflict") : errorStatus(e) === 400 ? t("intervene.rejected") : t("intervene.error"));
  const noteErr = steerNoteError(note);

  const send = async (applyNow: boolean) => {
    setBusy(true); setErr(""); setMsg("");
    try {
      const r = await agentV2Api.steer(taskId, note.trim(), applyNow);
      setNote(""); setConfirming(false);
      setMsg(applyNow ? (r.apply_now_status === "restarted" ? t("intervene.restarted") : t("intervene.notMidCall")) : t("intervene.queued"));
      await load();
    } catch (e) { fail(e); } finally { setBusy(false); }
  };
  const reassign = async () => {
    setBusy(true); setErr(""); setMsg("");
    try { await agentV2Api.reassign(taskId, target); setMsg(t("intervene.reassigned")); setTarget(""); await load(); } catch (e) { fail(e); } finally { setBusy(false); }
  };

  return (
    <div className="mt-2">
      <Btn data-testid={`task-intervene-${taskId}`} onClick={() => setOpen(!open)}>{t("intervene.button")}</Btn>
      {open && (
        <div className="mt-2 space-y-2 rounded-md border border-line bg-bg/60 p-2.5 text-[11.5px]">
          {!info && !err && <div className="text-mute">{t("profile.loading")}</div>}
          {info && (
            <>
              {info.can_steer && (
                <div className="space-y-1.5">
                  <textarea data-testid="task-steer-input" rows={3} value={note} maxLength={STEER_NOTE_MAX} onChange={(e) => { setNote(e.target.value); setConfirming(false); }} placeholder={t("intervene.notePlaceholder")}
                    className="w-full rounded-xl border border-line bg-panel2 px-3 py-2 text-xs text-ink outline-none focus:border-accent" />
                  <div className="flex flex-wrap gap-1.5">
                    <Btn data-testid="task-steer-send" kind="primary" onClick={() => send(false)} disabled={busy || noteErr !== ""}>{t("intervene.send")}</Btn>
                    {info.can_apply_now && !confirming && (
                      <Btn data-testid="task-steer-apply-now" onClick={() => setConfirming(true)} disabled={busy || noteErr !== ""}>{t("intervene.applyNow", { cost: fmtEstimate(info.estimated_rerun_usd) })}</Btn>
                    )}
                  </div>
                  {confirming && (
                    <div data-testid="task-steer-apply-now-confirm" role="alertdialog" className="space-y-1.5 rounded-md border border-warn/40 bg-warn/10 p-2">
                      <div className="text-ink2">{t("intervene.applyNowConfirm", { cost: fmtEstimate(info.estimated_rerun_usd) })}</div>
                      <div className="flex gap-1.5">
                        <Btn data-testid="task-steer-apply-now-confirm-yes" kind="danger" onClick={() => send(true)} disabled={busy || !canSendApplyNow(info, note, confirming)}>{t("intervene.confirm")}</Btn>
                        <Btn data-testid="task-steer-apply-now-confirm-no" onClick={() => setConfirming(false)}>{t("intervene.cancel")}</Btn>
                      </div>
                    </div>
                  )}
                </div>
              )}
              {info.steer_notes.length > 0 && (
                <div>
                  <div className="text-[10px] font-semibold uppercase tracking-wide text-mute">{t("intervene.previous")}</div>
                  <ul data-testid="task-steer-notes" className="mt-0.5 space-y-1">
                    {info.steer_notes.map((n) => (
                      <li key={n.id} className="text-ink2">{n.note}
                        <span className="ml-1 text-[10px] text-mute">{n.author} · {timeAgo(n.created_at)} · {n.delivered_at ? t("intervene.delivered") : t("intervene.pendingDelivery")}</span>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {info.can_reassign && (
                <div className="flex flex-wrap items-center gap-1.5">
                  <select data-testid="task-reassign-select" aria-label={t("intervene.reassign")} value={target} onChange={(e) => setTarget(e.target.value)}
                    className="min-w-0 flex-1 rounded-xl border border-line-strong bg-panel px-2 py-1.5 text-xs outline-none focus:border-accent">
                    <option value="">{t("intervene.reassign")}</option>
                    {info.reassign_candidates.map((c) => <option key={c.id} value={c.id}>{c.name} · {c.title || c.role}</option>)}
                  </select>
                  <Btn data-testid="task-reassign-confirm" onClick={reassign} disabled={busy || !target}>{t("intervene.reassignConfirm")}</Btn>
                </div>
              )}
            </>
          )}
          {msg && <div role="status" className="text-ok">{msg}</div>}
          {err && <div role="alert" className="text-err">{err}</div>}
        </div>
      )}
    </div>
  );
}
