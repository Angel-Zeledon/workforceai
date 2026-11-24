"use client";
import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { ApiError, api, type RoleTemplate } from "@/lib/api";
import { useT } from "@/lib/i18n";
import { registerRoleTemplates } from "@/lib/meta";
import { useStore } from "@/lib/store";
import { agentOfficesApi, type SuggestedSkill } from "@/lib/agentOfficesApi";
import { officesApi } from "@/lib/officesApi";
import { orgChartApi } from "@/lib/orgChartApi";
import { positionsApi, type PositionsView } from "@/lib/positions";
import { officeLabel } from "@/lib/agentOfficesLogic";
import type { OfficeView, PersonRef } from "@/lib/officesLogic";
import {
  HIRE_STEPS, NAME_MAX, emptyDraft, firstIssueStep, hireBody, hireError, isGeneralOffice, keptSkills, pickOffice, pickPosition, pickTemplate,
  stepIssue, togglePerson, toggleRemoved, visibilityChoices, type HireDraft, type HireStep, type HireVisibility,
} from "@/lib/hireLogic";
import { skillTitle } from "@/lib/skillTitles";
import { Modal } from "../templates/Modal";
import { btnGhost, btnPrimary, inputCls } from "../org/shared";

const row = "flex cursor-pointer items-start gap-2.5 rounded-[10px] border px-3 py-2.5 text-left";

/** "Contratar agente" in three steps: for which office, which position (the office's or the catalog's, with suggested skills
 *  that can be taken out), and the name with who may ask. Back never loses what was typed; a summary comes before
 *  "Contratar"; each refusal is shown in the step it belongs to. `officeId` opens it with step 1 already answered. */
export function HireWizard({ onClose, officeId }: { onClose: () => void; officeId?: string }) {
  const { t, locale } = useT();
  const [step, setStep] = useState<HireStep>(officeId ? 2 : 1);
  const [draft, setDraft] = useState<HireDraft>(emptyDraft(officeId ?? ""));
  const [offices, setOffices] = useState<OfficeView[] | null>(null);
  const [catalog, setCatalog] = useState<RoleTemplate[] | null>(null);
  const [positions, setPositions] = useState<PositionsView | null>(null);
  const [suggested, setSuggested] = useState<SuggestedSkill[] | null>(null);
  const [people, setPeople] = useState<PersonRef[]>([]);
  const [loadError, setLoadError] = useState(false);
  const [showIssue, setShowIssue] = useState(false);
  const [errors, setErrors] = useState<Partial<Record<HireStep, string>>>({});
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState<{ id: string; name: string; title: string } | null>(null);
  const [filter, setFilter] = useState("");
  const general = t("offices.general");

  useEffect(() => {
    let live = true;
    Promise.all([officesApi.list(), api.roleTemplates(locale)]).then(([o, c]) => {
      if (!live) return;
      registerRoleTemplates(c);
      const ok = hireable(o.offices, o.me.admin);
      setOffices(ok); setCatalog(c);
      setDraft((d) => (d.office || !ok.length ? d : pickOffice(d, ok.find((x) => x.general)?.id ?? ok[0].id)));
    }).catch(() => { if (live) setLoadError(true); });
    orgChartApi.chart().then((c) => {
      const out: PersonRef[] = [];
      const walk = (ds: typeof c.roots) => ds.forEach((d) => { d.members.forEach((m) => m.kind === "person" && out.push({ user_id: m.id, name: m.name })); walk(d.children); });
      walk(c.roots); c.unassigned.forEach((m) => m.kind === "person" && out.push({ user_id: m.id, name: m.name }));
      if (live) setPeople(out);
    }).catch(() => { /* people stay empty: "personas" then cannot be chosen */ });
    return () => { live = false; };
  }, [locale]);

  // the office's positions (General has none)
  useEffect(() => {
    let live = true;
    setPositions(null);
    if (!draft.office || isGeneralOffice(draft.office)) return;
    positionsApi.list(draft.office).then((v) => { if (live) setPositions(v); }).catch(() => { if (live) setPositions(null); });
    return () => { live = false; };
  }, [draft.office]);

  // what the position (first) and the profession suggest, for this office
  useEffect(() => {
    let live = true;
    setSuggested(null);
    if (!draft.template || !draft.office) return;
    agentOfficesApi.suggestedSkills(draft.template, isGeneralOffice(draft.office) ? "" : draft.office, isGeneralOffice(draft.office) ? "" : draft.position)
      .then((s) => { if (live) setSuggested(s); }).catch(() => { if (live) setSuggested([]); });
    return () => { live = false; };
  }, [draft.template, draft.office, draft.position]);

  const set = (patch: (d: HireDraft) => HireDraft) => { setDraft(patch); setShowIssue(false); setErrors({}); };
  const tpl = catalog?.find((x) => x.id === draft.template);
  const office = offices?.find((o) => o.id === draft.office);
  const officeName = officeLabel(office ? { id: office.id, name: office.name } : { id: draft.office, name: "" }, general);
  const position = positions?.positions.find((p) => p.id === draft.position);
  const kept = keptSkills(suggested ?? [], draft);
  const issue = stepIssue(draft, step);
  const stepTitle = t(`hire.step.${step}`);

  const go = (to: HireStep) => { setStep(to); setShowIssue(false); };
  const next = () => { if (issue) { setShowIssue(true); return; } go(Math.min(3, step + 1) as HireStep); };

  const hire = async () => {
    const bad = firstIssueStep(draft);
    if (bad) { go(bad); setShowIssue(true); return; }
    setBusy(true); setErrors({});
    try {
      const b = hireBody(draft, suggested ?? [], locale);
      const res = await agentOfficesApi.hire(b);
      await useStore.getState().loadAll();
      setDone({ id: res.agent.id, name: res.agent.name, title: tpl?.title ?? res.agent.title });
    } catch (e) {
      const err = e as ApiError;
      const status = typeof err?.status === "number" ? err.status : Number(/-> (\d{3})/.exec(String(err?.message))?.[1] ?? 0);
      const h = hireError(status, err?.code, draft);
      setErrors({ [h.step]: t(h.key) }); setStep(h.step);
    } finally { setBusy(false); }
  };

  const reset = () => { setDone(null); setStep(1); setDraft((d) => ({ ...emptyDraft(d.office), office: d.office })); setErrors({}); };
  const shownCatalog = (catalog ?? []).filter((c) => !filter.trim() || `${c.title} ${c.description}`.toLowerCase().includes(filter.trim().toLowerCase()));

  const asking = draft.visibility === "people" ? t("hire.asks.people", { n: draft.people.length })
    : draft.visibility === "directory" || isGeneralOffice(draft.office) && draft.visibility === "office" ? t("hire.asks.company") : t("hire.asks.office", { name: officeName });

  return (
    <Modal title={t("hire.title")} onClose={onClose} testid="hire-dialog" wide>
      {done ? (
        <div className="grid gap-3" data-testid="hire-done">
          <p role="status" data-testid="hire-result" className="rounded-[10px] border border-emerald-400/50 bg-emerald-500/10 px-3 py-2 text-sm font-semibold text-emerald-700">{t("hire.done2", { name: done.name, title: done.title, office: officeName })}</p>
          <div className="flex flex-wrap gap-2">
            <Link href={`/agents/${encodeURIComponent(done.id)}`} onClick={onClose} data-testid="hire-open-sheet" className={btnPrimary}>{t("ag.row.sheet")}</Link>
            <button type="button" onClick={reset} data-testid="hire-another" className={btnGhost}>{t("hire.another")}</button>
            <button type="button" onClick={onClose} className={btnGhost}>{t("common.close")}</button>
          </div>
        </div>
      ) : (
        <div className="grid gap-4">
          <nav aria-label={t("hire.progress")}>
            <ol className="flex items-center gap-2" data-testid="hire-progress">
              {HIRE_STEPS.map((s) => (
                <li key={s} aria-current={s === step ? "step" : undefined} className={`flex flex-1 items-center gap-2 border-t-2 pt-1.5 text-[12px] ${s === step ? "border-accent font-semibold text-accent" : s < step ? "border-accent/50 text-ink2" : "border-line text-mute"}`}>
                  <span className="min-w-0 truncate">{s}. {t(`hire.step.${s}.short`)}</span>
                </li>
              ))}
            </ol>
            <p className="mt-1.5 text-[12px] text-mute" aria-live="polite">{t("hire.stepOf", { n: step, total: 3 })} · {stepTitle}</p>
          </nav>

          {loadError && <div role="alert" data-testid="hire-load-error" className="text-sm font-semibold text-red-600">{t("hire.loadError")}</div>}
          {!loadError && (!offices || !catalog) && <div className="text-sm text-mute">{t("hire.loading")}</div>}
          {errors[step] && <div role="alert" data-testid="hire-error" className="rounded-[10px] border border-red-400/50 bg-red-500/10 px-3 py-2 text-sm font-semibold text-red-600">{errors[step]}</div>}

          {offices && catalog && step === 1 && (
            <fieldset className="grid gap-2" data-testid="hire-step-office">
              <legend className="mb-1 text-[13px] font-semibold">{t("hire.step.1")}</legend>
              {offices.map((o) => (
                <label key={o.id} className={`${row} ${draft.office === o.id ? "border-accent bg-accent-soft" : "border-line hover:border-accent/50"}`}>
                  <input type="radio" name="hire-office" className="mt-0.5 accent-[#0A66C2]" checked={draft.office === o.id} onChange={() => set((d) => pickOffice(d, o.id))} data-testid={`hire-office-${o.id}`} />
                  <span className="min-w-0"><span className="block text-[13px] font-semibold">{officeLabel({ id: o.id, name: o.name }, general)}</span>
                    <span className="block text-[12px] text-mute">{o.general ? t("offices.general.hint") : t("offices.counts", { people: o.member_count, agents: o.agents.length })}</span></span>
                </label>
              ))}
              {offices.length === 0 && <p role="alert" className="text-[12px] font-semibold text-red-600">{t("hire.err.office")}</p>}
              <p className="text-[12px] text-mute">{t("hire.office.default")}</p>
            </fieldset>
          )}

          {offices && catalog && step === 2 && (
            <div className="grid gap-4" data-testid="hire-step-position">
              <section>
                <h3 className="mb-1 text-[13px] font-semibold">{t("hire.profession")}</h3>
                <input type="search" value={filter} onChange={(e) => setFilter(e.target.value)} aria-label={t("hire.profession.search")} placeholder={t("hire.profession.search")} className={`${inputCls} mb-2 max-w-sm`} />
                <ul className="grid max-h-64 gap-1.5 overflow-y-auto pr-1 sm:grid-cols-2" role="radiogroup" aria-label={t("hire.profession")}>
                  {shownCatalog.map((c) => (
                    <li key={c.id}>
                      <label className={`${row} ${draft.template === c.id ? "border-accent bg-accent-soft" : "border-line hover:border-accent/50"}`}>
                        <input type="radio" name="hire-template" className="mt-0.5 accent-[#0A66C2]" checked={draft.template === c.id} onChange={() => set((d) => pickTemplate(d, c.id))} data-testid={`hire-template-${c.id}`} />
                        <span className="min-w-0"><span className="block text-[13px] font-semibold">{c.title}</span><span className="block text-[12px] leading-snug text-mute">{c.description}</span></span>
                      </label>
                    </li>
                  ))}
                  {shownCatalog.length === 0 && <li className="text-[13px] text-mute">{t("hire.empty")}</li>}
                </ul>
              </section>
              {!isGeneralOffice(draft.office) && (
                <label className="grid gap-1 text-[12px] font-semibold">
                  {t("hire.position.label", { office: officeName })}
                  <select data-testid="hire-position" value={draft.position} onChange={(e) => set((d) => pickPosition(d, e.target.value))} className={inputCls}>
                    <option value="">{t("hire.position.none")}</option>
                    {(positions?.positions ?? []).map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
                  </select>
                  <span className="text-[11px] font-normal text-mute">{t("hire.position.hint")}</span>
                </label>
              )}
              {draft.template && (
                <section data-testid="hire-skills" aria-labelledby="hire-skills-h">
                  <h3 id="hire-skills-h" className="text-[13px] font-semibold">{t("hire.skills.title")}</h3>
                  <p className="mb-1.5 text-[12px] text-mute">{t("hire.skills.hint")}</p>
                  {suggested === null ? <p className="text-[12px] text-mute">{t("hire.loading")}</p> : suggested.length === 0 ? <p className="text-[12px] text-mute">{t("hire.skills.none")}</p> : (
                    <ul className="grid gap-1">
                      {suggested.map((s) => (
                        <li key={s.id}>
                          <label className="flex cursor-pointer items-center gap-2 border-b border-line/70 py-1.5 text-[13px]">
                            <input type="checkbox" className="accent-[#0A66C2]" checked={!draft.removed.includes(s.id)} onChange={() => set((d) => ({ ...d, removed: toggleRemoved(d.removed, s.id) }))} data-testid={`hire-skill-${s.id}`} />
                            <span className="min-w-0 flex-1 truncate font-semibold">{skillTitle(s.name, locale)}</span>
                            <span className="shrink-0 text-[11px] text-mute">{s.via === "position" ? t("skills.via.position") : t("skills.via.role")}</span>
                          </label>
                        </li>
                      ))}
                    </ul>
                  )}
                </section>
              )}
              {showIssue && issue === "template" && <p role="alert" className="text-[12px] font-semibold text-red-600">{t("hire.issue.template")}</p>}
            </div>
          )}

          {offices && catalog && step === 3 && (
            <div className="grid gap-4" data-testid="hire-step-name">
              <label className="grid gap-1 text-[12px] font-semibold">
                {t("hire.name.label")}
                <input type="text" value={draft.name} maxLength={NAME_MAX} placeholder={t("hire.namePlaceholder")} data-testid="hire-name" onChange={(e) => set((d) => ({ ...d, name: e.target.value }))} className={inputCls} />
              </label>
              <fieldset className="grid gap-1.5">
                <legend className="mb-1 text-[13px] font-semibold">{t("hire.asks.title")}</legend>
                {visibilityChoices(draft.office).map((v: HireVisibility) => (
                  <label key={v} className={`${row} ${draft.visibility === v ? "border-accent bg-accent-soft" : "border-line hover:border-accent/50"}`}>
                    <input type="radio" name="hire-vis" className="mt-0.5 accent-[#0A66C2]" checked={draft.visibility === v} onChange={() => set((d) => ({ ...d, visibility: v }))} data-testid={`hire-vis-${v}`} />
                    <span><span className="block text-[13px] font-semibold">{v === "office" && isGeneralOffice(draft.office) ? t("hire.asks.company") : t(`hire.vis.${v}`, { name: officeName })}</span>
                      <span className="block text-[12px] text-mute">{t(`hire.vis.${v}.hint`)}</span></span>
                  </label>
                ))}
                {draft.visibility === "people" && (
                  <div className="ml-1 grid gap-1 border-l-2 border-line pl-3" data-testid="hire-people">
                    {people.length === 0 && <p className="text-[12px] text-mute">{t("hire.people.none")}</p>}
                    {people.map((p) => (
                      <label key={p.user_id} className="flex items-center gap-2 text-[13px]">
                        <input type="checkbox" className="accent-[#0A66C2]" checked={draft.people.includes(p.user_id)} onChange={() => set((d) => ({ ...d, people: togglePerson(d.people, p.user_id) }))} />{p.name}
                      </label>
                    ))}
                    {showIssue && issue === "people" && <p role="alert" className="text-[12px] font-semibold text-red-600">{t("hire.issue.people")}</p>}
                  </div>
                )}
              </fieldset>
              {showIssue && issue === "name" && <p role="alert" className="text-[12px] font-semibold text-red-600">{t("hire.issue.name", { max: NAME_MAX })}</p>}
              <section className="rounded-[10px] border border-line bg-panel2 px-3 py-2.5" aria-labelledby="hire-sum-h" data-testid="hire-summary">
                <h3 id="hire-sum-h" className="mb-1 text-[13px] font-semibold">{t("hire.summary")}</h3>
                <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-[12.5px]">
                  <dt className="text-mute">{t("hire.sum.office")}</dt><dd>{officeName}</dd>
                  <dt className="text-mute">{t("hire.sum.role")}</dt><dd>{tpl?.title}{position ? ` · ${position.name}` : ""}</dd>
                  <dt className="text-mute">{t("hire.sum.skills")}</dt><dd>{kept.length ? kept.map((s) => skillTitle(s.name, locale)).join(", ") : t("hire.sum.noSkills")}</dd>
                  <dt className="text-mute">{t("hire.sum.name")}</dt><dd>{draft.name.trim() || t("hire.sum.autoName")}</dd>
                  <dt className="text-mute">{t("hire.sum.asks")}</dt><dd>{asking}</dd>
                </dl>
              </section>
            </div>
          )}

          <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line pt-3">
            <button type="button" data-testid="hire-back" disabled={step === 1 || busy} onClick={() => go((step - 1) as HireStep)} className={btnGhost}>{t("hire.back")}</button>
            {step < 3
              ? <button type="button" data-testid="hire-next" onClick={next} disabled={!offices || !catalog} className={btnPrimary}>{t("hire.next")}</button>
              : <button type="button" data-testid="hire-confirm" onClick={hire} disabled={busy || !offices || !catalog} className={btnPrimary}>{busy ? t("hire.hiring") : t("hire.action")}</button>}
          </div>
        </div>
      )}
    </Modal>
  );
}

/** Offices where this person may hire: an admin in any open one, a responsible in the ones they lead. */
function hireable(all: OfficeView[], admin: boolean): OfficeView[] {
  return all.filter((o) => o.can_enter && (admin || o.can_manage)).sort((a, b) => Number(b.general) - Number(a.general) || a.name.localeCompare(b.name));
}
