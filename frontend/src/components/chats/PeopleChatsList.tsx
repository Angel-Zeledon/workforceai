"use client";
// The "Team" section of the chats sidebar: the all-hands channel, department / project channels, groups and DMs
// between people, each with its unread badge. "New" opens a DM or creates a group / department / project channel.
import { useEffect, useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { usePresence } from "../presence/store";
import { STATUS_COLOR } from "../presence/PeopleLayer";
import { usePeopleChat, type PChannel } from "./peopleChatStore";
import { badge } from "./peopleLogic";

/** Display name of a channel for the person looking at it (a DM is named after the other person). */
export function useChannelTitle() {
  const { t } = useT();
  const dir = usePeopleChat((s) => s.dir);
  return (c: PChannel) => {
    if (c.kind === "org") return t("pchat.org");
    if (c.kind === "dm") {
      const other = c.members.find((m) => m.kind === "user" && m.id !== dir.self);
      return dir.people.find((p) => p.id === other?.id)?.name ?? t("pchat.dm");
    }
    return c.name;
  };
}

function Row({ c, active, onPick }: { c: PChannel; active: boolean; onPick: () => void }) {
  const { t } = useT();
  const title = useChannelTitle()(c);
  const dir = usePeopleChat((s) => s.dir);
  const people = usePresence((s) => s.people);
  const counter = usePeopleChat((s) => s.counters[c.id]);
  const unread = counter?.unread ?? c.unread;
  const mentions = counter?.mentions ?? c.mentions_unread;
  const other = c.kind === "dm" ? c.members.find((m) => m.kind === "user" && m.id !== dir.self) : undefined;
  const online = other ? people[other.id] : undefined;
  const glyph = c.kind === "org" ? "#" : c.kind === "dm" ? title.slice(0, 1).toUpperCase() : c.kind === "project" ? "P" : c.kind === "department" ? "D" : "G";
  return (
    <button type="button" data-testid="pchat-row" data-channel={c.id} aria-current={active} onClick={onPick}
      className={`flex w-full items-center gap-2.5 rounded-lg px-2 py-2 text-left transition-colors ${active ? "bg-accent-soft" : "hover:bg-panel2"}`}>
      <span aria-hidden className="relative flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-mute/10 text-[13px] font-semibold text-ink2">
        {glyph}
        {online && <span className="absolute -bottom-0.5 -right-0.5 h-3 w-3 rounded-full border-2 border-panel" style={{ background: STATUS_COLOR[online.status] }} />}
      </span>
      <span className="min-w-0 flex-1">
        <span className={`block truncate text-[13px] ${unread ? "font-semibold text-ink" : "font-medium text-ink"}`}>{title}</span>
        <span className="block truncate text-[11.5px] text-mute">{c.preview || (c.kind === "dm" ? t("pchat.dm.sub") : t("pchat.members", { n: Math.max(c.members.length, 1) }))}</span>
      </span>
      {unread > 0 && (
        <span data-testid="pchat-unread" aria-label={t("pchat.unread", { n: unread })} className={`min-w-[20px] rounded-full px-1.5 py-0.5 text-center text-[10.5px] font-semibold text-white ${mentions ? "bg-err" : "bg-accent"}`}>
          {mentions ? "@" : ""}{badge(unread)}
        </span>
      )}
    </button>
  );
}

function NewDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (id: string) => void }) {
  const { t } = useT();
  const dir = usePeopleChat((s) => s.dir);
  const openDM = usePeopleChat((s) => s.openDM);
  const create = usePeopleChat((s) => s.create);
  const err = usePeopleChat((s) => s.error);
  const canShared = useCan("members:manage");
  const canPost = useCan("conversations:write");
  const [mode, setMode] = useState<"dm" | "group" | "department" | "project">(canPost ? "dm" : "group");
  const [name, setName] = useState("");
  const [scope, setScope] = useState("");
  const [picked, setPicked] = useState<Record<string, boolean>>({});
  const others = dir.people.filter((p) => p.id !== dir.self);
  const modes: ("dm" | "group" | "department" | "project")[] = !canPost ? [] : canShared ? ["dm", "group", "department", "project"] : ["dm", "group"];
  const members = Object.keys(picked).filter((k) => picked[k]).map((k) => ({ kind: k.startsWith("a:") ? ("agent" as const) : ("user" as const), id: k.slice(2) }));
  const valid = mode === "dm" ? members.length === 1 && members[0].kind === "user" : name.trim().length > 0 && (mode === "group" || scope.trim().length > 0);
  const submit = async () => {
    const id = mode === "dm" ? await openDM(members[0].id) : await create({ kind: mode, name: name.trim(), scope_key: mode === "group" ? undefined : scope.trim(), members });
    if (id) onCreated(id);
  };
  const toggle = (key: string) => setPicked((p) => (mode === "dm" ? { [key]: !p[key] } : { ...p, [key]: !p[key] }));
  return (
    <div role="dialog" aria-modal="true" aria-label={t("pchat.new")} data-testid="pchat-new" className="absolute inset-0 z-40 flex items-center justify-center bg-black/30 p-4" onClick={onClose}>
      <div className="max-h-full w-full max-w-sm overflow-y-auto rounded-xl border border-line-strong bg-panel p-4 shadow-float" onClick={(e) => e.stopPropagation()}>
        <h2 className="mb-3 text-[14px] font-semibold text-ink">{t("pchat.new")}</h2>
        <div role="tablist" className="mb-3 flex flex-wrap gap-1.5">
          {modes.map((m) => (
            <button key={m} type="button" role="tab" aria-selected={mode === m} onClick={() => { setMode(m); setPicked({}); }}
              className={`rounded-full border px-3 py-1 text-[11.5px] font-medium ${mode === m ? "border-accent bg-accent-soft text-ink" : "border-line text-ink2 hover:bg-panel2"}`}>{t(`pchat.new.${m}`)}</button>
          ))}
        </div>
        {mode !== "dm" && (
          <label className="mb-2 block text-[11.5px] font-medium text-ink2">{t("pchat.name")}
            <input value={name} maxLength={80} onChange={(e) => setName(e.target.value)} placeholder={t("pchat.name.placeholder")}
              className="mt-1 w-full rounded-lg border border-line bg-panel2 px-2.5 py-2 text-[12.5px] text-ink outline-none focus:border-accent" />
          </label>
        )}
        {(mode === "department" || mode === "project") && (
          <label className="mb-2 block text-[11.5px] font-medium text-ink2">{mode === "department" ? t("pchat.scope.department") : t("pchat.scope.project")}
            <input value={scope} maxLength={80} onChange={(e) => setScope(e.target.value)}
              className="mt-1 w-full rounded-lg border border-line bg-panel2 px-2.5 py-2 text-[12.5px] text-ink outline-none focus:border-accent" />
          </label>
        )}
        <p className="mb-1 text-[11.5px] font-medium text-ink2">{mode === "dm" ? t("pchat.pick.person") : t("pchat.pick.members")}</p>
        <ul className="max-h-56 overflow-y-auto rounded-lg border border-line">
          {others.map((p) => (
            <li key={p.id}>
              <label className="flex cursor-pointer items-center gap-2 px-2.5 py-1.5 text-[12.5px] text-ink hover:bg-panel2">
                <input type={mode === "dm" ? "radio" : "checkbox"} name="pchat-pick" checked={!!picked[`u:${p.id}`]} onChange={() => toggle(`u:${p.id}`)} />
                <span className="min-w-0 flex-1 truncate">{p.name}</span>
              </label>
            </li>
          ))}
          {mode !== "dm" && dir.agents.map((a) => (
            <li key={a.id}>
              <label className="flex cursor-pointer items-center gap-2 px-2.5 py-1.5 text-[12.5px] text-ink hover:bg-panel2">
                <input type="checkbox" checked={!!picked[`a:${a.id}`]} onChange={() => toggle(`a:${a.id}`)} />
                <span className="min-w-0 flex-1 truncate">{a.name}</span>
                <span className="rounded bg-mute/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase text-mute">{t("pchat.agent.badge")}</span>
              </label>
            </li>
          ))}
          {others.length === 0 && <li className="px-2.5 py-3 text-[12px] text-mute">{t("pchat.noPeople")}</li>}
        </ul>
        {err && <p role="alert" className="mt-2 text-[11.5px] text-err">{t("pchat.error")}</p>}
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" onClick={onClose} className="rounded-lg border border-line px-3 py-1.5 text-[12px] font-medium text-ink2 hover:bg-panel2">{t("pchat.cancel")}</button>
          <button type="button" data-testid="pchat-create" disabled={!valid} onClick={() => void submit()}
            className="rounded-lg bg-accent px-3 py-1.5 text-[12px] font-semibold text-white disabled:opacity-40">{t("pchat.create")}</button>
        </div>
      </div>
    </div>
  );
}

export function PeopleChatsList({ onPicked }: { onPicked?: () => void }) {
  const { t } = useT();
  const channels = usePeopleChat((s) => s.channels);
  const loaded = usePeopleChat((s) => s.loaded);
  const activeId = usePeopleChat((s) => s.activeId);
  const load = usePeopleChat((s) => s.loadChannels);
  const open = usePeopleChat((s) => s.open);
  const canPost = useCan("conversations:write");
  const [creating, setCreating] = useState(false);
  useEffect(() => { void load(); void usePeopleChat.getState().loadDirectory(); }, [load]);
  const sorted = useMemo(() => [...channels], [channels]);
  const pick = (id: string) => { void open(id); onPicked?.(); };
  return (
    <section aria-label={t("pchat.title")} data-testid="pchat-list" className="mt-4">
      <div className="flex items-center justify-between px-2 pb-1">
        <h2 className="text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute">{t("pchat.title")}</h2>
        {canPost && <button type="button" data-testid="pchat-new-btn" onClick={() => setCreating(true)} className="text-[11px] font-medium text-accent-hover hover:underline">{t("pchat.new")}</button>}
      </div>
      {!loaded ? <p className="px-3 py-3 text-xs text-mute">…</p> : sorted.length === 0 ? (
        <p className="rounded-xl border border-dashed border-line-strong px-3 py-4 text-center text-xs leading-relaxed text-mute">{t("pchat.empty.list")}</p>
      ) : sorted.map((c) => <Row key={c.id} c={c} active={activeId === c.id} onPick={() => pick(c.id)} />)}
      {creating && <NewDialog onClose={() => setCreating(false)} onCreated={(id) => { setCreating(false); pick(id); }} />}
    </section>
  );
}
