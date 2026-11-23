"use client";
// Moving furniture inside the "Estudio" office. The editing logic is the 3D office's own (EditorLayer + editorActions:
// grid, selection frame, ghost, drag, R / Supr / arrows / undo); this file only adds what Estudio draws differently:
// the invisible grab handles over the desks, rooms, built-in furniture and the organization's pieces. The studio draws
// the DRAFT (effective layout), so what is dragged moves live.
import { useMemo } from "react";
import { useRewards } from "@/lib/rewards";
import { useOfficeLayout } from "@/lib/officeLayout";
import { FIXTURE_IDS, FIXTURES, ZONE_NAMES, ZONE_SIZE, fixturePose, restOn, zoneCenterOf, type FixtureId, type Layout, type ZoneName } from "@/lib/officeLayoutModel";
import { pieceInfo } from "../pieceInfo";
import { EditorLayer } from "../EditorLayer";

const grab = (sel: () => void) => ({
  onPointerDown: (ev: { stopPropagation: () => void }) => { ev.stopPropagation(); const st = useOfficeLayout.getState(); if (st.placing) return; sel(); st.setDragging(true); },
  onPointerOver: () => { document.body.style.cursor = "grab"; },
  onPointerOut: () => { document.body.style.cursor = ""; },
});
const Ghostly = () => <meshBasicMaterial transparent opacity={0} depthWrite={false} />;

function ZoneHit({ name, layout }: { name: ZoneName; layout: Layout }) {
  const [cx, cz] = zoneCenterOf(layout, name), z = ZONE_SIZE[name];
  return (
    <mesh position={[cx + z.dx, 0.03, cz + z.dz]} {...grab(() => useOfficeLayout.getState().select({ kind: "zone", id: name }))}>
      <boxGeometry args={[z.w, 0.06, z.d]} /><Ghostly />
    </mesh>
  );
}

/** Grab handle of a built-in object: floor things a little roomier, small things on a surface hardly more than themselves. */
function FixtureHit({ id, layout }: { id: FixtureId; layout: Layout }) {
  const f = FIXTURES[id], p = fixturePose(layout, id);
  if (p.hidden) return null;
  const [cx, cz] = f.room ? zoneCenterOf(layout, f.room) : [0, 0];
  const small = f.level === "top";
  const w = small ? Math.max(0.16, f.w) + 0.05 : Math.max(0.3, f.w) + 0.2;
  const d = small ? Math.max(0.16, f.d) + 0.05 : Math.max(0.3, f.d) + 0.2;
  const y = f.level === "wall" ? 1.7 : f.h / 2;
  return (
    <group position={[cx + p.x, restOn(layout, id).y, cz + p.z]} rotation={[0, p.rot, 0]}>
      <mesh position={[0, y, 0]} {...grab(() => useOfficeLayout.getState().select({ kind: "fixture", id }))}>
        <boxGeometry args={[w, f.h, d]} /><Ghostly />
      </mesh>
    </group>
  );
}

const PIECE_H: Record<string, number> = { rug: 0.08, poster: 0.9, window: 1.2, light: 0.4, whiteboard: 1.1, divider: 1.6, shelf: 1.7, arcade: 1.8, cabinet: 1.1, sofa: 0.9, plant: 1.2, lamp: 1.5 };

function PieceHits({ layout }: { layout: Layout }) {
  const catalog = useRewards((s) => s.catalog);
  const list = useMemo(() => {
    const byId = new Map(catalog.map((i) => [i.id, i] as const));
    return layout.pieces.flatMap((p) => { const item = byId.get(p.item); return item ? [{ p, item }] : []; });
  }, [catalog, layout.pieces]);
  return (
    <>
      {list.map(({ p, item }) => {
        const info = pieceInfo(item);
        const h = PIECE_H[item.category] ?? 0.9;
        const wall = info.mount === "wall" || info.mount === "ceiling";
        const y = wall ? (p.y ?? info.y) : h / 2;
        return (
          <group key={p.uid} position={[p.x, 0, p.z]} rotation={[0, p.rot, 0]}>
            <mesh position={[0, y, 0]} {...grab(() => useOfficeLayout.getState().select({ kind: "piece", uid: p.uid }))}>
              <boxGeometry args={[Math.max(0.3, info.w * p.s), wall ? Math.max(0.3, h) : h, Math.max(0.3, info.d * p.s)]} /><Ghostly />
            </mesh>
          </group>
        );
      })}
    </>
  );
}

/** The desks: the handle follows the ghost while one is dragged (the studio draws desks from `desks`, which already does). */
function DeskHits({ desks }: { desks: { id: string; pos: [number, number] }[] }) {
  return (
    <>
      {desks.map(({ id, pos: [x, z] }) => (
        <mesh key={id} position={[x, 0.7, z + 0.55]} {...grab(() => useOfficeLayout.getState().select({ kind: "desk", id }))}>
          <boxGeometry args={[2.4, 1.4, 2.2]} /><Ghostly />
        </mesh>
      ))}
    </>
  );
}

/** Everything the editor needs inside the studio's canvas; nothing at all outside edit mode. */
export function StudioEdit({ layout, desks }: { layout: Layout; desks: { id: string; pos: [number, number] }[] }) {
  const editing = useOfficeLayout((s) => s.editing);
  if (!editing) return null;
  return (
    <>
      {ZONE_NAMES.map((n) => <ZoneHit key={n} name={n} layout={layout} />)}
      {FIXTURE_IDS.map((id) => <FixtureHit key={id} id={id} layout={layout} />)}
      <PieceHits layout={layout} />
      <DeskHits desks={desks} />
      <EditorLayer />
    </>
  );
}
