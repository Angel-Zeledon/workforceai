import type { SectionId } from "@/lib/navBridge";
import type { TourDefinition } from "../types";

/** One tour per screen. Add the tour file here and its anchors (`data-tour="..."`) in the screen's components. */
export const TOURS: TourDefinition[] = [];

export const tourForSection = (section: SectionId): TourDefinition | null => TOURS.find((t) => t.section === section) ?? null;
