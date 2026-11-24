"use client";
import { useEffect, useId, useState } from "react";
import { useStore } from "@/lib/store";
import { useT, fmtUsd } from "@/lib/i18n";
import { helpersApi, useHelpers } from "@/lib/helpers";
import { clampCount, countOptions, rescale } from "@/lib/helpersLogic";
import type { HelperGroup } from "@/lib/types";

/**
 * "Elena propone repartirlo entre 8 ayudantes": who proposes, how many, what each does, the estimated cost and the cap.
 * Nothing is launched until the person accepts; "Que lo haga Elena" declines and the normal plan goes on.
 * Used in the conversation and in "Te necesitan" of the right panel.
 */
export function HelperProposal({ group, compact = false }: { group: HelperGroup; compact?: boolean }) {
  const { t } = useT();
  const id = useId();
  const owner = useStore((s) => s.agents[group.owner_agent_id]);
  const first = (owner?.name ?? "").split(" ")[0] || t("common.system");
  const [count, setCount] = useState(group.count);
  const [busy, setBusy] = useState<"" | "launch" | "decline">("");
  const [err, setErr] = useState(false);
  useEffect(() => { setCount(group.count); }, [group.count]);
  const n = clampCount(count, group);
  const scaled = n === group.count ? { estimate: group.estimate, cap_usd: group.cap_usd } : rescale(group, n);
  const options = countOptions(group);
  const act = async (kind: "launch" | "decline") => {
    if (busy) return; // a double click launches once
    setBusy(kind); setErr(false);
    try {
      const r = kind === "launch" ? await helpersApi.launch(group.request_id, n, scaled.cap_usd) : await helpersApi.decline(group.request_id);
      useHelpers.getState().upsert(r?.group);
    } catch { setErr(true); setBusy(""); }
  };
  const share = Math.ceil(group.parts.length / n);
  return (
    <section data-testid="helper-proposal" data-group={group.id} aria-labelledby={`${id}-t`} className="rounded-[10px] border border-line bg-panel p-3">
      <h4 id={`${id}-t`} className="text-[13.5px] font-semibold leading-snug text-ink">{t("helpers.proposal.title", { name: first, n })}</h4>
      <p className="mt-1 text-[12.5px] leading-snug text-ink2">
        {group.mode === "free" ? t("helpers.proposal.body.free", { name: first, share }) : t("helpers.proposal.body", { name: first, share })}
      </p>
      {group.parts.length > 0 && !compact && (
        <details className="group mt-1.5">
          <summary className="flex min-h-9 cursor-pointer list-none items-center gap-1 text-[12px] font-medium text-accent">{t("helpers.proposal.what")}</summary>
          <ol className="mt-1 max-h-40 space-y-0.5 overflow-y-auto pl-4 text-[12px] text-ink2" style={{ listStyle: "decimal" }}>
            {group.parts.slice(0, 50).map((p) => <li key={p.index} className="break-words">{p.label}</li>)}
          </ol>
        </details>
      )}
      <p className="mt-2 text-[12.5px] text-ink" data-testid="helper-cost">
        {t("helpers.proposal.cost", { min: fmtUsd(scaled.estimate.min_usd), max: fmtUsd(scaled.estimate.max_usd) })}
        <span className="text-mute"> · {t("helpers.proposal.cap", { cap: fmtUsd(scaled.cap_usd) })}</span>
      </p>
      <p className="mt-0.5 text-[11.5px] text-mute">{t("helpers.proposal.capHint")}</p>
      <div className="mt-2.5 flex flex-wrap items-center gap-2">
        <button type="button" data-testid="helper-launch" disabled={!!busy} onClick={() => act("launch")}
          className="min-h-11 rounded-lg bg-accent px-3.5 text-[13px] font-semibold text-white transition hover:opacity-90 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-60 sm:min-h-10">
          {busy === "launch" ? t("helpers.proposal.launching") : t("helpers.proposal.launch", { n })}
        </button>
        <button type="button" data-testid="helper-decline" disabled={!!busy} onClick={() => act("decline")}
          className="min-h-11 rounded-lg border border-line bg-panel px-3 text-[13px] font-semibold text-ink transition hover:border-line-strong hover:bg-panel2 disabled:opacity-60 sm:min-h-10">
          {t("helpers.proposal.decline", { name: first })}
        </button>
        <label className="flex items-center gap-1.5 text-[12.5px] text-ink2">
          <span className="sr-only sm:not-sr-only">{t("helpers.proposal.change")}</span>
          <select data-testid="helper-count" value={n} disabled={!!busy} aria-label={t("helpers.proposal.count")} onChange={(e) => setCount(Number(e.target.value))}
            className="h-11 rounded-lg border border-line bg-panel px-2 text-[13px] font-semibold text-ink outline-none focus:border-accent sm:h-10">
            {options.map((o) => <option key={o} value={o}>{o}</option>)}
          </select>
        </label>
      </div>
      {group.max_count > 0 && <p className="mt-1.5 text-[11.5px] text-mute">{t("helpers.proposal.max", { n: group.max_count })}</p>}
      {err && <p role="alert" className="mt-1.5 text-[12px] text-err">{t("helpers.proposal.error")}</p>}
    </section>
  );
}

/** The proposals waiting for an answer, newest request last. */
export function useProposals(): HelperGroup[] {
  const enabled = useHelpers((s) => s.enabled);
  const groups = useHelpers((s) => s.groups);
  if (!enabled) return NONE;
  const list = Object.values(groups).filter((g) => g.status === "proposed");
  return list.length ? list : NONE;
}
const NONE: HelperGroup[] = [];
