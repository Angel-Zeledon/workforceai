"use client";
import { useMemo } from "react";
import { create } from "zustand";
import { roleMeta, type RoleMeta } from "./meta";
import { detectLocale, setCurrentLocale, trOr, type Locale } from "./i18n-core";
import { useStore } from "./store";
import type { Agent } from "./types";

export type Lighting = "ambient" | "day" | "night";
export type LabelSize = "s" | "m" | "l";
export type HairStyle = RoleMeta["hairStyle"];
export type Accessory = RoleMeta["accessory"];

/** Per-employee customization. Everything is optional: missing values fall back to the role / backend defaults. */
export interface AgentPrefs {
  name?: string; nickname?: string; title?: string;
  color?: string; skin?: string; hairStyle?: HairStyle; accessory?: Accessory;
}
export interface Prefs {
  version: 1;
  locale: Locale;
  lighting: Lighting;
  showNames: boolean;
  labelSize: LabelSize;
  reduceMotion: boolean;
  agents: Record<string, AgentPrefs>;
}

/**
 * Persistence layer. Today: localStorage. To move it to a per-user backend, implement this
 * interface (e.g. GET/PUT /api/v1/users/me/preferences) and swap it in with setPreferencesBackend().
 */
export interface PreferencesBackend {
  load(): Prefs | null;
  save(p: Prefs): void;
}

const KEY = "aiwos.preferences.v1";
export const DEFAULT_PREFS: Prefs = { version: 1, locale: "es", lighting: "ambient", showNames: true, labelSize: "m", reduceMotion: false, agents: {} };

export const localBackend: PreferencesBackend = {
  load() {
    try {
      const raw = window.localStorage.getItem(KEY);
      if (!raw) return null;
      const p = JSON.parse(raw) as Partial<Prefs>;
      return { ...DEFAULT_PREFS, ...p, agents: { ...(p.agents || {}) }, version: 1 };
    } catch { return null; }
  },
  save(p) {
    try { window.localStorage.setItem(KEY, JSON.stringify(p)); } catch { /* private mode / quota exceeded */ }
  },
};
let backend: PreferencesBackend = localBackend;
export const setPreferencesBackend = (b: PreferencesBackend) => { backend = b; };

export const PASTEL_COLORS = ["#f4895a", "#f7b267", "#e8b53a", "#8fd16a", "#4cb987", "#45b8cc", "#5a95e8", "#8b8fe8", "#9577d6", "#ee7aa8", "#f08a8a", "#b08968"];
export const SKIN_TONES = ["#f6d0b0", "#f2c09a", "#e0ac84", "#c98f68", "#a8734f", "#7d5236"];
export const HAIR_STYLES: { v: HairStyle; label: string }[] = [
  { v: "short", label: "Corto" }, { v: "long", label: "Largo" }, { v: "bob", label: "Bob" },
  { v: "bun", label: "Moño" }, { v: "ponytail", label: "Cola" }, { v: "bald", label: "Sin pelo" },
];
export const ACCESSORIES: { v: Accessory; label: string }[] = [
  { v: "none", label: "Ninguno" }, { v: "glasses", label: "Gafas" }, { v: "roundglasses", label: "Gafas redondas" },
  { v: "headphones", label: "Audífonos" }, { v: "headset", label: "Diadema" }, { v: "hardhat", label: "Casco" },
  { v: "beanie", label: "Gorro" }, { v: "lanyard", label: "Credencial" },
];
export const NICK_MAX = 10;
export const NAME_MAX = 32;

interface PrefState {
  prefs: Prefs;
  hydrated: boolean;
  hydrate: () => void;
  set: (patch: Partial<Omit<Prefs, "agents" | "version">>) => void;
  setAgent: (id: string, patch: AgentPrefs) => void;
  resetAgent: (id: string) => void;
}

export const usePreferences = create<PrefState>((set, get) => {
  const commit = (prefs: Prefs) => { set({ prefs }); backend.save(prefs); };
  return {
    prefs: DEFAULT_PREFS,
    hydrated: false,
    hydrate: () => {
      if (get().hydrated) return;
      const saved = backend.load();
      const prefs = saved ?? { ...DEFAULT_PREFS, locale: detectLocale() };
      setCurrentLocale(prefs.locale);
      set({ prefs, hydrated: true });
    },
    set: (patch) => {
      if (patch.locale) setCurrentLocale(patch.locale);
      commit({ ...get().prefs, ...patch });
    },
    setAgent: (id, patch) => {
      const cur = { ...(get().prefs.agents[id] || {}), ...patch } as Record<string, unknown>;
      for (const k of Object.keys(cur)) if (cur[k] === undefined || cur[k] === "") delete cur[k]; // empty value = back to the default
      commit({ ...get().prefs, agents: { ...get().prefs.agents, [id]: cur as AgentPrefs } });
    },
    resetAgent: (id) => {
      const agents = { ...get().prefs.agents }; delete agents[id];
      commit({ ...get().prefs, agents });
    },
  };
});

export interface AgentView {
  id: string; name: string; label: string; title: string; color: string; skin: string; hair: string;
  hairStyle: HairStyle; accessory: Accessory; female: boolean; tie: boolean; role: string; customized: boolean;
}
const firstWord = (s: string) => s.trim().split(/\s+/)[0] || s;

/** Merges the backend agent with local preferences. `label` is the short nickname used on 3D tags. */
export function resolveAgent(agent: Pick<Agent, "id" | "name" | "title" | "role"> | undefined, ap?: AgentPrefs): AgentView {
  const m = roleMeta(agent?.role);
  const name = (ap?.name?.trim() || agent?.name || agent?.id || "").slice(0, NAME_MAX);
  const label = (ap?.nickname?.trim() || firstWord(name)).slice(0, NICK_MAX);
  return {
    id: agent?.id ?? "", role: agent?.role ?? "", name, label,
    title: ap?.title?.trim() || (agent ? trOr(`agent.title.${agent.id}`, agent.title) : ""),
    color: ap?.color || m.color, skin: ap?.skin || m.skin, hair: m.hair,
    hairStyle: ap?.hairStyle || m.hairStyle, accessory: ap?.accessory || m.accessory,
    female: m.female, tie: m.tie, customized: !!ap && Object.keys(ap).length > 0,
  };
}

export function useAgentView(id: string): AgentView {
  const agent = useStore((s) => s.agents[id]);
  const ap = usePreferences((s) => s.prefs.agents[id]);
  const name = agent?.name, title = agent?.title, role = agent?.role;
  // eslint-disable-next-line react-hooks/exhaustive-deps
  return useMemo(() => resolveAgent(agent ? { id, name: name!, title: title!, role: role! } : undefined, ap), [id, name, title, role, ap, !!agent]);
}
/** Non-reactive variant for useFrame / imperative code. */
export function getAgentView(id: string): AgentView {
  return resolveAgent(useStore.getState().agents[id], usePreferences.getState().prefs.agents[id]);
}
