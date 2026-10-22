"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { trustApi, type AgentTrust, type Proposal, type TrustMethodology } from "@/lib/trustApi";
import { daysUntil, progressPct, rungs, thresholdText, valueText } from "@/lib/trustLogic";
import { Card } from "../ui";
import { useViewer } from "../connections/shared";

const STATUS_COLOR = { met: "#2f7d55", unmet: "#b4443c", not_available: "#5b6678" } as const;

/** "How does it work?": the thresholds exactly as the API returned them. */
function TrustMethodologyPopover({ m }: { m: TrustMethodology }) {
  const { t } = useT();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(false); };
    const onDown = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false); };
    document.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onDown);
    return () => { document.removeEventListener("keydown", onKey); document.removeEventListener("mousedown", onDown); };
  }, [open]);
  return (
    <div ref={ref} className="relative inline-block">
      <button type="button" data-testid="trust-methodology-open" aria-expanded={open} aria-haspopup="dialog" onClick={() => setOpen((v) => !v)}
        className="text-[11px] font-medium text-accent hover:underline">{t("trust.methodology.button")}</button>
      {open && (
        <div role="dialog" aria-label={t("trust.methodology.title")} data-testid="trust-methodology"
          className="absolute right-0 z-30 mt-1 max-h-[70vh] w-[min(340px,86vw)] overflow-y-auto rounded-xl border border-line bg-panel p-3 text-left shadow-float">
          <div className="text-[12px] font-semibold text-ink">{t("trust.methodology.title")}</div>
          <p className="mt-1 text-[11px] leading-snug text-ink2">{t("trust.methodology.intro", { days: m.probation_days, n: m.probation_decisions })}</p>
          <div className="mt-2 text-[11px] font-semibold text-ink">{t("trust.methodology.promotion")}</div>
          <ul className="mt-1 space-y-0.5 text-[11px] text-ink2">
            {m.promotion.map((c) => <li key={c.key}>{t(`trust.criterion.${c.key}`)} {thresholdText(c.key, c.op, c.threshold)}</li>)}
          </ul>
          <div className="mt-2 text-[11px] font-semibold text-ink">{t("trust.methodology.demotion")}</div>
          <ul className="mt-1 space-y-0.5 text-[11px] text-ink2">
            {m.demotion.map((c) => (
              <li key={c.key}>{t(`trust.trigger.${c.key}`)} {thresholdText(c.key, c.op, c.threshold)} · {t(c.auto ? "trust.methodology.auto" : "trust.methodology.suggested")}</li>
            ))}
          </ul>
          <ul className="mt-2 space-y-1 border-t border-line pt-2 text-[10.5px] text-mute">
            <li>{t("trust.methodology.window", { n: m.window_days, h: m.burst_hours })}</li>
            <li>{t("trust.methodology.human")}</li>
            <li>{t("trust.methodology.why_auto", { n: m.revert_days })}</li>
            <li>{t("trust.methodology.cooldown", { n: m.decline_cooldown_days })}</li>
          </ul>
        </div>
      )}
    </div>
  );
}

function errKey(e: unknown): string {
  const m = /-> (\d{3})/.exec(e instanceof Error ? e.message : "");
  return m?.[1] === "403" ? "trust.error.forbidden" : m?.[1] === "409" ? "trust.error.conflict" : "trust.error.action";
}

function Reasons({ p }: { p: Proposal }) {
  const { t } = useT();
  return <span>{p.reasons.map((r) => (r === "criteria_met" ? t("trust.reason.criteria_met") : t(`trust.trigger.${r}`))).join(" · ")}</span>;
}

/** Trust ladder card of the agent profile (B1): level, ceiling, probation, progress, and the human decision. */
export function TrustCard({ id }: { id: string }) {
  const { t, locale } = useT();
  const { isAdmin } = useViewer();
  const tick = useStore((s) => s.trustTick[id] ?? 0);
  const [data, setData] = useState<{ m: TrustMethodology; a: AgentTrust } | null>(null);
  const [err, setErr] = useState(false);
  const [busy, setBusy] = useState(false);
  const [actionErr, setActionErr] = useState("");

  const load = useCallback(async () => {
    try { const r = await trustApi.agent(id); setData({ m: r.methodology, a: r.agent }); setErr(false); } catch { setErr(true); }
  }, [id]);
  useEffect(() => { setData(null); setActionErr(""); }, [id]);
  useEffect(() => { load(); }, [load, tick]);

  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true); setActionErr("");
    try { await fn(); } catch (e) { setActionErr(errKey(e)); }
    setBusy(false);
    load();
  };

  const a = data?.a;
  const date = (iso?: string) => (iso ? new Date(iso).toLocaleDateString(locale) : "");
  const btn = "min-h-[36px] rounded-lg px-3 text-[12px] font-semibold disabled:opacity-50";
  return (
    <Card title={t("trust.card.title")} right={data && <TrustMethodologyPopover m={data.m} />}>
      <div data-testid="trust-card">
        {!data || !a ? <div className="text-xs text-mute">{err ? t("trust.error.load") : t("trust.loading")}</div> : (
          <>
            <ol className="flex flex-wrap gap-1" aria-label={t("trust.ladder")}>
              {rungs(a.level, a.ceiling).map((r) => (
                <li key={r.level} data-testid={`trust-rung-${r.level}`} data-state={r.state} aria-current={r.state === "current" ? "step" : undefined}
                  className={`min-w-0 flex-1 basis-[calc(50%-4px)] rounded-lg border px-2 py-1 text-center text-[11px] sm:basis-0 ${
                    r.state === "current" ? "border-accent bg-accent/10 font-semibold text-ink"
                      : r.state === "locked" ? "border-dashed border-line text-mute" : "border-line text-ink2"}`}>
                  {t(`autonomy.${r.level}`)}
                  {r.state === "locked" && <span className="block text-[9.5px]">{t("trust.locked")}</span>}
                </li>
              ))}
            </ol>
            <p className="mt-1 text-[10.5px] text-mute">{t("trust.ceiling", { level: t(`autonomy.${a.ceiling}`) })}</p>

            <div data-testid="trust-status" data-rec={a.recommendation} className="mt-2 text-[12.5px] font-semibold text-ink2">
              {t(`trust.rec.${a.recommendation}`, { level: a.target ? t(`autonomy.${a.target}`) : "" })}
            </div>
            {a.probation.active && (
              <p data-testid="trust-probation" className="mt-0.5 text-[11px] text-mute">
                {t("trust.probation", { days: daysUntil(a.probation.until), n: Math.max(0, a.probation.min_decisions - a.probation.decisions) })}
              </p>
            )}

            {a.pending && (
              <div data-testid="trust-pending" data-kind={a.pending.kind} className="mt-2 rounded-lg border border-accent/40 bg-accent/5 p-2">
                <div className="text-[12px] font-semibold text-ink">
                  {t(`trust.pending.${a.pending.kind}`, { from: t(`autonomy.${a.pending.from_level}`), to: t(`autonomy.${a.pending.to_level}`) })}
                </div>
                <div className="mt-0.5 text-[11px] text-ink2"><Reasons p={a.pending} /></div>
                <div className="mt-2 flex flex-wrap gap-2">
                  <button type="button" data-testid="trust-accept" disabled={busy || !isAdmin} onClick={() => act(() => trustApi.accept(a.pending!.id))}
                    className={`${btn} bg-accent text-white`}>{t(a.pending.kind === "promotion" ? "trust.accept.promotion" : "trust.accept.demotion")}</button>
                  <button type="button" data-testid="trust-decline" disabled={busy || !isAdmin} onClick={() => act(() => trustApi.decline(a.pending!.id))}
                    className={`${btn} border border-line text-ink2`}>{t("trust.decline")}</button>
                </div>
                {!isAdmin && <p className="mt-1 text-[10.5px] text-mute">{t("trust.adminOnly")}</p>}
              </div>
            )}

            {a.last_change && (
              <div data-testid="trust-last-change" data-status={a.last_change.status} className="mt-2 text-[11px] text-ink2">
                {t(`trust.change.${a.last_change.status}`, {
                  from: t(`autonomy.${a.last_change.from_level}`), to: t(`autonomy.${a.last_change.to_level}`),
                  date: date(a.last_change.resolved_at ?? a.last_change.created_at),
                })}{" "}
                {a.last_change.status === "applied" && <Reasons p={a.last_change} />}
                {a.can_revert && (
                  <button type="button" data-testid="trust-revert" disabled={busy || !isAdmin} onClick={() => act(() => trustApi.revert(a.last_change!.id))}
                    className={`${btn} mt-1 block border border-line text-ink2`}>{t("trust.revert", { level: t(`autonomy.${a.last_change.from_level}`) })}</button>
                )}
              </div>
            )}
            {actionErr && <p role="alert" data-testid="trust-error" className="mt-1 text-[11px] text-[#b4443c]">{t(actionErr)}</p>}

            {a.next_level && (
              <div className="mt-3">
                <div className="flex items-center justify-between gap-2 text-[11px]">
                  <span className="text-ink2">{t("trust.progress", { level: t(`autonomy.${a.next_level}`) })}</span>
                  <span data-testid="trust-progress" className="text-mute">{progressPct(a.progress)}%</span>
                </div>
                <div className="mt-0.5 h-1.5 overflow-hidden rounded bg-line"><div className="h-full rounded bg-accent" style={{ width: `${progressPct(a.progress)}%` }} /></div>
              </div>
            )}
            <ul className="mt-2 space-y-1">
              {a.criteria.map((c) => (
                <li key={c.key} data-testid={`trust-crit-${c.key}`} data-status={c.status} className="flex items-baseline justify-between gap-2 text-[11px]">
                  <span className="min-w-0 text-ink2"><span aria-hidden style={{ color: STATUS_COLOR[c.status] }}>●</span> {t(`trust.criterion.${c.key}`)}</span>
                  <span className="shrink-0 text-mute">{c.status === "not_available" ? t("trust.na") : valueText(c.key, c.value)} / {thresholdText(c.key, c.op, c.threshold)}</span>
                </li>
              ))}
            </ul>
            {a.triggers.some((x) => x.fired) && (
              <ul data-testid="trust-triggers" className="mt-2 space-y-0.5 text-[11px] text-[#b4443c]">
                {a.triggers.filter((x) => x.fired).map((x) => <li key={x.key}>{t(`trust.trigger.${x.key}`)}: {valueText(x.key, x.value)}</li>)}
              </ul>
            )}
            <p className="mt-2 text-[10.5px] text-mute">{t("trust.since", { date: date(a.evidence_since) })} {t("trust.footer")}</p>
          </>
        )}
      </div>
    </Card>
  );
}
