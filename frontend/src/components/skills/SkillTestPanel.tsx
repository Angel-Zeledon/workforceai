"use client";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import { SkillError, type SkillDetail } from "@/lib/skills";
import { skillTestsApi } from "@/lib/skillTests";
import {
  canCompare, caseIssue, pickCaseId, failKey, fmtSeconds, fmtUSD, groupRuns, testedLabel,
  type SkillTestAgent, type SkillTestCase, type SkillTestEstimate, type SkillTestOverview, type SkillTestRun,
} from "@/lib/skillTestsLogic";
import { Btn, Card } from "../ui";
import { field, label } from "./shared";

/** The tests of one skill: cases, the last result of each version. Loaded once by the review screen and shared by the
 * badge next to "Activar" and the test panel. */
export function useSkillTests(skillId: string) {
  const [ov, setOv] = useState<SkillTestOverview | null>(null);
  const reload = useCallback(async () => {
    try { setOv(await skillTestsApi.overview(skillId)); } catch { setOv(null); }
  }, [skillId]);
  useEffect(() => { void reload(); }, [reload]);
  return { ov, reload };
}

/** "Sin probar" or "Probada el ...": visible to whoever reviews, never required to activate. */
export function TestedBadge({ ov, version }: { ov: SkillTestOverview | null; version: number }) {
  const { t, locale } = useT();
  const l = testedLabel(ov, version);
  const text = l.kind === "never" ? t("skills.test.never") : t("skills.test.at", { date: new Date(l.at).toLocaleDateString(locale, { day: "numeric", month: "short" }) });
  return (
    <span data-testid="skill-test-badge" data-tested={l.kind === "at" ? "yes" : "no"}
      className={`inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-semibold ${l.kind === "at" ? "bg-accent/10 text-accent" : "bg-mute/10 text-mute"}`}>{text}</span>
  );
}

function RunCard({ r, activeVersion }: { r: SkillTestRun; activeVersion: number }) {
  const { t } = useT();
  const isActive = r.active || r.version === activeVersion;
  return (
    <div data-testid="skill-test-run" data-status={r.status} className="min-w-0 rounded-lg border border-line bg-panel p-3">
      <div className="mb-2 flex flex-wrap items-center gap-2 text-[11px]">
        <b className="text-ink">{t("skills.test.version", { v: r.version })}</b>
        <span className={`rounded px-1.5 py-0.5 ${isActive ? "bg-ok/10 text-ok" : "bg-accent/10 text-accent"}`}>{isActive ? t("skills.test.versionActive") : t("skills.test.versionTested")}</span>
      </div>
      {r.status === "failed" ? (
        <p data-testid="skill-test-fail" className="rounded-lg bg-err/10 px-3 py-2 text-xs text-err">{t(`skills.test.fail.${failKey(r.fail_code)}`)}</p>
      ) : (
        <pre data-testid="skill-test-output" className="max-h-[320px] overflow-auto whitespace-pre-wrap rounded-lg bg-panel2 p-3 font-sans text-[12px] text-ink2">{r.output}</pre>
      )}
      <dl className="mt-2 grid grid-cols-3 gap-2 text-[11px] text-mute">
        <div><dt>{t("skills.test.used")}</dt><dd data-testid="skill-test-used" className={`font-semibold ${r.used_skill ? "text-ok" : "text-warn"}`}>{r.used_skill ? t("skills.test.usedYes") : t("skills.test.usedNo")}</dd></div>
        <div><dt>{t("skills.test.time")}</dt><dd className="font-semibold text-ink2">{fmtSeconds(r.duration_ms)}</dd></div>
        <div><dt>{t("skills.test.cost")}</dt><dd className="font-semibold text-ink2">{fmtUSD(r.cost_usd)}</dd></div>
      </dl>
      {r.blocked.length > 0 && <p data-testid="skill-test-blocked" className="mt-2 text-[11px] text-mute">{t("skills.test.blocked", { tools: r.blocked.join(", ") })}</p>}
    </div>
  );
}

/** "Probar con un caso": pick or write a case, pick an agent, see the estimated cost, run it with the version under review
 * (and, if there is one, the active version side by side). A test sends, pays and changes nothing. */
export function SkillTestPanel({ detail, ov, reload, canRun, canWrite }: { detail: SkillDetail; ov: SkillTestOverview | null; reload: () => Promise<void>; canRun: boolean; canWrite: boolean }) {
  const { t } = useT();
  const version = detail.current_version;
  const [open, setOpen] = useState(false);
  const [agents, setAgents] = useState<SkillTestAgent[]>([]);
  const [caseId, setCaseId] = useState("");
  const [agentId, setAgentId] = useState("");
  const [compare, setCompare] = useState(false);
  const [est, setEst] = useState<SkillTestEstimate | null>(null);
  const [form, setForm] = useState<{ id: string; title: string; prompt: string; expect: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [fresh, setFresh] = useState<SkillTestRun[]>([]);
  const lim = ov?.limits ?? { cases: 10, title: 120, prompt: 2000, expect: 1000 };
  const cases = useMemo(() => ov?.cases ?? [], [ov]);
  const comparable = canCompare(detail.active_version, version);

  const fail = (e: unknown) => setErr(e instanceof SkillError && t(`skills.error.${e.code}`) !== `skills.error.${e.code}` ? t(`skills.error.${e.code}`) : t("skills.error.generic"));

  useEffect(() => {
    if (!open) return;
    let live = true;
    skillTestsApi.candidates(detail.id).then((l) => { if (live) { setAgents(l); setAgentId((a) => a || l[0]?.id || ""); } }).catch(() => undefined);
    return () => { live = false; };
  }, [open, detail.id]);
  useEffect(() => { const next = pickCaseId(cases, caseId); if (next !== caseId) setCaseId(next); }, [cases, caseId]);
  useEffect(() => {
    setEst(null);
    if (!open || !canRun || !caseId || !agentId) return;
    let live = true;
    skillTestsApi.estimate(detail.id, { case_id: caseId, agent_id: agentId, version, compare: compare && comparable }).then((e) => { if (live) setEst(e); }).catch(() => undefined);
    return () => { live = false; };
  }, [open, canRun, caseId, agentId, compare, comparable, version, detail.id]);

  const selected: SkillTestCase | undefined = cases.find((c) => c.id === caseId);
  const shown = groupRuns([...fresh, ...(ov?.runs ?? []).filter((r) => !fresh.some((f) => f.id === r.id))].filter((r) => r.case_id === caseId));

  const saveForm = async () => {
    if (!form) return;
    if (caseIssue(form, lim)) { setErr(t(`skills.test.issue.${caseIssue(form, lim)}`)); return; }
    setBusy(true); setErr("");
    try { const c = await skillTestsApi.saveCase(detail.id, { title: form.title, prompt: form.prompt, expect: form.expect }, form.id); setForm(null); setCaseId(c.id); await reload(); } catch (e) { fail(e); } finally { setBusy(false); }
  };
  const removeCase = async (c: SkillTestCase) => {
    if (!window.confirm(t("skills.test.caseDeleteConfirm"))) return;
    setBusy(true); setErr("");
    try { await skillTestsApi.deleteCase(detail.id, c.id); await reload(); } catch (e) { fail(e); } finally { setBusy(false); }
  };
  const run = async () => {
    setBusy(true); setErr("");
    try {
      const made = await skillTestsApi.run(detail.id, { case_id: caseId, agent_id: agentId, version, compare: compare && comparable });
      setFresh((f) => [...made, ...f]);
      await reload();
    } catch (e) { fail(e); } finally { setBusy(false); }
  };

  return (
    <Card title={t("skills.test.title")} right={<TestedBadge ov={ov} version={version} />} className="" >
      <div data-testid="skill-test-panel" className="space-y-3">
        <p className="text-xs text-ink2">{t("skills.test.intro")}</p>
        {!open ? (
          <Btn data-testid="skill-test-open" onClick={() => setOpen(true)}>{t("skills.test.open")}</Btn>
        ) : (
          <div className="space-y-3">
            <section aria-label={t("skills.test.cases")} className="space-y-1.5">
              <h4 className={label}>{t("skills.test.cases")}</h4>
              {cases.length === 0 && <p data-testid="skill-test-no-cases" className="text-xs text-mute">{t("skills.test.noCases")}</p>}
              {cases.map((c) => (
                <div key={c.id} className={`flex items-start gap-2 rounded-lg border px-3 py-2 text-xs ${c.id === caseId ? "border-accent bg-accent/5" : "border-line"}`}>
                  <input type="radio" name="skill-test-case" checked={c.id === caseId} onChange={() => setCaseId(c.id)} aria-label={c.title} className="mt-0.5" />
                  <button type="button" onClick={() => setCaseId(c.id)} className="min-w-0 flex-1 text-left">
                    <div className="truncate font-semibold text-ink">{c.title}</div>
                    <div className="line-clamp-2 text-mute">{c.prompt}</div>
                  </button>
                  {canWrite && <button type="button" className="text-[11px] text-accent hover:underline" onClick={() => setForm({ id: c.id, title: c.title, prompt: c.prompt, expect: c.expect })}>{t("skills.test.caseEdit")}</button>}
                  {canWrite && <button type="button" className="text-[11px] text-err hover:underline" onClick={() => removeCase(c)}>{t("skills.test.caseDelete")}</button>}
                </div>
              ))}
              {canWrite && !form && (
                <Btn data-testid="skill-test-new-case" disabled={cases.length >= lim.cases} onClick={() => setForm({ id: "", title: "", prompt: "", expect: "" })}>
                  {cases.length >= lim.cases ? t("skills.test.caseLimit", { n: lim.cases }) : t("skills.test.newCase")}
                </Btn>
              )}
              {form && (
                <div data-testid="skill-test-form" className="space-y-2 rounded-lg border border-line bg-panel2 p-3">
                  <div><label className={label}>{t("skills.test.caseTitle")}</label><input value={form.title} maxLength={lim.title} onChange={(e) => setForm({ ...form, title: e.target.value })} className={field} /></div>
                  <div><label className={label}>{t("skills.test.casePrompt")}</label><textarea data-testid="skill-test-prompt" rows={3} value={form.prompt} maxLength={lim.prompt} placeholder={t("skills.test.casePromptPh")} onChange={(e) => setForm({ ...form, prompt: e.target.value })} className={field} /></div>
                  <div><label className={label}>{t("skills.test.caseExpect")}</label><textarea rows={2} value={form.expect} maxLength={lim.expect} placeholder={t("skills.test.caseExpectPh")} onChange={(e) => setForm({ ...form, expect: e.target.value })} className={field} /></div>
                  <div className="flex gap-2"><Btn kind="primary" data-testid="skill-test-save-case" disabled={busy} onClick={saveForm}>{t("skills.test.caseSave")}</Btn><Btn disabled={busy} onClick={() => { setForm(null); setErr(""); }}>{t("skills.test.cancel")}</Btn></div>
                </div>
              )}
            </section>

            {selected?.expect && <p className="rounded-lg bg-panel2 px-3 py-2 text-xs text-ink2"><b>{t("skills.test.expectLabel")}</b> {selected.expect}</p>}

            {canRun ? (
              <section className="space-y-2">
                {agents.length === 0 ? <p data-testid="skill-test-no-agent" className="text-xs text-warn">{t("skills.test.noAgent")}</p> : (
                  <div>
                    <label className={label} htmlFor="skill-test-agent">{t("skills.test.agent")}</label>
                    <select id="skill-test-agent" data-testid="skill-test-agent" value={agentId} onChange={(e) => setAgentId(e.target.value)} className={field}>
                      {agents.map((a) => <option key={a.id} value={a.id}>{a.name}{a.title ? ` · ${a.title}` : ""}</option>)}
                    </select>
                  </div>
                )}
                {comparable && (
                  <label className="flex items-center gap-2 text-xs text-ink2"><input data-testid="skill-test-compare" type="checkbox" checked={compare} onChange={(e) => setCompare(e.target.checked)} />{t("skills.test.compare", { v: detail.active_version })}</label>
                )}
                <p data-testid="skill-test-estimate" className="text-[11px] text-mute">{est ? t("skills.test.estimate", { usd: fmtUSD(est.estimate_usd) }) : t("skills.test.estimateNone")} · {t("skills.test.noEffects")}</p>
                <Btn kind="primary" data-testid="skill-test-run" disabled={busy || !caseId || !agentId} onClick={run}>{busy ? t("skills.test.running") : t("skills.test.run")}</Btn>
              </section>
            ) : <p className="text-xs text-mute">{t("skills.test.onlyReviewers")}</p>}

            {err && <div data-testid="skill-test-error" className="rounded-lg bg-err/10 px-3 py-2 text-xs text-err">{err}</div>}

            <section aria-label={t("skills.test.results")} className="space-y-2">
              <h4 className={label}>{t("skills.test.results")}</h4>
              {shown.length === 0 && <p className="text-xs text-mute">{t("skills.test.noResults")}</p>}
              {shown.map((g) => (
                <div key={g[0].id} data-testid="skill-test-group" className={`grid gap-2 ${g.length > 1 ? "md:grid-cols-2" : ""}`}>
                  {g.map((r) => <RunCard key={r.id} r={r} activeVersion={detail.active_version} />)}
                </div>
              ))}
            </section>
          </div>
        )}
      </div>
    </Card>
  );
}
