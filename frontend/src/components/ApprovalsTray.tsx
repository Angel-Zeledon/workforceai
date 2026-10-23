"use client";
import { useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";
import { useStore } from "@/lib/store";
import type { Approval } from "@/lib/types";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { Btn, Dot, RiskBadge, useAgentColor, useAgentName } from "./ui";
import { CodeRunView } from "./code/CodeRunView";
import { DevilsAdvocate } from "./approvals/DevilsAdvocate";

/** `scope="office"` is the card shown over the approvals desk in the 3D office: same decision call and permission
 * checks, different test ids so the tray's `approval-<id>` selectors stay unique. */
export function ApprovalCard({ a, compact = false, scope = "tray" }: { a: Approval; compact?: boolean; scope?: "tray" | "office" }) {
  const { t } = useT();
  const name = useAgentName();
  const color = useAgentColor();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const canDecide = useCan("approvals:decide");
  // "Always allow this kind of action in this project": only offered when the server marked the approval as an
  // internal, reversible action of a project. The server checks it again; it never covers external actions.
  // The three choices of the card: approve once (default) / approve and remember for this session or this project / deny.
  const [always, setAlways] = useState(false);
  const [remember, setRemember] = useState<"session" | "project">("project");
  const [anyAgent, setAnyAgent] = useState(false);
  const decide = async (d: "approve" | "reject") => {
    setBusy(true); setErr("");
    try { await api.decide(a.id, d, undefined, d === "approve" && always ? { anyAgent, scope: remember } : undefined); } catch { setErr(t("approvals.decideError")); setBusy(false); }
  };
  const canRemember = !!a.standing_eligible && !!a.project_id;
  const approveLabel = !canRemember ? t("approvals.approve") : !always ? t("approvals.approveOnce") : remember === "session" ? t("approvals.approveSession") : t("approvals.approveProject");
  const pending = a.status === "pending";
  // Same rule as the server (IsHighRiskApproval): the risk the policy engine stored, or a double approval.
  const highRisk = a.risk === "high" || (a.required_approvals ?? 1) >= 2;
  return (
    <div data-testid={pending ? (scope === "office" ? `office-approval-card-${a.id}` : `approval-${a.id}`) : undefined} className="rounded-md border border-line bg-panel2 p-3">
      <div className="mb-1 flex items-center justify-between gap-2">
        <span className="flex items-center gap-1.5 text-[11px] text-mute"><Dot color={color(a.agent_id)} />{name(a.agent_id)} · <span className="font-mono">{a.action}</span></span>
        <RiskBadge risk={a.risk} />
      </div>
      <div className="text-[13px] font-semibold text-ink">{a.title}</div>
      {a.action === "code.run" ? <CodeRunView approvalId={a.id} status={a.status} compact={compact} />
        : !compact && a.details && <p className="mt-1 line-clamp-6 break-words text-[12px] leading-snug text-ink2 [overflow-wrap:anywhere]" title={a.details}>{a.details}</p>}
      {pending && !canDecide ? (
        <div data-testid={scope === "office" ? "office-approval-no-permission" : "approval-no-permission"} className="mt-2 text-[11px] text-mute">{t("approvals.noPermission")}</div>
      ) : pending ? (
        <>
        {highRisk && <DevilsAdvocate approvalId={a.id} />}
        {canRemember && (
          <div className="mt-2 space-y-1 text-[11px] text-ink2">
            <label className="flex items-center gap-1.5 font-semibold">
              <input type="checkbox" data-testid={`approval-always-${a.id}`} checked={always} disabled={busy} onChange={(e) => setAlways(e.target.checked)} />
              {t("approvals.always.label")}
            </label>
            {always && (
              <div role="radiogroup" aria-label={t("approvals.scope.label")} className="ml-5 flex flex-wrap items-center gap-x-4 gap-y-1">
                {(["session", "project"] as const).map((sc) => (
                  <label key={sc} className="flex items-center gap-1.5">
                    <input type="radio" name={`approval-scope-${a.id}`} data-testid={`approval-scope-${a.id}-${sc}`} checked={remember === sc} disabled={busy} onChange={() => setRemember(sc)} />
                    {t(`approvals.scope.${sc}`)}
                  </label>
                ))}
              </div>
            )}
            {always && remember === "project" && (
              <label className="ml-5 flex items-center gap-1.5">
                <input type="checkbox" data-testid={`approval-always-any-${a.id}`} checked={anyAgent} disabled={busy} onChange={(e) => setAnyAgent(e.target.checked)} />
                {t("approvals.always.anyAgent")}
              </label>
            )}
            {always && <p className="ml-5 text-mute">{remember === "session" ? t("approvals.scope.sessionHint") : t("approvals.always.hint")}</p>}
          </div>
        )}
        <div className="mt-2.5 flex items-center gap-2">
          <Btn kind="ok" disabled={busy} data-testid={`approve-btn-${a.id}`} onClick={() => decide("approve")}>{approveLabel}</Btn>
          <Btn kind="danger" disabled={busy} onClick={() => decide("reject")}>{t("approvals.reject")}</Btn>
          {err && <span className="text-[11px] text-red-700">{err}</span>}
        </div>
        </>
      ) : (
        <div className={`mt-2 text-[11px] font-semibold ${a.status === "approved" ? "text-emerald-700" : "text-red-700"}`}>
          {a.status === "approved" ? t("approvals.approved") : t("approvals.rejected")}
        </div>
      )}
    </div>
  );
}

/** Floating tray in office mode. Opens automatically when a new approval arrives. */
export function ApprovalsTray() {
  const { t } = useT();
  const approvals = useStore((s) => s.approvals);
  const pending = Object.values(approvals).filter((a) => a.status === "pending").sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at));
  const [open, setOpen] = useState(true);
  const seen = useRef(0);
  useEffect(() => {
    if (pending.length > seen.current) setOpen(true);
    seen.current = pending.length;
  }, [pending.length]);
  if (!pending.length) return null;
  return (
    <div className="pointer-events-auto w-[360px] max-w-[calc(100vw-32px)]">
      <button onClick={() => setOpen(!open)} className="mb-1.5 flex w-full items-center justify-between rounded-md border border-amber-500/50 bg-white px-3 py-2 text-left shadow-lg backdrop-blur">
        <span className="flex items-center gap-2 text-xs font-semibold text-amber-700">
          <span className="relative flex h-2.5 w-2.5"><span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-amber-400 opacity-70" /><span className="relative inline-flex h-2.5 w-2.5 rounded-full bg-amber-400" /></span>
          {t("approvals.pendingTitle", { count: pending.length })}
        </span>
        <span className="text-[10px] text-amber-700/70">{open ? t("common.hide") : t("common.show")}</span>
      </button>
      {open && (
        <div className="max-h-[46dvh] space-y-2 overflow-y-auto rounded-md border border-line bg-panel/95 p-2 shadow-xl backdrop-blur">
          {pending.length > 1 && <a href="/approvals/inbox" data-testid="approvals-open-inbox" className="block rounded-md border border-accent/30 bg-accent/5 px-2 py-1.5 text-center text-[11px] font-semibold text-accent">{t("approvalInbox.open", { count: pending.length })}</a>}
          {pending.map((a) => <ApprovalCard key={a.id} a={a} />)}
        </div>
      )}
    </div>
  );
}
