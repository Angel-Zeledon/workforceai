"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "@/lib/api";
import { useStore } from "@/lib/store";
import { fmtUsd, stateMeta } from "@/lib/meta";
import { useT } from "@/lib/i18n";
import { useAgentView } from "@/lib/preferences";
import { AgentCustomizer } from "./AgentCustomizer";
import type { AgentDetail } from "@/lib/types";
import { ActivityFeed } from "./ActivityFeed";
import { ConversationThread } from "./Conversation";
import { ReportView } from "./ReportView";
import { AgentFilesTab } from "./workspace/AgentFilesTab";
import { Card, Empty, Progress, ReasonChip, StateBadge, TaskBadge, readable, timeAgo } from "./ui";
import { AgentChat } from "./chat/AgentChat";
import { Avatar } from "./chat/ChatThread";
import { Icon } from "./icons";

const TABS = ["chat", "state", "tasks", "chats", "memory", "reports", "files", "activity", "profile"] as const;
type Tab = (typeof TABS)[number];

export function AgentPanel({ id }: { id: string }) {
  const { t } = useT();
  const view = useAgentView(id);
  const agent = useStore((s) => s.agents[id]);
  const tick = useStore((s) => s.agentTick[id] || 0);
  const select = useStore((s) => s.select);
  const reports = useStore((s) => s.reports);
  const storeTasks = useStore((s) => s.tasks);
  const [detail, setDetail] = useState<AgentDetail | null>(null);
  const [tab, setTab] = useState<Tab>("chat");
  const [openConv, setOpenConv] = useState<string | null>(null);
  const [openReport, setOpenReport] = useState<string | null>(null);
  const [error, setError] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => { setDetail(null); setTab("chat"); setOpenConv(null); setOpenReport(null); }, [id]);
  useEffect(() => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(async () => {
      try { setDetail(await api.agentDetail(id)); setError(false); } catch { setError(true); }
    }, detail ? 700 : 0);
    return () => { if (timer.current) clearTimeout(timer.current); };
  }, [id, tick]);

  const myReports = useMemo(() => Object.values(reports).filter((r) => r.contributors.includes(id)).sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at)), [reports, id]);
  if (!agent) return null;
  const meta = { color: view.color, label: "" };
  const roleLabel = t(`role.${agent.role}`);
  const sm = stateMeta(agent.state);
  const cur = agent.current_task_id ? storeTasks[agent.current_task_id] : null;
  const a = detail?.agent ?? agent;
  // tasks: prefer fresh store data over the (debounced) detail snapshot
  const tasks = (detail?.tasks ?? []).map((t) => storeTasks[t.id] ?? t);

  return (
    <aside className="pointer-events-auto flex h-full w-[420px] max-w-full flex-col border-l border-line bg-panel shadow-float">
      <div className="border-b border-line p-4">
        <div className="flex items-start justify-between gap-3">
          <div className="flex min-w-0 items-center gap-3">
            <Avatar id={id} size={40} />
            <div className="min-w-0">
              <h2 className="font-display truncate text-[17px] font-semibold leading-tight text-ink">{view.name}</h2>
              <div className="truncate text-xs text-mute">{view.title}<span className="mx-1.5 text-line-strong">·</span><span style={{ color: readable(meta.color) }} className="font-medium">{roleLabel}</span></div>
            </div>
          </div>
          <button onClick={() => select(null)} className="rounded-md p-1.5 text-mute hover:bg-mute/10 hover:text-ink" aria-label={t("common.close")}><Icon name="x" size={16} /></button>
        </div>
        <div className="mt-3 flex items-center gap-2"><StateBadge state={agent.state} /><span className="truncate text-xs text-ink2">{agent.activity}</span></div>
        {agent.progress > 0 && <div className="mt-2"><Progress value={agent.progress} color={sm.color} /></div>}
      </div>
      <nav role="tablist" className="flex gap-0.5 overflow-x-auto border-b border-line px-2">
        {TABS.map((tb) => (
          <button key={tb} role="tab" aria-selected={tab === tb} data-testid={`panel-tab-${tb}`} onClick={() => setTab(tb)} className={`whitespace-nowrap border-b-2 px-2.5 py-2.5 text-[11.5px] font-medium transition ${tab === tb ? "border-accent text-ink" : "border-transparent text-mute hover:text-ink"}`}>
            {t(`panel.tab.${tb}`)}
          </button>
        ))}
      </nav>
      <div className={tab === "chat" ? "min-h-0 flex-1" : "min-h-0 flex-1 space-y-3 overflow-y-auto p-4"}>
        {tab === "chat" && <AgentChat id={id} />}
        {error && !detail && <Empty>{t("panel.loadError")}</Empty>}
        {tab === "profile" && <Card title={t("panel.tab.profile")}><AgentCustomizer id={id} /></Card>}
        {tab === "state" && (
          <>
            <Card title={t("panel.currentTask")}>
              {cur ? (
                <div>
                  <div className="text-[13px] font-semibold text-ink">{cur.title}</div>
                  <p className="mt-0.5 text-xs text-mute">{cur.description}</p>
                  <ReasonChip agentId={id} reason={cur.assigned_reason} className="mt-2" />
                  <div className="mt-2 flex items-center gap-2"><TaskBadge status={cur.status} /><span className="font-mono text-[11px] text-mute">{agent.progress}%</span></div>
                  <div className="mt-2"><Progress value={agent.progress} color={sm.color} /></div>
                </div>
              ) : <div className="text-xs text-mute">{t("panel.noTask")}</div>}
            </Card>
            <Card title={t("panel.metrics")}>
              <div className="grid grid-cols-2 gap-2">
                {[
                  [t("panel.completed"), a.metrics.tasks_completed],
                  [t("panel.pending"), a.metrics.tasks_pending],
                  [t("panel.avgTime"), `${a.metrics.avg_seconds}s`],
                  [t("common.cost"), fmtUsd(a.metrics.cost_usd)],
                ].map(([k, v]) => (
                  <div key={k as string} className="rounded-md bg-panel2 px-3 py-2"><div className="text-[10px] uppercase tracking-wide text-mute">{k}</div><div className="font-mono text-base text-ink">{v}</div></div>
                ))}
              </div>
            </Card>
            <Card title={t("panel.about")}>
              <p className="mb-2 text-xs text-ink2">{a.description}</p>
              <div className="text-[11px] text-mute">{t("panel.autonomy")}: <span className="text-ink">{t(`autonomy.${a.autonomy}`)}</span></div>
              <div className="mt-2 flex flex-wrap gap-1">{(a.tools || []).map((t) => <span key={t} className="rounded bg-mute/[0.08] px-1.5 py-0.5 font-mono text-[10px] text-ink2">{t}</span>)}</div>
              <div className="mt-2 flex flex-wrap gap-1">{(a.permissions || []).map((t) => <span key={t} className="rounded border border-line px-1.5 py-0.5 text-[10px] text-mute">{t}</span>)}</div>
            </Card>
          </>
        )}
        {tab === "tasks" && (tasks.length ? tasks.map((task) => (
          <Card key={task.id}>
            <div className="flex items-start justify-between gap-2"><div className="text-[13px] font-semibold text-ink">{task.title}</div><TaskBadge status={task.status} /></div>
            <p className="mt-1 text-xs text-mute">{task.description}</p>
            <ReasonChip agentId={id} reason={task.assigned_reason} className="mt-2" />
            {task.output && (
              <div className="mt-2 space-y-1 rounded-md bg-bg/60 p-2 text-[11.5px] text-ink2">
                <div className="text-ink">{task.output.summary}</div>
                {task.output.findings.map((f, i) => <div key={i}>• {f}</div>)}
                <div className="text-mute">{t("common.confidence")} {Math.round(task.output.confidence * 100)}%</div>
              </div>
            )}
          </Card>
        )) : <Empty>{t("panel.noTasks")}</Empty>)}
        {tab === "chats" && ((detail?.conversations ?? []).length ? (detail!.conversations).map((c) => (
          <Card key={c.id}>
            <button className="flex w-full items-center justify-between text-left" onClick={() => setOpenConv(openConv === c.id ? null : c.id)}>
              <span className="text-[13px] font-semibold text-ink">{c.title}</span><span className="text-[10px] text-mute">{timeAgo(c.last_message_at)}</span>
            </button>
            {openConv === c.id && <div className="mt-3"><ConversationThread id={c.id} /></div>}
          </Card>
        )) : <Empty>{t("panel.noChats")}</Empty>)}
        {tab === "memory" && ((detail?.memory ?? []).length ? (detail!.memory).map((m) => (
          <div key={m.key} className="rounded-md border border-line bg-panel2 p-3">
            <div className="flex items-center justify-between"><span className="font-mono text-[11px] text-accent">{m.key}</span><span className="rounded bg-mute/[0.08] px-1.5 text-[10px] uppercase text-mute">{m.scope}</span></div>
            <div className="mt-1 text-xs text-ink2">{m.value}</div>
          </div>
        )) : <Empty>{t("panel.noMemory")}</Empty>)}
        {tab === "reports" && (openReport ? (
          <div><button className="mb-3 text-xs text-accent" onClick={() => setOpenReport(null)}><Icon name="back" size={12} className="mr-1 inline-block align-[-1px]" />{t("common.back")}</button><ReportView r={reports[openReport]} /></div>
        ) : myReports.length ? myReports.map((r) => (
          <button key={r.id} onClick={() => setOpenReport(r.id)} className="block w-full rounded-md border border-line bg-panel2 p-3 text-left hover:border-accent">
            <div className="text-[13px] font-semibold text-ink">{r.title}</div><div className="mt-1 line-clamp-2 text-xs text-mute">{r.summary}</div>
          </button>
        )) : <Empty>{t("panel.noReports")}</Empty>)}
        {tab === "files" && <AgentFilesTab id={id} />}
        {tab === "activity" && <ActivityFeed agentId={id} limit={40} />}
      </div>
    </aside>
  );
}
