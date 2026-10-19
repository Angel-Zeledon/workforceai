"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { logout, useSession } from "@/lib/session";
import { notifyApi, type PlanUsage } from "@/lib/notifyApi";


// A free-plan cap is small (cents matter): up to three decimals, never a misleading "$0.00" for a real spend.
const usdFmt = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", minimumFractionDigits: 2, maximumFractionDigits: 3 });
const fmtUsd = (n: number) => usdFmt.format(n);

/** Shown only in a trial account: how long is left, and the way to keep the work (a real account). At zero the session ends. */
function TrialRow() {
  const { t } = useT();
  const exp = useSession((s) => (s.user?.trial_expires_at ? Date.parse(s.user.trial_expires_at) : 0));
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!exp) return;
    const id = setInterval(() => setNow(Date.now()), 15_000);
    return () => clearInterval(id);
  }, [exp]);
  const left = exp - now;
  useEffect(() => {
    if (exp && left <= 0) void logout().then(() => { window.location.href = "/login?trial=expired"; });
  }, [exp, left]);
  if (!exp || left <= 0) return null;
  const h = Math.floor(left / 3_600_000);
  const m = Math.floor((left % 3_600_000) / 60_000);
  const warn = left < 3_600_000;
  return (
    <div data-testid="trial-banner" role="status" className={`flex shrink-0 flex-wrap items-center justify-center gap-x-3 gap-y-1 px-4 py-1.5 text-xs ${warn ? "bg-warn/15 text-warn" : "bg-accent-soft text-accent"}`}>
      <span className="font-semibold">{t("trialBanner.label")}</span>
      <span>{h > 0 ? t("trial.left.hm", { h, m }) : t("trial.left.m", { m })}</span>
      <button type="button" onClick={() => { void logout().then(() => { window.location.href = "/register"; }); }}
        className="rounded-md border border-current px-3 py-1.5 sm:px-2.5 sm:py-0.5 font-semibold hover:bg-white/40">{t("trialBanner.keep")}</button>
    </div>
  );
}

/**
 * Free plan with a HARD spend cap: spent / estimated / cap, always visible while the plan has a cap, and a clear
 * "paused" state when the cap is reached (the server stops the work and notifies; it never spends past the cap).
 * Hidden when the deployment has no free plan. Polls gently; a failed read leaves the last known state.
 */
export function PlanCapBanner() {
  const { t } = useT();
  const signedIn = useSession((s) => s.status === "authenticated");
  const [u, setU] = useState<PlanUsage | null>(null);
  useEffect(() => {
    if (!signedIn) return;
    let live = true;
    const load = () => { notifyApi.planUsage().then((d) => { if (live) setU(d); }).catch(() => { /* no plan endpoint, or no permission: stay hidden */ }); };
    load();
    const id = setInterval(load, 20_000);
    const onFocus = () => load();
    window.addEventListener("focus", onFocus);
    return () => { live = false; clearInterval(id); window.removeEventListener("focus", onFocus); };
  }, [signedIn]);
  if (!u || !u.hard) return null;
  const tone = u.paused ? "bg-err/10 text-err" : u.state === "warn" ? "bg-warn/15 text-warn" : "bg-accent-soft text-accent";
  const pct = Math.round(u.percent_used);
  return (
    <div data-testid="plan-cap" data-state={u.state} data-paused={u.paused ? "1" : "0"} role={u.paused ? "alert" : "status"}
      className={`flex shrink-0 flex-wrap items-center justify-center gap-x-3 gap-y-1 px-4 py-1.5 text-xs ${tone}`}>
      <span className="font-semibold">{t("plan.free.label")}</span>
      <span className="font-mono" data-testid="plan-cap-line">{t("plan.line", { spent: fmtUsd(u.spent_usd), estimated: fmtUsd(u.estimated_usd), cap: fmtUsd(u.cap_usd) })}</span>
      <span role="progressbar" aria-label={t("plan.bar")} aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct}
        className="relative hidden h-1.5 w-28 overflow-hidden rounded-full bg-black/10 sm:block">
        <span className="absolute inset-y-0 left-0 bg-current" style={{ width: `${pct}%` }} />
      </span>
      {u.paused ? <span data-testid="plan-cap-paused" className="font-semibold">{t("plan.paused")}</span>
        : u.state === "warn" ? <span>{t("plan.warn", { pct })}</span> : null}
    </div>
  );
}

/** The banners under the top bar: trial time left and, on the free plan, the spend cap. */
export function TrialBanner() {
  return (
    <>
      <TrialRow />
      <PlanCapBanner />
    </>
  );
}
