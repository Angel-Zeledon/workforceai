"use client";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { orgApi } from "@/lib/orgConfigApi";
import { connApi } from "@/lib/connections/api";
import { notifyApi, type DailyGoal } from "@/lib/notifyApi";
import { useRewards } from "@/lib/rewards";
import { useSession } from "@/lib/session";
import { go } from "@/lib/navBridge";
import { toast } from "@/lib/toast";
import { Icon, type IconName } from "../icons";

/**
 * "Primeros pasos": four real steps that take an account from empty to working: (1) tell the office about the business,
 * (2) connect a tool, (3) choose how much freedom the team has, (4) ask the first task. Progress comes from what the
 * account really has (the organization settings, an active connection, an agent above "suggest", a request), not from
 * clicking a box. Each step, once done, stays done (remembered per account), and the pill disappears when all are done.
 */
type StepId = "context" | "connect" | "autonomy" | "task";
const STEPS: { id: StepId; icon: IconName }[] = [
  { id: "context", icon: "flag" },
  { id: "connect", icon: "plug" },
  { id: "autonomy", icon: "shield" },
  { id: "task", icon: "chat" },
];
const key = (uid: string, s: string) => `wf.act.${uid}.${s}`;
const get = (k: string) => { try { return localStorage.getItem(k) === "1"; } catch { return false; } };
const put = (k: string) => { try { localStorage.setItem(k, "1"); } catch { /* storage unavailable: progress is just not remembered */ } };

function ActivationPill() {
  const { t } = useT();
  const uid = useSession((s) => s.user?.id ?? "anon");
  const asked = useStore((s) => Object.keys(s.requests).length > 0);
  const freer = useStore((s) => Object.values(s.agents).some((a) => a.autonomy === "rules" || a.autonomy === "autonomous"));
  const [configured, setConfigured] = useState(false); // the office was set up: business type, tone and the autonomy of the pack
  const [connected, setConnected] = useState(false);
  const [done, setDone] = useState<Record<StepId, boolean>>({ context: false, connect: false, autonomy: false, task: false });
  const [dismissed, setDismissed] = useState(true); // hidden until we know (avoids a flash)
  const [open, setOpen] = useState(false);
  const wrap = useRef<HTMLDivElement>(null);
  const announced = useRef(false);

  // What the account really has. Best effort: an older backend or a missing permission just leaves the step open.
  const refresh = useCallback(() => {
    orgApi.settings().then((o) => setConfigured(!!(o.onboarding_completed || (o.configured && o.business_type)))).catch(() => {});
    connApi.list().then((l) => setConnected(l.some((c) => c.status === "active"))).catch(() => {});
  }, []);
  useEffect(() => { refresh(); }, [refresh, uid]);
  // Load what this account already completed.
  useEffect(() => {
    setDone({ context: get(key(uid, "context")), connect: get(key(uid, "connect")), autonomy: get(key(uid, "autonomy")), task: get(key(uid, "task")) });
    setDismissed(get(key(uid, "dismissed")));
    announced.current = false;
  }, [uid]);
  // Remember each step the moment its real condition becomes true.
  const live: Record<StepId, boolean> = useMemo(() => ({ context: configured, connect: connected, autonomy: configured || freer, task: asked }), [configured, connected, freer, asked]);
  useEffect(() => {
    setDone((d) => {
      const next = { ...d };
      let changed = false;
      for (const s of STEPS) if (live[s.id] && !d[s.id]) { next[s.id] = true; put(key(uid, s.id)); changed = true; }
      return changed ? next : d;
    });
  }, [live, uid]);

  const count = STEPS.filter((s) => done[s.id]).length;
  const all = count === STEPS.length;
  useEffect(() => {
    if (all && !announced.current && !get(key(uid, "complete"))) {
      announced.current = true; put(key(uid, "complete"));
      toast(t("act.complete"));
    }
  }, [all, uid, t]);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => { if (wrap.current && !wrap.current.contains(e.target as Node)) setOpen(false); };
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(false); };
    window.addEventListener("mousedown", onDown); window.addEventListener("keydown", onKey);
    return () => { window.removeEventListener("mousedown", onDown); window.removeEventListener("keydown", onKey); };
  }, [open]);

  if (dismissed || all) return null;

  const run = (id: StepId) => {
    setOpen(false);
    if (id === "context") { go("/settings"); return; }
    if (id === "connect") { go("/settings?s=connections"); return; }
    go("/");
    if (id === "task") setTimeout(() => document.querySelector<HTMLInputElement>('[data-testid="command-input"]')?.focus(), 450);
  };
  return (
    <div ref={wrap} className="relative">
      <button type="button" data-testid="activation-pill" aria-expanded={open} aria-haspopup="dialog" onClick={() => { if (!open) refresh(); setOpen((o) => !o); }}
        title={t("act.title")} className="flex h-8 items-center gap-1.5 rounded-lg border border-accent/30 bg-accent-soft px-2.5 text-[11.5px] font-semibold text-accent transition hover:border-accent">
        <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden><circle cx="8" cy="8" r="6.2" fill="none" stroke="currentColor" strokeOpacity=".25" strokeWidth="2" />
          <circle cx="8" cy="8" r="6.2" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeDasharray={`${(count / STEPS.length) * 39} 39`} transform="rotate(-90 8 8)" /></svg>
        <span className="hidden sm:inline">{t("act.title")}</span>
        <span className="font-mono">{count}/{STEPS.length}</span>
      </button>
      {open && (
        <div role="dialog" aria-label={t("act.title")} data-testid="activation-panel"
          className="ac-pop absolute right-0 top-full z-[70] mt-2 w-[330px] max-w-[calc(100vw-24px)] rounded-xl border border-line bg-panel p-3 text-ink shadow-float">
          <div className="mb-2 flex items-baseline justify-between px-1">
            <b className="text-[13.5px]">{t("act.title")}</b>
            <span className="text-[11px] text-mute">{t("act.progress", { n: count, total: STEPS.length })}</span>
          </div>
          <ul className="space-y-1.5">
            {STEPS.map((s) => (
              <li key={s.id} data-testid={`act-${s.id}`} data-done={done[s.id] ? "1" : "0"} className={`flex items-start gap-2.5 rounded-lg border px-2.5 py-2 ${done[s.id] ? "border-line bg-panel2 opacity-70" : "border-line"}`}>
                <span className={`mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full ${done[s.id] ? "bg-ok text-white" : "border border-line-strong text-mute"}`}>
                  {done[s.id] ? <Icon name="check" size={12} /> : <Icon name={s.icon} size={11} />}
                </span>
                <span className="min-w-0 flex-1">
                  <span className={`block text-[12.5px] font-semibold ${done[s.id] ? "line-through" : ""}`}>{t(`act.${s.id}.t`)}</span>
                  <span className="block text-[11.5px] leading-snug text-mute">{t(`act.${s.id}.d`)}</span>
                </span>
                {!done[s.id] && <button type="button" onClick={() => run(s.id)} className="shrink-0 rounded-md border border-line px-2 py-1 text-[11px] font-semibold text-accent hover:bg-accent-soft">{t(`act.${s.id}.a`)}</button>}
              </li>
            ))}
          </ul>
          <button type="button" data-testid="activation-hide" onClick={() => { put(key(uid, "dismissed")); setDismissed(true); setOpen(false); }}
            className="mt-2 w-full rounded-md px-2 py-1.5 text-center text-[11px] font-medium text-mute hover:bg-panel2 hover:text-ink">{t("act.hide")}</button>
        </div>
      )}
    </div>
  );
}

/**
 * Daily goal: one mini-goal per day, paid in coins by the server only when real work was finished (rewards/daily.go).
 * Shows the progress; hidden when the server has the goal off or the rewards system is unavailable.
 */
function DailyGoalChip() {
  const { t } = useT();
  const [g, setG] = useState<DailyGoal | null>(null);
  const earnTick = useRewards((s) => s.earnTick); // a coin was just earned: the progress may have moved
  useEffect(() => {
    let live = true;
    notifyApi.dailyGoal().then((d) => { if (live) setG(d); }).catch(() => { if (live) setG(null); });
    return () => { live = false; };
  }, [earnTick]);
  if (!g || !g.enabled) return null;
  const shown = Math.min(g.progress, g.target);
  const pct = Math.round((shown / g.target) * 100);
  return (
    <span data-testid="daily-goal" data-done={g.done ? "1" : "0"} title={`${t("daily.title")}: ${t(`daily.goal.${g.id}`)}. ${t("daily.tip")}`}
      className={`hidden h-8 items-center gap-1.5 rounded-lg border px-2.5 text-[11.5px] font-semibold md:flex ${g.done ? "border-ok/40 bg-ok/10 text-ok" : "border-amber-300 bg-amber-100/70 text-amber-800"}`}>
      <Icon name={g.done ? "check" : "bag"} size={12} />
      <span className="max-w-[170px] truncate">{t(`daily.goal.${g.id}`)}</span>
      <span className="font-mono">{g.done ? t("daily.done") : `${shown}/${g.target}`}</span>
      {!g.done && <span className="relative h-1 w-8 overflow-hidden rounded-full bg-black/10" aria-hidden><span className="absolute inset-y-0 left-0 bg-amber-500" style={{ width: `${pct}%` }} /></span>}
      {!g.done && <span className="font-mono">{t("daily.reward", { coins: g.reward })}</span>}
    </span>
  );
}

export function ActivationChecklist() {
  return (
    <>
      <DailyGoalChip />
      <ActivationPill />
    </>
  );
}
