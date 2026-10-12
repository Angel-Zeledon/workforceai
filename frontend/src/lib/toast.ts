import { create } from "zustand";

export type ToastKind = "ok" | "info" | "warn" | "err";
export interface Toast { id: number; kind: ToastKind; text: string }
let seq = 0;

export const useToasts = create<{ items: Toast[]; push: (text: string, kind?: ToastKind, ms?: number) => void; dismiss: (id: number) => void }>((set, get) => ({
  items: [],
  push: (text, kind = "ok", ms = 4000) => {
    const id = ++seq;
    set((s) => ({ items: [...s.items.slice(-3), { id, kind, text }] }));
    setTimeout(() => get().dismiss(id), ms);
  },
  dismiss: (id) => set((s) => ({ items: s.items.filter((t) => t.id !== id) })),
}));

/** Show a short confirmation from anywhere (handlers, stores). */
export const toast = (text: string, kind: ToastKind = "ok") => useToasts.getState().push(text, kind);
