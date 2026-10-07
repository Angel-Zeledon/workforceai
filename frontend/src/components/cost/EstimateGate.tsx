"use client";
import { useEffect, useState } from "react";
import { useCost } from "@/lib/cost";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { Btn } from "../ui";
import { EstimateCard } from "./EstimateCard";
import { usd } from "./format";

/**
 * Modal that asks the user to confirm a plan whose estimated MAXIMUM exceeds the
 * confirmation threshold or the request cap. Nothing runs until the user answers.
 */
export function EstimateGate() {
  const { t } = useT();
  const waiting = useStore((s) =>
    Object.values(s.requests).filter((r) => r.status === "awaiting_confirmation").sort((a, b) => +new Date(a.created_at) - +new Date(b.created_at))[0]);
  const est = useCost((s) => (waiting ? s.estimates[waiting.id] : undefined));
  const confirm = useCost((s) => s.confirm);
  const loadEstimate = useCost((s) => s.loadEstimate);
  const [cap, setCap] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const id = waiting?.id;
  useEffect(() => { if (id && !est) void loadEstimate(id); }, [id, est, loadEstimate]);
  useEffect(() => { setCap(""); setErr(""); }, [id]);
  if (!waiting) return null;

  const answer = async (proceed: boolean) => {
    const value = Number(cap.replace(",", "."));
    if (proceed && cap.trim() && (!Number.isFinite(value) || value <= 0)) { setErr(t("cost.gate.capInvalid")); return; }
    setBusy(true); setErr("");
    try { await confirm(waiting.id, proceed, proceed && cap.trim() ? value : undefined); }
    catch { setErr(t("cost.gate.error")); }
    setBusy(false);
  };

  const reason = est?.confirm_reason;
  return (
    <div className="pointer-events-auto fixed inset-0 z-[70] flex items-center justify-center bg-ink/40 p-4 backdrop-blur-sm">
      <div data-testid="cost-estimate-dialog" role="dialog" aria-modal="true" aria-label={t("cost.gate.title")}
        className="max-h-full w-full max-w-[560px] overflow-auto rounded-xl border border-line bg-panel shadow-soft">
        <header className="border-b border-line px-5 py-3">
          <h2 className="font-display text-base font-semibold text-ink">{t("cost.gate.title")}</h2>
          <p className="mt-0.5 line-clamp-2 text-[12px] text-mute">{waiting.text}</p>
        </header>
        <div className="space-y-4 p-5">
          {est ? (
            <>
              {reason && (
                <p data-testid="cost-gate-reason" className="rounded-lg border border-amber-500/40 bg-amber-500/10 p-2.5 text-[12px] text-amber-800">
                  {t(`cost.gate.reason.${reason}`, { max: usd(est.total.max_usd), threshold: usd(est.threshold_usd), cap: usd(est.budget_cap_usd) })}
                </p>
              )}
              <EstimateCard requestId={waiting.id} />
            </>
          ) : <p className="text-[12px] text-mute">{t("cost.gate.loading")}</p>}
          <label className="block text-[12px]">
            <span className="font-semibold text-ink">{t("cost.gate.capLabel")}</span>
            <input data-testid="cost-confirm-cap-input" inputMode="decimal" value={cap} onChange={(e) => setCap(e.target.value)}
              placeholder={est ? usd(est.total.max_usd).replace("$", "") : ""}
              className="mt-1 w-full rounded-lg border border-line-strong bg-panel px-3 py-1.5 font-mono text-sm text-ink outline-none focus:border-accent" />
            <span className="mt-1 block text-[11px] text-mute">{t("cost.gate.capHint")}</span>
          </label>
          {err && <div role="alert" className="text-[12px] text-red-700">{err}</div>}
          <div className="flex justify-end gap-2">
            <Btn data-testid="cost-confirm-cancel" kind="danger" disabled={busy} onClick={() => answer(false)}>{t("cost.gate.cancel")}</Btn>
            <Btn data-testid="cost-confirm-proceed" kind="primary" disabled={busy} onClick={() => answer(true)}>{busy ? t("cost.gate.working") : t("cost.gate.proceed")}</Btn>
          </div>
        </div>
      </div>
    </div>
  );
}
