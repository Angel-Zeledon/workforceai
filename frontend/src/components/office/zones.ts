import { effective, useOfficeLayout } from "@/lib/officeLayout";
import { MEETING_SEATS, fixtureWorld, zoneCenterOf, type Layout, type ZoneName } from "@/lib/officeLayoutModel";
import { approvalsFrame, type MeetingRoomInput, type PartPose } from "./officeState";

const now = (): Layout => effective(useOfficeLayout.getState());

/** Center of a room: where the organization put it (the draft while editing) or its built-in place. */
export function zoneCenter(name: ZoneName): [number, number] {
  return zoneCenterOf(now(), name);
}
export const meetingCenter = (): [number, number] => zoneCenter("meeting");
export const cafeCenter = (): [number, number] => zoneCenter("cafe");

/** The meeting room as it stands now: table and seats where the organization put them (null = taken away). */
export function meetingRoom(): MeetingRoomInput {
  const l = now();
  return { center: zoneCenterOf(l, "meeting"), table: fixtureWorld(l, "meeting_table"), seats: MEETING_SEATS.map((id) => fixtureWorld(l, id)) };
}
/** Where the meeting looks: the table, or the room's center when the table was taken away. */
export function meetingFocus(): [number, number] {
  const t = fixtureWorld(now(), "meeting_table");
  return t ? [t.x, t.z] : meetingCenter();
}
/** The counter and the high table of the cafeteria (null = taken away). */
export function cafeParts(): { counter: PartPose | null; table: PartPose | null } {
  const l = now();
  return { counter: fixtureWorld(l, "cafe_counter"), table: fixtureWorld(l, "cafe_table") };
}

/** The approvals desk (the CEO desk): the user's anchor and the desk's turn (it follows the desk when moved). */
export function userFrame(): { user: [number, number]; rot: number } {
  const l = now();
  return approvalsFrame(zoneCenterOf(l, "approvals"), fixtureWorld(l, "approvals_desk"));
}
/** The user at the approvals desk: x, height of the label anchor, z. */
export const userPos = (): [number, number, number] => { const { user } = userFrame(); return [user[0], 1.6, user[1]]; };
/** A point at (dx, dz) from the user in the desk's own frame (in front of it is -z), at height y. */
export function nearUser(dx: number, dz: number, y = 0): [number, number, number] {
  const { user, rot } = userFrame();
  const c = Math.cos(rot), s = Math.sin(rot);
  return [user[0] + dx * c + dz * s, y, user[1] - dx * s + dz * c];
}
