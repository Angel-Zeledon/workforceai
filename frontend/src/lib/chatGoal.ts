"use client";
import { create } from "zustand";

/** The company goal the next chat message contributes to (F1). "" = none: POST /messages then carries no goal_id at all. */
export const useChatGoal = create<{ goalId: string; set: (id: string) => void }>((set) => ({
  goalId: "",
  set: (goalId) => set({ goalId }),
}));
