import * as THREE from "three";
import type { OfficeStyle } from "@/lib/officeLayoutModel";
import { lookOf, styleMaterial, type GeometryMode, type MatRole } from "./officeTheme";
import { foldedStrip, pleatedWall, puffyBox, wavyWall } from "./themeShapes";

/**
 * The style transform layer applied to the rendered scene (docs/architecture/office-editor.md, "Styles"). Every builder
 * keeps drawing its classic meshes from the same specs; this layer then restyles what was drawn:
 *  - lit materials get the style's color / finish (officeTheme.styleMaterial), faceting and, for neon, a pulsing edge glow;
 *  - geometry can be swapped for blocky (bricks) or low-poly (paper) versions, cached and shared, never per frame;
 *  - bricks collect studs for the upward faces of boxes into one instanced mesh.
 * Everything stores the original in `userData`, so going back to "classic" restores the exact original objects.
 * Unlit materials (labels, glows, screens, editor frames) are never touched. A subtree flagged `userData.noTheme` is
 * skipped; `userData.noStuds` keeps studs off it (characters, which move every frame). The role of a subtree picks the
 * variant of the style: `userData.figure` a character, `userData.ground` the floor / outer ground / desk mats (no
 * geometry swap), `userData.wall` the room's walls.
 */

const isLit = (m: THREE.Material): m is THREE.MeshStandardMaterial =>
  (m as THREE.MeshStandardMaterial).isMeshStandardMaterial === true || (m as unknown as THREE.MeshLambertMaterial).isMeshLambertMaterial === true || (m as unknown as THREE.MeshPhongMaterial).isMeshPhongMaterial === true;

interface MatState {
  style: OfficeStyle;
  /** the role (item / figure / ground / wall) the material was last styled for */
  variant: string;
  roughness: number; metalness: number; flat: boolean; envIntensity: number;
  /** the material's own reflection map before any style (usually none) */
  envMap: THREE.Texture | null;
  obc: THREE.Material["onBeforeCompile"]; key: THREE.Material["customProgramCacheKey"];
  /** the style shader patch is installed */
  fx: boolean;
  u: { uThSurf: { value: number }; uThBump: { value: number }; uThTex: { value: number }; uThLump: { value: number }; uThInk: { value: number }; uThCel: { value: number } };
}

/**
 * One shader patch for every style, so all styled materials of a base type share one program (switched by uniforms):
 *  - surface: procedural detail in object space perturbs the normal (bump from screen derivatives, no textures, no UVs):
 *    clay thumbprints, paper grain, wood grain, brushed metal, fabric weave, lacquer orange peel, skin;
 *  - lump: hand-made wobble of the vertices along their normal (clay);
 *  - ink: the surface darkens toward its silhouette like a drawn crease (paper);
 *  - cel: the light is regrouped in three flat bands plus a hard highlight; the color itself is untouched (cartoon).
 */
const NOISE = `
float thHash(vec3 p) { p = fract(p * 0.3183099 + 0.1); p *= 17.0; return fract(p.x * p.y * p.z * (p.x + p.y + p.z)); }
float thNoise(vec3 x) {
  vec3 i = floor(x); vec3 f = fract(x); f = f * f * (3.0 - 2.0 * f);
  return mix(mix(mix(thHash(i), thHash(i + vec3(1,0,0)), f.x), mix(thHash(i + vec3(0,1,0)), thHash(i + vec3(1,1,0)), f.x), f.y),
             mix(mix(thHash(i + vec3(0,0,1)), thHash(i + vec3(1,0,1)), f.x), mix(thHash(i + vec3(0,1,1)), thHash(i + vec3(1,1,1)), f.x), f.y), f.z);
}`;
const VERT_HEAD = `#include <common>
varying vec3 vThPos;
uniform float uThLump;${NOISE}`;
const VERT_BODY = `#include <begin_vertex>
vThPos = transformed;
if (uThLump > 0.0) transformed += objectNormal * ((thNoise(position * 3.5) - 0.5) + (thNoise(position * 9.0) - 0.5) * 0.4) * uThLump;`;
const FRAG_HEAD = `#include <common>
varying vec3 vThPos;
uniform float uThSurf; uniform float uThBump; uniform float uThTex; uniform float uThInk; uniform float uThCel;${NOISE}
/** Courses of stacked bricks (seams), running along the wall whichever way it faces. Declared before its callers:
 *  GLSL has no hoisting, a call above the definition fails to compile and every styled material disappears. */
float thBrick(vec3 p) {
  vec2 b = vec2(p.x + p.z, p.y) / vec2(0.48, 0.2);
  b.x += mod(floor(b.y), 2.0) * 0.5;
  vec2 f = fract(b);
  return smoothstep(0.0, 0.06, min(min(f.x, 1.0 - f.x) * 2.4, min(f.y, 1.0 - f.y)));
}
float thHeight(vec3 p, float k) {
  if (k < 0.5) return 0.0;
  if (k < 1.5) { float n = thNoise(p * 4.5); float f = thNoise(p * 13.0); return n + 0.35 * f * f + 0.12 * thNoise(p * 34.0); }      // clay: pressed dents, finger smudges
  if (k < 2.5) return thNoise(p * 160.0) * 0.6 + thNoise(p * vec3(30.0, 260.0, 30.0)) * 0.4;                                            // paper
  if (k < 3.5) { float n = thNoise(p * vec3(2.0, 9.0, 9.0)); return sin((p.y * 70.0 + p.z * 40.0) + n * 9.0) * 0.5 + thNoise(p * 90.0) * 0.3; } // wood
  if (k < 4.5) return thNoise(p * vec3(4.0, 600.0, 600.0));                                                                              // metal
  if (k < 5.5) return (sin(p.x * 160.0) * sin(p.y * 160.0 + p.z * 160.0)) * 0.25 + thNoise(p * 60.0) * 0.5;                             // fabric
  if (k < 6.5) return thNoise(p * 55.0);                                                                                                  // lacquer
  if (k < 7.5) return thNoise(p * 140.0) * 0.5;                                                                                           // skin
  if (k < 8.5) return thNoise(p * 40.0) * 0.4 + thNoise(p * 7.0) * 0.6;                                                                   // concrete
  if (k < 9.5) return thNoise(p * 26.0) * 0.7 + thNoise(p * 90.0) * 0.3;                                                                 // plaster
  return thBrick(p);                                                                                                                     // stacked bricks
}
/** The shade of the texture, 0..1 around 0.5, at a scale that still reads from the default camera (tens of cm). */
float thAlbedo(vec3 p, float k) {
  if (k < 1.5) { float a = thNoise(p * 1.7); float b = thNoise(p * 6.5); return a * 0.55 + b * b * 0.45; }                                    // clay smudges
  if (k < 2.5) return thNoise(p * 2.4) * 0.45 + thNoise(p * vec3(11.0, 34.0, 11.0)) * 0.35 + thNoise(p * 70.0) * 0.2;                    // paper fibres
  if (k < 3.5) { float n = thNoise(p * vec3(0.8, 3.5, 3.5)); return 0.5 + 0.5 * sin(p.x * 22.0 + n * 7.0) * (0.6 + 0.4 * thNoise(p * 9.0)); } // wood grain
  if (k < 4.5) return thNoise(p * vec3(1.5, 60.0, 60.0));                                                                                // metal streaks
  if (k < 5.5) return thNoise(p * 14.0);                                                                                                  // fabric
  if (k < 7.5) return 0.5;
  if (k < 8.5) return thNoise(p * 0.9) * 0.55 + thNoise(p * 4.0) * 0.3 + thNoise(p * 22.0) * 0.15;                                       // concrete mottle
  if (k < 9.5) return thNoise(p * 2.0) * 0.6 + thNoise(p * 12.0) * 0.4;                                                                  // plaster
  return 0.35 + 0.65 * thBrick(p);                                                                                                        // brick seams
}
vec3 thBump(vec3 pos, vec3 n, float h) {
  vec3 sx = dFdx(pos), sy = dFdy(pos);
  vec3 r1 = cross(sy, n), r2 = cross(n, sx);
  float det = dot(sx, r1);
  vec3 g = sign(det) * (dFdx(h) * r1 + dFdy(h) * r2);
  return normalize(abs(det) * n - g);
}`;
const FRAG_NORMAL = `#include <normal_fragment_maps>
if (uThSurf > 0.5) normal = thBump(-vViewPosition, normal, thHeight(vThPos, uThSurf) * uThBump * 0.012);`;
const FRAG_INK = `#include <emissivemap_fragment>
if (uThTex > 0.0) diffuseColor.rgb *= 1.0 + (thAlbedo(vThPos, uThSurf) - 0.5) * 2.0 * uThTex;
if (uThInk > 0.0) {
  float thF = 1.0 - clamp(abs(dot(normalize(normal), normalize(vViewPosition))), 0.0, 1.0);
  diffuseColor.rgb *= 1.0 - uThInk * smoothstep(0.62, 0.9, thF);
}`;
const FRAG_CEL = `if (uThCel > 0.5) {
  float thL = dot(outgoingLight, vec3(0.299, 0.587, 0.114));
  float thB = max(dot(diffuseColor.rgb, vec3(0.299, 0.587, 0.114)), 0.03);
  float thS = thL / thB;
  // flat bands a fixed ratio apart (whatever the light level), so lit, half-lit and shaded sides always separate
  float thQ = exp2(floor(log2(max(thS, 0.02)) * 1.6 + 0.5) / 1.6);
  outgoingLight = diffuseColor.rgb * thQ + totalEmissiveRadiance;
}
#include <opaque_fragment>`;

/**
 * The neutral room reflection of the realistic style. It is set on each styled material (`envMap`), never as
 * `scene.environment`: three.js ignores `envMapIntensity` for materials lit by `scene.environment` (it uses the scene's
 * single `environmentIntensity`), which washed out the whole office at full strength. Per material, every finish keeps
 * its own reflection strength (matte walls 0.04 ... metal 0.55) and unstyled materials get no reflection at all.
 */
let roomEnv: THREE.Texture | null = null;

function applyEnv(m: THREE.MeshStandardMaterial, st: MatState, style: OfficeStyle) {
  const want = style === "real" && roomEnv ? roomEnv : st.envMap;
  if (m.envMap !== want) { m.envMap = want; m.needsUpdate = true; }
}

/** Installs (or removes, with null) the room reflection and applies it to every material already styled under root. */
export function setRoomEnvironment(env: THREE.Texture | null, root: THREE.Object3D) {
  roomEnv = env;
  root.traverse((o) => {
    const mats = (o as THREE.Mesh).material;
    if (!mats) return;
    for (const m of Array.isArray(mats) ? mats : [mats]) {
      const st = (m.userData as { __th?: MatState }).__th;
      if (st && "roughness" in m) applyEnv(m as THREE.MeshStandardMaterial, st, st.style);
    }
  });
}

function restoreShader(m: THREE.Material, st: MatState) {
  m.onBeforeCompile = st.obc; m.customProgramCacheKey = st.key; m.needsUpdate = true; st.fx = false;
}

function themeMaterial(m: THREE.MeshStandardMaterial, style: OfficeStyle, role: MatRole): void {
  const ud = m.userData as { __th?: MatState };
  let st = ud.__th;
  const variant = role;
  if (!st) {
    if (style === "classic") return;
    st = {
      style: "classic", variant, roughness: m.roughness ?? 1, metalness: m.metalness ?? 0, flat: !!m.flatShading, envIntensity: m.envMapIntensity ?? 1,
      envMap: ("envMap" in m ? m.envMap : null) ?? null,
      obc: m.onBeforeCompile, key: m.customProgramCacheKey, fx: false,
      u: { uThSurf: { value: 0 }, uThBump: { value: 0 }, uThTex: { value: 0 }, uThLump: { value: 0 }, uThInk: { value: 0 }, uThCel: { value: 0 } },
    };
    ud.__th = st;
  }
  if (st.style === style && st.variant === variant) return;
  st.variant = variant;
  const std = "roughness" in m;
  if (style === "classic") {
    if (std) { m.roughness = st.roughness; m.metalness = st.metalness; m.envMapIntensity = st.envIntensity; applyEnv(m, st, "classic"); }
    if (m.flatShading !== st.flat) { m.flatShading = st.flat; m.needsUpdate = true; }
    if (st.fx) restoreShader(m, st);
    st.style = "classic";
    return;
  }
  // colors are never touched: only the finish, the shading and the surface
  const o = styleMaterial({ roughness: st.roughness, metalness: st.metalness, transparent: m.transparent && m.opacity < 0.99 }, style, role);
  if (std) { m.roughness = o.roughness; m.metalness = o.metalness; m.envMapIntensity = o.envIntensity; applyEnv(m, st, style); }
  const flat = st.flat || o.flat;
  if (m.flatShading !== flat) { m.flatShading = flat; m.needsUpdate = true; }
  st.u.uThSurf.value = o.surface;
  st.u.uThBump.value = o.bump;
  st.u.uThTex.value = o.tex;
  st.u.uThLump.value = o.lump;
  st.u.uThInk.value = o.ink;
  st.u.uThCel.value = o.cel ? 1 : 0;
  const needs = o.surface > 0 || o.lump > 0 || o.ink > 0 || o.tex > 0 || o.cel;
  if (needs && !st.fx) {
    const u = st.u, base = st.obc;
    m.onBeforeCompile = (shader, renderer) => {
      base?.call(m, shader, renderer);
      Object.assign(shader.uniforms, u);
      shader.vertexShader = shader.vertexShader.replace("#include <common>", VERT_HEAD).replace("#include <begin_vertex>", VERT_BODY);
      shader.fragmentShader = shader.fragmentShader
        .replace("#include <common>", FRAG_HEAD)
        .replace("#include <normal_fragment_maps>", FRAG_NORMAL)
        .replace("#include <emissivemap_fragment>", FRAG_INK)
        .replace("#include <opaque_fragment>", FRAG_CEL);
    };
    m.customProgramCacheKey = () => "office-style-fx";
    m.needsUpdate = true;
    st.fx = true;
  } else if (!needs && st.fx) restoreShader(m, st);
  st.style = style;
}

// ------------------------------------------------------------------------------------------------ geometry

const geoCache = new Map<string, THREE.BufferGeometry>();
const box3 = new THREE.Box3();
const v3 = new THREE.Vector3(), c3 = new THREE.Vector3();
const r3 = (n: number) => n.toFixed(3);

/** The geometry a style draws instead of `g` (cached by shape), or null to keep it. */
/** Size and center of a geometry's bounding box (null when empty). */
function boxOf(g: THREE.BufferGeometry): { sx: number; sy: number; sz: number; cx: number; cy: number; cz: number } | null {
  if (!g.boundingBox) g.computeBoundingBox();
  const bb = g.boundingBox;
  if (!bb || bb.isEmpty()) return null;
  bb.getSize(v3); bb.getCenter(c3);
  return { sx: Math.max(v3.x, 0.002), sy: Math.max(v3.y, 0.002), sz: Math.max(v3.z, 0.002), cx: c3.x, cy: c3.y, cz: c3.z };
}

/** A tall, thin post (a leg, a stem): two small sides and one long vertical one. */
const isPost = (b: { sx: number; sy: number; sz: number }) => b.sx < 0.09 && b.sz < 0.09 && b.sy > 4 * Math.max(b.sx, b.sz);
/** A slab thin in height (a desk or table top, a shelf): thickness and the center shift that keeps its top in place. */
function thickenSlab(b: { sx: number; sy: number; sz: number; cy: number }, to: number): void {
  if (b.sy < to && b.sx > 4 * b.sy && b.sz > 4 * b.sy) { const ny = Math.min(to, b.sy * 2.2); b.cy -= (ny - b.sy) / 2; b.sy = ny; }
}

function styledGeometry(g: THREE.BufferGeometry, mode: GeometryMode, hint: string | undefined, role: MatRole): THREE.BufferGeometry | null {
  if (mode === "keep") return null;
  const type = g.type;
  const p = (g as unknown as { parameters?: Record<string, number> }).parameters ?? {};
  const boxy = type === "BoxGeometry" || type === "ExtrudeGeometry";
  let key = "";
  let make: () => THREE.BufferGeometry;
  if (mode === "blocks") {
    const blocky = type === "SphereGeometry" || type === "CapsuleGeometry" || type === "ExtrudeGeometry" || type === "IcosahedronGeometry" || type === "OctahedronGeometry" || type === "DodecahedronGeometry" || type === "LatheGeometry";
    if (!blocky) return null;
    if (!g.boundingBox) g.computeBoundingBox();
    const bb = g.boundingBox ?? box3.makeEmpty();
    if (bb.isEmpty()) return null;
    bb.getSize(v3); bb.getCenter(c3);
    const sx = Math.max(v3.x, 0.002), sy = Math.max(v3.y, 0.002), sz = Math.max(v3.z, 0.002), cx = c3.x, cy = c3.y, cz = c3.z;
    const cyl = hint === "cyl";
    key = `${cyl ? "cyl" : "blk"}:${r3(sx)},${r3(sy)},${r3(sz)}@${r3(cx)},${r3(cy)},${r3(cz)}`;
    make = () => {
      const out = cyl ? new THREE.CylinderGeometry(Math.max(sx, sz) / 2, Math.max(sx, sz) / 2, sy * 0.92, 20) : new THREE.BoxGeometry(sx, sy, sz);
      out.translate(cx, cy, cz);
      return out;
    };
  } else if (mode === "soft") {
    // clay: modeled by hand. Walls get a wavy top, slabs turn into thick bulging pillows (desk tops, screens), posts get
    // chunky, every other box becomes a soft rounded lump; thin round posts plump up (spheres and capsules are soft already)
    if (type === "CylinderGeometry" && p.height > 5 * Math.max(p.radiusTop, p.radiusBottom) && Math.max(p.radiusTop, p.radiusBottom) < 0.05) {
      const r = 0.06;
      key = `soft-post:${r3(p.height)}`;
      make = () => new THREE.CylinderGeometry(r, r * 1.1, p.height, 14);
    } else {
      if (!boxy) return null;
      const b = boxOf(g);
      if (!b) return null;
      if (role === "wall") {
        key = `soft-wall:${r3(b.sx)},${r3(b.sy)},${r3(b.sz)}@${r3(b.cx)},${r3(b.cy)},${r3(b.cz)}`;
        make = () => wavyWall(b.sx, b.sy, b.sz).translate(b.cx, b.cy, b.cz);
      } else {
        thickenSlab(b, 0.11);
        if (isPost(b)) { b.sx = Math.max(b.sx, 0.12); b.sz = Math.max(b.sz, 0.12); }
        key = `soft:${r3(b.sx)},${r3(b.sy)},${r3(b.sz)}@${r3(b.cx)},${r3(b.cy)},${r3(b.cz)}`;
        make = () => puffyBox(b.sx, b.sy, b.sz).translate(b.cx, b.cy, b.cz);
      }
    }
  } else if (mode === "lowpoly" && (boxy || type === "CylinderGeometry")) {
    // paper: walls folded like a screen, legs as folded strips of card, tops as thick card with a visible edge
    const b = boxOf(g);
    if (!b) return null;
    const post = isPost(b) && (boxy || (p.height > 5 * Math.max(p.radiusTop, p.radiusBottom)));
    if (role === "wall" && boxy) {
      key = `paper-wall:${r3(b.sx)},${r3(b.sy)},${r3(b.sz)}@${r3(b.cx)},${r3(b.cy)},${r3(b.cz)}`;
      make = () => pleatedWall(b.sx, b.sy, b.sz).translate(b.cx, b.cy, b.cz);
    } else if (post) {
      const w = Math.max(0.07, Math.max(b.sx, b.sz) * 1.8);
      key = `paper-strip:${r3(w)},${r3(b.sy)}@${r3(b.cx)},${r3(b.cy)},${r3(b.cz)}`;
      make = () => foldedStrip(w, b.sy).translate(b.cx, b.cy, b.cz);
    } else if (boxy) {
      thickenSlab(b, 0.1);
      key = `paper-card:${r3(b.sx)},${r3(b.sy)},${r3(b.sz)}@${r3(b.cx)},${r3(b.cy)},${r3(b.cz)}`;
      make = () => new THREE.BoxGeometry(b.sx, b.sy, b.sz).translate(b.cx, b.cy, b.cz);
    } else {
      const n = Math.min(p.radialSegments ?? 8, 6);
      if (n === p.radialSegments) return null;
      key = `lp-c:${r3(p.radiusTop)},${r3(p.radiusBottom)},${r3(p.height)},${n},${p.openEnded ? 1 : 0},${r3(p.thetaStart)},${r3(p.thetaLength)}`;
      make = () => new THREE.CylinderGeometry(p.radiusTop, p.radiusBottom, p.height, n, 1, !!p.openEnded, p.thetaStart, p.thetaLength);
    }
  } else {
    // lowpoly: fewer segments everywhere, crisp folds instead of rounded edges
    switch (type) {
      case "SphereGeometry": {
        const ws = Math.min(p.widthSegments ?? 8, 6), hs = Math.min(p.heightSegments ?? 6, 4);
        if (ws === p.widthSegments && hs === p.heightSegments) return null;
        key = `lp-s:${r3(p.radius)},${ws},${hs},${r3(p.phiStart)},${r3(p.phiLength)},${r3(p.thetaStart)},${r3(p.thetaLength)}`;
        make = () => new THREE.SphereGeometry(p.radius, ws, hs, p.phiStart, p.phiLength, p.thetaStart, p.thetaLength);
        break;
      }
      case "CylinderGeometry": case "ConeGeometry": {
        const n = Math.min(p.radialSegments ?? 8, 6);
        if (n === p.radialSegments) return null;
        const top = type === "ConeGeometry" ? 0 : p.radiusTop, bot = type === "ConeGeometry" ? p.radius : p.radiusBottom;
        key = `lp-c:${r3(top)},${r3(bot)},${r3(p.height)},${n},${p.openEnded ? 1 : 0},${r3(p.thetaStart)},${r3(p.thetaLength)}`;
        make = () => new THREE.CylinderGeometry(top, bot, p.height, n, 1, !!p.openEnded, p.thetaStart, p.thetaLength);
        break;
      }
      case "CapsuleGeometry":
        key = `lp-cap:${r3(p.radius)},${r3(p.length)}`;
        make = () => new THREE.CapsuleGeometry(p.radius, p.length, 2, 6);
        break;
      case "TorusGeometry": {
        const rs = Math.min(p.radialSegments ?? 8, 4), ts = Math.min(p.tubularSegments ?? 12, 10);
        key = `lp-t:${r3(p.radius)},${r3(p.tube)},${rs},${ts},${r3(p.arc)}`;
        make = () => new THREE.TorusGeometry(p.radius, p.tube, rs, ts, p.arc);
        break;
      }
      case "ExtrudeGeometry": { // rounded boxes -> crisp folded boxes
        if (!g.boundingBox) g.computeBoundingBox();
        const bb = g.boundingBox!;
        bb.getSize(v3); bb.getCenter(c3);
        const sx = v3.x, sy = v3.y, sz = v3.z, cx = c3.x, cy = c3.y, cz = c3.z;
        key = `lp-b:${r3(sx)},${r3(sy)},${r3(sz)}@${r3(cx)},${r3(cy)},${r3(cz)}`;
        make = () => new THREE.BoxGeometry(sx, sy, sz).translate(cx, cy, cz);
        break;
      }
      default: return null;
    }
  }
  let out = geoCache.get(key);
  if (!out) { out = make(); out.userData.themeShared = true; geoCache.set(key, out); }
  return out;
}

interface GeoState { src: THREE.BufferGeometry; out: THREE.BufferGeometry; mode: GeometryMode }

function themeGeometry(mesh: THREE.Mesh, mode: GeometryMode, role: MatRole = "item"): void {
  const ud = mesh.userData as { __geo?: GeoState; brick?: string };
  let st = ud.__geo;
  if (st && mesh.geometry !== st.out) { ud.__geo = undefined; st = undefined; } // React replaced the geometry: start over
  if (st && st.mode === mode) return;
  const src = st?.src ?? mesh.geometry;
  const next = styledGeometry(src, mode, ud.brick, role);
  if (!next) {
    if (st) { mesh.geometry = st.src; ud.__geo = undefined; }
    return;
  }
  mesh.geometry = next;
  ud.__geo = { src, out: next, mode };
}

// ------------------------------------------------------------------------------------------------ studs

const PITCH = 0.16;
const STUD_D = 0.1, STUD_H = 0.042, MAX_PER_FACE = 48;

export interface StudSet { mesh: THREE.Mesh; top: THREE.Object3D; local: THREE.Matrix4[]; color: THREE.Color }

const ax = [new THREE.Vector3(), new THREE.Vector3(), new THREE.Vector3()];
const unit = [new THREE.Vector3(1, 0, 0), new THREE.Vector3(0, 1, 0), new THREE.Vector3(0, 0, 1)];
const tmpM = new THREE.Matrix4(), za = new THREE.Vector3(), xa = new THREE.Vector3(), ya = new THREE.Vector3(), pos = new THREE.Vector3();

/** Stud placements (in the mesh's local space) for the upward face of a box mesh, or null when it has none. */
function studsFor(mesh: THREE.Mesh): THREE.Matrix4[] | null {
  const g = mesh.geometry as THREE.BoxGeometry;
  if (g.type !== "BoxGeometry") return null;
  const p = g.parameters;
  const size = [p.width, p.height, p.depth];
  if (!g.boundingBox) g.computeBoundingBox();
  const center = g.boundingBox!.getCenter(new THREE.Vector3());
  const e = mesh.matrixWorld.elements;
  let up = -1, best = 0;
  const scale = [0, 0, 0];
  for (let i = 0; i < 3; i++) {
    ax[i].set(e[i * 4], e[i * 4 + 1], e[i * 4 + 2]);
    scale[i] = ax[i].length() || 1;
    const d = ax[i].y / scale[i];
    if (Math.abs(d) > Math.abs(best)) { best = d; up = i; }
  }
  if (up < 0 || Math.abs(best) < 0.97) return null;
  const a = (up + 1) % 3, b = (up + 2) % 3;
  const wa = size[a] * scale[a], wb = size[b] * scale[b];
  if (wa < 0.07 || wb < 0.07 || wa * wb > 10) return null;
  let pitch = PITCH;
  let na = Math.max(1, Math.floor(wa / pitch)), nb = Math.max(1, Math.floor(wb / pitch));
  if (na * nb > MAX_PER_FACE) { pitch = Math.sqrt((wa * wb) / MAX_PER_FACE); na = Math.max(1, Math.floor(wa / pitch)); nb = Math.max(1, Math.floor(wb / pitch)); }
  const dWorld = Math.min(STUD_D, Math.min(wa / na, wb / nb) * 0.62);
  const sign = Math.sign(best);
  // basis: stud X along local a, stud Y along local up (outward), Z = X x Y (keeps the winding right)
  xa.copy(unit[a]); ya.copy(unit[up]).multiplyScalar(sign); za.crossVectors(xa, ya);
  const sx = dWorld / scale[a], sy = STUD_H / scale[up], sz = dWorld / scale[b];
  const out: THREE.Matrix4[] = [];
  for (let i = 0; i < na; i++) for (let j = 0; j < nb; j++) {
    pos.copy(center);
    pos.setComponent(up, center.getComponent(up) + (sign * size[up]) / 2);
    pos.setComponent(a, center.getComponent(a) + ((i + 0.5) / na - 0.5) * size[a]);
    pos.setComponent(b, center.getComponent(b) + ((j + 0.5) / nb - 0.5) * size[b]);
    tmpM.makeBasis(xa.clone().multiplyScalar(sx), ya.clone().multiplyScalar(sy), za.clone().multiplyScalar(Math.abs(sz)));
    tmpM.setPosition(pos);
    out.push(tmpM.clone());
  }
  return out;
}

// ------------------------------------------------------------------------------------------------ the walk

export interface ThemeSweep { studs: StudSet[] | null }

function visibleChain(o: THREE.Object3D | null): boolean {
  for (let x = o; x; x = x.parent) if (!x.visible) return false;
  return true;
}

/**
 * Restyles every mesh under `root` for `style` (idempotent: cheap when nothing changed). With `collect`, also returns the
 * studs of the bricks style. `root` itself may be the scene.
 */
export function themeTree(root: THREE.Object3D, style: OfficeStyle, collect: boolean): ThemeSweep {
  const look = lookOf(style);
  const studs: StudSet[] | null = collect && look.studs ? [] : null;
  const walk = (o: THREE.Object3D, noStuds: boolean, inherited: MatRole, top: THREE.Object3D) => {
    const ud = o.userData;
    if (ud.noTheme) return;
    const ns = noStuds || !!ud.noStuds;
    const role: MatRole = ud.figure ? "figure" : ud.ground ? "ground" : ud.wall ? "wall" : inherited;
    const gr = role === "ground";
    const mesh = o as THREE.Mesh;
    if (mesh.isMesh && !(o as THREE.InstancedMesh).isInstancedMesh && mesh.material) {
      const mats = Array.isArray(mesh.material) ? mesh.material : [mesh.material];
      let lit = true;
      for (const m of mats) { if (isLit(m)) themeMaterial(m, style, role); else lit = false; }
      if (lit && !gr) themeGeometry(mesh, look.geometry, role);
      else if (mesh.userData.__geo) themeGeometry(mesh, "keep");
      if (studs && lit && !ns && mats.length === 1) {
        const m = mats[0] as THREE.MeshStandardMaterial;
        if (!(m.transparent && m.opacity < 0.99) && visibleChain(mesh)) {
          const local = studsFor(mesh);
          if (local?.length) studs.push({ mesh, top, local, color: m.color });
        }
      }
    }
    for (const c of o.children) walk(c, ns, role, top);
  };
  const r = root.userData;
  const rootRole: MatRole = r.figure ? "figure" : r.ground ? "ground" : r.wall ? "wall" : "item";
  for (const c of root.children) walk(c, !!r.noStuds, rootRole, c);
  return { studs };
}

// ------------------------------------------------------------------------------------------------ one instanced mesh of studs

export const STUD_GEOMETRY = (() => {
  const g = new THREE.CylinderGeometry(0.5, 0.5, 1, 12);
  g.translate(0, 0.5, 0);
  return g;
})();

/** All studs of the scene in one draw call; matrices follow their meshes (only the ones that moved are rewritten). */
export class StudBatch {
  readonly mesh: THREE.InstancedMesh;
  private sets: StudSet[] = [];
  private last: Float32Array[] = [];
  private count = 0;
  private readonly m = new THREE.Matrix4();
  private readonly zero = new THREE.Matrix4().makeScale(0, 0, 0);
  constructor(readonly capacity = 6000) {
    const mat = new THREE.MeshStandardMaterial({ color: "#ffffff", roughness: 0.3, metalness: 0 });
    this.mesh = new THREE.InstancedMesh(STUD_GEOMETRY, mat, capacity);
    this.mesh.count = 0;
    this.mesh.frustumCulled = false;
    this.mesh.castShadow = false; this.mesh.receiveShadow = true;
    this.mesh.userData.noTheme = true;
    this.mesh.instanceMatrix.setUsage(THREE.DynamicDrawUsage);
  }
  /** New stud sets from a sweep (colors are taken now; matrices on the next update). */
  set(sets: StudSet[] | null): void {
    this.sets = sets ?? [];
    this.last = this.sets.map(() => new Float32Array(16).fill(NaN));
    let n = 0;
    for (const s of this.sets) {
      for (let i = 0; i < s.local.length && n < this.capacity; i++, n++) this.mesh.setColorAt(n, s.color);
    }
    this.count = n;
    this.mesh.count = n;
    if (this.mesh.instanceColor) this.mesh.instanceColor.needsUpdate = true;
  }
  update(): void {
    let n = 0, dirty = false;
    for (let k = 0; k < this.sets.length; k++) {
      const s = this.sets[k];
      const gone = !s.top.parent;
      const e = s.mesh.matrixWorld.elements, last = this.last[k];
      let same = !gone;
      if (same) for (let q = 0; q < 16; q++) if (last[q] !== e[q]) { same = false; break; }
      if (same) { n += s.local.length; continue; }
      if (!gone) last.set(e); else last.fill(NaN);
      for (let i = 0; i < s.local.length && n < this.capacity; i++, n++) {
        this.mesh.setMatrixAt(n, gone ? this.zero : this.m.multiplyMatrices(s.mesh.matrixWorld, s.local[i]));
      }
      dirty = true;
    }
    if (dirty) this.mesh.instanceMatrix.needsUpdate = true;
    this.mesh.count = Math.min(n, this.count);
  }
  dispose(): void { this.mesh.dispose(); (this.mesh.material as THREE.Material).dispose(); }
}

/** Static studs for a one-shot render (shop thumbnails): one instanced mesh added under `root`. */
export function addStaticStuds(root: THREE.Object3D, sets: StudSet[] | null): THREE.InstancedMesh | null {
  if (!sets?.length) return null;
  const total = sets.reduce((a, s) => a + s.local.length, 0);
  const inst = new THREE.InstancedMesh(STUD_GEOMETRY, new THREE.MeshStandardMaterial({ color: "#ffffff", roughness: 0.3 }), total);
  inst.userData.noTheme = true;
  root.updateMatrixWorld(true);
  const inv = new THREE.Matrix4().copy(root.matrixWorld).invert();
  const m = new THREE.Matrix4();
  let n = 0;
  const w = new THREE.Matrix4();
  for (const s of sets) for (const l of s.local) { w.multiplyMatrices(s.mesh.matrixWorld, l); inst.setMatrixAt(n, m.multiplyMatrices(inv, w)); inst.setColorAt(n, s.color); n++; }
  root.add(inst);
  return inst;
}
