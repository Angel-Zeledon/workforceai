import * as THREE from "three";
import { RoundedBoxGeometry } from "three/examples/jsm/geometries/RoundedBoxGeometry.js";

/**
 * Shapes the office styles build instead of the classic boxes (themeRuntime.styledGeometry caches and shares them):
 * clay pillows and wavy walls, paper card slabs, folded strips and pleated screens. Every builder returns geometry
 * centered on the origin with the requested size; the caller translates it into place.
 */

/**
 * Clay: a soft lump of the given size, rounded all round and puffed along its thinnest side when it is a slab (a desk
 * top bulges, a screen turns into a cushion). The normals of the rounded box are kept: the bulge is gentle.
 */
export function puffyBox(sx: number, sy: number, sz: number): THREE.BufferGeometry {
  const dims = [sx, sy, sz];
  const g = new RoundedBoxGeometry(sx, sy, sz, 4, Math.min(sx, sy, sz) * 0.48);
  const t = dims.indexOf(Math.min(...dims)), a = (t + 1) % 3, b = (t + 2) % 3;
  if (dims[a] > 2.5 * dims[t] && dims[b] > 2.5 * dims[t]) {
    const bulge = Math.min(0.05, 0.12 * Math.min(dims[a], dims[b]), dims[t] * 0.8);
    const pos = g.attributes.position as THREE.BufferAttribute;
    const v = new THREE.Vector3();
    for (let i = 0; i < pos.count; i++) {
      v.fromBufferAttribute(pos, i);
      const ua = v.getComponent(a) / (dims[a] / 2), ub = v.getComponent(b) / (dims[b] / 2);
      const f = Math.max(0, 1 - ua * ua) * Math.max(0, 1 - ub * ub);
      const c = v.getComponent(t);
      v.setComponent(t, c + Math.sign(c) * bulge * f);
      pos.setXYZ(i, v.x, v.y, v.z);
    }
    pos.needsUpdate = true;
  }
  return g;
}

/** The horizontal axis a wall runs along (0 = x, 2 = z) and its length. */
function along(sx: number, sz: number): { ax: 0 | 2; len: number; thin: 0 | 2 } {
  return sx >= sz ? { ax: 0, len: sx, thin: 2 } : { ax: 2, len: sz, thin: 0 };
}

/** Clay: a wall pressed out by hand, its top edge rising and falling in soft waves and its faces slightly uneven. */
export function wavyWall(sx: number, sy: number, sz: number): THREE.BufferGeometry {
  const { ax, len, thin } = along(sx, sz);
  const n = Math.max(8, Math.ceil(len / 0.22));
  const g = new THREE.BoxGeometry(sx, sy, sz, ax === 0 ? n : 1, 8, ax === 2 ? n : 1);
  const pos = g.attributes.position as THREE.BufferAttribute;
  const v = new THREE.Vector3();
  for (let i = 0; i < pos.count; i++) {
    v.fromBufferAttribute(pos, i);
    const u = v.getComponent(ax);
    const h = (v.y + sy / 2) / sy; // 0 at the floor, 1 at the top
    v.y += (Math.sin(u * 2.1) * 0.1 + Math.sin(u * 4.7 + 1) * 0.05) * h * h;
    const c = v.getComponent(thin);
    v.setComponent(thin, c * (1 + 0.22 * Math.sin(u * 1.3 + v.y * 2.0)));
    pos.setXYZ(i, v.x, v.y, v.z);
  }
  pos.needsUpdate = true;
  g.computeVertexNormals();
  return g;
}

/** Paper: a wall folded like a screen, vertical pleats every ~0.9 m (flat shading makes every fold read). */
export function pleatedWall(sx: number, sy: number, sz: number): THREE.BufferGeometry {
  const { ax, len, thin } = along(sx, sz);
  const n = Math.max(2, Math.round(len / 0.9));
  const g = new THREE.BoxGeometry(sx, sy, sz, ax === 0 ? n : 1, 1, ax === 2 ? n : 1);
  const pos = g.attributes.position as THREE.BufferAttribute;
  const v = new THREE.Vector3();
  const seg = len / n;
  for (let i = 0; i < pos.count; i++) {
    v.fromBufferAttribute(pos, i);
    const k = Math.round((v.getComponent(ax) + len / 2) / seg);
    v.setComponent(thin, v.getComponent(thin) + (k % 2 ? 0.07 : -0.07));
    pos.setXYZ(i, v.x, v.y, v.z);
  }
  pos.needsUpdate = true;
  g.computeVertexNormals();
  return g;
}

/** Paper: a leg made of a strip of card folded lengthwise (an L-shaped profile), `w` wide and `h` tall. */
export function foldedStrip(w: number, h: number): THREE.BufferGeometry {
  const t = 0.014;
  const s = new THREE.Shape();
  s.moveTo(0, 0); s.lineTo(w, 0); s.lineTo(w, t); s.lineTo(t, t); s.lineTo(t, w); s.lineTo(0, w); s.closePath();
  const g = new THREE.ExtrudeGeometry(s, { depth: h, bevelEnabled: false });
  g.rotateX(-Math.PI / 2); // the extrusion runs up the leg
  g.translate(-w / 2, -h / 2, w / 2);
  return g;
}
