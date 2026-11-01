"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { notifyApi, PlanError, type PlanId, type PlanTier, type PlanView } from "@/lib/notifyApi";
import { Btn } from "../ui";
import { ErrorLine } from "../connections/shared";
import { fmtBytes } from "../nav/TrialBanner";

/**
 * Plan: Free, Pro and Max (backend/internal/policy/plans.go, docs/architecture/approvals-notify.md section 3.1).
 *
 *   GET /plan   metrics:read     the plan, its limits, what is used of each, the AI allowance of the month, the catalog
 *   PUT /plan   billing:manage   change the plan; only when the deployment has PLAN_SELF_SERVE (nothing charges yet)
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
  const { t } = useT();
  const canChange = useCan("billing:manage");
  const [v, setV] = useState<PlanView | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState<PlanId | "">("");
  const [period, setPeriod] = useState<Period>("monthly");
  const [done, setDone] = useState("");

  const load = useCallback(() => {
    notifyApi.orgPlan().then((d) => { setV(d); setErr(""); })
      .catch((e) => setErr(e instanceof PlanError && e.status === 403 ? t("planpage.err.forbidden") : t("planpage.err.load")));
  }, [t]);
  useEffect(() => { load(); }, [load]);

  const change = async (to: PlanId) => {
    setBusy(to); setErr(""); setDone("");
    try {
      setV(await notifyApi.changePlan(to));
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

      {(inCatalog || v.self_serve) && (
        <section>
          <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
            <h2 className="text-[11px] font-semibold uppercase tracking-[0.08em] text-mute">{t("planpage.catalog")}</h2>
            <div role="radiogroup" aria-label={t("planpage.period")} className="inline-flex rounded-lg border border-line bg-panel p-0.5 text-[12px]">
              {(["monthly", "yearly"] as const).map((p) => (
                <button key={p} type="button" role="radio" aria-checked={period === p} data-testid={`plan-period-${p}`} onClick={() => setPeriod(p)}
                  className={`rounded-md px-3 py-1 font-medium ${period === p ? "bg-accent text-white" : "text-ink2 hover:text-ink"}`}>
                  {t(`planpage.${p}`)}
                </button>
              ))}
            </div>
          </div>
          <div className="grid gap-3 md:grid-cols-3">
            {v.catalog.map((tier) => {
              const current = tier.id === v.plan;
              const up = rank(tier.id) > rank(v.plan);
              const action = current ? null
                : v.self_serve && canChange ? (
                  <Btn kind={up ? "primary" : "ghost"} className="w-full" disabled={busy !== ""} data-testid={`plan-choose-${tier.id}`} onClick={() => { void change(tier.id); }}>
                    {busy === tier.id ? t("planpage.changing") : t(up ? "planpage.upgradeTo" : "planpage.moveTo", { plan: t(`plan.${tier.id}.label`) })}
                  </Btn>
                ) : null;
              return <TierCard key={tier.id} tier={tier} current={current} period={period} action={action} />;
            })}
          </div>
          {!v.self_serve && <p data-testid="plan-not-self-serve" className="mt-3 text-[12.5px] text-mute">{t("planpage.contact")}</p>}
          {v.self_serve && !canChange && <p className="mt-3 text-[12.5px] text-mute">{t("planpage.ownerOnly")}</p>}
          {v.self_serve && <p className="mt-2 text-[12px] text-mute">{t("planpage.noCharge")}</p>}
        </section>
      )}
      {done && <p role="status" data-testid="plan-changed" className="text-[12.5px] font-medium text-ok">{done}</p>}
      {err && <ErrorLine msg={err} />}
    </div>
  );
}
