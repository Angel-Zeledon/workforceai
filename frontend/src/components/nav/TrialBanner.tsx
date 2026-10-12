"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { logout, useSession } from "@/lib/session";

/** Shown only in a trial account: how long is left, and the way to keep the work (a real account). At zero the session ends. */
export function TrialBanner() {
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
        className="rounded-md border border-current px-2.5 py-0.5 font-semibold hover:bg-white/40">{t("trialBanner.keep")}</button>
    </div>
  );
}
