"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { useAgentName } from "../../ui";
import { Icon } from "../../icons";
import type { RunRow, StepKind } from "./workspaceLogic";

function StepMark({ kind }: { kind: StepKind }) {
  if (kind === "done") return <span aria-hidden className="flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-ok text-white"><Icon name="check" size={10} strokeWidth={3} /></span>;
  if (kind === "failed") return <span aria-hidden className="flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-err text-white"><Icon name="x" size={10} strokeWidth={3} /></span>;
  if (kind === "active") return <span aria-hidden className="relative flex h-4 w-4 shrink-0 items-center justify-center"><span className="absolute h-4 w-4 rounded-full bg-accent/20 motion-safe:animate-pulse" /><span className="h-2 w-2 rounded-full bg-accent" /></span>;
  if (kind === "waiting") return <span aria-hidden className="flex h-4 w-4 shrink-0 items-center justify-center"><span className="h-2.5 w-2.5 rounded-full border-2 border-warn bg-panel" /></span>;
  return <span aria-hidden className="flex h-4 w-4 shrink-0 items-center justify-center"><span className="h-2 w-2 rounded-full border border-line-strong bg-panel" /></span>;
}

/** A request in motion: what was asked, a thin progress line and its steps with who does each, live from the store. */
export function RunBlock({ row, collapsible = false, defaultOpen = true }: { row: RunRow; collapsible?: boolean; defaultOpen?: boolean }) {
  const { t } = useT();
  const name = useAgentName();
  const [open, setOpen] = useState(defaultOpen);
  const shown = !collapsible || open;
  const waiting = row.steps.some((s) => s.kind === "waiting");
  const status = row.status === "planning" && row.total === 0 ? t("work.run.planning")
    : row.status === "paused" ? t("work.run.paused")
    : waiting ? t("work.run.waitingYou")
    : row.total ? t("work.run.progress", { done: row.done, total: row.total }) : t("work.run.starting");
  const head = (
    <>
      <span className="min-w-0 flex-1">
        <span className="line-clamp-2 block break-words text-[13px] font-semibold leading-snug text-ink">{row.text}</span>
        <span className={`mt-0.5 block text-[12px] ${waiting ? "font-medium text-warn" : "text-ink2"}`}>{status}</span>
      </span>
      {collapsible && <Icon name="down" size={14} className={`mt-1 shrink-0 text-mute transition-transform ${open ? "rotate-180" : ""}`} />}
    </>
  );
  return (
    <article data-testid={`run-${row.id}`} data-status={row.status} aria-label={row.text} className="rounded-[10px] border border-line bg-panel">
      {collapsible ? (
        <button type="button" aria-expanded={open} onClick={() => setOpen((o) => !o)} className="flex min-h-11 w-full items-start gap-2 rounded-[10px] px-3 py-2.5 text-left hover:bg-panel2">{head}</button>
      ) : <div className="flex items-start gap-2 px-3 pt-2.5">{head}</div>}
      <div className="mx-3 mt-2 h-1 overflow-hidden rounded-full bg-line" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={row.pct} aria-label={t("work.run.progressAria")}>
        <div className="h-full rounded-full bg-accent transition-[width] duration-500" style={{ width: `${row.pct}%` }} />
      </div>
      {shown && row.steps.length > 0 && (
        <ol className="space-y-1.5 px-3 pb-3 pt-2.5">
          {row.steps.map((s) => (
            <li key={s.id} data-testid={`run-step-${s.id}`} data-kind={s.kind} className="flex items-start gap-2">
              <span className="mt-0.5"><StepMark kind={s.kind} /></span>
              <span className="min-w-0 flex-1 text-[12.5px] leading-snug">
                <span className={`break-words ${s.kind === "done" ? "text-mute" : s.kind === "queued" ? "text-ink2" : "text-ink"}`}>{s.title}</span>
                <span className="block text-[11.5px] text-mute">{name(s.agent_id)}{s.kind === "waiting" ? ` · ${t("work.run.step.waiting")}` : s.kind === "failed" ? ` · ${t("work.run.step.failed")}` : ""}</span>
              </span>
            </li>
          ))}
        </ol>
      )}
      {!shown || row.steps.length === 0 ? <div className="h-2.5" /> : null}
    </article>
  );
}
