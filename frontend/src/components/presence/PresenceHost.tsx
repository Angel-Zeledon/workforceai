"use client";
// Overlays of human presence over the office: the pill with who is online, the presence panel (people and agents:
// where they are and what they are doing), the status menu and the "start conversation" offer when someone is near.
import { useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { stateMeta } from "@/lib/meta";
import { useAgentName } from "../ui";
import { usePeopleChat } from "../chats/peopleChatStore";
import { STATUS_COLOR } from "./PeopleLayer";
import { usePresence, type Person } from "./store";
import { startConversation, type ConversationRequest } from "./tap";
import { STATUSES, nearby, type NearInfo, type PStatus } from "./logic";

/** Status labels (also used by the 3D name tags' tooltips). */
function useStatusLabel() {
  const { t } = useT();
  return (s: PStatus, auto?: boolean) => (s === "away" && auto ? t("presence.status.auto") : t(`presence.status.${s}`));
}

/** People near me, with hysteresis. Also announces changes on window ("aiw:presence:proximity") as a hook point. */
export function useNearby(): NearInfo[] {
  const people = usePresence((s) => s.people);
  const selfId = usePresence((s) => s.selfId);
  const [list, setList] = useState<NearInfo[]>([]);
  const prev = useRef(new Set<string>());
  useEffect(() => {
    const me = selfId ? people[selfId] : undefined;
    if (!me) { if (prev.current.size) { prev.current = new Set(); setList([]); } return; }
    const others: Record<string, Person> = {};
    for (const [k, v] of Object.entries(people)) if (k !== selfId) others[k] = v;
    const next = nearby(me, others, prev.current);
    const ids = new Set(next.map((n) => n.id));
    for (const n of next) if (!prev.current.has(n.id)) window.dispatchEvent(new CustomEvent("aiw:presence:proximity", { detail: { userId: n.id, near: true, distance: n.distance, canTalk: n.canTalk } }));
    for (const id of prev.current) if (!ids.has(id)) window.dispatchEvent(new CustomEvent("aiw:presence:proximity", { detail: { userId: id, near: false, distance: 0, canTalk: false } }));
    prev.current = ids;
    setList((old) => (old.length === next.length && old.every((o, i) => o.id === next[i].id && o.canTalk === next[i].canTalk) ? old : next));
  }, [people, selfId]);
  return list;
}

/** Default behaviour of "start conversation": open the DM with that person in the chats screen. */
function useOpenDM() {
  const router = useRouter();
  return async (userId: string) => {
    const id = await usePeopleChat.getState().openDM(userId);
    if (!id) return;
    await usePeopleChat.getState().open(id);
    router.push("/chats");
  };
}

function Dot({ status, auto }: { status: PStatus; auto?: boolean }) {
  const label = useStatusLabel();
  return <span title={label(status, auto)} aria-label={label(status, auto)} className="inline-block h-2.5 w-2.5 shrink-0 rounded-full" style={{ background: STATUS_COLOR[status] }} />;
}

function StatusPicker() {
  const { t } = useT();
  const label = useStatusLabel();
  const me = usePresence((s) => (s.selfId ? s.people[s.selfId] : undefined));
  const set = usePresence((s) => s.setMyStatus);
  if (!me) return null;
  return (
    <div role="radiogroup" aria-label={t("presence.status.set")} className="grid grid-cols-2 gap-1.5">
      {STATUSES.map((s) => (
        <button key={s} type="button" role="radio" aria-checked={me.status === s} data-testid={`presence-status-${s}`} onClick={() => void set(s)}
          className={`flex items-center gap-2 rounded-lg border px-2.5 py-1.5 text-left text-[11.5px] font-medium transition-colors ${me.status === s ? "border-accent bg-accent-soft text-ink" : "border-line text-ink2 hover:bg-panel2"}`}>
          <Dot status={s} />
          <span className="min-w-0 truncate">{label(s)}</span>
        </button>
      ))}
    </div>
  );
}

function PersonRow({ p, self, onMessage }: { p: Person; self: boolean; onMessage: () => void }) {
  const { t } = useT();
  const label = useStatusLabel();
  const zone = p.zone === "meeting" ? t("presence.zone.meeting") : p.zone === "approvals" ? t("presence.zone.approvals") : p.zone === "cafe" ? t("presence.zone.cafe") : t("presence.zone.floor");
  return (
    <li className="flex items-center gap-2.5 rounded-lg px-2 py-1.5 hover:bg-panel2" data-testid="presence-row">
      <span aria-hidden className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-accent-soft text-[11px] font-semibold text-ink">{p.name.slice(0, 1).toUpperCase()}</span>
      <div className="min-w-0 flex-1">
        <div className="truncate text-[12.5px] font-semibold text-ink">{p.name}{self ? <span className="ml-1 font-normal text-mute">{t("presence.you")}</span> : null}</div>
        <div className="flex items-center gap-1.5 truncate text-[11px] text-mute"><Dot status={p.status} auto={p.auto_away} />{label(p.status, p.auto_away)} · {zone}</div>
      </div>
      {!self && (
        <button type="button" onClick={onMessage} aria-label={t("presence.message")} title={t("presence.message")}
          className="rounded-md border border-line px-2 py-1 text-[11px] font-medium text-ink2 hover:border-accent hover:text-ink">{t("presence.message")}</button>
      )}
    </li>
  );
}

function AgentRows() {
  const { t } = useT();
  const agents = useStore((s) => s.agents);
  const order = useStore((s) => s.agentOrder);
  const name = useAgentName();
  const ids = order.length ? order : Object.keys(agents);
  if (!ids.length) return null;
  return (
    <>
      <h3 className="px-2 pb-1 pt-3 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute">{t("presence.agents")}</h3>
      <ul>
        {ids.map((id) => {
          const a = agents[id];
          if (!a) return null;
          const sm = stateMeta(a.state);
          return (
            <li key={id} className="flex items-center gap-2.5 rounded-lg px-2 py-1.5 hover:bg-panel2" data-testid="presence-agent-row">
              <span aria-hidden className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-panel2 text-[11px] font-semibold text-ink2">{name(id).slice(0, 1)}</span>
              <div className="min-w-0 flex-1">
                <div className="truncate text-[12.5px] font-semibold text-ink">{name(id)}</div>
                <div className="flex items-center gap-1.5 truncate text-[11px] text-mute"><span aria-hidden className="inline-block h-2 w-2 shrink-0 rounded-full" style={{ background: sm.color }} />{sm.label}{a.activity ? ` · ${a.activity}` : ""}</div>
              </div>
            </li>
          );
        })}
      </ul>
    </>
  );
}

export function PresencePanel({ onClose }: { onClose: () => void }) {
  const { t } = useT();
  const people = usePresence((s) => s.people);
  const selfId = usePresence((s) => s.selfId);
  const openDM = useOpenDM();
  const list = useMemo(() => Object.values(people).sort((a, b) => (a.user_id === selfId ? -1 : b.user_id === selfId ? 1 : a.name.localeCompare(b.name))), [people, selfId]);
  return (
    <section data-testid="presence-panel" aria-label={t("presence.title")} className="pointer-events-auto w-[300px] max-w-[calc(100vw-32px)] rounded-xl border border-line bg-panel/95 p-3 shadow-float backdrop-blur">
      <div className="mb-2 flex items-center justify-between">
        <h2 className="text-[13px] font-semibold text-ink">{t("presence.title")}</h2>
        <button type="button" onClick={onClose} aria-label={t("presence.close")} className="rounded-md px-2 py-0.5 text-sm text-mute hover:bg-panel2 hover:text-ink">×</button>
      </div>
      <StatusPicker />
      <p className="mt-2 text-[10.5px] leading-snug text-mute">{t("presence.walkHint")}</p>
      <h3 className="px-2 pb-1 pt-3 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute">{t("presence.people")}</h3>
      <div className="max-h-[46vh] overflow-y-auto">
        {list.length <= 1 && <p className="px-2 pb-1 text-[11.5px] text-mute">{t("presence.none")}</p>}
        <ul>
          {list.map((p) => <PersonRow key={p.user_id} p={p} self={p.user_id === selfId} onMessage={() => void openDM(p.user_id)} />)}
        </ul>
        <AgentRows />
      </div>
    </section>
  );
}

/** The offer shown when someone available is within reach. */
function ProximityPrompt() {
  const { t } = useT();
  const near = useNearby();
  const people = usePresence((s) => s.people);
  const openDM = useOpenDM();
  const first = near.find((n) => n.canTalk);
  if (!first) return null;
  const p = people[first.id];
  if (!p) return null;
  const req: ConversationRequest = { withUserId: p.user_id, withName: p.name, distance: first.distance };
  return (
    <div role="status" data-testid="presence-proximity" className="pointer-events-auto flex items-center gap-3 rounded-full border border-line bg-panel/95 py-1.5 pl-4 pr-1.5 text-[12px] shadow-float backdrop-blur">
      <span className="text-ink2">{t("presence.near", { name: p.name })}</span>
      <button type="button" data-testid="presence-talk" onClick={() => startConversation(req, (r) => void openDM(r.withUserId))}
        className="rounded-full bg-accent px-3 py-1 text-[11.5px] font-semibold text-white transition hover:bg-accent-hover">{t("presence.talk")}</button>
    </div>
  );
}

/** Pill + panel (top right, under the lighting buttons) and the proximity offer (bottom center, above the command bar). */
export function PresenceHost() {
  const { t } = useT();
  const [open, setOpen] = useState(false);
  const people = usePresence((s) => s.people);
  const selfId = usePresence((s) => s.selfId);
  const me = selfId ? people[selfId] : undefined;
  const count = Object.keys(people).length;
  const selected = useStore((s) => s.selectedAgentId);
  return (
    <>
      <div data-tour="office.presence" className="pointer-events-none absolute z-40 flex flex-col items-end gap-2" style={{ right: `calc(${selected ? "var(--panel-w, 420px) + 16px" : "16px"})`, top: 64 }}>
        {me ? (
          <button type="button" data-testid="presence-pill" aria-expanded={open} onClick={() => setOpen((v) => !v)}
            className="pointer-events-auto inline-flex items-center gap-2 rounded-full border border-line bg-panel/95 px-3 py-1.5 text-[11.5px] font-semibold text-ink2 shadow-soft backdrop-blur transition hover:bg-panel2">
            <Dot status={me.status} auto={me.auto_away} />
            {t("presence.online", { n: count })}
          </button>
        ) : null}
        {open && me ? <PresencePanel onClose={() => setOpen(false)} /> : null}
      </div>
      <div className="pointer-events-none absolute inset-x-0 bottom-[84px] z-40 flex justify-center px-4"><ProximityPrompt /></div>
    </>
  );
}
