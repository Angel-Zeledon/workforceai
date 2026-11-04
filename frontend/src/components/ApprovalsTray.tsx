"use client";
import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useStore } from "@/lib/store";
import { pendingTotal } from "@/lib/inboxLogic";
import type { Approval } from "@/lib/types";
import { useT } from "@/lib/i18n";
import { ApprovalPending, QuestionPending } from "./approvals/PendingCard";
import { RememberNotice } from "./approvals/RememberApproval";

/** An approval drawn by the one pending card (`approvals/PendingCard`). `scope="office"` is the card over the approvals
 * desk in the 3D office: same decision call and permission checks, different test ids so the tray's `approval-<id>`
 * selectors stay unique. Kept under this name for the screens that import it (office desk, dashboard). */
export function ApprovalCard({ a, compact = false, scope = "tray" }: { a: Approval; compact?: boolean; scope?: "tray" | "office" | "away" }) {
  return <ApprovalPending a={a} variant={compact ? "compact" : "full"} scope={scope} />;
}

/** Floating tray in office mode ("Pendientes"): the questions agents asked (needs your help) first, then the
 * approvals, both as the compact pending card. Opens automatically when something new arrives. */
export function ApprovalsTray() {
  const { t } = useT();
  const approvals = useStore((s) => s.approvals);
  const questionsById = useStore((s) => s.questions);
  const pendingApprovals = Object.values(approvals).filter((a) => a.status === "pending").sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at));
  const questions = Object.values(questionsById).filter((q) => q.status === "open").sort((a, b) => +new Date(a.created_at) - +new Date(b.created_at));
  const total = pendingTotal(approvals, questionsById); // the same number as the menu's Pendientes
  const [open, setOpen] = useState(true);
  const seen = useRef(0);
  useEffect(() => {
    if (total > seen.current) setOpen(true);
    seen.current = total;
  }, [total]);
  if (!total) return null;
  const pending = pendingApprovals;
  return (
    <div className="pointer-events-auto w-full lg:w-[360px] lg:max-w-[calc(100vw-32px)]">
      <button onClick={() => setOpen(!open)} className="mb-1.5 flex min-h-10 w-full items-center justify-between gap-2 rounded-md border border-amber-500/50 bg-white px-3 py-2 text-left shadow-lg backdrop-blur lg:min-h-0">
        <span className="flex items-center gap-2 text-xs font-semibold text-amber-700">
          <span className="relative flex h-2.5 w-2.5"><span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-amber-400 opacity-70" /><span className="relative inline-flex h-2.5 w-2.5 rounded-full bg-amber-400" /></span>
          {questions.length ? t("questions.trayTitle", { count: total, help: questions.length }) : t("approvals.pendingTitle", { count: pending.length })}
        </span>
        <span className="text-[10px] text-amber-700/70">{open ? t("common.hide") : t("common.show")}</span>
      </button>
      {open && (
        <div className="max-h-[30dvh] space-y-2 overflow-y-auto overscroll-contain lg:max-h-[46dvh] rounded-md border border-line bg-panel/95 p-2 shadow-xl backdrop-blur">
          {(pending.length > 1 || questions.length > 1) && <Link href="/approvals/inbox" data-testid="approvals-open-inbox" className="block rounded-md border border-accent/30 bg-accent/5 px-2 py-1.5 text-center text-[11px] font-semibold text-accent">{t("approvalInbox.open", { count: total })}</Link>}
          <RememberNotice />
          {questions.map((q) => <QuestionPending key={q.id} q={q} variant="compact" />)}
          {pending.map((a) => <ApprovalPending key={a.id} a={a} variant="compact" />)}
        </div>
      )}
    </div>
  );
}
