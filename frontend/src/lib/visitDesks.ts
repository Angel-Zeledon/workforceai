import { assignDesks } from "./meta";
import { useStore } from "./store";
import { effective, useOfficeLayout } from "./officeLayout";
import { useCurrentOfficeStore } from "./currentOffice";
import { ALL_OFFICES, resolveCurrent, visitorIds, visitSpots } from "./currentOfficeLogic";

/** Visiting desks (spec §5): an agent whose base is another office and who works for the one on screen sits at a free
 *  place of the layout while it does. `deskOf` (office/registry.ts) asks here, so the 3D, Plano and Estudio agree.
 *  Zero cost when no agent is visiting: one check of the store, nothing else. */

/** Free places of the floor where a visiting table fits (the same rows the default desks use, then the right side). */
const CANDIDATES = [-12.5, -8, -3.5, 1, 5.5, 10].flatMap((x) => [{ x, z: 6.5 }]).concat([{ x: 10, z: 1.5 }, { x: -12.5, z: 1.5 }, { x: -12.5, z: -4.5 }]);
const GAP = 3.2;

let memo: { key: unknown[]; spots: Map<string, [number, number]> } | null = null;

function spotsNow(): Map<string, [number, number]> | null {
  const cs = useCurrentOfficeStore.getState();
  const current = resolveCurrent(cs.stored, cs.offices);
  if (current === ALL_OFFICES) return null;
  const S = useStore.getState();
  const layout = effective(useOfficeLayout.getState());
  const key = [current, cs.alsoIn, cs.bases, S.agentOrder, layout];
  if (memo && memo.key.every((k, i) => k === key[i])) return memo.spots;
  const visitors = visitorIds(S.agentOrder, current, (id) => cs.bases[id], cs.alsoIn);
  const spots = new Map<string, [number, number]>();
  if (visitors.length) {
    const own = S.agentOrder.filter((id) => !visitors.includes(id) && S.agents[id]);
    const home = assignDesks(own.map((id) => ({ id, role: S.agents[id].role })));
    const used = own.map((id) => { const m = layout.desks[id]; return m && m.x !== undefined && m.z !== undefined ? { x: m.x, z: m.z } : { x: home[id][0], z: home[id][1] }; })
      .concat(layout.pieces.map((p) => ({ x: p.x, z: p.z })));
    const free = visitSpots(CANDIDATES, used, visitors.length, GAP);
    visitors.forEach((id, i) => { const f = free[i]; if (f) spots.set(id, [f.x, f.z]); });
  }
  memo = { key, spots };
  return spots;
}

/** Where the agent sits while it visits the office on screen, or null (its own desk). */
export function visitDeskOf(id: string): [number, number] | null {
  const cs = useCurrentOfficeStore.getState();
  if (cs.stored === ALL_OFFICES || !Object.keys(cs.alsoIn).length) return null;
  return spotsNow()?.get(id) ?? null;
}

/** Same, as a stable string for React dependencies ("" = not visiting). */
export function useVisitKey(id: string): string {
  return useCurrentOfficeStore(() => { const v = visitDeskOf(id); return v ? `${v[0]},${v[1]}` : ""; });
}
