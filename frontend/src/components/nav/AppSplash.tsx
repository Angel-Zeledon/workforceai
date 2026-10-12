"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { usePreferences } from "@/lib/preferences";
import { AgentMark } from "../brand/AgentMark";
import { welcomeSeen } from "../onboarding/WelcomeOverlay";

const MIN_MS = 1100;    // long enough to read as a deliberate screen, short enough to never feel like a wait
const MAX_MS = 7000;    // failsafe: never trap the user behind it
const FADE_MS = 380;

/** Loading screen shown on EVERY entry to the app (each full page load), until the session is known.
 *  It is part of the server-rendered HTML, so it is on screen before any script runs. Returning users are greeted by name;
 *  a brand-new account sees the full welcome right after (components/onboarding/WelcomeOverlay). */
export function AppSplash() {
  const { t } = useT();
  const status = useSession((s) => s.status);
  const user = useSession((s) => s.user);
  const reduce = usePreferences((s) => s.prefs.reduceMotion);
  const [minDone, setMinDone] = useState(false);
  const [leaving, setLeaving] = useState(false);
  const [gone, setGone] = useState(false);

  useEffect(() => {
    const a = setTimeout(() => setMinDone(true), MIN_MS);
    const b = setTimeout(() => { setMinDone(true); setLeaving(true); }, MAX_MS);
    return () => { clearTimeout(a); clearTimeout(b); };
  }, []);
  useEffect(() => { if (status !== "loading" && minDone) setLeaving(true); }, [status, minDone]);
  useEffect(() => {
    if (!leaving) return;
    const id = setTimeout(() => setGone(true), FADE_MS);
    return () => clearTimeout(id);
  }, [leaving]);

  if (gone) return null;
  const first = (user?.name || "").trim().split(/\s+/)[0];
  const returning = !!user && welcomeSeen(user.id);
  const line = returning && first ? t("splash.hello", { name: first }) : t("splash.loading");
  return (
    <div data-testid="splash" role="status" aria-live="polite" aria-label={t("splash.loading")} data-leaving={leaving ? "1" : "0"} data-reduce={reduce ? "1" : "0"}
      className="wf-splash fixed inset-0 z-[9500] flex flex-col items-center justify-center bg-navy text-white">
      <div className="wf-splash-mark"><AgentMark size={84} tone="light" /></div>
      <div className="mt-5 text-[19px] font-semibold tracking-tight">AI Workforce OS</div>
      <div className="wf-splash-line mt-1.5 min-h-[20px] text-[13.5px] text-white/70">{line}</div>
      <div aria-hidden className="mt-6 h-[3px] w-44 overflow-hidden rounded-full bg-white/15"><div className="wf-splash-bar h-full w-1/3 rounded-full bg-[#5eead4]" /></div>
    </div>
  );
}
