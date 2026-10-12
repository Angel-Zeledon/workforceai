"use client";
import { create } from "zustand";
import type { TourDefinition } from "./types";
import { firstStep, nextShown } from "./anchors";
import { TOURS } from "./tours";

interface TourState {
  activeId: string | null;
  stepIndex: number;
  start: (id: string) => void;
  next: () => void;
  back: () => void;
  stop: () => void;
}

const find = (id: string | null): TourDefinition | null => (id ? TOURS.find((t) => t.id === id) ?? null : null);

export const useTour = create<TourState>((set, get) => ({
  activeId: null,
  stepIndex: 0,
  start: (id) => { const def = find(id); if (def) set({ activeId: id, stepIndex: firstStep(def) }); },
  next: () => {
    const { activeId, stepIndex } = get();
    const def = find(activeId);
    if (!def) return;
    const j = nextShown(def, stepIndex + 1, 1);
    if (j < 0) set({ activeId: null, stepIndex: 0 });
    else set({ stepIndex: j });
  },
  back: () => {
    const { activeId, stepIndex } = get();
    const def = find(activeId);
    if (!def) return;
    const j = nextShown(def, stepIndex - 1, -1);
    if (j >= 0) set({ stepIndex: j });
  },
  stop: () => set({ activeId: null, stepIndex: 0 }),
}));

export const activeTour = () => find(useTour.getState().activeId);
