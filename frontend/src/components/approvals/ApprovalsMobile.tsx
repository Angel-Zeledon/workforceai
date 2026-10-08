"use client";
import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { call } from "@/lib/api";
import { usePreferences } from "@/lib/preferences";
import { useT } from "@/lib/i18n";
import type { Approval } from "@/lib/types";
import { currentSubscription, disablePush, enablePush, getPushConfig, pushSupported, sendLabels, type PushConfig } from "@/lib/push";
import { Progress, RiskBadge } from "../ui";

type PushState = "loading" | "unsupported" | "unavailable" | "denied" | "off" | "on";

function PushControl() {
  const { t } = useT();
  const [state, setState] = useState<PushState>("loading");
  const [cfg, setCfg] = useState<PushConfig | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState(false);

  useEffect(() => {
    let live = true;
    (async () => {
      if (!pushSupported()) { if (live) setState("unsupported"); return; }
      let c: PushConfig = { enabled: false };
      try { c = await getPushConfig(); } catch { /* treated as unavailable */ }
      if (!live) return;
      setCfg(c);
      if (!c.enabled || !c.public_key) { setState("unavailable"); return; }
      if (Notification.permission === "denied") { setState("denied"); return; }
      const sub = await currentSubscription().catch(() => null);
      if (live) setState(sub && Notification.permission === "granted" ? "on" : "off");
    })();
    return () => { live = false; };
  }, []);

  // Keep the worker's notification texts in the user's language.
  useEffect(() => {
    sendLabels({
      risk_low: t("risk.low"), risk_medium: t("risk.medium"), risk_high: t("risk.high"), fallbackTitle: t("approvals.push.fallbackTitle"),
    }).catch(() => {});
  }, [t]);

  const toggle = async (on: boolean) => {
    if (!cfg?.public_key) return;
    setBusy(true); setErr(false);
    try {
      if (on) setState((await enablePush(cfg.public_key)) === "granted" ? "on" : "denied");
      else { await disablePush(); setState("off"); }
    } catch { setErr(true); }
    setBusy(false);
  };

  if (state === "loading") return null;
  const note: Partial<Record<PushState, string>> = {
    unsupported: t("approvals.push.unsupported"), unavailable: t("approvals.push.unavailable"),
    denied: t("approvals.push.denied"), on: t("approvals.push.on"), off: t("approvals.push.hint"),
  };
  return (
    <section className="rounded-lg border border-line bg-panel p-3" data-testid="push-control">
      <p className="text-[13px] text-ink2" data-testid="push-status">{note[state]}</p>
      {(state === "off" || state === "on") && (
        <button type="button" data-testid={state === "off" ? "enable-notifications" : "disable-notifications"} disabled={busy}
          onClick={() => toggle(state === "off")}
          className="mt-2 min-h-[44px] w-full rounded-md border border-accent bg-accent px-4 text-sm font-semibold text-white disabled:opacity-50 data-[on=true]:border-line-strong data-[on=true]:bg-panel data-[on=true]:text-ink"
          data-on={state === "on"}>
          {state === "off" ? t("approvals.push.enable") : t("approvals.push.disable")}
        </button>
      )}
      {err && <p role="alert" className="mt-2 text-[12px] text-err">{t("approvals.push.error")}</p>}
    </section>
  );
}

function ApprovalItem({ a, focused, onDone }: { a: Approval; focused: boolean; onDone: () => void }) {
  const { t } = useT();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState(false);
  const decide = async (d: "approve" | "reject") => {
    setBusy(true); setErr(false);
    try { await call("POST", `/approvals/${encodeURIComponent(a.id)}/decision`, { decision: d }); onDone(); }
    catch { setErr(true); setBusy(false); }
  };
  const total = a.required_approvals ?? 1;
  const done = a.decisions?.length ?? 0;
  return (
    <li data-testid={`approval-card-${a.id}`} aria-current={focused ? "true" : undefined}
      className={`rounded-lg border bg-panel p-4 ${focused ? "border-accent ring-2 ring-accent/30" : "border-line"}`}>
      <div className="flex items-start justify-between gap-3">
        <h2 className="text-[15px] font-semibold leading-snug text-ink">{a.title}</h2>
        <RiskBadge risk={a.risk} />
      </div>
      {focused && <p className="mt-1 text-[11px] font-medium text-accent">{t("approvals.mobile.focused")}</p>}
      {a.details && <p className="mt-2 text-[13px] leading-snug text-ink2 [overflow-wrap:anywhere]">{a.details}</p>}
      <dl className="mt-2 space-y-1 text-[12px] text-mute">
        <div className="font-mono">{a.action}</div>
        {a.required_role && <div data-testid={`approval-role-${a.id}`}>{t("approvals.mobile.requiredRole", { role: a.required_role })}</div>}
      </dl>
      {total > 1 && (
        <div className="mt-3" data-testid={`approval-progress-${a.id}`}>
          <div className="mb-1 flex justify-between text-[12px] text-ink2">
            <span>{t("approvals.mobile.progress", { done, total })}</span>
            <span className="text-mute">{t("approvals.mobile.doubleApproval")}</span>
          </div>
          <Progress value={(done / total) * 100} />
        </div>
      )}
      <div className="mt-4 grid grid-cols-2 gap-3">
        <button type="button" data-testid={`approval-approve-${a.id}`} disabled={busy} onClick={() => decide("approve")}
          className="min-h-[48px] rounded-md border border-ok/40 bg-ok/10 text-sm font-semibold text-ok disabled:opacity-40">{t("approvals.approve")}</button>
        <button type="button" data-testid={`approval-reject-${a.id}`} disabled={busy} onClick={() => decide("reject")}
          className="min-h-[48px] rounded-md border border-err/40 bg-err/10 text-sm font-semibold text-err disabled:opacity-40">{t("approvals.reject")}</button>
      </div>
      {err && <p role="alert" className="mt-2 text-[12px] text-err">{t("approvals.decideError")}</p>}
    </li>
  );
}

/** Mobile-first list of pending approvals. `focusId` comes from a notification link. */
export function ApprovalsMobile({ focusId }: { focusId?: string }) {
  const { t } = useT();
  const hydrate = usePreferences((s) => s.hydrate);
  const [items, setItems] = useState<Approval[] | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => { hydrate(); }, [hydrate]);

  const load = useCallback(async () => {
    try {
      const r = await call<Approval[] | { items: Approval[] }>("GET", "/approvals?status=pending");
      const list = Array.isArray(r) ? r : r.items ?? [];
      setItems(list.filter((a) => a.status === "pending"));
      setFailed(false);
    } catch { setFailed(true); }
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, 15000);
    const onVis = () => { if (document.visibilityState === "visible") load(); };
    document.addEventListener("visibilitychange", onVis);
    return () => { clearInterval(id); document.removeEventListener("visibilitychange", onVis); };
  }, [load]);

  const sorted = (items ?? []).slice().sort((a, b) => (a.id === focusId ? -1 : b.id === focusId ? 1 : +new Date(b.created_at) - +new Date(a.created_at)));
  const focusGone = !!focusId && items !== null && !items.some((a) => a.id === focusId);
  return (
    <main className="mx-auto min-h-screen max-w-xl space-y-3 bg-bg px-4 pb-10 pt-5" data-testid="approvals-page">
      <header>
        <h1 className="text-xl font-bold text-ink">{t("approvals.mobile.title")}</h1>
        <p className="text-[13px] text-mute">{t("approvals.mobile.subtitle")}</p>
      </header>
      <PushControl />
      {failed && (
        <div role="alert" className="flex items-center justify-between gap-3 rounded-lg border border-err/40 bg-err/10 p-3 text-[13px] text-err">
          <span>{t("approvals.mobile.loadError")}</span>
          <button type="button" data-testid="approvals-retry" onClick={load} className="min-h-[44px] rounded-md border border-err/40 px-3 font-semibold">{t("approvals.mobile.retry")}</button>
        </div>
      )}
      {focusGone && <p role="status" data-testid="approval-not-pending" className="rounded-lg border border-line bg-panel p-3 text-[13px] text-ink2">{t("approvals.mobile.notPending")}</p>}
      {items && items.length === 0 && !failed && <p data-testid="approvals-empty" className="py-8 text-center text-[14px] text-mute">{t("approvals.mobile.empty")}</p>}
      <ul className="space-y-3" aria-label={t("approvals.mobile.title")}>
        {sorted.map((a) => <ApprovalItem key={a.id} a={a} focused={a.id === focusId} onDone={load} />)}
      </ul>
      <Link href="/" className="block min-h-[44px] pt-3 text-center text-[13px] font-medium text-accent underline">{t("approvals.mobile.openOffice")}</Link>
    </main>
  );
}
