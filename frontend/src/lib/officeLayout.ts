"use client";
import { create } from "zustand";
import { call } from "./api";
import { useRewards } from "./rewards";
import { DEFAULT_LAYOUT, LEGACY_SLOTS, newUid, normalizeLayout, sameLayout, type FixtureId, type Layout, type Piece, type ZoneName } from "./officeLayoutModel";

export type EditorTab = "ambience" | "furniture" | "desks";
export type Selection = { kind: "piece"; uid: string } | { kind: "desk"; id: string } | { kind: "zone"; id: ZoneName } | { kind: "fixture"; id: FixtureId } | null;

interface LayoutDoc { layout: unknown; version: number }

interface State {
  /** The saved layout every viewer sees. */
  layout: Layout;
  version: number;
  loaded: boolean;
  /** Edit mode: `draft` is the working copy, nothing reaches other viewers until Save. */
  editing: boolean;
  draft: Layout | null;
  past: Layout[];
  future: Layout[];
  selection: Selection;
  /** Item id being placed (the ghost follows the pointer until it is dropped). */
  placing: string | null;
  dragging: boolean;
  /** A desk being dragged: where it would land (drawn by the desk itself; the characters only move when it is dropped). */
  deskGhost: { id: string; x: number; z: number; valid: boolean } | null;
  /** A room being dragged: where it would land (the characters only move when it is dropped). */
  zoneGhost: { id: ZoneName; x: number; z: number; valid: boolean } | null;
  saving: boolean;
  error: "conflict" | "forbidden" | "invalid" | "network" | null;
  tab: EditorTab;
  grid: boolean;

  load: () => Promise<void>;
  startEdit: () => void;
  cancel: () => void;
  save: () => Promise<boolean>;
  /** Applies a change to the draft. `coalesce` merges rapid changes of one control (sliders, color pickers) into one undo step. */
  mutate: (fn: (d: Layout) => Layout, coalesce?: string) => void;
  undo: () => void;
  redo: () => void;
  select: (s: Selection) => void;
  startPlacing: (item: string | null) => void;
  setDragging: (v: boolean) => void;
  setTab: (t: EditorTab) => void;
  toggleGrid: () => void;
  clearError: () => void;
  /** Changes the room's light from the HUD (outside edit mode) and saves it; edits made meanwhile in an open editor are untouched. */
  setLight: (patch: Partial<Layout["light"]>) => void;
}

const HISTORY = 80;
let lastCoalesce = { key: "", at: 0 };

/** The layout the 3D office draws: the draft while editing, the saved one otherwise. */
export const effective = (s: Pick<State, "draft" | "layout">): Layout => s.draft ?? s.layout;

/** Copies the items placed in the 12 legacy slots into free pieces at their fixed spots (the look does not change). */
function seeded(l: Layout): Layout {
  if (l.seeded) return l;
  const placed = useRewards.getState().office;
  const pieces = [...l.pieces];
  for (const [slot, item] of Object.entries(placed)) {
    const spot = LEGACY_SLOTS[slot];
    if (!spot || slot === "window_1" || pieces.some((p) => p.item === item)) continue;
    pieces.push({ uid: newUid(), item, x: spot.pos[0], z: spot.pos[2], rot: spot.rot ?? 0, s: 1, ...(spot.pos[1] > 0 ? { y: spot.pos[1] } : {}) });
  }
  return { ...l, pieces, seeded: true };
}

const errorOf = (e: unknown): State["error"] => {
  const m = /->\s*(\d+)/.exec(String((e as Error)?.message));
  const code = m ? Number(m[1]) : 0;
  return code === 409 ? "conflict" : code === 403 ? "forbidden" : code === 400 ? "invalid" : "network";
};

export const useOfficeLayout = create<State>((set, get) => ({
  layout: DEFAULT_LAYOUT, version: 0, loaded: false,
  editing: false, draft: null, past: [], future: [], selection: null, placing: null, dragging: false, deskGhost: null, zoneGhost: null, saving: false, error: null,
  tab: "furniture", grid: true,

  load: async () => {
    try {
      const doc = await call<LayoutDoc>("GET", "/office/layout");
      // the editing user keeps their draft: the conflict check at Save time covers a stale base
      set({ loaded: true, layout: normalizeLayout(doc?.layout), version: Number(doc?.version) || 0 });
    } catch {
      set({ loaded: true }); // an older backend without the editor: the built-in look stays
    }
  },

  startEdit: () => {
    const s = get();
    if (s.editing) return;
    set({ editing: true, draft: seeded(s.layout), past: [], future: [], selection: null, placing: null, error: null, tab: "furniture" });
  },
  cancel: () => set({ editing: false, draft: null, past: [], future: [], selection: null, placing: null, dragging: false, deskGhost: null, zoneGhost: null, error: null }),

  save: async () => {
    const s = get();
    if (!s.draft || s.saving) return false;
    if (sameLayout(s.draft, s.layout)) { get().cancel(); return true; }
    set({ saving: true, error: null });
    try {
      const doc = await call<LayoutDoc>("PUT", "/office/layout", { layout: s.draft, base_version: s.version });
      set({
        saving: false, editing: false, draft: null, past: [], future: [], selection: null, placing: null, dragging: false, deskGhost: null, zoneGhost: null,
        layout: normalizeLayout(doc?.layout ?? s.draft), version: Number(doc?.version) || s.version + 1,
      });
      return true;
    } catch (e) {
      set({ saving: false, error: errorOf(e) });
      return false;
    }
  },

  mutate: (fn, coalesce) => {
    const s = get();
    if (!s.draft) return;
    const next = fn(s.draft);
    if (next === s.draft) return;
    const now = Date.now();
    const merge = !!coalesce && lastCoalesce.key === coalesce && now - lastCoalesce.at < 900;
    lastCoalesce = { key: coalesce || "", at: now };
    set({ draft: next, past: merge ? s.past : [...s.past, s.draft].slice(-HISTORY), future: [] });
  },
  undo: () => {
    const s = get();
    if (!s.draft || !s.past.length) return;
    lastCoalesce = { key: "", at: 0 };
    set({ draft: s.past[s.past.length - 1], past: s.past.slice(0, -1), future: [s.draft, ...s.future].slice(0, HISTORY), selection: null });
  },
  redo: () => {
    const s = get();
    if (!s.draft || !s.future.length) return;
    lastCoalesce = { key: "", at: 0 };
    set({ draft: s.future[0], future: s.future.slice(1), past: [...s.past, s.draft].slice(-HISTORY), selection: null });
  },

  select: (selection) => set({ selection, placing: null }),
  startPlacing: (placing) => set({ placing, selection: null }),
  setDragging: (dragging) => set({ dragging }),
  setTab: (tab) => set({ tab, placing: null }),
  toggleGrid: () => set((s) => ({ grid: !s.grid })),
  clearError: () => set({ error: null }),

  setLight: (patch) => {
    const s = get();
    if (s.editing) { s.mutate((l) => ({ ...l, light: { ...l.light, ...patch } }), "light"); return; }
    const next = { ...s.layout, light: { ...s.layout.light, ...patch } };
    set({ layout: next }); // instant feedback; the save below is debounced
    lightPending = patch ? { ...(lightPending || {}), ...patch } : lightPending;
    if (lightTimer) clearTimeout(lightTimer);
    lightTimer = setTimeout(() => { void flushLight(); }, 600);
  },
}));

let lightPending: Partial<Layout["light"]> | null = null;
let lightTimer: ReturnType<typeof setTimeout> | null = null;
async function flushLight(retry = true) {
  const patch = lightPending;
  if (!patch) return;
  lightPending = null;
  const s = useOfficeLayout.getState();
  const doc = { ...s.layout, light: { ...s.layout.light, ...patch } };
  try {
    const res = await call<LayoutDoc>("PUT", "/office/layout", { layout: doc, base_version: s.version });
    useOfficeLayout.setState({ layout: normalizeLayout(res?.layout ?? doc), version: Number(res?.version) || s.version + 1 });
  } catch (e) {
    if (retry && errorOf(e) === "conflict") { // someone saved meanwhile: take their version and apply the light on top
      await useOfficeLayout.getState().load();
      lightPending = patch;
      await flushLight(false);
    }
  }
}

// ---- small draft helpers used by the editor ----
export const pieceOf = (l: Layout, uid: string): Piece | undefined => l.pieces.find((p) => p.uid === uid);
export const withPiece = (l: Layout, uid: string, patch: Partial<Piece>): Layout => ({ ...l, pieces: l.pieces.map((p) => (p.uid === uid ? { ...p, ...patch } : p)) });
export const withoutPiece = (l: Layout, uid: string): Layout => ({ ...l, pieces: l.pieces.filter((p) => p.uid !== uid) });

/** Only the saved-layout frame matters to other viewers: reload it (the editing user keeps their draft). */
export function applyLayoutFrame(type: string) {
  if (type === "office.layout_updated") void useOfficeLayout.getState().load();
}
