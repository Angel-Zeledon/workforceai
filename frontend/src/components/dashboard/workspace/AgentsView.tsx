"use client";
import { useStore } from "@/lib/store";
import { useT } from "@/lib/i18n";
import { fmtUsd } from "@/lib/meta";
import { stateMeta } from "@/lib/meta";
import { useAgentView } from "@/lib/preferences";
import { Avatar } from "../../chat/ChatThread";
import { Empty, Progress, readable } from "../../ui";

function Row({ id, onTalk }: { id: string; onTalk: (id: string) => void }) {
  const { t } = useT();
  const v = useAgentView(id);
  const a = useStore((s) => s.agents[id]);
  const select = useStore((s) => s.select);
  if (!a) return null;
  const m = stateMeta(a.state);
  const btn = "min-h-10 rounded-md border border-line-strong bg-panel px-3 text-[12.5px] font-medium text-ink2 transition hover:border-accent hover:text-accent sm:min-h-9";
  return (
    <li data-testid={`agents-row-${id}`} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-3.5 py-3">
      <div className="flex min-w-[200px] flex-1 items-center gap-3">
        <Avatar id={id} size={36} />
        <div className="min-w-0 flex-1">
          <div className="truncate text-[14px] font-semibold text-ink">{v.name}</div>
          <div className="truncate text-[12px] text-mute">{v.title}</div>
        </div>
      </div>
      <div className="min-w-[200px] flex-[2]">
        <div className="flex items-center gap-1.5 text-[12px] font-medium" style={{ color: readable(m.color) }}><span aria-hidden className="h-1.5 w-1.5 rounded-full" style={{ background: m.color }} />{m.label}</div>
        <div className="truncate text-[13px] text-ink2">{a.activity || t("work.agents.noActivity")}</div>
        {a.progress > 0 && <div className="mt-1 max-w-[260px]"><Progress value={a.progress} /></div>}
      </div>
      <div className="hidden min-w-[130px] text-[12px] text-mute lg:block">
        <div>{t("work.agents.done", { n: a.metrics.tasks_completed })}</div>
        <div>{t("work.agents.pending", { n: a.metrics.tasks_pending })} · {fmtUsd(a.metrics.cost_usd)}</div>
      </div>
      <div className="flex shrink-0 gap-1.5">
        <button type="button" data-testid={`agents-talk-${id}`} onClick={() => onTalk(id)} className={btn}>{t("work.agents.talk")}</button>
        <button type="button" data-testid={`agents-panel-${id}`} onClick={() => select(id)} className={btn}>{t("work.agents.panel")}</button>
      </div>
    </li>
  );
}

/** Agentes: one row per agent with what it is doing, its numbers and the two things you do with it (talk, open its panel). */
export function AgentsView({ onTalk }: { onTalk: (id: string) => void }) {
  const { t } = useT();
  const order = useStore((s) => s.agentOrder);
  if (!order.length) return <Empty>{t("work.agents.none")}</Empty>;
  return (
    <ul data-testid="agents-view" className="mx-auto w-full max-w-4xl divide-y divide-line overflow-hidden rounded-[10px] border border-line bg-panel">
      {order.map((id) => <Row key={id} id={id} onTalk={onTalk} />)}
    </ul>
  );
}
