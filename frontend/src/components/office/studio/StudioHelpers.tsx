"use client";
// The temporary helpers in the "Estudio" style: the glTF people that are already loaded (the same rig as the agents and the connected
// people), dressed in neutral grey. Where they stand, the hover tag and the click are HelperLayer's own; this only draws the body.
import { useEffect, useMemo } from "react";
import { useFrame } from "@react-three/fiber";
import { usePreferences } from "@/lib/preferences";
import type { Assets } from "./studioAssets";
import { buildRig } from "./rig";
import { ADD_CLIPS, type Add, type Model, type Outfit } from "./studioLogic";
import { HELPER_GREY, HELPER_HAIRS, HELPER_SKINS, type FigState } from "../helperFigures";
import type { HelperBody } from "../HelperLayer";

const NO_ADD = Object.fromEntries(ADD_CLIPS.map((k) => [k, 0])) as Record<Add, number>;
const MODELS: Model[] = ["suit", "casual", "classy"];

function Body({ a, seed, state }: { a: Assets; seed: number; state: FigState }) {
  const rig = useMemo(() => {
    const model = MODELS[seed % MODELS.length];
    const o: Outfit = { model, shirt: HELPER_GREY.shirt, jacket: HELPER_GREY.jacket, knit: HELPER_GREY.knit, pants: HELPER_GREY.pants, belt: HELPER_GREY.belt, height: 0.96 + ((seed >>> 7) % 9) / 100 };
    return buildRig(a.people[model], model, o, HELPER_SKINS[(seed >>> 2) % HELPER_SKINS.length], HELPER_HAIRS[(seed >>> 5) % HELPER_HAIRS.length]);
  }, [a, seed]);
  useEffect(() => () => rig.dispose(), [rig]);
  useFrame((_, delta) => {
    const reduce = usePreferences.getState().prefs.reduceMotion;
    // working: the head goes down to the work ("look"); waiting or failed: stands
    rig.update(Math.min(0.1, delta), "Idle", 0, 0, state === "working" ? { ...NO_ADD, look: 1 } : NO_ADD, false, reduce);
  });
  return <primitive object={rig.root} />;
}

/** The body renderer HelperLayer takes in this style. */
export function helperBody(a: Assets): HelperBody {
  return function StudioHelperBody({ id, state, seed }) { return <Body key={id} a={a} seed={seed} state={state} />; };
}
