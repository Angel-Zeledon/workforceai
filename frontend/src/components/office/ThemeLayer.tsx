"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { useFrame, useThree } from "@react-three/fiber";
import * as THREE from "three";
import { useOfficeStyle, useOfficeLayout } from "@/lib/officeLayout";
import { usePreferences } from "@/lib/preferences";
import { Bloom, EffectComposer, N8AO, SMAA, TiltShift2, ToneMapping } from "@react-three/postprocessing";
import { Effect, EffectAttribute, ToneMappingMode } from "postprocessing";
import type { OfficeStyle } from "@/lib/officeLayoutModel";
import { APPEAR_SECONDS, appearPose, lookOf, pbrKindsOf, type Outline, type PbrKind } from "./officeTheme";
import { RoomEnvironment } from "three/examples/jsm/environments/RoomEnvironment.js";
import { RGBELoader } from "three/examples/jsm/loaders/RGBELoader.js";
import { StudBatch, setPbrSet, setRoomEnvironment, themeTree, type PbrSet } from "./themeRuntime";
import { QUALITY_TIERS } from "./officeQuality";
import { useQualityTier } from "./FrameDriver";

/** Console handles for screenshots and FPS checks: development builds, or a build made with NEXT_PUBLIC_OFFICE_DEBUG=true. */
export const OFFICE_DEBUG = process.env.NODE_ENV !== "production" || process.env.NEXT_PUBLIC_OFFICE_DEBUG === "true";

/** Frames between two sweeps of the scene (new meshes pick up the style within this many frames). */
const SWEEP_EVERY = 8;

/**
 * Applies the office style to everything in the scene (materials, geometry, studs) and keeps the edge-glow clock running.
 * In "classic" it restores whatever was styled and then stays idle.
 */
export function ThemeApplier() {
  const style = useOfficeStyle();
  const scene = useThree((s) => s.scene);
  const getState = useThree((s) => s.get);
  const batch = useMemo(() => new StudBatch(), []);
  const st = useRef({ frame: 0, applied: "classic", idleSweeps: 0 });
  useEffect(() => () => batch.dispose(), [batch]);
  // development only: switch styles and inspect the scene from the console (screenshots, FPS checks)
  useEffect(() => {
    if (!OFFICE_DEBUG) return;
    const w = window as unknown as { __officeStyle?: unknown };
    w.__officeStyle = {
      scene,
      /** close-ups for screenshots: narrows the lens on a point of the floor plan (fov null: back to the default view) */
      view: (fov: number | null, x = 0.5, y = 0.4, z = -0.3) => {
        const st = getState();
        const cam = st.camera as THREE.PerspectiveCamera;
        const c = st.controls as unknown as { target: THREE.Vector3; update: () => void } | null;
        cam.fov = fov ?? 32; cam.updateProjectionMatrix();
        if (c) { c.target.set(x, y, z); c.update(); }
      },
      set: (s: string) => useOfficeLayout.setState((st) => ({ layout: { ...st.layout, style: s === "classic" ? undefined : (s as never) } })),
    };
    return () => { delete w.__officeStyle; };
  }, [scene, getState]);
  useFrame(() => {
    const s = st.current;
    s.frame++;
    const changed = s.applied !== style;
    if (style === "classic" && !changed && s.idleSweeps > 2) return; // nothing left to restore
    if (changed || s.frame % SWEEP_EVERY === 0) {
      const r = themeTree(scene, style, true);
      batch.set(r.studs);
      s.applied = style;
      s.idleSweeps = style === "classic" ? s.idleSweeps + 1 : 0;
    }
    if (lookOf(style).studs) batch.update();
  });
  return <primitive object={batch.mesh} />;
}

// ------------------------------------------------------------------------------------------------ entrances

/**
 * A piece's entrance in the active style (bricks stack in, paper unfolds, neon strikes, ...): played when the piece is
 * placed while editing and, staggered, when the style changes. Nothing in "classic" or with reduced motion.
 */
export function Appear({ seed = 0, leaving = false, children }: { seed?: number; leaving?: boolean; children: React.ReactNode }) {
  const style = useOfficeStyle();
  const ref = useRef<THREE.Group>(null);
  const anim = useRef({ t: leaving ? 0 : -1, delay: 0 });
  const first = useRef(true);
  useEffect(() => {
    if (leaving) return;
    const placedNow = first.current && useOfficeLayout.getState().editing;
    if (!first.current || placedNow) anim.current = { t: 0, delay: placedNow ? 0 : (seed % 12) * 0.05 };
    first.current = false;
  }, [style, seed, leaving]);
  useFrame((_, dt) => {
    const a = anim.current, g = ref.current;
    if (a.t < 0 || !g) return;
    const kind = lookOf(style).appear;
    const dur = APPEAR_SECONDS[kind];
    if (!dur || usePreferences.getState().prefs.reduceMotion) a.t = Infinity;
    else if (a.delay > 0) a.delay -= dt;
    else a.t += dt / dur;
    // leaving plays the same entrance backwards (bricks come apart, paper folds down, neon goes out, ...)
    const p = leaving ? appearPose(kind, Math.min(0.999, Math.max(0, 1 - a.t))) : appearPose(kind, a.t);
    g.position.y = p.y; g.scale.set(p.sx, p.sy, p.sz); g.rotation.set(p.rx, p.ry, 0); g.visible = leaving && a.t >= 1 ? false : p.visible;
    if (a.t >= 1) a.t = -1;
  });
  return <group ref={ref}>{children}</group>;
}

/**
 * Pieces removed from the layout stay a moment to play their exit in the active style (none in classic or with reduced
 * motion). Returns the removed pieces still on their way out.
 */
export function useLeaving<T extends { uid: string }>(items: T[]): T[] {
  const style = useOfficeStyle();
  const prev = useRef<T[]>(items);
  const [leaving, setLeaving] = useState<T[]>([]);
  useEffect(() => {
    const now = new Set(items.map((i) => i.uid));
    const gone = prev.current.filter((i) => !now.has(i.uid));
    prev.current = items;
    const dur = APPEAR_SECONDS[lookOf(style).appear];
    if (!gone.length || !dur || usePreferences.getState().prefs.reduceMotion) return;
    const out = (l: T[]) => l.filter((x) => !gone.some((g) => g.uid === x.uid));
    setLeaving((l) => [...out(l), ...gone]);
    const tm = setTimeout(() => setLeaving(out), dur * 1000 + 80);
    return () => clearTimeout(tm);
  }, [items, style]);
  return leaving;
}

/** Small stable number from a string (stagger of the entrances). */
export function seedOf(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
  return Math.abs(h);
}

// ------------------------------------------------------------------------------------------------ ambient effects

/**
 * What a style adds to the scene besides restyling it: drifting paper (papercraft), the
 * room reflections (realistic, toy plastic), the real texture sets (realistic) and the post-processing (ambient
 * occlusion, ink, bloom, antialiasing, tilt-shift). Nothing in "classic": no composer, no extra pass, no download.
 */
export function StyleAmbient() {
  const style = useOfficeStyle();
  const look = lookOf(style);
  const kinds = pbrKindsOf(style);
  return (
    <>
      {look.ambient === "paper" && <PaperDrift />}
      {look.env !== "none" && <StyleEnvironment />}
      {kinds.length > 0 && <StyleTextures kinds={kinds} />}
      {look.post && <StylePost style={style} />}
    </>
  );
}

/** The CC0 HDRI of a real room (Poly Haven, see public/office/CREDITS.md), desaturated so it never tints the office. */
const HDRI_URL = "/office/env/room_512.hdr";

/**
 * Reflections of a real room on every styled material (each with its own strength, see setRoomEnvironment); the light
 * stays the same. Loaded only while a style that reflects is active; the generated studio room is the fallback.
 */
function StyleEnvironment() {
  const { gl, scene } = useThree();
  useEffect(() => {
    let alive = true, env: THREE.Texture | null = null;
    const pm = new THREE.PMREMGenerator(gl);
    const fallback = () => {
      if (!alive) return;
      const room = new RoomEnvironment();
      env = pm.fromScene(room, 0.04).texture;
      room.dispose();
      setRoomEnvironment(env, scene);
    };
    new RGBELoader().load(HDRI_URL, (hdr) => {
      if (!alive) { hdr.dispose(); return; }
      hdr.mapping = THREE.EquirectangularReflectionMapping;
      env = pm.fromEquirectangular(hdr).texture;
      hdr.dispose();
      setRoomEnvironment(env, scene);
    }, undefined, fallback);
    return () => { alive = false; setRoomEnvironment(null, scene); env?.dispose(); pm.dispose(); };
  }, [gl, scene]);
  return null;
}

/**
 * The photographed texture sets of the realistic style (public/office/pbr, ~0.6 MB in all): fetched when the style is
 * turned on, released when it is left. Until they arrive the surfaces are simply smooth (never a placeholder pattern).
 */
function StyleTextures({ kinds }: { kinds: PbrKind[] }) {
  const { gl, scene } = useThree();
  const key = kinds.join(",");
  useEffect(() => {
    let alive = true;
    const held: { kind: PbrKind; set: PbrSet }[] = [];
    const loader = new THREE.TextureLoader();
    const aniso = Math.min(8, gl.capabilities.getMaxAnisotropy());
    for (const kind of key.split(",") as PbrKind[]) {
      Promise.all([loader.loadAsync(`/office/pbr/${kind}.jpg`), loader.loadAsync(`/office/pbr/${kind}_r.jpg`)]).then(([map, rgh]) => {
        if (!alive) { map.dispose(); rgh.dispose(); return; }
        for (const t of [map, rgh]) { t.wrapS = t.wrapT = THREE.RepeatWrapping; t.colorSpace = THREE.NoColorSpace; t.anisotropy = aniso; t.needsUpdate = true; }
        const set = { map, rgh };
        held.push({ kind, set });
        setPbrSet(kind, set, scene);
      }).catch(() => { /* offline or blocked: the surfaces stay smooth */ });
    }
    return () => {
      alive = false;
      for (const { kind, set } of held) { setPbrSet(kind, null, scene); set.map.dispose(); set.rgh.dispose(); }
    };
  }, [key, gl, scene]);
  return null;
}

// ------------------------------------------------------------------------------------------------ post-processing

/**
 * The ink of cartoon and papercraft, as one effect of the composer (no second render of the scene): a line where the
 * depth jumps (silhouettes), where it bends (creases) and where the shade changes sharply (edges between surfaces at
 * the same depth); the line keeps the same thickness on screen. Cartoon lines "boil": a few times per second the line
 * is redrawn with a slight, smooth wobble, like animation drawn by hand. Paper adds the soft shadow of a cut-out
 * lifted off the page.
 */
const INK_FRAG = /* glsl */ `
uniform vec3 uInk; uniform float uAlpha; uniform float uShadow; uniform float uPx; uniform float uBoil; uniform float uSeed;
float inkLum(vec2 uv) { vec3 c = texture2D(inputBuffer, uv).rgb; return dot(c / (1.0 + c), vec3(0.299, 0.587, 0.114)); }
float inkLin(vec2 uv) { return -getViewZ(readDepth(uv)); }
void mainImage(const in vec4 inputColor, const in vec2 uv, const in float depth, out vec4 outputColor) {
  vec2 q = uv;
  if (uBoil > 0.0) {
    vec2 f = uv * resolution;
    q += vec2(sin(f.y / 23.0 + uSeed * 7.13) + sin(f.y / 9.0 + uSeed * 3.7) * 0.5, cos(f.x / 19.0 + uSeed * 5.31) + cos(f.x / 8.0 + uSeed * 2.9) * 0.5) * texelSize * uBoil;
  }
  vec2 px = texelSize * uPx;
  float c = inkLin(q);
  float l = inkLin(q - vec2(px.x, 0.0)), r = inkLin(q + vec2(px.x, 0.0)), u = inkLin(q + vec2(0.0, px.y)), d = inkLin(q - vec2(0.0, px.y));
  float jump = max(max(abs(l - c), abs(r - c)), max(abs(u - c), abs(d - c))) / c;
  float bend = abs(l + r + u + d - 4.0 * c) / c;
  float lc = inkLum(q);
  float shade = max(max(abs(inkLum(q - vec2(px.x, 0.0)) - lc), abs(inkLum(q + vec2(px.x, 0.0)) - lc)), max(abs(inkLum(q + vec2(0.0, px.y)) - lc), abs(inkLum(q - vec2(0.0, px.y)) - lc)));
  float ink = max(max(smoothstep(0.008, 0.022, jump), smoothstep(0.003, 0.009, bend) * 0.8), smoothstep(0.07, 0.14, shade) * 0.7);
  ink *= 1.0 - smoothstep(60.0, 90.0, c); // the far ground fades out of the drawing
  vec3 col = mix(inputColor.rgb, uInk, ink * uAlpha);
  if (uShadow > 0.0) {
    // paper: every cut-out casts a soft shadow a few pixels down and to the right, as if lifted off the page
    float sh = 0.0;
    for (int i = 1; i <= 3; i++) {
      float o = float(i) * 2.2;
      sh += step(0.18, c - inkLin(uv + vec2(-px.x, px.y) * o)) / 3.0;
    }
    col *= 1.0 - sh * 0.32 * uShadow;
  }
  outputColor = vec4(col, inputColor.a);
}`;

class InkEffect extends Effect {
  constructor() {
    super("OfficeInkEffect", INK_FRAG, {
      attributes: EffectAttribute.DEPTH | EffectAttribute.CONVOLUTION,
      uniforms: new Map<string, THREE.Uniform>([
        ["uInk", new THREE.Uniform(new THREE.Color())], ["uAlpha", new THREE.Uniform(1)], ["uShadow", new THREE.Uniform(0)],
        ["uPx", new THREE.Uniform(1)], ["uBoil", new THREE.Uniform(0)], ["uSeed", new THREE.Uniform(0)],
      ]),
    });
  }
}

function Ink({ outline }: { outline: Outline }) {
  const effect = useMemo(() => new InkEffect(), []);
  useEffect(() => () => effect.dispose(), [effect]);
  useEffect(() => {
    const u = effect.uniforms;
    // the buffer holds linear light: the ink color goes in linear too, so it lands as picked once tone mapped
    (u.get("uInk")!.value as THREE.Color).set(outline.color);
    u.get("uAlpha")!.value = outline.alpha;
    u.get("uShadow")!.value = outline.shadow ?? 0;
    u.get("uPx")!.value = outline.px; // device pixels, like before
    u.get("uBoil")!.value = outline.boil ?? 0;
  }, [effect, outline]);
  useFrame(({ clock }) => {
    if (!outline.boil) return;
    const reduce = usePreferences.getState().prefs.reduceMotion || window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    effect.uniforms.get("uSeed")!.value = reduce ? 0 : Math.floor(clock.elapsedTime * (outline.boilFps ?? 8));
  });
  return <primitive object={effect} dispose={null} />;
}

/**
 * The style's post-processing: one composer, mounted only in the styles that have one. The scene is drawn once into a
 * half-float buffer, then: ambient occlusion (N8AO), ink, bloom of what glows, ACES tone mapping (the composer turns
 * the renderer's own off, so this keeps every color exactly as in classic), SMAA and tilt-shift. The quality tier
 * (FrameDriver) drops the costly ones on slow computers.
 */
/**
 * N8AO turns "transparency aware" on by itself when the scene has any transparent material (the office always does:
 * glass, labels, rings), and then draws the whole scene twice more every frame, ~15 % of the frame on a laptop. The AO
 * only needs the opaque depth: switched off.
 */
function opaqueAo(pass: { autoDetectTransparency?: boolean; configuration: { transparencyAware: boolean } } | null) {
  if (!pass) return;
  pass.autoDetectTransparency = false;
  pass.configuration.transparencyAware = false;
}

function StylePost({ style }: { style: OfficeStyle }) {
  const look = lookOf(style);
  const post = look.post!;
  const q = QUALITY_TIERS[useQualityTier()];
  const ao = post.ao > 0 && q.ao !== "off";
  const bloom = post.bloom > 0 && q.bloom;
  const tilt = post.tilt > 0 && q.tilt;
  return (
    <EffectComposer multisampling={0} depthBuffer>
      {ao ? <N8AO ref={opaqueAo} halfRes quality={q.ao === "full" ? "medium" : "performance"} aoRadius={post.aoRadius} intensity={post.ao} distanceFalloff={0.7} depthAwareUpsampling /> : <></>}
      {look.outline ? <Ink outline={look.outline} /> : <></>}
      {bloom ? <Bloom mipmapBlur intensity={post.bloom} luminanceThreshold={1.05} luminanceSmoothing={0.25} /> : <></>}
      <ToneMapping mode={ToneMappingMode.ACES_FILMIC} />
      {post.smaa ? <SMAA /> : <></>}
      {tilt ? <TiltShift2 blur={0.16 * post.tilt} taper={0.55} start={[0, 0.5]} end={[1, 0.5]} samples={6} /> : <></>}
    </EffectComposer>
  );
}

/**
 * The switch between two styles: a short veil in the new style's colors covers the frame while the scene restyles and
 * rebuilds (shaders compile, pieces play their entrance behind it). DOM overlay, placed over the canvas.
 */
export function StyleVeil() {
  const style = useOfficeStyle();
  const [veil, setVeil] = useState<{ key: number; bg: string } | null>(null);
  const first = useRef(true);
  useEffect(() => {
    if (first.current) { first.current = false; return; }
    const reduce = usePreferences.getState().prefs.reduceMotion || window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    if (reduce) return;
    const [x, y, z] = lookOf(style).swatch;
    setVeil({ key: Date.now(), bg: `radial-gradient(circle at 50% 55%, ${x} 0%, ${y} 60%, ${z} 100%)` });
    const t = setTimeout(() => setVeil(null), 700);
    return () => clearTimeout(t);
  }, [style]);
  if (!veil) return null;
  return (
    <div key={veil.key} aria-hidden className="pointer-events-none absolute inset-0 z-[5]" style={{ background: veil.bg, animation: "office-veil .7s ease-in-out forwards" }}>
      <style>{"@keyframes office-veil{0%{opacity:0}30%{opacity:.8}100%{opacity:0}}"}</style>
    </div>
  );
}

const PAPER_N = 24;
/** Plain sheets: white and off-white, so the office keeps its own colors. */
const PAPER_COLORS = ["#ffffff", "#f6f3ec", "#eef0f3"];

/** Scraps of paper drifting over the office, turning over slowly as they fall and loop back up. */
function PaperDrift() {
  const ref = useRef<THREE.InstancedMesh>(null);
  const seeds = useMemo(() => Array.from({ length: PAPER_N }, (_, i) => {
    const r = (k: number) => { const x = Math.sin(i * 12.9898 + k * 78.233) * 43758.5453; return x - Math.floor(x); };
    return { x: (r(1) - 0.5) * 26, z: (r(2) - 0.5) * 18, y: 0.6 + r(3) * 3, sp: 0.12 + r(4) * 0.18, rot: r(5) * 6, spin: 0.4 + r(6) * 0.8, sway: r(7) * 6 };
  }), []);
  const geo = useMemo(() => {
    const g = new THREE.BufferGeometry(); // a sheet folded once along its diagonal
    g.setAttribute("position", new THREE.Float32BufferAttribute([-0.09, 0, -0.07, 0.09, 0, -0.07, -0.09, 0.03, 0.07, 0.09, 0, -0.07, 0.09, 0, 0.07, -0.09, 0.03, 0.07], 3));
    g.computeVertexNormals();
    return g;
  }, []);
  useEffect(() => () => geo.dispose(), [geo]);
  const m = useMemo(() => new THREE.Matrix4(), []), q = useMemo(() => new THREE.Quaternion(), []), e = useMemo(() => new THREE.Euler(), []);
  const p = useMemo(() => new THREE.Vector3(), []), s1 = useMemo(() => new THREE.Vector3(1, 1, 1), []);
  useEffect(() => {
    const im = ref.current;
    if (!im) return;
    const c = new THREE.Color();
    seeds.forEach((_, i) => im.setColorAt(i, c.set(PAPER_COLORS[i % PAPER_COLORS.length])));
    if (im.instanceColor) im.instanceColor.needsUpdate = true;
  }, [seeds]);
  useFrame(({ clock }) => {
    const im = ref.current;
    if (!im) return;
    const reduce = usePreferences.getState().prefs.reduceMotion;
    const t = reduce ? 0 : clock.elapsedTime;
    seeds.forEach((s, i) => {
      const y = 0.4 + ((s.y + 3.6 - ((t * s.sp) % 3.6)) % 3.6);
      p.set(s.x + Math.sin(t * 0.5 + s.sway) * 0.6, y, s.z + Math.cos(t * 0.4 + s.sway) * 0.4);
      e.set(Math.sin(t * s.spin + s.rot) * 1.2, t * 0.3 + s.rot, Math.cos(t * s.spin * 0.7 + s.rot) * 0.8);
      im.setMatrixAt(i, m.compose(p, q.setFromEuler(e), s1));
    });
    im.instanceMatrix.needsUpdate = true;
  });
  return (
    <instancedMesh ref={ref} args={[geo, undefined, PAPER_N]} castShadow userData={{ noTheme: true }} frustumCulled={false}>
      <meshStandardMaterial side={THREE.DoubleSide} roughness={1} flatShading />
    </instancedMesh>
  );
}
