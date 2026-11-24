"use client";
import { useState } from "react";
import { useStore } from "@/lib/store";
import { useT, fmtUsd } from "@/lib/i18n";
import { helpersApi, useHelpers } from "@/lib/helpers";
import { countParts, isActiveGroup, partKind, spendOf, visibleParts, type PartKind } from "@/lib/helpersLogic";
import { Icon } from "../icons";
import type { HelperGroup, HelperPart } from "@/lib/types";

function Mark({ kind }: { kind: PartKind }) {
  if (kind === "done") return <span aria-hidden className="flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-ok text-white"><Icon name="check" size={10} strokeWidth={3} /></span>;
  if (kind === "failed") return <span aria-hidden className="flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-err text-xs font-bold leading-none text-white">!</span>;
  if (kind === "active") return <span aria-hidden className="relative flex h-4 w-4 shrink-0 items-center justify-center"><span className="absolute h-4 w-4 rounded-full bg-accent/20 motion-safe:animate-pulse" /><span className="h-2 w-2 rounded-full bg-accent" /></span>;
  if (kind === "stopped") return <span aria-hidden className="flex h-4 w-4 shrink-0 items-center justify-center"><span className="h-0.5 w-2.5 rounded bg-mute" /></span>;
  return <span aria-hidden className="flex h-4 w-4 shrink-0 items-center justify-center"><span className="h-2 w-2 rounded-full border border-line-strong bg-panel" /></span>;
}

function PartRow({ g, p, busy, run }: { g: HelperGroup; p: HelperPart; busy: boolean; run: (fn: () => Promise<unknown>) => void }) {
  const { t } = useT();
  const task = useStore((s) => (p.task_id ? s.tasks[p.task_id] : undefined));
  const [open, setOpen] = useState(false);
  const kind = partKind(p.status, g.status);
  const out = task?.output;
  return (
    <li data-testid={`helper-part-${p.index}`} data-kind={kind} className="text-[12.5px] leading-snug">
      <div className="flex items-start gap-2">
        <span className="mt-0.5"><Mark kind={kind} /></span>
        <span className="min-w-0 flex-1">
          <span className={`break-words ${kind === "done" ? "text-ink2" : "text-ink"}`}>{p.label}</span>
          <span className={`ml-1.5 text-[11.5px] ${kind === "failed" ? "font-medium text-err" : "text-mute"}`}>{t(`helpers.state.${kind}`)}</span>
        </span>
        <span className="flex shrink-0 items-center gap-0.5">
          {kind === "done" && out && (
            <button type="button" aria-expanded={open} aria-label={t("helpers.part.openAria", { label: p.label })} onClick={() => setOpen((v) => !v)}
              className="min-h-9 rounded-md px-2 text-[12px] font-semibold text-accent hover:bg-accent-soft md:min-h-8">{t("helpers.part.open")}</button>
          )}
          {(kind === "failed" || kind === "stopped") && isActiveGroupOrOpen(g) && (
            <button type="button" disabled={busy} data-testid={`helper-retry-${p.index}`} aria-label={t("helpers.part.retryAria", { label: p.label })} onClick={() => run(() => helpersApi.retry(g.request_id, p.index))}
              className="min-h-9 rounded-md px-2 text-[12px] font-semibold text-accent hover:bg-accent-soft disabled:opacity-60 md:min-h-8">{t("helpers.part.retry")}</button>
          )}
          {(kind === "active" || kind === "queued") && isActiveGroup(g) && (
            <button type="button" disabled={busy} aria-label={t("helpers.part.stopAria", { label: p.label })} onClick={() => run(() => helpersApi.stop(g.request_id, p.index))}
              className="min-h-9 rounded-md px-2 text-[12px] font-medium text-ink2 hover:bg-panel2 disabled:opacity-60 md:min-h-8">{t("helpers.part.stop")}</button>
          )}
        </span>
      </div>
      {open && out && (
        <p className="ml-6 mt-1 rounded-md bg-panel2 px-2 py-1.5 text-[12px] text-ink2">{out.summary}</p>
      )}
    </li>
  );
}
// a failed or stopped part can be retried while the group is not closed for good (done / declined)
const isActiveGroupOrOpen = (g: HelperGroup) => g.status !== "done" && g.status !== "declined";

/** "Elena + 8 ayudantes · 6 de 8": the parts with their state, retry / stop, the join step and the spend against the cap. */
export function HelperProgress({ group: g }: { group: HelperGroup }) {
  const { t } = useT();
  const owner = useStore((s) => s.agents[g.owner_agent_id]);
  const first = (owner?.name ?? "").split(" ")[0] || t("common.system");
  const [expanded, setExpanded] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState(false);
  const c = countParts(g);
  const { shown, hidden } = visibleParts(g, expanded);
  const spend = spendOf(g);
  const active = isActiveGroup(g);
  const run = async (fn: () => Promise<{ group?: HelperGroup } | unknown>) => {
    if (busy) return;
    setBusy(true); setErr(false);
    try { const r = (await fn()) as { group?: HelperGroup } | undefined; useHelpers.getState().upsert(r?.group); } catch { setErr(true); }
    setBusy(false);
  };
  const tail = g.status === "joining" ? t("helpers.run.joining", { name: first })
    : g.status === "done" ? t("helpers.run.done", { name: first })
    : g.status === "stopped" ? t("helpers.run.stopped", { name: first })
    : t("helpers.run.after", { name: first });
  return (
    <section data-testid="helper-progress" data-group={g.id} data-status={g.status} aria-label={t("helpers.run.label")} className="border-t border-line px-3 pb-3 pt-2.5">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5">
        <p className="text-[12.5px] font-semibold text-ink" data-testid="helper-title">{t("helpers.run.title", { name: first, n: g.count })}</p>
        <p role="status" aria-live="polite" className="text-[12px] text-ink2" data-testid="helper-count-line">
          {t("helpers.run.progress", { done: c.done, total: c.total })}{c.failed > 0 && <span className="font-medium text-err"> · {t("helpers.run.failedN", { n: c.failed })}</span>}
        </p>
      </div>
      <ul className="mt-2 space-y-1.5">
        {shown.map((p) => <PartRow key={p.index} g={g} p={p} busy={busy} run={run} />)}
      </ul>
      {(hidden > 0 || expanded) && (
        <button type="button" aria-expanded={expanded} onClick={() => setExpanded((v) => !v)} className="mt-1.5 min-h-10 text-[12px] font-semibold text-accent hover:underline">
          {expanded ? t("helpers.run.fold") : t("helpers.run.showAll", { n: g.parts.length, hidden })}
        </button>
      )}
      <div className="mt-2 flex flex-wrap items-center justify-between gap-2">
        <p className="min-w-0 text-[12px] text-ink2">{tail}</p>
        {active && !confirm && (
          <button type="button" data-testid="helper-stop-all" disabled={busy} onClick={() => setConfirm(true)}
            className="min-h-10 rounded-lg border border-line px-3 text-[12.5px] font-semibold text-ink hover:border-line-strong hover:bg-panel2 disabled:opacity-60">{t("helpers.run.stopAll")}</button>
        )}
      </div>
      {confirm && (
        <div role="alertdialog" aria-label={t("helpers.run.stopAll")} data-testid="helper-stop-confirm" className="mt-2 rounded-lg border border-line bg-panel2 p-2.5">
          <p className="text-[12.5px] text-ink">{t("helpers.run.stopConfirm", { name: first })}</p>
          <div className="mt-2 flex gap-2">
            <button type="button" autoFocus data-testid="helper-stop-yes" disabled={busy} onClick={() => { void run(() => helpersApi.stop(g.request_id)); setConfirm(false); }}
              className="min-h-10 rounded-lg bg-accent px-3 text-[12.5px] font-semibold text-white hover:opacity-90 disabled:opacity-60">{t("helpers.run.stopYes")}</button>
            <button type="button" onClick={() => setConfirm(false)} onKeyDown={(e) => { if (e.key === "Escape") setConfirm(false); }}
              className="min-h-10 rounded-lg border border-line bg-panel px-3 text-[12.5px] font-semibold text-ink hover:bg-panel2">{t("helpers.run.stopNo")}</button>
          </div>
        </div>
      )}
      {g.cap_usd > 0 && (
        <div className="mt-2">
          <div className="flex items-baseline justify-between text-[11.5px] text-mute">
            <span>{t("helpers.run.spend", { spent: fmtUsd(g.spent_usd), cap: fmtUsd(g.cap_usd) })}</span>
          </div>
          <div className="mt-1 h-1 overflow-hidden rounded-full bg-line" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={spend.pct} aria-label={t("helpers.run.spendAria")}>
            <div className={`h-full rounded-full transition-[width] duration-500 ${spend.reached ? "bg-warn" : "bg-accent"}`} style={{ width: `${spend.pct}%` }} />
          </div>
          {spend.reached && <p className="mt-1 text-[11.5px] text-warn">{t("helpers.run.capReached")}</p>}
        </div>
      )}
      {err && <p role="alert" className="mt-1.5 text-[12px] text-err">{t("helpers.proposal.error")}</p>}
    </section>
  );
}
