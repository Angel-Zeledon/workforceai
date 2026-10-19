"use client";
// The open people channel: header with its members (people and agents), the messages with highlighted @mentions,
// and the composer with @autocomplete over the people and agents of the channel.
import { useEffect, useMemo, useRef, useState } from "react";
import { useT, intlLocale } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { usePeopleChat, mentionablesOf, type PChannel } from "./peopleChatStore";
import { useChannelTitle } from "./PeopleChatsList";
import { insertMention, mentionQuery, splitMentions, suggest, type Mentionable, type PMessage } from "./peopleLogic";

function Body({ text, all, me }: { text: string; all: Mentionable[]; me: string }) {
  const parts = useMemo(() => splitMentions(text, all), [text, all]);
  return (
    <p className="whitespace-pre-wrap break-words text-[13px] leading-relaxed text-ink">
      {parts.map((p, i) => p.mention
        ? <span key={i} data-mention={p.mention.kind} className={`rounded px-1 font-medium ${p.mention.kind === "user" && p.mention.id === me ? "bg-warn/20 text-ink" : "bg-accent-soft text-accent-hover"}`}>{p.text}</span>
        : <span key={i}>{p.text}</span>)}
    </p>
  );
}

function Composer({ channel, all, disabled }: { channel: PChannel; all: Mentionable[]; disabled: boolean }) {
  const { t } = useT();
  const send = usePeopleChat((s) => s.send);
  const [text, setText] = useState("");
  const [caret, setCaret] = useState(0);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  const [sel, setSel] = useState(0);
  const ref = useRef<HTMLTextAreaElement>(null);
  const q = mentionQuery(text, caret);
  const options = useMemo(() => (q ? suggest(q.query, all) : []), [q?.query, all]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => setSel(0), [q?.query]);
  const choose = (m: Mentionable) => {
    if (!q) return;
    const r = insertMention(text, caret, q.start, m);
    setText(r.text);
    setCaret(r.caret);
    requestAnimationFrame(() => { const el = ref.current; if (el) { el.focus(); el.setSelectionRange(r.caret, r.caret); } });
  };
  const submit = async () => {
    const body = text.trim();
    if (!body || busy || disabled) return;
    setBusy(true); setFailed(false);
    try { await send(channel.id, body); setText(""); setCaret(0); } catch { setFailed(true); } finally { setBusy(false); }
  };
  if (disabled) return <div data-testid="pchat-readonly" className="border-t border-line bg-panel px-4 py-3 text-[11.5px] text-mute">{t("pchat.readonly")}</div>;
  return (
    <div className="relative border-t border-line bg-panel p-3">
      {options.length > 0 && (
        <ul role="listbox" aria-label={t("pchat.mention")} data-testid="pchat-mentions" className="absolute bottom-full left-3 mb-1 w-64 overflow-hidden rounded-lg border border-line-strong bg-panel shadow-float">
          {options.map((m, i) => (
            <li key={`${m.kind}:${m.id}`} role="option" aria-selected={i === sel}>
              <button type="button" onMouseDown={(e) => { e.preventDefault(); choose(m); }}
                className={`flex w-full items-center gap-2 px-3 py-1.5 text-left text-[12.5px] ${i === sel ? "bg-accent-soft" : "hover:bg-panel2"}`}>
                <span className="min-w-0 flex-1 truncate text-ink">{m.name}</span>
                {m.kind === "agent" && <span className="rounded bg-mute/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-mute">{t("pchat.agent.badge")}</span>}
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="flex items-end gap-2">
        <textarea
          ref={ref} data-testid="pchat-input" rows={1} value={text} maxLength={4000} placeholder={t("pchat.placeholder")}
          onChange={(e) => { setText(e.target.value); setCaret(e.target.selectionStart ?? e.target.value.length); }}
          onSelect={(e) => setCaret((e.target as HTMLTextAreaElement).selectionStart ?? 0)}
          onKeyDown={(e) => {
            if (options.length > 0) {
              if (e.key === "ArrowDown") { e.preventDefault(); setSel((s) => (s + 1) % options.length); return; }
              if (e.key === "ArrowUp") { e.preventDefault(); setSel((s) => (s - 1 + options.length) % options.length); return; }
              if (e.key === "Enter" || e.key === "Tab") { e.preventDefault(); choose(options[sel]); return; }
              if (e.key === "Escape") { e.preventDefault(); setCaret(0); return; }
            }
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); void submit(); }
          }}
          className="max-h-32 min-h-[40px] flex-1 resize-none rounded-lg border border-line bg-panel2 px-3 py-2 text-[13px] text-ink outline-none placeholder:text-mute focus:border-accent"
        />
        <button type="button" data-testid="pchat-send" disabled={busy || !text.trim()} onClick={() => void submit()}
          className="rounded-lg bg-accent px-4 py-2 text-[12.5px] font-semibold text-white disabled:opacity-40">{t("pchat.send")}</button>
      </div>
      {failed && <p role="alert" className="mt-1 text-[11.5px] text-err">{t("pchat.error")}</p>}
    </div>
  );
}

function Members({ channel, all, canManage }: { channel: PChannel; all: Mentionable[]; canManage: boolean }) {
  const { t } = useT();
  const dir = usePeopleChat((s) => s.dir);
  const add = usePeopleChat((s) => s.addMember);
  const remove = usePeopleChat((s) => s.removeMember);
  const [open, setOpen] = useState(false);
  const have = new Set(channel.members.map((m) => `${m.kind}:${m.id}`));
  const addable = [
    ...dir.people.filter((p) => !have.has(`user:${p.id}`)).map((p) => ({ kind: "user" as const, id: p.id, name: p.name })),
    ...dir.agents.filter((a) => !have.has(`agent:${a.id}`)).map((a) => ({ kind: "agent" as const, id: a.id, name: a.name })),
  ];
  const nameOf = (kind: string, id: string) => all.find((m) => m.kind === kind && m.id === id)?.name ?? id;
  return (
    <div className="relative">
      <button type="button" data-testid="pchat-members-btn" aria-expanded={open} onClick={() => setOpen((v) => !v)}
        className="rounded-md border border-line px-2 py-1 text-[11.5px] font-medium text-ink2 hover:border-accent hover:text-ink">{t("pchat.members", { n: channel.members.length })}</button>
      {open && (
        <div className="absolute right-0 z-30 mt-1 w-72 rounded-xl border border-line-strong bg-panel p-2 shadow-float">
          <ul className="max-h-56 overflow-y-auto">
            {channel.members.map((m) => (
              <li key={`${m.kind}:${m.id}`} className="flex items-center gap-2 rounded px-2 py-1 text-[12.5px] text-ink hover:bg-panel2">
                <span className="min-w-0 flex-1 truncate">{nameOf(m.kind, m.id)}</span>
                {m.kind === "agent" && <span className="rounded bg-mute/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-mute">{t("pchat.agent.badge")}</span>}
                {canManage && m.role !== "owner" && <button type="button" aria-label={t("pchat.remove")} onClick={() => void remove(channel.id, m.kind, m.id)} className="text-mute hover:text-err">×</button>}
              </li>
            ))}
          </ul>
          {canManage && addable.length > 0 && (
            <select aria-label={t("pchat.addMember")} defaultValue="" data-testid="pchat-add-member"
              onChange={(e) => { const [k, ...r] = e.target.value.split(":"); if (k) void add(channel.id, { kind: k as "user" | "agent", id: r.join(":") }); e.target.value = ""; }}
              className="mt-2 w-full rounded-lg border border-line bg-panel2 px-2 py-1.5 text-[12px] text-ink">
              <option value="">{t("pchat.addMember")}</option>
              {addable.map((m) => <option key={`${m.kind}:${m.id}`} value={`${m.kind}:${m.id}`}>{m.name}{m.kind === "agent" ? ` (${t("pchat.agent.badge")})` : ""}</option>)}
            </select>
          )}
        </div>
      )}
    </div>
  );
}

export function PeopleChatPane({ onBack }: { onBack: () => void }) {
  const { t } = useT();
  const activeId = usePeopleChat((s) => s.activeId);
  const channel = usePeopleChat((s) => s.channels.find((c) => c.id === activeId));
  const msgs = usePeopleChat((s) => (activeId ? s.messages[activeId] : undefined));
  const more = usePeopleChat((s) => (activeId ? s.more[activeId] : false));
  const loadOlder = usePeopleChat((s) => s.loadOlder);
  const dir = usePeopleChat((s) => s.dir);
  const title = useChannelTitle();
  const canPost = useCan("conversations:write");
  const canManageRole = useCan("members:manage");
  const all = useMemo(() => mentionablesOf(channel, dir), [channel, dir]);
  const endRef = useRef<HTMLDivElement>(null);
  const last = msgs?.length ? msgs[msgs.length - 1].seq : 0;
  useEffect(() => { endRef.current?.scrollIntoView({ block: "end" }); }, [activeId, last]);
  if (!channel) return null;
  const owner = channel.members.some((m) => m.kind === "user" && m.id === dir.self && m.role === "owner");
  const canManage = channel.kind !== "dm" && channel.kind !== "org" && (owner || canManageRole);
  const nameOf = (m: PMessage) => (m.author_kind === "user" ? dir.people.find((p) => p.id === m.author_id)?.name : dir.agents.find((a) => a.id === m.author_id)?.name) ?? m.author_id;
  const fmt = (iso: string) => { try { return new Date(iso).toLocaleTimeString(intlLocale(), { hour: "2-digit", minute: "2-digit" }); } catch { return ""; } };
  return (
    <section aria-label={title(channel)} data-testid="pchat-pane" className="flex min-h-0 min-w-0 flex-1 flex-col bg-bg">
      <header className="flex items-center gap-3 border-b border-line bg-panel px-4 py-2.5">
        <button type="button" onClick={onBack} aria-label={t("chats.back")} className="rounded-md p-1.5 text-ink2 hover:bg-panel2 md:hidden">←</button>
        <div className="min-w-0 flex-1">
          <h2 data-testid="pchat-title" className="truncate text-[14px] font-semibold text-ink">{channel.kind === "dm" ? "" : "# "}{title(channel)}</h2>
          <p className="truncate text-[11.5px] text-mute">{t(`pchat.kind.${channel.kind}`)}</p>
        </div>
        {channel.kind !== "dm" && <Members channel={channel} all={all} canManage={canManage} />}
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3" data-testid="pchat-messages">
        {more && <button type="button" onClick={() => void loadOlder(channel.id)} className="mx-auto mb-3 block text-[11.5px] font-medium text-accent-hover hover:underline">{t("pchat.older")}</button>}
        {msgs && msgs.length === 0 && <p className="py-10 text-center text-xs text-mute">{t("pchat.empty")}</p>}
        <ul className="space-y-3">
          {(msgs ?? []).map((m) => (
            <li key={m.id} data-testid="pchat-msg" className="flex gap-2.5">
              <span aria-hidden className={`flex h-8 w-8 shrink-0 items-center justify-center text-[12px] font-semibold ${m.author_kind === "agent" ? "rounded-md bg-panel2 text-ink2" : "rounded-full bg-accent-soft text-ink"}`}>{nameOf(m).slice(0, 1).toUpperCase()}</span>
              <div className="min-w-0 flex-1">
                <div className="flex items-baseline gap-2">
                  <span className="text-[12.5px] font-semibold text-ink">{nameOf(m)}</span>
                  {m.author_kind === "agent" && <span className="rounded bg-mute/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-mute">{t("pchat.agent.badge")}</span>}
                  <time className="text-[10.5px] text-mute" dateTime={m.created_at}>{fmt(m.created_at)}</time>
                </div>
                <Body text={m.body} all={all} me={dir.self} />
              </div>
            </li>
          ))}
        </ul>
        <div ref={endRef} />
      </div>
      <Composer key={channel.id} channel={channel} all={all} disabled={!canPost || channel.archived} />
    </section>
  );
}
