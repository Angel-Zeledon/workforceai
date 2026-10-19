"use client";
import { useState } from "react";
import { fmtDateTime, fmtUsd, useT } from "@/lib/i18n";
import type { Chart, ChartDept, ChartMember } from "@/lib/orgChartApi";
import { KindIcon, allDepts } from "./shared";

interface Handlers { onDept: (id: string) => void; onAgent: (id: string) => void; onPerson: (id: string) => void }

function MemberRow({ m, h }: { m: ChartMember; h: Handlers }) {
  const { t } = useT();
  const click = () => (m.kind === "agent" ? h.onAgent(m.id) : h.onPerson(m.id));
  return (
    <li>
      <button type="button" data-testid={`org-member-${m.kind}-${m.id}`} onClick={click}
        className="flex w-full items-start gap-2 rounded-lg border border-line bg-bg px-2.5 py-2 text-left hover:border-accent/50">
        <KindIcon kind={m.kind} />
        <span className="min-w-0 flex-1">
          <span className="flex flex-wrap items-center gap-1.5">
            <span className="truncate text-[13px] font-semibold">{m.name}</span>
            {m.head && <span className="rounded bg-accent/15 px-1.5 py-[1px] text-[10px] font-semibold uppercase tracking-wide text-accent">{t("org.head")}</span>}
          </span>
          <span className="block truncate text-[11px] text-mute">{m.title}</span>
          {m.reports_to_name && <span className="block truncate text-[11px] text-mute">{t("org.reportsToName", { name: m.reports_to_name })}</span>}
          {m.kind === "agent" && (
            <span className="mt-1 flex flex-wrap items-center gap-1.5 text-[10px] font-semibold">
              {m.open === undefined ? (
                <span className="rounded bg-panel2 px-1.5 py-[1px] text-mute">{t("org.hours.always")}</span>
              ) : m.open ? (
                <span data-testid={`org-open-${m.id}`} className="rounded bg-emerald-500/15 px-1.5 py-[1px] text-emerald-700">{t("org.hours.open")}</span>
              ) : (
                <span data-testid={`org-closed-${m.id}`} className="rounded bg-amber-500/15 px-1.5 py-[1px] text-amber-700">
                  {m.next_open ? t("org.hours.closedUntil", { when: fmtDateTime(m.next_open) }) : t("org.hours.closed")}
                </span>
              )}
              {m.budget_usd != null && <span className="rounded bg-panel2 px-1.5 py-[1px] text-mute">{t("org.budget.perMonth", { amount: fmtUsd(m.budget_usd) })}</span>}
            </span>
          )}
        </span>
      </button>
    </li>
  );
}

function DeptBox({ d, h, depth }: { d: ChartDept; h: Handlers; depth: number }) {
  const { t } = useT();
  return (
    <div data-testid={`org-dept-${d.id}`} className="rounded-xl border border-line bg-panel p-3">
      <div className="mb-2 flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <button type="button" data-testid={`org-dept-edit-${d.id}`} onClick={() => h.onDept(d.id)} className="text-left font-display text-[15px] font-semibold hover:text-accent">{d.name}</button>
          <div className="flex flex-wrap gap-x-3 text-[11px] text-mute">
            {d.cost_center && <span>{t("org.costCenter")}: {d.cost_center}</span>}
            <span>{t("org.headOf", { name: d.head_name || t("org.noHead") })}</span>
            <span>{t("org.counts", { people: d.people, agents: d.agents })}</span>
          </div>
        </div>
      </div>
      {d.members.length > 0 && <ul className="grid gap-1.5 sm:grid-cols-2">{d.members.map((m) => <MemberRow key={`${m.kind}:${m.id}`} m={m} h={h} />)}</ul>}
      {d.children.length > 0 && (
        <div className={`mt-3 grid gap-3 border-l-2 border-line pl-3 ${depth < 1 ? "lg:grid-cols-2" : ""}`}>
          {d.children.map((c) => <DeptBox key={c.id} d={c} h={h} depth={depth + 1} />)}
        </div>
      )}
    </div>
  );
}

function ListMode({ chart, h }: { chart: Chart; h: Handlers }) {
  const { t } = useT();
  const rows: { d: ChartDept; depth: number }[] = [];
  const walk = (ds: ChartDept[], depth: number) => ds.forEach((d) => { rows.push({ d, depth }); walk(d.children, depth + 1); });
  walk(chart.roots, 0);
  return (
    <ul className="grid gap-1" data-testid="org-list">
      {rows.map(({ d, depth }) => (
        <li key={d.id} style={{ paddingLeft: depth * 16 }}>
          <button type="button" onClick={() => h.onDept(d.id)} className="text-[13px] font-semibold hover:text-accent">{d.name}</button>
          <span className="ml-2 text-[11px] text-mute">{t("org.counts", { people: d.people, agents: d.agents })}</span>
          <ul className="ml-4">
            {d.members.map((m) => (
              <li key={`${m.kind}:${m.id}`} className="text-[12px]">
                <button type="button" onClick={() => (m.kind === "agent" ? h.onAgent(m.id) : h.onPerson(m.id))} className="hover:text-accent">
                  {m.kind === "agent" ? "◆" : "●"} {m.name}
                </button>
                <span className="text-mute"> — {m.title}{m.reports_to_name ? ` · ${t("org.reportsToName", { name: m.reports_to_name })}` : ""}</span>
              </li>
            ))}
          </ul>
        </li>
      ))}
    </ul>
  );
}

/** The org chart: departments nested down the hierarchy with their people and agents, plus the unassigned lane. */
export function OrgChartTree({ chart, onDept, onAgent, onPerson, onNewDept }: Handlers & { chart: Chart; onNewDept: () => void }) {
  const { t } = useT();
  const [mode, setMode] = useState<"tree" | "list">("tree");
  const h = { onDept, onAgent, onPerson };
  const hasDepts = allDepts(chart).length > 0;
  return (
    <div data-testid="org-chart">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div className="text-[12px] text-mute">{t("org.summary", { people: chart.people, agents: chart.agents, departments: allDepts(chart).length })}</div>
        <div role="tablist" aria-label={t("org.view.label")} className="flex gap-1">
          {(["tree", "list"] as const).map((m) => (
            <button key={m} type="button" role="tab" aria-selected={mode === m} data-testid={`org-mode-${m}`} onClick={() => setMode(m)}
              className={`rounded-md border px-3 py-1 text-[11px] font-semibold ${mode === m ? "border-accent/30 bg-accent-soft text-accent" : "border-line text-mute hover:text-ink"}`}>{t(`org.view.${m}`)}</button>
          ))}
        </div>
      </div>
      {!hasDepts && (
        <div className="mb-4 rounded-xl border border-dashed border-line-strong bg-panel p-5 text-center text-[13px] text-mute" data-testid="org-empty">
          <p className="mb-3">{t("org.empty.body")}</p>
          <button type="button" data-testid="org-empty-new" onClick={onNewDept} className="rounded-md bg-accent px-4 py-2 text-xs font-semibold text-white">{t("org.dept.new")}</button>
        </div>
      )}
      {hasDepts && (mode === "tree" ? (
        <div className="grid gap-3">{chart.roots.map((d) => <DeptBox key={d.id} d={d} h={h} depth={0} />)}</div>
      ) : <ListMode chart={chart} h={h} />)}
      {chart.unassigned.length > 0 && (
        <section className="mt-5" aria-label={t("org.unassigned")} data-testid="org-unassigned">
          <h3 className="mb-2 text-[12px] font-semibold uppercase tracking-wide text-mute">{t("org.unassigned")} ({chart.unassigned.length})</h3>
          <ul className="grid gap-1.5 sm:grid-cols-2 lg:grid-cols-3">{chart.unassigned.map((m) => <MemberRow key={`${m.kind}:${m.id}`} m={m} h={h} />)}</ul>
        </section>
      )}
    </div>
  );
}
