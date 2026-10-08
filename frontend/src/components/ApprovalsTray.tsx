"use client";
import { useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";
import { useStore } from "@/lib/store";
import type { Approval } from "@/lib/types";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { Btn, Dot, RiskBadge, useAgentColor, useAgentName } from "./ui";

export function ApprovalCard({ a, compact = false }: { a: Approval; compact?: boolean }) {
  const { t } = useT();
  const name = useAgentName();
  const color = useAgentColor();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const canDecide = useCan("approvals:decide");
  const decide = async (d: "approve" | "reject") => {
    setBusy(true); setErr("");
    try { await api.decide(a.id, d); } catch { setErr(t("approvals.decideError")); setBusy(false); }
  };
  const pending = a.status === "pending";
  return (
    <div data-testid={pending ? `approval-${a.id}` : undefined} className="rounded-md border border-line bg-panel2 p-3">
      <div className="mb-1 flex items-center justify-between gap-2">
        <span className="flex items-center gap-1.5 text-[11px] text-mute"><Dot color={color(a.agent_id)} />{name(a.agent_id)} · <span className="font-mono">{a.action}</span></span>
        <RiskBadge risk={a.risk} />
      </div>
      <div className="text-[13px] font-semibold text-ink">{a.title}</div>
      {!compact && a.details && <p className="mt-1 text-[12px] leading-snug text-ink2">{a.details}</p>}
      {pending && !canDecide ? (
        <div data-testid="approval-no-permission" className="mt-2 text-[11px] text-mute">{t("approvals.noPermission")}</div>
      ) : pending ? (
        <div className="mt-2.5 flex items-center gap-2">
          <Btn kind="ok" disabled={busy} onClick={() => decide("approve")}>{t("approvals.approve")}</Btn>
          <Btn kind="danger" disabled={busy} onClick={() => decide("reject")}>{t("approvals.reject")}</Btn>
          {err && <span className="text-[11px] text-red-700">{err}</span>}
        </div>
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
        <div className="max-h-[46vh] space-y-2 overflow-y-auto rounded-md border border-line bg-panel/95 p-2 shadow-xl backdrop-blur">
          {pending.map((a) => <ApprovalCard key={a.id} a={a} />)}
        </div>
      )}
    </div>
  );
}
