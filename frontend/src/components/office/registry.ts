import * as THREE from "three";
import type { Agent } from "@/lib/types";
import { roleMeta } from "@/lib/meta";

/** Live world positions of each character's head, written by <Character/> every frame and read by links/bubbles. */
export const headPos = new Map<string, THREE.Vector3>();
export const feetPos = new Map<string, THREE.Vector3>();

export const AISLE_Z = -1.3;

export function deskOf(agent: Pick<Agent, "role" | "id">, index: number): [number, number] {
  const m = roleMeta(agent.role);
  if (m.desk[1] === 6) return [-8 + index * 4.5, 6.5];
  return m.desk;
}
// Characters sit on the +z side of their desk, facing -z (toward the monitor).
export const seatOf = (d: [number, number]): [number, number] => [d[0], d[1] + 0.85];
export const standOf = (d: [number, number]): [number, number] => [d[0] + 1.45, d[1] + 0.95];
export const visitorOf = (d: [number, number]): [number, number] => [d[0] + 2.6, d[1] + 0.95];
