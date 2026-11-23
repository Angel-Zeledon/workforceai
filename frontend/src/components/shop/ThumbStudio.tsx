"use client";
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { Canvas, useFrame, useThree } from "@react-three/fiber";
import * as THREE from "three";
import type { ShopItem } from "@/lib/rewards";
import { BodyWear, Buddy, HeadWear, OutfitWear, useOutfit } from "../office/Worn";
import { ItemMesh } from "../office/OfficeItems";
import { getOfficeStyle, useOfficeLayout, useOfficeStyle } from "@/lib/officeLayout";
import type { OfficeStyle } from "@/lib/officeLayoutModel";
import { addStaticStuds, themeTree } from "../office/themeRuntime";
import { Model, PieceView } from "../office/studio/StudioRoom";
import { acquireAssets, releaseAssets, type Assets } from "../office/studio/studioAssets";
import { disposeFlatThumbs, fixtureThumb, flatDraws, flatThumb, roomThumb } from "../office/editor/flatThumb";
import { RoomSubject, fxKey, type Fx } from "../office/editor/RoomSubject";
import { pixelate } from "../office/editor/thumbPost";

/**
 * Item pictures (shop and office editor) drawn the way the office draws them in its current style, instead of flat icons.
 * One queue, one item at a time, each picture cached for the session PER STYLE; the card keeps its flat SVG (or the
 * previous style's picture) until the new one is ready. Only items that are on screen are requested.
 *  - Actual / Maqueta: the office's own 3D models in a hidden canvas (themeRuntime restyles the subject for Maqueta).
 *  - Estudio: the Kenney models of the Estudio office under studio light with a soft shadow.
 *  - Plano: the office's vector drawing (office/flat/draw.ts) in an offscreen PixiJS surface.
 *  - Pixel: the 3D render reduced to a 64 px sprite with a small palette and a contour (shown without smoothing).
 * An empty string in the cache means "this style has no picture of it": the caller keeps its flat drawing.
 */
const SIZE = 256;
const MAX_CACHE = 500;

const cache = new Map<string, string>();
interface Job { item?: ShopItem; fx?: Fx; style: OfficeStyle; key: string }
const queue: Job[] = [];
const queued = new Set<string>();
const subs = new Set<() => void>();
const notify = () => subs.forEach((f) => f());
const subscribe = (f: () => void) => { subs.add(f); return () => { subs.delete(f); }; };

const keyOf = (style: OfficeStyle, id: string) => (style === "classic" ? id : `${style}:${id}`);

export function requestFxThumb(fx: Fx, style: OfficeStyle = getOfficeStyle()) {
  const key = keyOf(style, fxKey(fx));
  if (cache.has(key) || queued.has(key)) return;
  queued.add(key);
  queue.push({ fx, style, key });
  notify();
}
/** Picture of a room or of a built-in object of the office, in the current style (same states as useThumb). */
export function useFxThumb(fx: Fx): string | null {
  const style = useOfficeStyle();
  const key = keyOf(style, fxKey(fx));
  const url = useSyncExternalStore(subscribe, () => cache.get(key) ?? null, () => null);
  useEffect(() => { if (url === null) requestFxThumb(fx, style); }, [url, fx.kind, fx.id, style]); // eslint-disable-line react-hooks/exhaustive-deps
  return url;
}

export function requestThumb(item: ShopItem, style: OfficeStyle = getOfficeStyle()) {
  const key = keyOf(style, item.id);
  if (cache.has(key) || queued.has(key)) return;
  queued.add(key);
  queue.push({ item, style, key });
  notify();
}
/** The rendered picture of an item (a data URL), null while it is still in the queue, "" when this style has none of it. */
export function useThumb(item: ShopItem): string | null {
  const style = useOfficeStyle();
  const key = keyOf(style, item.id);
  const url = useSyncExternalStore(subscribe, () => cache.get(key) ?? null, () => null);
  useEffect(() => { if (url === null) requestThumb(item, style); }, [url, item, style]);
  return url;
}

// ------------------------------------------------------------------ the subject on the stage

const SKIN = "#e8b894", HAIR = "#4a3020";

function HeadMesh({ worn }: { worn: Record<string, string> }) {
  return (
    <group scale={1.75}>
      <mesh castShadow position={[0, 0.15, 0]} userData={{ brick: "cyl" }}><sphereGeometry args={[0.165, 28, 20]} /><meshStandardMaterial color={SKIN} roughness={0.7} /></mesh>
      <mesh position={[0, 0.17, 0]} rotation={[-0.35, 0, 0]}><sphereGeometry args={[0.178, 24, 16, 0, Math.PI * 2, 0, Math.PI * 0.52]} /><meshStandardMaterial color={HAIR} roughness={0.9} /></mesh>
      {[0.058, -0.058].map((x) => <mesh key={x} position={[x, 0.17, 0.145]} scale={[1, 1.35, 0.5]}><sphereGeometry args={[0.022, 10, 8]} /><meshBasicMaterial color="#4a3626" /></mesh>)}
      {[0.065, -0.051].map((x) => <mesh key={x} position={[x, 0.182, 0.159]}><sphereGeometry args={[0.0075, 8, 6]} /><meshBasicMaterial color="#ffffff" /></mesh>)}
      {[0.1, -0.1].map((x) => <mesh key={x} position={[x, 0.1, 0.12]} scale={[1.2, 0.8, 0.4]}><sphereGeometry args={[0.025, 8, 6]} /><meshBasicMaterial color="#ff9fa8" transparent opacity={0.6} /></mesh>)}
      <mesh position={[0, 0.095, 0.155]} scale={[1.6, 0.45, 0.5]}><sphereGeometry args={[0.02, 10, 8]} /><meshBasicMaterial color="#8a3b3b" /></mesh>
      {[0.163, -0.163].map((x) => <mesh key={x} position={[x, 0.145, -0.005]} scale={[0.45, 1, 0.8]}><sphereGeometry args={[0.04, 10, 8]} /><meshStandardMaterial color={SKIN} roughness={0.8} /></mesh>)}
      <HeadWear worn={worn} />
    </group>
  );
}

/** Torso with arms and legs, wearing the item (an outfit, a scarf, a badge or a shoulder buddy). */
function BodyMesh({ worn, shirt = "#7f93ad", withHead = true }: { worn: Record<string, string>; shirt?: string; withHead?: boolean }) {
  const o = useOutfit(worn, shirt);
  const body = o.worn ? (o.style === "overalls" ? o.shirt : o.color) : shirt;
  const sleeve = o.worn ? (o.style === "vest" || o.style === "overalls" ? o.shirt : o.color) : shirt;
  const std = (c: string, r = 0.8) => <meshStandardMaterial color={c} roughness={r} />;
  return (
    <group>
      <mesh castShadow position={[0, -0.02, 0]}><sphereGeometry args={[0.19, 20, 14]} />{std(o.worn ? o.pants : "#6b7686")}</mesh>
      {[0.1, -0.1].map((x) => <mesh key={x} castShadow position={[x, -0.22, 0]}><capsuleGeometry args={[0.08, 0.12, 4, 10]} />{std(o.worn ? o.pants : "#6b7686")}</mesh>)}
      {[0.1, -0.1].map((x) => <mesh key={`s${x}`} castShadow position={[x, -0.34, 0.04]} scale={[1, 0.7, 1.5]}><sphereGeometry args={[0.09, 12, 10]} />{std("#6b4a36", 0.6)}</mesh>)}
      <group>
        <mesh castShadow position={[0, 0.16, 0]} scale={[1.08, 1, 0.9]}><capsuleGeometry args={[0.2, 0.04, 8, 16]} />{std(body)}</mesh>
        {o.worn && (o.style === "blazer" || o.style === "cardigan" || o.style === "vest" || o.style === "trench") && <mesh position={[0, 0.14, 0.165]} scale={[1, 1.25, 0.3]}><sphereGeometry args={[0.1, 12, 10]} />{std(o.shirt, 0.9)}</mesh>}
        <OutfitWear o={o} fallback={shirt} />
        <mesh position={[0, 0.33, 0]}><cylinderGeometry args={[0.05, 0.06, 0.07, 12]} />{std(SKIN)}</mesh>
        {[0.25, -0.25].map((x) => (
          <group key={x} position={[x, 0.27, 0]} rotation={[0, 0, x > 0 ? -0.18 : 0.18]}>
            <mesh castShadow position={[0, -0.1, 0]}><capsuleGeometry args={[0.07, 0.15, 4, 10]} />{std(sleeve)}</mesh>
            <mesh castShadow position={[0, -0.25, 0.02]}><sphereGeometry args={[0.072, 12, 10]} />{std(SKIN)}</mesh>
          </group>
        ))}
        <BodyWear worn={worn} />
        {worn.buddy && <Buddy worn={worn} />}
        {withHead && <group position={[0, 0.32, 0]}><HeadMesh worn={{}} /></group>}
      </group>
    </group>
  );
}

/** What the camera should frame, per kind of item: the head, the chest, the shoulder, the whole body or the object. */
type Focus = "head" | "chest" | "shoulder" | "torso" | "object";

/** Pieces that have a Kenney model or a studio shape in the Estudio office (windows are part of the wall there). */
const studioDraws = (item: ShopItem) => item.kind === "office" && item.category !== "window";

function Subject({ item, focus, assets }: { item: ShopItem; focus: React.RefObject<THREE.Group | null>; assets: Assets | null }) {
  const slot = item.slot;
  if (assets && studioDraws(item)) {
    return (
      <group ref={focus}>
        <PieceView a={assets} item={item} x={0} z={0} rot={0} s={1} />
        {item.category === "desk" && <>
          <Model a={assets} name="computerScreen" y={0.74} z={-0.12} />
          <Model a={assets} name="computerKeyboard" y={0.74} z={0.2} />
        </>}
      </group>
    );
  }
  if (item.kind === "office") {
    return <group ref={focus}><ItemMesh item={item} /></group>;
  }
  const worn = { [slot]: item.id };
  switch (item.category) {
    case "hat": case "glasses": case "headphones":
      return (
        <group>
          <group ref={focus} position={[0, 0.36, 0]}><HeadMesh worn={worn} /></group>
          <group position={[0, 0.0, 0]}><BodyMesh worn={{}} withHead={false} /></group>
        </group>
      );
    case "scarf": case "badge":
      return <group><group ref={focus}><BodyMesh worn={worn} /></group></group>;
    case "buddy":
      return <group ref={focus}><BodyMesh worn={worn} /></group>;
    default: // outfit
      return <group ref={focus}><BodyMesh worn={worn} /></group>;
  }
}

function focusOf(item: ShopItem): Focus {
  if (item.kind === "office") return "object";
  switch (item.category) {
    case "hat": case "glasses": case "headphones": return "head";
    case "scarf": case "badge": return "chest";
    case "buddy": return "shoulder";
    default: return "torso"; // outfits: from the knees up, so the cut reads
  }
}

/** Frames the subject (a focus-specific part of it), takes the picture a few frames later and reports it. */
function Shot({ item, fx, style, onDone, ground, shadow, assets }: { item?: ShopItem; fx?: Fx; style: OfficeStyle; onDone: (url: string) => void; ground: React.RefObject<THREE.Mesh | null>; shadow: React.RefObject<THREE.Mesh | null>; assets: Assets | null }) {
  const { gl, camera, scene } = useThree();
  const focusRef = useRef<THREE.Group>(null);
  const stage = useRef<THREE.Group>(null);
  const studs = useRef<THREE.InstancedMesh | null>(null);
  useEffect(() => () => { const m = studs.current; if (m) { m.dispose(); (m.material as THREE.Material).dispose(); } }, []);
  const frames = useRef(0);
  const kind = item ? focusOf(item) : "object";
  useFrame(() => {
    frames.current++;
    const g = focusRef.current;
    if (!g) return;
    if (frames.current === 1 && stage.current && style !== "classic" && !assets) {
      // the office style: same restyle as the 3D office (characters' clothes get no studs there either)
      stage.current.updateMatrixWorld(true);
      const r = themeTree(stage.current, style, true);
      studs.current = addStaticStuds(stage.current, r.studs);
    }
    if (frames.current === 2) {
      const box = new THREE.Box3().setFromObject(g);
      const size = box.getSize(new THREE.Vector3());
      const center = box.getCenter(new THREE.Vector3());
      if (kind === "head") center.y -= size.y * 0.08;
      if (kind === "torso") { center.set(0, 0.3, 0); size.set(0.82, 0.95, 0.5); }
      if (kind === "chest") { center.y = 0.3; size.set(0.55, 0.55, 0.45); center.x = 0; center.z = 0; }
      if (kind === "shoulder") { center.set(-0.12, 0.38, 0.05); size.set(0.55, 0.5, 0.45); }
      // the soft shadow sits under the object and is only as wide as it (Estudio: a real cast shadow instead of the blob)
      const lit = !!assets && kind === "object";
      if (ground.current) { ground.current.visible = kind === "object" && !lit; ground.current.position.y = box.min.y + 0.003; ground.current.scale.setScalar(Math.max(size.x, size.z) * 1.35); }
      if (shadow.current) { shadow.current.visible = lit; shadow.current.position.y = box.min.y + 0.002; }
      const cam = camera as THREE.PerspectiveCamera;
      const r = Math.max(size.x, size.y * 0.95, size.z) * 0.5;
      const dist = (r / Math.tan(THREE.MathUtils.degToRad(cam.fov / 2))) * (kind === "object" ? 1.45 : 1.3);
      const yaw = kind === "chest" ? 0.0 : kind === "torso" ? -0.4 : -0.55, pitch = kind === "object" ? 0.42 : 0.14;
      cam.position.set(center.x + Math.sin(yaw) * dist * Math.cos(pitch), center.y + Math.sin(pitch) * dist, center.z + Math.cos(yaw) * dist * Math.cos(pitch));
      cam.lookAt(center);
      cam.updateProjectionMatrix();
    }
    if (frames.current === 6) {
      gl.render(scene, camera);
      onDone(style === "pixel" ? pixelate(gl.domElement) : gl.domElement.toDataURL("image/png"));
    }
  });
  if (fx) return <group ref={stage}><RoomSubject fx={fx} assets={assets} focus={focusRef} /></group>;
  return <group ref={stage} userData={{ noStuds: item!.kind !== "office", figure: item!.kind !== "office" }}><Subject item={item!} focus={focusRef} assets={assets} /></group>;
}

/** The ground shadow of floor objects. */
const Ground = ({ gref }: { gref: React.RefObject<THREE.Mesh | null> }) => {
  const tex = useRef<THREE.CanvasTexture | null>(null);
  if (!tex.current && typeof document !== "undefined") {
    const c = document.createElement("canvas"); c.width = c.height = 64;
    const g = c.getContext("2d")!;
    const gr = g.createRadialGradient(32, 32, 2, 32, 32, 32);
    gr.addColorStop(0, "rgba(30,40,60,0.45)"); gr.addColorStop(1, "rgba(30,40,60,0)");
    g.fillStyle = gr; g.fillRect(0, 0, 64, 64);
    tex.current = new THREE.CanvasTexture(c);
  }
  return (
    <mesh ref={gref} rotation={[-Math.PI / 2, 0, 0]} position={[0, 0.003, 0]} visible={false}>
      <circleGeometry args={[0.5, 32]} />
      <meshBasicMaterial map={tex.current} transparent depthWrite={false} toneMapped={false} />
    </mesh>
  );
};

/** The light of each style: neutral for Actual and Pixel, warm and matte for Maqueta, soft key with a real shadow for Estudio. */
function Rig({ style }: { style: OfficeStyle | null }) {
  if (style === "studio") return (
    <>
      <hemisphereLight args={["#f4f7fb", "#9aa3b0", 1.05]} />
      <directionalLight position={[3, 5.5, 4]} intensity={2.4} castShadow shadow-mapSize={[1024, 1024]} shadow-radius={5} shadow-bias={-0.0004} shadow-normalBias={0.02}
        shadow-camera-left={-3} shadow-camera-right={3} shadow-camera-top={3} shadow-camera-bottom={-3} shadow-camera-near={0.5} shadow-camera-far={16} />
      <directionalLight position={[-4, 2.5, 2]} intensity={0.55} color="#dbe6f5" />
    </>
  );
  if (style === "clay") return (
    <>
      <hemisphereLight args={["#fff0dc", "#c9a98a", 1.0]} />
      <directionalLight position={[3, 5, 4]} intensity={2.0} color="#ffe3c0" />
      <directionalLight position={[-4, 2.5, 2]} intensity={0.5} color="#ffd9b8" />
      <directionalLight position={[0, 3, -4]} intensity={0.8} color="#fff1dc" />
    </>
  );
  return (
    <>
      <hemisphereLight args={["#ffffff", "#aab4c4", 0.9]} />
      <directionalLight position={[3, 5, 4]} intensity={2.2} />
      <directionalLight position={[-4, 2.5, 2]} intensity={0.7} color="#cfe0ff" />
      <directionalLight position={[0, 3, -4]} intensity={0.9} color="#fff1dc" />
    </>
  );
}

/** Mount once where pictures are shown (the shop, the editor): renders the queue, one item at a time, offscreen. */
export function ThumbStudio() {
  const [cur, setCur] = useState<Job | null>(null);
  // the office style decides how the pictures look: the shop needs the layout too
  useEffect(() => { if (!useOfficeLayout.getState().loaded) void useOfficeLayout.getState().load(); }, []);
  const busy = useRef(false);
  const groundRef = useRef<THREE.Mesh>(null);
  const shadowRef = useRef<THREE.Mesh>(null);
  const assets = useRef<Assets | null>(null);
  const held = useRef(false);
  const curRef = useRef<Job | null>(null);
  const alive = useRef(true);
  const finish = useCallback((job: Job, url: string) => {
    cache.set(job.key, url);
    queued.delete(job.key);
    if (cache.size > MAX_CACHE) { const first = cache.keys().next().value; if (first) cache.delete(first); }
    notify();
  }, []);
  const next = useCallback(async (): Promise<void> => {
    const n = queue.shift() ?? null;
    if (!n || !alive.current) { busy.current = false; setCur(null); return; }
    busy.current = true;
    try {
      if (n.style === "flat") {
        const url = n.fx ? (n.fx.kind === "room" ? await roomThumb(n.fx.id) : await fixtureThumb(n.fx.id)) : n.item && flatDraws(n.item) ? await flatThumb(n.item) : null;
        finish(n, url ?? "");
        return next();
      }
      if (n.style === "studio" && (n.fx || (n.item && studioDraws(n.item))) && !assets.current) {
        held.current = true;
        assets.current = await acquireAssets();
      }
      curRef.current = n;
      setCur(n);
    } catch {
      finish(n, ""); // this style could not draw it: the caller keeps its flat drawing
      return next();
    }
  }, [finish]);
  useEffect(() => {
    alive.current = true;
    const wake = () => { if (!busy.current && queue.length) void next(); };
    subs.add(wake);
    wake();
    return () => {
      alive.current = false; subs.delete(wake);
      if (held.current) { held.current = false; assets.current = null; releaseAssets(); }
      disposeFlatThumbs();
    };
  }, [next]);
  const done = useCallback((url: string) => {
    const c = curRef.current;
    if (c) finish(c, url);
    void next();
  }, [finish, next]);
  return (
    <div aria-hidden style={{ position: "fixed", left: -10000, top: 0, width: SIZE, height: SIZE, pointerEvents: "none" }}>
      <Canvas dpr={1} shadows="soft" gl={{ preserveDrawingBuffer: true, alpha: true, antialias: true }} camera={{ fov: 28, near: 0.1, far: 60, position: [0, 1, 4] }} style={{ width: SIZE, height: SIZE }}>
        <Rig style={cur?.style ?? null} />
        <Ground gref={groundRef} />
        <mesh ref={shadowRef} rotation={[-Math.PI / 2, 0, 0]} visible={false} receiveShadow><planeGeometry args={[8, 8]} /><shadowMaterial opacity={0.3} /></mesh>
        {cur && <Shot key={cur.key} item={cur.item} fx={cur.fx} style={cur.style} onDone={done} ground={groundRef} shadow={shadowRef} assets={cur.style === "studio" && (cur.fx || (cur.item && studioDraws(cur.item))) ? assets.current : null} />}
      </Canvas>
    </div>
  );
}
