"use client";
import { useState } from "react";
import { trOr, useT } from "@/lib/i18n";
import { useProjects } from "@/lib/projects/store";
import type { ProjectApproval } from "@/lib/projects/types";
import { Btn, Card, Empty, RiskBadge } from "../ui";
import { Modal } from "./shared";

const RISK_RANK = { low: 0, medium: 1, high: 2 } as const;

/** Pending approvals grouped by action; one confirmed click resolves a whole group (high risk requires typing the confirmation). */
export function ApprovalsBatch() {
  const { t } = useT();
  const approvals = useProjects((s) => s.detail!.approvals);
  const decide = useProjects((s) => s.decide);
  const decideBatch = useProjects((s) => s.decideBatch);
  const [confirm, setConfirm] = useState<{ action: string; decision: "approve" | "reject"; items: ProjectApproval[] } | null>(null);
  const [typed, setTyped] = useState("");
  const [err, setErr] = useState("");
  const pending = approvals.filter((a) => a.status === "pending");
  const groups = new Map<string, ProjectApproval[]>();
  for (const a of pending) groups.set(a.action, [...(groups.get(a.action) ?? []), a]);
  const resolved = approvals.filter((a) => a.status !== "pending").slice(-8).reverse();
  const high = confirm?.items.some((a) => a.risk === "high") ?? false;
  const expected = confirm ? t("pv.batch.typedText", { count: confirm.items.length }) : "";
  const canConfirm = !!confirm && (!high || typed.trim().toUpperCase() === expected.toUpperCase());
  const run = async () => {
    if (!confirm) return;
    setErr("");
    try { await decideBatch({ action: confirm.action, decision: confirm.decision, expected_count: confirm.items.length, include_high: high }); setConfirm(null); setTyped(""); }
    catch { setErr(t("pv.batch.conflict")); }
  };

  return (
    <div className="space-y-4">
      {groups.size === 0 && <Empty>{t("pv.batch.none")}</Empty>}
      {Array.from(groups, ([action, items]) => {
        const risk = items.reduce<ProjectApproval["risk"]>((m, a) => (RISK_RANK[a.risk] > RISK_RANK[m] ? a.risk : m), "low");
        return (
          <Card key={action} title={<span data-testid={`approval-group-${action}`} data-count={items.length}>{trOr(`pv.group.${action}`, action)} · {t("pv.batch.count", { count: items.length })}</span>} right={<RiskBadge risk={risk} />}>
            <ul className="space-y-1 text-[12px] text-ink">
              {items.slice(0, 4).map((a) => (
                <li key={a.id} data-testid={`project-approval-${a.id}`} className="flex items-center justify-between gap-2 rounded-xl bg-panel2 px-3 py-1.5">
                  <span className="truncate">{a.title}</span>
                  <span className="flex shrink-0 gap-1.5">
                    <Btn kind="ok" onClick={() => decide(a.id, "approve")}>{t("pv.approve")}</Btn>
                    <Btn kind="danger" onClick={() => decide(a.id, "reject")}>{t("pv.reject")}</Btn>
                  </span>
                </li>
              ))}
              {items.length > 4 && <li className="px-1 text-[11px] text-mute">{t("pv.batch.more", { count: items.length - 4 })}</li>}
            </ul>
            {items.length > 1 && (
              <div className="mt-3 flex gap-2">
                <Btn data-testid="approvals-batch-approve" kind="ok" onClick={() => { setConfirm({ action, decision: "approve", items }); setTyped(""); setErr(""); }}>{t("pv.batch.approveAll", { count: items.length })}</Btn>
                <Btn data-testid="approvals-batch-reject" kind="danger" onClick={() => { setConfirm({ action, decision: "reject", items }); setTyped(""); setErr(""); }}>{t("pv.batch.rejectAll", { count: items.length })}</Btn>
              </div>
            )}
          </Card>
        );
      })}
      {resolved.length > 0 && (
        <Card title={t("pv.batch.history")}>
          <ul className="space-y-1 text-[11px] text-mute">
            {resolved.map((a) => <li key={a.id} className="flex justify-between gap-2"><span className="truncate">{a.title}</span><span className={a.status === "approved" ? "font-semibold text-emerald-700" : "font-semibold text-red-700"}>{t(`pv.approval.${a.status}`)}</span></li>)}
          </ul>
        </Card>
      )}
      {confirm && (
        <Modal onClose={() => setConfirm(null)} testId="approvals-batch-confirm">
          <h3 className="font-display text-lg font-semibold text-ink">{t(confirm.decision === "approve" ? "pv.batch.confirmApprove" : "pv.batch.confirmReject", { count: confirm.items.length })}</h3>
          <ul className="mt-2 max-h-40 space-y-1 overflow-y-auto text-[12px] text-mute">{confirm.items.map((a) => <li key={a.id}>• {a.title}</li>)}</ul>
          {high && (
            <label className="mt-3 block text-[11px] font-semibold text-red-700">{t("pv.batch.typePrompt", { text: expected })}
              <input data-testid="approvals-batch-typed-confirm" value={typed} onChange={(e) => setTyped(e.target.value)} className="mt-1 w-full rounded-xl border border-line-strong bg-panel px-3 py-2 text-sm text-ink outline-none focus:border-red-400" />
            </label>
          )}
          {err && <div className="mt-2 text-[11px] text-red-700">{err}</div>}
          <div className="mt-4 flex justify-end gap-2">
            <Btn onClick={() => setConfirm(null)}>{t("common.close")}</Btn>
            <Btn data-testid="approvals-batch-confirm-go" kind={confirm.decision === "approve" ? "ok" : "danger"} disabled={!canConfirm} onClick={run}>{t("pv.batch.confirm")}</Btn>
          </div>
        </Modal>
      )}
    </div>
  );
}
