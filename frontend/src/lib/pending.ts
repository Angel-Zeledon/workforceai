"use client";
import { useStore } from "./store";
import { pendingTotal } from "./inboxLogic";

/** The one Pendientes number (approvals waiting + open questions), from the one store, for every screen. */
export const usePendingCount = () => useStore((s) => pendingTotal(s.approvals, s.questions));
