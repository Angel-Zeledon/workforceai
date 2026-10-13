"use client";
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { Canvas, useFrame, useThree } from "@react-three/fiber";
import * as THREE from "three";
import type { ShopItem } from "@/lib/rewards";
import { BodyWear, Buddy, HeadWear, OutfitWear, useOutfit } from "../office/Worn";
import { ItemMesh } from "../office/OfficeItems";

/**
 * Shop thumbnails rendered from the SAME 3D models the office uses (lit, with volume and a soft shadow), instead of flat
 * drawings. One hidden canvas renders one item at a time from a queue; each picture is cached for the session and the
 * card keeps its flat SVG until its picture is ready. Only items that are on screen are requested.
 */
const SIZE = 256;
const MAX_CACHE = 500;

const cache = new Map<string, string>();
const queue: ShopItem[] = [];
const queued = new Set<string>();
const subs = new Set<() => void>();
const notify = () => subs.forEach((f) => f());
const subscribe = (f: () => void) => { subs.add(f); return () => { subs.delete(f); }; };

export function requestThumb(item: ShopItem) {
  if (cache.has(item.id) || queued.has(item.id)) return;
  queued.add(item.id);
  queue.push(item);
  notify();
}
/** The rendered picture of an item (a data URL) or null while it is still in the queue. */
export function useThumb(item: ShopItem): string | null {
  const id = item.id;
  const url = useSyncExternalStore(subscribe, () => cache.get(id) ?? null, () => null);
  useEffect(() => { if (!url) requestThumb(item); }, [url, item]);
  return url;
}

// ------------------------------------------------------------------ the subject on the stage

const SKIN = "#e8b894", HAIR = "#4a3020";

function HeadMesh({ worn }: { worn: Record<string, string> }) {
  return (
    <group scale={1.75}>
      <mesh castShadow position={[0, 0.15, 0]}><sphereGeometry args={[0.165, 28, 20]} /><meshStandardMaterial color={SKIN} roughness={0.7} /></mesh>
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

function Subject({ item, focus }: { item: ShopItem; focus: React.RefObject<THREE.Group | null> }) {
  const slot = item.slot;
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
function Shot({ item, onDone, ground }: { item: ShopItem; onDone: (url: string) => void; ground: React.RefObject<THREE.Mesh | null> }) {
  const { gl, camera, scene } = useThree();
  const focusRef = useRef<THREE.Group>(null);
  const frames = useRef(0);
  const kind = focusOf(item);
  useFrame(() => {
    frames.current++;
    const g = focusRef.current;
    if (!g) return;
    if (frames.current === 2) {
      const box = new THREE.Box3().setFromObject(g);
      const size = box.getSize(new THREE.Vector3());
      const center = box.getCenter(new THREE.Vector3());
      if (kind === "head") center.y -= size.y * 0.08;
      if (kind === "torso") { center.set(0, 0.3, 0); size.set(0.82, 0.95, 0.5); }
      if (kind === "chest") { center.y = 0.3; size.set(0.55, 0.55, 0.45); center.x = 0; center.z = 0; }
      if (kind === "shoulder") { center.set(-0.12, 0.38, 0.05); size.set(0.55, 0.5, 0.45); }
      // the soft shadow sits under the object and is only as wide as it
      if (ground.current) { ground.current.visible = kind === "object"; ground.current.position.y = box.min.y + 0.003; ground.current.scale.setScalar(Math.max(size.x, size.z) * 1.35); }
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
      onDone(gl.domElement.toDataURL("image/png"));
    }
  });
  return <Subject item={item} focus={focusRef} />;
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

/** Mount once where thumbnails are shown (the shop, the editor): renders the queue, one item at a time, offscreen. */
export function ThumbStudio() {
  const [cur, setCur] = useState<ShopItem | null>(null);
  const busy = useRef(false);
  const groundRef = useRef<THREE.Mesh>(null);
  const next = useCallback(() => {
    busy.current = false;
    const n = queue.shift() ?? null;
    if (n) { busy.current = true; setCur(n); } else setCur(null);
  }, []);
  useEffect(() => {
    const wake = () => { if (!busy.current && queue.length) next(); };
    subs.add(wake);
    wake();
    return () => { subs.delete(wake); };
  }, [next]);
  const done = useCallback((url: string) => {
    setCur((c) => {
      if (c) {
        cache.set(c.id, url);
        queued.delete(c.id);
        if (cache.size > MAX_CACHE) { const first = cache.keys().next().value; if (first) cache.delete(first); }
      }
      return c;
    });
    notify();
    next();
  }, [next]);
  return (
    <div aria-hidden style={{ position: "fixed", left: -10000, top: 0, width: SIZE, height: SIZE, pointerEvents: "none" }}>
      <Canvas dpr={1} gl={{ preserveDrawingBuffer: true, alpha: true, antialias: true }} camera={{ fov: 28, near: 0.1, far: 60, position: [0, 1, 4] }} style={{ width: SIZE, height: SIZE }}>
        <hemisphereLight args={["#ffffff", "#aab4c4", 0.9]} />
        <directionalLight position={[3, 5, 4]} intensity={2.2} />
        <directionalLight position={[-4, 2.5, 2]} intensity={0.7} color="#cfe0ff" />
        <directionalLight position={[0, 3, -4]} intensity={0.9} color="#fff1dc" />
        <Ground gref={groundRef} />
        {cur && <Shot key={cur.id} item={cur} onDone={done} ground={groundRef} />}
      </Canvas>
    </div>
  );
}
