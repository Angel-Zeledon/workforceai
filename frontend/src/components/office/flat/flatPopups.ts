// Bridge between the "Plano" canvas and the HTML cards of the 3D office (the approvals desk card, with the same actions
// as the tray): the cards register themselves in labelEntries (useRegister) and the 3D office's LabelSolver positions
// them from 3D heads. Here the same entries are positioned from the isometric projection, and the feet of every drawn
// person are published to registry.feetPos, which the card reads to know who has reached the approvals desk.
import * as THREE from "three";
import { FIXED_ANCHORS, labelEntries } from "../labels";
import { feetPos } from "../registry";

export function publishFeet(id: string, x: number, z: number) {
  const v = feetPos.get(id);
  if (v) v.set(x, 0, z); else feetPos.set(id, new THREE.Vector3(x, 0, z));
}
export function dropFeet(ids: Iterable<string>) { for (const id of ids) feetPos.delete(id); }

/** Positions the popup cards (kind "popup") above their fixed anchors, kept inside the canvas. */
export function placePopups(toScreen: (x: number, z: number, h: number) => { x: number; y: number }, width: number, height: number) {
  labelEntries.forEach((e) => {
    if (e.kind !== "popup") return;
    const a = FIXED_ANCHORS[e.anchor];
    if (!a) return;
    const p = toScreen(a[0], a[2], a[1]);
    const w = Number(e.el.dataset.w || 260), h = Number(e.el.dataset.h || 150) + 10;
    const x = Math.max(w / 2 + 8, Math.min(width - w / 2 - 8, p.x));
    const y = Math.max(h + 12, Math.min(height - 8, p.y));
    e.el.style.transform = `translate(${x.toFixed(1)}px, ${y.toFixed(1)}px) translate(-50%, -100%)`;
    e.el.style.opacity = "1";
    e.el.style.zIndex = "1100";
  });
}
