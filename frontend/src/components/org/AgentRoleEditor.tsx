"use client";
import { useEffect, useMemo, useState } from "react";
import { api } from "@/lib/api";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { browserTimeZone } from "@/lib/orgConfigApi";
import { orgChartApi, type Chart, type Job } from "@/lib/orgChartApi";
import {
  ALL_DAYS, bossCandidates, defaultSchedule, isOpenAt, narrowTools, parseBudget, refKey, toggleDay, toggleTool, validateSchedule, wallClock,
  type Ref, type ScheduleLite,
} from "@/lib/orgChartLogic";
import { Modal } from "../templates/Modal";
import { COMMON_ZONES, Field, RefSelect, allDepts, allMembers, btnGhost, btnPrimary, inputCls, memberOptions, refOf } from "./shared";

const DAY_KEYS = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"];

/** Job description of an agent: department, reporting line, escalation, responsibilities, tools, schedule and budget. */
export function AgentRoleEditor({ agentId, chart, onClose, onSaved }: { agentId: string; chart: Chart; onClose: () => void; onSaved: () => void }) {
  const { t, locale } = useT();
  const agent = useStore((s) => s.agents[agentId]);
  const [job, setJob] = useState<Job | null>(null);
  const [roleTools, setRoleTools] = useState<string[] | null>(null);
  const [loadError, setLoadError] = useState(false);
  const [dept, setDept] = useState("");
  const [boss, setBoss] = useState<Ref>({ kind: "", id: "" });
  const [esc, setEsc] = useState<Ref>({ kind: "", id: "" });
  const [resp, setResp] = useState("");
  const [tools, setTools] = useState<string[]>([]);
  const [sched, setSched] = useState<ScheduleLite>(defaultSchedule(browserTimeZone()));
  const [budget, setBudget] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    orgChartApi.job(agentId).then((j) => {
      if (!alive) return;
      setJob(j); setDept(j.department_id); setBoss(j.reports_to); setEsc(j.escalation); setResp(j.responsibilities.join("\n")); setTools(j.allowed_tools);
      setSched(j.schedule.timezone ? j.schedule : defaultSchedule(browserTimeZone())); setBudget(j.budget_usd == null ? "" : String(j.budget_usd));
    }).catch(() => { if (alive) setLoadError(true); });
    return () => { alive = false; };
  }, [agentId]);
  useEffect(() => {
    if (!agent) return;
    let alive = true;
    api.roleTemplates(locale).then((r) => { if (alive) setRoleTools(r.find((x) => x.id === agent.role)?.tools ?? null); }).catch(() => { if (alive) setRoleTools(null); });
    return () => { alive = false; };
  }, [agent, locale]);

  const members = useMemo(() => allMembers(chart), [chart]);
  const self: Ref = { kind: "agent", id: agentId };
  // a job description can only narrow the tools of the role; without a template the agent's current tools are the ceiling
  const ceiling = useMemo(() => Array.from(new Set([...(roleTools ?? agent?.tools ?? []), ...(roleTools ? [] : job?.allowed_tools ?? [])])), [roleTools, agent, job]);
  const edges = useMemo(() => {
    const e: Record<string, string> = {};
    for (const m of members) if (m.reports_to && m.reports_to.kind) e[refKey(refOf(m))] = refKey(m.reports_to);
    return e;
  }, [members]);
  const allowedBosses = useMemo(() => new Set(bossCandidates(edges, refKey(self), members.map((m) => refKey(refOf(m))))), [edges, members, agentId]); // eslint-disable-line react-hooks/exhaustive-deps
  const bossOptions = memberOptions(chart, t, self).filter((o) => allowedBosses.has(refKey(o.ref)));
  const escOptions = memberOptions(chart, t, self);
  const bud = parseBudget(budget);
  const issue = validateSchedule(sched);
  const nowOpen = isOpenAt(sched, new Date());
  const local = sched.enabled && !issue ? wallClock(new Date(), sched.timezone) : null;
  const zones = Array.from(new Set([browserTimeZone(), sched.timezone, ...COMMON_ZONES].filter(Boolean)));

  const save = async () => {
    if (issue || !bud.ok) return;
    setBusy(true); setError(null);
    try {
      await orgChartApi.putJob(agentId, {
        department_id: dept, reports_to: boss, escalation: esc, responsibilities: resp.split("\n").map((l) => l.trim()).filter(Boolean),
        allowed_tools: narrowTools(ceiling, tools), schedule: sched, budget_usd: bud.value,
      });
      onSaved();
    } catch { setError(t("org.error.save")); } finally { setBusy(false); }
  };

  return (
    <Modal title={t("org.role.title", { name: agent?.name ?? agentId })} onClose={onClose} testid="org-role-editor" wide>
      {loadError && <div role="alert" className="text-sm font-semibold text-red-600">{t("org.error.load")}</div>}
      {!loadError && !job && <div className="text-sm text-mute">{t("org.loading")}</div>}
      {job && (
        <div className="grid gap-5">
          {agent && <p className="text-[12px] text-mute">{agent.title} · {t("org.role.roleOf", { role: agent.role })}</p>}
          <div className="grid gap-4 sm:grid-cols-3">
            <label className="flex flex-col gap-1 text-[12px] font-semibold">
              {t("org.role.department")}
              <select data-testid="org-role-dept" value={dept} onChange={(e) => setDept(e.target.value)} className={inputCls}>
                <option value="">{t("org.unassigned")}</option>
                {allDepts(chart).map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
              </select>
            </label>
            <RefSelect label={t("org.role.reportsTo")} value={boss} onChange={setBoss} options={bossOptions} noneLabel={t("org.none")} testid="org-role-boss" />
            <RefSelect label={t("org.role.escalation")} value={esc} onChange={setEsc} options={escOptions} noneLabel={t("org.none")} testid="org-role-escalation" />
          </div>

          <Field label={t("org.role.responsibilities")} hint={t("org.role.responsibilitiesHint")}>
            <textarea data-testid="org-role-resp" value={resp} onChange={(e) => setResp(e.target.value)} rows={5} className={inputCls} />
          </Field>

          <fieldset>
            <legend className="mb-1 text-[12px] font-semibold">{t("org.role.tools")}</legend>
            <p className="mb-2 text-[11px] text-mute">{t("org.role.toolsHint")}</p>
            <div className="flex flex-wrap gap-2">
              {ceiling.length === 0 && <span className="text-[12px] text-mute">{t("org.role.noTools")}</span>}
              {ceiling.map((tool) => (
                <label key={tool} className="flex items-center gap-1.5 rounded-md border border-line px-2.5 py-1 text-[12px] font-semibold">
                  <input type="checkbox" data-testid={`org-role-tool-${tool}`} checked={tools.includes(tool)} onChange={() => setTools(toggleTool(tools, tool))} />
                  {tool}
                </label>
              ))}
            </div>
          </fieldset>

          <fieldset className="rounded-xl border border-line p-3" data-testid="org-role-schedule">
            <legend className="px-1 text-[12px] font-semibold">{t("org.schedule.title")}</legend>
            <label className="mb-2 flex items-center gap-2 text-sm font-semibold">
              <input type="checkbox" data-testid="org-sched-enabled" checked={sched.enabled} onChange={(e) => setSched({ ...sched, enabled: e.target.checked })} />
              {t("org.schedule.enable")}
            </label>
            <p className="mb-3 text-[11px] text-mute">{t("org.schedule.queueNote")}</p>
            {sched.enabled && (
              <div className="grid gap-3">
                <label className="flex flex-col gap-1 text-[12px] font-semibold sm:max-w-sm">
                  {t("org.schedule.timezone")}
                  <select data-testid="org-sched-tz" value={sched.timezone} onChange={(e) => setSched({ ...sched, timezone: e.target.value })} className={inputCls}>
                    {zones.map((z) => <option key={z} value={z}>{z}</option>)}
                  </select>
                </label>
                <div role="group" aria-label={t("org.schedule.days")} className="flex flex-wrap gap-1.5">
                  {ALL_DAYS.map((d) => (
                    <button key={d} type="button" aria-pressed={sched.days.includes(d)} data-testid={`org-sched-day-${d}`} onClick={() => setSched({ ...sched, days: toggleDay(sched.days, d) })}
                      className={`rounded-md border px-3 py-1 text-[11px] font-semibold ${sched.days.includes(d) ? "border-accent/30 bg-accent-soft text-accent" : "border-line text-mute hover:text-ink"}`}>{t(`org.day.${DAY_KEYS[d]}`)}</button>
                  ))}
                </div>
                <div className="flex flex-wrap items-end gap-3">
                  <label className="flex flex-col gap-1 text-[12px] font-semibold">{t("org.schedule.start")}
                    <input type="time" data-testid="org-sched-start" value={sched.start} onChange={(e) => setSched({ ...sched, start: e.target.value })} className={inputCls} /></label>
                  <label className="flex flex-col gap-1 text-[12px] font-semibold">{t("org.schedule.end")}
                    <input type="time" data-testid="org-sched-end" value={sched.end === "24:00" ? "23:59" : sched.end} onChange={(e) => setSched({ ...sched, end: e.target.value })} className={inputCls} /></label>
                  <button type="button" data-testid="org-sched-24h" onClick={() => setSched({ ...sched, start: "00:00", end: "24:00", days: [...ALL_DAYS] })} className={btnGhost}>{t("org.schedule.allDay")}</button>
                </div>
                {issue ? (
                  <div role="alert" data-testid="org-sched-issue" className="text-[12px] font-semibold text-red-600">{t(`org.schedule.issue.${issue}`)}</div>
                ) : (
                  <div data-testid="org-sched-preview" className="text-[12px] text-mute">
                    <span className={`mr-2 rounded px-1.5 py-[1px] text-[10px] font-semibold ${nowOpen ? "bg-emerald-500/15 text-emerald-700" : "bg-amber-500/15 text-amber-700"}`}>{nowOpen ? t("org.hours.open") : t("org.hours.closed")}</span>
                    {local && t("org.schedule.localNow", { zone: sched.timezone, time: `${String(Math.floor(local.minutes / 60)).padStart(2, "0")}:${String(local.minutes % 60).padStart(2, "0")}` })}
                  </div>
                )}
              </div>
            )}
          </fieldset>

          <label className="flex flex-col gap-1 text-[12px] font-semibold sm:max-w-xs">
            {t("org.role.budget")}
            <input data-testid="org-role-budget" inputMode="decimal" value={budget} onChange={(e) => setBudget(e.target.value)} placeholder={t("org.role.budgetDefault")} className={inputCls} />
            <span className="text-[11px] font-normal text-mute">{t("org.role.budgetHint")}</span>
            {!bud.ok && <span role="alert" className="text-[11px] text-red-600">{t("org.role.budgetInvalid")}</span>}
          </label>

          {error && <div role="alert" data-testid="org-role-error" className="text-[12px] font-semibold text-red-600">{error}</div>}
          <div className="flex justify-end gap-2">
            <button type="button" onClick={onClose} className={btnGhost}>{t("org.cancel")}</button>
            <button type="button" data-testid="org-role-save" disabled={busy || !!issue || !bud.ok} onClick={() => void save()} className={btnPrimary}>{busy ? t("org.saving") : t("org.save")}</button>
          </div>
        </div>
      )}
    </Modal>
  );
}
