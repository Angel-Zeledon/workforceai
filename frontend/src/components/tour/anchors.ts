import type { TourDefinition, TourStep } from "./types";

export const anchorSelector = (a: string) => `[data-tour="${CSS.escape(a)}"]`;

/** A step can be shown when it has no anchor, or its anchor is on screen with a real size. */
export function stepShown(step: TourStep): boolean {
  if (!step.anchor) return true;
  if (typeof document === "undefined") return true;
  const el = document.querySelector<HTMLElement>(anchorSelector(step.anchor));
  if (!el) return false;
  const r = el.getBoundingClientRect();
  return r.width > 0 || r.height > 0;
}

/** Index of the nearest shown step from `from` going in `dir`, or -1. */
export function nextShown(tour: TourDefinition, from: number, dir: 1 | -1): number {
  for (let j = from; j >= 0 && j < tour.steps.length; j += dir) if (stepShown(tour.steps[j])) return j;
  return -1;
}

export function firstStep(tour: TourDefinition): number {
  const j = nextShown(tour, 0, 1);
  return j < 0 ? 0 : j;
}
