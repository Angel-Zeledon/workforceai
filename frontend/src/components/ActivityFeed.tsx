"use client";
import { useStore } from "@/lib/store";
import type { ActivityItem } from "@/lib/types";
import { fmtTime } from "@/lib/meta";
import { useT } from "@/lib/i18n";
import { Dot, Empty, readable, timeAgo, useAgentColor, useAgentName, useTicker } from "./ui";
import { Icon, type IconName } from "./icons";

const KIND_ICON: Record<string, IconName> = {
  "task.completed": "check", "task.failed": "x", "task.started": "play", "approval.requested": "alert", "approval.resolved": "check",
  "message.sent": "mail", "plan.created": "list", "request.received": "arrow", "request.completed": "flag", "request.queued": "clock", error: "x",
};

/** The tone of an event: what the badge on its avatar says at a glance (done, failed, waiting on you, in progress). */
type Tone = "ok" | "err" | "warn" | "info";
const KIND_TONE: Record<string, Tone> = {
  "task.completed": "ok", "approval.resolved": "ok", "request.completed": "ok",
  "task.failed": "err", error: "err",
  "approval.requested": "warn", "request.queued": "warn",
};
const TONE_BADGE: Record<Tone, string> = {
  ok: "bg-emerald-500 text-white", err: "bg-red-500 text-white", warn: "bg-amber-500 text-white", info: "bg-panel text-mute ring-1 ring-line",
};

export function ActivityFeed({ limit = 8, compact = false, agentId }: { limit?: number; compact?: boolean; agentId?: string }) {
  const { t } = useT();
  const activity = useStore((s) => s.activity);
  const color = useAgentColor();
  const name = useAgentName();
  const items = (agentId ? activity.filter((a) => a.agent_id === agentId) : activity).slice(0, limit);
  if (compact) return <CompactFeed items={items} />;
  if (!items.length) return <div data-testid="activity-feed"><Empty>{t("activity.empty")}</Empty></div>;
  return (
    <ul data-testid="activity-feed" className="space-y-1.5">
      {items.map((it) => (
        <li key={it.id} className="flex items-start gap-2 text-[11.5px] leading-snug">
          <span className="mt-[3px] w-[76px] shrink-0 font-mono text-[10px] text-mute">{fmtTime(it.ts)}</span>
          <span className="mt-[5px]"><Dot color={color(it.agent_id)} /></span>
          <span className="min-w-0 flex-1 text-ink2">
            <span className="mr-1 inline-block align-[-2px] text-mute"><Icon name={KIND_ICON[it.kind] || "chevron"} size={12} /></span>
            {it.agent_id && <span className="mr-1 font-semibold" style={{ color: readable(color(it.agent_id)) }}>{name(it.agent_id).split(" ")[0]}</span>}
            {it.text}
          </span>
        </li>
      ))}
    </ul>
  );
}

/**
 * The office's live feed: who (the agent's avatar, its color and initial), what happened (the event text, two lines at
 * most) and how it went (a small badge on the avatar: done, failed, waiting on you, in progress), with a relative time
 * that keeps itself fresh. New events slide in; the exact time stays in the tooltip.
 */
function CompactFeed({ items }: { items: ActivityItem[] }) {
  const { t } = useT();
  const color = useAgentColor();
  const name = useAgentName();
  useTicker(30_000); // "2 min" becomes "3 min" without new events
  if (!items.length) {
    return (
      <div data-testid="activity-feed" className="flex items-center gap-3 rounded-lg border border-dashed border-line px-3 py-3">
        <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-panel2 text-mute"><Icon name="pulse" size={15} /></span>
        <span className="text-[11.5px] leading-snug text-mute">{t("activity.empty")}</span>
      </div>
    );
  }
  return (
    <ul data-testid="activity-feed" className="-mx-1 divide-y divide-line/70">
      {items.map((it) => {
        const tone = KIND_TONE[it.kind] ?? "info";
        const who = it.agent_id ? name(it.agent_id) : "";
        const c = it.agent_id ? color(it.agent_id) : "";
        return (
          <li key={it.id} className="ac-pop flex items-start gap-2.5 rounded-md px-1 py-2 transition-colors hover:bg-panel2/70" title={`${who ? `${who} · ` : ""}${fmtTime(it.ts)}`}>
            <span className="relative mt-0.5 shrink-0">
              {it.agent_id ? (
                <span className="flex h-7 w-7 items-center justify-center rounded-full text-[11px] font-semibold text-white ring-2 ring-panel"
                  style={{ background: c }} aria-label={who}>{(who.trim()[0] || "?").toUpperCase()}</span>
              ) : (
                <span className="flex h-7 w-7 items-center justify-center rounded-full bg-panel2 text-mute ring-2 ring-panel"><Icon name="layers" size={13} /></span>
              )}
              <span className={`absolute -bottom-0.5 -right-0.5 flex h-3.5 w-3.5 items-center justify-center rounded-full ${TONE_BADGE[tone]}`} aria-hidden>
                <Icon name={KIND_ICON[it.kind] || "chevron"} size={8} strokeWidth={3} />
              </span>
            </span>
            <span className="min-w-0 flex-1">
              {who && <span className="block truncate text-[11px] font-semibold" style={{ color: readable(c) }}>{who.split(" ")[0]}</span>}
              <span className="line-clamp-2 text-[11.5px] leading-snug text-ink2">{it.text}</span>
            </span>
            <time dateTime={it.ts} className="mt-0.5 shrink-0 text-[10.5px] tabular-nums text-mute">{timeAgo(it.ts)}</time>
          </li>
        );
      })}
    </ul>
  );
}
