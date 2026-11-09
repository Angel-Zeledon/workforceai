"use client";
import { Fragment, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { usePendingCount } from "@/lib/pending";
import { useStore } from "@/lib/store";
import { useT } from "@/lib/i18n";
import { useArtifacts } from "@/lib/artifacts";
import { customersApi } from "@/lib/customersApi";
import { fmtTime, fmtUsd, roleMeta, taskStatusLabel, TASK_STATUS_COLOR } from "@/lib/meta";
import { resolveAgent, usePreferences } from "@/lib/preferences";
import { go as goRoute, openInOffice } from "@/lib/navBridge";
import type { Task } from "@/lib/types";
import { ActivityFeed } from "../ActivityFeed";
import { ApprovalCard } from "../ApprovalsTray";
import { InitiativePanel } from "../initiative/InitiativePanel";
import { ConversationThread } from "../Conversation";
import { ReportView } from "../ReportView";
import { BudgetLimitsPanel, CostBreakdownPanel } from "../cost/CostBreakdownPanel";
import { RoiPanel } from "../cost/RoiPanel";
import { QualityOverview } from "../quality/QualityOverview";
import { AuditDetail } from "../quality/AuditStamp";
import { taskAuditOf } from "@/lib/auditLogic";
import { Icon } from "../icons";
import { Card, Dot, Empty, Progress, ReasonChip, StateBadge, TaskBadge, readable, timeAgo, useAgentColor, useAgentName } from "../ui";
import { useViewAll } from "@/lib/viewMode";
import { SIMPLE_DASHBOARD, splitTabs } from "@/lib/viewModeLogic";
import { MoreButton } from "../nav/ViewAll";
import { GO_TO, LIST_SECTIONS, sectionHref, targetOfSearch, type GoToId, type ListSection } from "./listViewLogic";

/**
 * The Office's "Vista lista" (it used to be "Dashboard", another app inside the Office with 11 sections). It keeps
 * what is its own: Resumen, Agentes, Tareas, Calidad, Costos (and Informes, which have no screen of their own).
 * Clientes, Pedidos, Pendientes and Archivos have their own screen in the menu: here they are links with their number
 * ("Ir a"). Old links (`/dashboard?section=customers`, `?tab=files`…) land on the screen or section that holds them now.
 */

function Kpi({ label, value, sub, color = "#151b26" }: { label: string; value: string | number; sub?: string; color?: string }) {
  return (
    <div className="rounded-xl border border-line bg-panel shadow-pop p-3 sm:p-4">
      <div className="text-[10px] font-semibold uppercase tracking-[0.08em] text-mute">{label}</div>
      <div className="mt-1 text-xl font-semibold tracking-tight sm:mt-1.5 sm:text-2xl" style={{ color }}>{value}</div>
      {sub && <div className="mt-0.5 text-[11px] text-mute">{sub}</div>}
    </div>
  );
}

/** The number each "Ir a" link carries. null = not known yet (the link still works, it just shows no number). */
function useGoToCounts(): Record<GoToId, number | null> {
  const pending = usePendingCount(); // the one Pendientes number
  const m = useStore((s) => s.metrics);
  const requests = useStore((s) => s.requests);
  const files = useArtifacts((s) => s.artifacts);
  const [customers, setCustomers] = useState<number | null>(null);
  useEffect(() => {
    let live = true;
    customersApi.district().then((d) => { if (live) setCustomers(d.customers.length); }).catch(() => { /* older server: no number */ });
    return () => { live = false; };
  }, []);
  return {
    customers,
    requests: m?.requests_total ?? Object.keys(requests).length,
    approvals: pending,
    files: Object.keys(files).length,
  };
}

export function Dashboard() {
  const { t } = useT();
  const router = useRouter();
  const [section, setSectionState] = useState<ListSection>("overview");
  const [anchor, setAnchor] = useState<string | null>(null);
  const viewAll = useViewAll();
  const [more, setMore] = useState(false);
  const counts = useGoToCounts();

  // Old links: a section that has its own screen now goes there; the rest opens the section that holds it.
  useEffect(() => {
    const read = () => {
      const target = targetOfSearch(window.location.search);
      if (target.kind === "route") { router.replace(target.href); return; }
      setSectionState(target.section);
      setAnchor(target.anchor);
    };
    read();
    window.addEventListener("popstate", read);
    return () => window.removeEventListener("popstate", read);
  }, [router]);
  // A block inside the section (conversations under Agentes, decisions in Resumen): scroll to it once it is drawn.
  useEffect(() => {
    if (!anchor) return;
    // once now and again when the agents have loaded (the cards above push the block down); a touch or wheel stops it
    let stop = false;
    const halt = () => { stop = true; };
    const jump = () => { if (!stop) document.getElementById(`dash-${anchor}`)?.scrollIntoView({ block: "start" }); };
    const ids = [120, 800, 1600].map((ms) => window.setTimeout(jump, ms));
    window.addEventListener("wheel", halt, { passive: true });
    window.addEventListener("touchstart", halt, { passive: true });
    return () => { ids.forEach((i) => window.clearTimeout(i)); window.removeEventListener("wheel", halt); window.removeEventListener("touchstart", halt); };
  }, [anchor, section]);

  const setSection = (s: ListSection) => {
    setSectionState(s);
    setAnchor(null);
    try { window.history.replaceState(window.history.state, "", sectionHref(s)); } catch { /* ignore */ }
  };
  // simple mode: Resumen, Agentes and Tareas open; Calidad, Costos and Informes behind "Más vistas"
  const shown = splitTabs(LIST_SECTIONS, SIMPLE_DASHBOARD, viewAll || more, [section]);
  return (
    <div data-testid="list-view" className="flex h-full min-h-0 flex-col md:flex-row">
      <nav data-tour="dashboard.nav" className="flex shrink-0 gap-0.5 overflow-x-auto border-b border-line bg-panel p-2 md:block md:w-[210px] md:space-y-0.5 md:overflow-y-auto md:border-b-0 md:border-r md:p-3">
        {shown.shown.map((k) => (
          <button key={k} type="button" data-testid={`dash-nav-${k}`} aria-current={section === k ? "page" : undefined} onClick={() => setSection(k)}
            className={`flex min-h-10 shrink-0 items-center justify-between gap-2 whitespace-nowrap rounded-md px-3 py-2 text-left text-[13px] transition md:min-h-0 md:w-full ${section === k ? "bg-accent-soft font-medium text-accent" : "text-ink2 hover:bg-mute/[0.08] hover:text-ink"}`}>
            {t(`dash.section.${k}`)}
          </button>
        ))}
        {!viewAll && <div className="flex shrink-0 items-center px-1 md:px-0 md:pt-1"><MoreButton testid="dash-more" count={more ? 1 : shown.folded.length} open={more} onToggle={() => setMore((m) => !m)} /></div>}
        {/* "Ir a": what has its own screen (on the phone the same links are the cards on top of Resumen) */}
        <div data-testid="dash-goto-nav" className="hidden md:mt-4 md:block md:border-t md:border-line md:pt-3">
          <div className="px-3 pb-1 text-[10px] font-semibold uppercase tracking-[0.08em] text-mute">{t("dash.goTo")}</div>
          {GO_TO.map((g) => {
            const n = counts[g.id];
            return (
              <a key={g.id} href={g.href} data-testid={`dash-nav-${g.id}`} data-goto={g.href}
                onClick={(e) => { if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return; e.preventDefault(); goRoute(g.href); }}
                className="flex w-full items-center gap-2 rounded-md px-3 py-1.5 text-[13px] text-ink2 transition hover:bg-mute/[0.08] hover:text-ink">
                <Icon name={g.icon} size={14} className="shrink-0 text-mute" />
                <span className="min-w-0 flex-1 truncate">{t(`dash.go.${g.id}`)}</span>
                {g.id === "approvals" && n ? <span className="rounded-full bg-warn px-1.5 text-[10px] font-semibold text-white">{n}</span>
                  : n != null ? <span className="font-mono text-[11px] text-mute">{n}</span> : null}
                <Icon name="arrow" size={12} className="shrink-0 text-mute" />
              </a>
            );
          })}
        </div>
      </nav>
      <div className="min-w-0 flex-1 overflow-y-auto p-3 sm:p-4 md:p-6">
        {section === "overview" && <Overview go={setSection} counts={counts} openDecisions={anchor === "decisions"} />}
        {section === "tasks" && <Tasks />}
        {section === "agents" && <Agents />}
        {section === "reports" && <Reports />}
        {section === "quality" && <QualityOverview />}
        {section === "costs" && <Costs />}
      </div>
    </div>
  );
}

/** "Ir a": one card per screen of its own, with its number. */
function GoToCards({ counts }: { counts: Record<GoToId, number | null> }) {
  const { t } = useT();
  return (
    <section data-testid="dash-goto" aria-label={t("dash.goTo")}>
      <div className="mb-2 flex items-baseline justify-between gap-2">
        <h2 className="text-[11px] font-semibold uppercase tracking-[0.08em] text-mute">{t("dash.goTo")}</h2>
        <span className="truncate text-[11px] text-mute">{t("dash.goTo.help")}</span>
      </div>
      <div className="grid grid-cols-2 gap-2 sm:gap-3 lg:grid-cols-4">
        {GO_TO.map((g) => {
          const n = counts[g.id];
          const warn = g.id === "approvals" && !!n;
          return (
            <a key={g.id} href={g.href} data-testid={`dash-go-${g.id}`} data-goto={g.href}
              onClick={(e) => { if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return; e.preventDefault(); goRoute(g.href); }}
              className={`group flex min-h-[76px] flex-col justify-between rounded-xl border bg-panel p-3 shadow-pop transition hover:border-accent sm:p-4 ${warn ? "border-amber-500/50" : "border-line"}`}>
              <div className="flex items-center justify-between gap-2">
                <span className="flex min-w-0 items-center gap-2 text-[13px] font-semibold text-ink">
                  <Icon name={g.icon} size={15} className={`shrink-0 ${warn ? "text-warn" : "text-accent"}`} />
                  <span className="truncate">{t(`dash.go.${g.id}`)}</span>
                </span>
                <Icon name="arrow" size={13} className="shrink-0 text-mute transition group-hover:translate-x-0.5 group-hover:text-accent" />
              </div>
              <div className="mt-1.5 flex items-end justify-between gap-2">
                <span className="line-clamp-2 min-w-0 text-[11px] leading-snug text-mute">{t(`dash.go.${g.id}.hint`)}</span>
                {n != null && <span data-testid={`dash-go-${g.id}-count`} className={`shrink-0 text-xl font-semibold tracking-tight ${warn ? "text-warn" : "text-ink"}`}>{n}</span>}
              </div>
            </a>
          );
        })}
      </div>
    </section>
  );
}

function Overview({ go, counts, openDecisions }: { go: (s: ListSection) => void; counts: Record<GoToId, number | null>; openDecisions: boolean }) {
  const { t } = useT();
  const m = useStore((s) => s.metrics);
  const agents = useStore((s) => s.agents);
  const agentPrefs = usePreferences((s) => s.prefs.agents);
  const order = useStore((s) => s.agentOrder);
  const approvalsMap = useStore((s) => s.approvals);
  const tasksMap = useStore((s) => s.tasks);
  const all = Object.values(approvalsMap).sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at));
  const approvals = all.filter((a) => a.status === "pending");
  const decided = all.filter((a) => a.status !== "pending");
  const pendingCount = usePendingCount();
  const select = useStore((s) => s.select);
  const active = Object.values(agents).filter((a) => !["idle", "completed"].includes(a.state)).length;
  const budgetPct = m ? Math.min(100, (m.cost_usd / Math.max(0.01, m.budget_usd)) * 100) : 0;
  return (
    <div className="space-y-5">
      <div data-tour="dashboard.kpis" className="grid grid-cols-2 gap-2 sm:grid-cols-3 sm:gap-3 xl:grid-cols-5 [&>*:last-child]:col-span-2 sm:[&>*:last-child]:col-span-1">
        <Kpi label={t("dash.kpi.tasksActive")} value={m?.tasks_active ?? 0} color="#2b6cb0" />
        <Kpi label={t("dash.kpi.tasksDone")} value={m?.tasks_done ?? 0} color="#2f7d55" />
        <Kpi label={t("common.cost")} value={fmtUsd(m?.cost_usd)} sub={t("dash.kpi.ofBudget", { budget: fmtUsd(m?.budget_usd), pct: budgetPct.toFixed(1) })} />
        <Kpi label={t("dash.kpi.errors")} value={m?.errors ?? 0} color={(m?.errors ?? 0) > 0 ? "#b4443c" : undefined} />
        <Kpi label={t("dash.kpi.activeAgents")} value={`${active}/${order.length}`} />
      </div>
      <GoToCards counts={counts} />
      <div className="grid gap-5 xl:grid-cols-3 [&>*]:min-w-0">
        <Card title={t("dash.team")} className="xl:col-span-2" right={<button type="button" className="text-[11px] text-accent" onClick={() => go("agents")}>{t("dash.viewAll")}</button>}>
          <div data-tour="dashboard.team" className="grid gap-2 md:grid-cols-2">
            {order.map((id) => {
              const a = agents[id]; if (!a) return null;
              const v = resolveAgent(a, agentPrefs[id]);
              return (
                <button key={id} type="button" onClick={() => openInOffice(id, select)} className="rounded-md border border-line bg-panel2 p-3 text-left transition hover:border-line-strong">
                  <div className="flex items-center justify-between gap-2"><span className="flex min-w-0 items-center gap-2 truncate text-[13px] font-semibold text-ink"><Dot color={v.color} />{v.name}</span><StateBadge state={a.state} /></div>
                  <div className="mt-1 truncate text-[11px] text-mute">{v.title}</div>
                  <div className="mt-1.5 truncate text-[11.5px] text-ink2">{a.activity || "—"}</div>
                  {a.progress > 0 && <div className="mt-2"><Progress value={a.progress} /></div>}
                  {a.current_task_id && tasksMap[a.current_task_id]?.assigned_reason && <ReasonChip agentId={id} reason={tasksMap[a.current_task_id].assigned_reason} compact className="mt-2" />}
                </button>
              );
            })}
          </div>
        </Card>
        <div data-tour="dashboard.side" className="space-y-5">
          {/* the decision stays one click away here; the whole list (and the questions) lives in Pendientes */}
          <Card title={t("dash.pendingApprovals")} right={pendingCount > 0 ? <button type="button" data-testid="dash-pending-all" className="text-[11px] text-accent" onClick={() => goRoute("/approvals/inbox")}>{t("dash.pending.seeAll", { count: pendingCount })}</button> : undefined}>
            {approvals.length ? <div className="space-y-2">{approvals.slice(0, 3).map((a) => <ApprovalCard key={a.id} a={a} compact />)}</div> : <Empty concept="pending">{t("dash.nothingToApprove")}</Empty>}
          </Card>
          <InitiativePanel />
          <Card title={t("hud.liveActivity")}><ActivityFeed limit={12} compact /></Card>
          {decided.length > 0 && (
            <details id="dash-decisions" data-testid="dash-decisions" open={openDecisions || undefined} className="group rounded-xl border border-line bg-panel shadow-pop">
              <summary className="flex min-h-10 cursor-pointer list-none items-center justify-between gap-2 px-4 py-2.5 text-[13px] font-semibold text-ink">
                {t("dash.decisions.title", { count: decided.length })}
                <Icon name="down" size={13} className="shrink-0 text-mute transition-transform group-open:rotate-180" />
              </summary>
              <div className="max-h-[420px] space-y-2 overflow-y-auto border-t border-line p-3">{decided.map((a) => <ApprovalCard key={a.id} a={a} compact />)}</div>
            </details>
          )}
        </div>
      </div>
    </div>
  );
}

function Tasks() {
  const { t: tt } = useT();
  const tasks = useStore((s) => s.tasks);
  const requests = useStore((s) => s.requests);
  const name = useAgentName();
  const color = useAgentColor();
  const [filter, setFilter] = useState("all");
  const [status, setStatus] = useState("all");
  const [open, setOpen] = useState<string | null>(null);
  const all = Object.values(tasks).sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at));
  const list = all.filter((t) => (filter === "all" || t.request_id === filter) && (status === "all" || t.status === status));
  const title = (id: string) => tasks[id]?.title ?? id;
  const dur = (t: Task) => (t.started_at && t.finished_at ? `${Math.round((+new Date(t.finished_at) - +new Date(t.started_at)) / 1000)}s` : "—");
  if (!all.length) return <Empty concept="task" how={tt("dash.tasks.how")}>{tt("dash.tasks.empty")}</Empty>;
  return (
    <Card title={tt("dash.tasks.title", { count: list.length })} right={
      <div className="flex gap-2">
        <select value={filter} onChange={(e) => setFilter(e.target.value)} className="max-w-[220px] rounded border border-line bg-panel2 px-2 py-1 text-[11px] text-ink">
          <option value="all">{tt("dash.tasks.allRequests")}</option>
          {Object.values(requests).map((r) => <option key={r.id} value={r.id}>{r.text.slice(0, 40)}</option>)}
        </select>
        <select value={status} onChange={(e) => setStatus(e.target.value)} className="rounded border border-line bg-panel2 px-2 py-1 text-[11px] text-ink">
          <option value="all">{tt("dash.tasks.allStatuses")}</option>
          {["pending", "running", "awaiting_approval", "blocked", "done", "failed"].map((s) => <option key={s} value={s}>{taskStatusLabel(s)}</option>)}
        </select>
      </div>}>
      <div className="overflow-x-auto wf-scroll-x">
        <table className="w-full text-left text-[12px]">
          <thead><tr className="border-b border-line text-[10px] uppercase tracking-wider text-mute">
            <th className="py-2 pr-3">{tt("dash.tasks.col.task")}</th><th className="pr-3">{tt("dash.tasks.col.agent")}</th><th className="pr-3">{tt("dash.tasks.col.status")}</th><th className="pr-3">{tt("dash.tasks.col.dependsOn")}</th><th className="pr-3">{tt("dash.tasks.col.start")}</th><th>{tt("dash.tasks.col.duration")}</th>
          </tr></thead>
          <tbody>
            {list.map((t) => (
              <Fragment key={t.id}>
                <tr onClick={() => setOpen(open === t.id ? null : t.id)} className="cursor-pointer border-b border-line/60 hover:bg-mute/[0.05]">
                  <td className="py-2.5 pr-3 font-medium text-ink">{t.title}{t.assigned_reason && <div className="mt-0.5 max-w-[320px] truncate text-[10.5px] font-normal text-mute" title={t.assigned_reason}>{tt("reason.assignedShort", { reason: t.assigned_reason })}</div>}</td>
                  <td className="pr-3"><span className="flex items-center gap-1.5"><Dot color={color(t.agent_id)} />{name(t.agent_id)}</span></td>
                  <td className="pr-3"><TaskBadge status={t.status} /></td>
                  <td className="pr-3 text-mute">{t.depends_on.length ? t.depends_on.map((d) => (
                    <span key={d} className="mr-1 inline-flex items-center gap-1 rounded bg-mute/[0.08] px-1.5 py-0.5 text-[10px]"><Dot color={TASK_STATUS_COLOR[tasks[d]?.status] || "#7b8494"} />{title(d)}</span>
                  )) : "—"}</td>
                  <td className="pr-3 font-mono text-mute">{fmtTime(t.started_at)}</td>
                  <td className="font-mono text-mute">{dur(t)}</td>
                </tr>
                {open === t.id && (
                  <tr key={`${t.id}-o`} className="border-b border-line/60 bg-bg/50"><td colSpan={6} className="p-3 text-[12px] text-ink2">
                    <p className="mb-2 text-mute">{t.description}</p>
                    <ReasonChip agentId={t.agent_id} reason={t.assigned_reason} className="mb-2" />
                    {t.output ? (<div className="space-y-1">{(() => { const a = taskAuditOf(t); return a ? <AuditDetail audit={a} testId={`task-audit-${t.id}`} /> : null; })()}<div className="font-semibold text-ink">{t.output.summary}</div>{t.output.findings.map((f, i) => <div key={i}>• {f}</div>)}
                      {Object.keys(t.output.metrics).length > 0 && <div className="flex flex-wrap gap-2 pt-1">{Object.entries(t.output.metrics).map(([k, v]) => <span key={k} className="rounded bg-mute/[0.08] px-2 py-0.5 font-mono text-[11px]">{k}: {String(v)}</span>)}</div>}
                      {t.output.recommendations.length > 0 && <div className="pt-1 text-mute">{tt("dash.tasks.recommendations")}: {t.output.recommendations.join(" · ")}</div>}
                      <div className="text-mute">{tt("common.confidence")} {Math.round(t.output.confidence * 100)}%</div></div>) : <span className="text-mute">{tt("dash.tasks.noOutput")}</span>}
                  </td></tr>
                )}
              </Fragment>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  );
}

function Agents() {
  const { t } = useT();
  const agents = useStore((s) => s.agents);
  const agentPrefs = usePreferences((s) => s.prefs.agents);
  const order = useStore((s) => s.agentOrder);
  const select = useStore((s) => s.select);
  return (
    <div className="space-y-6">
    <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
      {order.map((id) => {
        const a = agents[id]; if (!a) return null;
        const meta = roleMeta(a.role);
        const v = resolveAgent(a, agentPrefs[id]);
        return (
          <Card key={id}>
            <div className="mb-1 text-[10px] font-semibold uppercase tracking-[0.16em]" style={{ color: readable(v.color) }}>{meta.label}</div>
            <div className="flex items-start justify-between"><div><div className="text-base font-semibold text-ink">{v.name}</div><div className="text-xs text-mute">{v.title}</div></div><StateBadge state={a.state} /></div>
            <p className="mt-2 text-xs text-ink2">{a.activity}</p>
            {a.progress > 0 && <div className="mt-2"><Progress value={a.progress} /></div>}
            <div className="mt-3 grid grid-cols-2 gap-2 text-center sm:grid-cols-4">
              {[[t("dash.agents.done"), a.metrics.tasks_completed], [t("dash.agents.pend"), a.metrics.tasks_pending], [t("dash.agents.avg"), `${a.metrics.avg_seconds}s`], [t("common.cost"), fmtUsd(a.metrics.cost_usd)]].map(([k, v]) => (
                <div key={k as string} className="rounded bg-panel2 py-1.5"><div className="text-[9px] uppercase text-mute">{k}</div><div className="font-mono text-[12px] text-ink">{v}</div></div>
              ))}
            </div>
            <div className="mt-3 flex items-center justify-between text-[11px] text-mute"><span>{t("dash.agents.autonomy")}: {a.autonomy}</span>
              <button type="button" className="text-accent" onClick={() => openInOffice(id, select)}>{t("dash.agents.viewInOffice")}</button></div>
          </Card>
        );
      })}
    </div>
    {/* the conversations between agents used to be a section of their own (`?section=conversations` lands here) */}
    <section id="dash-conversations" data-testid="dash-conversations" className="scroll-mt-4">
      <h2 className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-mute">{t("dash.conversations.between")}</h2>
      <Conversations />
    </section>
    </div>
  );
}

function Conversations() {
  const { t } = useT();
  const conversations = useStore((s) => s.conversations);
  const name = useAgentName();
  const list = Object.values(conversations).sort((a, b) => +new Date(b.last_message_at) - +new Date(a.last_message_at));
  const [sel, setSel] = useState<string | null>(null);
  const cur = conversations[sel ?? list[0]?.id ?? ""];
  if (!list.length) return <Empty>{t("dash.conversations.empty")}</Empty>;
  return (
    <div className="grid gap-5 xl:grid-cols-[340px_1fr] [&>*]:min-w-0">
      <Card title={t("dash.conversations.title")}>
        <ul className="space-y-1.5">
          {list.map((c) => (
            <li key={c.id}><button type="button" onClick={() => setSel(c.id)} className={`w-full rounded-md border p-2.5 text-left ${cur?.id === c.id ? "border-accent bg-accent/10" : "border-line bg-panel2 hover:border-line-strong"}`}>
              <div className="line-clamp-1 text-[12.5px] font-semibold text-ink">{c.title}</div>
              <div className="mt-1 flex justify-between text-[10px] text-mute"><span>{c.participants.map((p) => name(p).split(" ")[0]).join(", ")}</span><span>{timeAgo(c.last_message_at)}</span></div>
            </button></li>
          ))}
        </ul>
      </Card>
      {cur && <Card title={cur.title}><ConversationThread key={cur.id} id={cur.id} height="max-h-[55vh]" /></Card>}
    </div>
  );
}

function Reports() {
  const { t } = useT();
  const reports = Object.values(useStore((s) => s.reports)).sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at));
  const [sel, setSel] = useState<string | null>(null);
  const cur = reports.find((r) => r.id === sel) ?? reports[0];
  if (!reports.length) return <Empty concept="report" how={t("dash.reports.how")} action={<button type="button" onClick={() => goRoute("/chats")} className="min-h-10 rounded-md border border-line-strong bg-panel px-3.5 py-1.5 text-xs font-medium text-ink hover:bg-panel2 sm:min-h-0">{t("dash.goChats")}</button>}>{t("dash.reports.empty")}</Empty>;
  return (
    <div className="grid gap-5 xl:grid-cols-[320px_1fr] [&>*]:min-w-0">
      <Card title={t("dash.reports.title")}><ul className="space-y-1.5">{reports.map((r) => (
        <li key={r.id}><button onClick={() => setSel(r.id)} className={`w-full rounded-md border p-2.5 text-left ${cur?.id === r.id ? "border-accent bg-accent/10" : "border-line bg-panel2 hover:border-line-strong"}`}>
          <div className="line-clamp-2 text-[12.5px] font-semibold text-ink">{r.title}</div><div className="mt-1 text-[10px] text-mute">{timeAgo(r.created_at)} · {fmtUsd(r.cost_usd)}</div>
        </button></li>))}</ul></Card>
      {cur && <Card><ReportView r={cur} /></Card>}
    </div>
  );
}

function Costs() {
  const { t: tt } = useT();
  const m = useStore((s) => s.metrics);
  const agentsMap = useStore((s) => s.agents);
  const agents = Object.values(agentsMap);
  const requests = Object.values(useStore((s) => s.requests));
  const errors = useStore((s) => s.errors);
  const tasks = useStore((s) => s.tasks);
  const name = useAgentName();
  const color = useAgentColor();
  const maxCost = Math.max(0.0001, ...agents.map((a) => a.metrics.cost_usd));
  const failed = Object.values(tasks).filter((t) => t.status === "failed" || t.status === "blocked");
  const pct = m ? Math.min(100, (m.cost_usd / Math.max(0.01, m.budget_usd)) * 100) : 0;
  return (
    <div className="grid gap-5 xl:grid-cols-2 [&>*]:min-w-0">
      <Card title={tt("dash.costs.budget")}>
        <div className="flex items-end justify-between"><div className="font-mono text-3xl text-ink">{fmtUsd(m?.cost_usd)}</div><div className="text-xs text-mute">{tt("dash.costs.of", { budget: fmtUsd(m?.budget_usd) })}</div></div>
        <div className="mt-3"><Progress value={pct} color={pct > 80 ? "#b4443c" : pct > 50 ? "#a86208" : "#2f7d55"} /></div>
        <div className="mt-1 text-[11px] text-mute">{tt("dash.costs.budgetPct", { pct: pct.toFixed(1) })}</div>
      </Card>
      <Card title={tt("dash.costs.perAgent")}>
        <div className="space-y-2">{agents.map((a) => (
          <div key={a.id}><div className="mb-0.5 flex justify-between text-[11px]"><span className="text-ink2">{a.name}</span><span className="font-mono text-mute">{fmtUsd(a.metrics.cost_usd)}</span></div>
            <div className="h-2 overflow-hidden rounded bg-mute/[0.08]"><div className="h-full rounded" style={{ width: `${(a.metrics.cost_usd / maxCost) * 100}%`, background: color(a.id) }} /></div></div>
        ))}</div>
      </Card>
      <Card title={tt("dash.costs.perRequest")}>
        {requests.length ? <table className="w-full text-[12px]"><tbody>{requests.map((r) => (
          <tr key={r.id} className="border-b border-line/60"><td className="line-clamp-1 py-2 pr-3 text-ink">{r.text.slice(0, 60)}</td><td className="text-right font-mono text-mute">{fmtUsd(r.cost_usd)}</td></tr>
        ))}</tbody></table> : <Empty>{tt("dash.costs.noRequests")}</Empty>}
      </Card>
      <div className="xl:col-span-2"><CostBreakdownPanel /></div>
      <div className="xl:col-span-2"><RoiPanel /></div>
      <BudgetLimitsPanel />
      <Card title={tt("dash.costs.errorsTitle", { count: errors.length + failed.length })}>
        {errors.length + failed.length === 0 ? <Empty>{tt("dash.costs.noErrors")}</Empty> : (
          <ul className="space-y-2 text-[12px]">
            {errors.map((e) => (<li key={e.id} className="rounded-md border border-red-500/30 bg-red-500/5 p-2.5"><div className="flex justify-between text-[10px] text-mute"><span>{e.agent_id ? name(e.agent_id) : tt("common.system")}</span><span className="font-mono">{fmtTime(e.ts)}</span></div><div className="text-red-700">{e.message}</div></li>))}
            {failed.map((t) => (<li key={t.id} className="rounded-md border border-line bg-panel2 p-2.5"><div className="flex justify-between"><span className="text-ink">{t.title}</span><TaskBadge status={t.status} /></div><div className="text-[10px] text-mute">{name(t.agent_id)}</div></li>))}
          </ul>
        )}
      </Card>
    </div>
  );
}
