"use client";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import { useStore, typingIn } from "@/lib/store";
import { chatsApi, useChats, type ConvRow, type ChatThreadInfo } from "@/lib/chats";
import {
  GENERAL, agentBadges, convForThread, dayKey, highlightParts, isChatConv, mergeById, preview, threadFromPath, threadOf, threadPath, turnAgents,
  type ThreadMsg,
} from "@/lib/chatsLogic";
import { intlLocale, trOr, useT } from "@/lib/i18n";
import { usePreferences, useAgentView } from "@/lib/preferences";
import { startRealtime } from "@/lib/ws";
import { useFileViewer } from "@/lib/fileView";
import { useProjects } from "@/lib/projects/store";
import { fmtTime } from "@/lib/meta";
import type { ChatMessage } from "@/lib/types";
import { Avatar, Bubble, ChatInput, sendChat } from "../chat/ChatThread";
import { FileViewerPanel } from "../files/FileViewerPanel";
import { ReportView } from "../ReportView";
import { Icon } from "../icons";
import { readable, useAgentColor, useAgentName } from "../ui";
import { PeopleChatsList } from "./PeopleChatsList"; // people chat (presence.md)
import { PeopleChatPane } from "./PeopleChatPane";
import { usePeopleChat } from "./peopleChatStore";

// ---------------------------------------------------------------------------------------------
// Chat history: General (the whole office) and one thread per agent, side by side. Both read the
// same messages (docs/architecture/chat-history.md): what you wrote in General and was routed to
// an agent is in that agent's thread, and what you write to an agent shows in General.
// ---------------------------------------------------------------------------------------------

type Sel = { kind: "thread"; key: string } | { kind: "conv"; id: string };
const EMPTY: ChatMessage[] = [];
const SUGGESTIONS = ["cmd.suggestionHello", "cmd.suggestionFinance", "cmd.suggestion1"];

/** Small line icons (kept local: this screen owns its glyphs). */
const GLYPH = {
  search: "M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14zM20 20l-4-4",
  archive: "M4 5h16v4H4zM6 9v10h12V9M10 13h4",
  trash: "M5 7h14M10 7V4h4v3M7 7l1 13h8l1-13M10 11v6M14 11v6",
  edit: "M4 20h4L19 9l-4-4L4 16zM13.5 6.5l4 4",
  users: "M9 11a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM3 20a6 6 0 0 1 12 0M16 5.2a3 3 0 0 1 0 5.6M18 14.5a6 6 0 0 1 3 5.5",
  back: "M19 12H5M11 6l-6 6 6 6",
  office: "M4 21V6l8-3 8 3v15M4 21h16M9 9h2M13 9h2M9 13h2M13 13h2M10 21v-4h4v4",
  doc: "M6 3h8l5 5v13H6zM14 3v5h5M9 13h7M9 17h7",
  folder: "M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z",
  chat: "M4 5h16v11H10l-5 4v-4H4z",
  x: "M6 6l12 12M18 6L6 18",
  up: "M6 15l6-6 6 6",
  restore: "M4 12a8 8 0 1 0 3-6.2M4 4v5h5",
  external: "M14 4h6v6M20 4l-9 9M18 14v5H5V6h5",
};
function Glyph({ name, size = 15, className = "" }: { name: keyof typeof GLYPH; size?: number; className?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.75} strokeLinecap="round" strokeLinejoin="round" aria-hidden focusable="false" className={className}>
      <path d={GLYPH[name]} />
    </svg>
  );
}

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

/** "12:40", "Ayer", "mar", "3 mar": short time of a list row. */
function shortTime(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso), now = new Date();
  if (isNaN(+d) || d.getFullYear() < 2000) return ""; // the backend sends a zero time for a thread without messages
  const days = Math.floor((+new Date(now.getFullYear(), now.getMonth(), now.getDate()) - +new Date(d.getFullYear(), d.getMonth(), d.getDate())) / 86400000);
  const loc = intlLocale();
  if (days <= 0) return fmtTime(iso);
  if (days === 1) return new Intl.RelativeTimeFormat(loc, { numeric: "auto" }).format(-1, "day");
  if (days < 7) return new Intl.DateTimeFormat(loc, { weekday: "short" }).format(d);
  return new Intl.DateTimeFormat(loc, { day: "numeric", month: "short" }).format(d);
}
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

function ConvItem({ row, active, q, onPick }: { row: ConvRow; active: boolean; q: string; onPick: () => void }) {
  const { t } = useT();
  const name = useAgentName();
  const color = useAgentColor();
  const icon = row.kind === "request" ? "folder" : row.kind === "agent" ? "chat" : "office";
  const sender = row.preview_from && row.preview_from !== "user" ? `${name(row.preview_from).split(" ")[0]}: ` : row.preview_from === "user" ? `${t("common.you")}: ` : "";
  return (
    <button
      type="button" onClick={onPick} data-testid="chats-conv" data-conv={row.id} data-kind={row.kind} data-active={active} aria-current={active ? "page" : undefined}
      className={`chats-t flex w-full items-start gap-2.5 rounded-xl px-2 py-2 text-left transition-colors ${active ? "bg-accent-soft ring-1 ring-accent/40" : "hover:bg-panel2"}`}
    >
      <span aria-hidden className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-mute/10 text-ink2"
        style={row.kind === "agent" && row.agent_id ? { color: readable(color(row.agent_id)) } : undefined}><Glyph name={icon} size={14} /></span>
      <span className="min-w-0 flex-1">
        <span className="flex items-baseline gap-2">
          <span className="min-w-0 flex-1 truncate text-[12.5px] font-semibold text-ink"><Highlight text={row.title} q={q} /></span>
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

// ---------------------------------------------------------------------------------------------
// Request / project / report links of a message
// ---------------------------------------------------------------------------------------------

function RequestLinks({ requestId, onOpenReport, onOpenConv }: { requestId: string; onOpenReport: (reportId: string) => void; onOpenConv: (id: string) => void }) {
  const { t } = useT();
  const router = useRouter();
  const req = useStore((s) => s.requests[requestId]);
  const report = useStore((s) => Object.values(s.reports).find((r) => r.request_id === requestId));
  const conv = useChats((s) => s.convs.find((c) => c.request_id === requestId));
  const status = req?.status ?? "";
  const openProject = (id: string) => {
    const p = useProjects.getState();
    p.setOpen(true);
    void p.openProject(id);
    router.push("/");
  };
  const btn = "chats-t inline-flex items-center gap-1 rounded-md border border-line-strong bg-panel px-2 py-1 text-[11px] font-medium text-accent-hover transition-colors hover:border-accent";
  return (
    <div data-testid="chats-request" data-request={requestId} className="chats-in mt-1.5 flex max-w-full flex-wrap items-center gap-1.5 rounded-xl border border-line bg-panel2/70 px-2.5 py-1.5">
      <span className="text-[10.5px] font-semibold uppercase tracking-wide text-mute">{t("chats.request")}</span>
      {status && <span data-testid="chats-request-status" className="rounded bg-mute/10 px-1.5 py-[1px] text-[10.5px] font-medium text-ink2">{trOr(`chats.req.${status}`, trOr(`cmd.status.${status}`, status))}</span>}
      {report && <button type="button" data-testid="chats-open-report" className={btn} onClick={() => onOpenReport(report.id)}><Glyph name="doc" size={12} />{t("chats.viewReport")}</button>}
      {conv?.project_id && <button type="button" data-testid="chats-open-project" className={btn} onClick={() => openProject(conv.project_id!)}><Glyph name="folder" size={12} />{t("chats.viewProject")}</button>}
      {conv && <button type="button" data-testid="chats-open-work" className={btn} onClick={() => onOpenConv(conv.id)}><Glyph name="chat" size={12} />{t("chats.viewWork")}</button>}
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
  const key = sel.kind === "thread" ? sel.key : `conv:${sel.id}`;
  const hist = useChats((s) => s.hist[key]);
  const live = useStore((s) => s.chat);
  const msgs = useMemo(() => {
    const base = hist?.items ?? EMPTY;
    if (sel.kind === "conv") return base;
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

function Messages({ sel, onOpenReport, onOpenConv, empty }: { sel: Sel; onOpenReport: (id: string) => void; onOpenConv: (id: string) => void; empty: ReactNode }) {
  const { t } = useT();
  const { msgs, hist } = useLiveMessages(sel);
  const loadOlder = useChats((s) => s.loadOlder);
  const retry = useChats((s) => s.loadHist);
  const retryConv = useChats((s) => s.loadRequestConv);
  const routes = useStore((s) => s.routes);
  const box = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const prevHeight = useRef(0);
  const key = sel.kind === "thread" ? sel.key : `conv:${sel.id}`;
  const turns = useMemo(() => turnAgents(msgs as ThreadMsg[]), [msgs]);

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
        <button type="button" data-testid="chats-retry" className="rounded-md border border-line-strong bg-panel px-3 py-1.5 text-xs font-medium text-ink hover:bg-panel2" onClick={() => (sel.kind === "thread" ? retry(sel.key) : retryConv(sel.id))}>{t("chats.retry")}</button>
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
      {rows.map(({ m, sep, grouped }) => {
        const mine = m.from === "user";
        const badges = agentBadges(m as ThreadMsg, turns);
        return (
          <div key={m.id}>
            {sep && (
              <div data-testid="chats-day" className="my-2 flex items-center gap-3 text-[10.5px] font-medium uppercase tracking-wide text-mute">
                <span className="h-px flex-1 bg-line" /><span>{dayLabel(sep, t)}</span><span className="h-px flex-1 bg-line" />
              </div>
            )}
            <div className="chats-in" data-testid="chats-msg" data-id={m.id} data-from={m.from} data-conv={m.conversation} data-badges={badges.join(",")}>
              <Bubble m={m} grouped={grouped} showRoute />
              {mine && !(m.turn_id && routes[m.turn_id]) && <div className="pr-1"><RoutedTo ids={badges} /></div>}
              {m.request_id && <div className={mine ? "flex justify-end" : "pl-8"}><RequestLinks requestId={m.request_id} onOpenReport={onOpenReport} onOpenConv={onOpenConv} /></div>}
            </div>
          </div>
        );
      })}
    </div>
  );
}

// ---------------------------------------------------------------------------------------------
// Header of the right pane
// ---------------------------------------------------------------------------------------------

function ConvHeaderActions({ row, onGone, onBack }: { row: ConvRow; onGone: () => void; onBack: () => void }) {
  const { t } = useT();
  const patch = useChats((s) => s.patchConv);
  const drop = useChats((s) => s.dropConv);
  const [mode, setMode] = useState<"" | "rename" | "delete">("");
  const [title, setTitle] = useState(row.title);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState(false);
  useEffect(() => { setMode(""); setTitle(row.title); setErr(false); }, [row.id, row.title]);
  const run = async (f: () => Promise<void>) => { setBusy(true); setErr(false); try { await f(); } catch { setErr(true); } setBusy(false); };
  const b = "chats-t inline-flex items-center gap-1 rounded-md border border-line-strong bg-panel px-2 py-1 text-[11.5px] font-medium text-ink2 transition-colors hover:border-accent hover:text-ink disabled:opacity-50";
  return (
    <div className="flex flex-wrap items-center justify-end gap-1.5">
      {mode === "rename" ? (
        <form className="flex items-center gap-1" onSubmit={(e) => { e.preventDefault(); void run(async () => { const r = await chatsApi.rename(row.id, title); patch({ ...row, ...r, title: r.title ?? title }); setMode(""); }); }}>
          <input autoFocus data-testid="chats-rename-input" value={title} onChange={(e) => setTitle(e.target.value)} maxLength={120} aria-label={t("chats.rename")}
            className="w-56 rounded-md border border-line-strong bg-panel2 px-2 py-1 text-[12px] text-ink outline-none focus:border-accent" />
          <button type="submit" data-testid="chats-rename-save" disabled={busy || !title.trim()} className={b}>{t("chats.save")}</button>
          <button type="button" className={b} onClick={() => { setMode(""); setTitle(row.title); }}>{t("chats.cancel")}</button>
        </form>
      ) : mode === "delete" ? (
        <div role="alertdialog" aria-label={t("chats.delete.title")} className="flex flex-wrap items-center gap-1.5 rounded-md border border-err/40 bg-err/10 px-2 py-1">
          <span className="text-[11.5px] text-err">{t("chats.delete.confirm")}</span>
          <button type="button" data-testid="chats-delete-confirm" disabled={busy} className="rounded-md border border-err/50 bg-err px-2 py-0.5 text-[11.5px] font-semibold text-white disabled:opacity-50"
            onClick={() => void run(async () => { await chatsApi.remove(row.id); drop(row.id); onGone(); })}>{t("chats.delete.yes")}</button>
          <button type="button" className={b} onClick={() => setMode("")}>{t("chats.cancel")}</button>
        </div>
      ) : (
        <>
          <button type="button" data-testid="chats-rename" className={b} onClick={() => setMode("rename")}><Glyph name="edit" size={12} />{t("chats.rename")}</button>
          <button type="button" data-testid="chats-archive" disabled={busy} className={b}
            onClick={() => void run(async () => { const r = await chatsApi.archive(row.id, !row.archived); patch({ ...row, ...r, archived: !row.archived }); if (!row.archived) onBack(); })}>
            <Glyph name={row.archived ? "restore" : "archive"} size={12} />{row.archived ? t("chats.unarchive") : t("chats.archive")}
          </button>
          <button type="button" data-testid="chats-delete" className={`${b} hover:border-err hover:text-err`} onClick={() => setMode("delete")}><Glyph name="trash" size={12} />{t("chats.delete")}</button>
        </>
      )}
      {err && <span role="alert" className="text-[11px] text-err">{t("chats.actionError")}</span>}
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
  const [report, setReport] = useState<string | null>(null);
  const [searching, setSearching] = useState(false);
  const agents = useStore((s) => s.agents);
  const agentOrder = useStore((s) => s.agentOrder);
  const loaded = useStore((s) => s.loaded);
  const viewerOpen = useFileViewer((s) => !!s.ref);
  const threads = useChats((s) => s.threads);
  const threadsLoaded = useChats((s) => s.threadsLoaded);
  const convs = useChats((s) => s.convs);
  const convsLoaded = useChats((s) => s.convsLoaded);
  const convsMore = useChats((s) => s.convsMore);
  const loadThreads = useChats((s) => s.loadThreads);
  const loadConvs = useChats((s) => s.loadConvs);
  const loadHist = useChats((s) => s.loadHist);
  const loadRequestConv = useChats((s) => s.loadRequestConv);
  const markRead = useChats((s) => s.markRead);
  const markConvRead = useChats((s) => s.markConvRead);
  const name = useAgentName();
  const report0 = useStore((s) => (report ? s.reports[report] : undefined));

  // realtime + preferences, in case the screen is opened on its own (the office does the same)
  useEffect(() => startRealtime(), []);
  useEffect(() => { hydrate(); }, [hydrate]);

  // the URL follows the selection without remounting (and back/forward work)
  const peopleOpen = usePeopleChat((s) => s.activeId); // a people channel is open (replaces the thread on the right)
  const navigate = useCallback((next: Sel) => {
    void usePeopleChat.getState().open(null);
    setSel(next);
    setShowThread(true);
    if (typeof window !== "undefined" && next.kind === "thread") {
      const p = threadPath(next.key);
      if (window.location.pathname !== p) window.history.pushState(null, "", p);
    }
  }, []);
  useEffect(() => {
    const onPop = () => { if (window.location.pathname.startsWith("/chats")) { setSel({ kind: "thread", key: threadFromPath(window.location.pathname) }); } };
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  // first load
  useEffect(() => { void loadThreads(); void loadConvs({}); }, [loadThreads, loadConvs]);

  // search (debounced): replaces the lists with matching conversations
  useEffect(() => {
    const term = q.trim();
    setSearching(term.length > 0);
    const id = setTimeout(() => { void loadConvs({ q: term, archived: term ? false : showArchived }); }, term ? 250 : 0);
    return () => clearTimeout(id);
  }, [q, showArchived, loadConvs]);

  const histKey = sel.kind === "thread" ? sel.key : `conv:${sel.id}`;
  useEffect(() => {
    if (sel.kind === "thread") void loadHist(sel.key); else void loadRequestConv(sel.id);
  }, [sel, loadHist, loadRequestConv]);

  // anything new (a message, a request) refreshes the lists and the open thread, debounced
  const stamp = useStore((s) => { let n = Object.keys(s.requests).length; for (const k in s.chat) n += s.chat[k].length; return n; });
  const first = useRef(true);
  useEffect(() => {
    if (first.current) { first.current = false; return; }
    const id = setTimeout(() => {
      void loadThreads();
      if (!q.trim()) void loadConvs({ archived: showArchived });
      if (sel.kind === "thread") void loadHist(sel.key);
    }, 600);
    return () => clearTimeout(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [stamp]);

  // opening a conversation reads it
  const info = sel.kind === "thread" ? threads.find((x) => x.key === sel.key) : undefined;
  const row = sel.kind === "conv" ? convs.find((c) => c.id === sel.id) : undefined;
  const unreadHere = (info?.unread ?? row?.unread ?? 0);
  useEffect(() => {
    if (!unreadHere) return;
    const id = setTimeout(() => { if (document.visibilityState === "hidden") return; if (sel.kind === "thread") markRead(sel.key); else markConvRead(sel.id); }, 900);
    return () => clearTimeout(id);
  }, [unreadHere, sel, markRead, markConvRead, histKey]);

  // list model: General first, then every agent (the ones with the most recent activity first)
  const threadList = useMemo(() => {
    const byKey = new Map(threads.map((x) => [x.key, x]));
    const empty = (key: string): ChatThreadInfo => ({ key, agent_id: key === GENERAL ? undefined : key, preview: "", last_message_at: "", message_count: 0, unread: 0 });
    const ids = agentOrder.length ? agentOrder : Object.keys(agents);
    const agentsRows = ids.map((id) => byKey.get(id) ?? empty(id));
    agentsRows.sort((a, b) => (+new Date(b.last_message_at || 0) - +new Date(a.last_message_at || 0)) || 0);
    return [byKey.get(GENERAL) ?? empty(GENERAL), ...agentsRows];
  }, [threads, agents, agentOrder]);
  const past = useMemo(() => convs.filter((c) => c.kind === "request" && c.archived === (showArchived && !searching)).sort((a, b) => +new Date(b.updated_at) - +new Date(a.updated_at)), [convs, showArchived, searching]);
  const hits = useMemo(() => (searching ? convs : []), [convs, searching]);

  const pickConv = (c: ConvRow) => {
    if (c.kind === "office") navigate({ kind: "thread", key: GENERAL });
    else if (c.kind === "agent" && c.agent_id) navigate({ kind: "thread", key: c.agent_id });
    else navigate({ kind: "conv", id: c.id });
  };
  const goOffice = (agent?: string) => { if (agent) useStore.getState().select(agent); router.push("/"); };
  const back = () => { setShowThread(false); };
  const afterGone = () => { navigate({ kind: "thread", key: GENERAL }); setShowThread(false); };

  const threadKey = sel.kind === "thread" ? sel.key : "";
  const agentView = useAgentView(threadKey && threadKey !== GENERAL ? threadKey : "");
  const first1 = (agentView.name || threadKey).split(" ")[0];
  const unknownAgent = sel.kind === "thread" && sel.key !== GENERAL && loaded && !agents[sel.key];

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

  return (
    <div data-testid="chats-view" data-reduce={reduce ? "1" : "0"} data-thread={sel.kind === "thread" ? sel.key : `conv:${sel.id}`} className="chats-root relative flex h-full min-h-0 w-full overflow-hidden bg-bg text-ink">
      <style>{CSS}</style>

      {/* ---- left: threads + past conversations ---- */}
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
          {searching ? (
            <section aria-label={t("chats.results")} data-testid="chats-results">
              <h2 className="px-2 pb-1 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute">{t("chats.results")}</h2>
              {!convsLoaded ? <Skeleton kind="list" rows={3} /> : hits.length === 0 ? (
                <p data-testid="chats-no-results" className="chats-fade px-3 py-8 text-center text-xs leading-relaxed text-mute">{t("chats.search.none", { q: q.trim() })}</p>
              ) : hits.map((c) => (
                <ConvItem key={c.id} row={c} q={q.trim()} active={(sel.kind === "conv" && sel.id === c.id) || (sel.kind === "thread" && ((c.kind === "office" && sel.key === GENERAL) || (c.kind === "agent" && c.agent_id === sel.key)))} onPick={() => pickConv(c)} />
              ))}
            </section>
          ) : (
            <>
              <section aria-label={t("chats.threads")} data-testid="chats-threads" data-tour="chats.threads">
                <h2 className="px-2 pb-1 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute">{t("chats.threads")}</h2>
                {listLoading ? <Skeleton kind="list" rows={6} /> : threadList.map((x) => (
                  <ThreadRow key={x.key} info={x} active={sel.kind === "thread" && sel.key === x.key} onPick={() => navigate({ kind: "thread", key: x.key })} />
                ))}
              </section>
              <PeopleChatsList onPicked={() => setShowThread(true)} />
              <section aria-label={t("chats.past")} data-testid="chats-past" data-tour="chats.past" className="mt-4">
                <div className="flex items-center justify-between px-2 pb-1">
                  <h2 className="text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute">{showArchived ? t("chats.archived") : t("chats.past")}</h2>
                  <button type="button" data-testid="chats-toggle-archived" onClick={() => setShowArchived((v) => !v)} className="text-[11px] font-medium text-accent-hover hover:underline">
                    {showArchived ? t("chats.showActive") : t("chats.showArchived")}
                  </button>
                </div>
                {!convsLoaded ? <Skeleton kind="list" rows={2} /> : past.length === 0 ? (
                  <p data-testid="chats-past-empty" className="chats-fade rounded-xl border border-dashed border-line-strong px-3 py-5 text-center text-xs leading-relaxed text-mute">
                    {showArchived ? t("chats.archived.empty") : t("chats.past.empty")}
                  </p>
                ) : past.map((c) => <ConvItem key={c.id} row={c} q="" active={sel.kind === "conv" && sel.id === c.id} onPick={() => pickConv(c)} />)}
                {convsMore && !searching && (
                  <button type="button" onClick={() => void loadConvs({ archived: showArchived, append: true })} className="mt-1 w-full rounded-md py-1.5 text-center text-[11.5px] font-medium text-accent-hover hover:bg-panel2">{t("chats.loadMore")}</button>
                )}
              </section>
            </>
          )}
        </div>
      </aside>

      {/* ---- right: the open thread ---- */}
      {peopleOpen && (
        <div className={`${showThread ? "flex" : "hidden md:flex"} min-h-0 min-w-0 flex-1`}>
          <PeopleChatPane onBack={() => { void usePeopleChat.getState().open(null); setShowThread(false); }} />
        </div>
      )}
      <section aria-label={t("chats.conversation")} className={`${peopleOpen ? "hidden" : showThread ? "flex" : "hidden md:flex"} min-h-0 min-w-0 flex-1 flex-col bg-bg`}>
        <header data-tour="chats.header" className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-line bg-panel px-4 py-2.5">
          <button type="button" data-testid="chats-back" onClick={back} aria-label={t("chats.back")} className="flex min-h-10 min-w-10 items-center justify-center rounded-md p-1.5 text-ink2 hover:bg-panel2 md:hidden"><Glyph name="back" size={16} /></button>
          {sel.kind === "thread" ? (
            <>
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
            </>
          ) : (
            <>
              <span aria-hidden className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-mute/10 text-ink2"><Glyph name="folder" size={16} /></span>
              <div className="min-w-0 flex-1">
                <h2 data-testid="chats-title" className="truncate text-[14px] font-semibold text-ink">{row?.title ?? t("chats.work")}</h2>
                <p className="truncate text-[11.5px] text-mute">{t("chats.work.sub")}{row?.archived ? ` · ${t("chats.archived.badge")}` : ""}</p>
              </div>
              {row && <ConvHeaderActions row={row} onGone={afterGone} onBack={afterGone} />}
            </>
          )}
        </header>

        {sel.kind === "thread" && sel.key === GENERAL && (
          <p data-testid="chats-hint" className="border-b border-line bg-accent-soft/50 px-4 py-1.5 text-[11px] text-ink2">{t("chats.general.hint")}</p>
        )}

        {unknownAgent ? (
          <div data-testid="chats-unknown" className="flex flex-1 flex-col items-center justify-center gap-2 px-6 text-center">
            <p className="text-[13px] font-medium text-ink">{t("chats.unknown.title")}</p>
            <button type="button" className="rounded-md border border-line-strong bg-panel px-3 py-1.5 text-xs font-medium text-ink hover:bg-panel2" onClick={() => navigate({ kind: "thread", key: GENERAL })}>{t("chats.unknown.go")}</button>
          </div>
        ) : (
          <div key={histKey} data-tour="chats.messages" className="chats-fade flex min-h-0 flex-1 flex-col">
            <Messages sel={sel} onOpenReport={setReport} onOpenConv={(id) => navigate({ kind: "conv", id })} empty={sel.kind === "thread" ? emptyThread : <p className="py-10 text-center text-xs text-mute">{t("chats.work.empty")}</p>} />
            {sel.kind === "thread" && <TypingNow thread={sel.key} />}
          </div>
        )}

        {sel.kind === "thread" && !unknownAgent ? (
          <div data-tour="chats.input" className="border-t border-line bg-panel p-3">
            <ChatInput
              key={sel.key} conv={convForThread(sel.key)} general={sel.key === GENERAL} testId="chats-input" submitTestId="chats-send"
              placeholder={sel.key === GENERAL ? t("cmd.placeholder") : t("chat.placeholder.agent", { name: first1 })}
            />
          </div>
        ) : sel.kind === "conv" ? (
          <div data-testid="chats-readonly" className="flex flex-wrap items-center gap-2 border-t border-line bg-panel px-4 py-2.5 text-[11.5px] text-mute">
            <span className="min-w-0 flex-1">{t("chats.readonly")}</span>
            <button type="button" className="chats-t rounded-md border border-line-strong bg-panel px-2 py-1 font-medium text-accent-hover transition-colors hover:border-accent" onClick={() => navigate({ kind: "thread", key: GENERAL })}>{t("chats.continueGeneral")}</button>
          </div>
        ) : null}
      </section>

      {/* files opened from a message (same viewer as the office chat) */}
      {viewerOpen && (
        <div data-testid="file-viewer-host" className="pointer-events-none absolute bottom-0 right-0 top-0 z-30 max-w-full">
          <FileViewerPanel />
        </div>
      )}

      {/* report produced by a request */}
      {report && (
        <div role="dialog" aria-modal="true" aria-label={t("chats.viewReport")} data-testid="chats-report" className="chats-fade absolute inset-0 z-40 flex items-center justify-center bg-black/30 p-4" onClick={() => setReport(null)}>
          <div className="chats-in max-h-full w-full max-w-2xl overflow-y-auto rounded-xl border border-line-strong bg-panel p-5 shadow-float" onClick={(e) => e.stopPropagation()}>
            <button type="button" data-testid="chats-report-close" onClick={() => setReport(null)} className="mb-3 inline-flex items-center gap-1 text-xs text-accent"><Glyph name="back" size={12} />{t("common.back")}</button>
            {report0 ? <ReportView r={report0} /> : <p className="text-xs text-mute">{t("chats.report.missing")}</p>}
          </div>
        </div>
      )}
    </div>
  );
}
