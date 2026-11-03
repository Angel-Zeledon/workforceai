import * as THREE from "three";
import { RoundedBoxGeometry } from "three/examples/jsm/geometries/RoundedBoxGeometry.js";
import type { OfficeStyle } from "@/lib/officeLayoutModel";
import { lookOf, styleMaterial, type GeometryMode, type MatRole, type PbrKind } from "./officeTheme";
import { foldedStrip, pleatedWall, puffyBox, wavyWall } from "./themeShapes";

/**
 * The style transform layer applied to the rendered scene (docs/architecture/office-editor.md, "Styles"). Every builder
 * keeps drawing its classic meshes from the same specs; this layer then restyles what was drawn:
 *  - lit materials get the style's finish (officeTheme.styleMaterial; never a color), surface detail and shading;
 *  - geometry can be swapped for toy-brick (bevelled blocks, round bricks), clay or papercraft versions, cached and
 *    shared, never per frame;
 *  - bricks collect studs for the upward faces of blocks and round bricks into one instanced mesh.
 * Everything stores the original in `userData`, so going back to "classic" restores the exact original objects.
 * Unlit materials (labels, glows, screens, editor frames) are never touched. A subtree flagged `userData.noTheme` is
 * skipped; `userData.noStuds` keeps studs off it (characters, which move every frame). The role of a subtree picks the
 * variant of the style: `userData.figure` a character, `userData.ground` the floor / outer ground / desk mats (no
 * geometry swap), `userData.wall` the room's walls; `userData.surfaceHint` says what a ground is (the floor's kind,
 * "backdrop").
 */

const isLit = (m: THREE.Material): m is THREE.MeshStandardMaterial =>
  (m as THREE.MeshStandardMaterial).isMeshStandardMaterial === true || (m as unknown as THREE.MeshLambertMaterial).isMeshLambertMaterial === true || (m as unknown as THREE.MeshPhongMaterial).isMeshPhongMaterial === true;

type U<T> = { value: T };
interface MatState {
  style: OfficeStyle;
  /** the role (item / figure / ground / wall) and hint the material was last styled for */
  variant: string;
  roughness: number; metalness: number; flat: boolean; envIntensity: number;
  /** the material's own reflection map before any style (usually none) */
  envMap: THREE.Texture | null;
  obc: THREE.Material["onBeforeCompile"]; key: THREE.Material["customProgramCacheKey"];
  /** the style shader patch is installed */
  fx: boolean;
  /** the real texture set this material uses in the active style (null: none) */
  pbr: PbrKind | null;
  u: {
    /** packed in two vec4 (fewer uniforms to walk for every object drawn): A = surface, bump, tex, ink;
     *  B = cel, texture set on, tile (m), roughness variation */
    uThA: U<THREE.Vector4>; uThB: U<THREE.Vector4>; uThLump: U<number>; uThMap: U<THREE.Texture | null>; uThRgh: U<THREE.Texture | null>;
  };
}

/**
 * One shader patch for every style, so all styled materials of a base type share one program (switched by uniforms).
 * Nothing in it is a grid of cells: the old value noise (one random value per cubic cell, blended) left blocky,
 * axis-aligned patches at the scale seen from the camera, the "Minecraft" look; the procedural detail now comes from
 * smooth 3D simplex noise, and the realistic surfaces from photographed CC0 textures.
 *  - real textures: triplanar projection in the object's space, scaled to meters (the meshes have no useful UVs); the
 *    normal map is blended per axis ("whiteout"), the albedo detail multiplies the color around 1 (its mean is 1, so
 *    the catalog color stays the same on average) and the roughness detail multiplies the finish;
 *  - procedural relief (clay dents, paper grain, brick seams): a height sampled at three points in object space gives a
 *    true gradient (no screen-space derivatives, which drew 2x2-pixel steps), faded out before it gets finer than a
 *    pixel so it never shimmers;
 *  - lump: hand-made wobble of the vertices along their normal (clay);
 *  - ink: the surface darkens toward its silhouette like a drawn crease (paper);
 *  - cel: the light is regrouped in flat bands; the color itself is untouched (cartoon).
 * The perturbed normal is computed in object space and taken to view space with `normalMatrix`, declared in the
 * fragment stage too (the same uniform three.js already sets for the vertex stage).
 */
const NOISE = `
vec3 thMod289(vec3 x) { return x - floor(x * (1.0 / 289.0)) * 289.0; }
vec4 thMod289(vec4 x) { return x - floor(x * (1.0 / 289.0)) * 289.0; }
vec4 thPermute(vec4 x) { return thMod289(((x * 34.0) + 1.0) * x); }
/** 3D simplex noise (after Ashima Arts / Stefan Gustavson, MIT), -1..1, smooth, no cell pattern. */
float thSimplex(vec3 v) {
  const vec2 C = vec2(1.0 / 6.0, 1.0 / 3.0);
  vec3 i = floor(v + dot(v, C.yyy));
  vec3 x0 = v - i + dot(i, C.xxx);
  vec3 g = step(x0.yzx, x0.xyz);
  vec3 l = 1.0 - g;
  vec3 i1 = min(g.xyz, l.zxy), i2 = max(g.xyz, l.zxy);
  vec3 x1 = x0 - i1 + C.xxx, x2 = x0 - i2 + C.yyy, x3 = x0 - 0.5;
  i = thMod289(i);
  vec4 p = thPermute(thPermute(thPermute(i.z + vec4(0.0, i1.z, i2.z, 1.0)) + i.y + vec4(0.0, i1.y, i2.y, 1.0)) + i.x + vec4(0.0, i1.x, i2.x, 1.0));
  vec4 j = p - 49.0 * floor(p * (1.0 / 49.0));
  vec4 x_ = floor(j * (1.0 / 7.0)), y_ = floor(j - 7.0 * x_);
  vec4 x = (x_ * 2.0 + 0.5) / 7.0 - 1.0, y = (y_ * 2.0 + 0.5) / 7.0 - 1.0;
  vec4 h = 1.0 - abs(x) - abs(y);
  vec4 b0 = vec4(x.xy, y.xy), b1 = vec4(x.zw, y.zw);
  vec4 s0 = floor(b0) * 2.0 + 1.0, s1 = floor(b1) * 2.0 + 1.0;
  vec4 sh = -step(h, vec4(0.0));
  vec4 a0 = b0.xzyw + s0.xzyw * sh.xxyy, a1 = b1.xzyw + s1.xzyw * sh.zzww;
  vec3 g0 = vec3(a0.xy, h.x), g1 = vec3(a0.zw, h.y), g2 = vec3(a1.xy, h.z), g3 = vec3(a1.zw, h.w);
  vec4 nr = 1.79284291400159 - 0.85373472095314 * vec4(dot(g0, g0), dot(g1, g1), dot(g2, g2), dot(g3, g3));
  g0 *= nr.x; g1 *= nr.y; g2 *= nr.z; g3 *= nr.w;
  vec4 m = max(0.6 - vec4(dot(x0, x0), dot(x1, x1), dot(x2, x2), dot(x3, x3)), 0.0);
  m = m * m;
  return 42.0 * dot(m * m, vec4(dot(g0, x0), dot(g1, x1), dot(g2, x2), dot(g3, x3)));
}`;
const VERT_HEAD = `#include <common>
varying vec3 vThPos;
varying vec3 vThNrm;
uniform float uThLump;${NOISE}`;
const VERT_BODY = `#include <begin_vertex>
vec3 thScl = max(vec3(length(modelMatrix[0].xyz), length(modelMatrix[1].xyz), length(modelMatrix[2].xyz)), vec3(1e-4));
vThPos = transformed * thScl; // object space in meters: the detail sticks to the piece and is never stretched
vThNrm = normalize(objectNormal / thScl);
if (uThLump > 0.0) transformed += objectNormal * (thSimplex(position * 3.5) * 0.5 + thSimplex(position * 9.0) * 0.2) * uThLump;`;
const FRAG_HEAD = `#include <common>
varying vec3 vThPos;
varying vec3 vThNrm;
uniform mat3 normalMatrix;
uniform vec4 uThA; uniform vec4 uThB;
#define uThSurf uThA.x
#define uThBump uThA.y
#define uThTex uThA.z
#define uThInk uThA.w
#define uThCel uThB.x
#define uThMapOn uThB.y
#define uThTile uThB.z
#define uThRoughVar uThB.w
uniform sampler2D uThMap; uniform sampler2D uThRgh;${NOISE}
/** Toy-brick courses: 1 on a brick, 0 in the thin seam between bricks, antialiased by the pixel size px (meters).
 *  Declared before its callers: GLSL has no hoisting, a call above the definition fails to compile and every styled
 *  material disappears. */
float thBrick(vec3 p, float px) {
  vec2 sz = vec2(0.64, 0.192);
  vec2 b = vec2(p.x + p.z, p.y) / sz;
  b.x += mod(floor(b.y), 2.0) * 0.5;
  vec2 f = fract(b);
  vec2 d = min(f, 1.0 - f) * sz;
  return smoothstep(0.003, 0.003 + max(px, 0.002) * 1.5, min(d.x, d.y));
}
/** Toy baseplate: 1 on the top of a stud (one every 16 cm), 0 between them; ring 1 just around a stud (its contact
 *  shadow). Only on faces looking up. */
vec2 thStud(vec3 p, float px) {
  vec2 c = (fract(p.xz / 0.16) - 0.5) * 0.16;
  float d = length(c), aa = max(px, 0.0015);
  float top = smoothstep(0.05 + aa, 0.05 - aa, d);
  float ring = smoothstep(0.05 - aa, 0.05 + aa, d) * (1.0 - smoothstep(0.05, 0.072, d));
  float lod = 1.0 - smoothstep(0.035, 0.06, px); // fewer than ~3 pixels per stud: an even plate, no shimmer
  return vec2(top, ring) * lod;
}
/** Procedural relief in meters at object-space point p, faded out (lod 0..1) where it would be finer than a pixel. */
float thHeight(vec3 p, float k, float px) {
  float h = 0.0;
  if (k < 1.5) { // clay: soft pressed dents and smaller finger marks
    float f = 1.0 - smoothstep(0.02, 0.05, px);
    h = (thSimplex(p * 4.0) * 0.6 + thSimplex(p * 13.0) * 0.28 * f + thSimplex(p * 31.0) * 0.1 * f) * 0.0055;
  } else if (k < 2.5) { // paper / card: fibres running mostly one way, and the slight cockle of a sheet
    float f = 1.0 - smoothstep(0.006, 0.014, px);
    h = (thSimplex(p * vec3(70.0, 70.0, 220.0)) * 0.6 * f + thSimplex(p * 9.0) * 0.4) * 0.0009;
  } else if (k > 10.5) h = thStud(p, px).x * 0.012; // baseplate studs
  else if (k > 9.5) h = thBrick(p, px) * 0.004; // brick seams: a shallow groove
  return h;
}
/** Shade of the procedural surface around 1 (multiplies the color; mean 1). */
float thAlbedo(vec3 p, float k, float px) {
  float a = 1.0;
  if (k < 2.5 && k > 1.5) { // paper: a soft mottle and, close up, fibres
    float f = 1.0 - smoothstep(0.006, 0.02, px);
    a = 1.0 + thSimplex(p * 5.0) * 0.5 + thSimplex(p * vec3(40.0, 40.0, 120.0)) * 0.5 * f;
  } else if (k > 10.5) { vec2 s = thStud(p, px); a = 1.0 + s.x * 0.05 - s.y * 0.16; } // stud tops catch light, rings shade
  else if (k > 9.5) a = 1.0 - (1.0 - thBrick(p, px)) * 2.5; // the dark seam between bricks
  return a;
}
/** Object-space normal offset of the procedural relief (true gradient from three samples). */
vec3 thRelief(vec3 p, vec3 n, float k, float amt, float px) {
  vec3 t = normalize(abs(n.y) < 0.9 ? cross(n, vec3(0.0, 1.0, 0.0)) : cross(n, vec3(1.0, 0.0, 0.0)));
  vec3 b = cross(n, t);
  float e = 0.0025;
  float h0 = thHeight(p, k, px);
  vec2 g = vec2(thHeight(p + t * e, k, px) - h0, thHeight(p + b * e, k, px) - h0) / e;
  return -(t * g.x + b * g.y) * amt;
}
/** Triplanar sample of the real texture set: albedo detail (around 1), roughness detail (around 1), normal (object). */
vec3 thTriplanar(vec3 p, vec3 n, float strength, out vec3 nOut) {
  vec3 w = pow(abs(n), vec3(4.0));
  w /= w.x + w.y + w.z;
  vec2 ux = p.zy / uThTile, uy = p.zx / uThTile, uz = p.xy / uThTile; // tops: the grain runs along the long side
  vec4 ax = texture2D(uThMap, ux), ay = texture2D(uThMap, uy), az = texture2D(uThMap, uz);
  float r = texture2D(uThRgh, ux).r * w.x + texture2D(uThRgh, uy).r * w.y + texture2D(uThRgh, uz).r * w.z;
  vec3 tx = vec3((ax.rg * 2.0 - 1.0) * strength, 0.0), ty = vec3((ay.rg * 2.0 - 1.0) * strength, 0.0), tz = vec3((az.rg * 2.0 - 1.0) * strength, 0.0);
  tx.z = sqrt(max(0.0, 1.0 - dot(tx.xy, tx.xy))); ty.z = sqrt(max(0.0, 1.0 - dot(ty.xy, ty.xy))); tz.z = sqrt(max(0.0, 1.0 - dot(tz.xy, tz.xy)));
  // whiteout blend (B. Golus): each projection's tangent normal is added to the surface normal in its own axes
  tx = vec3(tx.xy + n.zy, abs(tx.z) * n.x);
  ty = vec3(ty.xy + n.zx, abs(ty.z) * n.y);
  tz = vec3(tz.xy + n.xy, abs(tz.z) * n.z);
  nOut = normalize(tx.zyx * w.x + ty.yzx * w.y + tz.xyz * w.z);
  float d = ax.b * w.x + ay.b * w.y + az.b * w.z;
  return vec3(d * 2.0, r * 2.0, 0.0);
}`;
/** Surface detail: computed once, right after the color is known (every lit material has this chunk). */
const FRAG_COLOR = `#include <color_fragment>
vec3 thN = normalize(vThNrm);
vec3 thDelta = vec3(0.0);
float thRgh = 1.0;
float thPx = length(fwidth(vThPos));
if (uThMapOn > 0.5) {
  vec3 thNt;
  vec3 thS = thTriplanar(vThPos, thN, uThBump, thNt);
  thDelta = thNt - thN;
  diffuseColor.rgb *= mix(1.0, thS.x, uThTex);
  thRgh = mix(1.0, thS.y, uThRoughVar);
} else if (uThSurf > 0.5 && (uThSurf < 10.5 || thN.y > 0.9)) {
  thDelta = thRelief(vThPos, thN, uThSurf, uThBump, thPx);
  if (uThTex > 0.0) diffuseColor.rgb *= max(0.0, 1.0 + (thAlbedo(vThPos, uThSurf, thPx) - 1.0) * uThTex);
}`;
const FRAG_ROUGH = `#include <roughnessmap_fragment>
roughnessFactor = clamp(roughnessFactor * thRgh, 0.03, 1.0);`;
const FRAG_NORMAL = `#include <normal_fragment_maps>
if (dot(thDelta, thDelta) > 1e-8) normal = normalize(normal + faceDirection * (normalMatrix * thDelta));`;
/** The room reflection adds reflections only: its diffuse light is dropped, so the office is lit exactly as in classic
 *  (the light is the organization's mood setting, never the style's). */
const FRAG_IBL = `#include <lights_fragment_maps>
iblIrradiance *= 0.0;`;
const FRAG_INK = `#include <emissivemap_fragment>
if (uThInk > 0.0) {
  float thF = 1.0 - clamp(abs(dot(normalize(normal), normalize(vViewPosition))), 0.0, 1.0);
  diffuseColor.rgb *= 1.0 - uThInk * smoothstep(0.62, 0.9, thF);
}`;
const FRAG_CEL = `if (uThCel > 0.5) {
  float thL = dot(outgoingLight, vec3(0.299, 0.587, 0.114));
  float thB = max(dot(diffuseColor.rgb, vec3(0.299, 0.587, 0.114)), 0.03);
  float thS = thL / thB;
  // flat bands a fixed ratio apart (whatever the light level), so lit, half-lit and shaded sides always separate; the
  // band edge is antialiased over one pixel instead of stepping
  float thE = log2(max(thS, 0.02)) * 1.6;
  float thW = min(max(fwidth(thE), 1e-3), 0.3);
  float thQ = floor(thE) + smoothstep(0.5 - thW, 0.5 + thW, fract(thE));
  outgoingLight = diffuseColor.rgb * exp2(thQ / 1.6) + totalEmissiveRadiance;
}
#include <opaque_fragment>`;

/**
 * The room reflection of the styles that use one (realistic, toy plastic). It is set on each styled material
 * (`envMap`), never as `scene.environment`: three.js ignores `envMapIntensity` for materials lit by `scene.environment`
 * (it uses the scene's single `environmentIntensity`), which washed out the whole office at full strength. Per
 * material, every finish keeps its own reflection strength (matte walls 0.05 ... metal 0.7) and unstyled materials get
 * no reflection at all.
 */
let roomEnv: THREE.Texture | null = null;

function applyEnv(m: THREE.MeshStandardMaterial, st: MatState, style: OfficeStyle) {
  const want = lookOf(style).env !== "none" && roomEnv ? roomEnv : st.envMap;
  if (m.envMap !== want) { m.envMap = want; m.needsUpdate = true; }
}

/** Calls `f` on every material under root that this layer has styled. */
function eachStyled(root: THREE.Object3D, f: (m: THREE.Material, st: MatState) => void) {
  root.traverse((o) => {
    const mats = (o as THREE.Mesh).material;
    if (!mats) return;
    for (const m of Array.isArray(mats) ? mats : [mats]) {
      const st = (m.userData as { __th?: MatState }).__th;
      if (st) f(m, st);
    }
  });
}

/** Installs (or removes, with null) the room reflection and applies it to every material already styled under root. */
export function setRoomEnvironment(env: THREE.Texture | null, root: THREE.Object3D) {
  roomEnv = env;
  eachStyled(root, (m, st) => { if ("roughness" in m) applyEnv(m as THREE.MeshStandardMaterial, st, st.style); });
}

/** The real texture sets in memory (loaded by ThemeLayer only while a style that uses them is active). */
export interface PbrSet { map: THREE.Texture; rgh: THREE.Texture }
const pbrSets: Partial<Record<PbrKind, PbrSet>> = {};

function applyPbr(st: MatState) {
  const set = st.pbr ? pbrSets[st.pbr] : undefined;
  st.u.uThMap.value = set?.map ?? null;
  st.u.uThRgh.value = set?.rgh ?? null;
  st.u.uThB.value.y = set ? 1 : 0;
}

/** Installs (or removes, with null) a texture set and updates every material already styled under root. */
export function setPbrSet(kind: PbrKind, set: PbrSet | null, root: THREE.Object3D) {
  if (set) pbrSets[kind] = set; else delete pbrSets[kind];
  eachStyled(root, (_, st) => { if (st.pbr === kind) applyPbr(st); });
}

function restoreShader(m: THREE.Material, st: MatState) {
  m.onBeforeCompile = st.obc; m.customProgramCacheKey = st.key; m.needsUpdate = true; st.fx = false;
}

function themeMaterial(m: THREE.MeshStandardMaterial, style: OfficeStyle, role: MatRole, hint?: string): void {
  const ud = m.userData as { __th?: MatState };
  let st = ud.__th;
  const variant = hint ? `${role}:${hint}` : role;
  if (!st) {
    if (style === "classic") return;
    st = {
      style: "classic", variant, roughness: m.roughness ?? 1, metalness: m.metalness ?? 0, flat: !!m.flatShading, envIntensity: m.envMapIntensity ?? 1,
      envMap: ("envMap" in m ? m.envMap : null) ?? null,
      obc: m.onBeforeCompile, key: m.customProgramCacheKey, fx: false, pbr: null,
      u: {
        uThA: { value: new THREE.Vector4() }, uThB: { value: new THREE.Vector4(0, 0, 1, 0) }, uThLump: { value: 0 }, uThMap: { value: null }, uThRgh: { value: null },
      },
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
    st.pbr = null; applyPbr(st);
    st.style = "classic";
    return;
  }
  // colors are never touched: only the finish, the shading and the surface
  const o = styleMaterial({ roughness: st.roughness, metalness: st.metalness, transparent: m.transparent && m.opacity < 0.99, hint }, style, role);
  if (std) { m.roughness = o.roughness; m.metalness = o.metalness; m.envMapIntensity = o.envIntensity; applyEnv(m, st, style); }
  const flat = st.flat || o.flat;
  if (m.flatShading !== flat) { m.flatShading = flat; m.needsUpdate = true; }
  st.u.uThA.value.set(o.surface, o.bump, o.tex, o.ink);
  const on = st.u.uThB.value.y;
  st.u.uThB.value.set(o.cel ? 1 : 0, on, o.tile, o.roughVar);
  st.u.uThLump.value = o.lump;
  st.pbr = o.pbr; applyPbr(st);
  const needs = o.surface > 0 || o.lump > 0 || o.ink > 0 || o.tex > 0 || o.cel || !!o.pbr || o.envIntensity > 0;
  if (needs && !st.fx) {
    const u = st.u, base = st.obc;
    m.onBeforeCompile = (shader, renderer) => {
      base?.call(m, shader, renderer);
      Object.assign(shader.uniforms, u);
      shader.vertexShader = shader.vertexShader.replace("#include <common>", VERT_HEAD).replace("#include <begin_vertex>", VERT_BODY);
      shader.fragmentShader = shader.fragmentShader
        .replace("#include <common>", FRAG_HEAD)
        .replace("#include <color_fragment>", FRAG_COLOR)
        .replace("#include <roughnessmap_fragment>", FRAG_ROUGH)
        .replace("#include <normal_fragment_maps>", FRAG_NORMAL)
        .replace("#include <lights_fragment_maps>", FRAG_IBL)
        .replace("#include <emissivemap_fragment>", FRAG_INK)
        .replace("#include <opaque_fragment>", FRAG_CEL);
    };
    m.customProgramCacheKey = () => "office-style-fx-2";
    m.needsUpdate = true;
    st.fx = true;
  } else if (!needs && st.fx) restoreShader(m, st);
  st.style = style;
}

// ------------------------------------------------------------------------------------------------ geometry

const geoCache = new Map<string, THREE.BufferGeometry>();
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

/** A round toy brick: a cylinder of radius r and height h with chamfered rims, centered on the origin. */
function roundBrick(r: number, h: number): THREE.BufferGeometry {
  const c = Math.min(r * 0.18, h * 0.18, 0.012);
  const pts = [
    new THREE.Vector2(0, -h / 2), new THREE.Vector2(r - c, -h / 2), new THREE.Vector2(r - c * 0.3, -h / 2 + c * 0.3), new THREE.Vector2(r, -h / 2 + c),
    new THREE.Vector2(r, h / 2 - c), new THREE.Vector2(r - c * 0.3, h / 2 - c * 0.3), new THREE.Vector2(r - c, h / 2), new THREE.Vector2(0, h / 2),
  ];
  const g = new THREE.LatheGeometry(pts, 24);
  g.computeVertexNormals();
  return g;
}

function styledGeometry(g: THREE.BufferGeometry, mode: GeometryMode, hint: string | undefined, role: MatRole): THREE.BufferGeometry | null {
  if (mode === "keep") return null;
  const type = g.type;
  const p = (g as unknown as { parameters?: Record<string, number> }).parameters ?? {};
  const boxy = type === "BoxGeometry" || type === "ExtrudeGeometry";
  let key = "";
  let make: () => THREE.BufferGeometry;
  if (mode === "blocks") {
    // toy bricks: boxes become blocks with a small molded bevel (light runs along every edge), and what is round becomes
    // a round brick (a cylinder with chamfered rims), never a cube: no voxel look. Both get studs (studsFor).
    const round = hint === "cyl" || type === "SphereGeometry" || type === "CapsuleGeometry" || type === "IcosahedronGeometry" || type === "OctahedronGeometry" || type === "DodecahedronGeometry" || type === "LatheGeometry";
    if (!round && !boxy) return null;
    const b = boxOf(g);
    if (!b) return null;
    const { sx, sy, sz, cx, cy, cz } = b;
    if (round) {
      const r = Math.max(sx, sz) / 2, h = hint === "cyl" ? sy * 0.92 : sy;
      key = `rnd:${r3(r)},${r3(h)}@${r3(cx)},${r3(cy)},${r3(cz)}`;
      make = () => { const out = roundBrick(r, h).translate(cx, cy, cz); out.userData.round = { r, h, c: [cx, cy, cz] }; return out; };
    } else {
      const m = Math.min(sx, sy, sz);
      if (m < 0.008 && type === "BoxGeometry") return null; // sheets and labels stay as they are
      key = `blk:${r3(sx)},${r3(sy)},${r3(sz)}@${r3(cx)},${r3(cy)},${r3(cz)}`;
      make = () => {
        const out = new RoundedBoxGeometry(sx, sy, sz, 2, Math.min(0.014, m * 0.2)).translate(cx, cy, cz);
        out.userData.box = { w: sx, h: sy, d: sz, c: [cx, cy, cz] };
        return out;
      };
    }
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
        // a whole sphere becomes a folded paper ball (icosphere: even triangular facets); a 6 x 4 UV sphere read as a
        // block from the camera. Partial spheres (hair caps, domes) keep their cut with fewer, still roundish facets.
        const full = (p.phiLength ?? Math.PI * 2) > 6.2 && (p.thetaLength ?? Math.PI) > 3.1 && !(p.thetaStart > 0.01);
        if (full) {
          key = `lp-ico:${r3(p.radius)}`;
          make = () => new THREE.IcosahedronGeometry(p.radius, 1);
          break;
        }
        const ws = Math.min(p.widthSegments ?? 8, 10), hs = Math.min(p.heightSegments ?? 6, 6);
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
        make = () => new THREE.CapsuleGeometry(p.radius, p.length, 2, 7);
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
/** Per mesh, kept outside `userData`: React replaces `userData` whenever a component re-renders with a literal
 *  (`userData={{ ... }}`), which used to lose the original geometry and leave a toy brick in the next style. */
const geoState = new WeakMap<THREE.Mesh, GeoState>();

function themeGeometry(mesh: THREE.Mesh, mode: GeometryMode, role: MatRole = "item"): void {
  const ud = mesh.userData as { brick?: string };
  let st = geoState.get(mesh);
  if (st && mesh.geometry !== st.out) { geoState.delete(mesh); st = undefined; } // React replaced the geometry: start over
  if (st && st.mode === mode) return;
  const src = st?.src ?? mesh.geometry;
  const next = styledGeometry(src, mode, ud.brick, role);
  if (!next) {
    if (st) { mesh.geometry = st.src; geoState.delete(mesh); }
    return;
  }
  mesh.geometry = next;
  geoState.set(mesh, { src, out: next, mode });
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
  const g = mesh.geometry;
  const ud = g.userData as { box?: { w: number; h: number; d: number; c: number[] }; round?: { r: number; h: number; c: number[] } };
  let size: number[];
  const center = new THREE.Vector3();
  if (ud.box) { size = [ud.box.w, ud.box.h, ud.box.d]; center.fromArray(ud.box.c); }
  else if (ud.round) { size = [ud.round.r * 2, ud.round.h, ud.round.r * 2]; center.fromArray(ud.round.c); }
  else if (g.type === "BoxGeometry") {
    const p = (g as THREE.BoxGeometry).parameters;
    size = [p.width, p.height, p.depth];
    if (!g.boundingBox) g.computeBoundingBox();
    g.boundingBox!.getCenter(center);
  } else return null;
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
  if (ud.round && up !== 1) return null; // a round brick lying on its side has no top
  const a = (up + 1) % 3, b = (up + 2) % 3;
  const wa = size[a] * scale[a], wb = size[b] * scale[b];
  if (ud.round) return roundStuds(size, center, scale, Math.sign(best), wa, wb);
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

/** Studs on the top of a round brick: one in the middle, or a grid of the ones that fit inside the circle. */
function roundStuds(size: number[], center: THREE.Vector3, scale: number[], sign: number, wa: number, wb: number): THREE.Matrix4[] | null {
  const dia = Math.min(wa, wb);
  if (dia < 0.06 || dia > 3) return null;
  const d = Math.min(STUD_D, dia * 0.5);
  const spots: [number, number][] = [];
  if (dia < PITCH * 2.2) spots.push([0, 0]);
  else {
    const n = Math.floor(dia / PITCH), r = dia / 2;
    for (let i = 0; i < n; i++) for (let j = 0; j < n; j++) {
      const x = (i + 0.5 - n / 2) * PITCH, z = (j + 0.5 - n / 2) * PITCH;
      if (Math.hypot(x, z) + d * 0.6 < r && spots.length < MAX_PER_FACE) spots.push([x, z]);
    }
  }
  xa.copy(unit[2]); ya.copy(unit[1]).multiplyScalar(sign); za.crossVectors(xa, ya);
  const out: THREE.Matrix4[] = [];
  for (const [x, z] of spots) {
    pos.set(center.x + x / scale[0], center.y + (sign * size[1]) / 2, center.z + z / scale[2]);
    tmpM.makeBasis(xa.clone().multiplyScalar(d / scale[2]), ya.clone().multiplyScalar(STUD_H / scale[1]), za.clone().multiplyScalar(d / scale[0]));
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
  const walk = (o: THREE.Object3D, noStuds: boolean, inherited: MatRole, top: THREE.Object3D, hintIn: string | undefined) => {
    const ud = o.userData;
    if (ud.noTheme) return;
    const ns = noStuds || !!ud.noStuds;
    const role: MatRole = ud.figure ? "figure" : ud.ground ? "ground" : ud.wall ? "wall" : inherited;
    const hint: string | undefined = ud.surfaceHint ?? hintIn;
    const gr = role === "ground";
    const mesh = o as THREE.Mesh;
    if (mesh.isMesh && !(o as THREE.InstancedMesh).isInstancedMesh && mesh.material) {
      const mats = Array.isArray(mesh.material) ? mesh.material : [mesh.material];
      let lit = true;
      for (const m of mats) { if (isLit(m)) themeMaterial(m, style, role, hint); else lit = false; }
      if (lit && !gr) themeGeometry(mesh, look.geometry, role);
      else if (geoState.has(mesh)) themeGeometry(mesh, "keep");
      if (studs && lit && !ns && mats.length === 1) {
        const m = mats[0] as THREE.MeshStandardMaterial;
        if (!(m.transparent && m.opacity < 0.99) && visibleChain(mesh)) {
          const local = studsFor(mesh);
          if (local?.length) studs.push({ mesh, top, local, color: m.color });
        }
      }
    }
    for (const c of o.children) walk(c, ns, role, top, hint);
  };
  const r = root.userData;
  const rootRole: MatRole = r.figure ? "figure" : r.ground ? "ground" : r.wall ? "wall" : "item";
  for (const c of root.children) walk(c, !!r.noStuds, rootRole, c, r.surfaceHint);
  return { studs };
}

// ------------------------------------------------------------------------------------------------ one instanced mesh of studs

/** A toy stud (unit size: radius 0.5, height 1, base at 0): straight sides and a rounded top rim that catches the light. */
export const STUD_GEOMETRY = (() => {
  const pts: THREE.Vector2[] = [new THREE.Vector2(0.5, 0)];
  for (let i = 0; i <= 4; i++) { const t = (i / 4) * (Math.PI / 2); pts.push(new THREE.Vector2(0.38 + Math.cos(t) * 0.12, 0.72 + Math.sin(t) * 0.28)); }
  pts.push(new THREE.Vector2(0, 1));
  const g = new THREE.LatheGeometry(pts, 16);
  g.computeVertexNormals();
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
    const mat = new THREE.MeshStandardMaterial({ color: "#ffffff", roughness: 0.2, metalness: 0, envMapIntensity: 0.55 });
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
    const mat = this.mesh.material as THREE.MeshStandardMaterial;
    if (mat.envMap !== roomEnv) { mat.envMap = roomEnv; mat.needsUpdate = true; } // glossy studs reflect like the bricks
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
