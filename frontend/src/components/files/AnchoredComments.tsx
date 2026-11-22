"use client";
import { useCallback, useEffect, useMemo, useRef, useState, type RefObject } from "react";
import { fmtDateTime, useT } from "@/lib/i18n";
import { DriveError, type DriveFile, type DriveVersion } from "@/lib/drive";
import { driveComments } from "@/lib/driveComments";
import { MAX_BODY, anchorState, buildThreads, canAskChange, cleanQuote, findQuote, openCount, shortQuote, type FileComment, type Thread } from "@/lib/anchoredComments";
import { pickEditAgent } from "@/lib/fileEdit";
import { fileKind } from "@/lib/fileView";
import { useStore } from "@/lib/store";
import { Icon } from "../icons";
import { useAgentName } from "../ui";
import { errorText } from "@/lib/errors";

/**
 * Comments anchored to a passage of a text document (F7, docs/architecture/anchored-comments.md).
 *  - useFileComments loads the thread list of the version being shown (the server locates each anchor in that version).
 *  - useSelectionQuote follows what the person selects inside the document.
 *  - useCommentHighlights marks the commented passages with the CSS Custom Highlight API (no DOM is touched).
 *  - SelectionBar is the "Comment" button that appears over a selection; CommentsPanel is the side list of threads.
 */

const KNOWN_ERRORS = ["anchor_not_in_text", "anchor_lost", "change_already_requested", "not_found", "read_only_folder", "already_resolved", "not_anchorable", "no_anchor", "invalid_comment", "too_many_comments"];

export interface FileCommentsState {
  comments: FileComment[];
  threads: Thread[];
  open: number;
  loading: boolean;
  error: string;
  reload: () => Promise<void>;
  patch: (c: FileComment) => void;
}

export function useFileComments(file: DriveFile | null, version: number, enabled: boolean): FileCommentsState {
  const [comments, setComments] = useState<FileComment[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const id = file?.id, current = file?.current_version;
  const reload = useCallback(async () => {
    if (!id || !enabled) return;
    try {
      const r = await driveComments.list(id, version === current ? 0 : version);
      setComments(r.comments); setError("");
    } catch (e) { setError(e instanceof DriveError ? e.code : "unavailable"); }
    setLoading(false);
  }, [id, enabled, version, current]);
  useEffect(() => { setComments([]); setLoading(!!id && enabled); }, [id, enabled]);
  useEffect(() => { reload(); }, [reload]);
  const patch = useCallback((c: FileComment) => setComments((all) => all.map((x) => (x.id === c.id ? { ...x, ...c, location: x.location } : x))), []);
  const threads = useMemo(() => buildThreads(comments), [comments]);
  return { comments, threads, open: openCount(comments), loading, error, reload, patch };
}

/** The text the person has selected inside `ref`, kept until it is used or dismissed (a click on the button must not lose it). */
export function useSelectionQuote(ref: RefObject<HTMLElement | null>, enabled: boolean) {
  const [quote, setQuote] = useState<string | null>(null);
  useEffect(() => {
    if (!enabled) { setQuote(null); return; }
    const on = () => {
      const s = document.getSelection();
      const el = ref.current;
      if (!s || s.isCollapsed || !el || s.rangeCount === 0) return;
      const r = s.getRangeAt(0);
      if (!el.contains(r.commonAncestorContainer)) return;
      const q = cleanQuote(s.toString());
      if (q) setQuote(q);
    };
    document.addEventListener("selectionchange", on);
    return () => document.removeEventListener("selectionchange", on);
  }, [ref, enabled]);
  return { quote, clear: () => setQuote(null), set: setQuote };
}

function textNodesOf(root: HTMLElement): Text[] {
  const out: Text[] = [];
  const w = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode: (n) => (n.nodeValue && !(n.parentElement?.closest("[data-fc-skip]")) ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT),
  });
  for (let n = w.nextNode(); n; n = w.nextNode()) out.push(n as Text);
  return out;
}

type Registry = { set: (k: string, v: unknown) => void; delete: (k: string) => void };
const registry = (): Registry | null => (typeof CSS !== "undefined" && (CSS as unknown as { highlights?: Registry }).highlights) || null;
const HighlightCtor = (): (new () => { add: (r: Range) => void }) | null => (typeof window !== "undefined" && (window as unknown as { Highlight?: new () => { add: (r: Range) => void } }).Highlight) || null;

/** Marks the open threads' passages (and the active one stronger). Returns reveal(id): scrolls a passage into view. */
export function useCommentHighlights(ref: RefObject<HTMLElement | null>, threads: Thread[], active: string | null, enabled: boolean) {
  const ranges = useRef(new Map<string, Range>());
  useEffect(() => {
    const root = ref.current, reg = registry(), H = HighlightCtor();
    if (!enabled || !root) return;
    let timer = 0;
    const apply = () => {
      ranges.current.clear();
      const nodes = textNodesOf(root);
      const texts = nodes.map((n) => n.data);
      const all = H && new H(), act = H && new H();
      for (const t of threads) {
        const a = t.root.anchor;
        if (!a || t.root.resolved || anchorState(t.root) !== "found") continue;
        const f = findQuote(texts, a.quote, a.prefix, a.suffix);
        if (!f) continue;
        const r = document.createRange();
        r.setStart(nodes[f.startNode], f.startOffset);
        r.setEnd(nodes[f.endNode], f.endOffset);
        ranges.current.set(t.root.id, r);
        (t.root.id === active ? act : all)?.add(r);
      }
      reg?.set("fc-comments", all); reg?.set("fc-active", act);
    };
    const later = () => { window.clearTimeout(timer); timer = window.setTimeout(apply, 60); };
    apply();
    const mo = new MutationObserver(later);
    mo.observe(root, { childList: true, subtree: true, characterData: true });
    return () => { window.clearTimeout(timer); mo.disconnect(); reg?.delete("fc-comments"); reg?.delete("fc-active"); };
  }, [ref, threads, active, enabled]);
  return useCallback((id: string) => {
    const r = ranges.current.get(id);
    r?.startContainer.parentElement?.scrollIntoView({ block: "center", behavior: "smooth" });
  }, []);
}

/** The button that appears over a selected passage. Sticky at the top of the document; a plain button, reachable with Tab. */
export function SelectionBar({ quote, onComment, onDismiss }: { quote: string | null; onComment: () => void; onDismiss: () => void }) {
  const { t } = useT();
  if (!quote) return null;
  return (
    // zero-height sticky wrapper: the bar floats over the top of the document instead of pushing the text down mid-selection
    <div data-fc-skip className="sticky top-0 z-10 h-0">
      <div data-testid="fc-selection-bar" className="absolute inset-x-0 top-0 flex items-center gap-2 border-b border-line bg-panel/95 px-3 py-1.5 text-[11.5px] shadow-sm backdrop-blur">
        <span className="min-w-0 flex-1 truncate text-ink2" title={quote}>{t("fcomments.selected")} <q className="text-ink">{shortQuote(quote, 90)}</q></span>
        <button type="button" data-testid="fc-comment-selection" onClick={onComment}
          className="inline-flex min-h-9 items-center gap-1 rounded-md border border-accent bg-accent px-2.5 font-semibold text-white hover:bg-accent-hover sm:min-h-0 sm:py-1">
          <Icon name="chat" size={12} />{t("fcomments.comment")}
        </button>
        <button type="button" aria-label={t("fcomments.dismiss")} onClick={onDismiss} className="min-h-9 rounded-md border border-line px-2 text-mute hover:text-ink sm:min-h-0 sm:py-1"><Icon name="x" size={12} /></button>
      </div>
    </div>
  );
}

export function CommentsPanel({ file, versions, shown, state, pending, onClearPending, onVersion, active, onActive, onReveal, composerRef }: {
  file: DriveFile; versions: DriveVersion[]; shown: number; state: FileCommentsState;
  pending: string | null; onClearPending: () => void; onVersion: (v: number) => void;
  active: string | null; onActive: (id: string | null) => void; onReveal: (id: string) => void;
  composerRef: RefObject<HTMLTextAreaElement | null>;
}) {
  const { t, locale } = useT();
  const name = useAgentName();
  const agents = useStore((s) => s.agents);
  const order = useStore((s) => s.agentOrder);
  const ids = useMemo(() => order.filter((id) => agents[id]), [order, agents]);
  const suggested = useMemo(() => pickEditAgent(versions, agents, order, fileKind(file.name, file.mime)), [versions, agents, order, file.name, file.mime]);
  const [picked, setPicked] = useState<string | null>(null);
  const agent = picked && agents[picked] ? picked : suggested;
  const [text, setText] = useState("");
  const [manual, setManual] = useState(false);
  const [manualQuote, setManualQuote] = useState("");
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");
  useEffect(() => { setText(""); setErr(""); setNote(""); setManual(false); setManualQuote(""); setPicked(null); }, [file.id]);

  const fail = (e: unknown) => {
    const code = e instanceof DriveError ? e.code : "unavailable";
    setErr(KNOWN_ERRORS.includes(code) ? t(`fcomments.err.${code}`) : errorText(e));
  };
  const quote = pending ?? (manual ? cleanQuote(manualQuote) : null);

  const submit = async (parent?: string, body = text, done?: () => void) => {
    const v = body.trim();
    if (!v || busy) return;
    setBusy(parent ?? "new"); setErr(""); setNote("");
    try {
      const c = await driveComments.add(file.id, { body: v, ...(parent ? { parent_id: parent } : quote ? { quote, version: shown } : {}) });
      await state.reload();
      if (!parent) { setText(""); onClearPending(); setManual(false); setManualQuote(""); onActive(c.id); }
      done?.();
    } catch (e) { fail(e); }
    setBusy("");
  };
  const resolve = async (c: FileComment, resolved: boolean) => {
    setBusy(c.id); setErr(""); setNote("");
    try { state.patch(await driveComments.resolve(file.id, c.id, resolved)); await state.reload(); } catch (e) { fail(e); }
    setBusy("");
  };
  const ask = async (c: FileComment) => {
    if (!agent) return;
    setBusy(c.id); setErr(""); setNote("");
    try {
      state.patch(await driveComments.requestChange(file.id, c.id, agent, locale));
      setNote(t("fcomments.asked", { name: name(agent).split(" ")[0] }));
    } catch (e) { fail(e); }
    setBusy("");
  };

  return (
    <aside aria-label={t("fcomments.panelLabel")} data-testid="fc-panel" className="flex max-h-[55%] min-h-0 flex-col border-t border-line bg-panel sm:max-h-none sm:w-[296px] sm:shrink-0 sm:border-l sm:border-t-0">
      <div className="flex items-center gap-2 border-b border-line px-3 py-2">
        <h3 className="text-[12px] font-semibold text-ink">{t("fcomments.title")}</h3>
        <span className="rounded-full bg-accent/10 px-1.5 text-[10.5px] font-semibold text-accent-hover" data-testid="fc-open-count">{state.open}</span>
        <span className="flex-1" />
        {ids.length > 0 && (
          <label className="flex items-center gap-1 text-[11px] text-mute">
            <span>{t("fcomments.agent")}</span>
            <select data-testid="fc-agent" value={agent ?? ""} onChange={(e) => setPicked(e.target.value)} className="max-w-[110px] rounded-md border border-line bg-panel2 px-1 py-0.5 text-ink outline-none focus:border-accent">
              {ids.map((id) => <option key={id} value={id}>{name(id)}</option>)}
            </select>
          </label>
        )}
      </div>

      <div className="min-h-0 flex-1 overflow-auto px-3 py-2">
        {state.loading && <p className="text-[11.5px] text-mute">{t("drive.loading")}</p>}
        {state.error && <p role="alert" className="text-[11.5px] text-err">{errorText({ code: state.error })}</p>}
        {!state.loading && !state.error && state.threads.length === 0 && <p className="text-[11.5px] leading-relaxed text-mute" data-testid="fc-empty">{t("fcomments.empty")}</p>}
        <ul className="flex flex-col gap-2.5">
          {state.threads.map((th) => (
            <ThreadItem key={th.root.id} th={th} active={active === th.root.id} busy={busy} agent={agent} agentName={agent ? name(agent) : ""} author={(c) => authorName(c, name, t)}
              onActive={() => { onActive(th.root.id); onReveal(th.root.id); }} onResolve={resolve} onAsk={ask} onReply={(body, done) => submit(th.root.id, body, done)} onVersion={onVersion} />
          ))}
        </ul>
      </div>

      <form onSubmit={(e) => { e.preventDefault(); submit(); }} className="flex flex-col gap-1.5 border-t border-line p-3">
        <label htmlFor="fc-composer" className="text-[11px] font-semibold text-ink2">{quote ? t("fcomments.composerQuote") : t("fcomments.composerFile")}</label>
        {quote && <blockquote data-testid="fc-pending-quote" className="max-h-16 overflow-auto rounded-md border-l-2 border-accent bg-accent/5 px-2 py-1 text-[11.5px] text-ink2">{shortQuote(quote, 220)}</blockquote>}
        {!pending && (manual
          ? <input aria-label={t("fcomments.manualLabel")} data-testid="fc-manual-quote" value={manualQuote} onChange={(e) => setManualQuote(e.target.value)} placeholder={t("fcomments.manualPlaceholder")}
              className="rounded-md border border-line bg-panel2 px-2 py-1 text-[12px] text-ink outline-none placeholder:text-mute focus:border-accent" />
          : <button type="button" data-testid="fc-manual" onClick={() => setManual(true)} className="self-start text-[11px] text-accent underline">{t("fcomments.manual")}</button>)}
        <textarea id="fc-composer" ref={composerRef} data-testid="fc-input" rows={2} maxLength={MAX_BODY} value={text} onChange={(e) => setText(e.target.value)} placeholder={t("fcomments.placeholder")}
          onKeyDown={(e) => { if ((e.metaKey || e.ctrlKey) && e.key === "Enter" && !e.nativeEvent.isComposing) { e.preventDefault(); submit(); } }}
          className="min-h-[52px] w-full resize-y rounded-lg border border-line bg-panel2 px-2.5 py-1.5 text-[12.5px] text-ink outline-none placeholder:text-mute focus:border-accent" />
        <div className="flex items-center gap-1.5">
          <button type="submit" data-testid="fc-send" disabled={!!busy || !text.trim()} className="rounded-md border border-accent bg-accent px-2.5 py-1.5 text-xs font-semibold text-white hover:bg-accent-hover disabled:opacity-40">{t("fcomments.send")}</button>
          {(pending || manual) && <button type="button" onClick={() => { onClearPending(); setManual(false); setManualQuote(""); }} className="rounded-md border border-line px-2.5 py-1.5 text-xs font-medium text-ink2 hover:border-accent">{t("fcomments.cancelQuote")}</button>}
          <span className="ml-auto text-[10.5px] text-mute">{t("fcomments.hint")}</span>
        </div>
      </form>
      <div role="status" aria-live="polite" className="px-3 pb-2 text-[11px] empty:hidden">
        {note && <span className="text-ok" data-testid="fc-note">{note}</span>}
        {err && <span role="alert" className="text-err" data-testid="fc-error">{err}</span>}
      </div>
    </aside>
  );
}

function authorName(c: FileComment, name: (id: string) => string, t: (k: string) => string) {
  return c.author.kind === "agent" ? name(c.author.id) : t("drive.person");
}

function ThreadItem({ th, active, busy, agent, agentName, author, onActive, onResolve, onAsk, onReply, onVersion }: {
  th: Thread; active: boolean; busy: string; agent: string | null; agentName: string; author: (c: FileComment) => string;
  onActive: () => void; onResolve: (c: FileComment, r: boolean) => void; onAsk: (c: FileComment) => void;
  onReply: (body: string, done: () => void) => void; onVersion: (v: number) => void;
}) {
  const { t } = useT();
  const name = useAgentName();
  const c = th.root;
  const state = anchorState(c);
  const [reply, setReply] = useState("");
  const [replying, setReplying] = useState(false);
  const byAgent = c.resolved_by?.startsWith("agent:") ? c.resolved_by.slice(6) : null;
  return (
    <li data-testid="fc-thread" data-state={state} data-resolved={c.resolved ? "1" : "0"}
      className={`rounded-lg border p-2.5 text-[12px] ${active ? "border-accent bg-accent/5" : "border-line bg-panel"} ${c.resolved ? "opacity-80" : ""}`}>
      {c.anchor && (
        <button type="button" onClick={onActive} disabled={state === "changed"} aria-label={t("fcomments.goTo")} data-testid="fc-goto"
          className="mb-1.5 block w-full rounded-md border-l-2 border-accent/60 bg-panel2 px-2 py-1 text-left text-[11.5px] text-ink2 hover:bg-accent/5 disabled:cursor-default disabled:hover:bg-panel2">
          {shortQuote(c.anchor.quote, 160)}
        </button>
      )}
      {state === "changed" && <p data-testid="fc-changed" className="mb-1.5 inline-flex items-center gap-1 rounded-full bg-warn/10 px-2 py-0.5 text-[10.5px] font-semibold text-warn"><Icon name="alert" size={11} />{t("fcomments.changed")}</p>}
      <Message c={c} who={author(c)} />
      {th.replies.map((r) => <div key={r.id} className="mt-1.5 border-l border-line pl-2"><Message c={r} who={author(r)} /></div>)}

      {c.change && !c.resolved && <p data-testid="fc-asked" className="mt-1.5 text-[11px] text-accent-hover">{t("fcomments.waiting", { name: name(c.change.agent_id).split(" ")[0], v: c.change.base_version })}</p>}
      {c.resolved && (
        <p data-testid="fc-resolved" className="mt-1.5 flex flex-wrap items-center gap-1 text-[11px] text-ok">
          <Icon name="check" size={11} />
          {byAgent ? t("fcomments.resolvedBy", { name: name(byAgent).split(" ")[0] }) : t("fcomments.resolved")}
          {c.resolved_version ? <button type="button" data-testid="fc-version-link" onClick={() => onVersion(c.resolved_version!)} className="font-mono text-accent underline">{t("fcomments.seeVersion", { v: c.resolved_version })}</button> : null}
        </p>
      )}

      <div className="mt-2 flex flex-wrap items-center gap-1.5">
        {canAskChange(c) && agent && (
          <button type="button" data-testid="fc-ask" disabled={busy === c.id} onClick={() => onAsk(c)}
            className="rounded-md border border-accent bg-accent px-2 py-1 text-[11px] font-semibold text-white hover:bg-accent-hover disabled:opacity-40">{t("fcomments.ask", { name: agentName.split(" ")[0] })}</button>
        )}
        <button type="button" data-testid="fc-resolve" disabled={busy === c.id} onClick={() => onResolve(c, !c.resolved)}
          className="rounded-md border border-line px-2 py-1 text-[11px] font-medium text-ink2 hover:border-accent disabled:opacity-40">{c.resolved ? t("fcomments.reopen") : t("fcomments.resolve")}</button>
        {!replying && <button type="button" data-testid="fc-reply-open" onClick={() => setReplying(true)} className="rounded-md px-2 py-1 text-[11px] font-medium text-accent hover:underline">{t("fcomments.reply")}</button>}
      </div>
      {replying && (
        <form className="mt-1.5 flex flex-col gap-1" onSubmit={(e) => { e.preventDefault(); onReply(reply, () => { setReply(""); setReplying(false); }); }}>
          <textarea aria-label={t("fcomments.reply")} data-testid="fc-reply-input" rows={2} autoFocus maxLength={MAX_BODY} value={reply} onChange={(e) => setReply(e.target.value)}
            className="w-full resize-y rounded-md border border-line bg-panel2 px-2 py-1 text-[12px] text-ink outline-none focus:border-accent" />
          <div className="flex gap-1.5">
            <button type="submit" data-testid="fc-reply-send" disabled={!reply.trim() || !!busy} className="rounded-md border border-accent bg-accent px-2 py-1 text-[11px] font-semibold text-white disabled:opacity-40">{t("fcomments.send")}</button>
            <button type="button" onClick={() => { setReplying(false); setReply(""); }} className="rounded-md border border-line px-2 py-1 text-[11px] text-ink2">{t("fileviewer.cancel")}</button>
          </div>
        </form>
      )}
    </li>
  );
}

function Message({ c, who }: { c: FileComment; who: string }) {
  return (
    <div>
      <div className="flex items-baseline gap-1.5 text-[10.5px] text-mute"><span className="font-semibold text-ink2">{who}</span><time dateTime={c.created_at}>{fmtDateTime(c.created_at)}</time></div>
      <p className="whitespace-pre-wrap break-words text-[12.5px] leading-snug text-ink">{c.body}</p>
    </div>
  );
}
