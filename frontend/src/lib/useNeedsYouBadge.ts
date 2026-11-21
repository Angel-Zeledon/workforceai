"use client";
import { useEffect } from "react";
import { useStore } from "./store";
import { useNeedsYou } from "./needsYou";
import { badgeText } from "./needsYouLogic";

const SAFETY_MS = 120_000;

/**
 * The badge of "Te necesitan" in the menu. Live events reload it (store.ts); it is also read when the socket
 * (re)connects and every two minutes while the tab is visible, in case an event was missed.
 */
export function useNeedsYouBadge(): string {
  const count = useNeedsYou((s) => s.count);
  const connected = useStore((s) => s.connected);
  useEffect(() => {
    const load = () => { if (typeof document === "undefined" || document.visibilityState !== "hidden") void useNeedsYou.getState().loadCount(); };
    load();
    const id = setInterval(load, SAFETY_MS);
    return () => clearInterval(id);
  }, [connected]);
  return badgeText(count);
}
