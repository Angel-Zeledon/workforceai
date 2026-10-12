/**
 * Mock of the chat history contract (docs/architecture/chat-history.md, 08-api.md): the enriched
 * GET /conversations, rename/archive/delete, read markers and the two views that go hand in hand
 * (GET /chats/threads, /chats/threads/{key}/messages). It reads the same messages the mock chat
 * keeps, through the same pure logic (chatsLogic.ts), so General and the agent threads always agree.
 */
import { tr } from "../i18n-core";
import { GENERAL, agentBadges, convKind, isChatConv, matches, preview, threadOf, turnAgents, type ThreadMsg } from "../chatsLogic";
import type { Agent, ChatMessage, Conversation, Message } from "../types";

export interface HistoryHost {
  /** every office / 1:1 chat message by conversation */
  chat(): Record<string, ChatMessage[]>;
  /** conversations of requests and their messages */
  requestConvs(): Conversation[];
  requestMessages(id: string): Message[];
  agents(): Agent[];
  removeChat(conv: string): void;
  removeRequestConv(id: string): void;
  renameRequestConv(id: string, title: string): void;
}

interface Row {
  id: string; title: string; kind: "office" | "agent" | "request"; agent_id?: string; participants: string[];
  request_id: string | null; project_id: string | null; preview: string; preview_from?: string;
  message_count: number; unread: number; archived: boolean; title_custom: boolean; updated_at: string; last_message_at: string;
}

const asChat = (m: Message): ChatMessage => ({ id: m.id, conversation: m.conversation_id, turn_id: null, from: m.from, to: m.to, kind: m.kind, text: m.text, reply_to: null, ts: m.ts });

export class MockChatHistory {
  private archived = new Map<string, string>();
  private titles = new Map<string, string>();
  private reads = new Map<string, string>();
  constructor(private host: HistoryHost) {}

  reset() { this.archived.clear(); this.titles.clear(); this.reads.clear(); }

  /** Which requests this class answers (the rest of /conversations stays as it was). */
  matches(method: string, seg: string[], q: URLSearchParams): boolean {
    const [r0, r1, r2] = seg;
    if (r0 === "chats") return true;
    if (r0 !== "conversations") return false;
    if (!r1) return method === "GET" && ["kind", "agent_id", "q", "limit", "offset", "archived"].some((k) => q.has(k));
    if (!r2) return method === "PATCH" || method === "DELETE";
    return method === "POST" && r2 === "read";
  }

  private all(): ChatMessage[] {
    return Object.values(this.host.chat()).flat();
  }
  private name(id: string) { return this.host.agents().find((a) => a.id === id)?.name.split(" ")[0] ?? id; }
  private readAt(key: string) { return this.reads.get(key) ?? ""; }
  private unread(msgs: { from: string; ts: string }[], key: string) {
    const at = this.readAt(key);
    return msgs.filter((m) => m.from !== "user" && m.ts > at).length;
  }

  private rows(): Row[] {
    const chat = this.host.chat();
    const out: Row[] = [];
    const mk = (id: string, title: string, msgs: ChatMessage[], extra: Partial<Row>): Row => {
      const last = msgs[msgs.length - 1];
      return {
        id, title: this.titles.get(id) ?? title, kind: convKind(id), participants: ["user", ...new Set(msgs.flatMap((m) => [m.from, m.to]).filter((p) => p !== "user" && p !== "all" && p !== "system"))],
        request_id: null, project_id: null, preview: last ? preview(last.text, 140) : "", preview_from: last?.from, message_count: msgs.length,
        unread: this.unread(msgs, `conv:${id}`), archived: this.archived.has(id), title_custom: this.titles.has(id),
        updated_at: last?.ts ?? new Date(0).toISOString(), last_message_at: last?.ts ?? new Date(0).toISOString(), ...extra,
      };
    };
    for (const [id, msgs] of Object.entries(chat)) {
      if (!isChatConv(id) || msgs.length === 0) continue;
      out.push(id === "office"
        ? mk(id, tr("chats.title.office"), msgs, {})
        : mk(id, tr("chats.title.agent", { name: this.name(id.slice(6)) }), msgs, { agent_id: id.slice(6) }));
    }
    for (const c of this.host.requestConvs()) {
      const msgs = this.host.requestMessages(c.id).map(asChat);
      out.push(mk(c.id, c.title, msgs, { request_id: c.request_id, updated_at: c.last_message_at, last_message_at: c.last_message_at }));
    }
    return out;
  }

  private rowMessages(id: string): ChatMessage[] {
    return isChatConv(id) ? (this.host.chat()[id] ?? []) : this.host.requestMessages(id).map(asChat);
  }

  handle(method: string, seg: string[], q: URLSearchParams, _body?: any): unknown {
    const body = (_body ?? {}) as { title?: string; archived?: boolean };
    const [r0, r1, r2, r3] = seg;
    if (r0 === "chats" && r1 === "threads" && !r2 && method === "GET") return this.threads();
    if (r0 === "chats" && r1 === "threads" && r2 && r3 === "messages" && method === "GET") return this.thread(decodeURIComponent(r2), q);
    if (r0 === "chats" && r1 === "threads" && r2 && r3 === "read" && method === "POST") return this.readThread(decodeURIComponent(r2));
    if (r0 === "conversations" && !r1 && method === "GET") return this.list(q);
    const id = r1 ? decodeURIComponent(r1) : "";
    const row = this.rows().find((r) => r.id === id);
    if (r0 === "conversations" && id && !row) throw new Error(`${method} /conversations/${id} -> 404`);
    if (r0 === "conversations" && id && !r2 && method === "PATCH") {
      if (typeof body.title === "string") {
        const t = body.title.replace(/\s+/g, " ").trim().slice(0, 120);
        if (!t) throw new Error("PATCH /conversations -> 400");
        this.titles.set(id, t);
        if (!isChatConv(id)) this.host.renameRequestConv(id, t);
      }
      if (typeof body.archived === "boolean") { if (body.archived) { if (!this.archived.has(id)) this.archived.set(id, new Date().toISOString()); } else this.archived.delete(id); }
      return this.rows().find((r) => r.id === id);
    }
    if (r0 === "conversations" && id && !r2 && method === "DELETE") {
      if (isChatConv(id)) this.host.removeChat(id); else this.host.removeRequestConv(id);
      this.archived.delete(id); this.titles.delete(id); this.reads.delete(`conv:${id}`);
      return {};
    }
    if (r0 === "conversations" && id && r2 === "read" && method === "POST") { this.reads.set(`conv:${id}`, new Date().toISOString()); return {}; }
    throw new Error(`mock: ${method} /${seg.join("/")} not implemented`);
  }

  private list(q: URLSearchParams) {
    const needle = q.get("q")?.trim() ?? "", kind = q.get("kind") ?? "", agent = q.get("agent_id") ?? "", arch = q.get("archived") ?? "";
    const limit = Math.min(500, Number(q.get("limit")) || 500), offset = Number(q.get("offset")) || 0;
    let rows = this.rows().filter((r) => (arch === "all" ? true : arch === "only" ? r.archived : !r.archived));
    if (kind) rows = rows.filter((r) => r.kind === kind);
    if (agent) rows = rows.filter((r) => r.id === `agent:${agent}` || this.rowMessages(r.id).some((m) => m.from === agent || m.to === agent));
    if (needle) rows = rows.filter((r) => matches(r.title, needle) || this.rowMessages(r.id).some((m) => matches(m.text, needle)));
    rows.sort((a, b) => +new Date(b.updated_at) - +new Date(a.updated_at));
    const page = rows.slice(offset, offset + limit);
    return { items: page, more: offset + limit < rows.length, next: String(offset + page.length) };
  }

  private threadKeys() { return [GENERAL, ...this.host.agents().map((a) => a.id)]; }

  private threads() {
    const all = this.all();
    return this.threadKeys().map((key) => {
      const msgs = threadOf<ChatMessage & ThreadMsg>(all, key);
      const last = msgs[msgs.length - 1];
      return {
        key, ...(key === GENERAL ? {} : { agent_id: key }), preview: last ? preview(last.text, 140) : "", preview_from: last?.from ?? "",
        last_message_at: last?.ts ?? "", message_count: msgs.length, unread: this.unread(msgs, `thread:${key}`),
      };
    });
  }

  private thread(key: string, q: URLSearchParams) {
    if (key !== GENERAL && !this.host.agents().some((a) => a.id === key)) throw new Error(`GET /chats/threads/${key}/messages -> 404`);
    const all = this.all();
    const msgs = threadOf<ChatMessage & ThreadMsg>(all, key);
    const limit = Math.min(200, Math.max(1, Number(q.get("limit")) || 50));
    const before = q.get("before");
    let end = msgs.length;
    if (before) { end = msgs.findIndex((m) => m.id === before); if (end < 0) throw new Error(`GET /chats/threads/${key}/messages -> 404`); }
    const start = Math.max(0, end - limit);
    const turns = turnAgents(all);
    const items = msgs.slice(start, end).map((m) => ({ ...m, agent_ids: agentBadges(m, turns) }));
    return { items, more: start > 0, next: items[0]?.id ?? "" };
  }

  private readThread(key: string) {
    const now = new Date().toISOString();
    this.reads.set(`thread:${key}`, now);
    if (key === GENERAL) { for (const id of Object.keys(this.host.chat())) if (isChatConv(id)) this.reads.set(`conv:${id}`, now); }
    else this.reads.set(`conv:agent:${key}`, now);
    return {};
  }
}
