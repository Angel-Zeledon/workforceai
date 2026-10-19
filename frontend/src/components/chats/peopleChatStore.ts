"use client";
import { create } from "zustand";
import { call } from "@/lib/api";
import { MOCK } from "@/lib/config";
import {
  applyIncoming, applyRead, fromChannels, totalMentions, totalUnread, type Counters, type Mentionable, type PMessage,
} from "./peopleLogic";

export type ChannelKind = "org" | "department" | "project" | "group" | "dm";
export interface PMember { kind: "user" | "agent"; id: string; role: string; last_read_seq: number }
export interface PChannel {
  id: string; kind: ChannelKind; name: string; scope_key?: string; created_by: string; last_seq: number; last_message_at?: string;
  archived: boolean; members: PMember[]; unread: number; mentions_unread: number; preview?: string;
}
export interface Directory { people: { id: string; name: string; role: string }[]; agents: { id: string; name: string }[]; self: string }

interface State {
  channels: PChannel[];
  loaded: boolean;
  counters: Counters;
  messages: Record<string, PMessage[]>;
  more: Record<string, boolean>;
  dir: Directory;
  activeId: string | null;
  error: string | null;
  loadChannels: () => Promise<void>;
  loadDirectory: () => Promise<void>;
  open: (id: string | null) => Promise<void>;
  loadOlder: (id: string) => Promise<void>;
  send: (id: string, text: string) => Promise<void>;
  openDM: (userId: string) => Promise<string | null>;
  create: (b: { kind: "group" | "department" | "project"; name: string; scope_key?: string; members: { kind: "user" | "agent"; id: string }[] }) => Promise<string | null>;
  addMember: (id: string, m: { kind: "user" | "agent"; id: string }) => Promise<void>;
  removeMember: (id: string, kind: string, mid: string) => Promise<void>;
  markRead: (id: string) => void;
  apply: (type: string, payload: any) => void;
}

const EMPTY_DIR: Directory = { people: [], agents: [], self: "" };
const PAGE = 50;
const focused = () => typeof document === "undefined" || document.visibilityState === "visible";

export const usePeopleChat = create<State>((set, get) => ({
  channels: [], loaded: false, counters: {}, messages: {}, more: {}, dir: EMPTY_DIR, activeId: null, error: null,

  loadChannels: async () => {
    if (MOCK) { set({ loaded: true }); return; }
    try {
      const rows = await call<PChannel[]>("GET", "/people-chat/channels");
      set({ channels: rows, counters: fromChannels(rows), loaded: true, error: null });
      if (!get().dir.self) void get().loadDirectory();
    } catch { set({ loaded: true, error: "load" }); }
  },
  loadDirectory: async () => {
    if (MOCK) return;
    try { set({ dir: await call<Directory>("GET", "/people-chat/directory") }); } catch { /* the viewer role may lack it: mentions then use channel members only */ }
  },

  open: async (id) => {
    set({ activeId: id });
    if (!id) return;
    if (!get().messages[id]) {
      try {
        const page = await call<PMessage[]>("GET", `/people-chat/channels/${id}/messages?limit=${PAGE}`);
        set((s) => ({ messages: { ...s.messages, [id]: page }, more: { ...s.more, [id]: page.length >= PAGE } }));
      } catch { set({ error: "messages" }); }
    }
    get().markRead(id);
  },
  loadOlder: async (id) => {
    const cur = get().messages[id] ?? [];
    if (!cur.length) return;
    try {
      const page = await call<PMessage[]>("GET", `/people-chat/channels/${id}/messages?limit=${PAGE}&before=${cur[0].seq}`);
      set((s) => ({ messages: { ...s.messages, [id]: [...page, ...(s.messages[id] ?? [])] }, more: { ...s.more, [id]: page.length >= PAGE } }));
    } catch { /* keep what is shown */ }
  },
  send: async (id, text) => {
    const m = await call<PMessage>("POST", `/people-chat/channels/${id}/messages`, { text });
    get().apply("pchat.message", { channel_id: id, message: m }); // idempotent with the echo over the socket
  },
  openDM: async (userId) => {
    try {
      const ch = await call<PChannel>("POST", "/people-chat/dm", { user_id: userId });
      get().apply("pchat.channel", { channel: ch });
      return ch.id;
    } catch { set({ error: "dm" }); return null; }
  },
  create: async (b) => {
    try {
      const ch = await call<PChannel>("POST", "/people-chat/channels", b);
      get().apply("pchat.channel", { channel: ch });
      return ch.id;
    } catch { set({ error: "create" }); return null; }
  },
  addMember: async (id, m) => { await call("POST", `/people-chat/channels/${id}/members`, m); await get().loadChannels(); },
  removeMember: async (id, kind, mid) => {
    await call("DELETE", `/people-chat/channels/${id}/members/${kind}/${encodeURIComponent(mid)}`);
    await get().loadChannels();
  },
  markRead: (id) => {
    const c = get().counters[id];
    if (!c) return;
    const seq = c.lastSeq;
    set((s) => ({ counters: applyRead(s.counters, id, seq) }));
    if (c.unread > 0 || c.mentions > 0) void call("POST", `/people-chat/channels/${id}/read`, { seq }).catch(() => {});
  },

  apply: (type, p) => {
    if (!p) return;
    if (type === "pchat.message" && p.message) {
      const msg = p.message as PMessage;
      const me = get().dir.self;
      const known = get().channels.some((c) => c.id === msg.channel_id);
      set((s) => {
        const list = s.messages[msg.channel_id];
        const messages = list && !list.some((m) => m.seq === msg.seq) ? { ...s.messages, [msg.channel_id]: [...list, msg].sort((a, b) => a.seq - b.seq) } : s.messages;
        const counters = applyIncoming(s.counters, msg, me, { activeId: s.activeId, focused: focused() });
        const channels = s.channels.map((c) => (c.id === msg.channel_id
          ? { ...c, last_seq: Math.max(c.last_seq, msg.seq), last_message_at: msg.created_at, preview: msg.body.slice(0, 90), unread: counters[c.id]?.unread ?? c.unread, mentions_unread: counters[c.id]?.mentions ?? c.mentions_unread }
          : c));
        return { messages, counters, channels };
      });
      if (!known) void get().loadChannels(); // a channel I was just added to
      if (get().activeId === msg.channel_id && focused()) get().markRead(msg.channel_id);
    } else if (type === "pchat.channel" && p.channel) {
      const ch = p.channel as PChannel;
      const still = ch.members?.some((m) => m.kind === "user" && m.id === get().dir.self) || ch.kind === "org";
      if (!still) set((s) => ({ channels: s.channels.filter((c) => c.id !== ch.id) }));
      else set((s) => ({ channels: s.channels.some((c) => c.id === ch.id) ? s.channels.map((c) => (c.id === ch.id ? { ...c, ...ch, unread: c.unread, mentions_unread: c.mentions_unread } : c)) : [{ ...ch, unread: 0, mentions_unread: 0 }, ...s.channels] , counters: s.counters[ch.id] ? s.counters : { ...s.counters, [ch.id]: { unread: 0, mentions: 0, lastSeq: ch.last_seq ?? 0 } } }));
    } else if (type === "pchat.read" && p.channel_id) {
      set((s) => ({ counters: applyRead(s.counters, p.channel_id, p.seq ?? 0) })); // another tab of mine read it
    }
  },
}));

/** Unread totals for badges (the chats entry of the navigation). */
export function usePeopleUnread() {
  const counters = usePeopleChat((s) => s.counters);
  return { unread: totalUnread(counters), mentions: totalMentions(counters) };
}

/** People and agents that can be @mentioned in a channel. */
export function mentionablesOf(ch: PChannel | undefined, dir: Directory): Mentionable[] {
  const name = new Map<string, string>(dir.people.map((p) => [`user:${p.id}`, p.name]));
  for (const a of dir.agents) name.set(`agent:${a.id}`, a.name);
  if (!ch || ch.kind === "org") {
    return [...dir.people.map((p) => ({ kind: "user" as const, id: p.id, name: p.name })), ...dir.agents.filter((a) => ch?.members.some((m) => m.kind === "agent" && m.id === a.id)).map((a) => ({ kind: "agent" as const, id: a.id, name: a.name }))];
  }
  return ch.members.map((m) => ({ kind: m.kind, id: m.id, name: name.get(`${m.kind}:${m.id}`) ?? m.id }));
}
