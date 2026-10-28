"use client";
import { create } from "zustand";
import { api } from "./api";
import { useStore } from "./store";
import type { Agent, AgentState } from "./types";
import type { AwayFrame } from "./awayApi";
import { replaySchedule } from "./awayLogic";

/**
 * Timelapse of the absence (B3): replays the agent state changes the server stored (agent.state_changed events)
 * on the office at high speed. It only changes the state and activity of the agents in the local store, the same
 * fields a live event changes, so the 3D office reacts as usual; when it ends (or is stopped) the real roster is
 * loaded again from the server. Nothing is sent to the backend.
 */
interface ReplayState {
  playing: boolean;
  at: string | null;
  index: number;
  total: number;
  start: (frames: AwayFrame[]) => void;
  stop: () => void;
}

let timers: ReturnType<typeof setTimeout>[] = [];
let snapshot: Record<string, Agent> | null = null;

function clearTimers() {
  timers.forEach(clearTimeout);
  timers = [];
}

async function restore() {
  const snap = snapshot;
  snapshot = null;
  try {
    const agents = await api.agents();
    if (agents.length) {
      useStore.setState((s) => ({ agents: { ...s.agents, ...Object.fromEntries(agents.map((a) => [a.id, a])) } }));
      return;
    }
  } catch { /* offline: fall back to the snapshot below */ }
  if (snap) useStore.setState((s) => ({ agents: { ...s.agents, ...snap } }));
}

export const useAwayReplay = create<ReplayState>((set, get) => ({
  playing: false, at: null, index: 0, total: 0,
  start: (frames) => {
    if (!frames.length) return;
    clearTimers();
    if (!snapshot) snapshot = { ...useStore.getState().agents };
    const at = replaySchedule(frames.length);
    set({ playing: true, at: frames[0].at, index: 0, total: frames.length });
    frames.forEach((f, i) => {
      timers.push(setTimeout(() => {
        useStore.setState((s) => {
          const a = s.agents[f.agent_id];
          if (!a) return {};
          return { agents: { ...s.agents, [f.agent_id]: { ...a, state: f.state as AgentState, activity: f.activity ?? a.activity } } };
        });
        set({ at: f.at, index: i + 1 });
      }, at[i]));
    });
    timers.push(setTimeout(() => get().stop(), at[at.length - 1] + 1800));
  },
  stop: () => {
    const was = get().playing;
    clearTimers();
    set({ playing: false, at: null, index: 0, total: 0 });
    if (was) void restore();
  },
}));
