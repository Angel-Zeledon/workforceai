"use client";
import { useState } from "react";
import { api } from "@/lib/api";
import { useT } from "@/lib/i18n";
import { approvalReason } from "@/lib/permissionsLogic";
import { permissionsApi, statusOf, undoRemember, type RememberScope } from "@/lib/permissionsApi";
import { useRememberNotice } from "@/lib/rememberNotice";
import { useProjects } from "@/lib/projects/store";
import { useStore } from "@/lib/store";
import { toast } from "@/lib/toast";
import type { Approval } from "@/lib/types";
import { Btn } from "../ui";

/** "Approve and do not ask again" next to Approve (docs/architecture/06-permisos-autonomia.md section 11). Only offered
 *  when the agent's own permissions asked (never the safety floor, the organization rules or high risk). It approves,
 *  then creates the exception for this agent and category, or for this project or batch when the approval comes from
 *  one; Pendientes shows what was created with Undo. */
export function RememberApproval({ a, busy, setBusy }: { a: Approval; busy: boolean; setBusy: (b: boolean) => void }) {
  const { t } = useT();
  const r = approvalReason(a.policy_rule, Array.isArray(a.context?.flags) ? (a.context!.flags as unknown[]).map(String) : []);
  const batchId = useProjects((s) => (a.project_id ? s.projects[a.project_id]?.batch_id : undefined));
  const agentName = useStore((s) => s.agents[a.agent_id]?.name ?? a.agent_id);
  const [open, setOpen] = useState(false);
  const [scope, setScope] = useState<RememberScope>(a.project_id ? "project" : "agent");
  const [err, setErr] = useState("");
  if (!r?.rememberable || !r.category) return null;
  const cat = t(`perm.cat.${r.category}`);
  const scopes: RememberScope[] = ["agent", ...(a.project_id ? ["project" as const] : []), ...(batchId ? ["batch" as const] : [])];
  const go = async () => {
    setBusy(true); setErr("");
    try {
      await api.decide(a.id, "approve");
    } catch { setErr(t("approvals.decideError")); setBusy(false); return; }
    try {
      const res = await permissionsApi.remember(a.id, scope);
      useRememberNotice.getState().set({ ...res, agentName });
      toast(t(`perm.remember.done.${res.scope}`, { agent: agentName, cat }));
    } catch (e) {
      // The approval stands; only the "do not ask again" part failed.
      toast(t(statusOf(e) === 403 ? "perm.error.forbidden" : "perm.remember.failed"), "warn");
    }
  };
  if (!open) {
    return <Btn data-testid={`approve-remember-${a.id}`} disabled={busy} onClick={() => setOpen(true)}>{t("perm.remember.button")}</Btn>;
  }
  return (
    <div data-testid={`remember-${a.id}`} className="mt-2 w-full rounded-md border border-line bg-panel p-2 text-[11.5px] text-ink2">
      <div className="font-semibold text-ink">{t("perm.remember.title", { cat })}</div>
      <div role="radiogroup" aria-label={t("perm.remember.title", { cat })} className="mt-1 space-y-1">
        {scopes.map((sc) => (
          <label key={sc} className="flex items-center gap-1.5">
            <input type="radio" name={`remember-${a.id}`} data-testid={`remember-${a.id}-${sc}`} checked={scope === sc} disabled={busy} onChange={() => setScope(sc)} />
            {t(`perm.remember.scope.${sc}`, { agent: agentName, cat })}
          </label>
        ))}
      </div>
      <p className="mt-1 text-mute">{t("perm.remember.hint")}</p>
      <div className="mt-2 flex gap-2">
        <Btn kind="ok" disabled={busy} data-testid={`remember-confirm-${a.id}`} onClick={go}>{t("perm.remember.confirm")}</Btn>
        <Btn disabled={busy} onClick={() => setOpen(false)}>{t("perm.cancel")}</Btn>
      </div>
      {err && <p className="mt-1 text-err">{err}</p>}
    </div>
  );
}

/** What the last "approve and do not ask again" created, with Undo (shown at the top of Pendientes). */
export function RememberNotice() {
  const { t } = useT();
  const last = useRememberNotice((s) => s.last);
  const [busy, setBusy] = useState(false);
  if (!last) return null;
  const cat = t(`perm.cat.${last.category}`);
  return (
    <div data-testid="remember-notice" className="mb-2 rounded-md border border-ok/40 bg-ok/10 p-2 text-[11.5px] text-ink2">
      <p>{t(`perm.remember.done.${last.scope}`, { agent: last.agentName, cat })}</p>
      <div className="mt-1 flex gap-3">
        <button type="button" data-testid="remember-undo" disabled={busy} className="font-semibold text-accent hover:underline disabled:opacity-50"
          onClick={async () => {
            setBusy(true);
            try { await undoRemember(last); useRememberNotice.getState().set(null); toast(t("perm.remember.undone")); } catch { toast(t("perm.error.action"), "err"); } finally { setBusy(false); }
          }}>{t("perm.remember.undo")}</button>
        <button type="button" className="text-mute hover:underline" onClick={() => useRememberNotice.getState().set(null)}>{t("common.close")}</button>
      </div>
    </div>
  );
}
