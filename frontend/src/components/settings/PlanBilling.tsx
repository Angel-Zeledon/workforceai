"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { useCan, useSession } from "@/lib/session";
import { notifyApi, PlanError, type PlanId, type PlanStarted, type PlanTier, type PlanView } from "@/lib/notifyApi";
import { PLAN_CHANGED_EVENT } from "@/lib/planLimit";
import { OnvoPayDialog } from "./OnvoPayDialog";
import { Btn } from "../ui";
import { ErrorLine } from "../connections/shared";
import { fmtBytes } from "../nav/TrialBanner";

/**
 * Plan: Free, Pro and Max (backend/internal/policy/plans.go, docs/architecture/approvals-notify.md section 3.1).
 *
 *   GET /plan   metrics:read     the plan, its limits, what is used of each, the AI allowance of the month, the catalog
 *   PUT /plan   billing:manage   change the plan; only when the deployment has PLAN_SELF_SERVE (nothing charges)
 *   POST /plan/subscribe, /plan/confirm, /plan/cancel   billing:manage   with ONVO Pay (billing "onvo"): ONVO's card
 *               form opens here (OnvoPayDialog); the plan changes once the backend sees the payment in ONVO.
 *               Changing between paid plans is immediate and the rest of the current period is not refunded.
 *
 * Going over a limit never deletes anything: it only refuses the next one. The AI allowance is a hard monthly cap.
 */

const usd = (n: number, digits = 0) => new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", minimumFractionDigits: digits, maximumFractionDigits: Math.max(digits, 2) }).format(n);

type Period = "monthly" | "yearly";

/** The singular text for exactly one ("1 proyecto activo"), else the plural. */
const count = (key: string, n: number) => (n === 1 ? `${key}.one` : key);

function Meter({ label, used, limit, fmt = String, testid }: { label: string; used: number; limit: number; fmt?: (n: number) => string; testid: string }) {
  const { t } = useT();
  const known = used >= 0;
  const pct = limit > 0 && known ? Math.min(100, Math.round((used / limit) * 100)) : 0;
  const tone = pct >= 100 ? "bg-err" : pct >= 80 ? "bg-warn" : "bg-accent";
  return (
    <div data-testid={testid} className="rounded-lg border border-line bg-panel p-3">
      <div className="flex items-baseline justify-between gap-2 text-[12.5px]">
        <span className="text-ink2">{label}</span>
        <span className="font-mono text-ink">{known ? fmt(used) : "—"} <span className="text-mute">/ {limit > 0 ? fmt(limit) : t("planpage.unlimited")}</span></span>
      </div>
      {limit > 0 && (
        <span role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} className="mt-2 block h-1.5 overflow-hidden rounded-full bg-line">
          <span className={`block h-full ${tone}`} style={{ width: `${pct}%` }} />
        </span>
      )}
    </div>
  );
}

function TierCard({ tier, current, period, action }: { tier: PlanTier; current: boolean; period: Period; action: React.ReactNode }) {
  const { t } = useT();
  const price = period === "yearly" ? tier.price_yearly_usd : tier.price_monthly_usd;
  const rows = [
    t("planpage.f.ai", { usd: usd(tier.ai_allowance_usd) }),
    t(count("planpage.f.projects", tier.max_active_projects), { n: tier.max_active_projects }),
    t(count("planpage.f.schedules", tier.max_schedules), { n: tier.max_schedules }),
    t(count("planpage.f.seats", tier.max_seats), { n: tier.max_seats }),
    t("planpage.f.storage", { size: fmtBytes(tier.storage_bytes) }),
    t("planpage.f.agents", { n: tier.max_agents }),
  ];
  return (
    <section data-testid={`plan-tier-${tier.id}`} data-current={current ? "1" : "0"}
      className={`flex flex-col rounded-xl border bg-panel p-5 ${current ? "border-accent ring-1 ring-accent" : "border-line"}`}>
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-[15px] font-semibold text-ink">{t(`plan.${tier.id}.label`)}</h3>
        {current && <span className="rounded-md bg-accent-soft px-2 py-0.5 text-[11px] font-semibold text-accent">{t("planpage.current")}</span>}
      </div>
      <p className="mt-1 text-[12.5px] text-mute">{t(`planpage.${tier.id}.desc`)}</p>
      <p className="mt-4 text-ink">
        {price > 0 ? (
          <><span className="text-[26px] font-semibold tracking-tight">{usd(price)}</span>
            <span className="ml-1 text-[12.5px] text-mute">{t(period === "yearly" ? "planpage.perYear" : "planpage.perMonth")}</span></>
        ) : <span className="text-[26px] font-semibold tracking-tight">{t("planpage.freePrice")}</span>}
      </p>
      <ul className="mt-4 flex-1 space-y-1.5 text-[12.5px] text-ink2">
        {rows.map((r) => <li key={r} className="flex gap-2"><span aria-hidden className="text-accent">✓</span><span>{r}</span></li>)}
      </ul>
      <div className="mt-5">{action}</div>
    </section>
  );
}

export function PlanBilling() {
  const { t, locale } = useT();
  const canChange = useCan("billing:manage");
  const [v, setV] = useState<PlanView | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState<PlanId | "cancel" | "">("");
  const user = useSession((st) => st.user);
  const [period, setPeriod] = useState<Period>("monthly");
  const [done, setDone] = useState("");
  // ONVO: the plan the person is about to pay (asked to confirm first when it replaces a paid one), and the
  // subscription being paid in the dialog.
  const [ask, setAsk] = useState<PlanId | "cancel" | null>(null);
  const [paying, setPaying] = useState<{ started: PlanStarted; plan: PlanId } | null>(null);

  const load = useCallback(() => {
    notifyApi.orgPlan().then((d) => { setV(d); setErr(""); })
      .catch((e) => setErr(e instanceof PlanError && e.status === 403 ? t("planpage.err.forbidden") : t("planpage.err.load")));
  }, [t]);
  useEffect(() => { load(); }, [load]);
  // A plan the server just changed: shown here, and the plan bar on top reloads at once.
  const applied = useCallback((nv: PlanView) => { setV(nv); window.dispatchEvent(new Event(PLAN_CHANGED_EVENT)); }, []);

  const payError = (e: unknown) =>
    setErr(e instanceof PlanError && e.status === 403 ? t("planpage.ownerOnly") : t("planpage.err.payment"));

  const buy = async (to: PlanId) => {
    setAsk(null); setBusy(to); setErr(""); setDone("");
    try {
      const started = await notifyApi.subscribe(to, period, user?.name ?? "", user?.email ?? "");
      setPaying({ started, plan: to });
    } catch (e) { payError(e); } finally { setBusy(""); }
  };

  const cancel = async (resume: boolean) => {
    setAsk(null); setBusy("cancel"); setErr(""); setDone("");
    try {
      applied(await notifyApi.cancel(resume));
      setDone(t(resume ? "planpage.sub.resumed" : "planpage.sub.cancelled"));
    } catch (e) { payError(e); } finally { setBusy(""); }
  };

  const paid = useCallback((nv: PlanView) => {
    applied(nv); setPaying(null);
    setDone(t("planpage.paid", { plan: t(`plan.${nv.plan}.label`) }));
  }, [t, applied]);
  const closePay = useCallback(() => { setPaying(null); load(); }, [load]);

  const change = async (to: PlanId) => {
    setBusy(to); setErr(""); setDone("");
    try {
      applied(await notifyApi.changePlan(to));
      setDone(t("planpage.changed", { plan: t(`plan.${to}.label`) }));
    } catch (e) {
      setErr(e instanceof PlanError && e.status === 403 ? t("planpage.err.notSelfServe") : t("planpage.err.change"));
    } finally { setBusy(""); }
  };

  if (!v) {
    return (
      <div className="mx-auto max-w-4xl px-5 py-6 sm:px-6">
        {err ? <ErrorLine msg={err} /> : <div className="h-40 animate-pulse rounded-xl bg-panel2" />}
      </div>
    );
  }

  const inCatalog = v.catalog.some((c) => c.id === v.plan);
  const onvo = v.billing === "onvo";
  const sub = v.subscription;
  const can = (plan: string, per: Period) => !onvo || (v.available ?? []).includes(`${plan}_${per}`);
  const yearly = !onvo || (v.available ?? []).some((k) => k.endsWith("_yearly"));
  const when = (iso?: string) => (iso ? new Date(iso).toLocaleDateString(locale === "en" ? "en-US" : locale === "pt" ? "pt-BR" : "es-CR", { dateStyle: "long" }) : "");
  const rank = (id: string) => { const i = v.catalog.findIndex((c) => c.id === id); return i < 0 ? v.catalog.length : i; };
  const u = v.usage;
  const aiPct = Math.round(u.percent_used);

  return (
    <div data-testid="plan-page" className="mx-auto max-w-4xl space-y-6 px-5 py-6 sm:px-6">
      {inCatalog ? (
        <section className="rounded-xl border border-line bg-panel p-5">
          <div className="flex flex-wrap items-baseline justify-between gap-2">
            <h2 className="text-[15px] font-semibold text-ink">{t("planpage.yours", { plan: t(`plan.${v.plan}.label`) })}</h2>
            <span className="text-[12px] text-mute">{t("planpage.resets")}</span>
          </div>
          <div data-testid="plan-ai" className="mt-4">
            <div className="flex items-baseline justify-between gap-2 text-[12.5px]">
              <span className="text-ink2">{t("planpage.ai")}</span>
              <span className="font-mono text-ink">{usd(u.spent_usd, 2)} <span className="text-mute">/ {usd(u.cap_usd, 2)}</span></span>
            </div>
            <span role="progressbar" aria-label={t("planpage.ai")} aria-valuemin={0} aria-valuemax={100} aria-valuenow={aiPct} className="mt-2 block h-2 overflow-hidden rounded-full bg-line">
              <span className={`block h-full ${u.paused ? "bg-err" : u.state === "warn" ? "bg-warn" : "bg-accent"}`} style={{ width: `${Math.min(100, aiPct)}%` }} />
            </span>
            {u.paused && <p className="mt-2 text-[12.5px] font-medium text-err">{t("planpage.aiPaused")}</p>}
          </div>
          <div className="mt-4 grid gap-2.5 sm:grid-cols-2">
            <Meter testid="plan-used-projects" label={t("planpage.l.projects")} used={v.used.active_projects} limit={v.limits.max_active_projects} />
            <Meter testid="plan-used-schedules" label={t("planpage.l.schedules")} used={v.used.schedules} limit={v.limits.max_schedules} />
            <Meter testid="plan-used-seats" label={t("planpage.l.seats")} used={v.used.seats} limit={v.limits.max_seats} />
            <Meter testid="plan-used-storage" label={t("planpage.l.storage")} used={v.used.storage_bytes} limit={v.limits.storage_bytes} fmt={fmtBytes} />
            <Meter testid="plan-used-agents" label={t("planpage.l.agents")} used={v.used.agents} limit={v.limits.max_agents} />
          </div>
          <p className="mt-4 text-[12px] leading-relaxed text-mute">{t("planpage.never")}</p>
        </section>
      ) : (
        <section data-testid="plan-none" className="rounded-xl border border-line bg-panel p-5 text-[13px] text-ink2">{t("planpage.none")}</section>
      )}

      {onvo && sub?.subscribed && (
        <section data-testid="plan-subscription" className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-line bg-panel p-5">
          <div className="min-w-0">
            <h2 className="text-[14px] font-semibold text-ink">{t("planpage.sub.title")}</h2>
            <p className={`mt-0.5 text-[12.5px] ${sub.past_due ? "font-medium text-warn" : "text-mute"}`}>
              {sub.past_due ? t("planpage.sub.pastDue")
                : sub.cancel_at_period_end ? t("planpage.sub.ends", { date: when(sub.period_end) || t("planpage.sub.periodEnd") })
                : sub.period_end ? t("planpage.sub.renews", { date: when(sub.period_end) }) : t("planpage.sub.active")}
            </p>
          </div>
          {canChange && sub.past_due && v.plan !== "free" && (
            <Btn kind="primary" data-testid="plan-new-card" disabled={busy !== ""} onClick={() => { void buy(v.plan); }}>{t("planpage.sub.newCard")}</Btn>
          )}
          {canChange && !sub.past_due && (sub.cancel_at_period_end
            ? <Btn kind="primary" data-testid="plan-resume" disabled={busy !== ""} onClick={() => { void cancel(true); }}>{t("planpage.sub.resume")}</Btn>
            : <Btn data-testid="plan-cancel" disabled={busy !== ""} onClick={() => setAsk("cancel")}>{t("planpage.sub.cancel")}</Btn>)}
        </section>
      )}

      {ask && (
        <section role="alertdialog" data-testid="plan-ask" className="rounded-xl border border-warn/40 bg-warn/10 p-4 text-[12.5px] text-ink2">
          <p>{ask === "cancel" ? t("planpage.ask.cancel", { date: when(sub?.period_end) || t("planpage.sub.periodEnd") })
            : t("planpage.ask.change", { plan: t(`plan.${ask}.label`), current: t(`plan.${v.plan}.label`) })}</p>
          <div className="mt-3 flex gap-2">
            <Btn kind={ask === "cancel" ? "danger" : "primary"} data-testid="plan-ask-yes" onClick={() => { if (ask === "cancel") void cancel(false); else void buy(ask); }}>
              {ask === "cancel" ? t("planpage.sub.cancel") : t("planpage.ask.pay")}
            </Btn>
            <Btn onClick={() => setAsk(null)}>{t("planpage.ask.back")}</Btn>
          </div>
        </section>
      )}

      {(inCatalog || v.self_serve) && (
        <section>
          <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
            <h2 className="text-[11px] font-semibold uppercase tracking-[0.08em] text-mute">{t("planpage.catalog")}</h2>
            {yearly && <div role="radiogroup" aria-label={t("planpage.period")} className="inline-flex rounded-lg border border-line bg-panel p-0.5 text-[12px]">
              {(["monthly", "yearly"] as const).map((p) => (
                <button key={p} type="button" role="radio" aria-checked={period === p} data-testid={`plan-period-${p}`} onClick={() => setPeriod(p)}
                  className={`rounded-md px-3 py-1 font-medium ${period === p ? "bg-accent text-white" : "text-ink2 hover:text-ink"}`}>
                  {t(`planpage.${p}`)}
                </button>
              ))}
            </div>}
          </div>
          <div className="grid gap-3 md:grid-cols-3">
            {v.catalog.map((tier) => {
              const current = tier.id === v.plan;
              const up = rank(tier.id) > rank(v.plan);
              const label = t(up ? "planpage.upgradeTo" : "planpage.moveTo", { plan: t(`plan.${tier.id}.label`) });
              const action = current || !canChange ? null
                : onvo ? (
                  tier.price_monthly_usd > 0 && can(tier.id, period) ? (
                    <Btn kind="primary" className="w-full" disabled={busy !== "" || !!ask} data-testid={`plan-choose-${tier.id}`}
                      onClick={() => { if (sub?.subscribed) setAsk(tier.id); else void buy(tier.id); }}>
                      {busy === tier.id ? t("planpage.opening") : label}
                    </Btn>
                  ) : tier.price_monthly_usd === 0 && sub?.subscribed && !sub.cancel_at_period_end ? (
                    <Btn className="w-full" disabled={busy !== "" || !!ask} data-testid={`plan-choose-${tier.id}`} onClick={() => setAsk("cancel")}>{label}</Btn>
                  ) : null
                )
                : v.self_serve ? (
                  <Btn kind={up ? "primary" : "ghost"} className="w-full" disabled={busy !== ""} data-testid={`plan-choose-${tier.id}`} onClick={() => { void change(tier.id); }}>
                    {busy === tier.id ? t("planpage.changing") : t(up ? "planpage.upgradeTo" : "planpage.moveTo", { plan: t(`plan.${tier.id}.label`) })}
                  </Btn>
                ) : null;
              return <TierCard key={tier.id} tier={tier} current={current} period={period} action={action} />;
            })}
          </div>
          {v.billing === "none" && <p data-testid="plan-not-self-serve" className="mt-3 text-[12.5px] text-mute">{t("planpage.contact")}</p>}
          {v.billing !== "none" && !canChange && <p className="mt-3 text-[12.5px] text-mute">{t("planpage.ownerOnly")}</p>}
          {v.billing === "self_serve" && <p className="mt-2 text-[12px] text-mute">{t("planpage.noCharge")}</p>}
          {onvo && <p data-testid="plan-onvo-note" className="mt-2 text-[12px] text-mute">{t("planpage.onvoNote")}</p>}
        </section>
      )}
      {paying && (
        <OnvoPayDialog started={paying.started} title={t("planpage.pay.title", { plan: t(`plan.${paying.plan}.label`) })} onDone={paid} onClose={closePay} />
      )}
      {done && <p role="status" data-testid="plan-changed" className="text-[12.5px] font-medium text-ok">{done}</p>}
      {err && <ErrorLine msg={err} />}
    </div>
  );
}
