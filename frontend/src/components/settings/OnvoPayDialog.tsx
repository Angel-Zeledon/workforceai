"use client";
import { useEffect, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { notifyApi, PlanError, type PlanStarted, type PlanView } from "@/lib/notifyApi";
import { Btn } from "../ui";

/**
 * Pays a subscription with ONVO Pay's form (https://docs.onvopay.com/en/integrations/sdk): the card goes straight to
 * ONVO, never through our servers. After the SDK's onSuccess, POST /plan/confirm asks the backend to check the payment
 * with ONVO; while ONVO is still confirming it answers 409 not_paid, so it is asked again for a little while. If the
 * person closes this before that, the backend's sync job finishes the change on its own.
 */
const SDK = "https://sdk.onvopay.com/sdk.js";

type OnvoSDK = { pay: (o: Record<string, unknown>) => { render: (sel: string) => void } };
declare global { interface Window { onvo?: OnvoSDK } }

let loading: Promise<OnvoSDK> | null = null;
function loadSdk(): Promise<OnvoSDK> {
  if (window.onvo) return Promise.resolve(window.onvo);
  if (!loading) {
    loading = new Promise((resolve, reject) => {
      const s = document.createElement("script");
      s.src = SDK;
      s.async = true;
      s.onload = () => (window.onvo ? resolve(window.onvo) : reject(new Error("onvo sdk")));
      s.onerror = () => { loading = null; reject(new Error("onvo sdk")); };
      document.head.appendChild(s);
    });
  }
  return loading;
}

export function OnvoPayDialog({ started, title, onDone, onClose }: {
  started: PlanStarted; title: string; onDone: (v: PlanView) => void; onClose: () => void;
}) {
  const { t } = useT();
  const [state, setState] = useState<"loading" | "form" | "confirming" | "error">("loading");
  const [err, setErr] = useState("");
  const live = useRef(true);
  useEffect(() => () => { live.current = false; }, []);

  useEffect(() => {
    const confirm = async () => {
      setState("confirming");
      for (let i = 0; i < 15 && live.current; i++) {
        try { onDone(await notifyApi.confirm(started.subscription_id)); return; } catch (e) {
          if (!(e instanceof PlanError && e.code === "not_paid")) break;
          await new Promise((r) => setTimeout(r, 2000));
        }
      }
      if (live.current) { setErr(t("planpage.pay.slow")); setState("error"); }
    };
    loadSdk().then((onvo) => {
      if (!live.current) return;
      setState("form");
      onvo.pay({
        publicKey: started.publishable_key, subscriptionId: started.subscription_id, customerId: started.customer_id,
        paymentType: "subscription",
        onSuccess: () => { void confirm(); },
        onError: () => { setErr(t("planpage.pay.declined")); },
      }).render("#onvo-pay");
    }).catch(() => { if (live.current) { setErr(t("planpage.pay.noSdk")); setState("error"); } });
  }, [started, onDone, t]);

  return (
    <div role="dialog" aria-modal="true" aria-label={title} data-testid="onvo-pay" className="fixed inset-0 z-[130] flex items-center justify-center bg-black/30 p-4">
      <div className="max-h-[92vh] w-full max-w-md overflow-y-auto rounded-xl border border-line bg-panel p-5 shadow-float">
        <div className="mb-3 flex items-center justify-between gap-3">
          <h2 className="text-[15px] font-semibold text-ink">{title}</h2>
          <button type="button" aria-label={t("common.close")} onClick={onClose} className="rounded px-2 text-lg leading-none text-mute hover:text-ink">×</button>
        </div>
        {state === "loading" && <div className="h-48 animate-pulse rounded-lg bg-panel2" />}
        <div id="onvo-pay" className={state === "form" ? "" : "hidden"} />
        {state === "confirming" && <p role="status" className="py-6 text-center text-[13px] text-ink2">{t("planpage.pay.confirming")}</p>}
        {err && <p role="alert" data-testid="onvo-pay-error" className="mt-3 rounded-md border border-err/30 bg-err/5 px-3 py-2 text-[12.5px] text-err">{err}</p>}
        <p className="mt-4 text-[11.5px] leading-relaxed text-mute">{t("planpage.pay.secure")}</p>
        {state === "error" && <div className="mt-3 text-right"><Btn onClick={onClose}>{t("common.close")}</Btn></div>}
      </div>
    </div>
  );
}
