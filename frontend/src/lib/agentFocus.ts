"use client";
import { create } from "zustand";
import { useArtifacts } from "./artifacts";
import { go } from "./navBridge";
import { useStore } from "./store";

/** A request to open an agent's side panel on a given tab (and, for the thought audit, a task to point at). */
export interface AgentFocus { agentId: string; tab: "thoughts"; taskId: string; n: number }

let seq = 0;

export const useAgentFocus = create<{ req: AgentFocus | null; clear: () => void }>((set) => ({
  req: null,
  clear: () => set({ req: null }),
}));

/** Opens the office with the agent's panel on its thought audit, pointing at taskId (B10: the shield links here). */
export function openThoughts(agentId: string, taskId = "") {
  useArtifacts.setState({ deskId: agentId, size: "dock" }); // the audit lives in the docked panel
  useStore.getState().select(agentId);
  useAgentFocus.setState({ req: { agentId, tab: "thoughts", taskId, n: ++seq } });
  if (typeof window !== "undefined" && window.location.pathname !== "/") go("/");
}
