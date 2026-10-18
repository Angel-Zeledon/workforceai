"use client";
import { useEffect } from "react";
import { useT } from "@/lib/i18n";
import type { SectionId } from "@/lib/navBridge";
import { Icon } from "../icons";
import { useTour } from "./store";
import { tourForSection } from "./tours";

/** The "?" in the top bar. Renders nothing on screens without a tour, so it is never a control that does nothing.
 *  Stays mounted (lit) while the tour runs and closes it when pressed again. Leaving the screen abandons the tour. */
export function TourLauncher({ section }: { section: SectionId }) {
  const { t, locale } = useT();
  const tour = tourForSection(section);
  const activeId = useTour((s) => s.activeId);
  const stop = useTour((s) => s.stop);
  const start = useTour((s) => s.start);
  useEffect(() => { stop(); }, [section, stop]);
  if (!tour) return null;
  const running = activeId === tour.id;
  const name = (locale === "en" ? tour.copy.en : tour.copy.es)[tour.name] ?? "";
  const label = running ? t("tour.close") : `${t("tour.launch")}: ${name}`;
  return (
    <button type="button" data-testid="tour-launch" aria-pressed={running} onClick={() => (running ? stop() : start(tour.id))} title={label} aria-label={label}
      className={`relative z-[9100] inline-flex h-10 w-10 items-center sm:h-8 sm:w-8 justify-center rounded-lg border transition ${running ? "border-accent bg-accent-soft text-accent" : "border-line text-mute hover:border-line-strong hover:text-ink"}`}>
      <Icon name="help" size={16} />
    </button>
  );
}
