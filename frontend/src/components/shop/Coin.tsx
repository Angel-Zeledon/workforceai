"use client";
import { useEffect, useRef, useState } from "react";
import { useRewards, useShopNav } from "@/lib/rewards";
import { usePreferences } from "@/lib/preferences";
import { useT, fmtNumber } from "@/lib/i18n";
import { useStore } from "@/lib/store";

/** The coin: a gold disc with a star. Deliberately not a "$": coins are earned by work, they are not money. */
export function CoinIcon({ size = 14, className = "" }: { size?: number; className?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden focusable="false" className={className}>
      <circle cx="12" cy="12" r="10.5" fill="#e8b53a" stroke="#a86208" strokeWidth="1.5" />
      <circle cx="12" cy="12" r="7.6" fill="none" stroke="#f7dd8a" strokeWidth="1.2" />
      <path d="M12 6.6l1.65 3.4 3.7.5-2.7 2.6.65 3.7L12 15l-3.3 1.8.65-3.7-2.7-2.6 3.7-.5z" fill="#fff6d6" stroke="#a86208" strokeWidth=".6" strokeLinejoin="round" />
    </svg>
  );
}

/** Header counter: the organization's coins, next to (and clearly different from) the dollar cost. Opens the Shop. */
export function CoinCounter() {
  const { t } = useT();
  const available = useRewards((s) => s.available);
  const balance = useRewards((s) => s.wallet.balance);
  const tick = useRewards((s) => s.earnTick);
  const toggle = () => useShopNav.getState().toggle();
  const [pulse, setPulse] = useState(false);
  const first = useRef(true);
  useEffect(() => {
    if (first.current) { first.current = false; return; }
    if (usePreferences.getState().prefs.reduceMotion) return;
    setPulse(true);
    const id = setTimeout(() => setPulse(false), 900);
    return () => clearTimeout(id);
  }, [tick]);
  if (!available) return null;
  return (
    <button type="button" data-testid="hud-coins" data-balance={balance} onClick={toggle} title={t("hud.coinsTitle")} aria-label={`${t("hud.coins")}: ${balance}`}
      className={`flex items-center gap-1.5 rounded-full border border-amber-300 bg-amber-100/70 px-2.5 py-1 text-[11px] font-medium text-amber-800 transition hover:bg-amber-100 ${pulse ? "scale-110 shadow-pop" : ""}`}>
      <CoinIcon size={14} />
      <b data-testid="hud-coins-value" className="font-mono text-[12px] font-semibold tabular-nums">{fmtNumber(balance, 0)}</b>
    </button>
  );
}

/** Screen-reader (and test) announcement of the coins a finished task just paid; the visible "+N" lives in the 3D scene. */
export function CoinAnnouncer() {
  const { t } = useT();
  const floats = useRewards((s) => s.floats);
  const agents = useStore((s) => s.agents);
  const last = floats[floats.length - 1];
  return (
    <div data-testid="coin-announcer" aria-live="polite" role="status" className="sr-only">
      {last ? t("coins.earnedAria", { agent: agents[last.agent_id]?.name ?? last.agent_id, count: last.amount }) : ""}
    </div>
  );
}
