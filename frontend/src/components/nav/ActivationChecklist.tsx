"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { useRewards } from "@/lib/rewards";
import { useSession } from "@/lib/session";
import { go } from "@/lib/navBridge";
import { toast } from "@/lib/toast";
import { Icon, type IconName } from "../icons";

/**
 * "Primeros pasos": four real actions that show the product working (ask, meet an employee, decide an approval, spend
 * the welcome coins). Progress comes from what the user actually did, not from clicking a box. Each step, once done,
 * stays done (remembered per account), and the whole pill disappears when everything is complete.
 */
type StepId = "ask" | "meet" | "approve" | "shop";
const STEPS: { id: StepId; icon: IconName }[] = [
  { id: "ask", icon: "chat" },
  { id: "meet", icon: "users" },
  { id: "approve", icon: "shield" },
  { id: "shop", icon: "bag" },
];
const key = (uid: string, s: string) => `wf.act.${uid}.${s}`;
const get = (k: string) => { try { return localStorage.getItem(k) === "1"; } catch { return false; } };
const put = (k: string) => { try { localStorage.setItem(k, "1"); } catch { /* storage unavailable: progress is just not remembered */ } };

export function ActivationChecklist() {
  const { t } = useT();
  const uid = useSession((s) => s.user?.id ?? "anon");
  const asked = useStore((s) => Object.keys(s.requests).length > 0);
  const decided = useStore((s) => Object.values(s.approvals).some((a) => a.status !== "pending"));
  const selected = useStore((s) => s.selectedAgentId);
  const bought = useRewards((s) => Object.keys(s.owned).length > 0);
  const [done, setDone] = useState<Record<StepId, boolean>>({ ask: false, meet: false, approve: false, shop: false });
  const [dismissed, setDismissed] = useState(true); // hidden until we know (avoids a flash)
  const [open, setOpen] = useState(false);
  const wrap = useRef<HTMLDivElement>(null);
  const announced = useRef(false);

  useEffect(() => { void useRewards.getState().load(); }, []);
  // Load what this account already completed.
  useEffect(() => {
    setDone({ ask: get(key(uid, "ask")), meet: get(key(uid, "meet")), approve: get(key(uid, "approve")), shop: get(key(uid, "shop")) });
    setDismissed(get(key(uid, "dismissed")));
    announced.current = false;
  }, [uid]);
  // Remember each step the moment its real condition becomes true.
  const live: Record<StepId, boolean> = useMemo(() => ({ ask: asked, meet: !!selected, approve: decided, shop: bought }), [asked, selected, decided, bought]);
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
    if (id === "shop") { go("/shop"); return; }
    go("/");
    if (id === "ask") setTimeout(() => document.querySelector<HTMLInputElement>('[data-testid="command-input"]')?.focus(), 450);
  };
  return (
    <div ref={wrap} className="relative">
      <button type="button" data-testid="activation-pill" aria-expanded={open} aria-haspopup="dialog" onClick={() => setOpen((o) => !o)}
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
