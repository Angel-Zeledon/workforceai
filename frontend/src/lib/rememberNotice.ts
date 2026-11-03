import { create } from "zustand";
import type { RememberResult } from "./permissionsApi";

/** The last "approve and do not ask again", shown in Pendientes with a summary and Undo until dismissed. */
export const useRememberNotice = create<{ last: (RememberResult & { agentName: string }) | null; set: (r: (RememberResult & { agentName: string }) | null) => void }>((set) => ({
  last: null,
  set: (r) => set({ last: r }),
}));
