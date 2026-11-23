import { useStore } from "@/lib/store";
import { detectHandoffs, type Handoff } from "./officeState";

const NON_AGENT = new Set(["user", "all", "system", ""]);

/**
 * The real events that make a folder travel between two agents: a task finishing whose output another agent's task
 * needs, and a delegation / consult link between two agents. Shared by the 3D office (Folders) and the flat one.
 * Returns the unsubscribe function.
 */
export function watchHandoffs(push: (h: Handoff) => void): () => void {
  let prev = detectHandoffs(null, useStore.getState().tasks).next;
  let linkTs = Date.now();
  return useStore.subscribe((s, p) => {
    if (s.tasks !== p.tasks) {
      const r = detectHandoffs(prev, s.tasks as never);
      prev = r.next;
      r.handoffs.forEach(push);
    }
    if (s.links !== p.links) {
      for (let i = s.links.length - 1; i >= 0 && s.links[i].ts > linkTs; i--) {
        const l = s.links[i];
        if ((l.kind === "delegation" || l.kind === "consult") && !l.chat && !NON_AGENT.has(l.from) && !NON_AGENT.has(l.to)) push({ from: l.from, to: l.to });
      }
      if (s.links.length) linkTs = Math.max(linkTs, s.links[s.links.length - 1].ts);
    }
  });
}
