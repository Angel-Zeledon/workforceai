"use client";
// Demo mode only (NEXT_PUBLIC_MOCK=true): exposes the stores on window.__aiw so a test browser can drive real agent
// states (frames shaped exactly like the ones the mock backend emits) and change / save / reload the office style.
// Nothing is exposed in a real build.
import { MOCK } from "@/lib/config";
import { useStore } from "@/lib/store";
import { useOfficeLayout } from "@/lib/officeLayout";
import { useCost } from "@/lib/cost";
import { useRewards } from "@/lib/rewards";
import { usePresence } from "../presence/store";
import { deskOf, feetPos } from "./registry";

declare global {
  interface Window {
    __aiw?: {
      store: typeof useStore; layout: typeof useOfficeLayout; cost: typeof useCost; rewards: typeof useRewards; presence: typeof usePresence; feet: typeof feetPos; desk: typeof deskOf;
      frame: (type: string, payload: Record<string, unknown>, agentId?: string) => void;
    };
  }
}

let n = 0;
if (MOCK && typeof window !== "undefined") {
  window.__aiw = {
    store: useStore,
    layout: useOfficeLayout,
    cost: useCost,
    rewards: useRewards,
    presence: usePresence,
    feet: feetPos,
    desk: deskOf,
    frame: (type, payload, agentId) =>
      useStore.getState().apply({ id: `dbg_${++n}`, type, ts: new Date().toISOString(), agent_id: agentId, payload }),
  };
}
