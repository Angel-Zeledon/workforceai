"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { ctlApi } from "@/lib/connections/api";
import { useConnections } from "@/lib/connections/store";
import type { EmailDraft } from "@/lib/connections/types";
import { useStore } from "@/lib/store";
import { Btn, useTicker } from "../ui";
import { ErrorLine, errText, inputCls } from "../connections/shared";

const EDITABLE: EmailDraft["status"][] = ["draft", "pending_approval", "blocked"];

/**
 * Editable email draft with human approval and a MANDATORY cancellation window (decided by the server, never below 60 s).
 * Editing after an approval is impossible; editing before it changes the content hash, so a previous approval would be void.
 */
export function EmailDraftCard({ draft }: { draft: EmailDraft }) {
  const { t } = useT();
  useTicker(500);
  const agents = useStore((s) => s.agents);
  const controls = useConnections((s) => s.controls);
  const [to, setTo] = useState(draft.to.join(", "));
  const [subject, setSubject] = useState(draft.subject);
  const [body, setBody] = useState(draft.body);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const editable = EDITABLE.includes(draft.status);

  // Re-sync local fields when the server version changes (another tab/agent edited it) and we are not mid-edit.
  const [seenVersion, setSeenVersion] = useState(draft.version);
  useEffect(() => {
    if (draft.version !== seenVersion) { setTo(draft.to.join(", ")); setSubject(draft.subject); setBody(draft.body); setSeenVersion(draft.version); }
  }, [draft, seenVersion]);

  const dirty = to !== draft.to.join(", ") || subject !== draft.subject || body !== draft.body;
  const recipients = () => to.split(",").map((x) => x.trim()).filter(Boolean);
  const blockedByControls = controls?.kill_switch_level !== "none" || controls?.mode === "read_only";
  const seconds = draft.status === "held" && draft.hold_until ? Math.max(0, Math.ceil((new Date(draft.hold_until).getTime() - Date.now()) / 1000)) : 0;

  useEffect(() => {
    if (draft.status === "held" && seconds === 0) { const id = setTimeout(() => void useConnections.getState().loadOutbox(), 900); return () => clearTimeout(id); }
  }, [draft.status, seconds]);

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true); setErr(null);
    try { await fn(); await useConnections.getState().loadOutbox(); await useConnections.getState().loadUsage(); }
    catch (e) { setErr(errText(e)); await useConnections.getState().loadOutbox(); }
    finally { setBusy(false); }
  };
  const save = () => run(async () => { await ctlApi.editDraft(draft.id, { to: recipients(), subject, body }); });
  const approve = () => run(async () => {
    let version = draft.version;
    if (dirty) version = (await ctlApi.editDraft(draft.id, { to: recipients(), subject, body })).version;
    await ctlApi.approveDraft(draft.id, version);
  });

  return (
    <div data-testid={`ctl-draft-${draft.id}`} data-status={draft.status} className="rounded-xl border border-line bg-panel2 p-3">
      <div className="mb-2 flex items-center justify-between gap-2">
        <div className="text-xs font-semibold">{t("ctl.draft.by", { name: agents[draft.agent_id]?.name ?? draft.agent_id })}</div>
        <span data-testid="ctl-draft-status" className="rounded-full bg-line px-2 py-[1px] text-[10px] font-semibold uppercase text-mute">{t(`ctl.draft.status.${draft.status}`)}</span>
      </div>

      <div className="mb-2 flex flex-wrap gap-x-4 gap-y-1 text-[11px]">
        <span data-testid="conn-approval-account"><b>{t("ctl.draft.account")}:</b> {draft.account_label ?? "—"}</span>
        <span data-testid="conn-approval-reversibility" data-value={draft.reversibility} className="font-semibold text-red-700">{t(`conn.approval.rev.${draft.reversibility}`)}</span>
        {draft.origin_external && <span data-testid="conn-approval-taint" className="font-semibold text-amber-700">{t("conn.approval.external_origin")}</span>}
        {draft.new_recipient && <span className="font-semibold text-amber-700">{t("ctl.draft.newRecipient")}</span>}
      </div>

      <div className="space-y-2">
        <label className="block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("ctl.draft.to")}
          <input data-testid="ctl-draft-to" className={`${inputCls} mt-1 font-normal normal-case`} value={to} onChange={(e) => setTo(e.target.value)} disabled={!editable} />
        </label>
        <label className="block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("ctl.draft.subject")}
          <input data-testid="ctl-draft-subject" className={`${inputCls} mt-1 font-normal normal-case`} value={subject} onChange={(e) => setSubject(e.target.value)} disabled={!editable} />
        </label>
        <label className="block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("ctl.draft.body")}
          <textarea data-testid="ctl-draft-body" rows={6} className={`${inputCls} mt-1 resize-y font-normal normal-case`} value={body} onChange={(e) => setBody(e.target.value)} disabled={!editable} />
        </label>
      </div>

      {draft.block_reason && <p className="mt-2 text-[11px] font-semibold text-amber-700">{t(`conn.deny.${draft.block_reason}`)}</p>}

      {draft.status === "held" && (
        <div data-testid="ctl-hold-countdown" data-seconds={seconds} className="mt-3 rounded-xl border border-amber-300 bg-amber-50 p-3">
          <div className="flex items-center justify-between gap-2">
            <span className="text-xs font-semibold text-amber-800">{t("ctl.hold.sendingIn", { seconds })}</span>
            <Btn data-testid="ctl-hold-cancel" kind="danger" disabled={busy || seconds === 0} onClick={() => run(() => ctlApi.cancelHold(draft.id))}>{t("ctl.hold.cancel")}</Btn>
          </div>
          <p data-testid="ctl-hold-undo-note" className="mt-1 text-[11px] text-amber-800">{t("ctl.hold.undoNote")}</p>
          <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-amber-200"><div className="h-full rounded-full bg-amber-500 transition-all" style={{ width: `${(seconds / Math.max(1, draft.hold_seconds)) * 100}%` }} /></div>
        </div>
      )}

      {draft.status === "sent" && <p data-testid="ctl-sent-irreversible" className="mt-2 text-[11px] font-semibold text-red-700">{t("ctl.hold.sentIrreversible")}</p>}

      {editable && (
        <>
          <p data-testid="conn-approval-hold" className="mt-2 text-[11px] text-mute">{t("ctl.hold.rule", { seconds: draft.hold_seconds })}</p>
          {blockedByControls && <p className="mt-1 text-[11px] font-semibold text-red-700">{t("ctl.draft.blockedByControls")}</p>}
          <div className="mt-2 flex flex-wrap justify-end gap-2">
            <Btn data-testid="ctl-draft-reject" kind="danger" disabled={busy} onClick={() => run(() => ctlApi.rejectDraft(draft.id))}>{t("ctl.draft.reject")}</Btn>
            <Btn data-testid="ctl-draft-save" disabled={busy || !dirty} onClick={save}>{t("ctl.draft.save")}</Btn>
            <Btn data-testid="ctl-draft-approve" kind="ok" disabled={busy || blockedByControls || recipients().length === 0 || !subject.trim()} onClick={approve}>{t("ctl.draft.approveSend")}</Btn>
          </div>
        </>
      )}
      <div className="mt-2"><ErrorLine msg={err} /></div>
    </div>
  );
}
