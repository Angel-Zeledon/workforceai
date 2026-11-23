"use client";
// The people connected to the office, in the "Estudio" style: the same glTF figures as the agents (rig.ts), with the Idle and Walk
// clips, instead of the capsules of the other 3D styles. Where they stand, their name tag and the interaction are PeopleLayer's own;
// this only draws the body. Clothes are picked by a hash of the user id, like the agents' (studioLogic.outfitFor).
import { useEffect, useMemo } from "react";
import { useFrame } from "@react-three/fiber";
import { usePreferences } from "@/lib/preferences";
import { WALK_SPEED } from "../gestures";
import type { Assets } from "./studioAssets";
import { buildRig } from "./rig";
import { ADD_CLIPS, hashId, outfitFor, type Add } from "./studioLogic";
import type { BodyRender } from "../../presence/PeopleLayer";

/** Skin and hair tones people have (the rig falls back to a neutral skin for anything outside the human band). */
const SKINS = ["#e8c4a0", "#d9a77c", "#c68b63", "#f0cfb0", "#b9805a"];
const HAIRS = ["#2b2118", "#4a3020", "#6b4a2c", "#1d1d22", "#8a6a44"];
const NO_ADD = Object.fromEntries(ADD_CLIPS.map((k) => [k, 0])) as Record<Add, number>;

function Body({ a, id, moving }: { a: Assets; id: string; moving: { current: boolean } }) {
  const h = hashId(id);
  const rig = useMemo(() => {
    const o = outfitFor(id, h % 3 === 0);
    return buildRig(a.people[o.model], o.model, o, SKINS[h % SKINS.length], HAIRS[(h >>> 4) % HAIRS.length]);
  }, [a, id, h]);
  useEffect(() => () => rig.dispose(), [rig]);
  useFrame((_, delta) => {
    const reduce = usePreferences.getState().prefs.reduceMotion;
    const walking = moving.current;
    rig.update(Math.min(0.1, delta), walking ? "Walk" : "Idle", 0, walking ? 3.2 / WALK_SPEED : 0, NO_ADD, false, reduce);
  });
  return <primitive object={rig.root} />;
}

/** The body renderer PeopleLayer takes: one glTF person per user. */
export function connectedBody(a: Assets): BodyRender {
  return function ConnectedBody({ id, moving }) { return <Body key={id} a={a} id={id} moving={moving} />; };
}
