import type { SectionId } from "@/lib/navBridge";
import type { TourDefinition } from "../types";
import { officeTour } from "./office";
import { dashboardTour } from "./dashboard";
import { chatsTour } from "./chats";
import { projectsTour } from "./projects";
import { filesTour } from "./files";
import { skillsTour } from "./skills";
import { shopTour } from "./shop";
import { settingsTour } from "./settings";

/** One tour per screen. Add the tour file here and its anchors (`data-tour="..."`) in the screen's components. */
export const TOURS: TourDefinition[] = [officeTour, dashboardTour, chatsTour, projectsTour, filesTour, skillsTour, shopTour, settingsTour];

export const tourForSection = (section: SectionId): TourDefinition | null => TOURS.find((t) => t.section === section) ?? null;
