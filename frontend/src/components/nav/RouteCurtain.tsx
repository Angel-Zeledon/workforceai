"use client";
import { useEffect, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { SECTIONS, type SectionId } from "@/lib/navBridge";
import { usePreferences } from "@/lib/preferences";
import { Icon, type IconName } from "../icons";

const MS = 620;

/** Transition between sections: a short curtain with the destination's name that lifts to reveal the screen.
 *  Plays on every change of section (not on the first one, which is the splash's job), never blocks clicks. */
export function RouteCurtain({ section }: { section: SectionId }) {
  const { t } = useT();
  const reduce = usePreferences((s) => s.prefs.reduceMotion);
  const prev = useRef<SectionId | null>(null);
  const [run, setRun] = useState<{ n: number; section: SectionId } | null>(null);

  useEffect(() => {
    const before = prev.current;
    prev.current = section;
    if (before === null || before === section) return;
    if (reduce || window.matchMedia?.("(prefers-reduced-motion: reduce)").matches) return;
    setRun((r) => ({ n: (r?.n ?? 0) + 1, section }));
    const id = setTimeout(() => setRun(null), MS + 40);
    return () => clearTimeout(id);
  }, [section, reduce]);

  if (!run) return null;
  const meta: { label: string; icon: IconName } = run.section === "settings"
    ? { label: "nav.settings", icon: "settings" }
    : run.section === "dashboard"
      ? { label: "mode.dashboard", icon: "grid" }
      : (SECTIONS.find((s) => s.id === run.section) ?? SECTIONS[0]);
  return (
    <div key={run.n} aria-hidden data-testid="curtain" className="wf-curtain pointer-events-none absolute inset-0 z-[45] flex items-center justify-center bg-bg">
      <div className="wf-curtain-label flex items-center gap-2.5 text-ink">
        <span className="flex h-10 w-10 items-center justify-center rounded-xl bg-accent-soft text-accent"><Icon name={meta.icon} size={20} /></span>
        <span className="text-[17px] font-semibold tracking-tight">{t(meta.label)}</span>
      </div>
    </div>
  );
}
