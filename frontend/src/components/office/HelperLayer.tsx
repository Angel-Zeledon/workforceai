"use client";
// The temporary helpers in the 3D office (all three 3D styles: classic, bricks, pixel, and Estudio with its own glTF bodies).
// Figures stand next to the desk of the employee who coordinates them (helperFigures.ts plans where), in neutral grey clothes, with no
// name of their own: hovering one says "Ayudante 3 · Lima · leyendo". A click selects the OWNER. They are not agents: they are not in the
// store's agent list, the labels, the counters or the simulation. With the flag off or no active group nothing is mounted. With software
// rendering or reduced motion only a "+8 ayudantes" label stands over the desk.
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { useHelpers } from "@/lib/helpers";
import { isActiveGroup } from "@/lib/helpersLogic";
import { useStore } from "@/lib/store";
import { useOfficeLayout, effective } from "@/lib/officeLayout";
import { usePreferences } from "@/lib/preferences";
import { useT } from "@/lib/i18n";
import { PersonTag } from "../presence/PersonTag";
import { LOWEST_TIER } from "./officeQuality";
import { useQualityTier } from "./FrameDriver";
import { deskOf } from "./registry";
import { FAIL_RED, HELPER_GREY, HELPER_HAIRS, HELPER_SKINS, LEAVE_S, hashKey, planHelpers, presence, type Fig, type FigState, type Plan } from "./helperFigures";

/** What draws the body of a helper (feet at the origin, facing +z). The default is the light capsule figure; Estudio supplies its glTF people. */
export type HelperBody = (o: { id: string; state: FigState; seed: number }) => ReactNode;

const SHARED = {
  torso: new THREE.CapsuleGeometry(0.2, 0.52, 4, 10),
  head: new THREE.SphereGeometry(0.18, 14, 10),
  hair: new THREE.SphereGeometry(0.19, 12, 8, 0, Math.PI * 2, 0, Math.PI * 0.55),
  ring: new THREE.RingGeometry(0.3, 0.38, 24),
};

function CapsuleBody({ seed }: { id: string; state: FigState; seed: number }) {
  const skin = useMemo(() => new THREE.MeshStandardMaterial({ color: HELPER_SKINS[seed % HELPER_SKINS.length], roughness: 0.8 }), [seed]);
  const hair = useMemo(() => new THREE.MeshStandardMaterial({ color: HELPER_HAIRS[(seed >>> 3) % HELPER_HAIRS.length], roughness: 0.9 }), [seed]);
  const cloth = useMemo(() => new THREE.MeshStandardMaterial({ color: HELPER_GREY.body, roughness: 0.8 }), []);
  useEffect(() => () => { skin.dispose(); hair.dispose(); cloth.dispose(); }, [skin, hair, cloth]);
  return (
    <>
      <mesh geometry={SHARED.torso} material={cloth} position-y={0.55} castShadow />
      <mesh geometry={SHARED.head} material={skin} position-y={1.2} castShadow />
      <mesh geometry={SHARED.hair} material={hair} position-y={1.22} rotation-x={-0.25} />
    </>
  );
}
const defaultBody: HelperBody = (o) => <CapsuleBody {...o} />;

/** A small tag on the figure only while the pointer is over it. */
function HoverTag({ text }: { text: string }) {
  return <PersonTag position={[0, 1.75, 0]} name={text} color="#8b95a5" self={false} away={false} testId="helper-tag" />;
}

interface Item { fig: Fig; born: number; gone: number | null }

function Figure({ item, body, label }: { item: Item; body: HelperBody; label: string }) {
  const { fig } = item;
  const g = useRef<THREE.Group>(null);
  const lean = useRef<THREE.Group>(null);
  const [hover, setHover] = useState(false);
  const seed = useMemo(() => hashKey(fig.key), [fig.key]);
  const phase = useMemo(() => (seed % 628) / 100, [seed]);
  useFrame(({ clock }) => {
    const grp = g.current;
    if (!grp) return;
    const now = performance.now() / 1000;
    const p = presence(now - item.born, item.gone === null ? null : now - item.gone);
    grp.visible = p > 0.01;
    grp.scale.setScalar(0.4 + 0.6 * p);
    grp.position.y = (1 - p) * -0.25; // rises into place
    const l = lean.current;
    // working: leans a little toward the desk and nods, like someone reading; failed: stands still
    if (l) l.rotation.x = fig.state === "working" ? 0.1 + Math.sin(clock.elapsedTime * 1.7 + phase) * 0.05 : 0;
  });
  return (
    <group position={[fig.x, 0, fig.z]} rotation-y={fig.face}>
      <group ref={g} userData={{ noStuds: true, figure: true }}
        onPointerOver={(e) => { e.stopPropagation(); setHover(true); document.body.style.cursor = "pointer"; }}
        onPointerOut={() => { setHover(false); document.body.style.cursor = ""; }}
        onClick={(e) => { e.stopPropagation(); useStore.getState().select(fig.owner); }}>
        <group ref={lean}>{body({ id: fig.key, state: fig.state, seed })}</group>
        {/* a hit area a little larger than the body so it is easy to point at */}
        <mesh position-y={0.8} visible={false}><cylinderGeometry args={[0.35, 0.35, 1.6, 8]} /><meshBasicMaterial /></mesh>
        {fig.state === "failed" && <mesh position={[0, 1.62, 0]}><sphereGeometry args={[0.07, 10, 8]} /><meshBasicMaterial color={FAIL_RED} /></mesh>}
        {fig.state === "queued" && <mesh geometry={SHARED.ring} rotation-x={-Math.PI / 2} position-y={0.02}><meshBasicMaterial color="#8b95a5" transparent opacity={0.5} /></mesh>}
        {hover && <HoverTag text={label} />}
      </group>
    </group>
  );
}

function CountTag({ x, z, text }: { x: number; z: number; text: string }) {
  return (
    <group position={[x, 0, z]}>
      <PersonTag position={[0, 2.5, 0]} name={text} color="#8b95a5" self={false} away={false} testId="helper-more-tag" />
    </group>
  );
}

function Inner({ body }: { body: HelperBody }) {
  const { t } = useT();
  const groups = useHelpers((s) => s.groups);
  const tier = useQualityTier();
  const reduce = usePreferences((s) => s.prefs.reduceMotion);
  const labelOnly = reduce || tier >= LOWEST_TIER;
  const key = useStore((s) => s.agentOrder.map((id) => `${id}:${s.agents[id]?.role ?? ""}`).join(","));
  const layout = useOfficeLayout((s) => effective(s));
  const desks = useMemo(() => {
    const S = useStore.getState();
    const m: Record<string, [number, number]> = {};
    S.agentOrder.forEach((id, i) => { const ag = S.agents[id]; if (ag) m[id] = deskOf({ id, role: ag.role }, i); });
    return m;
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `key` stands for the team, `layout` for where its desks are
  }, [key, layout]);
  const slots = useRef<Record<string, number>>({});
  const plan: Plan = useMemo(() => {
    const p = planHelpers(Object.values(groups), desks, { labelOnly, prev: slots.current });
    slots.current = p.slotOf;
    return p;
  }, [groups, desks, labelOnly]);
  // figures that left stay a moment to walk off / fade out
  const items = useRef(new Map<string, Item>());
  const [, bump] = useState(0);
  const now = performance.now() / 1000;
  const here = new Set(plan.figures.map((f) => f.key));
  for (const f of plan.figures) {
    const it = items.current.get(f.key);
    if (it) { it.fig = f; it.gone = null; } else items.current.set(f.key, { fig: f, born: now, gone: null });
  }
  let leaving = false;
  for (const [k, it] of items.current) {
    if (here.has(k)) continue;
    if (it.gone === null) it.gone = now;
    if (now - it.gone > LEAVE_S) items.current.delete(k); else leaving = true;
  }
  useEffect(() => {
    if (!leaving) return;
    const id = setTimeout(() => bump((n) => n + 1), LEAVE_S * 1000 + 50);
    return () => clearTimeout(id);
  });
  const stateText = (s: FigState) => t(`helpers.fig.${s}`);
  const list = [...items.current.values()];
  return (
    <>
      {list.map((it) => <Figure key={it.fig.key} item={it} body={body} label={t("helpers.fig.name", { n: it.fig.n, part: it.fig.label, state: stateText(it.fig.state) })} />)}
      {plan.more.map((m) => <CountTag key={m.groupId} x={m.x} z={m.z} text={t("helpers.fig.more", { n: m.n })} />)}
      {plan.labels.map((l) => <CountTag key={l.owner} x={l.x} z={l.z} text={l.n === 1 ? t("helpers.fig.label1") : t("helpers.fig.label", { n: l.n })} />)}
    </>
  );
}

/** Mounts only while a group is working (and a moment after, so the last helpers can leave). Nothing at all with the flag off. */
export function HelperLayer({ body }: { body?: HelperBody } = {}) {
  const editing = useOfficeLayout((s) => s.editing);
  const active = useHelpers((s) => s.enabled && Object.values(s.groups).some(isActiveGroup));
  const [lingering, setLingering] = useState(false);
  useEffect(() => {
    if (active) { setLingering(true); return; }
    const id = setTimeout(() => setLingering(false), LEAVE_S * 1000 + 100);
    return () => clearTimeout(id);
  }, [active]);
  if (editing || (!active && !lingering)) return null;
  return <Inner body={body ?? defaultBody} />;
}
