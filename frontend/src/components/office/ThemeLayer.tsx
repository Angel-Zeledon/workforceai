"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { useFrame, useThree } from "@react-three/fiber";
import * as THREE from "three";
import { useOfficeStyle, useOfficeLayout } from "@/lib/officeLayout";
import { usePreferences } from "@/lib/preferences";
import { APPEAR_SECONDS, appearPose, lookOf, type Outline } from "./officeTheme";
import { RoomEnvironment } from "three/examples/jsm/environments/RoomEnvironment.js";
import { StudBatch, setRoomEnvironment, themeTree } from "./themeRuntime";

/** Frames between two sweeps of the scene (new meshes pick up the style within this many frames). */
const SWEEP_EVERY = 8;

/**
 * Applies the office style to everything in the scene (materials, geometry, studs) and keeps the edge-glow clock running.
 * In "classic" it restores whatever was styled and then stays idle.
 */
export function ThemeApplier() {
  const style = useOfficeStyle();
  const scene = useThree((s) => s.scene);
  const batch = useMemo(() => new StudBatch(), []);
  const st = useRef({ frame: 0, applied: "classic", idleSweeps: 0 });
  useEffect(() => () => batch.dispose(), [batch]);
  // development only: switch styles and inspect the scene from the console (screenshots, FPS checks)
  useEffect(() => {
    if (process.env.NODE_ENV === "production") return;
    const w = window as unknown as { __officeStyle?: unknown };
    w.__officeStyle = {
      scene,
      set: (s: string) => useOfficeLayout.setState((st) => ({ layout: { ...st.layout, style: s === "classic" ? undefined : (s as never) } })),
    };
    return () => { delete w.__officeStyle; };
  }, [scene]);
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

/** Just above the floor slab and the desk mats (top at 0.021), so the overlays never fight with them. */
const FLOOR_W = 28.4, FLOOR_D = 20.4, FLOOR_Y = 0.026;

/**
 * What a style adds to the scene besides restyling it: the studded baseplate (bricks), drifting paper (papercraft), the
 * neutral room reflections (realistic) and the ink outline pass (cartoon). Nothing in "classic".
 */
export function StyleAmbient() {
  const look = lookOf(useOfficeStyle());
  return (
    <>
      {look.ambient === "baseplate" && <Baseplate />}
      {look.ambient === "paper" && <PaperDrift />}
      {look.env && <RoomReflections />}
      {look.outline && <InkOutline outline={look.outline} />}
    </>
  );
}

/** Realistic: a neutral studio room (generated, no file) as the reflection of every material; the light stays the same. */
function RoomReflections() {
  const { gl, scene } = useThree();
  useEffect(() => {
    const pm = new THREE.PMREMGenerator(gl);
    const room = new RoomEnvironment();
    const env = pm.fromScene(room, 0.04).texture;
    // per material, not scene.environment (see setRoomEnvironment): each finish keeps its own reflection strength
    setRoomEnvironment(env, scene);
    return () => { setRoomEnvironment(null, scene); env.dispose(); pm.dispose(); room.dispose(); };
  }, [gl, scene]);
  return null;
}

/**
 * Outlines (cartoon ink, papercraft cut edges): the scene is drawn into a buffer and a full-screen pass draws a line where
 * the depth jumps (silhouettes), where it bends (creases) and where the shade changes sharply (edges between surfaces at
 * the same depth). One extra pass, no per-object outline meshes; the line keeps the same thickness on screen.
 */
function InkOutline({ outline }: { outline: Outline }) {
  const { gl, scene, camera, size } = useThree();
  const res = useMemo(() => {
    const rt = new THREE.WebGLRenderTarget(1, 1, { samples: 4, type: THREE.HalfFloatType });
    rt.depthTexture = new THREE.DepthTexture(1, 1);
    rt.depthTexture.type = THREE.UnsignedIntType;
    const mat = new THREE.ShaderMaterial({
      uniforms: {
        tColor: { value: rt.texture }, tDepth: { value: rt.depthTexture }, uPx: { value: new THREE.Vector2() }, uNear: { value: 0.5 }, uFar: { value: 160 },
        uInk: { value: new THREE.Color() }, uAlpha: { value: 1 }, uShadow: { value: 0 },
      },
      vertexShader: "varying vec2 vUv; void main() { vUv = uv; gl_Position = vec4(position.xy, 0.0, 1.0); }",
      fragmentShader: `
        #include <common>
        varying vec2 vUv;
        uniform sampler2D tColor; uniform sampler2D tDepth; uniform vec2 uPx; uniform float uNear; uniform float uFar; uniform vec3 uInk; uniform float uAlpha; uniform float uShadow;
        float lum(vec2 uv) { vec3 c = texture2D(tColor, uv).rgb; return dot(c / (1.0 + c), vec3(0.299, 0.587, 0.114)); }
        float lin(vec2 uv) { float z = texture2D(tDepth, uv).r * 2.0 - 1.0; return (2.0 * uNear * uFar) / (uFar + uNear - z * (uFar - uNear)); }
        void main() {
          vec4 col = texture2D(tColor, vUv);
          float c = lin(vUv);
          float l = lin(vUv - vec2(uPx.x, 0.0)), r = lin(vUv + vec2(uPx.x, 0.0)), u = lin(vUv + vec2(0.0, uPx.y)), d = lin(vUv - vec2(0.0, uPx.y));
          float jump = max(max(abs(l - c), abs(r - c)), max(abs(u - c), abs(d - c))) / c;
          float bend = abs(l + r + u + d - 4.0 * c) / c;
          float lc = lum(vUv);
          float shade = max(max(abs(lum(vUv - vec2(uPx.x, 0.0)) - lc), abs(lum(vUv + vec2(uPx.x, 0.0)) - lc)), max(abs(lum(vUv + vec2(0.0, uPx.y)) - lc), abs(lum(vUv - vec2(0.0, uPx.y)) - lc)));
          float ink = max(max(smoothstep(0.008, 0.022, jump), smoothstep(0.003, 0.009, bend) * 0.8), smoothstep(0.07, 0.14, shade) * 0.7);
          ink *= 1.0 - smoothstep(60.0, 90.0, c); // the far ground fades out of the drawing
          col.rgb = mix(col.rgb, uInk, ink * uAlpha);
          if (uShadow > 0.0) {
            // paper: every cut-out casts a soft shadow a few pixels down and to the right, as if lifted off the page
            float sh = 0.0;
            for (int i = 1; i <= 3; i++) {
              float o = float(i) * 2.2;
              sh += step(0.18, c - lin(vUv + vec2(-uPx.x, uPx.y) * o)) / 3.0;
            }
            col.rgb *= 1.0 - sh * 0.32 * uShadow;
          }
          gl_FragColor = col;
          #include <tonemapping_fragment>
          #include <colorspace_fragment>
        }`,
      depthTest: false, depthWrite: false,
    });
    mat.toneMapped = true;
    const quad = new THREE.Mesh(new THREE.PlaneGeometry(2, 2), mat);
    quad.frustumCulled = false;
    const qScene = new THREE.Scene();
    qScene.add(quad);
    return { rt, mat, quad, qScene, qCam: new THREE.OrthographicCamera(-1, 1, 1, -1, 0, 1) };
  }, []);
  useEffect(() => {
    const dpr = gl.getPixelRatio();
    res.rt.setSize(Math.max(1, Math.floor(size.width * dpr)), Math.max(1, Math.floor(size.height * dpr)));
    res.mat.uniforms.uPx.value.set(outline.px / Math.max(1, size.width * dpr), outline.px / Math.max(1, size.height * dpr));
    // the buffer holds linear light: the ink color goes in linear too, so it lands exactly as picked on screen
    res.mat.uniforms.uInk.value.set(outline.color);
    res.mat.uniforms.uAlpha.value = outline.alpha;
    res.mat.uniforms.uShadow.value = outline.shadow ?? 0;
  }, [gl, size, res, outline]);
  useEffect(() => () => { res.rt.dispose(); res.mat.dispose(); res.quad.geometry.dispose(); }, [res]);
  useFrame(() => {
    const cam = camera as THREE.PerspectiveCamera;
    res.mat.uniforms.uNear.value = cam.near; res.mat.uniforms.uFar.value = cam.far;
    gl.setRenderTarget(res.rt);
    gl.render(scene, camera);
    gl.setRenderTarget(null);
    gl.render(res.qScene, res.qCam);
  }, 1); // priority 1: this pass draws the frame instead of the default render
  return null;
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

/**
 * The floor as a toy baseplate: a repeated stud texture that only adds light and shadow, so the floor keeps its own color
 * (one quad; real studs on the whole floor would be ~20k meshes).
 */
function Baseplate() {
  const tex = useMemo(() => {
    const S = 64, c = document.createElement("canvas");
    c.width = c.height = S;
    const g = c.getContext("2d")!;
    // the shadow ring, the stud body, then the light catching its top edge
    g.fillStyle = "rgba(0,0,0,0.20)"; g.beginPath(); g.ellipse(S / 2 + 3, S / 2 + 4, S * 0.31, S * 0.29, 0, 0, Math.PI * 2); g.fill();
    g.fillStyle = "rgba(255,255,255,0.10)"; g.beginPath(); g.arc(S / 2, S / 2, S * 0.3, 0, Math.PI * 2); g.fill();
    g.strokeStyle = "rgba(255,255,255,0.55)"; g.lineWidth = 3; g.beginPath(); g.arc(S / 2, S / 2, S * 0.27, Math.PI * 0.95, Math.PI * 1.75); g.stroke();
    g.strokeStyle = "rgba(0,0,0,0.18)"; g.lineWidth = 2; g.beginPath(); g.arc(S / 2, S / 2, S * 0.3, Math.PI * 0.05, Math.PI * 0.85); g.stroke();
    const t = new THREE.CanvasTexture(c);
    t.wrapS = t.wrapT = THREE.RepeatWrapping;
    t.repeat.set(FLOOR_W / 0.4, FLOOR_D / 0.4);
    t.anisotropy = 4;
    return t;
  }, []);
  useEffect(() => () => tex.dispose(), [tex]);
  return (
    <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, FLOOR_Y, 0]} receiveShadow userData={{ noTheme: true }}>
      <planeGeometry args={[FLOOR_W, FLOOR_D]} />
      <meshStandardMaterial map={tex} transparent depthWrite={false} roughness={0.4} polygonOffset polygonOffsetFactor={-2} />
    </mesh>
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
