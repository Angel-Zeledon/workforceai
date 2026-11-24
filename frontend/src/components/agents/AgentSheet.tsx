"use client";
import Link from "next/link";
import { Suspense, useCallback, useEffect, useRef, useState, type KeyboardEvent } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { stateMeta } from "@/lib/meta";
import { officesApi } from "@/lib/officesApi";
import { orgChartApi } from "@/lib/orgChartApi";
import { agentOfficesApi } from "@/lib/agentOfficesApi";
import type { DirectoryEntry, OfficesResponse, PersonRef } from "@/lib/officesLogic";
import { askers, baseOffice, invitableOffices, officesSummary, type AgentOfficesView } from "@/lib/agentOfficesLogic";
import { SHEET_TABS, resolveSheetTab, type SheetTab } from "@/lib/agentSheetLogic";
import { Avatar } from "../chat/ChatThread";
import { ProfileSheet } from "../agent/ProfileSheet";
import { PositionSection } from "../agent/PositionSection";
import { PermissionsSection } from "../agent/PermissionsSection";
import { FolderAccessSection } from "../agent/FolderAccessSection";
import { AgentSkillsCard } from "../skills/AgentSkillsCard";
import { AgentAccessDialog } from "../org/AgentAccessDialog";
import { btnGhost, btnPrimary, inputCls } from "../org/shared";

/** The agent's sheet (`/agents/<id>`): four tabs that do not mix. Quién es (the profile), Dónde y para quién trabaja (base
 *  office, the others it also works in, who may ask, its position), Qué sabe hacer (skills and where each comes from) and
 *  Permisos. Each tab reuses the existing section; nothing is duplicated here. */
/** "su oficina" / "toda la empresa" start with a capital when they stand alone in the ficha (not after a "+ ..." list). */
function askText(text: string, kind: string, hasExtra: boolean): string {
  if (hasExtra || (kind !== "office" && kind !== "company")) return text;
  return text.charAt(0).toLocaleUpperCase() + text.slice(1);
}

export function AgentSheet({ id }: { id: string }) {
  return <Suspense fallback={null}><Sheet id={id} /></Suspense>;
}

function Sheet({ id }: { id: string }) {
  const { t } = useT();
  const router = useRouter();
  const search = useSearchParams();
  const agent = useStore((s) => s.agents[id]);
  const [tab, setTab] = useState<SheetTab>(resolveSheetTab(search.get("tab")));
  const list = useRef<HTMLDivElement>(null);
  const pick = (k: SheetTab, focus = false) => {
    setTab(k);
    router.replace(`/agents/${encodeURIComponent(id)}?tab=${k}`, { scroll: false });
    if (focus) requestAnimationFrame(() => list.current?.querySelector<HTMLElement>(`[data-tab="${k}"]`)?.focus());
  };
  const onKey = (e: KeyboardEvent) => {
    const i = SHEET_TABS.indexOf(tab);
    const to = e.key === "ArrowRight" ? (i + 1) % SHEET_TABS.length : e.key === "ArrowLeft" ? (i + SHEET_TABS.length - 1) % SHEET_TABS.length : e.key === "Home" ? 0 : e.key === "End" ? SHEET_TABS.length - 1 : -1;
    if (to >= 0) { e.preventDefault(); pick(SHEET_TABS[to], true); }
  };
  const meta = agent?.state ? stateMeta(agent.state) : null;

  return (
    <div className="flex h-full flex-col overflow-y-auto" data-testid="agent-sheet">
      <div className="border-b border-line bg-panel px-4 pt-3 sm:px-6">
        <Link href="/agents" className="text-[12px] font-semibold text-accent hover:underline" data-testid="agent-sheet-back">{t("ag.sheet.back")}</Link>
        <div className="mt-2 flex flex-wrap items-center justify-between gap-3">
          <div className="flex min-w-0 items-center gap-3">
            <Avatar id={id} size={40} />
            <div className="min-w-0">
              <h1 className="truncate font-display text-[18px] font-semibold leading-tight text-ink sm:text-xl" data-testid="agent-sheet-name">{agent?.name ?? id}</h1>
              <p className="flex flex-wrap items-center gap-x-2 text-[12.5px] text-mute">
                <span>{agent?.title}</span>
                {meta && <span className="inline-flex items-center gap-1.5 text-ink2"><span aria-hidden className="h-2 w-2 rounded-full" style={{ background: meta.color }} />{meta.label}</span>}
              </p>
            </div>
          </div>
          <Link href={`/chats/${encodeURIComponent(id)}`} prefetch={false} data-testid="agent-sheet-talk" className={btnPrimary}>{t("ag.row.talk")}</Link>
        </div>
        <div ref={list} role="tablist" aria-label={t("ag.sheet.tabs")} onKeyDown={onKey} className="wf-hscroll mt-3 flex gap-1 overflow-x-auto">
          {SHEET_TABS.map((k) => (
            <button key={k} type="button" role="tab" id={`sheet-tab-${k}`} data-tab={k} data-testid={`sheet-tab-${k}`} aria-selected={tab === k} aria-controls={`sheet-panel-${k}`} tabIndex={tab === k ? 0 : -1}
              onClick={() => pick(k)}
              className={`min-h-10 shrink-0 whitespace-nowrap border-b-2 px-3 py-2 text-[13px] font-semibold ${tab === k ? "border-accent text-accent" : "border-transparent text-mute hover:text-ink"}`}>{t(`ag.sheet.tab.${k}`)}</button>
          ))}
        </div>
      </div>
      <div role="tabpanel" id={`sheet-panel-${tab}`} aria-labelledby={`sheet-tab-${tab}`} className="mx-auto w-full max-w-4xl flex-1 px-4 py-4 sm:px-6 sm:py-5">
        {tab === "who" && (
          <div className="grid gap-4" data-testid="sheet-who">
            {agent?.description && <p className="text-[13px] leading-snug text-ink2">{agent.description}</p>}
            <ProfileSheet id={id} part="who" />
          </div>
        )}
        {tab === "where" && <WhereTab id={id} />}
        {tab === "skills" && <div className="grid gap-4" data-testid="sheet-skills"><ProfileSheet id={id} part="tags" /><AgentSkillsCard id={id} /></div>}
        {tab === "perms" && (
          <div className="grid gap-4" data-testid="sheet-perms">
            <PermissionsSection id={id} />
            <FolderAccessSection id={id} />
            <PositionSection id={id} parts={["folders"]} />
          </div>
        )}
      </div>
    </div>
  );
}

/** Where the agent works and for whom: base office, the other offices it also works in (only when the server allows it),
 *  who may ask it, and its position. */
function WhereTab({ id }: { id: string }) {
  const { t } = useT();
  const [entry, setEntry] = useState<DirectoryEntry | null>(null);
  const [offices, setOffices] = useState<OfficesResponse | null>(null);
  const [people, setPeople] = useState<PersonRef[]>([]);
  const [view, setView] = useState<AgentOfficesView | null>(null);
  const [invite, setInvite] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [loadFailed, setLoadFailed] = useState(false);
  const [access, setAccess] = useState(false);
  const general = t("offices.general");

  const load = useCallback(() => {
    setLoadFailed(false);
    Promise.all([officesApi.directory(), officesApi.list()]).then(([d, o]) => { setEntry(d.agents.find((a) => a.id === id) ?? null); setOffices(o); }).catch(() => setLoadFailed(true));
    agentOfficesApi.get(id).then(setView).catch(() => setView(null));
  }, [id]);
  useEffect(() => { load(); }, [load]);
  useEffect(() => {
    orgChartApi.chart().then((c) => {
      const out: PersonRef[] = [];
      const walk = (ds: typeof c.roots) => ds.forEach((d) => { d.members.forEach((m) => m.kind === "person" && out.push({ user_id: m.id, name: m.name })); walk(d.children); });
      walk(c.roots); c.unassigned.forEach((m) => m.kind === "person" && out.push({ user_id: m.id, name: m.name }));
      setPeople(out);
    }).catch(() => setPeople([]));
  }, []);

  const act = async (fn: () => Promise<AgentOfficesView>) => {
    setBusy(true); setError("");
    try { setView(await fn()); setInvite(""); }
    catch (e) { setError(t(String(e).includes("403") ? "ag.where.err.forbidden" : "ag.where.err.generic")); }
    finally { setBusy(false); }
  };

  if (loadFailed) return <div role="alert" className="text-sm font-semibold text-red-600">{t("offices.error.load")} <button type="button" onClick={load} className="underline">{t("org.retry")}</button></div>;
  if (!entry || !offices) return <div className="text-sm text-mute">{t("org.loading")}</div>;

  const base = baseOffice(entry);
  const sum = officesSummary(base, view, general);
  const who = askers(entry, view);
  const invitable = sum.enabled ? invitableOffices(offices.offices, base, view) : [];
  const baseView = offices.offices.find((o) => o.id === base.id);

  return (
    <div className="grid gap-6" data-testid="sheet-where">
      <section aria-labelledby="where-base" className="border-b border-line pb-4">
        <h2 id="where-base" className="text-[14px] font-semibold">{t("ag.where.base")}</h2>
        <p className="mt-1 text-[13px]">
          <Link href={`/offices/${encodeURIComponent(base.id)}`} data-testid="where-base-office" className="font-semibold text-accent hover:underline">{sum.baseLabel}</Link>
          <span className="ml-2 text-[12px] text-mute">{t("ag.where.base.hint")}</span>
        </p>
        {baseView && !baseView.can_enter && <p className="text-[12px] text-mute">{t("offices.closedDoor")}</p>}
      </section>

      {sum.enabled && (
        <section aria-labelledby="where-also" className="border-b border-line pb-4" data-testid="where-also">
          <h2 id="where-also" className="text-[14px] font-semibold">{t("ag.where.also")}</h2>
          <p className="text-[12px] text-mute">{t("ag.where.also.hint")}</p>
          {sum.others.length === 0 ? <p className="mt-2 text-[13px] text-mute">{t("ag.where.also.none")}</p> : (
            <ul className="mt-2">
              {sum.others.map((o) => (
                <li key={o.id} data-testid={`where-other-${o.id}`} className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-line/70 py-2">
                  <span className="min-w-0 flex-1 text-[13px] font-semibold">{o.label}</span>
                  <span className={`text-[12px] ${o.pending ? "font-semibold text-amber-700" : "text-mute"}`}>{t(o.pending ? "ag.where.pending" : "ag.where.accepted")}</span>
                  {o.pending && o.canAccept && <button type="button" disabled={busy} data-testid={`where-accept-${o.id}`} onClick={() => void act(() => agentOfficesApi.accept(id, o.id))} className="min-h-9 text-[12px] font-semibold text-accent hover:underline sm:min-h-0">{t("ag.where.accept")}</button>}
                  {o.canRemove && <button type="button" disabled={busy} data-testid={`where-remove-${o.id}`} onClick={() => void act(() => agentOfficesApi.remove(id, o.id))} className="min-h-9 text-[12px] font-semibold text-red-600 hover:underline sm:min-h-0">{t(o.pending ? "ag.where.reject" : "ag.where.remove")}</button>}
                </li>
              ))}
            </ul>
          )}
          {error && <p role="alert" className="mt-2 text-[12px] font-semibold text-red-600">{error}</p>}
          {invitable.length > 0 && (
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <select data-testid="where-invite-select" aria-label={t("ag.where.invite")} value={invite} onChange={(e) => setInvite(e.target.value)} className={`${inputCls} w-auto max-w-full`}>
                <option value="">{t("ag.where.invite")}</option>
                {invitable.map((o) => <option key={o.id} value={o.id}>{o.general ? general : o.name}</option>)}
              </select>
              <button type="button" disabled={!invite || busy} data-testid="where-invite-go" onClick={() => void act(() => agentOfficesApi.grant(id, invite))} className={btnGhost}>{t("ag.where.invite.go")}</button>
            </div>
          )}
        </section>
      )}

      <section aria-labelledby="where-asks" className="border-b border-line pb-4" data-testid="where-asks">
        <h2 id="where-asks" className="text-[14px] font-semibold">{t("ag.where.asks")}</h2>
        <p className="mt-1 text-[13px]">{askText(t(`ag.asks.${who.kind}`, { n: who.people }), who.kind, who.extra.length > 0)}{who.extra.length > 0 && ` + ${who.extra.join(", ")}`}</p>
        {entry.departed && <p className="text-[12px] font-semibold text-amber-700">{t("offices.dir.departed")}</p>}
        {entry.rights.edit && <button type="button" data-testid="where-asks-edit" onClick={() => setAccess(true)} className={`${btnGhost} mt-2`}>{t("ag.where.asks.change")}</button>}
      </section>

      <PositionSection id={id} parts={["position"]} />
      {access && <AgentAccessDialog entry={entry} offices={offices} people={people} onClose={() => setAccess(false)} onSaved={() => { setAccess(false); load(); }} />}
    </div>
  );
}
