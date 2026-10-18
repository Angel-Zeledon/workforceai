"use client";
import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties } from "react";
import { useT } from "@/lib/i18n";
import { Icon } from "../icons";
import { activeTour, useTour } from "./store";
import { anchorSelector, stepShown } from "./anchors";
import type { TourDefinition } from "./types";

const CARD_W = 340;
const PANEL_W = 460; // un-anchored step: a deliberate centred card, not a tooltip that lost its target
const PAD = 8;
const GAP = 12;

interface Rect { top: number; left: number; width: number; height: number }

const anchorEl = (anchor?: string) => (anchor ? document.querySelector<HTMLElement>(anchorSelector(anchor)) : null);

function anchorRect(anchor?: string): Rect | null {
  const el = anchorEl(anchor);
  if (!el) return null;
  const r = el.getBoundingClientRect();
  if (r.width === 0 && r.height === 0) return null;
  return { top: r.top - PAD, left: r.left - PAD, width: r.width + PAD * 2, height: r.height + PAD * 2 };
}

function copyOf(tour: TourDefinition, locale: string, key: string) {
  return (locale === "en" ? tour.copy.en : tour.copy.es)[key] ?? tour.copy.es[key] ?? key;
}

export function TourOverlay() {
  const activeId = useTour((s) => s.activeId);
  const stepIndex = useTour((s) => s.stepIndex);
  const { next, back, stop } = useTour.getState();
  const { t, locale } = useT();
  const active = activeId ? activeTour() : null;
  const step = active?.steps[stepIndex];
  const [rect, setRect] = useState<Rect | null>(null);
  const [topOverride, setTopOverride] = useState<number | null>(null);
  const [narrow, setNarrow] = useState(false);
  const cardRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const m = window.matchMedia("(max-width: 639px)");
    const f = () => setNarrow(m.matches);
    f();
    m.addEventListener("change", f);
    return () => m.removeEventListener("change", f);
  }, []);

  // Measure after paint and on resize/scroll: a spotlight that lags the element it points at is worse than none.
  useLayoutEffect(() => {
    if (!step) { setRect(null); return; }
    const measure = () => {
      anchorEl(step.anchor)?.scrollIntoView({ block: "nearest", inline: "nearest" });
      setRect(anchorRect(step.anchor));
    };
    measure();
    window.addEventListener("resize", measure);
    window.addEventListener("scroll", measure, true);
    // Panels animate in (agent panel, drawers): re-measure once they settle.
    const id = window.setTimeout(measure, 350);
    return () => { window.removeEventListener("resize", measure); window.removeEventListener("scroll", measure, true); window.clearTimeout(id); };
  }, [step]);

  useLayoutEffect(() => { setTopOverride(null); }, [step, rect?.top, rect?.left, rect?.height]);

  // The card height is unknown until it renders: decide the final vertical position from the measured height.
  useLayoutEffect(() => {
    const el = cardRef.current;
    if (!el || narrow || topOverride !== null) return;
    const r = el.getBoundingClientRect();
    const vh = window.innerHeight;
    const h = r.height;
    if (!rect) {
      const centred = Math.max(12, Math.round((vh - h) / 2));
      if (Math.abs(centred - r.top) > 1) setTopOverride(centred);
      return;
    }
    const above = rect.top - GAP - h;
    const below = rect.top + rect.height + GAP;
    let wanted: number;
    if (below + h <= vh - 12) wanted = below;
    else if (above >= 12) wanted = above;
    else wanted = rect.top > vh - rect.top - rect.height ? 12 : Math.max(12, vh - 12 - h);
    if (Math.abs(wanted - r.top) > 1) setTopOverride(wanted);
  }, [step, rect, topOverride, narrow]);

  useEffect(() => {
    if (!activeId) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") { e.preventDefault(); stop(); }
      if (e.key === "ArrowRight") { e.preventDefault(); next(); }
      if (e.key === "ArrowLeft") { e.preventDefault(); back(); }
    };
    window.addEventListener("keydown", onKey);
    cardRef.current?.focus();
    return () => window.removeEventListener("keydown", onKey);
  }, [activeId, next, back, stop]);

  if (!active || !step) return null;

  const shown = active.steps.map((s, i) => (i === stepIndex || stepShown(s) ? i : -1)).filter((i) => i >= 0);
  const total = shown.length;
  const position = shown.indexOf(stepIndex);
  const isLast = position === total - 1;

  let cardStyle: CSSProperties;
  if (narrow) cardStyle = { left: 12, right: 12, bottom: 12, width: "auto" };
  else if (!rect) cardStyle = { left: Math.max(12, Math.round((window.innerWidth - PANEL_W) / 2)), top: 12, width: PANEL_W };
  else {
    const tall = rect.height > window.innerHeight * 0.55;
    const roomRight = window.innerWidth - (rect.left + rect.width) > CARD_W + 24;
    const roomLeft = rect.left > CARD_W + 24;
    if (tall && (roomRight || roomLeft)) {
      // A very tall anchor leaves no room above or below: beside it is the only honest placement.
      cardStyle = {
        left: roomRight ? rect.left + rect.width + GAP : rect.left - GAP - CARD_W,
        top: Math.max(12, Math.min(rect.top, window.innerHeight - 260)),
        width: CARD_W,
      };
    } else {
      const left = Math.min(Math.max(12, rect.left + rect.width / 2 - CARD_W / 2), window.innerWidth - CARD_W - 12);
      cardStyle = { top: rect.top + rect.height + GAP, left, width: CARD_W };
    }
  }

  // Four rects around the cut-out: needs no mask element and leaves the highlighted element un-tinted.
  const panels: CSSProperties[] = rect && !narrow
    ? [
        { top: 0, left: 0, right: 0, height: Math.max(0, rect.top) },
        { top: rect.top + rect.height, left: 0, right: 0, bottom: 0 },
        { top: rect.top, left: 0, width: Math.max(0, rect.left), height: rect.height },
        { top: rect.top, left: rect.left + rect.width, right: 0, height: rect.height },
      ]
    : [{ inset: 0 }];

  return (
    <div role="dialog" aria-modal="true" aria-label={copyOf(active, locale, active.name)} data-testid="tour" style={{ position: "fixed", inset: 0, zIndex: 9000 }}>
      {panels.map((p, i) => <div key={i} onClick={stop} style={{ position: "fixed", background: "rgba(8,20,32,0.55)", ...p }} />)}
      {rect && !narrow && (
        <div data-testid="tour-spot" style={{ position: "fixed", pointerEvents: "none", top: rect.top, left: rect.left, width: rect.width, height: rect.height, borderRadius: 10, boxShadow: "0 0 0 2px rgb(var(--accent))" }} />
      )}
      <div ref={cardRef} tabIndex={-1} data-testid="tour-card"
        className="flex flex-col rounded-xl border border-line bg-panel p-4 shadow-float outline-none"
        style={{ position: "fixed", maxHeight: "calc(100dvh - 24px)", ...cardStyle, ...(topOverride !== null && !narrow ? { top: topOverride } : null) }}>
        <div className="mb-1.5 flex items-start gap-2.5">
          <div className="flex-1 text-[14px] font-bold text-ink">{copyOf(active, locale, step.title)}</div>
          <button type="button" onClick={stop} aria-label={t("tour.close")} title={t("tour.close")} className="-m-2 rounded p-2.5 text-mute hover:text-ink"><Icon name="x" size={15} /></button>
        </div>
        <div className="mb-3.5 min-h-0 overflow-y-auto overscroll-contain">
          <p className="m-0 whitespace-pre-line text-[12.5px] leading-[1.65] text-ink2">{copyOf(active, locale, step.body)}</p>
        </div>
        <div className="flex items-center gap-2">
          <span className="font-mono text-[11px] text-mute">{position + 1} / {total}</span>
          <div className="flex-1" />
          {position > 0 && (
            <button type="button" onClick={back} data-testid="tour-back" className="inline-flex items-center gap-1.5 min-h-10 rounded-lg border border-line px-3 py-2 text-xs font-semibold text-ink hover:bg-panel2">
              <Icon name="back" size={13} /> {t("tour.back")}
            </button>
          )}
          <button type="button" onClick={next} data-testid="tour-next" className="inline-flex min-h-10 items-center gap-1.5 rounded-lg bg-accent px-3.5 py-2 text-xs font-bold text-white hover:bg-accent-hover">
            {isLast ? t("tour.finish") : t("tour.next")}
            {!isLast && <Icon name="arrow" size={13} />}
          </button>
        </div>
      </div>
    </div>
  );
}
