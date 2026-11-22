"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { ApiError } from "@/lib/api";
import { watchdogApi, type StallAction, type StallDetail } from "@/lib/watchdogApi";
import { canCancel, confirmIds, stallAge, stallDoneKey, stallErrorKey } from "@/lib/watchdogLogic";
import { Btn, useAgentName } from "../ui";

/**
 * Recovery of a stalled task (F6 watchdog): Retry (requests:create), Reassign and Cancel (tasks:manage). Cancel opens a
 * confirmation that lists the active dependents it also stops; the server checks that exact list. Renders nothing when
 * the task is not stalled (unless `explain` asks to say so: the suggestion of a task that moved again).
 */
export function StallActions({ taskId, explain = false, onDone }: { taskId: string; explain?: boolean; onDone?: () => void }) {
  const { t } = useT();
  const name = useAgentName();
  const canRetry = useCan("requests:create");
  const canManage = useCan("tasks:manage");
  const [d, setD] = useState<StallDetail | null>(null);
  const [gone, setGone] = useState(false);
  const [busy, setBusy] = useState(false);
  const [picking, setPicking] = useState(false);
  const [target, setTarget] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");

  const load = useCallback(async () => {
    try { setD(await watchdogApi.detail(taskId)); setGone(false); } catch { setD(null); setGone(true); }
  }, [taskId]);
  useEffect(() => { load(); }, [load]);

  if (!d?.stalled) {
    if (msg) return <div role="status" data-testid={`stall-done-${taskId}`} className="mt-2 text-[11px] font-semibold text-emerald-700">{msg}</div>;
    return explain && (d || gone) ? <div className="mt-2 text-[11px] text-mute">{t("stall.noLonger")}</div> : null;
  }
  const st = d.stall!;
  const age = stallAge(st.minutes);

  const run = async (action: StallAction, fn: () => Promise<{ status: string }>) => {
    setBusy(true); setErr(""); setMsg("");
    try {
      const r = await fn();
      setMsg(t(stallDoneKey(action, r.status)));
      setConfirming(false); setPicking(false); setTarget("");
      onDone?.();
    } catch (e) {
      const status = e instanceof ApiError ? e.status : 0;
      setErr(t(stallErrorKey(status, e instanceof ApiError ? e.code : undefined)));
    } finally {
      setBusy(false);
      await load();
    }
  };

  return (
    <div data-testid={`stall-actions-${taskId}`} className="mt-2 space-y-2 rounded-md border border-line bg-panel2 p-2.5 text-[11.5px]">
      <div className="text-ink2">
        <span className="font-semibold text-ink">{t("stall.title")}</span> · {t(age.key, { n: age.n })} · <span className="text-mute">{t(`stall.kind.${st.kind}`)}</span>
      </div>
      <div className="flex flex-wrap gap-1.5">
        {canRetry && <Btn kind="primary" disabled={busy} data-testid={`stall-retry-${taskId}`} onClick={() => run("retry", () => watchdogApi.retry(taskId))}>{t("stall.retry")}</Btn>}
        {canManage && <Btn disabled={busy} data-testid={`stall-reassign-${taskId}`} onClick={() => setPicking(!picking)}>{t("stall.reassign")}</Btn>}
        {canManage && <Btn kind="danger" disabled={busy || !canCancel(d)} data-testid={`stall-cancel-${taskId}`} onClick={() => setConfirming(true)}>{t("stall.cancel")}</Btn>}
      </div>
      {picking && (
        d.reassign_candidates.length === 0 ? <div className="text-mute">{t("stall.noCandidates")}</div> : (
          <div className="flex flex-wrap items-center gap-1.5">
            <select data-testid={`stall-target-${taskId}`} value={target} onChange={(e) => setTarget(e.target.value)}
              className="rounded-md border border-line-strong bg-panel px-2 py-1 text-xs text-ink outline-none focus:border-accent">
              <option value="">{t("stall.pick")}</option>
              {d.reassign_candidates.map((c) => <option key={c.id} value={c.id}>{c.name}{c.title ? ` · ${c.title}` : ""}</option>)}
            </select>
            <Btn kind="primary" disabled={busy || !target} data-testid={`stall-reassign-confirm-${taskId}`}
              onClick={() => run("reassign", () => watchdogApi.reassign(taskId, target))}>{t("stall.reassign")}</Btn>
          </div>
        )
      )}
      {d.hidden_cascade > 0 && <div className="text-mute">{t("stall.err.cascadeHidden")}</div>}
      {confirming && (
        <CancelDialog title={d.task.title} cascade={d.cancel_cascade.map((c) => ({ id: c.id, title: c.title, agent: name(c.agent_id) }))} busy={busy}
          onCancel={() => setConfirming(false)} onConfirm={() => run("cancel", () => watchdogApi.cancel(taskId, confirmIds(d.cancel_cascade)))} />
      )}
      {msg && <div role="status" className="font-semibold text-emerald-700">{msg}</div>}
      {err && <div role="alert" data-testid={`stall-error-${taskId}`} className="text-red-700">{err}</div>}
    </div>
  );
}

function CancelDialog({ title, cascade, busy, onCancel, onConfirm }: {
  title: string; cascade: { id: string; title: string; agent: string }[]; busy: boolean; onCancel: () => void; onConfirm: () => void;
}) {
  const { t } = useT();
  useEffect(() => {
    const k = (e: KeyboardEvent) => { if (e.key === "Escape") onCancel(); };
    document.addEventListener("keydown", k);
    return () => document.removeEventListener("keydown", k);
  }, [onCancel]);
  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-black/40 p-0 sm:items-center sm:p-4" role="presentation" onMouseDown={onCancel}>
      <div role="dialog" aria-modal="true" aria-label={t("stall.cancelTitle")} data-testid="stall-cancel-dialog" onMouseDown={(e) => e.stopPropagation()}
        className="ac-pop max-h-[92dvh] w-full overflow-y-auto rounded-t-2xl border border-line bg-panel px-4 pb-[max(1rem,env(safe-area-inset-bottom))] pt-4 shadow-float sm:w-[min(440px,92vw)] sm:rounded-xl sm:p-4">
        <h3 className="text-[14px] font-semibold text-ink">{t("stall.cancelTitle")}</h3>
        <p className="mt-2 text-[12.5px] leading-snug text-ink2">
          {cascade.length === 0 ? t("stall.cancelOnly", { task: title }) : t("stall.cancelWith", { task: title, n: cascade.length })}
        </p>
        {cascade.length > 0 && (
          <ul data-testid="stall-cancel-cascade" className="mt-2 max-h-48 space-y-1 overflow-y-auto">
            {cascade.map((c) => (
              <li key={c.id} className="rounded-md bg-panel2 px-2 py-1 text-[12px] text-ink">{c.title} <span className="text-mute">· {c.agent}</span></li>
            ))}
          </ul>
        )}
        <div className="mt-4 flex justify-end gap-2">
          <Btn onClick={onCancel}>{t("stall.keep")}</Btn>
          <Btn kind="danger" disabled={busy} data-testid="stall-cancel-confirm" onClick={onConfirm}>{t("stall.cancelConfirm")}</Btn>
        </div>
      </div>
    </div>
  );
}
