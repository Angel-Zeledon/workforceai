"use client";
import { useMemo, useState, type ReactNode } from "react";
import { useStore } from "@/lib/store";
import { useT } from "@/lib/i18n";
import { fileKind, useFileViewer } from "@/lib/fileView";
import { ApprovalPending, QuestionPending } from "../../approvals/PendingCard";
import { ApprovalCard } from "../../ApprovalsTray";
import { FileBadge } from "../../files/FileCard";
import { Icon } from "../../icons";
import { Loading, timeAgo, useAgentName } from "../../ui";
import { BlockBoundary } from "./BlockBoundary";
import { RunBlock } from "./RunBlock";
import { HelperProposal, useProposals } from "../../helpers/HelperProposal";
import { inProgress, needsYouItems, readyItems } from "./workspaceLogic";

/** A block of the panel: a title with its number, or a single quiet line when there is nothing in it. */
function Block({ id, title, count, empty, children }: { id: string; title: string; count: number; empty: string; children: ReactNode }) {
  return (
    <section data-testid={`panel-${id}`} data-count={count} aria-label={title} className={count ? "px-3 pt-4" : "px-3 pt-3"}>
      {count > 0 ? (
        <>
          <h3 className="mb-2 flex items-baseline gap-1.5 text-[13px] font-semibold text-ink">
            {title}<span className={`text-[12px] font-medium ${id === "needs" ? "text-warn" : "text-mute"}`}>{count}</span>
          </h3>
          {children}
        </>
      ) : (
        <p className="flex items-baseline gap-1.5 text-[12.5px] text-mute"><span className="font-semibold text-ink2">{title}</span><span>{empty}</span></p>
      )}
    </section>
  );
}

const NEEDS_SHOWN = 2; // the rest is one click away, so what is in motion and what is ready stay in view

function NeedsBlock() {
  const { t } = useT();
  const approvals = useStore((s) => s.approvals);
  const questions = useStore((s) => s.questions);
  const items = useMemo(() => needsYouItems(Object.values(approvals), Object.values(questions ?? {})), [approvals, questions]);
  const props = useProposals();
  const [all, setAll] = useState(false);
  const shown = all ? items : items.slice(0, NEEDS_SHOWN);
  return (
    <Block id="needs" title={t("work.panel.needs")} count={items.length + props.length} empty={t("work.panel.needs.empty")}>
      <ul className="space-y-2">
        {props.map((g) => <li key={g.id}><BlockBoundary name={`helpers-${g.id}`}><HelperProposal group={g} compact /></BlockBoundary></li>)}
        {shown.map((it) => (
          <li key={`${it.kind}:${it.id}`}>
            <BlockBoundary name={`need-${it.id}`}>
              {it.kind === "approval" ? <ApprovalPending a={approvals[it.id]} variant="compact" /> : <QuestionPending q={questions[it.id]} variant="compact" />}
            </BlockBoundary>
          </li>
        ))}
      </ul>
      {items.length > NEEDS_SHOWN && (
        <button type="button" data-testid="needs-more" aria-expanded={all} onClick={() => setAll((v) => !v)}
          className="mt-1.5 min-h-10 w-full rounded-md text-[12.5px] font-semibold text-accent transition hover:bg-accent-soft">
          {all ? t("work.panel.needs.less") : t("work.panel.needs.more", { n: items.length })}
        </button>
      )}
    </Block>
  );
}

function RunningBlock() {
  const { t } = useT();
  const requests = useStore((s) => s.requests);
  const tasks = useStore((s) => s.tasks);
  const rows = useMemo(() => inProgress(requests, Object.values(tasks)), [requests, tasks]);
  return (
    <Block id="running" title={t("work.panel.running")} count={rows.length} empty={t("work.panel.running.empty")}>
      <ul className="space-y-2">
        {rows.map((r, i) => <li key={r.id}><BlockBoundary name={`run-${r.id}`}><RunBlock row={r} collapsible defaultOpen={i === 0} /></BlockBoundary></li>)}
      </ul>
    </Block>
  );
}

function ReadyBlock({ onOpenReport }: { onOpenReport: () => void }) {
  const { t } = useT();
  const chat = useStore((s) => s.chat);
  const reports = useStore((s) => s.reports);
  const open = useFileViewer((s) => s.open);
  const name = useAgentName();
  const items = useMemo(() => readyItems(chat, reports, 6), [chat, reports]);
  return (
    <Block id="ready" title={t("work.panel.ready")} count={items.length} empty={t("work.panel.ready.empty")}>
      <ul className="divide-y divide-line overflow-hidden rounded-[10px] border border-line bg-panel">
        {items.map((it) => (
          <li key={it.key}>
            {it.kind === "file" ? (
              <button type="button" data-testid="ready-file" data-path={it.file.path} onClick={() => open(it.file)} aria-label={t("fileviewer.openFile", { name: it.file.name })}
                className="flex min-h-12 w-full items-center gap-2.5 px-3 py-2 text-left transition hover:bg-panel2">
                <FileBadge kind={fileKind(it.file.name, undefined)} size={30} />
                <span className="min-w-0 flex-1"><span className="block truncate text-[13px] font-medium text-ink">{it.file.name}</span><span className="block truncate text-[11.5px] text-mute">{name(it.from)} · {timeAgo(it.ts)}</span></span>
                <span className="shrink-0 text-[12px] font-semibold text-accent">{t("fileviewer.open")}</span>
              </button>
            ) : (
              <button type="button" data-testid="ready-report" onClick={onOpenReport}
                className="flex min-h-12 w-full items-center gap-2.5 px-3 py-2 text-left transition hover:bg-panel2">
                <span aria-hidden className="flex h-[30px] w-[30px] shrink-0 items-center justify-center rounded-md bg-accent-soft text-accent"><Icon name="doc" size={16} /></span>
                <span className="min-w-0 flex-1"><span className="block truncate text-[13px] font-medium text-ink">{it.title}</span><span className="block truncate text-[11.5px] text-mute">{t("work.panel.report")} · {timeAgo(it.ts)}</span></span>
                <span className="shrink-0 text-[12px] font-semibold text-accent">{t("fileviewer.open")}</span>
              </button>
            )}
          </li>
        ))}
      </ul>
    </Block>
  );
}

/** The decided approvals (the history that used to sit in Resumen): folded, `?section=history` opens it. */
function DecidedBlock({ open }: { open: boolean }) {
  const { t } = useT();
  const approvals = useStore((s) => s.approvals);
  const decided = useMemo(() => Object.values(approvals).filter((a) => a.status !== "pending").sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at)), [approvals]);
  if (!decided.length) return null;
  return (
    <details id="dash-decisions" data-testid="dash-decisions" open={open || undefined} className="group mx-3 mt-4 rounded-[10px] border border-line bg-panel">
      <summary className="flex min-h-11 cursor-pointer list-none items-center justify-between gap-2 px-3 py-2 text-[13px] font-semibold text-ink">
        {t("dash.decisions.title", { count: decided.length })}
        <Icon name="down" size={13} className="shrink-0 text-mute transition-transform group-open:rotate-180" />
      </summary>
      <div className="max-h-[360px] space-y-2 overflow-y-auto border-t border-line p-2.5">{decided.map((a) => <ApprovalCard key={a.id} a={a} compact />)}</div>
    </details>
  );
}

/** Right side: what needs you (answer in place), what is in motion, and what is ready. Each block fails alone. */
export function WorkPanel({ onOpenReport, openDecisions = false, className = "" }: { onOpenReport: () => void; openDecisions?: boolean; className?: string }) {
  const { t } = useT();
  const loaded = useStore((s) => s.loaded);
  return (
    <aside aria-label={t("work.panel.label")} data-testid="work-panel" data-tour="dashboard.side" className={`min-h-0 overflow-y-auto pb-6 ${className}`}>
      {!loaded ? <div className="p-3"><Loading rows={3} /></div> : (
        <>
          <BlockBoundary name="needs"><NeedsBlock /></BlockBoundary>
          <BlockBoundary name="running"><RunningBlock /></BlockBoundary>
          <BlockBoundary name="ready"><ReadyBlock onOpenReport={onOpenReport} /></BlockBoundary>
          <BlockBoundary name="decided"><DecidedBlock open={openDecisions} /></BlockBoundary>
        </>
      )}
    </aside>
  );
}
