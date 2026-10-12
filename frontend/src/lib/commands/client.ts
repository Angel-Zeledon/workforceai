"use client";
// Client side of the slash commands: API calls, shared registry store and the local execution of client-only commands.
import { create } from "zustand";
import { call } from "../api";
import { go } from "../navBridge";
import { useStore } from "../store";
import { STATIC_COMMANDS, type CmdResult, type Command, type Completion, type Lang, type RunBody, type RunResponse, type Suggestion } from "./registry";

const qs = (o: Record<string, string | number | undefined>) => {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(o)) if (v !== undefined && v !== "") p.set(k, String(v));
  const s = p.toString();
  return s ? `?${s}` : "";
};

export const commandsApi = {
  list: async (): Promise<Command[]> => {
    const r = await call<unknown>("GET", "/commands");
    const items = Array.isArray(r) ? r : (r as { items?: unknown[] })?.items;
    return Array.isArray(items) ? (items as Command[]) : [];
  },
  complete: async (kind: string, q: string, limit = 8): Promise<Completion[]> => {
    const r = await call<unknown>("GET", `/commands/complete${qs({ kind, q, limit })}`);
    return Array.isArray(r) ? (r as Completion[]) : [];
  },
  suggest: async (surface: string, conversation_id?: string): Promise<Suggestion[]> => {
    const r = await call<unknown>("GET", `/commands/suggest${qs({ surface, conversation_id })}`);
    return Array.isArray(r) ? (r as Suggestion[]) : [];
  },
  run: (body: RunBody) => call<RunResponse>("POST", "/commands/run", body),
};

interface RegistryState { commands: Command[]; loaded: boolean; load: () => Promise<void> }
/** Static registry first (instant paint, mock mode); the API list replaces it once it arrives. */
export const useRegistry = create<RegistryState>((set, get) => ({
  commands: STATIC_COMMANDS,
  loaded: false,
  load: async () => {
    if (get().loaded) return;
    set({ loaded: true });
    try {
      const list = await commandsApi.list();
      if (list.length) set({ commands: list });
    } catch { set({ loaded: false }); }
  },
}));

let seq = 0;
const lid = (p: string) => `local:${p}:${Date.now().toString(36)}${(seq++).toString(36)}`;

/** Adds the typed command (user bubble) to the local conversation. */
export function echoUser(conv: string, raw: string) {
  useStore.getState().addLocalChat({ id: lid("u"), conversation: conv, turn_id: null, from: "user", to: conv === "office" ? "all" : conv.slice(6), kind: "chat", text: raw, reply_to: null, ts: new Date().toISOString() });
}
/** Adds a system card with the command result. */
export function addCard(conv: string, card: CmdResult & { command?: string; error?: boolean }, text = "") {
  useStore.getState().addLocalChat({ id: lid("c"), conversation: conv, turn_id: null, from: "system", to: "user", kind: "system", text: text || card.title || card.text || "", reply_to: null, ts: new Date().toISOString(), card });
}

/** Applies a backend result to the UI: card in the conversation, navigation, local clear. */
export function applyResult(conv: string, cmd: Command, res: RunResponse, fallbackErr: string) {
  if (res.status === "error" || !res.result) {
    addCard(conv, { kind: "message", text: res.error?.message || fallbackErr, command: cmd.id, error: true });
    return;
  }
  const r = res.result;
  const data = r.data as { clear?: boolean } | undefined;
  if (cmd.id === "clear" && data?.clear) useStore.setState((s) => ({ chat: { ...s.chat, [conv]: [] } }));
  addCard(conv, { ...r, command: cmd.id });
  if (r.kind === "navigate" && r.href) go(r.href);
}

export function confirmText(res: RunResponse, lang: Lang): string {
  const m = res.confirm?.message;
  if (!m) return "";
  return typeof m === "string" ? m : m[lang] || m.es || m.en || "";
}

/** Client-only commands run here (no round trip). Returns true when handled. */
export function runLocal(cmd: Command, lang: Lang, value: string): { handled: boolean; help?: boolean } {
  switch (cmd.id) {
    case "help": return { handled: true, help: true };
    case "shop": go("/shop"); return { handled: true };
    case "inventory": go("/shop?tab=inventory"); return { handled: true };
    case "file": go("/files"); return { handled: true };
    case "open": go(value && value.startsWith("/") ? `/files?path=${encodeURIComponent(value)}` : "/files"); return { handled: true };
    default: return { handled: false };
  }
}
