"use client";
// The "Estudio" office style: the REAL office (layout, furniture, agents) as a photographed studio. Skinned glTF people
// with recorded clips and additive gestures, CC0 furniture recolored to a sober palette, an HDRI room light, one soft
// shadow-casting sun and ambient occlusion. Same (no) props as OfficeScene: the office view mounts it with
// dynamic(..., { ssr: false }) when the style is "studio", so three's loaders and the models load only when it is chosen.
// Names, speech bubbles and the approvals card are the 3D office's own (LabelLayer + LabelSolver), fed by the heads
// registered in registry.ts. Drawing is paused off screen and throttled when idle by FrameDriver (also its quality tiers).
import { placed } from "../editorActions";
import dynamic from "next/dynamic";
import { Suspense, useEffect, useMemo, useRef, useState } from "react";
import { Canvas, useFrame, useThree } from "@react-three/fiber";
import { Environment, OrbitControls } from "@react-three/drei";
import { EffectComposer, N8AO, SMAA, ToneMapping, Vignette } from "@react-three/postprocessing";
import { ToneMappingMode } from "postprocessing";
import * as THREE from "three";
import type { OrbitControls as OrbitControlsImpl } from "three-stdlib";
import { useStore } from "@/lib/store";
import { useCost } from "@/lib/cost";
import { useRewards } from "@/lib/rewards";
import { startProjectsRealtime } from "@/lib/projects/store";
import { effective, useOfficeLayout } from "@/lib/officeLayout";
import { usePreferences } from "@/lib/preferences";
import { MOOD_BASE, localHour, timeOfDayMix, type LightMix } from "@/lib/officeLayoutModel";
import { LabelLayer } from "../LabelLayer";
import { LabelSolver } from "../LabelSolver";
import { FrameDriver, useQualityTier } from "../FrameDriver";
import { QUALITY_TIERS } from "../officeQuality";
import { deskOf, seatOf, standOf } from "../registry";
import { acquireAssets, releaseAssets, type Assets } from "./studioAssets";
import { Fixtures, Mats, Pieces, RoomShell, Workstations, disposeShared, type Desk } from "./StudioRoom";
import { People, type DeskInfo } from "./StudioPeople";
import { StudioEdit } from "./StudioEdit";
import { Links } from "../Links";
import { Folders } from "../Folders";
import { CoinFloats } from "../CoinFloats";
import { ShieldFloats } from "../ShieldFloats";
import { PeopleLayer } from "../../presence/PeopleLayer";
import { connectedBody } from "./StudioConnected";
import { HelperLayer } from "../HelperLayer";
import { helperBody } from "./StudioHelpers";

const OfficeScene = dynamic(() => import("../OfficeScene"), { ssr: false });

const VIEW = { pos: new THREE.Vector3(7, 16, 19), target: new THREE.Vector3(0.5, 0.4, -0.3) };
/** The editor looks down on the floor plan so the grid and the footprints read clearly (same view as the 3D office). */
const EDIT = { pos: new THREE.Vector3(0, 25, 15), target: new THREE.Vector3(0, 0, 0.8) };
const WARM = new THREE.Color("#ffb878"), COOL = new THREE.Color("#b8d4ff");

/** Mixes a color toward warm (temp < 0) or cool (temp > 0): the lighting-temperature slider of the editor. */
function tint(hex: string, temp: number) {
  const c = new THREE.Color(hex);
  if (temp < 0) c.lerp(WARM, Math.min(1, -temp) * 0.8);
  else if (temp > 0) c.lerp(COOL, Math.min(1, temp) * 0.8);
  return c;
}

/** The light the room has now: the organization's mood (tuned by temperature and brightness) or, in automatic mode, the viewer's hour. */
function useMix(): { mix: LightMix; temp: number; k: number; night: boolean } {
  const light = useOfficeLayout((s) => effective(s).light);
  const auto = usePreferences((s) => s.prefs.lightAuto);
  const [hour, setHour] = useState(() => localHour(new Date()));
  useEffect(() => {
    if (!auto) return;
    setHour(localHour(new Date()));
    const i = setInterval(() => setHour(localHour(new Date())), 60_000);
    return () => clearInterval(i);
  }, [auto]);
  return useMemo(() => {
    const mix = auto ? timeOfDayMix(hour) : (MOOD_BASE[light.mood] ?? MOOD_BASE.natural);
    return { mix, temp: auto || light.mood === "night" ? 0 : light.temp, k: light.brightness, night: mix.amb < 0.62 };
  }, [auto, hour, light.mood, light.brightness, light.temp]);
}

/** Studio light: the HDRI of a real room, a little sky, one low sun with soft shadows. The quality tier sets the shadow map (FrameDriver). */
function Lights() {
  const { mix, temp, k, night } = useMix();
  const bg = useMemo(() => {
    const open = Math.max(0, Math.min(1, (mix.amb - 0.5) / 0.45)) * 0.85;
    return "#" + new THREE.Color(mix.bg).lerp(new THREE.Color("#f2f3f5"), open).getHexString();
  }, [mix.bg, mix.amb]);
  const sun = useMemo(() => tint(mix.sunColor, temp), [mix.sunColor, temp]);
  const sky = useMemo(() => tint(mix.hemiSky, temp), [mix.hemiSky, temp]);
  return (
    <>
      <color attach="background" args={[bg]} />
      <fog attach="fog" args={[bg, 45, 110]} />
      <Suspense fallback={null}><Environment files="/office/env/room_512.hdr" environmentIntensity={(night ? 0.14 : 0.3) * k} /></Suspense>
      <hemisphereLight args={[sky, mix.hemiGround, 0.1 * mix.hemi * k]} />
      <directionalLight
        castShadow position={mix.sunPos} intensity={(1.25 * mix.sun / 1.55) * k} color={sun}
        shadow-mapSize={[2048, 2048]} shadow-bias={-0.0004} shadow-normalBias={0.02} shadow-radius={6}
        shadow-camera-left={-18} shadow-camera-right={18} shadow-camera-top={14} shadow-camera-bottom={-14} shadow-camera-near={1} shadow-camera-far={60}
      />
    </>
  );
}

/** The same near-isometric camera as the other 3D styles, with bounded zoom and turn (a phone upright backs off and widens). */
function Rig() {
  const ref = useRef<OrbitControlsImpl>(null);
  const editing = useOfficeLayout((s) => s.editing);
  const dragging = useOfficeLayout((s) => s.dragging);
  const camera = useThree((s) => s.camera) as THREE.PerspectiveCamera;
  const aspect = useThree((s) => s.size.width / Math.max(1, s.size.height));
  const narrow = aspect < 0.9;
  const back = narrow ? 1 + Math.min(0.55, (0.9 - aspect) * 1.2) : 1;
  useEffect(() => {
    const fov = narrow ? Math.min(52, 32 + (0.9 - aspect) * 40) : 32;
    if (camera.fov !== fov) { camera.fov = fov; camera.updateProjectionMatrix(); }
    const c = ref.current;
    if (!c || editing) return;
    c.object.position.copy(c.target.clone().add(VIEW.pos.clone().sub(VIEW.target).multiplyScalar(back)));
    c.update();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only a change of shape reframes the view
  }, [narrow]);
  const tween = useRef<{ t: number; fromPos: THREE.Vector3; fromTarget: THREE.Vector3; to: typeof VIEW } | null>(null);
  const first = useRef(true);
  useEffect(() => {
    if (first.current) { first.current = false; return; }
    const c = ref.current;
    if (!c) return;
    tween.current = { t: 0, fromPos: c.object.position.clone(), fromTarget: c.target.clone(), to: editing ? EDIT : { pos: VIEW.target.clone().add(VIEW.pos.clone().sub(VIEW.target).multiplyScalar(back)), target: VIEW.target } };
  }, [editing]); // eslint-disable-line react-hooks/exhaustive-deps -- `back` is read when the edit mode ends
  useFrame((_, dt) => {
    const c = ref.current;
    if (!c) return;
    const tw = tween.current;
    if (tw) { // the camera glides between the editing view and the normal one
      tw.t = Math.min(1, tw.t + dt / 0.9);
      const e = 1 - Math.pow(1 - tw.t, 3);
      c.object.position.lerpVectors(tw.fromPos, tw.to.pos, e);
      c.target.lerpVectors(tw.fromTarget, tw.to.target, e);
      if (tw.t >= 1) tween.current = null;
      return;
    }
    const t = c.target, wide = editing || narrow;
    t.x = THREE.MathUtils.clamp(t.x, wide ? -10 : -7, wide ? 10 : 7);
    t.z = THREE.MathUtils.clamp(t.z, wide ? -8 : -5, wide ? 8 : 6);
    t.y = THREE.MathUtils.clamp(t.y, 0, 1.2);
  });
  return (
    <OrbitControls
      ref={ref as never} makeDefault target={[0.5, 0.4, -0.3]} enabled={!dragging} enableDamping dampingFactor={0.08}
      minDistance={editing ? 8 : narrow ? 7 : 11} maxDistance={(editing ? 38 : 30) * back} minPolarAngle={editing ? 0.08 : 0.55} maxPolarAngle={editing ? 1.2 : 1.1}
      minAzimuthAngle={editing || narrow ? -1.4 : -0.7} maxAzimuthAngle={editing || narrow ? 1.4 : 0.7}
      panSpeed={narrow ? 1 : 0.7} rotateSpeed={0.45} zoomSpeed={0.7} screenSpacePanning={!narrow}
      touches={{ ONE: narrow ? THREE.TOUCH.PAN : THREE.TOUCH.ROTATE, TWO: THREE.TOUCH.DOLLY_ROTATE }}
    />
  );
}

/** Ambient occlusion and a film curve, only while the quality tier allows (the tier drops them on slow computers). */
function PostFx() {
  const tier = useQualityTier();
  if (QUALITY_TIERS[tier].ao === "off") return null;
  return (
    <EffectComposer multisampling={0} depthBuffer>
      <N8AO halfRes quality="performance" aoRadius={0.55} intensity={2.2} distanceFalloff={0.8} />
      <ToneMapping mode={ToneMappingMode.ACES_FILMIC} />
      <SMAA />
      <Vignette offset={0.3} darkness={0.3} />
    </EffectComposer>
  );
}

function World({ a }: { a: Assets }) {
  const base = useOfficeLayout((s) => effective(s));
  const zoneGhost = useOfficeLayout((s) => s.zoneGhost);
  const deskGhost = useOfficeLayout((s) => s.deskGhost);
  // a room being dragged shows where it would land: its furniture follows
  const layout = useMemo(() => (zoneGhost ? { ...base, zones: { ...base.zones, [zoneGhost.id]: { x: zoneGhost.x, z: zoneGhost.z } } } : base), [base, zoneGhost]);
  const catalog = useRewards((s) => s.catalog);
  const key = useStore((s) => s.agentOrder.map((id) => `${id}:${s.agents[id]?.role ?? ""}`).join(","));
  const { mix, night } = useMix();
  const ground = useMemo(() => "#" + new THREE.Color(mix.bg).lerp(new THREE.Color("#e9ecf0"), night ? 0 : 0.85).getHexString(), [mix.bg, night]);
  // the workstations as the layout puts them (desks the organization moved, the desk and chair it picked)
  const { desks, info } = useMemo(() => {
    const S = useStore.getState();
    const byId = new Map(catalog.map((i) => [i.id, i] as const));
    const list: Desk[] = [];
    const map = new Map<string, DeskInfo>();
    S.agentOrder.forEach((id, i) => {
      const ag = S.agents[id];
      if (!ag) return;
      const pos: [number, number] = deskGhost?.id === id ? [deskGhost.x, deskGhost.z] : deskOf({ id, role: ag.role }, i);
      const cfg = layout.desks[id];
      list.push({ id, pos, deskItem: cfg?.desk ? byId.get(cfg.desk) : undefined, chairItem: cfg?.chair ? byId.get(cfg.chair) : undefined });
      map.set(id, { pos, own: { seat: seatOf(pos), stand: standOf(pos) } });
    });
    return { desks: list, info: map };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `key` stands for the team (ids and roles)
  }, [key, layout, catalog, deskGhost]);
  const body = useMemo(() => connectedBody(a), [a]); // the connected people are glTF figures here, like the agents
  const hBody = useMemo(() => helperBody(a), [a]); // the helpers are glTF people too, in grey
  const infoRef = useRef(info);
  infoRef.current = info;
  return (
    <>
      <RoomShell layout={layout} ground={ground} />
      <Mats layout={layout} desks={desks} />
      <Workstations a={a} desks={desks} />
      <Fixtures a={a} layout={layout} />
      <Pieces a={a} layout={layout} />
      <People a={a} desks={infoRef} />
      <Links />
      <Folders />
      <CoinFloats />
      <ShieldFloats />
      <HelperLayer body={hBody} />
      <PeopleLayer body={body} />
      <StudioEdit layout={layout} desks={desks} />
    </>
  );
}

export default function OfficeStudioScene() {
  const select = useStore((s) => s.select);
  const [assets, setAssets] = useState<Assets | null>(null);
  const [failed, setFailed] = useState(false);
  // the same data feeds as the other 3D styles: project summaries, the layout and its catalog, the remaining budget per agent
  useEffect(() => startProjectsRealtime(), []);
  useEffect(() => { void useRewards.getState().load(); void useOfficeLayout.getState().load(); }, []);
  useEffect(() => {
    const { loadStatus } = useCost.getState();
    void loadStatus();
    const i = setInterval(() => void useCost.getState().loadStatus(), 20000);
    return () => clearInterval(i);
  }, []);
  // the models belong to this scene: loaded when it opens, disposed when it closes
  useEffect(() => {
    let on = true;
    acquireAssets().then((a) => { if (on) setAssets(a); }).catch((e) => { console.error("STUDIO models failed", e); if (on) setFailed(true); });
    return () => { on = false; releaseAssets(); disposeShared(); };
  }, []);

  if (failed) return <OfficeScene />; // the models could not load (offline, blocked): the normal office instead of a blank stage
  return (
    <div className="relative h-full w-full" data-testid="office-studio">
      <Canvas
        shadows="soft" dpr={[1, 1.75]}
        frameloop="never" /* FrameDriver draws the frames: paused off screen, 30 fps when idle, adaptive quality */
        camera={{ position: [7, 16, 19], fov: 32, near: 0.5, far: 160 }}
        gl={{ antialias: false, powerPreference: "high-performance" }}
        onPointerMissed={() => {
          const ed = useOfficeLayout.getState();
          if (ed.editing) { if (!ed.placing && !ed.dragging && performance.now() - placed.at > 400) ed.select(null); } else select(null);
        }}
      >
        <Lights />
        <Suspense fallback={null}>{assets && <World a={assets} />}</Suspense>
        <PostFx />
        <LabelSolver />
        <Rig />
        <FrameDriver />
      </Canvas>
      <LabelLayer />
    </div>
  );
}
