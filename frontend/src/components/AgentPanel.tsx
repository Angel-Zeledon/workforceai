"use client";
import Link from "next/link";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { api } from "@/lib/api";
import { useStore } from "@/lib/store";
import { fmtUsd, stateMeta } from "@/lib/meta";
import { useT } from "@/lib/i18n";
import { useAgentView, usePreferences } from "@/lib/preferences";
import { useAgentFocus } from "@/lib/agentFocus";
import { AgentCustomizer } from "./AgentCustomizer";
import type { AgentDetail } from "@/lib/types";
import { ActivityFeed } from "./ActivityFeed";
import { ConversationThread } from "./Conversation";
import { ReportView } from "./ReportView";
import { AgentFilesTab } from "./workspace/AgentFilesTab";
import { Card, Empty, Progress, ReasonChip, StateBadge, TaskBadge, readable, timeAgo } from "./ui";
import { AgentChat } from "./chat/AgentChat";
import { Avatar } from "./chat/ChatThread";
import { Icon, type IconName } from "./icons";
import { ProfileSheet } from "./agent/ProfileSheet";
import { ThoughtAudit } from "./agent/ThoughtAudit";
import { TaskIntervention } from "./agent/TaskIntervention";
import { AgentPauseToggle } from "./agent/AgentPauseToggle";
import { RehearsalToggle } from "./rehearsal/RehearsalToggle";
import { DeskFiles } from "./drive/DeskFiles";
import { FolderAccessSection } from "./agent/FolderAccessSection";
import { MemoryTab } from "./agent/MemoryTab";
import { AgentSkillsCard } from "./skills/AgentSkillsCard";
import { QualityCard } from "./quality/QualityCard";
import { TrustCard } from "./trust/TrustCard";
import { PermissionsSection } from "./agent/PermissionsSection";
import { SaveAsSkillButton } from "./skills/SaveAsSkillButton";
import { useViewAll } from "@/lib/viewMode";
import { SIMPLE_AGENT_TABS, splitTabs } from "@/lib/viewModeLogic";
import { AGENT_TABS, resolvePanelTarget, type AgentTab, type Folded } from "./agent/panelTabs";

// Tanda 2 (spec 5.3): 5 tabs always visible in one row (it slides sideways on a phone). The 11 sections of the old wheel
// live inside them (agent/panelTabs.ts says where); old links and focus requests open the new tab, scroll to the part
// and unfold it. The jump buttons keep the old `panel-tab-<section>` test ids.
const JUMP_ICON: Record<string, IconName> = { state: "pulse", tasks: "list", reports: "doc", files: "folder", activity: "clock", thoughts: "help", chats: "users", skills: "bag", memory: "layers" };
const WORK_JUMPS = ["state", "tasks", "reports", "files", "activity", "thoughts", "chats"] as const;
const KNOWS_JUMPS = ["skills", "memory"] as const;

export function AgentPanel({ id }: { id: string }) {
  const { t } = useT();
  const view = useAgentView(id);
  const agent = useStore((s) => s.agents[id]);
  const tick = useStore((s) => s.agentTick[id] || 0);
  const select = useStore((s) => s.select);
  const reports = useStore((s) => s.reports);
  const storeTasks = useStore((s) => s.tasks);
  const reduce = usePreferences((s) => s.prefs.reduceMotion);
  const [detail, setDetail] = useState<AgentDetail | null>(null);
  const [tab, setTab] = useState<AgentTab>("chat");
  const [unfolded, setUnfolded] = useState<Folded[]>([]);
  const [jump, setJump] = useState<{ key: string; n: number } | null>(null);
  const [more, setMore] = useState(false);
  const [openConv, setOpenConv] = useState<string | null>(null);
  const [openReport, setOpenReport] = useState<string | null>(null);
  const [error, setError] = useState(false);
  const [focusTask, setFocusTask] = useState("");
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const body = useRef<HTMLDivElement>(null);
  const tabRow = useRef<HTMLDivElement>(null);
  const viewAll = useViewAll();

  /** Opens an old section or a new tab by name: the tab, the folded block and the scroll to it. */
  const goTo = (name: string | null | undefined) => {
    const to = resolvePanelTarget(name);
    if (!to) return false;
    setTab(to.tab);
    if (to.unfold) { const u = to.unfold; setUnfolded((cur) => (cur.includes(u) ? cur : [...cur, u])); }
    setJump(to.section ? { key: to.section, n: Date.now() } : null);
    return true;
  };

  useEffect(() => {
    setDetail(null); setTab("chat"); setUnfolded([]); setJump(null); setOpenConv(null); setOpenReport(null); setFocusTask("");
    // an old link `?section=<old section>` opens the new tab that holds it (then the parameter is dropped)
    if (typeof window === "undefined") return;
    const url = new URL(window.location.href);
    if (goTo(url.searchParams.get("section"))) { url.searchParams.delete("section"); window.history.replaceState(window.history.state, "", url.toString()); }
  }, [id]); // eslint-disable-line react-hooks/exhaustive-deps
  // B10: the shield notice opens this panel on "Cómo lo pensó" (now folded inside Trabajo) at the task that was warned
  const focus = useAgentFocus((s) => s.req);
  useEffect(() => {
    if (!focus || focus.agentId !== id) return;
    goTo(focus.tab); setFocusTask(focus.taskId); useAgentFocus.getState().clear();
  }, [id, focus]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!jump) return;
    const el = body.current?.querySelector<HTMLElement>(`[data-section="${jump.key}"]`);
    el?.scrollIntoView({ block: "start", behavior: reduce ? "auto" : "smooth" });
  }, [jump, tab, reduce]);
  // keep the open tab in sight inside the row (it slides sideways on a phone)
  useEffect(() => { tabRow.current?.querySelector<HTMLElement>('[aria-selected="true"]')?.scrollIntoView({ block: "nearest", inline: "nearest" }); }, [tab]);
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
  const convs = detail?.conversations ?? [];

  // simple mode: Chat and Trabajo open, the rest one click away ("Más"); the open tab always shows
  const split = splitTabs(AGENT_TABS, SIMPLE_AGENT_TABS, viewAll || more, [tab]);
  const counts: Record<string, number | undefined> = { tasks: tasks.length, reports: myReports.length, chats: convs.length };

  const onTabKey = (e: KeyboardEvent<HTMLDivElement>) => {
    const i = split.shown.indexOf(tab);
    let n = i;
    if (e.key === "ArrowRight") n = (i + 1) % split.shown.length;
    else if (e.key === "ArrowLeft") n = (i - 1 + split.shown.length) % split.shown.length;
    else if (e.key === "Home") n = 0;
    else if (e.key === "End") n = split.shown.length - 1;
    else return;
    e.preventDefault();
    setTab(split.shown[n]); setJump(null);
    tabRow.current?.querySelector<HTMLElement>(`#agent-tab-${split.shown[n]}`)?.focus();
  };

  const jumpLabel = (k: string) => (k === "skills" ? t("panel.sec.skills") : t(`panel.tab.${k}`));
  const jumpRow = (keys: readonly string[]) => (
    <nav aria-label={t("panel.jump.label")} className="sticky -top-3 z-10 -mx-3 -mt-3 flex flex-wrap gap-1.5 border-b border-line bg-panel/95 px-3 py-2 backdrop-blur-[2px] wf-hscroll sm:-top-4 sm:-mx-4 sm:-mt-4 sm:px-4">
      {keys.map((k) => (
        <button key={k} type="button" data-testid={k === "skills" ? "panel-jump-skills" : `panel-tab-${k}`} onClick={() => goTo(k)}
          className="inline-flex min-h-8 items-center gap-1 rounded-full border border-line bg-panel2 px-2.5 py-1 text-[11.5px] font-medium text-ink2 hover:border-accent hover:text-accent">
          <Icon name={JUMP_ICON[k]} size={12} />{jumpLabel(k)}
          {counts[k] ? <span className="font-mono text-[10px] text-mute">{counts[k]}</span> : null}
        </button>
      ))}
    </nav>
  );
  const part = (k: string, title: string | null, children: ReactNode) => (
    <section key={k} data-section={k} className="scroll-mt-14 space-y-2">
      {title && <h3 className="flex items-center gap-1.5 px-0.5 text-[11px] font-semibold uppercase tracking-[0.08em] text-mute"><Icon name={JUMP_ICON[k] ?? "layers"} size={12} />{title}{counts[k] ? <span className="font-mono normal-case tracking-normal">{counts[k]}</span> : null}</h3>}
      {children}
    </section>
  );
  const fold = (k: Folded, hint: string, children: ReactNode) => {
    const isOpen = unfolded.includes(k);
    return (
      <section key={k} data-section={k} className="scroll-mt-14 rounded-xl border border-line bg-panel shadow-pop">
        <button type="button" data-testid={`panel-fold-${k}`} aria-expanded={isOpen} aria-controls={`panel-fold-body-${k}`}
          onClick={() => setUnfolded((cur) => (isOpen ? cur.filter((x) => x !== k) : [...cur, k]))}
          className="flex min-h-11 w-full items-center gap-2.5 rounded-xl px-3 py-2.5 text-left hover:bg-panel2 sm:px-4">
          <Icon name={JUMP_ICON[k]} size={15} className="shrink-0 text-accent" />
          <span className="min-w-0 flex-1"><span className="block text-[13px] font-semibold text-ink">{t(`panel.tab.${k}`)}{counts[k] ? <span className="ml-1.5 font-mono text-[10.5px] font-normal text-mute">{counts[k]}</span> : null}</span><span className="block text-[11px] text-mute">{hint}</span></span>
          <Icon name="down" size={14} className={`shrink-0 text-mute transition-transform ${isOpen ? "rotate-180" : ""}`} />
        </button>
        {isOpen && <div id={`panel-fold-body-${k}`} className="space-y-3 border-t border-line p-3 sm:p-4">{children}</div>}
      </section>
    );
  };

  return (
    <aside className="pointer-events-auto relative flex h-full w-[min(420px,100vw)] flex-col border-l border-line bg-panel shadow-float">
      <div className="border-b border-line px-3 pt-[max(0.75rem,env(safe-area-inset-top))] sm:px-4 sm:pt-4">
        <div className="flex items-start justify-between gap-3">
          <div className="flex min-w-0 items-center gap-3">
            <Avatar id={id} size={40} />
            <div className="min-w-0">
              <h2 className="font-display truncate text-[17px] font-semibold leading-tight text-ink">{view.name}</h2>
              <div className="truncate text-xs text-mute">{view.title}<span className="mx-1.5 text-line-strong">·</span><span style={{ color: readable(meta.color) }} className="font-medium">{roleLabel}</span></div>
            </div>
          </div>
          <button onClick={() => select(null)} className="flex min-h-10 min-w-10 shrink-0 items-center justify-center rounded-md p-1.5 text-mute hover:bg-mute/10 hover:text-ink md:min-h-0 md:min-w-0" aria-label={t("common.close")}><Icon name="x" size={16} /></button>
        </div>
        <div className="mt-3 flex items-center gap-2"><StateBadge state={agent.state} /><span className="truncate text-xs text-ink2">{agent.activity}</span></div>
        {agent.progress > 0 && <div className="mt-2"><Progress value={agent.progress} color={sm.color} /></div>}
        <div ref={tabRow} role="tablist" aria-label={t("panel.nav.label")} data-testid="panel-nav" onKeyDown={onTabKey}
          className="-mx-3 mt-2.5 flex flex-nowrap gap-0.5 overflow-x-auto px-2 wf-tabs [scrollbar-width:none] sm:-mx-4 sm:px-3">
          {split.shown.map((tb) => {
            const on = tb === tab;
            return (
              <button key={tb} type="button" role="tab" id={`agent-tab-${tb}`} aria-selected={on} aria-controls="agent-panel-body" tabIndex={on ? 0 : -1}
                data-testid={`panel-tab-${tb}`} onClick={() => { setTab(tb); setJump(null); }}
                className={`inline-flex shrink-0 items-center whitespace-nowrap border-b-2 px-2 py-2.5 text-[12.5px] font-medium transition-colors ${on ? "border-accent text-accent" : "border-transparent text-mute hover:text-ink"}`}>
                <span data-testid={on ? "panel-nav-current" : undefined}>{t(`panel.tab.${tb}`)}</span>
              </button>
            );
          })}
          {split.folded.length > 0 && (
            <button type="button" data-testid="panel-view-all" onClick={() => setMore(true)} title={t("panel.tab.moreHint")} aria-label={t("panel.tab.moreHint")}
              className="inline-flex shrink-0 items-center gap-1 whitespace-nowrap border-b-2 border-transparent px-2 py-2.5 text-[12.5px] font-semibold text-accent hover:text-accent-hover">
              {t("panel.tab.more")}<span className="font-mono text-[10px] text-mute">{split.folded.length}</span><Icon name="down" size={12} className="-rotate-90" />
            </button>
          )}
        </div>
      </div>
      <div ref={body} id="agent-panel-body" role="tabpanel" aria-labelledby={`agent-tab-${tab}`} data-testid={`panel-body-${tab}`}
        className={tab === "chat" ? "min-h-0 flex-1" : "min-h-0 flex-1 space-y-4 overflow-y-auto p-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] sm:p-4"}>
        {tab === "chat" && <AgentChat id={id} />}
        {error && !detail && tab !== "chat" && <Empty>{t("panel.loadError")}</Empty>}

        {tab === "work" && (
          <>
            {jumpRow(WORK_JUMPS)}
            {part("state", t("panel.tab.state"), (
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
              </>
            ))}
            {part("tasks", t("panel.tab.tasks"), tasks.length ? tasks.map((task) => (
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
                <SaveAsSkillButton task={task} />
                <TaskIntervention taskId={task.id} status={task.status} />
              </Card>
            )) : <Empty>{t("panel.noTasks")}</Empty>)}
            {part("reports", t("panel.tab.reports"), openReport ? (
              <div><button className="mb-3 text-xs text-accent" onClick={() => setOpenReport(null)}><Icon name="back" size={12} className="mr-1 inline-block align-[-1px]" />{t("common.back")}</button><ReportView r={reports[openReport]} /></div>
            ) : myReports.length ? myReports.map((r) => (
              <button key={r.id} onClick={() => setOpenReport(r.id)} className="block w-full rounded-md border border-line bg-panel2 p-3 text-left hover:border-accent">
                <div className="text-[13px] font-semibold text-ink">{r.title}</div><div className="mt-1 line-clamp-2 text-xs text-mute">{r.summary}</div>
              </button>
            )) : <Empty>{t("panel.noReports")}</Empty>)}
            {part("files", t("panel.tab.files"), <><DeskFiles id={id} /><AgentFilesTab id={id} /></>)}
            {part("activity", t("panel.tab.activity"), <ActivityFeed agentId={id} limit={40} />)}
            {fold("thoughts", t("panel.fold.thoughtsHint"), <ThoughtAudit id={id} focusTaskId={focusTask} />)}
            {fold("chats", t("panel.fold.chatsHint"), convs.length ? convs.map((c) => (
              <Card key={c.id}>
                <button className="flex w-full items-center justify-between text-left" onClick={() => setOpenConv(openConv === c.id ? null : c.id)}>
                  <span className="text-[13px] font-semibold text-ink">{c.title}</span><span className="text-[10px] text-mute">{timeAgo(c.last_message_at)}</span>
                </button>
                {openConv === c.id && <div className="mt-3"><ConversationThread id={c.id} /></div>}
              </Card>
            )) : <Empty>{t("panel.noChats")}</Empty>)}
          </>
        )}

        {tab === "can" && (
          <>
            {part("controls", null, (
              <Card title={t("panel.can.controls")}>
                <div className="flex flex-wrap items-center gap-2"><AgentPauseToggle id={id} /><RehearsalToggle id={id} /></div>
              </Card>
            ))}
            {part("permissions", null, <PermissionsSection id={id} />)}
            {part("folders", null, <FolderAccessSection id={id} />)}
            {part("tools", null, (
              <Card title={t("panel.can.tools")} right={<Link href="/connections" data-testid="panel-can-connections" className="text-[11px] font-medium text-accent hover:underline">{t("panel.can.toolsLink")}</Link>}>
                <div className="text-[11px] text-mute">{t("panel.autonomy")}: <span className="text-ink">{t(`autonomy.${a.autonomy}`)}</span></div>
                <div className="mt-2 flex flex-wrap gap-1">{(a.tools || []).map((x) => <span key={x} className="rounded bg-mute/[0.08] px-1.5 py-0.5 font-mono text-[10px] text-ink2">{x}</span>)}</div>
                <div className="mt-2 flex flex-wrap gap-1">{(a.permissions || []).map((x) => <span key={x} className="rounded border border-line px-1.5 py-0.5 text-[10px] text-mute">{x}</span>)}</div>
              </Card>
            ))}
          </>
        )}

        {tab === "knows" && (
          <>
            {jumpRow(KNOWS_JUMPS)}
            {part("skills", null, <AgentSkillsCard id={id} />)}
            {part("memory", t("panel.tab.memory"), <MemoryTab id={id} legacy={detail?.memory ?? []} />)}
          </>
        )}

        {tab === "profile" && (
          <>
            {a.description && part("about", null, <Card title={t("panel.about")}><p className="text-xs text-ink2">{a.description}</p></Card>)}
            {part("job", null, <ProfileSheet id={id} />)}
            {part("look", null, <Card title={t("panel.profile.look")}><AgentCustomizer id={id} /></Card>)}
            {part("trust", null, <TrustCard id={id} />)}
            {part("quality", null, <QualityCard id={id} />)}
          </>
        )}
      </div>
    </aside>
  );
}
