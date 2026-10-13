import * as THREE from "three";
import type { Agent } from "@/lib/types";
import { assignDesks, roleMeta } from "@/lib/meta";
import { useStore } from "@/lib/store";
import { effective, useOfficeLayout } from "@/lib/officeLayout";

/** Live world positions of each character's head, written by <Character/> every frame and read by links/bubbles. */
export const headPos = new Map<string, THREE.Vector3>();
export const feetPos = new Map<string, THREE.Vector3>();

export const AISLE_Z = -1.3;

export function deskOf(agent: Pick<Agent, "role" | "id">, index: number): [number, number] {
  const S = useStore.getState();
  const list = S.agentOrder.map((id) => S.agents[id]).filter(Boolean).map((a) => ({ id: a.id, role: a.role }));
  if (!list.some((a) => a.id === agent.id)) list.push({ id: agent.id, role: agent.role });
  const home = assignDesks(list)[agent.id] ?? roleMeta(agent.role).desk;
  // a desk the organization moved in the office editor (draft while editing, saved otherwise)
  const moved = effective(useOfficeLayout.getState()).desks[agent.id];
  return moved && moved.x !== undefined && moved.z !== undefined ? [moved.x, moved.z] : home;
}
// Characters sit on the +z side of their desk, facing -z (toward the monitor).
export const seatOf = (d: [number, number]): [number, number] => [d[0], d[1] + 0.85];
export const standOf = (d: [number, number]): [number, number] => [d[0] + 1.45, d[1] + 0.95];
export const visitorOf = (d: [number, number]): [number, number] => [d[0] + 2.6, d[1] + 0.95];
