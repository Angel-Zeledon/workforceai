"use client";
import Link from "next/link";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, call } from "@/lib/api";
import { fmtUsd, trOr, useT } from "@/lib/i18n";
import {
  batchGroupOf, clampIndex, deckOf, keyToCommand, needsConfirm, swipeIntent, UNDO_MS,
  type ApprovalInbox, type InboxGroup, type InboxItem,
} from "@/lib/inboxLogic";
import { usePreferences } from "@/lib/preferences";
import { useCan, useSession } from "@/lib/session";
import { OrgIndicator } from "../auth/OrgIndicator";
import { RiskBadge } from "../ui";
import { DevilsAdvocate } from "./DevilsAdvocate";

type Decision = "approve" | "reject";
interface Held { item: InboxItem; decision: Decision; timer: ReturnType<typeof setTimeout>; until: number }

/** B17 "approvals in one minute": one card at a time across all projects. Keyboard A/R/J/K/E, swipe on touch.
 * A decision is HELD on this device for UNDO_MS (the API has no undo) and then sent; leaving the page sends what is
 * held. High risk never approves on a swipe or a key without an explicit confirm. Nothing here changes a setting. */
export function ApprovalsInbox() {
  const { t } = useT();
  const hydrate = usePreferences((s) => s.hydrate);
  const signedIn = useSession((s) => s.status === "authenticated");
  const canDecide = useCan("approvals:decide");
  const [inbox, setInbox] = useState<ApprovalInbox | null>(null);
  const [failed, setFailed] = useState(false);
  const [idx, setIdx] = useState(0);
  const [expanded, setExpanded] = useState(false);
  const [held, setHeld] = useState<Held[]>([]);
  const [errors, setErrors] = useState<string[]>([]);
  const [confirm, setConfirm] = useState<{ kind: "high"; item: InboxItem } | { kind: "batch"; group: InboxGroup } | null>(null);
  const [names, setNames] = useState<Record<string, string>>({});
  const [hiddenTips, setHiddenTips] = useState<Set<string>>(new Set());
  const [dx, setDx] = useState(0);
  const drag = useRef<{ x: number; y: number; id: number } | null>(null);
  const heldRef = useRef<Held[]>([]);
  heldRef.current = held;
  useEffect(() => { hydrate(); }, [hydrate]);

  const load = useCallback(async () => {
    try { setInbox(await call<ApprovalInbox>("GET", "/approvals/inbox")); setFailed(false); } catch { setFailed(true); }
  }, []);
  useEffect(() => {
    void load();
    api.agents().then((as) => setNames(Object.fromEntries(as.map((a) => [a.id, a.name])))).catch(() => {});
    const iv = setInterval(() => { if (!heldRef.current.length) void load(); }, 20000);
    return () => clearInterval(iv);
  }, [load]);

  const send = useCallback(async (h: Pick<Held, "item" | "decision">) => {
    try { await call("POST", `/approvals/${encodeURIComponent(h.item.id)}/decision`, { decision: h.decision }); }
    catch { setErrors((e) => [...e.slice(-2), t("approvalInbox.sendError", { title: h.item.title })]); }
    finally { setHeld((x) => x.filter((y) => y.item.id !== h.item.id)); void load(); }
  }, [load, t]);

  // Leaving the page (or hiding the tab) sends what is held instead of dropping it. Mounted once (refs), so a
  // re-render never sends early.
  const sendRef = useRef(send);
  sendRef.current = send;
  useEffect(() => {
    const flush = () => { for (const h of heldRef.current) { clearTimeout(h.timer); void sendRef.current(h); } };
    const onVis = () => { if (document.visibilityState === "hidden") flush(); };
    document.addEventListener("visibilitychange", onVis);
    return () => { document.removeEventListener("visibilitychange", onVis); flush(); };
  }, []);

  const hidden = useMemo(() => new Set(held.map((h) => h.item.id)), [held]);
  const deck = useMemo(() => deckOf(inbox?.items ?? [], hidden), [inbox, hidden]);
  const i = clampIndex(idx, deck.length);
  const cur = deck[i] ?? null;
  const group = cur && inbox ? batchGroupOf(cur, inbox.groups) : null;

  const decide = useCallback((item: InboxItem, decision: Decision, confirmed = false) => {
    if (!canDecide) return;
    if (needsConfirm(item, decision) && !confirmed) { setConfirm({ kind: "high", item }); return; }
    const until = Date.now() + UNDO_MS;
    const h: Held = { item, decision, until, timer: setTimeout(() => void sendRef.current({ item, decision }), UNDO_MS) };
    setHeld((x) => [...x, h]);
    setExpanded(false);
  }, [canDecide]);

  const undo = (id: string) => setHeld((x) => {
    const h = x.find((y) => y.item.id === id);
    if (h) clearTimeout(h.timer);
    return x.filter((y) => y.item.id !== id);
  });

  const runBatch = async (g: InboxGroup) => {
    setConfirm(null);
    try {
      await call("POST", "/approvals/batch", { filter: { project_id: g.project_id, action: g.action }, decision: "approve", expected_count: g.count, include_high: false });
    } catch { setErrors((e) => [...e.slice(-2), t("approvalInbox.batchError")]); }
    void load();
  };

  // Keyboard: A approve, R reject, J/K next/prev, E expand. Ignored while typing or with a dialog open.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement | null)?.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
      if (confirm) {
        if (e.key === "Escape") setConfirm(null);
        return;
      }
      const c = keyToCommand(e);
      if (!c) return;
      if ((c === "approve" || c === "reject") && cur) { e.preventDefault(); decide(cur, c); }
      else if (c === "next") { e.preventDefault(); setIdx(clampIndex(i + 1, deck.length)); setExpanded(false); }
      else if (c === "prev") { e.preventDefault(); setIdx(clampIndex(i - 1, deck.length)); setExpanded(false); }
      else if (c === "expand") { e.preventDefault(); setExpanded((v) => !v); }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [confirm, cur, decide, deck.length, i]);

  // Swipe (touch / pen): right approves, left rejects. High risk opens the confirm instead of approving.
  const onDown = (e: React.PointerEvent) => { if (e.pointerType !== "mouse" && canDecide) drag.current = { x: e.clientX, y: e.clientY, id: e.pointerId }; };
  const onMove = (e: React.PointerEvent) => { if (drag.current && drag.current.id === e.pointerId) setDx(e.clientX - drag.current.x); };
  const onUp = (e: React.PointerEvent) => {
    const d = drag.current;
    drag.current = null;
    setDx(0);
    if (!d || !cur) return;
    const intent = swipeIntent(e.clientX - d.x, e.clientY - d.y);
    if (intent) decide(cur, intent);
  };

  const last = held[held.length - 1];
  const who = (it: InboxItem) => names[it.agent_id] ?? it.agent_id;
  const tips = (inbox?.suggestions ?? []).filter((s) => !hiddenTips.has(s.action));

  return (
    <main data-testid="inbox-page" className="mx-auto min-h-dvh max-w-xl space-y-3 bg-bg px-4 pb-[max(2.5rem,env(safe-area-inset-bottom))] pt-5">
      <header className="flex items-start justify-between gap-3">
        <div>
          <h1 className="text-xl font-bold text-ink">{t("approvalInbox.title")}</h1>
          <p className="text-[13px] text-mute">{t("approvalInbox.subtitle")}</p>
          {signedIn && <OrgIndicator className="mt-1 text-[12px] font-medium text-ink2" />}
        </div>
        {deck.length > 0 && <span data-testid="inbox-position" className="shrink-0 rounded-md bg-panel2 px-2 py-1 font-mono text-[12px] text-ink2">{t("approvalInbox.position", { n: i + 1, total: deck.length })}</span>}
      </header>
      {failed && (
        <div role="alert" className="flex items-center justify-between gap-3 rounded-lg border border-err/40 bg-err/10 p-3 text-[13px] text-err">
          <span>{t("approvalInbox.loadError")}</span>
          <button type="button" onClick={() => void load()} className="min-h-[44px] rounded-md border border-err/40 px-3 font-semibold">{t("approvalInbox.retry")}</button>
        </div>
      )}
      {!canDecide && inbox && <p className="rounded-lg border border-line bg-panel p-3 text-[13px] text-ink2">{t("approvals.noPermission")}</p>}
      {errors.map((m, k) => <p key={k} role="alert" className="rounded-lg border border-err/40 bg-err/10 p-2 text-[12px] text-err">{m}</p>)}

      {inbox && deck.length === 0 && !held.length && <p data-testid="inbox-empty" className="py-10 text-center text-[14px] text-mute">{t("approvalInbox.empty")}</p>}

      {cur && (
        <article data-testid={`inbox-card-${cur.id}`} data-risk={cur.risk} onPointerDown={onDown} onPointerMove={onMove} onPointerUp={onUp} onPointerCancel={() => { drag.current = null; setDx(0); }}
          style={{ transform: dx ? `translateX(${dx}px) rotate(${dx / 40}deg)` : undefined, touchAction: "pan-y" }}
          className={`select-none rounded-xl border bg-panel p-4 shadow-soft transition-[border-color] ${dx > 60 ? "border-ok" : dx < -60 ? "border-err" : cur.high_risk ? "border-err/50" : "border-line"}`}>
          <div className="flex items-start justify-between gap-3">
            <h2 className="text-[16px] font-semibold leading-snug text-ink">{cur.title}</h2>
            <RiskBadge risk={cur.risk} />
          </div>
          <dl className="mt-2 grid grid-cols-2 gap-2 text-[12px]">
            <Fact k={t("approvalInbox.who")} v={who(cur)} />
            <Fact k={t("approvalInbox.what")} v={trOr(`pv.group.${cur.action}`, cur.action)} mono />
            <Fact k={t("approvalInbox.cost")} v={fmtUsd(cur.task_cost_usd)} mono />
            <Fact k={t("approvalInbox.project")} v={cur.project_name || (cur.project_id ? cur.project_id : t("approvalInbox.noProject"))} />
          </dl>
          {(cur.required_approvals ?? 1) > 1 && <p className="mt-2 text-[12px] text-ink2">{t("approvals.mobile.progress", { done: cur.decisions?.length ?? 0, total: cur.required_approvals ?? 1 })} · {t("approvals.mobile.doubleApproval")}</p>}
          {cur.counterargument?.summary && (
            <div data-testid="inbox-devil-summary" className="mt-2 rounded-md border border-amber-600/30 bg-amber-500/10 px-2.5 py-1.5 text-[12px] text-amber-900">
              <div className="text-[10px] font-semibold uppercase tracking-wide">{t("approvals.devil.title")}</div>
              <p>{cur.counterargument.summary}</p>
              {expanded && cur.counterargument.risks.length > 0 && <ul className="mt-1 list-disc pl-4">{cur.counterargument.risks.map((r, k) => <li key={k}>{r}</li>)}</ul>}
            </div>
          )}
          {expanded && (
            <div data-testid="inbox-expanded" className="mt-2 space-y-1 text-[13px] text-ink2">
              {cur.details && <p className="[overflow-wrap:anywhere]">{cur.details}</p>}
              {cur.task_title && <p className="text-[12px] text-mute">{t("approvalInbox.task", { title: cur.task_title })}</p>}
              {cur.high_risk && !cur.counterargument && <DevilsAdvocate approvalId={cur.id} />}
            </div>
          )}
          {group && canDecide && (
            <button type="button" data-testid={`inbox-batch-${group.key}`} onClick={() => setConfirm({ kind: "batch", group })}
              className="mt-3 min-h-[44px] w-full rounded-md border border-ok/40 bg-ok/5 px-3 text-[13px] font-semibold text-ok">
              {t("approvalInbox.batch", { count: group.count, project: group.project_name || group.project_id })}
            </button>
          )}
          {canDecide && (
            <div className="mt-3 grid grid-cols-2 gap-3">
              <button type="button" data-testid="inbox-reject" onClick={() => decide(cur, "reject")} className="min-h-[48px] rounded-md border border-err/40 bg-err/10 text-sm font-semibold text-err">{t("approvalInbox.reject")} <kbd className="ml-1 hidden rounded border border-err/30 px-1 text-[10px] sm:inline">R</kbd></button>
              <button type="button" data-testid="inbox-approve" onClick={() => decide(cur, "approve")} className="min-h-[48px] rounded-md border border-ok/40 bg-ok/10 text-sm font-semibold text-ok">{t("approvalInbox.approve")} <kbd className="ml-1 hidden rounded border border-ok/30 px-1 text-[10px] sm:inline">A</kbd></button>
            </div>
          )}
          <div className="mt-2 flex items-center justify-between gap-2 text-[12px]">
            <button type="button" data-testid="inbox-prev" disabled={i === 0} onClick={() => { setIdx(i - 1); setExpanded(false); }} className="min-h-[40px] rounded-md px-2 text-mute disabled:opacity-30">‹ {t("approvalInbox.prev")}</button>
            <button type="button" data-testid="inbox-expand" aria-expanded={expanded} onClick={() => setExpanded((v) => !v)} className="min-h-[40px] rounded-md px-2 font-semibold text-accent">{t(expanded ? "approvalInbox.collapse" : "approvalInbox.expand")}</button>
            <button type="button" data-testid="inbox-next" disabled={i >= deck.length - 1} onClick={() => { setIdx(i + 1); setExpanded(false); }} className="min-h-[40px] rounded-md px-2 text-mute disabled:opacity-30">{t("approvalInbox.next")} ›</button>
          </div>
        </article>
      )}
      {cur && <p className="hidden text-center text-[11px] text-mute sm:block">{t("approvalInbox.keys")}</p>}
      {cur && <p className="text-center text-[11px] text-mute sm:hidden">{t("approvalInbox.swipe")}</p>}

      {last && (
        <div data-testid="inbox-undo-bar" role="status" className="sticky bottom-3 flex items-center justify-between gap-3 rounded-lg border border-line bg-ink px-3 py-2 text-[13px] text-white shadow-soft">
          <span className="truncate">{t(last.decision === "approve" ? "approvalInbox.heldApprove" : "approvalInbox.heldReject", { title: last.item.title })}{held.length > 1 ? ` (+${held.length - 1})` : ""}</span>
          <button type="button" data-testid="inbox-undo" onClick={() => undo(last.item.id)} className="min-h-[40px] shrink-0 rounded-md border border-white/40 px-3 font-semibold">{t("approvalInbox.undo")}</button>
        </div>
      )}
      {held.length > 0 && <p className="text-center text-[11px] text-mute">{t("approvalInbox.undoHint", { seconds: Math.round(UNDO_MS / 1000) })}</p>}

      {tips.length > 0 && (
        <section className="space-y-2" aria-label={t("approvalInbox.tipsTitle")}>
          {tips.map((s) => (
            <div key={s.action} data-testid={`inbox-suggestion-${s.action}`} className="rounded-lg border border-accent/30 bg-accent/5 p-3 text-[12px] text-ink2">
              <div className="text-[10px] font-semibold uppercase tracking-wide text-accent">{t("approvalInbox.tipsTitle")}</div>
              <p>{t("approvalInbox.tip", { pct: Math.round(s.rate * 1000) / 10, approved: s.approved, decided: s.decided, action: trOr(`pv.group.${s.action}`, s.action), days: s.window_days })}</p>
              <p className="mt-1 text-mute">{t("approvalInbox.tipWhy", { min: inbox?.rules.suggest_min_decisions ?? 20 })}</p>
              <button type="button" onClick={() => setHiddenTips((x) => new Set([...x, s.action]))} className="mt-1 text-[11px] font-semibold text-accent underline">{t("approvalInbox.tipDismiss")}</button>
            </div>
          ))}
        </section>
      )}

      {confirm && (
        <div role="dialog" aria-modal="true" data-testid="inbox-confirm" className="fixed inset-0 z-50 flex items-end justify-center bg-black/40 p-4 sm:items-center" onClick={() => setConfirm(null)}>
          <div className="w-full max-w-md rounded-xl bg-panel p-4 shadow-soft" onClick={(e) => e.stopPropagation()}>
            {confirm.kind === "high" ? (
              <>
                <h3 className="text-[16px] font-semibold text-ink">{t("approvalInbox.confirmHighTitle")}</h3>
                <p className="mt-1 text-[13px] text-ink2">{confirm.item.title}</p>
                <p className="mt-2 text-[12px] text-err">{t("approvalInbox.confirmHighBody")}</p>
              </>
            ) : (
              <>
                <h3 className="text-[16px] font-semibold text-ink">{t("approvalInbox.confirmBatchTitle", { count: confirm.group.count })}</h3>
                <ul className="mt-2 max-h-40 space-y-1 overflow-y-auto text-[12px] text-mute">
                  {(inbox?.items ?? []).filter((x) => confirm.group.approval_ids.includes(x.id)).map((x) => <li key={x.id}>• {x.title}</li>)}
                </ul>
                <p className="mt-2 text-[12px] text-ink2">{t("approvalInbox.confirmBatchBody")}</p>
              </>
            )}
            <div className="mt-4 flex justify-end gap-2">
              <button type="button" onClick={() => setConfirm(null)} className="min-h-[44px] rounded-md border border-line px-4 text-sm">{t("common.close")}</button>
              <button type="button" data-testid="inbox-confirm-go" autoFocus
                onClick={() => { if (confirm.kind === "high") { const it = confirm.item; setConfirm(null); decide(it, "approve", true); } else void runBatch(confirm.group); }}
                className="min-h-[44px] rounded-md border border-ok/40 bg-ok/10 px-4 text-sm font-semibold text-ok">{t(confirm.kind === "high" ? "approvalInbox.confirmHighGo" : "approvalInbox.confirmBatchGo")}</button>
            </div>
          </div>
        </div>
      )}
      <Link href="/approvals" className="block min-h-[44px] pt-3 text-center text-[13px] font-medium text-accent underline">{t("approvalInbox.listView")}</Link>
    </main>
  );
}

const Fact = ({ k, v, mono }: { k: string; v: string; mono?: boolean }) => (
  <div className="rounded-lg bg-panel2 px-2 py-1.5"><dt className="text-[9px] font-semibold uppercase tracking-wide text-mute">{k}</dt><dd className={`truncate text-ink ${mono ? "font-mono" : ""}`}>{v}</dd></div>
);
