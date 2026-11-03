"use client";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useStore, typingIn } from "@/lib/store";
import { useChats, type ConvRow, type ChatThreadInfo } from "@/lib/chats";
import {
  GENERAL, agentBadges, convForThread, dayKey, highlightParts, isChatConv, mergeById, preview, threadFromPath, threadOf, threadPath, turnAgents,
  type ThreadMsg,
} from "@/lib/chatsLogic";
import { needsAttention, pedidoCardAnchors, pedidoFromSearch, pedidoHref, pedidoInfo, pedidoRows, searchGroups, type PedidoRow } from "@/lib/pedidosLogic";
import { intlLocale, useT } from "@/lib/i18n";
import { PEDIDO_OPEN_EVENT } from "@/lib/pedidoNav";
import { usePreferences, useAgentView } from "@/lib/preferences";
import { startRealtime } from "@/lib/ws";
import { useFileViewer } from "@/lib/fileView";
import type { ChatMessage } from "@/lib/types";
import { Avatar, Bubble, ChatInput, LegacyDivider, legacyEdge, sendChat } from "../chat/ChatThread";
import { useSession } from "@/lib/session";
import { FileViewerPanel } from "../files/FileViewerPanel";
import { useAgentColor, useAgentName, readable } from "../ui";
import { PeopleChatsList, useChannelTitle } from "./PeopleChatsList"; // people chat (presence.md)
import { PeopleChatPane } from "./PeopleChatPane";
import { usePeopleChat } from "./peopleChatStore";
import { Glyph, StatusPill, shortTime } from "./chatsUi";
import { PedidoPanel, usePedidoConv, usePedidoCtx } from "./PedidoPanel";

// ---------------------------------------------------------------------------------------------
// Chats in order (docs/superpowers/specs/2026-11-03-chats-y-pedidos-design.md): one chat per "who you talk to"
// (agents, people) and every piece of work is a pedido that opens in a panel beside the chat, never instead of it.
// The agent threads are views over the same messages (docs/architecture/chat-history.md): what you wrote in
// "The whole office" and was routed to an agent is in that agent's thread, and what you write to an agent shows there.
// ---------------------------------------------------------------------------------------------

type Sel = { kind: "thread"; key: string };
const EMPTY: ChatMessage[] = [];
const SUGGESTIONS = ["cmd.suggestionHello", "cmd.suggestionFinance", "cmd.suggestion1"];

const CSS = `
@keyframes chats-in { from { opacity: 0; transform: translateY(5px); } to { opacity: 1; transform: none; } }
@keyframes chats-fade { from { opacity: 0; } to { opacity: 1; } }
@keyframes chats-shimmer { 0% { opacity: .45; } 50% { opacity: .9; } 100% { opacity: .45; } }
.chats-in { animation: chats-in .22s ease-out both; }
.chats-fade { animation: chats-fade .2s ease-out both; }
.chats-skel { animation: chats-shimmer 1.3s ease-in-out infinite; }
[data-reduce="1"] .chats-in, [data-reduce="1"] .chats-fade, [data-reduce="1"] .chats-skel { animation: none; }
[data-reduce="1"] .chats-t { transition: none !important; }
@media (prefers-reduced-motion: reduce) { .chats-in, .chats-fade, .chats-skel { animation: none; } .chats-t { transition: none !important; } }
`;

function dayLabel(iso: string, t: (k: string) => string): string {
  const d = new Date(iso), now = new Date();
  const days = Math.floor((+new Date(now.getFullYear(), now.getMonth(), now.getDate()) - +new Date(d.getFullYear(), d.getMonth(), d.getDate())) / 86400000);
  if (days <= 0) return t("chats.today");
  if (days === 1) return t("chats.yesterday");
  return new Intl.DateTimeFormat(intlLocale(), { weekday: "long", day: "numeric", month: "long", ...(d.getFullYear() !== now.getFullYear() ? { year: "numeric" } : {}) }).format(d);
}

function Skeleton({ rows = 5, kind }: { rows?: number; kind: "list" | "thread" }) {
  if (kind === "list") {
    return (
      <div data-testid="chats-skeleton" className="space-y-1 px-2 py-2" aria-hidden>
        {Array.from({ length: rows }, (_, i) => (
          <div key={i} className="chats-skel flex items-center gap-2.5 rounded-xl px-2 py-2.5" style={{ animationDelay: `${i * 90}ms` }}>
            <span className="h-9 w-9 shrink-0 rounded-lg bg-mute/15" />
            <span className="min-w-0 flex-1 space-y-1.5"><span className="block h-2.5 w-2/5 rounded bg-mute/15" /><span className="block h-2 w-4/5 rounded bg-mute/10" /></span>
          </div>
        ))}
      </div>
    );
  }
  return (
    <div data-testid="chats-skeleton" className="space-y-4 px-4 py-4" aria-hidden>
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className={`chats-skel flex gap-2 ${i % 3 === 1 ? "flex-row-reverse" : ""}`} style={{ animationDelay: `${i * 90}ms` }}>
          {i % 3 !== 1 && <span className="h-6 w-6 shrink-0 rounded-md bg-mute/15" />}
          <span className="block space-y-1.5" style={{ width: `${40 + ((i * 17) % 35)}%` }}><span className="block h-8 rounded-xl bg-mute/12" /></span>
        </div>
      ))}
    </div>
  );
}

function Highlight({ text, q }: { text: string; q: string }) {
  const parts = useMemo(() => highlightParts(text, q), [text, q]);
  return <>{parts.map((p, i) => (p.hit ? <mark key={i} className="rounded bg-accent/20 px-0.5 text-ink">{p.text}</mark> : <span key={i}>{p.text}</span>))}</>;
}

function GeneralAvatar({ size = 36 }: { size?: number }) {
  return (
    <span aria-hidden className="flex shrink-0 items-center justify-center rounded-lg bg-accent text-white" style={{ width: size, height: size }}>
      <Glyph name="users" size={Math.round(size * 0.5)} />
    </span>
  );
}

function UnreadBadge({ n }: { n: number }) {
  if (n <= 0) return null;
  return <span data-testid="chats-unread" className="min-w-[18px] rounded-full bg-accent px-1.5 py-[1px] text-center text-[10px] font-semibold leading-4 text-white">{n > 99 ? "99+" : n}</span>;
}

// ---------------------------------------------------------------------------------------------
// Left column
// ---------------------------------------------------------------------------------------------

function ThreadRow({ info, active, onPick }: { info: ChatThreadInfo; active: boolean; onPick: () => void }) {
  const { t } = useT();
  const isGeneral = info.key === GENERAL;
  const view = useAgentView(info.key);
  const name = useAgentName();
  const who = info.preview_from && info.preview_from !== "user" ? `${name(info.preview_from).split(" ")[0]}: ` : info.preview_from === "user" ? `${t("common.you")}: ` : "";
  return (
    <button
      type="button" onClick={onPick} data-testid="chats-thread" data-thread={info.key} data-active={active} aria-current={active ? "page" : undefined}
      className={`chats-t group flex w-full items-center gap-2.5 rounded-xl px-2 py-2 text-left transition-colors ${active ? "bg-accent-soft ring-1 ring-accent/40" : "hover:bg-panel2"}`}
    >
      {isGeneral ? <GeneralAvatar /> : <Avatar id={info.key} size={36} />}
      <span className="min-w-0 flex-1">
        <span className="flex items-baseline gap-2">
          <span className="min-w-0 flex-1 truncate text-[13px] font-semibold text-ink">{isGeneral ? t("chats.general") : view.name}</span>
          {info.last_message_at && <span className="shrink-0 font-mono text-[10px] text-mute">{shortTime(info.last_message_at)}</span>}
        </span>
        <span className="flex items-center gap-2">
          <span className="min-w-0 flex-1 truncate text-[11.5px] text-mute">
            {info.preview ? <>{who}{preview(info.preview, 80)}</> : isGeneral ? t("chats.general.sub") : view.title || t("chats.noMessages")}
          </span>
          <UnreadBadge n={info.unread} />
        </span>
      </span>
    </button>
  );
}


/** A search hit inside a chat (the office or an agent): the matching text, highlighted. */
function ConvItem({ row, active, q, onPick }: { row: ConvRow; active: boolean; q: string; onPick: () => void }) {
  const { t } = useT();
  const name = useAgentName();
  const sender = row.preview_from && row.preview_from !== "user" ? `${name(row.preview_from).split(" ")[0]}: ` : row.preview_from === "user" ? `${t("common.you")}: ` : "";
  const isOffice = row.kind === "office" || !row.agent_id;
  return (
    <button
      type="button" onClick={onPick} data-testid="chats-conv" data-conv={row.id} data-kind={row.kind} data-active={active} aria-current={active ? "page" : undefined}
      className={`chats-t flex w-full items-start gap-2.5 rounded-xl px-2 py-2 text-left transition-colors ${active ? "bg-accent-soft ring-1 ring-accent/40" : "hover:bg-panel2"}`}
    >
      {isOffice ? <GeneralAvatar /> : <Avatar id={row.agent_id!} size={36} />}
      <span className="min-w-0 flex-1">
        <span className="flex items-baseline gap-2">
          <span className="min-w-0 flex-1 truncate text-[12.5px] font-semibold text-ink">{isOffice ? t("chats.general") : name(row.agent_id!)}</span>
          <span className="shrink-0 font-mono text-[10px] text-mute">{shortTime(row.updated_at)}</span>
        </span>
        <span className="flex items-center gap-2">
          <span className="min-w-0 flex-1 truncate text-[11.5px] text-mute">{row.preview ? <>{sender}<Highlight text={preview(row.preview, 90)} q={q} /></> : t("chats.noMessages")}</span>
          <UnreadBadge n={row.unread} />
        </span>
      </span>
    </button>
  );
}

/** Who does a pedido, short: "Lucía, Diego +2". */
function useWhoShort() {
  const name = useAgentName();
  return (ids: string[]) => (ids.length ? ids.slice(0, 2).map((id) => name(id).split(" ")[0]).join(", ") + (ids.length > 2 ? ` +${ids.length - 2}` : "") : "");
}

/** One pedido of the sidebar: title, status, who does it and when. */
function PedidoItem({ row, active, q, hitPreview, onPick }: { row: PedidoRow; active: boolean; q: string; hitPreview?: string; onPick: () => void }) {
  const who = useWhoShort()(row.agents);
  return (
    <button
      type="button" onClick={onPick} data-testid="chats-pedido" data-request={row.id} data-conv={row.convId} data-status={row.status} data-active={active} aria-current={active ? "true" : undefined}
      className={`chats-t flex w-full items-start gap-2.5 rounded-xl px-2 py-2 text-left transition-colors ${active ? "bg-accent-soft ring-1 ring-accent/40" : "hover:bg-panel2"}`}
    >
      <span aria-hidden className={`mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg ${needsAttention(row.status) ? "bg-warn/15 text-warn" : "bg-mute/10 text-ink2"}`}><Glyph name="pedido" size={14} /></span>
      <span className="min-w-0 flex-1">
        <span className="flex items-baseline gap-2">
          <span className="min-w-0 flex-1 truncate text-[12.5px] font-semibold text-ink"><Highlight text={row.title} q={q} /></span>
          <span className="shrink-0 font-mono text-[10px] text-mute">{shortTime(row.updatedAt)}</span>
        </span>
        <span className="mt-0.5 flex items-center gap-1.5">
          <StatusPill status={row.status} />
          <span className="min-w-0 flex-1 truncate text-[11px] text-mute">{who}</span>
          <UnreadBadge n={row.unread} />
        </span>
        {hitPreview && <span className="mt-0.5 block truncate text-[11px] text-mute"><Highlight text={preview(hitPreview, 90)} q={q} /></span>}
      </span>
    </button>
  );
}

function GroupTitle({ children, action }: { children: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex items-center justify-between px-2 pb-1">
      <h2 className="text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute">{children}</h2>
      {action}
    </div>
  );
}

// ---------------------------------------------------------------------------------------------
// The card of a pedido in the chat (one per pedido, on its latest message)
// ---------------------------------------------------------------------------------------------

function PedidoCard({ requestId, onOpen }: { requestId: string; onOpen: (id: string) => void }) {
  const { t } = useT();
  const ctx = usePedidoCtx();
  const conv = usePedidoConv(requestId);
  const req = useStore((s) => s.requests[requestId]);
  const info = useMemo(() => pedidoInfo(requestId, ctx, conv.participants), [requestId, ctx, conv.participants]);
  const who = useWhoShort()(info.agents);
  const attention = needsAttention(info.status);
  const title = (conv.row?.title_custom ? conv.title : "") || req?.text || conv.title || t("pedido.label");
  return (
    <div data-testid="chats-request" data-request={requestId} data-status={info.status}
      className={`chats-in mt-1.5 w-full max-w-[380px] rounded-xl border bg-panel px-3 py-2.5 ${attention ? "border-warn/50" : "border-line-strong"}`}>
      <div className="flex items-start gap-2">
        <span aria-hidden className={`mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-md ${attention ? "bg-warn/15 text-warn" : "bg-accent-soft text-accent-hover"}`}><Glyph name="pedido" size={13} /></span>
        <span className="min-w-0 flex-1">
          <span className="block text-[10px] font-semibold uppercase tracking-[0.08em] text-mute">{t("pedido.label")}</span>
          <span className="line-clamp-2 block text-[12.5px] font-semibold leading-snug text-ink">{title}</span>
        </span>
      </div>
      <div className="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 pl-8">
        <StatusPill status={info.status} testId="chats-request-status" />
        {who && <span className="truncate text-[11px] text-mute">{who}</span>}
      </div>
      {attention && <p className="mt-1 pl-8 text-[11.5px] leading-snug text-ink2">{t(info.status === "needs_help" ? "pedido.attention.help" : "pedido.attention.approval")}</p>}
      <div className="mt-2 flex flex-wrap gap-1.5 pl-8">
        <button type="button" data-testid="chats-open-pedido" onClick={() => onOpen(requestId)}
          className="chats-t inline-flex items-center gap-1 rounded-md bg-accent px-2.5 py-1 text-[11.5px] font-semibold text-white transition-colors hover:bg-accent-hover">
          {t("pedido.open")}<Glyph name="chevron" size={12} />
        </button>
        {attention && (
          <Link href="/approvals/inbox" data-testid="chats-to-pending"
            className="chats-t inline-flex items-center gap-1 rounded-md border border-warn/50 bg-panel px-2.5 py-1 text-[11.5px] font-semibold text-warn transition-colors hover:bg-warn/10">{t("pedido.toPending")}</Link>
        )}
      </div>
    </div>
  );
}

function RoutedTo({ ids }: { ids: string[] }) {
  const { t } = useT();
  const name = useAgentName();
  const color = useAgentColor();
  if (!ids.length) return null;
  return (
    <div data-testid="chats-routed" className="mt-0.5 flex flex-wrap items-center justify-end gap-x-1.5 gap-y-0.5 text-[10.5px] text-mute">
      <span>{t("chats.routedTo")}</span>
      {ids.map((id) => (
        <span key={id} data-testid="chats-badge" data-agent={id} className="inline-flex items-center gap-1 rounded-full border border-line bg-panel px-1.5 py-[1px] font-medium" style={{ color: readable(color(id)) }}>
          <span aria-hidden className="h-1.5 w-1.5 rounded-full" style={{ background: color(id) }} />{name(id).split(" ")[0]}
        </span>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------------------------
// Right pane: messages
// ---------------------------------------------------------------------------------------------

function useLiveMessages(sel: Sel): { msgs: ChatMessage[]; hist: ReturnType<typeof useChats.getState>["hist"][string] | undefined } {
  const hist = useChats((s) => s.hist[sel.key]);
  const live = useStore((s) => s.chat);
  const msgs = useMemo(() => {
    const base = hist?.items ?? EMPTY;
    // the live tail: what arrived over the WebSocket (or was just sent) that the loaded page does not have yet
    const floor = hist?.more ? base[0]?.ts : "";
    const tail: ChatMessage[] = [];
    for (const [conv, list] of Object.entries(live)) if (isChatConv(conv)) for (const m of list) if (!floor || m.ts >= floor) tail.push(m);
    const merged = mergeById<ChatMessage>(base, tail);
    return threadOf<ChatMessage & ThreadMsg>(merged, sel.key) as ChatMessage[];
  }, [hist, live, sel]);
  return { msgs, hist };
}

function TypingNow({ thread }: { thread: string }) {
  const { t } = useT();
  const name = useAgentName();
  const typing = useStore((s) => s.typing);
  const reduce = usePreferences((s) => s.prefs.reduceMotion);
  const [, bump] = useState(0);
  useEffect(() => { const i = setInterval(() => bump((x) => x + 1), 2000); return () => clearInterval(i); }, []);
  const ids = thread === GENERAL
    ? [...new Set([...typingIn(typing, "office"), ...Object.keys(typing).filter((c) => c.startsWith("agent:")).flatMap((c) => typingIn(typing, c))])]
    : [...new Set([...typingIn(typing, `agent:${thread}`), ...typingIn(typing, "office").filter((id) => id === thread)])];
  return (
    <div role="status" aria-live="polite" data-testid="chats-typing" data-count={ids.length} data-reduce={reduce ? "1" : "0"} className="min-h-[18px] px-4 text-[11px] text-mute">
      {ids.length > 0 && (
        <span className="inline-flex items-center gap-1.5">
          <span className="ac-dots" aria-hidden><span /><span /><span /></span>
          {t(ids.length === 1 ? "chat.typing.one" : "chat.typing.many", { name: name(ids[0]).split(" ")[0] })}
        </span>
      )}
    </div>
  );
}

function Messages({ sel, onOpenPedido, empty }: { sel: Sel; onOpenPedido: (id: string) => void; empty: ReactNode }) {
  const { t } = useT();
  const { msgs, hist } = useLiveMessages(sel);
  const loadOlder = useChats((s) => s.loadOlder);
  const retry = useChats((s) => s.loadHist);
  const routes = useStore((s) => s.routes);
  const box = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const prevHeight = useRef(0);
  const key = sel.key;
  const turns = useMemo(() => turnAgents(msgs as ThreadMsg[]), [msgs]);
  const anchors = useMemo(() => pedidoCardAnchors(msgs), [msgs]); // one card per pedido, on its latest message
  const me = useSession((s) => s.user?.id);

  // keep the reader where they were when older messages are prepended; stay at the bottom for new ones
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    if (prevHeight.current && !stick.current) el.scrollTop += el.scrollHeight - prevHeight.current;
    else if (stick.current) el.scrollTop = el.scrollHeight;
    prevHeight.current = 0;
  }, [msgs.length, hist?.loaded]);
  useEffect(() => { stick.current = true; prevHeight.current = 0; }, [key]);

  const rows = useMemo(() => {
    let lastDay = "";
    return msgs.map((m, i) => {
      const day = dayKey(m.ts);
      const sep = day !== lastDay ? m.ts : "";
      lastDay = day;
      const prev = msgs[i - 1];
      const grouped = !!prev && !sep && prev.from === m.from && prev.kind === m.kind && m.from !== "user" && prev.turn_id === m.turn_id;
      return { m, sep, grouped };
    });
  }, [msgs]);

  if (!hist || (!hist.loaded && msgs.length === 0)) return <Skeleton kind="thread" />;
  if (hist.error && msgs.length === 0) {
    return (
      <div data-testid="chats-error" role="alert" className="flex flex-1 flex-col items-center justify-center gap-2 px-6 py-10 text-center">
        <p className="text-[13px] font-medium text-ink">{t("chats.error.title")}</p>
        <p className="max-w-xs text-xs text-mute">{t("chats.error.hint")}</p>
        <button type="button" data-testid="chats-retry" className="rounded-md border border-line-strong bg-panel px-3 py-1.5 text-xs font-medium text-ink hover:bg-panel2" onClick={() => retry(sel.key)}>{t("chats.retry")}</button>
      </div>
    );
  }
  return (
    <div
      ref={box} role="log" aria-live="polite" aria-relevant="additions" aria-label={t("chat.log")} data-testid="chats-log" tabIndex={0} data-count={msgs.length}
      onScroll={(e) => { const el = e.currentTarget; stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 64; }}
      className="min-h-0 flex-1 space-y-1.5 overflow-y-auto px-4 py-3"
    >
      {hist.more && (
        <div className="flex justify-center pb-2">
          <button type="button" data-testid="chats-older" disabled={hist.loading}
            onClick={() => { prevHeight.current = box.current?.scrollHeight ?? 0; stick.current = false; void loadOlder(key); }}
            className="chats-t inline-flex items-center gap-1.5 rounded-full border border-line-strong bg-panel px-3 py-1 text-[11.5px] font-medium text-ink2 transition-colors hover:border-accent disabled:opacity-60">
            <Glyph name="up" size={12} />{hist.loading ? t("chats.loadingOlder") : t("chats.loadOlder")}
          </button>
        </div>
      )}
      {msgs.length === 0 && empty}
      {rows.map(({ m, sep, grouped }, i) => {
        const mine = m.from === "user" && !m.shared_legacy && !(m.author_id && me && m.author_id !== me);
        const edge = legacyEdge(msgs, i);
        const badges = agentBadges(m as ThreadMsg, turns);
        return (
          <div key={m.id}>
            {sep && (
              <div data-testid="chats-day" className="my-2 flex items-center gap-3 text-[10.5px] font-medium uppercase tracking-wide text-mute">
                <span className="h-px flex-1 bg-line" /><span>{dayLabel(sep, t)}</span><span className="h-px flex-1 bg-line" />
              </div>
            )}
            {/* "The whole office" mixes the office with every 1:1 chat: only the start of the earlier shared history is marked there */}
            {(edge === "start" || (edge === "end" && sel.key !== GENERAL)) && <LegacyDivider start={edge === "start"} />}
            <div className="chats-in" data-testid="chats-msg" data-id={m.id} data-from={m.from} data-conv={m.conversation} data-badges={badges.join(",")}>
              <Bubble m={m} grouped={grouped} showRoute />
              {mine && !(m.turn_id && routes[m.turn_id]) && <div className="pr-1"><RoutedTo ids={badges} /></div>}
              {m.request_id && anchors.has(m.id) && <div className={mine ? "flex justify-end" : "pl-8"}><PedidoCard requestId={m.request_id} onOpen={onOpenPedido} /></div>}
            </div>
          </div>
        );
      })}
    </div>
  );
}

// ---------------------------------------------------------------------------------------------
// The screen
// ---------------------------------------------------------------------------------------------

export function ChatsView({ agentId }: { agentId?: string }) {
  const { t } = useT();
  const router = useRouter();
  const hydrate = usePreferences((s) => s.hydrate);
  const reduce = usePreferences((s) => s.prefs.reduceMotion);
  const [sel, setSel] = useState<Sel>({ kind: "thread", key: agentId || GENERAL });
  const [showThread, setShowThread] = useState(!!agentId); // phones: list first, thread after picking
  const [q, setQ] = useState("");
  const [showArchived, setShowArchived] = useState(false);
  const [searching, setSearching] = useState(false);
  // the open pedido (request id), beside the chat; mirrored in ?pedido=<id>
  const [pedido, setPedido] = useState<string | null>(null);
  const [legacyConv, setLegacyConv] = useState(""); // an old link to a work conversation (?conv=<id>): opens its pedido
  const pushedPedido = useRef(false); // the open pedido has its own history entry: closing goes Back to where the person was
  const agents = useStore((s) => s.agents);
  const agentOrder = useStore((s) => s.agentOrder);
  const loaded = useStore((s) => s.loaded);
  const storeConvs = useStore((s) => s.conversations);
  const viewerOpen = useFileViewer((s) => !!s.ref);
  const threads = useChats((s) => s.threads);
  const threadsLoaded = useChats((s) => s.threadsLoaded);
  const convs = useChats((s) => s.convs);
  const convsLoaded = useChats((s) => s.convsLoaded);
  const convsMore = useChats((s) => s.convsMore);
  const loadThreads = useChats((s) => s.loadThreads);
  const loadConvs = useChats((s) => s.loadConvs);
  const loadHist = useChats((s) => s.loadHist);
  const markRead = useChats((s) => s.markRead);
  const markConvRead = useChats((s) => s.markConvRead);
  const channels = usePeopleChat((s) => s.channels);
  const channelTitle = useChannelTitle();
  const name = useAgentName();
  const ctx = usePedidoCtx();

  // realtime + preferences, in case the screen is opened on its own (the office does the same)
  useEffect(() => startRealtime(), []);
  useEffect(() => { hydrate(); }, [hydrate]);

  // the URL follows the selection without remounting (and back/forward work)
  const peopleOpen = usePeopleChat((s) => s.activeId); // a people channel is open (replaces the thread on the right)
  const navigate = useCallback((next: Sel) => {
    void usePeopleChat.getState().open(null);
    setSel(next);
    setShowThread(true);
    if (typeof window !== "undefined") {
      const p = pedidoHref(threadPath(next.key), window.location.search, pedidoFromSearch(window.location.search).pedido || null);
      if (window.location.pathname + window.location.search !== p) { window.history.pushState(null, "", p); pushedPedido.current = false; }
    }
  }, []);
  const openPedido = useCallback((id: string) => {
    if (typeof window === "undefined") return;
    const href = pedidoHref(window.location.pathname, window.location.search, id);
    // switching from one pedido to another keeps a single history entry: Back still closes the panel
    if (pedidoFromSearch(window.location.search).pedido) window.history.replaceState(null, "", href);
    else { window.history.pushState(null, "", href); pushedPedido.current = true; }
    setPedido(id);
  }, []);
  const closePedido = useCallback(() => {
    if (typeof window === "undefined") return;
    setPedido(null);
    if (pushedPedido.current) { pushedPedido.current = false; window.history.back(); return; }
    window.history.replaceState(null, "", pedidoHref(window.location.pathname, window.location.search, null));
  }, []);
  // a pedido opened from elsewhere on this screen (a command card in the chat)
  useEffect(() => {
    const on = (e: Event) => { const id = (e as CustomEvent<string>).detail; if (id) openPedido(id); };
    window.addEventListener(PEDIDO_OPEN_EVENT, on);
    return () => window.removeEventListener(PEDIDO_OPEN_EVENT, on);
  }, [openPedido]);
  useEffect(() => {
    const read = () => { const p = pedidoFromSearch(window.location.search); setPedido(p.pedido || null); if (p.conv && !p.pedido) setLegacyConv(p.conv); };
    read(); // first render: a link straight to a pedido (a notification, a command card) or to an old work conversation
    const onPop = () => {
      if (!window.location.pathname.startsWith("/chats")) return;
      setSel({ kind: "thread", key: threadFromPath(window.location.pathname) });
      pushedPedido.current = false;
      read();
    };
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);
  // ?conv=<id>: the pedido of that conversation, once the lists know it
  useEffect(() => {
    if (!legacyConv) return;
    const rid = convs.find((c) => c.id === legacyConv)?.request_id ?? storeConvs[legacyConv]?.request_id ?? null;
    if (rid) {
      window.history.replaceState(null, "", pedidoHref(window.location.pathname, window.location.search, rid));
      setPedido(rid);
      setLegacyConv("");
    } else if (convsLoaded && loaded) {
      window.history.replaceState(null, "", pedidoHref(window.location.pathname, window.location.search, null));
      setLegacyConv("");
    }
  }, [legacyConv, convs, storeConvs, convsLoaded, loaded]);

  // first load
  useEffect(() => { void loadThreads(); void loadConvs({}); }, [loadThreads, loadConvs]);

  // search (debounced): looks in agents, people and pedidos at once
  useEffect(() => {
    const term = q.trim();
    setSearching(term.length > 0);
    const id = setTimeout(() => { void loadConvs({ q: term, archived: term ? false : showArchived }); }, term ? 250 : 0);
    return () => clearTimeout(id);
  }, [q, showArchived, loadConvs]);
  // the list without a search (the backend search replaces `convs` with its hits): local matches look here
  const [base, setBase] = useState<ConvRow[]>([]);
  useEffect(() => { if (!searching && convsLoaded) setBase(convs); }, [convs, convsLoaded, searching]);

  useEffect(() => { void loadHist(sel.key); }, [sel, loadHist]);

  // anything new (a message, a request) refreshes the lists and the open thread, debounced
  const stamp = useStore((s) => { let n = Object.keys(s.requests).length; for (const k in s.chat) n += s.chat[k].length; return n; });
  const first = useRef(true);
  useEffect(() => {
    if (first.current) { first.current = false; return; }
    const id = setTimeout(() => {
      void loadThreads();
      if (!q.trim()) void loadConvs({ archived: showArchived });
      void loadHist(sel.key);
    }, 600);
    return () => clearTimeout(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [stamp]);

  // opening a chat reads it; opening a pedido reads its conversation
  const unreadHere = threads.find((x) => x.key === sel.key)?.unread ?? 0;
  useEffect(() => {
    if (!unreadHere) return;
    const id = setTimeout(() => { if (document.visibilityState === "hidden") return; markRead(sel.key); }, 900);
    return () => clearTimeout(id);
  }, [unreadHere, sel, markRead]);
  const pedidoConv = pedido ? convs.find((c) => c.request_id === pedido) : undefined;
  useEffect(() => {
    if (!pedidoConv?.unread) return;
    const id = setTimeout(() => { if (document.visibilityState !== "hidden") markConvRead(pedidoConv.id); }, 900);
    return () => clearTimeout(id);
  }, [pedidoConv?.id, pedidoConv?.unread, markConvRead]);

  // Agents: the whole office first, then every agent (the ones with the most recent activity first)
  const threadList = useMemo(() => {
    const byKey = new Map(threads.map((x) => [x.key, x]));
    const empty = (key: string): ChatThreadInfo => ({ key, agent_id: key === GENERAL ? undefined : key, preview: "", last_message_at: "", message_count: 0, unread: 0 });
    const ids = agentOrder.length ? agentOrder : Object.keys(agents);
    const agentsRows = ids.map((id) => byKey.get(id) ?? empty(id));
    agentsRows.sort((a, b) => (+new Date(b.last_message_at || 0) - +new Date(a.last_message_at || 0)) || 0);
    return [byKey.get(GENERAL) ?? empty(GENERAL), ...agentsRows];
  }, [threads, agents, agentOrder]);
  // Pedidos: every piece of work asked for, with its status (archived ones behind "see archived")
  const pedidos = useMemo(() => pedidoRows(convs, ctx, { archived: showArchived }), [convs, ctx, showArchived]);

  // search in the three groups (local names + what the backend found in the messages)
  const results = useMemo(() => {
    if (!searching) return null;
    const hits = convs;
    const local = [...pedidoRows(base, ctx, { archived: false }), ...pedidoRows(base, ctx, { archived: true })];
    const g = searchGroups(q, {
      agents: threadList.map((x) => ({ key: x.key, name: x.key === GENERAL ? t("chats.general") : name(x.key), sub: x.key === GENERAL ? t("chats.general.sub") : agents[x.key]?.title ?? "" })),
      people: channels.map((c) => ({ id: c.id, title: channelTitle(c), sub: c.preview ?? "" })),
      pedidos: local,
      hits,
    });
    const hitOf = (key: string) => hits.find((h) => (key === GENERAL ? h.kind === "office" : h.kind === "agent" && h.agent_id === key));
    const rowOf = new Map([...local, ...pedidoRows(hits, ctx, { archived: false })].map((r) => [r.id, r]));
    return {
      agents: g.agents.map((key) => ({ key, hit: hitOf(key), info: threadList.find((x) => x.key === key) })).filter((x) => x.hit || x.info),
      people: g.people,
      pedidos: g.pedidos.flatMap((id) => { const row = rowOf.get(id); return row ? [{ row, hit: hits.find((h) => h.kind === "request" && h.request_id === id) }] : []; }),
    };
  }, [searching, convs, q, threadList, channels, channelTitle, base, ctx, agents, name, t]);
  const noResults = !!results && convsLoaded && !results.agents.length && !results.people.length && !results.pedidos.length;

  const goOffice = (agent?: string) => { if (agent) useStore.getState().select(agent); router.push("/"); };
  const back = () => { setShowThread(false); };

  const threadKey = sel.key;
  const agentView = useAgentView(threadKey && threadKey !== GENERAL ? threadKey : "");
  const first1 = (agentView.name || threadKey).split(" ")[0];
  const unknownAgent = sel.key !== GENERAL && loaded && !agents[sel.key];

  const listLoading = !threadsLoaded || !loaded;

  const emptyThread = (
    <div data-testid="chats-empty" className="chats-fade mx-auto flex max-w-sm flex-col items-center gap-3 px-4 py-10 text-center">
      {threadKey === GENERAL ? <GeneralAvatar size={48} /> : <Avatar id={threadKey} size={48} />}
      <h3 className="text-[14px] font-semibold text-ink">{threadKey === GENERAL ? t("chats.empty.general.title") : t("chats.empty.agent.title", { name: first1 })}</h3>
      <p className="text-xs leading-relaxed text-mute">{threadKey === GENERAL ? t("chats.empty.general.hint") : t("chats.empty.agent.hint", { name: first1 })}</p>
      {threadKey === GENERAL && (
        <div className="flex flex-wrap justify-center gap-1.5">
          {SUGGESTIONS.map((s) => (
            <button key={s} type="button" data-testid="chats-suggestion" onClick={() => void sendChat("office", t(`${s}.prompt`)).catch(() => {})}
              className="chats-t rounded-md border border-line bg-panel px-3 py-1 text-[11.5px] text-ink2 transition-colors hover:border-accent hover:text-ink">{t(`${s}.label`)}</button>
          ))}
        </div>
      )}
    </div>
  );

  const archivedToggle = (
    <button type="button" data-testid="chats-toggle-archived" onClick={() => setShowArchived((v) => !v)} className="text-[11px] font-medium text-accent-hover hover:underline">
      {showArchived ? t("chats.pedidos.showActive") : t("chats.pedidos.showArchived")}
    </button>
  );

  return (
    <div data-testid="chats-view" data-reduce={reduce ? "1" : "0"} data-thread={sel.key} data-pedido={pedido ?? ""} className="chats-root relative flex h-full min-h-0 w-full overflow-hidden bg-bg text-ink">
      <style>{CSS}</style>

      {/* ---- left: agents, people, pedidos ---- */}
      <aside aria-label={t("chats.title")} className={`${showThread ? "hidden md:flex" : "flex"} w-full min-h-0 shrink-0 flex-col border-r border-line bg-panel md:w-[330px]`}>
        <div className="space-y-2 border-b border-line px-3 py-3">
          <div className="flex items-center justify-between gap-2">
            <h1 className="text-[15px] font-semibold tracking-tight text-ink">{t("chats.title")}</h1>
            <button type="button" data-testid="chats-to-office" data-tour="chats.toOffice" onClick={() => goOffice()} className="chats-t inline-flex items-center gap-1 rounded-md border border-line px-2 py-1 text-[11px] font-medium text-ink2 transition-colors hover:border-accent hover:text-ink">
              <Glyph name="office" size={12} />{t("chats.toOffice")}
            </button>
          </div>
          <label data-tour="chats.search" className="relative block">
            <span className="sr-only">{t("chats.search")}</span>
            <Glyph name="search" size={14} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-mute" />
            <input
              data-testid="chats-search" type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("chats.search.placeholder")} autoComplete="off" maxLength={100}
              className="w-full rounded-lg border border-line bg-panel2 py-2 pl-8 pr-8 text-[12.5px] text-ink outline-none transition-colors placeholder:text-mute focus:border-accent"
            />
            {q && (
              <button type="button" aria-label={t("chats.search.clear")} onClick={() => setQ("")} className="absolute right-1.5 top-1/2 -translate-y-1/2 rounded p-1 text-mute hover:bg-mute/10 hover:text-ink"><Glyph name="x" size={12} /></button>
            )}
          </label>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto px-2 py-2">
          {results ? (
            <div aria-label={t("chats.results")} data-testid="chats-results">
              {!convsLoaded ? <Skeleton kind="list" rows={3} /> : noResults ? (
                <p data-testid="chats-no-results" className="chats-fade px-3 py-8 text-center text-xs leading-relaxed text-mute">{t("chats.search.none", { q: q.trim() })}</p>
              ) : (
                <>
                  {results.agents.length > 0 && (
                    <section aria-label={t("chats.group.agents")} data-testid="chats-results-agents">
                      <GroupTitle>{t("chats.group.agents")}</GroupTitle>
                      {results.agents.map((x) => x.hit
                        ? <ConvItem key={x.key} row={x.hit} q={q.trim()} active={!peopleOpen && sel.key === x.key} onPick={() => navigate({ kind: "thread", key: x.key })} />
                        : <ThreadRow key={x.key} info={x.info!} active={!peopleOpen && sel.key === x.key} onPick={() => navigate({ kind: "thread", key: x.key })} />)}
                    </section>
                  )}
                  {results.people.length > 0 && <PeopleChatsList heading={t("chats.group.people")} only={results.people} onPicked={() => setShowThread(true)} />}
                  {results.pedidos.length > 0 && (
                    <section aria-label={t("chats.group.pedidos")} data-testid="chats-results-pedidos" className="mt-4">
                      <GroupTitle>{t("chats.group.pedidos")}</GroupTitle>
                      {results.pedidos.map(({ row, hit }) => <PedidoItem key={row.id} row={row} q={q.trim()} hitPreview={hit?.preview} active={pedido === row.id} onPick={() => openPedido(row.id)} />)}
                    </section>
                  )}
                </>
              )}
            </div>
          ) : (
            <>
              <section aria-label={t("chats.group.agents")} data-testid="chats-threads" data-tour="chats.threads">
                <GroupTitle>{t("chats.group.agents")}</GroupTitle>
                {listLoading ? <Skeleton kind="list" rows={6} /> : threadList.map((x) => (
                  <ThreadRow key={x.key} info={x} active={!peopleOpen && sel.key === x.key} onPick={() => navigate({ kind: "thread", key: x.key })} />
                ))}
              </section>
              <PeopleChatsList heading={t("chats.group.people")} onPicked={() => setShowThread(true)} />
              <section aria-label={t("chats.group.pedidos")} data-testid="chats-pedidos" data-tour="chats.past" className="mt-4">
                <GroupTitle action={archivedToggle}>{showArchived ? t("chats.pedidos.archived") : t("chats.group.pedidos")}</GroupTitle>
                {!convsLoaded ? <Skeleton kind="list" rows={2} /> : pedidos.length === 0 ? (
                  <p data-testid="chats-pedidos-empty" className="chats-fade rounded-xl border border-dashed border-line-strong px-3 py-5 text-center text-xs leading-relaxed text-mute">
                    {showArchived ? t("chats.pedidos.archivedEmpty") : t("chats.pedidos.empty")}
                  </p>
                ) : pedidos.map((r) => <PedidoItem key={r.id} row={r} q="" active={pedido === r.id} onPick={() => openPedido(r.id)} />)}
                {convsMore && (
                  <button type="button" onClick={() => void loadConvs({ archived: showArchived, append: true })} className="mt-1 w-full rounded-md py-1.5 text-center text-[11.5px] font-medium text-accent-hover hover:bg-panel2">{t("chats.loadMore")}</button>
                )}
              </section>
            </>
          )}
        </div>
      </aside>

      {/* ---- right: the open chat ---- */}
      {peopleOpen && (
        <div className={`${showThread ? "flex" : "hidden md:flex"} min-h-0 min-w-0 flex-1`}>
          <PeopleChatPane onBack={() => { void usePeopleChat.getState().open(null); setShowThread(false); }} />
        </div>
      )}
      <section aria-label={t("chats.conversation")} className={`${peopleOpen ? "hidden" : showThread ? "flex" : "hidden md:flex"} min-h-0 min-w-0 flex-1 flex-col bg-bg`}>
        <header data-tour="chats.header" className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-line bg-panel px-4 py-2.5">
          <button type="button" data-testid="chats-back" onClick={back} aria-label={t("chats.back")} className="flex min-h-10 min-w-10 items-center justify-center rounded-md p-1.5 text-ink2 hover:bg-panel2 md:hidden"><Glyph name="back" size={16} /></button>
          {sel.key === GENERAL ? <GeneralAvatar size={32} /> : <Avatar id={sel.key} size={32} />}
          <div className="min-w-0 flex-1">
            <h2 data-testid="chats-title" className="truncate text-[14px] font-semibold text-ink">{sel.key === GENERAL ? t("chats.general") : name(sel.key)}</h2>
            <p className="truncate text-[11.5px] text-mute">{sel.key === GENERAL ? t("chats.general.sub") : agentView.title || t("chats.agent.sub")}</p>
          </div>
          {sel.key !== GENERAL && (
            <button type="button" data-testid="chats-open-office" onClick={() => goOffice(sel.key)} className="chats-t inline-flex items-center gap-1 rounded-md border border-line-strong bg-panel px-2 py-1 text-[11.5px] font-medium text-ink2 transition-colors hover:border-accent hover:text-ink">
              <Glyph name="external" size={12} />{t("chats.seeInOffice")}
            </button>
          )}
        </header>

        {sel.key === GENERAL && (
          <p data-testid="chats-hint" className="border-b border-line bg-accent-soft/50 px-4 py-1.5 text-[11px] text-ink2">{t("chats.general.hint")}</p>
        )}

        {unknownAgent ? (
          <div data-testid="chats-unknown" className="flex flex-1 flex-col items-center justify-center gap-2 px-6 text-center">
            <p className="text-[13px] font-medium text-ink">{t("chats.unknown.title")}</p>
            <button type="button" className="rounded-md border border-line-strong bg-panel px-3 py-1.5 text-xs font-medium text-ink hover:bg-panel2" onClick={() => navigate({ kind: "thread", key: GENERAL })}>{t("chats.unknown.go")}</button>
          </div>
        ) : (
          <div key={sel.key} data-tour="chats.messages" className="chats-fade flex min-h-0 flex-1 flex-col">
            <Messages sel={sel} onOpenPedido={openPedido} empty={emptyThread} />
            <TypingNow thread={sel.key} />
          </div>
        )}

        {!unknownAgent && (
          <div data-tour="chats.input" className="border-t border-line bg-panel p-3">
            <ChatInput
              key={sel.key} conv={convForThread(sel.key)} general={sel.key === GENERAL} testId="chats-input" submitTestId="chats-send"
              placeholder={sel.key === GENERAL ? t("cmd.placeholder") : t("chat.placeholder.agent", { name: first1 })}
            />
          </div>
        )}
      </section>

      {/* ---- beside the chat: the open pedido ---- */}
      {pedido && <PedidoPanel requestId={pedido} onClose={closePedido} />}

      {/* files opened from a message or a pedido (same viewer as the office chat) */}
      {viewerOpen && (
        <div data-testid="file-viewer-host" className="pointer-events-none absolute bottom-0 right-0 top-0 z-30 max-w-full">
          <FileViewerPanel />
        </div>
      )}
    </div>
  );
}
