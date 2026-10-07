"use client";
import { Fragment, useMemo, useState } from "react";
import { useStore } from "@/lib/store";
import { trOr, useT } from "@/lib/i18n";
import { fmtTime, fmtUsd, roleMeta, taskStatusLabel, TASK_STATUS_COLOR } from "@/lib/meta";
import { resolveAgent, usePreferences } from "@/lib/preferences";
import type { Task } from "@/lib/types";
import { ActivityFeed } from "../ActivityFeed";
import { ApprovalCard } from "../ApprovalsTray";
import { ConversationThread } from "../Conversation";
import { ReportView } from "../ReportView";
import { DashboardFiles } from "../workspace/DashboardFiles";
import { BudgetLimitsPanel, CostBreakdownPanel } from "../cost/CostBreakdownPanel";
import { EstimateCard } from "../cost/EstimateCard";
import { Card, Dot, Empty, Progress, StateBadge, TaskBadge, readable, timeAgo, useAgentColor, useAgentName } from "../ui";

const SECTIONS = ["overview", "requests", "tasks", "agents", "conversations", "approvals", "reports", "files", "costs"] as const;
type Section = (typeof SECTIONS)[number];

function Kpi({ label, value, sub, color = "#151b26" }: { label: string; value: string | number; sub?: string; color?: string }) {
  return (
    <div className="rounded-xl border border-line bg-panel shadow-pop p-4">
      <div className="text-[10px] font-semibold uppercase tracking-[0.08em] text-mute">{label}</div>
      <div className="mt-1.5 text-2xl font-semibold tracking-tight" style={{ color }}>{value}</div>
      {sub && <div className="mt-0.5 text-[11px] text-mute">{sub}</div>}
    </div>
  );
}

export function Dashboard() {
  const { t } = useT();
  const [section, setSection] = useState<Section>("overview");
  const approvalsMap = useStore((s) => s.approvals);
  const pending = Object.values(approvalsMap).filter((a) => a.status === "pending").length;
  return (
    <div className="flex h-full min-h-0 flex-col md:flex-row">
      <nav className="flex shrink-0 gap-0.5 overflow-x-auto border-b border-line bg-panel p-2 md:block md:w-[210px] md:space-y-0.5 md:overflow-visible md:border-b-0 md:border-r md:p-3">
        {SECTIONS.map((k) => (
          <button key={k} data-testid={`dash-nav-${k}`} onClick={() => setSection(k)}
            className={`flex shrink-0 items-center justify-between gap-2 whitespace-nowrap rounded-md px-3 py-2 text-left text-[13px] transition md:w-full ${section === k ? "bg-accent-soft font-medium text-accent" : "text-ink2 hover:bg-mute/[0.08] hover:text-ink"}`}>
            {t(`dash.section.${k}`)}
            {k === "approvals" && pending > 0 && <span className="rounded-full bg-warn px-1.5 text-[10px] font-semibold text-white">{pending}</span>}
          </button>
        ))}
      </nav>
      <div className="min-w-0 flex-1 overflow-y-auto p-4 md:p-6">
        {section === "overview" && <Overview go={setSection} />}
        {section === "requests" && <Requests />}
        {section === "tasks" && <Tasks />}
        {section === "agents" && <Agents />}
        {section === "conversations" && <Conversations />}
        {section === "approvals" && <Approvals />}
        {section === "reports" && <Reports />}
        {section === "files" && <DashboardFiles />}
        {section === "costs" && <Costs />}
      </div>
    </div>
  );
}

function Overview({ go }: { go: (s: Section) => void }) {
  const { t } = useT();
  const m = useStore((s) => s.metrics);
  const agents = useStore((s) => s.agents);
  const agentPrefs = usePreferences((s) => s.prefs.agents);
  const order = useStore((s) => s.agentOrder);
  const approvalsMap = useStore((s) => s.approvals);
  const approvals = Object.values(approvalsMap).filter((a) => a.status === "pending");
  const select = useStore((s) => s.select);
  const setMode = useStore((s) => s.setMode);
  const active = Object.values(agents).filter((a) => !["idle", "completed"].includes(a.state)).length;
  const budgetPct = m ? Math.min(100, (m.cost_usd / Math.max(0.01, m.budget_usd)) * 100) : 0;
  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4 xl:grid-cols-7">
        <Kpi label={t("dash.kpi.requests")} value={m?.requests_total ?? 0} />
        <Kpi label={t("dash.kpi.tasksActive")} value={m?.tasks_active ?? 0} color="#2b6cb0" />
        <Kpi label={t("dash.kpi.tasksDone")} value={m?.tasks_done ?? 0} color="#2f7d55" />
        <Kpi label={t("dash.kpi.approvals")} value={m?.approvals_pending ?? 0} color={(m?.approvals_pending ?? 0) > 0 ? "#a86208" : undefined} sub={t("dash.kpi.pending")} />
        <Kpi label={t("common.cost")} value={fmtUsd(m?.cost_usd)} sub={t("dash.kpi.ofBudget", { budget: fmtUsd(m?.budget_usd), pct: budgetPct.toFixed(1) })} />
        <Kpi label={t("dash.kpi.errors")} value={m?.errors ?? 0} color={(m?.errors ?? 0) > 0 ? "#b4443c" : undefined} />
        <Kpi label={t("dash.kpi.activeAgents")} value={`${active}/${order.length}`} />
      </div>
      <div className="grid gap-5 xl:grid-cols-3">
        <Card title={t("dash.team")} className="xl:col-span-2" right={<button className="text-[11px] text-accent" onClick={() => go("agents")}>{t("dash.viewAll")}</button>}>
          <div className="grid gap-2 md:grid-cols-2">
            {order.map((id) => {
              const a = agents[id]; if (!a) return null;
              const meta = roleMeta(a.role);
              const v = resolveAgent(a, agentPrefs[id]);
              return (
                <button key={id} onClick={() => { select(id); setMode("office"); }} className="rounded-md border border-line bg-panel2 p-3 text-left transition hover:border-line-strong">
                  <div className="flex items-center justify-between"><span className="flex items-center gap-2 text-[13px] font-semibold text-ink"><Dot color={v.color} />{v.name}</span><StateBadge state={a.state} /></div>
                  <div className="mt-1 truncate text-[11px] text-mute">{v.title}</div>
                  <div className="mt-1.5 truncate text-[11.5px] text-ink2">{a.activity || "—"}</div>
                  {a.progress > 0 && <div className="mt-2"><Progress value={a.progress} /></div>}
                </button>
              );
            })}
          </div>
        </Card>
        <div className="space-y-5">
          <Card title={t("dash.pendingApprovals")}>
            {approvals.length ? <div className="space-y-2">{approvals.map((a) => <ApprovalCard key={a.id} a={a} compact />)}</div> : <Empty>{t("dash.nothingToApprove")}</Empty>}
          </Card>
          <Card title={t("hud.liveActivity")}><ActivityFeed limit={12} compact /></Card>
        </div>
      </div>
    </div>
  );
}

function depthOf(tasks: { id: string; depends_on: string[] }[]) {
  const map = new Map(tasks.map((t) => [t.id, t]));
  const memo = new Map<string, number>();
  const d = (id: string, seen = new Set<string>()): number => {
    if (memo.has(id)) return memo.get(id)!;
    if (seen.has(id)) return 0;
    seen.add(id);
    const t = map.get(id);
    const v = !t || !t.depends_on.length ? 0 : 1 + Math.max(...t.depends_on.map((x) => d(x, seen)));
    memo.set(id, v);
    return v;
  };
  return (id: string) => d(id);
}

function Requests() {
  const { t } = useT();
  const requests = useStore((s) => s.requests);
  const tasks = useStore((s) => s.tasks);
  const plans = useStore((s) => s.plans);
  const reports = useStore((s) => s.reports);
  const name = useAgentName();
  const color = useAgentColor();
  const list = Object.values(requests).sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at));
  const [sel, setSel] = useState<string | null>(null);
  const cur = requests[sel ?? list[0]?.id ?? ""];
  const items = useMemo(() => {
    if (!cur) return [];
    const real = Object.values(tasks).filter((t) => t.request_id === cur.id);
    if (real.length) return real.map((t) => ({ id: t.id, title: t.title, agent_id: t.agent_id, depends_on: t.depends_on, status: t.status as string }));
    return (plans[cur.id] || []).map((p) => ({ ...p, status: "pending" }));
  }, [cur, tasks, plans]);
  const depth = depthOf(items);
  const cols = Math.max(0, ...items.map((i) => depth(i.id))) + 1;
  const statusColor: Record<string, string> = { planning: "#7c6bb0", running: "#2b6cb0", awaiting_approval: "#a86208", awaiting_confirmation: "#a86208", paused: "#b4443c", done: "#2f7d55", failed: "#b4443c" };
  const statusLabel = (st: string) => trOr(`dash.reqStatus.${st}`, st);
  if (!list.length) return <Empty>{t("dash.requests.empty")}</Empty>;
  return (
    <div className="grid gap-5 xl:grid-cols-[340px_1fr]">
      <Card title={t("dash.requests.title")}>
        <ul className="space-y-1.5">
          {list.map((r) => (
            <li key={r.id}>
              <button onClick={() => setSel(r.id)} className={`w-full rounded-md border p-2.5 text-left ${cur?.id === r.id ? "border-accent bg-accent/10" : "border-line bg-panel2 hover:border-line-strong"}`}>
                <div className="line-clamp-2 text-[12.5px] text-ink">{r.text}</div>
                <div className="mt-1.5 flex items-center justify-between text-[10px]">
                  <span className="font-semibold uppercase tracking-wide" style={{ color: statusColor[r.status] }}>{statusLabel(r.status)}</span>
                  <span className="text-mute">{timeAgo(r.created_at)} · {fmtUsd(r.cost_usd)}</span>
                </div>
              </button>
            </li>
          ))}
        </ul>
      </Card>
      {cur && (
        <Card title={t("dash.requests.plan", { count: items.length })} right={cur.report_id && reports[cur.report_id] ? <span className="text-[11px] text-emerald-700">{t("dash.requests.reportReady")}</span> : undefined}>
          <p className="mb-4 text-sm text-ink">{cur.text}</p>
          <div className="mb-4"><EstimateCard requestId={cur.id} compact /></div>
          {items.length ? (
            <div className="grid gap-3 overflow-x-auto" style={{ gridTemplateColumns: `repeat(${cols}, minmax(200px, 1fr))` }}>
              {Array.from({ length: cols }).map((_, c) => (
                <div key={c} className="space-y-2">
                  <div className="text-[10px] font-semibold uppercase tracking-widest text-mute">{c === 0 ? t("dash.requests.start") : t("dash.requests.phase", { n: c + 1 })}</div>
                  {items.filter((i) => depth(i.id) === c).map((i) => (
                    <div key={i.id} className="rounded-md border bg-panel2 p-2.5" style={{ borderColor: `${TASK_STATUS_COLOR[i.status] || "#e2e6ec"}88`, borderLeftWidth: 3, borderLeftColor: color(i.agent_id) }}>
                      <div className="text-[12px] font-semibold text-ink">{i.title}</div>
                      <div className="mt-1 flex items-center justify-between"><span className="text-[11px]" style={{ color: readable(color(i.agent_id)) }}>{name(i.agent_id)}</span><TaskBadge status={i.status} /></div>
                      {i.depends_on.length > 0 && <div className="mt-1 text-[10px] text-mute">{t("dash.dependsOn")}: {i.depends_on.map((d) => items.find((x) => x.id === d)?.title ?? d).join(", ")}</div>}
                    </div>
                  ))}
                </div>
              ))}
            </div>
          ) : <Empty>{t("dash.requests.waitingPlan")}</Empty>}
        </Card>
      )}
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
  if (!all.length) return <Empty>{tt("dash.tasks.empty")}</Empty>;
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
      <div className="overflow-x-auto">
        <table className="w-full text-left text-[12px]">
          <thead><tr className="border-b border-line text-[10px] uppercase tracking-wider text-mute">
            <th className="py-2 pr-3">{tt("dash.tasks.col.task")}</th><th className="pr-3">{tt("dash.tasks.col.agent")}</th><th className="pr-3">{tt("dash.tasks.col.status")}</th><th className="pr-3">{tt("dash.tasks.col.dependsOn")}</th><th className="pr-3">{tt("dash.tasks.col.start")}</th><th>{tt("dash.tasks.col.duration")}</th>
          </tr></thead>
          <tbody>
            {list.map((t) => (
              <Fragment key={t.id}>
                <tr onClick={() => setOpen(open === t.id ? null : t.id)} className="cursor-pointer border-b border-line/60 hover:bg-mute/[0.05]">
                  <td className="py-2.5 pr-3 font-medium text-ink">{t.title}</td>
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
                    {t.output ? (<div className="space-y-1"><div className="font-semibold text-ink">{t.output.summary}</div>{t.output.findings.map((f, i) => <div key={i}>• {f}</div>)}
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
  const setMode = useStore((s) => s.setMode);
  return (
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
            <div className="mt-3 grid grid-cols-4 gap-2 text-center">
              {[[t("dash.agents.done"), a.metrics.tasks_completed], [t("dash.agents.pend"), a.metrics.tasks_pending], [t("dash.agents.avg"), `${a.metrics.avg_seconds}s`], [t("common.cost"), fmtUsd(a.metrics.cost_usd)]].map(([k, v]) => (
                <div key={k as string} className="rounded bg-panel2 py-1.5"><div className="text-[9px] uppercase text-mute">{k}</div><div className="font-mono text-[12px] text-ink">{v}</div></div>
              ))}
            </div>
            <div className="mt-3 flex items-center justify-between text-[11px] text-mute"><span>{t("dash.agents.autonomy")}: {a.autonomy}</span>
              <button className="text-accent" onClick={() => { select(id); setMode("office"); }}>{t("dash.agents.viewInOffice")}</button></div>
          </Card>
        );
      })}
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
    <div className="grid gap-5 xl:grid-cols-[340px_1fr]">
      <Card title={t("dash.conversations.title")}>
        <ul className="space-y-1.5">
          {list.map((c) => (
            <li key={c.id}><button onClick={() => setSel(c.id)} className={`w-full rounded-md border p-2.5 text-left ${cur?.id === c.id ? "border-accent bg-accent/10" : "border-line bg-panel2 hover:border-line-strong"}`}>
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

function Approvals() {
  const { t } = useT();
  const approvals = Object.values(useStore((s) => s.approvals)).sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at));
  const pend = approvals.filter((a) => a.status === "pending"), done = approvals.filter((a) => a.status !== "pending");
  return (
    <div className="grid gap-5 xl:grid-cols-2">
      <Card title={t("dash.approvals.pending", { count: pend.length })}>{pend.length ? <div className="space-y-2">{pend.map((a) => <ApprovalCard key={a.id} a={a} />)}</div> : <Empty>{t("dash.approvals.nonePending")}</Empty>}</Card>
      <Card title={t("dash.approvals.history")}>{done.length ? <div className="space-y-2">{done.map((a) => <ApprovalCard key={a.id} a={a} compact />)}</div> : <Empty>{t("dash.approvals.noDecisions")}</Empty>}</Card>
    </div>
  );
}

function Reports() {
  const { t } = useT();
  const reports = Object.values(useStore((s) => s.reports)).sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at));
  const [sel, setSel] = useState<string | null>(null);
  const cur = reports.find((r) => r.id === sel) ?? reports[0];
  if (!reports.length) return <Empty>{t("dash.reports.empty")}</Empty>;
  return (
    <div className="grid gap-5 xl:grid-cols-[320px_1fr]">
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
    <div className="grid gap-5 xl:grid-cols-2">
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
