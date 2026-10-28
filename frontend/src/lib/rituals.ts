import { create } from "zustand";
import type { WsFrame } from "./types";
import { gatheringFrom, type Gathering } from "./ritualsLogic";

/**
 * Office side of the rituals (B4): a ritual.held frame sends its participants to the meeting room for a while
 * (officeSignals reads `gathering`). `seq` changes on every held ritual so open ritual views can refresh.
 */
interface RState {
  gathering: Gathering | null;
  seq: number;
  applyFrame: (f: WsFrame) => void;
}

let timer: ReturnType<typeof setTimeout> | null = null;

export const useRituals = create<RState>((set) => ({
  gathering: null,
  seq: 0,
  applyFrame: (f) => {
    if (f.type !== "ritual.held") return;
    const g = gatheringFrom(f.payload, Date.now());
    set((s) => ({ gathering: g ?? s.gathering, seq: s.seq + 1 }));
    if (!g) return;
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => { timer = null; set({ gathering: null }); }, Math.max(0, g.until - Date.now()));
  },
}));
