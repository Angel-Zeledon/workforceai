"use client";
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { fmtDateTime, fmtUsd, useT } from "@/lib/i18n";
import { orgChartApi, type Chart } from "@/lib/orgChartApi";
import { flattenDepartments } from "@/lib/orgChartLogic";
import { PageHeader } from "../nav/ui2";
import { AgentRoleEditor } from "./AgentRoleEditor";
import { DelegationPanel } from "./DelegationPanel";
import { DepartmentEditor } from "./DepartmentEditor";
import { OrgChartTree } from "./OrgChartTree";
import { PersonEditor } from "./PersonEditor";
import { allDepts, allMembers, btnPrimary } from "./shared";

type Tab = "chart" | "departments" | "roles" | "delegation";
const TABS: Tab[] = ["chart", "departments", "roles", "delegation"];

/** Organization page: org chart, department editor, agent job descriptions and delegation rules. */
export function OrgPage() {
  const { t } = useT();
  const [tab, setTab] = useState<Tab>("chart");
  const [chart, setChart] = useState<Chart | null>(null);
  const [error, setError] = useState(false);
  const [dept, setDept] = useState<string | null | undefined>(undefined); // undefined = closed, null = new
  const [agent, setAgent] = useState<string | null>(null);
  const [person, setPerson] = useState<string | null>(null);

  const load = useCallback(() => {
    setError(false);
    orgChartApi.chart().then(setChart).catch(() => setError(true));
  }, []);
  useEffect(load, [load]);
  const saved = () => { setDept(undefined); setAgent(null); setPerson(null); load(); };

  const depts = allDepts(chart);
  const flat = flattenDepartments(depts.map((d) => ({ id: d.id, name: d.name, parent_id: d.parent_id })));
  const byId = new Map(depts.map((d) => [d.id, d]));
  const agents = allMembers(chart).filter((m) => m.kind === "agent");

  return (
    <div className="flex h-full flex-col overflow-y-auto" data-testid="org-page">
      <PageHeader title={t("org.title")} description={t("org.subtitle")} testid="org-header"
        actions={<button type="button" data-testid="org-new-dept" onClick={() => setDept(null)} className={btnPrimary}>{t("org.dept.new")}</button>} />
      <div role="tablist" aria-label={t("org.title")} className="flex flex-wrap gap-1.5 border-b border-line bg-panel px-5 py-2 sm:px-6">
        {TABS.map((k) => (
          <button key={k} type="button" role="tab" aria-selected={tab === k} data-testid={`org-tab-${k}`} onClick={() => setTab(k)}
            className={`rounded-md border px-3 py-1 text-[12px] font-semibold ${tab === k ? "border-accent/30 bg-accent-soft text-accent" : "border-line text-mute hover:text-ink"}`}>{t(`org.tab.${k}`)}</button>
        ))}
        <Link href="/settings?s=templates" prefetch={false} data-testid="org-industries-link" className="ml-auto self-center text-[12px] font-semibold text-accent hover:underline">{t("org.industriesLink")}</Link>
      </div>
      <div className="mx-auto w-full max-w-6xl flex-1 px-5 py-5 sm:px-6">
        {error && <div role="alert" data-testid="org-error" className="text-sm font-semibold text-red-600">{t("org.error.load")} <button type="button" onClick={load} className="underline">{t("org.retry")}</button></div>}
        {!error && !chart && <div className="text-sm text-mute">{t("org.loading")}</div>}
        {chart && tab === "chart" && <OrgChartTree chart={chart} onDept={setDept} onAgent={setAgent} onPerson={setPerson} onNewDept={() => setDept(null)} />}
        {chart && tab === "departments" && (
          <div data-testid="org-dept-table">
            {flat.length === 0 && <p className="text-[13px] text-mute">{t("org.empty.body")}</p>}
            <ul className="grid gap-1.5">
              {flat.map((f) => {
                const d = byId.get(f.id)!;
                return (
                  <li key={f.id} style={{ marginLeft: f.depth * 18 }}>
                    <button type="button" data-testid={`org-dept-row-${f.id}`} onClick={() => setDept(f.id)}
                      className="flex w-full flex-wrap items-center justify-between gap-2 rounded-xl border border-line bg-panel px-3 py-2 text-left hover:border-accent/50">
                      <span className="text-[13px] font-semibold">{d.name}</span>
                      <span className="flex flex-wrap gap-x-3 text-[11px] text-mute">
                        {d.cost_center && <span>{t("org.costCenter")}: {d.cost_center}</span>}
                        <span>{t("org.headOf", { name: d.head_name || t("org.noHead") })}</span>
                        <span>{t("org.counts", { people: d.people, agents: d.agents })}</span>
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          </div>
        )}
        {chart && tab === "roles" && (
          <ul className="grid gap-2" data-testid="org-roles">
            {agents.length === 0 && <li className="text-[13px] text-mute">{t("org.roles.empty")}</li>}
            {agents.map((a) => (
              <li key={a.id}>
                <button type="button" data-testid={`org-role-row-${a.id}`} onClick={() => setAgent(a.id)}
                  className="flex w-full flex-wrap items-center justify-between gap-2 rounded-xl border border-line bg-panel px-3 py-2 text-left hover:border-accent/50">
                  <span><span className="text-[13px] font-semibold">{a.name}</span> <span className="text-[11px] text-mute">{a.title}</span></span>
                  <span className="flex flex-wrap items-center gap-2 text-[11px] text-mute">
                    <span>{a.dept_name || t("org.unassigned")}</span>
                    {a.reports_to_name && <span>{t("org.reportsToName", { name: a.reports_to_name })}</span>}
                    {a.open === undefined ? <span>{t("org.hours.always")}</span>
                      : a.open ? <span className="font-semibold text-emerald-700">{t("org.hours.open")}</span>
                        : <span className="font-semibold text-amber-700">{a.next_open ? t("org.hours.closedUntil", { when: fmtDateTime(a.next_open) }) : t("org.hours.closed")}</span>}
                    {a.budget_usd != null && <span>{t("org.budget.perMonth", { amount: fmtUsd(a.budget_usd) })}</span>}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
        {chart && tab === "delegation" && <DelegationPanel chart={chart} />}
      </div>
      {chart && dept !== undefined && <DepartmentEditor deptId={dept} chart={chart} onClose={() => setDept(undefined)} onSaved={saved} />}
      {chart && agent && <AgentRoleEditor agentId={agent} chart={chart} onClose={() => setAgent(null)} onSaved={saved} />}
      {chart && person && <PersonEditor userId={person} chart={chart} onClose={() => setPerson(null)} onSaved={saved} />}
    </div>
  );
}
