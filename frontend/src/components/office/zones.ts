import { effective, useOfficeLayout } from "@/lib/officeLayout";
import { ZONE_DEFAULTS, type ZoneName } from "@/lib/officeLayoutModel";

/** Center of a room: where the organization put it (the draft while editing) or its built-in place. */
export function zoneCenter(name: ZoneName): [number, number] {
  const z = effective(useOfficeLayout.getState()).zones[name];
  return z ? [z.x, z.z] : ZONE_DEFAULTS[name];
}
export const meetingCenter = (): [number, number] => zoneCenter("meeting");
export const cafeCenter = (): [number, number] => zoneCenter("cafe");
/** The approvals desk (the CEO desk): x, height of the label anchor, z. */
export const userPos = (): [number, number, number] => { const [x, z] = zoneCenter("approvals"); return [x, 1.6, z]; };
