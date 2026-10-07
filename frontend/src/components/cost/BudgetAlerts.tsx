"use client";
import { useEffect, useState } from "react";
import { alertKey, useCost, type BudgetAlert, type BudgetWarning } from "@/lib/cost";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { Btn, useAgentName } from "../ui";
import { suggestCap, usd } from "./format";

/** A budget stop explained in plain words, with the "raise the cap and continue" action. */
function AlertCard({ alert }: { alert: BudgetAlert }) {
  const { t } = useT();
  const name = useAgentName();
  const reqText = useStore((s) => s.requests[alert.request_id]?.text);
  const setRequestCap = useCost((s) => s.setRequestCap);
  const setAgentCap = useCost((s) => s.setAgentCap);
  const hide = useCost((s) => s.hide);
  const key = alertKey(alert.scope, alert.scope_id);
  const [value, setValue] = useState(() => String(suggestCap(alert.needed_usd)));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => { setValue(String(suggestCap(alert.needed_usd))); }, [alert.needed_usd, alert.cap_usd]);

  const vars = { spent: usd(alert.spent_usd), cap: usd(alert.cap_usd), needed: usd(alert.needed_usd), agent: name(alert.agent_id || alert.scope_id) };
  const raise = async () => {
    const v = Number(value.replace(",", "."));
    if (!Number.isFinite(v) || v < alert.needed_usd) { setErr(t("cost.alert.tooLow", { needed: usd(alert.needed_usd) })); return; }
    setBusy(true); setErr("");
    try {
      if (alert.scope === "request") await setRequestCap(alert.scope_id, v);
      else await setAgentCap(alert.scope_id, v);
    } catch { setErr(t("cost.alert.error")); }
    setBusy(false);
  };

  return (
    <div data-testid={`cost-alert-${alert.scope}`} data-scope={alert.scope} role="alert"
      className="pointer-events-auto w-[min(440px,92vw)] rounded-xl border border-red-500/50 bg-panel p-4 shadow-soft">
      <div className="flex items-start justify-between gap-3">
        <h3 className="font-display text-sm font-semibold text-red-700">{t(`cost.alert.${alert.scope}.title`, vars)}</h3>
        <button type="button" onClick={() => hide(key)} aria-label={t("cost.alert.dismiss")} className="text-[11px] font-semibold text-mute hover:text-ink">{t("cost.alert.dismiss")}</button>
      </div>
      {alert.scope === "request" && reqText && <p className="mt-0.5 line-clamp-1 text-[11px] text-mute">{reqText}</p>}
      <p className="mt-2 text-[12px] text-ink">{t(`cost.alert.${alert.scope}.body`, vars)}</p>
      {alert.resumable && (
        <div className="mt-3 flex flex-wrap items-end gap-2">
          <label className="text-[11px] font-semibold text-mute">
            {t("cost.alert.raiseLabel")}
            <input data-testid="cost-raise-input" inputMode="decimal" value={value} onChange={(e) => setValue(e.target.value)}
              className="mt-0.5 block w-28 rounded-lg border border-line-strong bg-panel px-2 py-1 font-mono text-sm text-ink outline-none focus:border-accent" />
          </label>
          <Btn data-testid="cost-raise-submit" kind="primary" disabled={busy} onClick={raise}>{busy ? t("cost.gate.working") : t("cost.alert.raise")}</Btn>
        </div>
      )}
      {err && <div className="mt-2 text-[12px] text-red-700">{err}</div>}
    </div>
  );
}

function WarningToast({ w }: { w: BudgetWarning }) {
  const { t } = useT();
  const name = useAgentName();
  const hide = useCost((s) => s.hide);
  return (
    <div data-testid="cost-warning" className="pointer-events-auto flex w-[min(440px,92vw)] items-center justify-between gap-3 rounded-xl border border-amber-500/50 bg-panel px-3 py-2 text-[12px] shadow-pop">
      <span className="text-ink">
        <b>{t("cost.warn.title", { pct: Math.round(w.pct) })}</b>{" "}
        {t("cost.warn.body", { scope: t(`cost.scope.${w.scope}`), who: w.scope === "agent" ? name(w.scope_id) : "", spent: usd(w.spent_usd), cap: usd(w.cap_usd) }).replace(/\s+/g, " ")}
      </span>
      <button type="button" onClick={() => hide(alertKey(w.scope, w.scope_id))} className="text-[11px] font-semibold text-mute hover:text-ink">{t("cost.alert.dismiss")}</button>
    </div>
  );
}

/** Active budget stops and 80% warnings, stacked at the top of the screen. */
export function BudgetAlerts() {
  const alerts = useCost((s) => s.alerts);
  const warnings = useCost((s) => s.warnings);
  const hidden = useCost((s) => s.hidden);
  const requests = useStore((s) => s.requests);
  const agents = useStore((s) => s.agents);

  // Only what is still true: the request is still paused / the agent is still blocked.
  const live = Object.entries(alerts).filter(([k, a]) => {
    if (hidden[k]) return false;
    if (a.scope === "request") return requests[a.scope_id]?.status === "paused";
    if (a.scope === "agent") return agents[a.scope_id]?.state === "blocked";
    return true; // org budget: stays until dismissed
  });
  const warns = Object.entries(warnings).filter(([k, w]) => !hidden[k] && !alerts[k] && Date.now() - w.ts < 5 * 60_000);
  if (!live.length && !warns.length) return null;
  return (
    <div data-testid="cost-alerts" className="pointer-events-none fixed inset-x-0 top-[64px] z-[60] flex flex-col items-center gap-2 px-3">
      {live.map(([k, a]) => <AlertCard key={k} alert={a} />)}
      {warns.map(([k, w]) => <WarningToast key={k} w={w} />)}
    </div>
  );
}
