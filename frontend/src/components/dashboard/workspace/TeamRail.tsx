"use client";
import { useMemo, useState } from "react";
import { useStore } from "@/lib/store";
import { useT } from "@/lib/i18n";
import { stateMeta } from "@/lib/meta";
import { useAgentView } from "@/lib/preferences";
import { Avatar } from "../../chat/ChatThread";
import { Icon } from "../../icons";
import { readable } from "../../ui";
import { groupAgents, needsYouAgentIds, type RailGroup } from "./workspaceLogic";
import { useOfficeAgentIds } from "@/lib/currentOffice";

const SEARCH_FROM = 8; // with many agents a search box appears

function AgentRow({ id, active, waiting, onPick, onPanel }: { id: string; active: boolean; waiting: boolean; onPick: () => void; onPanel: () => void }) {
  const { t } = useT();
  const v = useAgentView(id);
  const a = useStore((s) => s.agents[id]);
  if (!a) return null;
  const m = stateMeta(a.state);
  const idle = a.state === "idle" || a.state === "completed";
  return (
    <li className="group relative">
      <button type="button" data-testid={`team-row-${id}`} data-active={active ? "1" : "0"} aria-pressed={active} onClick={onPick}
        title={t("work.team.talkTo", { name: v.name })}
        className={`flex min-h-[52px] w-full items-center gap-2.5 rounded-[10px] py-1.5 pl-2.5 pr-10 text-left transition-colors ${active ? "bg-accent-soft" : "hover:bg-panel2"}`}>
        <Avatar id={id} size={30} />
        <span className="min-w-0 flex-1">
          <span className={`block truncate text-[13px] font-semibold ${active ? "text-accent" : "text-ink"}`}>{v.name}</span>
          {/* one line: what they are doing; free people show their job instead of "Disponible" twice */}
          <span className="flex items-center gap-1.5 text-[12px] leading-snug text-ink2">
            <span aria-hidden className={`h-1.5 w-1.5 shrink-0 rounded-full ${waiting ? "bg-warn" : ""}`} style={waiting ? undefined : { background: m.color }} />
            <span className="min-w-0 truncate" style={idle || waiting ? undefined : { color: readable(m.color) }}>{idle ? (waiting ? t("work.team.needsReply") : v.title) : (a.activity || m.label)}</span>
            <span className="sr-only">, {m.label}</span>
          </span>
        </span>
      </button>
      <button type="button" data-testid={`team-panel-${id}`} onClick={onPanel} aria-label={t("work.team.openPanel", { name: v.name })} title={t("work.team.openPanel", { name: v.name })}
        className="absolute right-1 top-1/2 flex h-10 w-10 -translate-y-1/2 items-center justify-center rounded-md text-mute transition hover:bg-panel hover:text-accent focus-visible:bg-panel md:opacity-0 md:focus-visible:opacity-100 md:group-hover:opacity-100">
        <Icon name="user" size={15} />
      </button>
    </li>
  );
}

const GROUP_KEY: Record<RailGroup, string> = { waiting: "work.team.waiting", working: "work.team.working", free: "work.team.free" };

/** The team as rows (no cards): who waits for you, who works, who is free. Picking one changes the conversation. */
export function TeamRail({ target, onPick, onPanel, className = "" }: { target: string | null; onPick: (id: string | null) => void; onPanel: (id: string) => void; className?: string }) {
  const { t } = useT();
  const { ids: order } = useOfficeAgentIds();
  const agents = useStore((s) => s.agents);
  const approvals = useStore((s) => s.approvals);
  const questions = useStore((s) => s.questions);
  const [q, setQ] = useState("");
  const needs = useMemo(() => needsYouAgentIds(Object.values(approvals), Object.values(questions ?? {})), [approvals, questions]);
  const sections = useMemo(() => groupAgents(order, agents, needs, q), [order, agents, needs, q]);
  const working = Object.values(agents).filter((a) => ["thinking", "working", "talking", "reviewing", "waiting"].includes(a.state)).length;
  return (
    <nav aria-label={t("work.team.title")} data-testid="team-rail" data-tour="dashboard.team" className={`flex min-h-0 flex-col ${className}`}>
      <div className="px-3 pb-1 pt-3 text-[13px] font-semibold text-ink">{t("work.team.title")}</div>
      <div className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-3">
        <button type="button" data-testid="team-all" data-active={target === null ? "1" : "0"} aria-pressed={target === null} onClick={() => onPick(null)}
          className={`mb-1 flex min-h-[48px] w-full items-center gap-2.5 rounded-[10px] px-2.5 py-1.5 text-left transition-colors ${target === null ? "bg-accent-soft" : "hover:bg-panel2"}`}>
          <span aria-hidden className={`flex h-[30px] w-[30px] shrink-0 items-center justify-center rounded-md ${target === null ? "bg-accent text-white" : "bg-panel2 text-ink2"}`}><Icon name="users" size={16} /></span>
          <span className="min-w-0 flex-1">
            <span className={`block truncate text-[13px] font-semibold ${target === null ? "text-accent" : "text-ink"}`}>{t("work.all")}</span>
            <span className="block truncate text-[12px] text-ink2">{working > 0 ? t("work.team.nWorking", { n: working }) : t("work.team.allIdle")}</span>
          </span>
        </button>
        {order.length >= SEARCH_FROM && (
          <div className="px-1 pb-1.5 pt-1">
            <input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("work.team.search")} aria-label={t("work.team.search")} data-testid="team-search"
              className="h-9 w-full rounded-lg border border-line bg-panel px-2.5 text-[13px] text-ink outline-none placeholder:text-mute focus:border-accent" />
          </div>
        )}
        {sections.map((s) => (
          <section key={s.group} data-testid={`team-group-${s.group}`} aria-label={t(GROUP_KEY[s.group])} className="mt-2">
            <h3 className={`px-2.5 pb-0.5 text-[12px] font-semibold ${s.group === "waiting" ? "text-warn" : "text-mute"}`}>{t(GROUP_KEY[s.group])} <span className="font-normal text-mute">{s.ids.length}</span></h3>
            <ul>{s.ids.map((id) => <AgentRow key={id} id={id} active={target === id} waiting={s.group === "waiting"} onPick={() => onPick(id)} onPanel={() => onPanel(id)} />)}</ul>
          </section>
        ))}
        {q && sections.length === 0 && <p className="px-3 py-4 text-[12.5px] text-mute">{t("work.team.noMatch")}</p>}
      </div>
    </nav>
  );
}

/** Tablet: the same team as a strip of chips above the conversation (picking one changes who the box writes to). */
export function TeamStrip({ target, onPick, onPanel }: { target: string | null; onPick: (id: string | null) => void; onPanel: (id: string) => void }) {
  const { t } = useT();
  const { ids: order } = useOfficeAgentIds();
  const agents = useStore((s) => s.agents);
  const chip = (active: boolean) => `inline-flex min-h-10 shrink-0 items-center gap-1.5 rounded-full border px-3 text-[13px] font-medium transition-colors ${active ? "border-accent bg-accent-soft text-accent" : "border-line bg-panel text-ink hover:bg-panel2"}`;
  return (
    <nav aria-label={t("work.team.title")} data-testid="team-strip" className="flex shrink-0 gap-1.5 overflow-x-auto border-b border-line px-3 py-2 md:px-4">
      <button type="button" data-testid="team-all" aria-pressed={target === null} onClick={() => onPick(null)} className={chip(target === null)}>{t("work.all")}</button>
      {order.map((id) => {
        const a = agents[id];
        if (!a) return null;
        const m = stateMeta(a.state);
        return (
          <button key={id} type="button" data-testid={`team-row-${id}`} aria-pressed={target === id} onClick={() => onPick(id)} onDoubleClick={() => onPanel(id)} title={`${a.name}: ${m.label}`} className={chip(target === id)}>
            <span aria-hidden className="h-2 w-2 rounded-full" style={{ background: m.color }} />{a.name.split(" ")[0]}<span className="sr-only">, {m.label}</span>
          </button>
        );
      })}
    </nav>
  );
}
