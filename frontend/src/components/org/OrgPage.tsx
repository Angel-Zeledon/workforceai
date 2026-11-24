"use client";
import { Suspense, useCallback, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { fmtDateTime, fmtUsd, useT } from "@/lib/i18n";
import { orgChartApi, type Chart } from "@/lib/orgChartApi";
import { AGENT_SECTIONS, OFFICE_TABS, resolveOrgTab, type AgentSection, type OrgTab } from "@/lib/orgTabsLogic";
import { PageHeader } from "../nav/ui2";
import { AgentsList } from "../agents/AgentsList";
import { HireWizard } from "../agents/HireWizard";
import { IndustryTemplates } from "../templates/IndustryTemplates";
import { Modal } from "../templates/Modal";
import { AgentRoleEditor } from "./AgentRoleEditor";
import { DelegationPanel } from "./DelegationPanel";
import { DepartmentEditor } from "./DepartmentEditor";
import { OfficesPanel } from "./OfficesPanel";
import { OrgChartTree } from "./OrgChartTree";
import { PersonEditor } from "./PersonEditor";
import { allDepts, allMembers, btnGhost, btnPrimary, type MemberAt } from "./shared";
import { baseOffice, officeLabel } from "@/lib/agentOfficesLogic";
import { useViewAll } from "@/lib/viewMode";
import { SIMPLE_ORG_TABS, splitTabs } from "@/lib/viewModeLogic";
import { MoreButton } from "../nav/ViewAll";
import { useViewer } from "../connections/shared";
import { TeamsGallery } from "../teams/TeamsGallery";

/**
 * The two screens that replaced "Agentes y oficinas" (lib/orgTabsLogic.ts). One component with a scope, so the editors,
 * the chart and the hire dialog exist once:
 *  - AgentsScreen (`/agents`): the directory and the agents' job descriptions (Puestos), with "Contratar".
 *  - OfficesScreen (`/offices`): Oficinas (with their own space, and without one yet), Organigrama and Reglas de
 *    delegación; "Empezar con un equipo listo" opens the ready-to-hire teams gallery here.
 */
export function AgentsScreen() {
  // useSearchParams needs a Suspense boundary for the static build
  return <Suspense fallback={null}><OrgScreen scope="agents" /></Suspense>;
}
export function OfficesScreen() {
  return <Suspense fallback={null}><OrgScreen scope="offices" /></Suspense>;
}

function OrgScreen({ scope }: { scope: "agents" | "offices" }) {
  const { t } = useT();
  const router = useRouter();
  const search = useSearchParams();
  const linked = resolveOrgTab(search.get("tab"));
  const officeTab: OrgTab = linked && (OFFICE_TABS as readonly string[]).includes(linked.tab) ? linked.tab : "offices";
  const [tab, setTab] = useState<OrgTab>(scope === "agents" ? "agents" : officeTab);
  const [section, setSection] = useState<AgentSection>(linked?.section ?? "directory");
  const focusParam = search.get("focus") === "departments" || linked?.focus === "departments";
  const [focusDepts, setFocusDepts] = useState(focusParam);
  const [teamsOpen, setTeamsOpen] = useState(false); // the ready-to-hire teams gallery, inside Oficinas
  const gallery = scope === "offices" && (teamsOpen || !!search.get("team") || !!search.get("pack") || !!search.get("template"));
  const [chart, setChart] = useState<Chart | null>(null);
  const [error, setError] = useState(false);
  const [dept, setDept] = useState<string | null | undefined>(undefined); // undefined = closed, null = new
  const [agent, setAgent] = useState<string | null>(null);
  const [person, setPerson] = useState<string | null>(null);
  const [hire, setHire] = useState(search.get("hire") === "1");
  const hireOffice = search.get("office") || undefined;
  const [industries, setIndustries] = useState(false);
  const viewAll = useViewAll();
  const [more, setMore] = useState(false);
  const { isAdmin } = useViewer();
  // simple mode: Oficinas is open; Organigrama and Reglas de delegación one click away (the open tab stays)
  const tabs = splitTabs(OFFICE_TABS, SIMPLE_ORG_TABS, viewAll || more, [tab]);

  // a new ?tab= while the page is open applies too
  const rawTab = search.get("tab");
  useEffect(() => {
    const place = resolveOrgTab(rawTab);
    if (!place) return;
    if (scope === "agents") { if (place.section) setSection(place.section); return; }
    if ((OFFICE_TABS as readonly string[]).includes(place.tab)) setTab(place.tab);
  }, [rawTab, scope]);
  useEffect(() => { setFocusDepts(focusParam); }, [focusParam]);

  const load = useCallback(() => {
    setError(false);
    orgChartApi.chart().then(setChart).catch(() => setError(true));
  }, []);
  useEffect(load, [load]);
  const saved = () => { setDept(undefined); setAgent(null); setPerson(null); load(); };

  const depts = allDepts(chart);
  const agents = allMembers(chart).filter((m) => m.kind === "agent");
  const people = allMembers(chart).filter((m) => m.kind === "person").map((m) => ({ user_id: m.id, name: m.name }));
  const pick = (k: OrgTab) => { setTab(k); setFocusDepts(false); };
  const closeGallery = () => { setTeamsOpen(false); router.replace("/offices"); };

  return (
    <div className="flex h-full flex-col overflow-y-auto" data-testid={scope === "agents" ? "agents-page" : "offices-page"}>
      <PageHeader title={scope === "agents" ? t("nav.agents") : t("nav.offices")} description={t(scope === "agents" ? "org.agents.subtitle" : "org.subtitle")}
        testid="org-header" concept={scope === "agents" ? "agents" : "office"}
        actions={scope === "offices" && isAdmin && !gallery ? <button type="button" data-testid="org-hire" onClick={() => setHire(true)} className={btnPrimary}>{t("hire.open")}</button> : undefined} />
      {scope === "offices" && (
        <div role="tablist" aria-label={t("nav.offices")} className="flex flex-wrap items-center gap-1.5 border-b border-line bg-panel px-4 py-2 sm:px-6 wf-tabs">
          {tabs.shown.map((k) => (
            <button key={k} type="button" role="tab" aria-selected={!gallery && tab === k} data-testid={`org-tab-${k}`} onClick={() => { setTeamsOpen(false); pick(k); }}
              className={`rounded-md border px-3 py-1 text-[12px] font-semibold ${!gallery && tab === k ? "border-accent/30 bg-accent-soft text-accent" : "border-line text-mute hover:text-ink"}`}>{t(`org.tab.${k}`)}</button>
          ))}
          {!viewAll && <MoreButton testid="org-more" count={more ? 1 : tabs.folded.length} open={more} onToggle={() => setMore((m) => !m)} />}
          <button type="button" data-testid="org-teams-start" onClick={() => setTeamsOpen(true)} className={`${btnGhost} ml-auto`}>{t("org.teams.start")}</button>
        </div>
      )}
      <div className="mx-auto w-full max-w-6xl flex-1 px-4 py-4 sm:px-6 sm:py-5">
        {error && <div role="alert" data-testid="org-error" className="text-sm font-semibold text-red-600">{t("org.error.load")} <button type="button" onClick={load} className="underline">{t("org.retry")}</button></div>}
        {!error && !chart && <div className="text-sm text-mute">{t("org.loading")}</div>}
        {gallery && (
          <div className="grid gap-4" data-testid="offices-teams">
            <div><button type="button" data-testid="org-teams-back" onClick={closeGallery} className={btnGhost}>{t("org.teams.back")}</button></div>
            <TeamsGallery team={search.get("team") || undefined} pack={search.get("pack") || undefined} template={search.get("template") || undefined} />
          </div>
        )}

        {chart && scope === "agents" && (
          <div className="grid gap-4" data-testid="org-agents">
            <div className="flex flex-wrap items-center justify-between gap-2">
              {/* the old "Directorio de agentes" and "Puestos de agentes" tabs, now one selector (same test ids) */}
              <div role="group" aria-label={t("nav.agents")} className="flex gap-1 rounded-[10px] border border-line bg-panel2 p-1 wf-hscroll">
                {AGENT_SECTIONS.map((s) => (
                  <button key={s} type="button" aria-pressed={section === s} data-testid={`org-tab-${s}`} onClick={() => setSection(s)}
                    className={`min-h-9 rounded-md px-3 py-1 text-[12px] font-semibold sm:min-h-0 ${section === s ? "bg-panel text-ink shadow-sm" : "text-mute hover:text-ink"}`}>{t(`org.tab.${s}`)}</button>
                ))}
              </div>
              {/* "Contratar agente" */}
              {isAdmin && <button type="button" data-testid="org-hire" onClick={() => setHire(true)} className={btnPrimary}>{t("hire.open")}</button>}
            </div>
            {section === "directory" && <AgentsList />}
            {section === "roles" && <AgentRoles agents={agents} onOpen={setAgent} />}
          </div>
        )}
        {chart && !gallery && scope === "offices" && tab === "offices" && (
          <OfficesPanel people={people} onChanged={load} depts={depts} onEditDept={setDept} onNewDept={() => setDept(null)} focusDepartments={focusDepts}
            actions={<button type="button" data-testid="org-industries-link" onClick={() => setIndustries(true)} className={btnGhost}>{t("org.offices.industries")}</button>} />
        )}
        {chart && !gallery && scope === "offices" && tab === "chart" && <OrgChartTree chart={chart} onDept={setDept} onAgent={setAgent} onPerson={setPerson} onNewDept={() => setDept(null)} />}
        {chart && !gallery && scope === "offices" && tab === "delegation" && <DelegationPanel chart={chart} />}
      </div>
      {chart && dept !== undefined && <DepartmentEditor deptId={dept} chart={chart} onClose={() => setDept(undefined)} onSaved={saved} />}
      {chart && agent && <AgentRoleEditor agentId={agent} chart={chart} onClose={() => setAgent(null)} onSaved={saved} />}
      {chart && person && <PersonEditor userId={person} chart={chart} onClose={() => setPerson(null)} onSaved={saved} />}
      {hire && <HireWizard officeId={hireOffice} onClose={() => { setHire(false); load(); }} />}
      {industries && (
        <Modal title={t("org.offices.industries")} onClose={() => { setIndustries(false); load(); }} testid="org-industries" wide>
          <IndustryTemplates onDone={() => { setIndustries(false); pick("offices"); load(); }} />
        </Modal>
      )}
    </div>
  );
}

/** "Puestos": each agent's job description (office, who it reports to, hours, budget); a click opens the editor. */
function AgentRoles({ agents, onOpen }: { agents: MemberAt[]; onOpen: (id: string) => void }) {
  const { t } = useT();
  return (
    <div className="grid gap-2">
      <p className="text-[13px] text-mute">{t("org.roles.intro")}</p>
      <ul className="grid gap-2" data-testid="org-roles">
        {agents.length === 0 && <li className="text-[13px] text-mute">{t("org.roles.empty")}</li>}
        {agents.map((a) => (
          <li key={a.id}>
            <button type="button" data-testid={`org-role-row-${a.id}`} onClick={() => onOpen(a.id)}
              className="flex w-full flex-wrap items-center justify-between gap-2 rounded-[10px] border border-line bg-panel px-3 py-2 text-left hover:border-accent/50">
              <span><span className="text-[13px] font-semibold">{a.name}</span> <span className="text-[11px] text-mute">{a.title}</span></span>
              <span className="flex flex-wrap items-center gap-2 text-[11px] text-mute">
                <span data-testid={`org-role-office-${a.id}`}>{officeLabel(baseOffice({ office_id: a.dept_id, office_name: a.dept_name }), t("offices.general"))}</span>
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
    </div>
  );
}
