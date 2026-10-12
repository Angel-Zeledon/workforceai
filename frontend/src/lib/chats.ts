import { create } from "zustand";
import { API_URL, MOCK } from "./config";
import { call, toChatMessage } from "./api";
import { authFetch } from "./session";
import { GENERAL, mergeById, type ThreadMsg } from "./chatsLogic";
import type { ChatMessage } from "./types";

/** GET /chats/threads item: General or one agent. */
export interface ChatThreadInfo {
  key: string; agent_id?: string; preview: string; preview_from?: string; last_message_at: string; message_count: number; unread: number;
}
/** GET /conversations item (chat history list). */
export interface ConvRow {
  id: string; title: string; kind: "office" | "agent" | "request"; agent_id?: string; participants: string[];
  request_id: string | null; project_id: string | null; preview: string; preview_from?: string;
  message_count: number; unread: number; archived: boolean; title_custom: boolean; updated_at: string;
}
export interface Page<T> { items: T[]; more: boolean; next: string }

async function getPage<T>(path: string): Promise<Page<T>> {
  if (MOCK) {
    const r = await call<{ items?: T[]; more?: boolean; next?: string } | T[]>("GET", path);
    return Array.isArray(r) ? { items: r, more: false, next: "" } : { items: r.items ?? [], more: !!r.more, next: r.next ?? "" };
  }
  const res = await authFetch(`${API_URL}${path}`, { cache: "no-store" });
  if (!res.ok) throw new Error(`GET ${path} -> ${res.status}`);
  const body = await res.json();
  return { items: Array.isArray(body) ? body : [], more: res.headers.get("X-Has-More") === "true", next: res.headers.get("X-Next-Before") ?? res.headers.get("X-Next-Offset") ?? "" };
}

const qs = (o: Record<string, string | number | undefined>) => {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(o)) if (v !== undefined && v !== "") p.set(k, String(v));
  const s = p.toString();
  return s ? `?${s}` : "";
};

export const chatsApi = {
  threads: async () => {
    const r = await call<unknown>("GET", "/chats/threads");
    return (Array.isArray(r) ? r : []) as ChatThreadInfo[];
  },
  threadMessages: async (key: string, before?: string, limit = 40): Promise<Page<ChatMessage>> => {
    const p = await getPage<any>(`/chats/threads/${encodeURIComponent(key)}/messages${qs({ limit, before })}`);
    return { ...p, items: p.items.map((x) => toChatMessage(x, x.conversation_id ?? x.conversation ?? "office")) };
  },
  conversations: (o: { q?: string; kind?: string; agentId?: string; archived?: "only" | "all"; offset?: number; limit?: number } = {}) =>
    getPage<ConvRow>(`/conversations${qs({ q: o.q?.trim(), kind: o.kind, agent_id: o.agentId, archived: o.archived, offset: o.offset, limit: o.limit ?? 50 })}`),
  conversationMessages: async (id: string): Promise<ChatMessage[]> => {
    const r = await call<unknown>("GET", `/conversations/${encodeURIComponent(id)}/messages`);
    return (Array.isArray(r) ? r : []).map((x) => toChatMessage(x, id));
  },
  rename: (id: string, title: string) => call<ConvRow>("PATCH", `/conversations/${encodeURIComponent(id)}`, { title }),
  archive: (id: string, archived: boolean) => call<ConvRow>("PATCH", `/conversations/${encodeURIComponent(id)}`, { archived }),
  remove: (id: string) => call<unknown>("DELETE", `/conversations/${encodeURIComponent(id)}`),
  readThread: (key: string) => call<unknown>("POST", `/chats/threads/${encodeURIComponent(key)}/read`, {}),
  readConversation: (id: string) => call<unknown>("POST", `/conversations/${encodeURIComponent(id)}/read`, {}),
};

/** History of one thread ("general", an agent id) or of a request conversation ("conv:<id>"). */
export interface Hist { items: ChatMessage[]; more: boolean; next: string; loading: boolean; loaded: boolean; error: boolean }
const EMPTY_HIST: Hist = { items: [], more: false, next: "", loading: false, loaded: false, error: false };

interface ChatsState {
  threads: ChatThreadInfo[];
  threadsLoaded: boolean;
  convs: ConvRow[];
  convsLoaded: boolean;
  convsMore: boolean;
  hist: Record<string, Hist>;
  loadThreads: () => Promise<void>;
  loadConvs: (o?: { q?: string; archived?: boolean; append?: boolean }) => Promise<void>;
  /** first page of a thread (or refresh when already loaded: merges, never loses older pages) */
  loadHist: (key: string) => Promise<void>;
  loadOlder: (key: string) => Promise<void>;
  loadRequestConv: (id: string) => Promise<void>;
  markRead: (key: string) => void;
  markConvRead: (id: string) => void;
  patchConv: (row: ConvRow) => void;
  dropConv: (id: string) => void;
}

let convToken = 0;

export const useChats = create<ChatsState>((set, get) => ({
  threads: [], threadsLoaded: false, convs: [], convsLoaded: false, convsMore: false, hist: {},

  loadThreads: async () => {
    try { set({ threads: await chatsApi.threads(), threadsLoaded: true }); } catch { set({ threadsLoaded: true }); }
  },

  loadConvs: async (o = {}) => {
    const token = ++convToken;
    const offset = o.append ? get().convs.length : 0;
    try {
      const p = await chatsApi.conversations({ q: o.q, archived: o.archived ? "only" : undefined, kind: undefined, offset, limit: 50 });
      if (token !== convToken) return; // a newer search superseded this one
      set((s) => ({ convs: o.append ? [...s.convs, ...p.items.filter((x) => !s.convs.some((c) => c.id === x.id))] : p.items, convsLoaded: true, convsMore: p.more }));
    } catch {
      if (token === convToken) set({ convsLoaded: true });
    }
  },

  loadHist: async (key) => {
    const cur = get().hist[key] ?? EMPTY_HIST;
    if (cur.loading) return;
    set((s) => ({ hist: { ...s.hist, [key]: { ...cur, loading: true, error: false } } }));
    try {
      const p = await chatsApi.threadMessages(key);
      set((s) => {
        const prev = s.hist[key] ?? EMPTY_HIST;
        const items = mergeById<ChatMessage & ThreadMsg>(prev.items as never, p.items as never) as ChatMessage[];
        // keep the older pages the user already loaded: only a first load decides "more"
        return { hist: { ...s.hist, [key]: { items, more: prev.loaded ? prev.more : p.more, next: prev.loaded ? prev.next : p.next, loading: false, loaded: true, error: false } } };
      });
    } catch {
      set((s) => ({ hist: { ...s.hist, [key]: { ...(s.hist[key] ?? EMPTY_HIST), loading: false, loaded: true, error: true } } }));
    }
  },

  loadOlder: async (key) => {
    const cur = get().hist[key];
    if (!cur || cur.loading || !cur.more || !cur.next) return;
    set((s) => ({ hist: { ...s.hist, [key]: { ...cur, loading: true } } }));
    try {
      const p = await chatsApi.threadMessages(key, cur.next);
      set((s) => {
        const prev = s.hist[key] ?? cur;
        return { hist: { ...s.hist, [key]: { items: mergeById<ChatMessage & ThreadMsg>(p.items as never, prev.items as never) as ChatMessage[], more: p.more, next: p.next, loading: false, loaded: true, error: false } } };
      });
    } catch {
      set((s) => ({ hist: { ...s.hist, [key]: { ...(s.hist[key] ?? cur), loading: false, error: true } } }));
    }
  },

  loadRequestConv: async (id) => {
    const key = `conv:${id}`;
    if (get().hist[key]?.loading) return;
    set((s) => ({ hist: { ...s.hist, [key]: { ...(s.hist[key] ?? EMPTY_HIST), loading: true, error: false } } }));
    try {
      const items = await chatsApi.conversationMessages(id);
      set((s) => ({ hist: { ...s.hist, [key]: { items, more: false, next: "", loading: false, loaded: true, error: false } } }));
    } catch {
      set((s) => ({ hist: { ...s.hist, [key]: { ...(s.hist[key] ?? EMPTY_HIST), loading: false, loaded: true, error: true } } }));
    }
  },

  markRead: (key) => {
    const t = get().threads.find((x) => x.key === key);
    if (!t || t.unread === 0) return;
    set((s) => ({ threads: s.threads.map((x) => (x.key === key ? { ...x, unread: 0 } : x)) }));
    chatsApi.readThread(key).catch(() => {});
  },
  markConvRead: (id) => {
    const c = get().convs.find((x) => x.id === id);
    if (!c || c.unread === 0) return;
    set((s) => ({ convs: s.convs.map((x) => (x.id === id ? { ...x, unread: 0 } : x)) }));
    chatsApi.readConversation(id).catch(() => {});
  },
  patchConv: (row) => set((s) => ({ convs: s.convs.map((c) => (c.id === row.id ? { ...c, ...row } : c)) })),
  dropConv: (id) => set((s) => ({ convs: s.convs.filter((c) => c.id !== id), hist: Object.fromEntries(Object.entries(s.hist).filter(([k]) => k !== `conv:${id}`)) })),
}));

export { GENERAL };
