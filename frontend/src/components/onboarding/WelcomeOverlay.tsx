"use client";
import { useEffect, useRef, useState } from "react";
import { fmtNumber, useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { usePreferences } from "@/lib/preferences";
import { useRewards } from "@/lib/rewards";
import { AgentMark } from "../brand/AgentMark";
import { CoinIcon } from "../shop/Coin";
import { Icon } from "../icons";
import { useTour } from "../tour/store";

/** One welcome per account (per browser when auth is off): what this office is, the starter coins, and where to begin. */
const seenKey = (uid: string) => `wf.welcome.v1.${uid}`;
const COINS = 14; // falling coins in the animation
export const welcomeSeen = (uid: string) => { try { return localStorage.getItem(seenKey(uid)) === "1"; } catch { return false; } };
const read = (k: string) => { try { return localStorage.getItem(k) === "1"; } catch { return false; } };
const write = (k: string) => { try { localStorage.setItem(k, "1"); } catch { /* storage unavailable: it will show again */ } };

export function WelcomeOverlay() {
  const { t } = useT();
  const status = useSession((s) => s.status);
  const user = useSession((s) => s.user);
  const reduce = usePreferences((s) => s.prefs.reduceMotion);
  const balance = useRewards((s) => s.wallet.balance);
  const [open, setOpen] = useState(false);
  const [shown, setShown] = useState(0);
  const card = useRef<HTMLDivElement>(null);
  const key = seenKey(user?.id ?? "anon");
  const noMotion = reduce || (typeof window !== "undefined" && window.matchMedia?.("(prefers-reduced-motion: reduce)").matches);

  // Open once, after the scene has had a moment to appear. Needs a settled session (signed in, or auth turned off).
  useEffect(() => {
    if (status !== "authenticated" && status !== "disabled") return;
    if (read(key)) return;
    void useRewards.getState().load();
    const id = setTimeout(() => setOpen(true), 900);
    return () => clearTimeout(id);
  }, [status, key]);

  // Count the starter coins up, unless motion is reduced.
  useEffect(() => {
    if (!open) return;
    if (noMotion || balance <= 0) { setShown(balance); return; }
    let raf = 0;
    const t0 = performance.now();
    const step = (now: number) => {
      const p = Math.min(1, (now - t0) / 1100);
      setShown(Math.round(balance * (1 - Math.pow(1 - p, 3))));
      if (p < 1) raf = requestAnimationFrame(step);
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [open, balance, noMotion]);

  const close = (tour: boolean) => {
    write(key);
    setOpen(false);
    if (tour) setTimeout(() => useTour.getState().start("office-v1"), 250);
  };

  useEffect(() => {
    if (!open) return;
    card.current?.focus();
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") close(false); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  if (!open) return null;
  const first = (user?.name || "").trim().split(/\s+/)[0];
  const steps: { icon: "chat" | "office" | "shield"; k: string }[] = [
    { icon: "chat", k: "1" }, { icon: "office", k: "2" }, { icon: "shield", k: "3" },
  ];
  return (
    <div className="fixed inset-0 z-[8800] flex items-center justify-center bg-ink/45 p-4 ac-fade" data-testid="welcome" onMouseDown={(e) => { if (e.target === e.currentTarget) close(false); }}>
      <div ref={card} tabIndex={-1} role="dialog" aria-modal="true" aria-label={t("welcome.aria")}
        className="wf-welcome relative w-full max-w-md overflow-hidden rounded-2xl border border-line bg-panel p-6 text-center shadow-float outline-none focus-visible:shadow-float">
        {!noMotion && balance > 0 && (
          <div aria-hidden className="pointer-events-none absolute inset-x-0 top-0 h-full overflow-hidden">
            {Array.from({ length: COINS }).map((_, i) => (
              <span key={i} className="wf-coin" style={{ left: `${(i * 97) % 100}%`, animationDelay: `${(i % 7) * 0.12 + 0.2}s`, animationDuration: `${1.5 + (i % 5) * 0.18}s` }}>
                <CoinIcon size={14 + (i % 3) * 3} />
              </span>
            ))}
          </div>
        )}
        <div className={`relative mx-auto mb-3 w-fit ${noMotion ? "" : "wf-wave"}`}><AgentMark size={64} /></div>
        <h2 className="relative text-xl font-semibold tracking-tight text-ink">{first ? t("welcome.titleName", { name: first }) : t("welcome.title")}</h2>
        <p className="relative mx-auto mt-1.5 max-w-sm text-[13.5px] leading-relaxed text-ink2">{t("welcome.sub")}</p>

        {balance > 0 && (
          <div data-testid="welcome-coins" className="relative mx-auto mt-4 flex w-fit items-center gap-2 rounded-full border border-amber-300 bg-amber-100/70 px-4 py-1.5">
            <CoinIcon size={18} />
            <b className="font-mono text-[15px] text-amber-800">+{fmtNumber(shown)}</b>
            <span className="text-[12.5px] font-medium text-amber-800">{t("welcome.coins")}</span>
          </div>
        )}
        {balance > 0 && <p className="relative mt-1.5 text-[11.5px] text-mute">{t("welcome.coinsHint")}</p>}

        <ol className="relative mt-5 grid gap-2 text-left">
          {steps.map((s) => (
            <li key={s.k} className="flex items-start gap-3 rounded-xl border border-line bg-panel2 px-3 py-2.5">
              <span className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-accent-soft text-accent"><Icon name={s.icon} size={15} /></span>
              <span className="min-w-0 text-[12.5px] leading-snug text-ink2"><b className="block text-[13px] text-ink">{t(`welcome.step${s.k}.t`)}</b>{t(`welcome.step${s.k}.d`)}</span>
            </li>
          ))}
        </ol>

        <div className="relative mt-5 flex flex-col gap-2 sm:flex-row-reverse">
          <button type="button" data-testid="welcome-start" onClick={() => close(false)} className="flex-1 rounded-lg bg-accent px-4 py-2.5 text-sm font-semibold text-white transition hover:bg-accent-hover">{t("welcome.start")}</button>
          <button type="button" data-testid="welcome-tour" onClick={() => close(true)} className="flex-1 rounded-lg border border-line px-4 py-2.5 text-sm font-semibold text-ink transition hover:bg-panel2">{t("welcome.tour")}</button>
        </div>
      </div>
    </div>
  );
}
