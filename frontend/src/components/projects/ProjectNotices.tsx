"use client";
import Link from "next/link";
import { useEffect, type ReactNode } from "react";
import { useT } from "@/lib/i18n";
import { useProjects } from "@/lib/projects/store";
import { Icon } from "../icons";

/** One amber notice above Seguimiento: what waits, one line of why, and the button that opens it. */
function Notice({ testid, icon, title, body, children }: { testid: string; icon: "bell" | "layers"; title: ReactNode; body: string; children: ReactNode }) {
  return (
    <div data-testid={testid} role="status" className="flex flex-col gap-2 rounded-[10px] border border-amber-500/30 bg-amber-500/[0.07] px-3 py-2.5 sm:flex-row sm:items-center sm:gap-3">
      <div className="flex min-w-0 flex-1 items-start gap-2.5">
        <span aria-hidden className="mt-0.5 inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-amber-500/15 text-amber-700"><Icon name={icon} size={13} /></span>
        <div className="min-w-0">
          <div className="text-[13px] font-semibold text-ink">{title}</div>
          <div className="text-[11px] text-mute">{body}</div>
        </div>
      </div>
      <div className="flex shrink-0 flex-wrap items-center gap-2 pl-[34px] sm:pl-0">{children}</div>
    </div>
  );
}

const btn = "inline-flex min-h-10 items-center rounded-md border px-3 py-1.5 text-xs font-semibold transition sm:min-h-0";

/**
 * What used to be its own tab ("Cambios de plan") and the project's pending approvals, as notices on top of
 * Seguimiento: they only show when something waits, and their button opens it (Plan → Historial de cambios, Permisos).
 * The plan changes are loaded here too (not only in Historial de cambios), when the project opens, starts, changes
 * state or its plan changes, so the notice can tell about them before anyone opens that view.
 */
export function ProjectNotices() {
  const { t } = useT();
  const setTab = useProjects((s) => s.setTab);
  const loadChanges = useProjects((s) => s.loadChanges);
  const activeId = useProjects((s) => s.activeId);
  const status = useProjects((s) => s.detail?.project.status);
  const version = useProjects((s) => s.detail?.structure_version);
  useEffect(() => { if (activeId && status && status !== "draft" && status !== "planning") void loadChanges(); }, [loadChanges, activeId, status, version]);
  const changes = useProjects((s) => s.changes.filter((c) => c.status === "pending").length);
  const approvals = useProjects((s) => s.detail?.approvals.filter((a) => a.status === "pending").length ?? 0);
  if (!changes && !approvals) return null;
  return (
    <div className="space-y-2">
      {approvals > 0 && (
        <Notice testid="pv-pending-notice" icon="bell" title={t("pv.notice.pending", { count: approvals })} body={t("pv.notice.pendingBody")}>
          <button type="button" data-testid="pv-pending-open" onClick={() => setTab("approvals")} className={`${btn} border-accent bg-accent text-white hover:bg-accent-hover`}>{t("pv.notice.pendingOpen")}</button>
          <Link href="/approvals" data-testid="pv-pending-all" className={`${btn} border-line-strong bg-panel text-ink hover:bg-panel2`}>{t("pv.notice.pendingAll")}</Link>
        </Notice>
      )}
      {changes > 0 && (
        <Notice testid="pv-changes-notice" icon="layers" title={t("pv.notice.changes", { count: changes })} body={t("pv.notice.changesBody")}>
          <button type="button" data-testid="pv-changes-open" onClick={() => setTab("changes")} className={`${btn} border-accent bg-accent text-white hover:bg-accent-hover`}>{t("pv.notice.changesOpen")}</button>
        </Notice>
      )}
    </div>
  );
}
