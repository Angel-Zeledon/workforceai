"use client";
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { useStore } from "@/lib/store";
import { useT } from "@/lib/i18n";
import { fmtTime, fmtUsd } from "@/lib/meta";
import { go as goRoute } from "@/lib/navBridge";
import { BudgetLimitsPanel, CostBreakdownPanel } from "../cost/CostBreakdownPanel";
import { RoiPanel } from "../cost/RoiPanel";
import { QualityOverview } from "../quality/QualityOverview";
import { ConversationThread } from "../Conversation";
import { ReportView } from "../ReportView";
import { Card, Empty, Progress, TaskBadge, timeAgo, useAgentColor, useAgentName } from "../ui";
import { useViewAll } from "@/lib/viewMode";
import { SIMPLE_DASHBOARD, splitTabs } from "@/lib/viewModeLogic";
import { MoreButton } from "../nav/ViewAll";
import { LIST_SECTIONS, sectionHref, targetOfSearch, type ListSection } from "./listViewLogic";
import { WorkSpace } from "./workspace/WorkSpace";
import { TasksView } from "./workspace/TasksView";
import { AgentsView } from "./workspace/AgentsView";
import { BlockBoundary } from "./workspace/BlockBoundary";

/**
 * The Office's "Lista": a workspace for working with several agents (docs/architecture/equipos-oficinas.md, section 8).
 * "Trabajo" is the screen: ask the team, see who does it, answer what they ask, collect what is done. Tareas, Agentes,
 * Informes, Calidad and Costos are quiet tabs in the header (simple mode keeps Trabajo, Agentes and Tareas open).
 * Old links (`/dashboard?section=customers`, `?tab=files`...) land on the screen or section that holds them now.
 */

function Stat({ label, value, sub, tone }: { label: string; value: string | number; sub?: string; tone?: string }) {
  return (
    <div className="min-w-[120px] flex-1 border-l border-line pl-4 first:border-l-0 first:pl-0">
      <div className="text-[12.5px] text-ink2">{label}</div>
      <div className={`text-[22px] font-semibold tracking-tight ${tone ?? "text-ink"}`}>{value}</div>
      {sub && <div className="text-[12px] text-mute">{sub}</div>}
    </div>
  );
}

export function Dashboard() {
  const { t } = useT();
  const router = useRouter();
  const select = useStore((s) => s.select);
  const [section, setSectionState] = useState<ListSection>("overview");
  const [anchor, setAnchor] = useState<string | null>(null);
  const [talk, setTalk] = useState<string | null>(null);
  const viewAll = useViewAll();
  const [more, setMore] = useState(false);

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
  // A block inside the section (conversations under Agentes, decisions in Trabajo): scroll to it once it is drawn.
  useEffect(() => {
    if (!anchor) return;
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
  const shown = splitTabs(LIST_SECTIONS, SIMPLE_DASHBOARD, viewAll || more, [section]);
  const label = (k: ListSection) => (k === "overview" ? t("work.tab.work") : t(`dash.section.${k}`));
  return (
    <div data-testid="list-view" className="flex h-full min-h-0 flex-col bg-bg">
      <nav aria-label={t("work.views")} data-tour="dashboard.nav" className="flex shrink-0 items-center gap-1 overflow-x-auto border-b border-line bg-panel px-2 md:px-4">
        {shown.shown.map((k) => (
          <button key={k} type="button" data-testid={`dash-nav-${k}`} aria-current={section === k ? "page" : undefined} onClick={() => setSection(k)}
            className={`relative min-h-11 shrink-0 whitespace-nowrap px-3 text-[13.5px] transition-colors ${section === k ? "font-semibold text-accent" : "font-medium text-ink2 hover:text-ink"}`}>
            {label(k)}
            {section === k && <span aria-hidden className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-accent" />}
          </button>
        ))}
        {!viewAll && <div className="flex shrink-0 items-center px-1"><MoreButton testid="dash-more" count={more ? 1 : shown.folded.length} open={more} onToggle={() => setMore((m) => !m)} /></div>}
      </nav>
      <div className="min-h-0 flex-1">
        {section === "overview" ? (
          <WorkSpace picked={talk} setPicked={setTalk} onOpenReport={() => setSection("reports")} onOpenPanel={select} openDecisions={anchor === "decisions"} />
        ) : (
          <div className="h-full overflow-y-auto p-3 sm:p-4 md:p-6">
            <BlockBoundary name={`section-${section}`}>
              {section === "tasks" && <TasksView />}
              {section === "agents" && (
                <div className="space-y-8">
                  <AgentsView onTalk={(id) => { setTalk(id); setSection("overview"); }} />
                  <section id="dash-conversations" data-testid="dash-conversations" className="mx-auto w-full max-w-4xl scroll-mt-4">
                    <h2 className="mb-2 text-[14px] font-semibold text-ink">{t("dash.conversations.between")}</h2>
                    <Conversations />
                  </section>
                </div>
              )}
              {section === "reports" && <Reports />}
              {section === "quality" && <QualityOverview />}
              {section === "costs" && <Costs />}
            </BlockBoundary>
          </div>
        )}
      </div>
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
    <div className="space-y-5">
    <div data-testid="costs-stats" className="flex flex-wrap gap-x-6 gap-y-4 rounded-[10px] border border-line bg-panel p-4">
      <Stat label={tt("dash.kpi.tasksActive")} value={m?.tasks_active ?? 0} />
      <Stat label={tt("dash.kpi.tasksDone")} value={m?.tasks_done ?? 0} />
      <Stat label={tt("common.cost")} value={fmtUsd(m?.cost_usd)} sub={tt("dash.kpi.ofBudget", { budget: fmtUsd(m?.budget_usd), pct: pct.toFixed(1) })} />
      <Stat label={tt("dash.kpi.errors")} value={m?.errors ?? 0} tone={(m?.errors ?? 0) > 0 ? "text-err" : undefined} />
    </div>
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
            {errors.map((ev) => (<li key={ev.id} className="rounded-md border border-red-500/30 bg-red-500/5 p-2.5"><div className="flex justify-between text-[10px] text-mute"><span>{ev.agent_id ? name(ev.agent_id) : tt("common.system")}</span><span className="font-mono">{fmtTime(ev.ts)}</span></div><div className="text-red-700">{ev.message}</div></li>))}
            {failed.map((t) => (<li key={t.id} className="rounded-md border border-line bg-panel2 p-2.5"><div className="flex justify-between"><span className="text-ink">{t.title}</span><TaskBadge status={t.status} /></div><div className="text-[10px] text-mute">{name(t.agent_id)}</div></li>))}
          </ul>
        )}
      </Card>
    </div>
    </div>
  );
}
