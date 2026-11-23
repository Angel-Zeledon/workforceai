"use client";
// The 3D subject of the picture of a room or of a built-in object of the office (not catalog items): the SAME pieces the
// office draws (OfficeEnv's for Actual / Maqueta / Pixel, StudioRoom's for Estudio) on the room's mat, at their default place.
import type * as THREE from "three";
import { DEFAULT_LAYOUT, FIXTURES, ROOM_PARTS, ZONE_SIZE, fixtureWorld, restOn, zoneCenterOf, type FixtureId, type ZoneName } from "@/lib/officeLayoutModel";
import { FixtureMesh } from "../OfficeEnv";
import { Fixture } from "../studio/StudioRoom";
import type { Assets } from "../studio/studioAssets";

export type Fx = { kind: "room"; id: ZoneName } | { kind: "fixture"; id: FixtureId };
export const fxKey = (fx: Fx) => `fx:${fx.kind}:${fx.id}`;

const MAT: Record<ZoneName, string> = { meeting: "#cfd6df", approvals: "#d5dae1", cafe: "#e1d8c8" };

export function RoomSubject({ fx, assets, focus }: { fx: Fx; assets: Assets | null; focus: React.RefObject<THREE.Group | null> }) {
  const ids: FixtureId[] = fx.kind === "room" ? [...ROOM_PARTS[fx.id]] : [fx.id];
  const L = DEFAULT_LAYOUT;
  const table = fixtureWorld(L, "meeting_table", assets ? undefined : [0, 0]);
  const c: readonly [number, number] = assets && fx.kind === "room" ? zoneCenterOf(L, fx.id) : [0, 0]; // Estudio draws in the room's own place
  const tableAt: [number, number] = table ? [table.x, table.z] : [0, 0];
  return (
    <group ref={focus}>
      {fx.kind === "room" && (() => { const s = ZONE_SIZE[fx.id]; return <mesh position={[c[0] + s.dx, 0.011, c[1] + s.dz]} receiveShadow><boxGeometry args={[s.w - 0.2, 0.02, s.d - 0.2]} /><meshStandardMaterial color={MAT[fx.id]} roughness={1} /></mesh>; })()}
      {ids.map((id) => {
        const p = fixtureWorld(L, id, [0, 0]);
        if (!p) return null;
        if (assets) return <Fixture key={id} a={assets} id={id} layout={{ ...L, zones: {} }} tableAt={tableAt} pending={0} />;
        const rest = FIXTURES[id].level === "top" ? restOn(L, id) : { y: 0 };
        return <group key={id} position={[p.x, rest.y, p.z]} rotation={[0, p.rot, 0]}><FixtureMesh id={id} /></group>;
      })}
    </group>
  );
}
