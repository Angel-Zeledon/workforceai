"use client";
import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { api } from "@/lib/api";
import { useStore, typingIn } from "@/lib/store";
import { fmtTime } from "@/lib/meta";
import { trOr, useT } from "@/lib/i18n";
import { usePreferences, useAgentView } from "@/lib/preferences";
import type { ChatMessage, RouteDecision, TurnIntent } from "@/lib/types";
import { Btn, readable, useAgentColor, useAgentName } from "../ui";
import { Icon } from "../icons";
import { FileCards } from "../files/FileCard";
import { drive, DriveError } from "@/lib/drive";
import type { FileRef } from "@/lib/types";
import { CommandCard } from "./commands/CommandCard";
import { useSlashComposer } from "./commands/useSlashComposer";
import { surfaceOf } from "@/lib/commands/logic";
import { withOpenFile } from "@/lib/fileEdit";
import { useFileViewer } from "@/lib/fileView";
import { AttachMenu, AttachmentTray, DrivePicker, DropOverlay, MAX_FILES, useAttachments } from "./attachments";

const EMPTY: ChatMessage[] = [];

export function Avatar({ id, size = 24 }: { id: string; size?: number }) {
  const v = useAgentView(id);
  return (
    <span aria-hidden className="flex shrink-0 items-center justify-center rounded-md font-semibold text-white" style={{ width: size, height: size, background: v.color, fontSize: size * 0.48 }}>
      {v.name.slice(0, 1)}
    </span>
  );
}

const INTENT_STYLE: Record<TurnIntent, string> = {
  smalltalk: "bg-mute/10 text-ink2",
  question: "bg-info/10 text-info",
  task: "bg-accent-soft text-accent-hover",
};
/** Visible type of turn (conversation / question / task) from route.decided. */
export function TurnChip({ intent, withHint = false }: { intent: TurnIntent; withHint?: boolean }) {
  const { t } = useT();
  return (
    <span data-testid="turn-chip" data-intent={intent} title={t(`turn.hint.${intent}`)} className={`inline-flex items-center gap-1 whitespace-nowrap rounded px-1.5 py-[2px] text-[10px] font-semibold uppercase tracking-wide ${INTENT_STYLE[intent]}`}>
      <span className="h-1.5 w-1.5 rounded-full bg-current" />
      {t(`turn.${intent}`)}
      {withHint && <span className="sr-only">. {t(`turn.hint.${intent}`)}</span>}
    </span>
  );
}

/** Who answers and why, shown right under the user's message. */
function RouteNote({ r }: { r: RouteDecision }) {
  const { t } = useT();
  const name = useAgentName();
  const color = useAgentColor();
  return (
    <div data-testid="route-note" data-intent={r.intent} className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[10.5px] leading-snug text-mute">
      <TurnChip intent={r.intent} />
      {r.topic && <span>{t("turn.topic", { topic: trOr(`route.topic.${r.topic}`, r.topic) })}</span>}
      {r.responders.map((x) => (
        <span key={x.agent_id}>
          <b className="font-semibold" style={{ color: readable(color(x.agent_id)) }}>{name(x.agent_id).split(" ")[0]}</b>{" "}
          {t(`turn.${x.role}`)}{x.reason ? ` · ${x.reason}` : ""}
        </span>
      ))}
    </div>
  );
}

export function TypingLine({ conv }: { conv: string }) {
  const { t } = useT();
  const name = useAgentName();
  const typing = useStore((s) => s.typing);
  const reduce = usePreferences((s) => s.prefs.reduceMotion);
  const [, bump] = useState(0);
  useEffect(() => { const i = setInterval(() => bump((x) => x + 1), 2000); return () => clearInterval(i); }, []);
  const ids = typingIn(typing, conv);
  return (
    <div role="status" aria-live="polite" data-testid="chat-typing" data-count={ids.length} data-reduce={reduce ? "1" : "0"} className="min-h-[18px] px-1 text-[11px] text-mute">
      {ids.length > 0 && (
        <span className="inline-flex items-center gap-1.5">
          <span className="ac-dots" aria-hidden><span /><span /><span /></span>
          {ids.length === 1 ? t("chat.typing.one", { name: name(ids[0]).split(" ")[0] }) : t("chat.typing.many", { name: name(ids[0]).split(" ")[0] })}
        </span>
      )}
    </div>
  );
}

/** Colleague an agent is redirecting the user to: explicit handoff_to, else the one other agent named in a 1:1 reply. */
function useHandoff(m: ChatMessage): string | null {
  const agents = useStore((s) => s.agents);
  // only the latest redirect is actionable: once the user answered, the offer is over
  const answered = useStore((s) => (s.chat[m.conversation] || []).some((x) => x.from === "user" && +new Date(x.ts) > +new Date(m.ts)));
  if (answered || m.from === "user" || m.kind === "consult") return null;
  if (m.handoff_to && agents[m.handoff_to]) return m.handoff_to;
  if (!m.conversation.startsWith("agent:") || m.from !== m.conversation.slice(6) || m.kind !== "chat") return null;
  const hits = Object.values(agents).filter((a) => a.id !== m.from && new RegExp(`(^|[^\\p{L}])${a.name.split(" ")[0]}([^\\p{L}]|$)`, "u").test(m.text));
  return hits.length === 1 ? hits[0].id : null;
}

export function Bubble({ m, grouped, showRoute }: { m: ChatMessage; grouped: boolean; showRoute: boolean }) {
  const { t } = useT();
  const name = useAgentName();
  const color = useAgentColor();
  const route = useStore((s) => (m.turn_id ? s.routes[m.turn_id] : undefined));
  const mine = m.from === "user";
  const handoff = useHandoff(m);
  const select = useStore((s) => s.select);
  const setDraft = useStore((s) => s.setDraft);
  const ask = () => {
    if (!handoff) return;
    const list = useStore.getState().chat[m.conversation] || [];
    const idx = list.findIndex((x) => x.id === m.id);
    const prev = [...list.slice(0, Math.max(0, idx))].reverse().find((x) => x.from === "user");
    if (prev) setDraft(`agent:${handoff}`, prev.text);
    select(handoff);
  };
  if (m.card) return <CommandCard m={m} />;
  if (m.kind === "system" || m.from === "system") {
    return <div className="py-0.5 text-center text-[10.5px] text-mute">{m.text}</div>;
  }
  const toAgent = m.to && !["user", "all", "system", ""].includes(m.to) && !mine ? name(m.to).split(" ")[0] : "";
  // kind tag only for team traffic (assignments, questions between agents); plain replies to you need no label
  const tag = !mine && (m.kind === "delegation" || m.kind === "consult" || (m.kind === "answer" && toAgent)) ? t(`chat.kind.${m.kind}`) : "";
  return (
    <>
    <div data-testid="chat-msg" data-from={m.from} data-kind={m.kind} className={`flex gap-2 ${mine ? "flex-row-reverse" : ""} ${grouped ? "" : "pt-1"}`}>
      {!mine && (grouped ? <span className="w-6 shrink-0" /> : <Avatar id={m.from} />)}
      <div className={`flex min-w-0 max-w-[86%] flex-col ${mine ? "items-end" : "items-start"}`}>
        {!mine && !grouped && (
          <div className="mb-0.5 flex items-center gap-1.5 text-[10.5px] text-mute">
            <span className="font-semibold" style={{ color: readable(color(m.from)) }}>{name(m.from)}</span>
            {toAgent && <span>{t("chat.to", { name: toAgent })}</span>}
            {tag && <span className="rounded bg-mute/10 px-1 text-[9.5px] font-semibold uppercase tracking-wide">{tag}</span>}
            <span className="font-mono text-[10px]">{fmtTime(m.ts)}</span>
          </div>
        )}
        <div className={`whitespace-pre-line break-words rounded-xl px-3 py-1.5 text-[12.5px] leading-[1.45] ${mine ? "rounded-tr-sm bg-accent text-white" : `rounded-tl-sm border border-line bg-panel2 text-ink ${m.kind === "delegation" ? "border-l-2" : ""}`}`} style={!mine && m.kind === "delegation" ? { borderLeftColor: readable(color(m.from)) } : undefined}>
          {m.text}
        </div>
        <FileCards files={m.files} mine={mine} />
        {handoff && !mine && (
          <button type="button" data-testid="chat-ask-colleague" data-colleague={handoff} onClick={ask} title={t("chat.askColleagueHint", { name: name(handoff).split(" ")[0] })}
            className="mt-1 inline-flex items-center gap-1 rounded-md border border-line-strong bg-panel px-2 py-1 text-[11px] font-medium text-accent-hover transition hover:border-accent">
            <Icon name="arrow" size={12} />{t("chat.askColleague", { name: name(handoff).split(" ")[0] })}
          </button>
        )}
      </div>
    </div>
    {mine && showRoute && route && <div className="pb-1 pl-1"><RouteNote r={route} /></div>}
    </>
  );
}

/** Message list with typing indicator. aria-live so screen readers announce new messages politely. */
export function ChatThread({ conv, showRoute = false, className = "", emptyText }: { conv: string; showRoute?: boolean; className?: string; emptyText: string }) {
  const { t } = useT();
  const msgs = useStore((s) => s.chat[conv]) ?? EMPTY;
  const loadChat = useStore((s) => s.loadChat);
  const box = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const reduce = usePreferences((s) => s.prefs.reduceMotion);
  useEffect(() => { loadChat(conv); }, [conv, loadChat]);
  useEffect(() => {
    const el = box.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [msgs.length]);
  const rows = useMemo(() => msgs.map((m, i) => ({ m, grouped: i > 0 && msgs[i - 1].from === m.from && msgs[i - 1].kind === m.kind && m.from !== "user" && msgs[i - 1].turn_id === m.turn_id })), [msgs]);
  return (
    <div className={`flex min-h-0 flex-col ${className}`}>
      <div
        ref={box} role="log" aria-live="polite" aria-relevant="additions" aria-label={t("chat.log")} data-testid="chat-log" data-reduce={reduce ? "1" : "0"} tabIndex={0}
        onScroll={(e) => { const el = e.currentTarget; stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 48; }}
        className="min-h-0 flex-1 space-y-1.5 overflow-y-auto px-3 py-2"
      >
        {msgs.length === 0 && <div className="py-6 text-center text-xs leading-relaxed text-mute">{emptyText}</div>}
        {rows.map(({ m, grouped }) => <Bubble key={m.id} m={m} grouped={grouped} showRoute={showRoute} />)}
      </div>
      <div className="px-3 pb-1.5"><TypingLine conv={conv} /></div>
    </div>
  );
}

/** Sends to POST /messages (falls back to the legacy request endpoint if the backend has no chat yet, office only).
 * `attachments` are drive paths already uploaded: they show as file cards in the user's message and reach the agents as paths to fs.read. */
export async function sendChat(conv: string, text: string, from = "user", attachments: string[] = [], refs?: FileRef[]) {
  const store = useStore.getState();
  const files = refs ?? attachments.map((p) => ({ path: p, name: p.split("/").pop() || p, action: "attach" }));
  store.addLocalChat({ id: `local:${Date.now().toString(36)}`, conversation: conv, turn_id: null, from, to: conv === "office" ? "all" : conv.slice(6), kind: "chat", text, reply_to: null, ts: new Date().toISOString(), ...(files.length ? { files } : {}) });
  try {
    await api.postMessage(conv, text, attachments);
  } catch (e) {
    if (conv === "office" && /-> (404|405|501)\b/.test(String((e as Error)?.message))) { await api.postRequest(text); return; }
    throw e;
  }
}

export function ChatInput({ conv, placeholder, testId, submitTestId, onSent, autoFocus = false, general = false }: { conv: string; placeholder: string; testId?: string; submitTestId?: string; onSent?: () => void; autoFocus?: boolean; general?: boolean }) {
  const { t } = useT();
  const [text, setTextRaw] = useState("");
  const [caret, setCaret] = useState(0);
  const setText = (v: string, c?: number) => { setTextRaw(v); setCaret(c ?? v.length); };
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const att = useAttachments(conv);
  const [drag, setDrag] = useState(false);
  const [picker, setPicker] = useState<null | "files" | "folder">(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const dirRef = useRef<HTMLInputElement>(null);
  const dragDepth = useRef(0);
  const picked = att.items; // attachments of this message (they upload as soon as they are added)
  const draft = useStore((s) => s.drafts[conv]);
  const setDraft = useStore((s) => s.setDraft);
  useEffect(() => {
    if (!draft) return;
    setText(draft); setDraft(conv, "");
    inputRef.current?.focus();
  }, [draft, conv, setDraft]);
  const editCtx = useFileViewer((s) => (s.editInChat ? s.ref : null));
  const setEditInChat = useFileViewer((s) => s.setEditInChat);
  // the box grows with what is typed (up to ~8 lines) and shrinks back when it is sent
  useEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`;
  }, [text]);
  const sc = useSlashComposer({ conv, surface: surfaceOf(conv, general), text, caret, setText, inputRef, onSent });
  const submit = async (e?: FormEvent) => {
    e?.preventDefault();
    if (!picked.length && sc.submit()) return;
    const v = text.trim() || (att.refs.length ? t("chat.attach.defaultText") : "");
    if (!v || busy || att.uploading) return;
    setBusy(true); setErr("");
    try {
      let refs: FileRef[] = att.refs;
      let body = v;
      const vw = useFileViewer.getState(); // "Edit this file in chat": the open file travels as context (path + version)
      if (vw.editInChat && vw.ref) {
        const cur = await drive.byPath(vw.ref.path).catch(() => null);
        if (cur) {
          const c = withOpenFile(v, refs, { ...vw.ref, file_id: cur.id, version: cur.current_version, mime: cur.mime }, t("fileviewer.editInChatHint", { name: cur.name, path: cur.path, v: cur.current_version }));
          body = c.text; refs = c.refs;
        }
      }
      await sendChat(conv, body, "user", refs.map((r) => r.path), refs);
      setText(""); att.clear(); onSent?.();
    } catch { setErr(t("chat.sendError")); }
    setBusy(false);
  };
  return (
    <>
    <form onSubmit={submit} className="relative flex flex-col gap-1"
      onDragEnter={(e) => { if (e.dataTransfer.types.includes("Files")) { dragDepth.current++; setDrag(true); } }}
      onDragOver={(e) => { if (e.dataTransfer.types.includes("Files")) e.preventDefault(); }}
      onDragLeave={() => { dragDepth.current = Math.max(0, dragDepth.current - 1); if (!dragDepth.current) setDrag(false); }}
      onDrop={(e) => { if (!e.dataTransfer.files.length) return; e.preventDefault(); dragDepth.current = 0; setDrag(false); if (att.addFiles(Array.from(e.dataTransfer.files))) setErr(t("att.err.tooMany", { n: MAX_FILES })); }}>
      {drag && <DropOverlay dest={att.dest} />}
      {sc.menu}
      {sc.suggestionChip}
      {sc.chips}
      {editCtx && (
        <div data-testid="chat-edit-context" className="flex items-center gap-2 rounded-md bg-accent-soft px-2 py-1 text-[11px] text-accent-hover">
          <span className="min-w-0 flex-1 truncate">{t("chat.editingFile", { name: editCtx.name })}</span>
          <button type="button" onClick={() => setEditInChat(false)} className="shrink-0 font-semibold underline-offset-2 hover:underline">{t("chat.editingFileOff")}</button>
        </div>
      )}
      <AttachmentTray items={att.items} onRemove={att.remove} />
      <div className="flex items-end gap-2">
        <input ref={fileRef} type="file" multiple data-testid="chat-attach-input" className="hidden" onChange={(e) => { const fs = Array.from(e.target.files ?? []); if (fs.length && att.addFiles(fs)) setErr(t("att.err.tooMany", { n: MAX_FILES })); e.target.value = ""; }} />
        <input ref={dirRef} type="file" multiple className="hidden" data-testid="chat-attach-dir" {...({ webkitdirectory: "", directory: "" } as Record<string, string>)} onChange={(e) => { const fs = Array.from(e.target.files ?? []); if (fs.length && att.addFiles(fs, true)) setErr(t("att.err.tooMany", { n: MAX_FILES })); e.target.value = ""; }} />
        <AttachMenu dest={att.dest} full={att.full} disabled={busy} onUpload={() => fileRef.current?.click()} onFolderUpload={() => dirRef.current?.click()} onDrive={() => setPicker("files")} onPickDest={() => setPicker("folder")} />
        <textarea
          ref={inputRef} data-testid={testId} value={text} onChange={(e) => { setText(e.target.value, e.target.selectionStart ?? e.target.value.length); sc.clearErr(); }}
          onSelect={(e) => setCaret(e.currentTarget.selectionStart ?? 0)}
          onPaste={(e) => { const fs = Array.from(e.clipboardData.files); if (fs.length) { e.preventDefault(); const named = fs.map((f, i) => (f.name && f.name !== "image.png" ? f : new File([f], `captura-${new Date().toTimeString().slice(0, 8).replace(/:/g, "")}${fs.length > 1 ? `-${i + 1}` : ""}.${(f.type.split("/")[1] || "png").replace("jpeg", "jpg")}`, { type: f.type }))); if (att.addFiles(named)) setErr(t("att.err.tooMany", { n: MAX_FILES })); } }} onKeyDown={(e) => {
            sc.onKeyDown(e);
            if (e.defaultPrevented) return;
            // Enter sends, Shift+Enter makes a new line (and an IME composition keeps its own Enter)
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); void submit(); }
          }} {...sc.aria} placeholder={placeholder} aria-label={placeholder} autoFocus={autoFocus} rows={1}
          className="max-h-[200px] min-h-[40px] min-w-0 flex-1 resize-none overflow-y-auto rounded-lg border border-line bg-panel2 px-3 py-2 text-base sm:text-[13px] leading-[1.45] text-ink outline-none placeholder:text-mute"
        />
        <button type="button" data-testid="cmd-help" title={t("cmds.help.button")} aria-label={t("cmds.help.button")} onClick={sc.openHelp}
          className="min-h-10 min-w-10 rounded-lg border border-line bg-panel2 px-2.5 py-1.5 text-[12px] font-semibold text-mute hover:border-accent hover:text-ink sm:min-h-0 sm:min-w-0">?</button>
        <Btn data-testid={submitTestId} type="submit" kind="primary" className="min-h-10 sm:min-h-0" disabled={busy || sc.running || att.uploading || (!text.trim() && !att.refs.length)}><span className="flex items-center gap-1.5"><Icon name="arrow" size={13} />{busy ? t("cmd.sending") : t("cmd.send")}</span></Btn>
      </div>
      {picked.length > 0 && <div className="text-[10.5px] text-mute">{t("att.savedIn", { path: att.dest })}</div>}
      {sc.status}
      {(err || sc.err) && <div role="alert" data-testid="cmd-error" className="text-[11px] text-err">{err || sc.err}</div>}
    </form>
    {sc.dialogs}
    {picker && <DrivePicker mode={picker} start={picker === "folder" ? att.dest : "/Shared"} onClose={() => setPicker(null)} onFiles={att.addDrive} onFolder={att.setDest} />}
    </>
  );
}
