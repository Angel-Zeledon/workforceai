"use client";
import { useCallback } from "react";
import { labelEntries, type LabelEntry } from "./labels";

/** Ref callback that (un)registers a DOM label with the screen-space solver (LabelSolver). */
export function useRegister(key: string, kind: LabelEntry["kind"], anchor: string) {
  return useCallback((el: HTMLElement | null) => {
    if (el) labelEntries.set(key, { el, kind, anchor });
    else labelEntries.delete(key);
  }, [key, kind, anchor]);
}
