// Loads and owns the glTF models of the "Estudio" office (Quaternius people, Kenney furniture, both CC0, see
// public/office-lab/CREDITS.md). One load per open office, shared by every placement; when the last user lets go the
// geometries, materials and textures are disposed (WebGL memory: Chrome froze before on heavy scenes).
import * as THREE from "three";
import { GLTFLoader } from "three/examples/jsm/loaders/GLTFLoader.js";
import type { Model } from "./studioLogic";

const BASE = "/office-lab";
export const FURNITURE = [
  "bookcaseOpen", "chair", "chairDesk", "coatRackStanding", "computerKeyboard", "computerMouse", "computerScreen", "desk",
  "lampRoundFloor", "lampSquareTable", "laptop", "loungeSofa", "pottedPlant", "rugRectangle", "tableRound", "trashcan",
] as const;
export type FurnitureName = (typeof FURNITURE)[number];
const PEOPLE: Record<Model, string> = { suit: "Suit_Male", casual: "Casual2_Female", classy: "OldClassy_Female" };

export interface PersonModel { scene: THREE.Object3D; animations: THREE.AnimationClip[] }
export interface Assets { furniture: Record<FurnitureName, THREE.Object3D>; people: Record<Model, PersonModel> }

let current: Promise<Assets> | null = null;
let loaded: Assets | null = null;
let users = 0;

async function load(): Promise<Assets> {
  const loader = new GLTFLoader();
  const get = (url: string) => loader.loadAsync(url);
  const [furniture, people] = await Promise.all([
    Promise.all(FURNITURE.map((n) => get(`${BASE}/kenney-furniture/${n}.glb`).then((g) => [n, g.scene] as const))),
    Promise.all((Object.keys(PEOPLE) as Model[]).map((m) => get(`${BASE}/quaternius-characters/${PEOPLE[m]}.glb`).then((g) => [m, { scene: g.scene, animations: g.animations }] as const))),
  ]);
  return { furniture: Object.fromEntries(furniture) as unknown as Assets["furniture"], people: Object.fromEntries(people) as unknown as Assets["people"] };
}

function disposeTree(root: THREE.Object3D) {
  root.traverse((o) => {
    const m = o as THREE.Mesh;
    if (!m.isMesh) return;
    m.geometry?.dispose();
    for (const mat of Array.isArray(m.material) ? m.material : [m.material]) disposeMaterial(mat);
  });
}

export function disposeMaterial(mat: THREE.Material | undefined) {
  if (!mat) return;
  for (const v of Object.values(mat)) if (v && (v as THREE.Texture).isTexture) (v as THREE.Texture).dispose();
  mat.dispose();
}

/** Takes the models (loading them the first time). Pair every call with `releaseAssets`. */
export function acquireAssets(): Promise<Assets> {
  users++;
  if (!current) {
    current = load().then((a) => {
      if (users === 0) { disposeAssets(a); current = null; } else loaded = a; // everyone left while it was loading
      return a;
    }, (e) => { current = null; throw e; });
  }
  return current;
}

/** Lets go of the models; the last one out disposes them. */
export function releaseAssets() {
  users = Math.max(0, users - 1);
  if (users > 0) return;
  const a = loaded;
  if (!a) return; // still loading: the load itself disposes when it finds nobody waiting (and a quick re-open reuses it)
  current = null; loaded = null;
  disposeAssets(a);
}

function disposeAssets(a: Assets) {
  for (const f of Object.values(a.furniture)) disposeTree(f);
  for (const p of Object.values(a.people)) disposeTree(p.scene);
}
