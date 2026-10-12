import type { SectionId } from "@/lib/navBridge";

/**
 * Guided tours: short walkthroughs of each screen, opened from the "?" button in the top bar.
 *
 * Rules this encodes:
 *  - A step points at a REAL element, found by its `data-tour` attribute. If the anchor is absent or
 *    not visible (empty screen, feature off, panel closed) the step is SKIPPED, never shown pointing at
 *    nothing. That is what keeps a tour from quietly lying after a refactor.
 *  - A tour never blocks: Escape, the X, or a click on the shade leaves it at any step.
 *  - A step says WHY the thing exists / what to do with it, not what the control is called.
 *  - Copy lives in each tour file (es + en together), so a screen's walkthrough is added or removed as one unit.
 *    Only the shell strings (Next, Back, ...) are in the shared i18n catalogue.
 */
export interface TourStep {
  /** Value of `data-tour` on the element to highlight. Omit for an intro card about the whole screen. */
  anchor?: string;
  /** Keys into the tour's own `copy` tables. */
  title: string;
  body: string;
}

export interface TourDefinition {
  /** Stable id; also what is remembered as "seen". */
  id: string;
  /** Screen this tour belongs to. */
  section: SectionId;
  /** Copy key for the tour's name (button tooltip). */
  name: string;
  steps: TourStep[];
  copy: { es: Record<string, string>; en: Record<string, string> };
}
