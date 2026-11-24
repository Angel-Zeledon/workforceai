"use client";
import Link from "next/link";
import { Suspense, useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useT } from "@/lib/i18n";
import { officesApi } from "@/lib/officesApi";
import { orgChartApi } from "@/lib/orgChartApi";
import { skillsApi, type Skill } from "@/lib/skills";
import { skillTitle } from "@/lib/skillTitles";
import { officeBadge, type DirectoryEntry, type OfficesResponse, type PersonRef } from "@/lib/officesLogic";
import { baseOffice, officesSummary } from "@/lib/agentOfficesLogic";
import { OFFICE_PAGE_TABS, officeAgents, resolveOfficeTab, skillsOfOffice, type OfficePageTab } from "@/lib/officePageLogic";
import { useAgentOffices } from "@/lib/useAgentOffices";
import { Avatar } from "../chat/ChatThread";
import { HireWizard } from "../agents/HireWizard";
import { OfficesPanel } from "../org/OfficesPanel";
import { TeamMemory } from "../org/TeamMemory";
import { PositionsScreen } from "../positions/PositionsScreen";
import { btnGhost, btnPrimary } from "../org/shared";

/** `/offices/<id>`: one office with its People, Agents, Positions, Skills, Files and Memory. Each tab reuses the existing screen. */
export function OfficePage({ id }: { id: string }) {
  return <Suspense fallback={null}><Page id={id} /></Suspense>;
}

function Page({ id }: { id: string }) {
  const { t } = useT();
  const router = useRouter();
  const search = useSearchParams();
  const [tab, setTab] = useState<OfficePageTab>(resolveOfficeTab(search.get("tab")));
  const [data, setData] = useState<OfficesResponse | null>(null);
  const [error, setError] = useState(false);
  const [hire, setHire] = useState(false);
  const [people, setPeople] = useState<PersonRef[]>([]);
  const list = useRef<HTMLDivElement>(null);

  const load = useCallback(() => { setError(false); officesApi.list().then(setData).catch(() => setError(true)); }, []);
  useEffect(load, [load]);
  useEffect(() => {
    orgChartApi.chart().then((c) => {
      const out: PersonRef[] = [];
      const walk = (ds: typeof c.roots) => ds.forEach((d) => { d.members.forEach((m) => m.kind === "person" && out.push({ user_id: m.id, name: m.name })); walk(d.children); });
      walk(c.roots); c.unassigned.forEach((m) => m.kind === "person" && out.push({ user_id: m.id, name: m.name }));
      setPeople(out);
    }).catch(() => setPeople([]));
  }, []);

  const pick = (k: OfficePageTab, focus = false) => {
    setTab(k);
    router.replace(`/offices/${encodeURIComponent(id)}?tab=${k}`, { scroll: false });
    if (focus) requestAnimationFrame(() => list.current?.querySelector<HTMLElement>(`[data-tab="${k}"]`)?.focus());
  };
  const onKey = (e: KeyboardEvent) => {
    const n = OFFICE_PAGE_TABS.length, i = OFFICE_PAGE_TABS.indexOf(tab);
    const to = e.key === "ArrowRight" ? (i + 1) % n : e.key === "ArrowLeft" ? (i + n - 1) % n : e.key === "Home" ? 0 : e.key === "End" ? n - 1 : -1;
    if (to >= 0) { e.preventDefault(); pick(OFFICE_PAGE_TABS[to], true); }
  };

  const o = data?.offices.find((x) => x.id === id);
  const name = o ? (o.general ? t("offices.general") : o.name) : id;
  const canHire = !!o && o.can_enter && (data?.me.admin || o.can_manage);

  return (
    <div className="flex h-full flex-col overflow-y-auto" data-testid="office-page">
      <div className="border-b border-line bg-panel px-4 pt-3 sm:px-6">
        <Link href="/offices" className="text-[12px] font-semibold text-accent hover:underline" data-testid="office-page-back">{t("ofp.back")}</Link>
        <div className="mt-2 flex flex-wrap items-end justify-between gap-3">
          <div className="min-w-0">
            <h1 className="truncate font-display text-[18px] font-semibold leading-tight text-ink sm:text-xl" data-testid="office-page-name">{name}</h1>
            {o && <p className="text-[12.5px] text-mute">{t(`offices.badge.${officeBadge(o)}`)} · {t("offices.leads", { names: o.leads.map((l) => l.name || "?").join(", ") || t("org.noHead") })}</p>}
          </div>
          {canHire && <button type="button" data-testid="office-hire" onClick={() => setHire(true)} className={btnPrimary}>{t("hire.open")}</button>}
        </div>
        <div ref={list} role="tablist" aria-label={t("ofp.tabs")} onKeyDown={onKey} className="wf-hscroll mt-3 flex gap-1 overflow-x-auto">
          {OFFICE_PAGE_TABS.map((k) => (
            <button key={k} type="button" role="tab" id={`office-tab-${k}`} data-tab={k} data-testid={`office-tab-${k}`} aria-selected={tab === k} aria-controls={`office-panel-${k}`} tabIndex={tab === k ? 0 : -1}
              onClick={() => pick(k)} className={`min-h-10 shrink-0 whitespace-nowrap border-b-2 px-3 py-2 text-[13px] font-semibold ${tab === k ? "border-accent text-accent" : "border-transparent text-mute hover:text-ink"}`}>{t(`ofp.tab.${k}`)}</button>
          ))}
        </div>
      </div>
      <div role="tabpanel" id={`office-panel-${tab}`} aria-labelledby={`office-tab-${tab}`} className="mx-auto w-full max-w-5xl flex-1 px-4 py-4 sm:px-6 sm:py-5">
        {error && <div role="alert" className="text-sm font-semibold text-red-600">{t("offices.error.load")} <button type="button" onClick={load} className="underline">{t("org.retry")}</button></div>}
        {!error && !data && <div className="text-sm text-mute">{t("org.loading")}</div>}
        {data && !o && <p className="text-[13px] text-mute" data-testid="office-missing">{t("ofp.missing")}</p>}
        {data && o && !o.can_enter && <p className="text-[13px] text-mute" data-testid="office-closed">{t("offices.closedDoor")}</p>}
        {data && o && o.can_enter && (
          <>
            {tab === "people" && <OfficesPanel officeId={id} people={people} onChanged={load} />}
            {tab === "agents" && <OfficeAgents id={id} canHire={!!canHire} onHire={() => setHire(true)} />}
            {tab === "positions" && (o.general ? <p className="text-[13px] text-mute" data-testid="office-no-positions">{t("ofp.positions.general")}</p> : <PositionsScreen officeId={id} />)}
            {tab === "skills" && <OfficeSkills id={id} />}
            {tab === "files" && (
              <div className="grid gap-2" data-testid="office-files">
                <p className="text-[13px] text-mute">{t("ofp.files.hint")}</p>
                <div><Link href={`/d/${encodeURIComponent(id)}`} prefetch={false} data-testid="office-files-open" className={btnGhost}>{t("ofp.files.open")}</Link></div>
              </div>
            )}
            {tab === "memory" && (
              <div className="grid gap-2" data-testid="office-memory">
                <p className="text-[13px] text-mute">{t("ofp.memory.hint")}</p>
                <TeamMemory scope="office" refId={id} />
              </div>
            )}
          </>
        )}
      </div>
      {hire && <HireWizard officeId={id} onClose={() => { setHire(false); load(); }} />}
    </div>
  );
}

function OfficeAgents({ id, canHire, onHire }: { id: string; canHire: boolean; onHire: () => void }) {
  const { t } = useT();
  const [entries, setEntries] = useState<DirectoryEntry[] | null>(null);
  const [error, setError] = useState(false);
  const general = t("offices.general");
  useEffect(() => { officesApi.directory().then((d) => setEntries(d.agents)).catch(() => setError(true)); }, []);
  const ids = useMemo(() => (entries ?? []).map((e) => e.id), [entries]);
  const multi = useAgentOffices(ids);
  const alsoIn = useMemo(() => {
    const out: Record<string, { id: string; pending: boolean }[]> = {};
    for (const e of entries ?? []) out[e.id] = officesSummary(baseOffice(e), multi.views[e.id], general).others;
    return out;
  }, [entries, multi.views, general]);

  if (error) return <p role="alert" className="text-sm font-semibold text-red-600">{t("offices.error.load")}</p>;
  if (!entries) return <div className="text-sm text-mute">{t("org.loading")}</div>;
  const { base, visiting } = officeAgents(entries, id, (e) => baseOffice(e).id, alsoIn);
  const row = (e: DirectoryEntry, note?: string) => (
    <li key={e.id} data-testid={`office-agent-${e.id}`} className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-line/70 py-2.5">
      <Link href={`/agents/${encodeURIComponent(e.id)}`} prefetch={false} className="flex min-w-0 flex-1 basis-56 items-center gap-2.5 hover:text-accent">
        <Avatar id={e.id} size={28} />
        <span className="min-w-0"><span className="block truncate text-[13.5px] font-semibold">{e.name}</span><span className="block truncate text-[12px] text-mute">{e.title}</span></span>
      </Link>
      {note && <span className="text-[12px] text-mute">{note}</span>}
      {e.rights.use && <Link href={`/chats/${encodeURIComponent(e.id)}`} prefetch={false} className="text-[12px] font-semibold text-accent hover:underline">{t("ag.row.talk")}</Link>}
    </li>
  );
  return (
    <div className="grid gap-4" data-testid="office-agents">
      {canHire && <div><button type="button" onClick={onHire} data-testid="office-agents-hire" className={btnGhost}>{t("hire.open")}</button></div>}
      {base.length === 0 && visiting.length === 0 && <p className="text-[13px] text-mute">{t("ofp.agents.none")}</p>}
      {base.length > 0 && <ul>{base.map((e) => row(e))}</ul>}
      {visiting.length > 0 && (
        <section aria-labelledby="ofp-visiting">
          <h3 id="ofp-visiting" className="mb-1 border-b border-line pb-1 text-[13px] font-semibold">{t("ofp.agents.visiting")}</h3>
          <ul>{visiting.map((v) => row(v.entry, t("ofp.agents.from", { office: officesSummary(baseOffice(v.entry), null, general).baseLabel }) + (v.pending ? ` · ${t("ag.where.pending")}` : "")))}</ul>
        </section>
      )}
    </div>
  );
}

function OfficeSkills({ id }: { id: string }) {
  const { t, locale } = useT();
  const [skills, setSkills] = useState<Skill[] | null>(null);
  const [error, setError] = useState(false);
  useEffect(() => { skillsApi.list().then(setSkills).catch(() => setError(true)); }, []);
  if (error) return <p role="alert" className="text-sm font-semibold text-red-600">{t("skills.error.load")}</p>;
  if (!skills) return <div className="text-sm text-mute">{t("skills.loading")}</div>;
  const { own, company } = skillsOfOffice(skills, id);
  const block = (title: string, items: Skill[], empty: string, testid: string) => (
    <section aria-label={title} data-testid={testid}>
      <h3 className="mb-1 border-b border-line pb-1 text-[13px] font-semibold">{title} <span className="font-normal text-mute">{items.length}</span></h3>
      {items.length === 0 ? <p className="py-1 text-[13px] text-mute">{empty}</p> : (
        <ul>{items.map((s) => (
          <li key={s.id} className="border-b border-line/70 py-2"><p className="text-[13px] font-semibold">{skillTitle(s.name, locale)}</p><p className="text-[12px] text-mute">{s.description}</p></li>
        ))}</ul>
      )}
    </section>
  );
  return (
    <div className="grid gap-5" data-testid="office-skills">
      {block(t("ofp.skills.own"), own, t("ofp.skills.ownNone"), "office-skills-own")}
      {block(t("ofp.skills.company"), company, t("ofp.skills.companyNone"), "office-skills-company")}
      <div><Link href="/skills" className={btnGhost}>{t("ofp.skills.library")}</Link></div>
    </div>
  );
}
