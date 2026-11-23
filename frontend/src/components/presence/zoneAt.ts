// The room a point of the floor is in, from the organization's own layout. Plain module (no three / React) so the 2D
// office can use it without loading the 3D scene.
import { ZONE_NAMES, ZONE_SIZE } from "@/lib/officeLayoutModel";
import { zoneCenter } from "../office/zones";

/** The room a point is in ("meeting" | "approvals" | "cafe") or "floor". */
export function zoneAt(x: number, z: number): string {
  for (const n of ZONE_NAMES) {
    const [cx, cz] = zoneCenter(n);
    const s = ZONE_SIZE[n];
    if (Math.abs(x - (cx + s.dx)) <= s.w / 2 && Math.abs(z - (cz + s.dz)) <= s.d / 2) return n;
  }
  return "floor";
}
