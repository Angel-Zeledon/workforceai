// A person of the "Estudio" office: a skinned glTF figure (Quaternius, CC0) animated with an AnimationMixer. The recorded
// clips (Idle, Walk, SitDown, StandUp) are cross-faded as the base; the gestures the pack does not have (typing, talking,
// nodding, waving...) are keyframe clips made here and blended ADDITIVELY on top. The technique is the one of the lab's
// panel B (officeLab/PanelB.tsx), applied to the real agents.
import * as THREE from "three";
import { clone as cloneSkinned } from "three/examples/jsm/utils/SkeletonUtils.js";
import { humanSkin } from "../flat/isoMath";
import { ADD_CLIPS, SIT_S, STAND_S, type Add, type Base, type Model, type Outfit } from "./studioLogic";
import { disposeMaterial, type PersonModel } from "./studioAssets";

const X = new THREE.Vector3(1, 0, 0), Y = new THREE.Vector3(0, 1, 0), Z = new THREE.Vector3(0, 0, 1);
const TAU = Math.PI * 2;
export const PERSON_H = 1.78;

const bone = (model: THREE.Object3D, name: string) => model.getObjectByName(name) ?? model.getObjectByName(name.replace(/\./g, "")) ?? null;

/**
 * A hand-made additive clip: for each bone, rotations about axes of the PERSON (X forward/back, Y turn, Z sideways)
 * expressed in the bone's own space at the reference pose, so it does not depend on how each bone is oriented.
 */
function additiveClip(name: string, model: THREE.Object3D, dur: number, n: number, spec: Record<string, (u: number) => [number, number, number]>) {
  const tracks: THREE.KeyframeTrack[] = [];
  const times: number[] = [];
  for (let i = 0; i <= n; i++) times.push((dur * i) / n);
  const qw = new THREE.Quaternion(), inv = new THREE.Quaternion(), q = new THREE.Quaternion(), qa = new THREE.Quaternion();
  for (const [bn, fn] of Object.entries(spec)) {
    const b = bone(model, bn);
    if (!b) continue;
    b.getWorldQuaternion(qw);
    inv.copy(qw).invert();
    const ax = X.clone().applyQuaternion(inv), ay = Y.clone().applyQuaternion(inv), az = Z.clone().applyQuaternion(inv);
    const vals: number[] = [];
    for (const t of times) {
      const [rx, ry, rz] = fn(t / dur);
      q.setFromAxisAngle(ax, rx);
      q.multiply(qa.setFromAxisAngle(ay, ry));
      q.multiply(qa.setFromAxisAngle(az, rz));
      vals.push(q.x, q.y, q.z, q.w);
    }
    tracks.push(new THREE.QuaternionKeyframeTrack(`${b.name}.quaternion`, times, vals));
  }
  return new THREE.AnimationClip(name, dur, tracks, THREE.AdditiveAnimationBlendMode);
}

const s = Math.sin;
/** Every gesture: [duration, samples, bones]. Angles in radians. */
const GESTURES: Record<Add, { dur: number; n: number; refPose: "sit" | "idle"; spec: Record<string, (u: number) => [number, number, number]> }> = {
  type: {
    dur: 0.5, n: 10, refPose: "sit", spec: {
      "UpperArm.L": () => [-0.45, 0, 0], "UpperArm.R": () => [-0.45, 0, 0],
      "LowerArm.L": (u) => [-0.55 + s(u * TAU * 2) * 0.09, 0, 0], "LowerArm.R": (u) => [-0.55 + s(u * TAU * 2 + Math.PI) * 0.09, 0, 0],
      "Head": (u) => [0.12 + s(u * TAU) * 0.02, 0, 0],
    },
  },
  talk: {
    dur: 2.4, n: 24, refPose: "idle", spec: {
      "UpperArm.R": (u) => [-0.75 + s(u * TAU * 2) * 0.22, 0, -0.15 + s(u * TAU) * 0.12],
      "LowerArm.R": (u) => [-0.8 + s(u * TAU * 3) * 0.3, 0, 0],
      "UpperArm.L": (u) => [-0.35 + s(u * TAU + 1) * 0.15, 0, 0.1],
      "LowerArm.L": (u) => [-0.6 + s(u * TAU * 2 + 1) * 0.2, 0, 0],
      "Head": (u) => [s(u * TAU * 4) * 0.05, s(u * TAU) * 0.15, 0],
      "Torso": (u) => [0, s(u * TAU) * 0.08, 0],
    },
  },
  reach: { dur: 1, n: 2, refPose: "idle", spec: { "UpperArm.R": () => [-1.15, 0, 0.1], "LowerArm.R": () => [-0.25, 0, 0], "Head": () => [0.15, 0, 0], "Torso": () => [0.12, 0, 0] } },
  nod: { dur: 1.1, n: 22, refPose: "idle", spec: { "Head": (u) => [(1 - Math.cos(u * TAU * 2)) * 0.12, 0, 0], "Neck": (u) => [(1 - Math.cos(u * TAU * 2)) * 0.05, 0, 0] } },
  shake: { dur: 1.2, n: 24, refPose: "idle", spec: { "Head": (u) => [0.04, s(u * TAU * 3) * 0.32, 0], "Neck": (u) => [0, s(u * TAU * 3) * 0.12, 0] } },
  wave: {
    dur: 1.3, n: 26, refPose: "idle", spec: {
      "UpperArm.R": () => [-0.35, 0, -1.75],
      "LowerArm.R": (u) => [-0.15, 0, -0.55 + s(u * TAU * 2) * 0.45],
      "Head": () => [0, 0.12, 0.04],
    },
  },
  look: { dur: 3, n: 30, refPose: "idle", spec: { "Head": (u) => [0.02, s(u * TAU) * 0.55, 0], "Neck": (u) => [0, s(u * TAU) * 0.2, 0], "Torso": (u) => [0, s(u * TAU) * 0.1, 0] } },
  stretch: {
    dur: 2, n: 20, refPose: "idle", spec: {
      // arms rise out to the sides with the elbows a little bent (straight, parallel arms read as "hands up"), the chest opens and
      // the whole thing breathes in and out once; it is a stretch, not a pose
      "UpperArm.L": (u) => [-2.3 - s(u * Math.PI) * 0.25, 0, 0.75], "UpperArm.R": (u) => [-2.3 - s(u * Math.PI) * 0.25, 0, -0.75],
      "LowerArm.L": () => [-0.45, 0, 0], "LowerArm.R": () => [-0.45, 0, 0],
      "Torso": (u) => [-0.1 - s(u * Math.PI) * 0.08, 0, 0], "Head": (u) => [-0.16 - s(u * Math.PI) * 0.06, 0, 0],
    },
  },
  sip: { dur: 1, n: 2, refPose: "idle", spec: { "UpperArm.R": () => [-0.7, 0, -0.1], "LowerArm.R": () => [-1.75, 0, 0], "Head": () => [-0.12, 0, 0] } },
};

/** The additive clips of one model, built once against a throw-away copy and shared by every person of that model. */
const clipCache = new WeakMap<THREE.Object3D, Record<Add, THREE.AnimationClip>>();
function additiveClips(src: PersonModel): Record<Add, THREE.AnimationClip> {
  const hit = clipCache.get(src.scene);
  if (hit) return hit;
  const model = cloneSkinned(src.scene);
  const root = new THREE.Group();
  root.add(model);
  const mixer = new THREE.AnimationMixer(model);
  const find = (n: string) => src.animations.find((a) => a.name === n)!;
  const pose = (n: "sit" | "idle") => {
    mixer.stopAllAction();
    const clip = n === "sit" ? find("SitDown") : find("Idle");
    const a = mixer.clipAction(clip);
    a.reset().play();
    a.time = n === "sit" ? clip.duration - 0.01 : 0;
    mixer.update(0);
    root.updateMatrixWorld(true);
  };
  const out = {} as Record<Add, THREE.AnimationClip>;
  for (const k of ADD_CLIPS) {
    const g = GESTURES[k];
    pose(g.refPose);
    out[k] = additiveClip(k, model, g.dur, g.n, g.spec);
  }
  mixer.stopAllAction();
  mixer.uncacheRoot(model);
  clipCache.set(src.scene, out);
  return out;
}

const hex = (n: number) => "#" + n.toString(16).padStart(6, "0");

export interface Rig {
  root: THREE.Group;
  /** the part that animates (its bones and meshes); the hit proxy and the ring are siblings, not affected */
  update(dt: number, base: Base, baseT: number, walkSpeed: number, add: Record<Add, number>, mug: boolean, reduce: boolean): void;
  dispose(): void;
}

/** Colors by the material names of each pack (the packs ship a near-black mannequin skin and game colors). */
function colorsFor(model: Model, o: Outfit, skin: string, hair: string): Record<string, string> {
  if (model === "suit") return { Skin: skin, Black: o.jacket, Shirt: o.shirt, Details: "#3b4452", Belt: o.belt, Hair: hair };
  if (model === "casual") return { Skin: skin, Shirt: o.knit, Pants: o.pants, Belt: o.belt, Hair: hair };
  return { Skin: skin, Shirt: o.shirt, Pants: o.pants, Detail: o.knit, Belt: o.belt, Hair: hair };
}

export function buildRig(src: PersonModel, model: Model, outfit: Outfit, skinHex: string, hairHex: string): Rig {
  const scene = cloneSkinned(src.scene);
  const skin = hex(humanSkin(skinHex)), colors = colorsFor(model, outfit, skin, hairHex);
  const own: THREE.Material[] = [];
  scene.traverse((n) => {
    const m = n as THREE.Mesh;
    if (!m.isMesh) return;
    m.castShadow = true; m.receiveShadow = true; m.frustumCulled = false;
    m.raycast = () => { /* clicks go to the hit proxy: raycasting a skinned mesh is slow */ };
    const mat = (m.material as THREE.MeshStandardMaterial).clone();
    own.push(mat);
    const c = colors[mat.name];
    if (c) mat.color.set(c);
    if (mat.name === "Skin") mat.roughness = 0.62;
    mat.envMapIntensity = 0.6;
    m.material = mat;
    if (mat.name === "Hat") m.visible = false;
  });
  scene.updateMatrixWorld(true);
  const box = new THREE.Box3().setFromObject(scene);
  scene.scale.multiplyScalar((PERSON_H * outfit.height) / ((box.max.y - box.min.y) || 1));
  const root = new THREE.Group();
  root.add(scene);

  const mixer = new THREE.AnimationMixer(scene);
  const clip = (n: string) => src.animations.find((a) => a.name === n)!;
  const base: Record<Base, THREE.AnimationAction> = {
    Idle: mixer.clipAction(clip("Idle")), Walk: mixer.clipAction(clip("Walk")), SitDown: mixer.clipAction(clip("SitDown")), StandUp: mixer.clipAction(clip("StandUp")),
    Seated: mixer.clipAction(THREE.AnimationUtils.subclip(clip("SitDown"), "Seated", Math.max(0, Math.floor(clip("SitDown").duration * 30) - 2), Math.floor(clip("SitDown").duration * 30), 30)),
  };
  for (const [k, a] of Object.entries(base) as [Base, THREE.AnimationAction][]) {
    const once = k === "SitDown" || k === "StandUp";
    a.setLoop(once ? THREE.LoopOnce : THREE.LoopRepeat, Infinity);
    a.clampWhenFinished = true;
    if (k === "SitDown") a.timeScale = clip("SitDown").duration / SIT_S;
    if (k === "StandUp") a.timeScale = clip("StandUp").duration / STAND_S;
  }
  const clips = additiveClips(src);
  const add = {} as Record<Add, THREE.AnimationAction>;
  for (const k of ADD_CLIPS) { add[k] = mixer.clipAction(clips[k]); add[k].play(); add[k].setEffectiveWeight(0); }

  // a mug in the right hand for the coffee break and the sips
  const hand = bone(scene, "Fist.R");
  let mug: THREE.Mesh | null = null;
  const mugGeo = new THREE.CylinderGeometry(0.036, 0.032, 0.085, 14);
  const mugMat = new THREE.MeshStandardMaterial({ color: "#f4efe6", roughness: 0.5 });
  if (hand) {
    const sc = hand.getWorldScale(new THREE.Vector3()).x || 1;
    mug = new THREE.Mesh(mugGeo, mugMat);
    mug.scale.setScalar(1 / sc);
    mug.position.set(0, 0.05 / sc, 0.02 / sc);
    mug.castShadow = true;
    mug.visible = false;
    hand.add(mug);
  }

  let cur: Base | "" = "";
  const weights = Object.fromEntries(ADD_CLIPS.map((k) => [k, 0])) as Record<Add, number>;
  const update: Rig["update"] = (dt, want, wantT, speed, target, withMug, reduce) => {
    if (want !== cur) {
      const next = base[want];
      next.reset();
      if (want === "SitDown" || want === "StandUp") next.time = Math.min(next.getClip().duration, wantT * next.timeScale);
      next.play();
      if (!cur || reduce) { for (const [k, a] of Object.entries(base)) if (k !== want) a.stop(); next.setEffectiveWeight(1); }
      else { next.fadeIn(want === "Seated" ? 0.05 : 0.28); base[cur].fadeOut(0.28); }
      cur = want;
    }
    if (want === "Walk") base.Walk.timeScale = 0.45 + 0.75 * Math.min(1, speed);
    for (const k of ADD_CLIPS) {
      weights[k] += (target[k] - weights[k]) * (reduce ? 1 : Math.min(1, dt * 7));
      add[k].setEffectiveWeight(weights[k]);
    }
    if (mug) mug.visible = withMug;
    mixer.update(reduce ? 0 : dt);
    if (reduce) mixer.update(0);
  };
  return {
    root, update,
    dispose() {
      mixer.stopAllAction();
      mixer.uncacheRoot(scene);
      for (const m of own) disposeMaterial(m);
      mugGeo.dispose(); mugMat.dispose();
      scene.traverse((o) => { const sk = o as THREE.SkinnedMesh; if (sk.isSkinnedMesh) sk.skeleton?.dispose(); });
    },
  };
}
