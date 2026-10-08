"use client";
import { useStore } from "@/lib/store";
import { fmtTime } from "@/lib/meta";
import { useT } from "@/lib/i18n";
import { Dot, Empty, readable, useAgentColor, useAgentName } from "./ui";
import { Icon, type IconName } from "./icons";

const KIND_ICON: Record<string, IconName> = {
  "task.completed": "check", "task.failed": "x", "task.started": "play", "approval.requested": "alert", "approval.resolved": "check",
  "message.sent": "mail", "plan.created": "list", "request.received": "arrow", "request.completed": "flag", "request.queued": "clock", error: "x",
};

export function ActivityFeed({ limit = 8, compact = false, agentId }: { limit?: number; compact?: boolean; agentId?: string }) {
  const { t } = useT();
  const activity = useStore((s) => s.activity);
  const color = useAgentColor();
  const name = useAgentName();
  const items = (agentId ? activity.filter((a) => a.agent_id === agentId) : activity).slice(0, limit);
  if (!items.length) return <div data-testid="activity-feed"><Empty>{t("activity.empty")}</Empty></div>;
  return (
    <ul data-testid="activity-feed" className={compact ? "space-y-1" : "space-y-1.5"}>
      {items.map((it) => (
        <li key={it.id} className="flex items-start gap-2 text-[11.5px] leading-snug">
          <span className="mt-[3px] w-[76px] shrink-0 font-mono text-[10px] text-mute">{fmtTime(it.ts)}</span>
          <span className="mt-[5px]"><Dot color={color(it.agent_id)} /></span>
          <span className="min-w-0 flex-1 text-ink2">
            <span className="mr-1 inline-block align-[-2px] text-mute"><Icon name={KIND_ICON[it.kind] || "chevron"} size={12} /></span>
            {it.agent_id && !compact && <span className="mr-1 font-semibold" style={{ color: readable(color(it.agent_id)) }}>{name(it.agent_id).split(" ")[0]}</span>}
            {it.text}
          </span>
        </li>
      ))}
    </ul>
  );
}
