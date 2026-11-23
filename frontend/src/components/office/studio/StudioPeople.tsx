"use client";
// The people of the "Estudio" office: one glTF figure per real agent. WHERE it is and WHAT it is doing comes from the same
// simulation the "Plano" office uses (flat/agentSim.ts: the real state, zoneFor, meetingPlace, the approvals desk, the
// cafeteria, gestures); this file only turns that into recorded clips and additive gestures (rig.ts).
import { useEffect, useMemo, useRef } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { useStore } from "@/lib/store";
import { useAgentView, usePreferences } from "@/lib/preferences";
import { WALK_SPEED, envelope } from "../gestures";
import { feetPos, headPos, standOf, visitorOf } from "../registry";
import { makeSim, stepAgent, type Own, type Sim } from "../flat/agentSim";
import type { Assets } from "./studioAssets";
import { buildRig, type Rig } from "./rig";
import { addWeights, nextBase, outfitFor, startBase, type BaseState } from "./studioLogic";

export interface DeskInfo { pos: [number, number]; own: Own }

function Person({ id, a, desks }: { id: string; a: Assets; desks: React.RefObject<Map<string, DeskInfo>> }) {
  const view = useAgentView(id);
  const outfit = useMemo(() => outfitFor(id, view.female), [id, view.female]);
  const rig = useMemo<Rig>(() => buildRig(a.people[outfit.model], outfit.model, outfit, view.skin, view.hair), [a, outfit, view.skin, view.hair]);
  useEffect(() => () => rig.dispose(), [rig]);
  const root = useRef<THREE.Group>(null!);
  const ring = useRef<THREE.Mesh>(null!);
  const st = useRef<{ sim: Sim; base: BaseState | null; hold: boolean; seatKey: string }>({ sim: null as unknown as Sim, base: null, hold: false, seatKey: "" });

  useFrame((_, delta) => {
    const info = desks.current?.get(id);
    const S = useStore.getState();
    const ag = S.agents[id];
    if (!info || !ag || !root.current) return;
    const me = st.current;
    const seatKey = `${info.own.seat[0].toFixed(2)},${info.own.seat[1].toFixed(2)}`;
    if (!me.sim) { me.sim = makeSim(id, info.own.seat); me.seatKey = seatKey; }
    else if (seatKey !== me.seatKey) { me.seatKey = seatKey; me.sim.place = ""; } // the desk moved: plan a new route
    const reduce = usePreferences.getState().prefs.reduceMotion;
    const dt = Math.min(0.1, delta);
    const selected = S.selectedAgentId === id;
    const step = stepAgent(me.sim, id, me.hold ? 0 : dt, info.own, {
      selected, reduce,
      deskStand: (o) => { const d = desks.current?.get(o); return d ? standOf(d.pos) : null; },
      deskVisit: (o) => { const d = desks.current?.get(o); return d ? visitorOf(d.pos) : null; },
    });
    if (!step) return;
    const sm = me.sim;
    const seated = step.mode.startsWith("seat:");
    const want = { walking: step.walking, seated };
    if (!me.base || reduce) { me.base = startBase(seated); me.hold = false; }
    else { const r = nextBase(me.base, want, dt); me.base = r.state; me.hold = r.hold; }
    const gesture = sm.gKind;
    const add = addWeights({ mode: step.mode, state: ag.state, talking: step.talking, gesture, env: gesture ? envelope(step.gu) : 0, base: me.base.base });
    rig.update(dt, me.base.base, me.base.t, sm.speed / WALK_SPEED, add, step.mug, reduce);
    const sitting = me.base.base === "Seated" || me.base.base === "SitDown";
    root.current.position.set(sm.x, 0, sm.z + (sitting ? -0.06 : 0));
    root.current.rotation.y = sm.yaw;
    if (ring.current) ring.current.visible = selected;
    // where the labels and bubbles hang (LabelSolver adds half a meter above the head)
    let h = headPos.get(id); if (!h) { h = new THREE.Vector3(); headPos.set(id, h); }
    h.set(sm.x, sitting ? 1.15 : 1.4, sm.z);
    let f = feetPos.get(id); if (!f) { f = new THREE.Vector3(); feetPos.set(id, f); }
    f.set(sm.x, 0, sm.z);
  });
  useEffect(() => () => { headPos.delete(id); feetPos.delete(id); }, [id]);

  const select = useStore((s) => s.select);
  return (
    <group>
      <group ref={root}>
        <primitive object={rig.root} />
        {/* the click target: a capsule-ish proxy, because raycasting a skinned mesh is slow and the pose is not its bind pose */}
        <mesh position={[0, 0.9, 0]}
          onClick={(e) => { e.stopPropagation(); select(id); }}
          onPointerOver={() => { document.body.style.cursor = "pointer"; }} onPointerOut={() => { document.body.style.cursor = ""; }}>
          <cylinderGeometry args={[0.42, 0.42, 1.8, 10]} />
          <meshBasicMaterial transparent opacity={0} depthWrite={false} />
        </mesh>
        <mesh ref={ring} rotation={[-Math.PI / 2, 0, 0]} position={[0, 0.03, 0]} visible={false}>
          <ringGeometry args={[0.52, 0.64, 40]} />
          <meshBasicMaterial color="#0A66C2" toneMapped={false} transparent opacity={0.95} />
        </mesh>
      </group>
    </group>
  );
}

export function People({ a, desks }: { a: Assets; desks: React.RefObject<Map<string, DeskInfo>> }) {
  const order = useStore((s) => s.agentOrder);
  return <>{order.map((id) => <Person key={id} id={id} a={a} desks={desks} />)}</>;
}
