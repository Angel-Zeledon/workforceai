"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { projectsApi } from "@/lib/projects/api";
import { useProjects } from "@/lib/projects/store";
import {
  TEMPLATE_CATEGORIES, TPL_LIMITS, addParam, addPhase, addTask, addWorkflow, blankState, canAddPhase, canAddTask, canAddWorkflow, canAddParam, countOf,
  fromTemplate, localIssues, moveTask, movePhase, moveWorkflow, patchParam, patchPhase, patchTask, patchWorkflow, placeholders, removeParam, removePhase,
  removeTask, removeWorkflow, splitList, listMax, taskIndex, toInput, type BuilderState,
} from "@/lib/projects/templateLogic";
import type { ProjectTemplate, TemplateIssue, TemplatePreview } from "@/lib/projects/types";
import { useStore } from "@/lib/store";
import { Btn } from "../ui";
import { FocusCtx, ParamRow, RepeatSelect, RowTools, TaskEditor, TextField, useInserter, type TaskRef } from "./builderParts";
import { fmtMoney } from "./shared";
import { TemplateHistoryModal, httpStatus, inputCls, issuesOfError, labelCls, useIssueText } from "./templateUi";

const panelCls = "rounded-xl border border-line bg-panel p-4 shadow-pop";
const h2Cls = "text-[11px] font-semibold uppercase tracking-[0.08em] text-mute";

/** Form-based builder of a work template: parameters, phases > workflows > tasks, live preview, validation and versions. */
export function TemplateBuilder() {
  const { t, locale } = useT();
  const issueText = useIssueText();
  const editingId = useProjects((s) => s.editingId);
  const setView = useProjects((s) => s.setView);
  const loadTemplates = useProjects((s) => s.loadTemplates);

  const [s, setS] = useState<BuilderState | null>(editingId ? null : blankState());
  const [tplId, setTplId] = useState<string | null>(editingId);
  const [version, setVersion] = useState(0);
  const [baseline, setBaseline] = useState(() => (editingId ? "" : JSON.stringify(toInput(blankState()))));
  const [loadErr, setLoadErr] = useState(false);
  const [open, setOpen] = useState<Set<string>>(new Set());
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [saveErr, setSaveErr] = useState("");
  const [conflict, setConflict] = useState(false);
  const [issues, setIssues] = useState<TemplateIssue[] | null>(null);
  const [checking, setChecking] = useState(false);
  const [history, setHistory] = useState(false);
  const inserter = useInserter();

  // ---- load ------------------------------------------------------------------------------------------------
  const apply = (tpl: ProjectTemplate) => {
    const st = fromTemplate(tpl, (k) => t(k));
    setS(st); setTplId(tpl.id); setVersion(tpl.version);
    setBaseline(JSON.stringify(toInput(st)));
    const total = countOf(st).tasks;
    setOpen(total <= 12 ? new Set(st.phases.flatMap((p) => p.workflows.flatMap((w) => w.tasks.map((x) => x.key)))) : new Set());
    setConflict(false); setSaveErr(""); setIssues(null);
  };
  useEffect(() => {
    if (!editingId) { setOpen(new Set(["t1"])); return; }
    let alive = true;
    projectsApi.getTemplate(editingId).then((tpl) => { if (alive) apply(tpl); }).catch(() => { if (alive) setLoadErr(true); });
    return () => { alive = false; };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editingId]);

  const current = useMemo(() => (s ? JSON.stringify(toInput(s)) : ""), [s]);
  const dirty = !!s && current !== baseline;
  useEffect(() => {
    if (!dirty) return;
    const h = (e: BeforeUnloadEvent) => { e.preventDefault(); e.returnValue = ""; };
    window.addEventListener("beforeunload", h);
    return () => window.removeEventListener("beforeunload", h);
  }, [dirty]);

  const leave = () => {
    if (dirty && !window.confirm(t("tpl.bld.leaveConfirm"))) return;
    setView("library");
  };

  const upd = (f: (x: BuilderState) => BuilderState) => { setS((x) => (x ? f(x) : x)); setSaved(false); };
  const index: TaskRef[] = useMemo(() => {
    if (!s) return [];
    const out: TaskRef[] = [];
    s.phases.forEach((p, i) => p.workflows.forEach((w) => w.tasks.forEach((x) => out.push({ key: x.key, title: x.title, repeat: w.repeat, where: `${p.title || `${t("tpl.bld.phase")} ${i + 1}`} › ${w.title || w.key}` }))));
    return out;
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [s, locale]);
  const toggle = (k: string) => setOpen((o) => { const n = new Set(o); if (n.has(k)) n.delete(k); else n.add(k); return n; });

  // ---- save / validate -------------------------------------------------------------------------------------
  const save = async () => {
    if (!s || busy || !s.name.trim()) return;
    setBusy(true); setSaveErr(""); setConflict(false); setSaved(false);
    try {
      const body = toInput(s, { baseVersion: tplId ? version : undefined, note: tplId ? note : undefined });
      const tpl = tplId ? await projectsApi.updateTemplate(tplId, body) : await projectsApi.createTemplate(body);
      setTplId(tpl.id); setVersion(tpl.version); setBaseline(JSON.stringify(toInput(s))); setNote(""); setSaved(true); setIssues(null);
      loadTemplates();
    } catch (e) {
      if (httpStatus(e) === 409) setConflict(true);
      else {
        const { issues: iss, limit } = issuesOfError(e);
        if (iss.length) { setIssues(iss); setSaveErr(t("tpl.bld.saveInvalid")); }
        else if (limit) setSaveErr(issueText(limit.code, limit.limit));
        else setSaveErr(t("tpl.bld.saveError"));
      }
    }
    setBusy(false);
  };
  const validate = async () => {
    if (!s || checking) return;
    setChecking(true); setSaveErr("");
    const local = localIssues(s).filter((i) => !(i.code === "empty_title" || i.code === "no_agent"));
    try {
      const r = await projectsApi.validateTemplate(toInput(s));
      setIssues([...local, ...r.issues]);
    } catch (e) {
      const { issues: iss } = issuesOfError(e);
      setIssues(iss.length ? iss : local);
      if (!iss.length) setSaveErr(t("tpl.bld.validateError"));
    }
    setChecking(false);
  };
  const reload = () => { if (!tplId) return; if (dirty && !window.confirm(t("tpl.bld.reloadConfirm"))) return; projectsApi.getTemplate(tplId).then(apply).catch(() => setSaveErr(t("tpl.bld.saveError"))); };

  if (loadErr) return <div className="mx-auto max-w-[900px] p-6"><button type="button" onClick={() => setView("library")} className="text-[11px] font-semibold text-accent hover:underline">‹ {t("tpl.bld.back")}</button><div className="mt-3 text-sm text-mute">{t("tpl.bld.loadError")}</div></div>;
  if (!s) return <div data-testid="template-builder" className="p-6 text-sm text-mute">{t("tpl.lib.loading")}</div>;

  const c = countOf(s);
  const chips = [...new Set([...placeholders(s), ...s.params.filter((p) => p.type === "list" && p.itemName).map((p) => `{${p.itemName}}`)])];

  const addTaskAt = (i: number, j: number) => {
    const next = addTask(s, i, j);
    const k = next.phases[i].workflows[j].tasks.at(-1)?.key;
    upd(() => next);
    if (k && next !== s) setOpen((o) => new Set(o).add(k));
  };
  const confirmRemove = (count: number) => count < 2 || window.confirm(t("tpl.bld.removeConfirm", { count }));
  const lenOfPhase = (i: number) => s.phases[i].workflows.reduce((n, w) => n + w.tasks.length, 0);

  return (
    <FocusCtx.Provider value={{ register: inserter.register }}>
      <div data-testid="template-builder" className="mx-auto max-w-[1280px] space-y-4 p-6">
        {/* header */}
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div>
            <button type="button" data-testid="tb-back" onClick={leave} className="text-[11px] font-semibold text-accent hover:underline">‹ {t("tpl.bld.back")}</button>
            <h1 className="font-display text-2xl font-semibold text-ink">{tplId ? t("tpl.bld.titleEdit", { version }) : t("tpl.bld.titleNew")}</h1>
            <p className="max-w-[680px] text-xs text-mute">{t("tpl.bld.subtitle")}</p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {dirty && <span data-testid="tb-dirty" className="text-[11px] font-semibold text-amber-700">{t("tpl.bld.unsaved")}</span>}
            {saved && !dirty && <span data-testid="tb-saved" className="text-[11px] font-semibold text-ok">{t("tpl.bld.saved")}</span>}
            {tplId && <input data-testid="tb-note" value={note} onChange={(e) => setNote(e.target.value)} placeholder={t("tpl.bld.notePh")} aria-label={t("tpl.bld.note")} maxLength={200} className={`${inputCls} !w-[200px]`} />}
            {tplId && <Btn data-testid="tb-history" onClick={() => setHistory(true)}>{t("tpl.bld.history")}</Btn>}
            <Btn data-testid="tb-save" kind="primary" disabled={busy || !s.name.trim()} onClick={save}>{busy ? t("tpl.save.saving") : tplId ? t("tpl.bld.saveVersion") : t("tpl.bld.save")}</Btn>
          </div>
        </div>
        {conflict && (
          <div data-testid="tb-conflict" role="alert" className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-900">
            <span>{t("tpl.bld.conflict")}</span>
            <Btn data-testid="tb-reload" onClick={reload}>{t("tpl.bld.reload")}</Btn>
          </div>
        )}
        {saveErr && <div data-testid="tb-error" role="alert" className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700">{saveErr}</div>}

        <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,1fr)_340px]">
          <div className="min-w-0 space-y-4">
            {/* details */}
            <section className={panelCls}>
              <h2 className={h2Cls}>{t("tpl.bld.details")}</h2>
              <div className="mt-3 grid gap-3 md:grid-cols-[minmax(0,1fr)_200px]">
                <label className={labelCls}>{t("tpl.bld.name")}
                  <TextField testId="tb-name" value={s.name} onChange={(name) => upd((x) => ({ ...x, name }))} placeholder={t("tpl.bld.namePh")} className="mt-1" maxLength={120} />
                </label>
                <label className={labelCls}>{t("tpl.bld.category")}
                  <select data-testid="tb-category" value={s.category} onChange={(e) => upd((x) => ({ ...x, category: e.target.value }))} className={`${inputCls} mt-1`}>
                    {TEMPLATE_CATEGORIES.map((k) => <option key={k} value={k}>{t(`tpl.cat.${k}`)}</option>)}
                  </select>
                </label>
                <label className={`${labelCls} md:col-span-2`}>{t("tpl.bld.description")}
                  <TextField multiline rows={2} testId="tb-description" value={s.description} onChange={(description) => upd((x) => ({ ...x, description }))} placeholder={t("tpl.bld.descriptionPh")} className="mt-1" maxLength={4000} />
                </label>
                <label className="flex items-start gap-2 text-xs text-ink md:col-span-2">
                  <input data-testid="tb-shared" type="checkbox" checked={s.shared} onChange={(e) => upd((x) => ({ ...x, shared: e.target.checked }))} className="mt-0.5" />
                  <span>{t("tpl.bld.shared")}<span className="block text-[10px] text-mute">{t("tpl.bld.sharedHint")}</span></span>
                </label>
              </div>
            </section>

            {/* parameters */}
            <section className={panelCls}>
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <h2 className={h2Cls}>{t("tpl.bld.params")} <span className="font-mono normal-case">{c.params}/{TPL_LIMITS.params}</span></h2>
                  <p className="mt-0.5 text-[11px] text-mute">{t("tpl.bld.paramsHint")}</p>
                </div>
                <Btn data-testid="tb-add-param" disabled={!canAddParam(s)} onClick={() => upd(addParam)}>{t("tpl.bld.addParam")}</Btn>
              </div>
              <div className="mt-3 space-y-2">
                {s.params.length === 0 && <div className="rounded-lg border border-dashed border-line-strong px-3 py-4 text-center text-xs text-mute">{t("tpl.bld.paramsEmpty")}</div>}
                {s.params.map((p, i) => <ParamRow key={i} p={p} i={i} state={s} onPatch={(patch) => upd((x) => patchParam(x, i, patch))} onRemove={() => upd((x) => removeParam(x, i))} />)}
              </div>
            </section>

            {/* structure */}
            <section className={panelCls}>
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <h2 className={h2Cls}>{t("tpl.bld.structure")}</h2>
                  <p data-testid="tb-counts" className="mt-0.5 font-mono text-[10px] text-mute">{t("tpl.bld.counts", { phases: c.phases, maxPhases: TPL_LIMITS.phases, workflows: c.workflows, maxWorkflows: TPL_LIMITS.workflows, tasks: c.tasks, maxTasks: TPL_LIMITS.tasks })}</p>
                </div>
                <Btn data-testid="tb-add-phase" disabled={!canAddPhase(s)} onClick={() => upd(addPhase)}>{t("tpl.bld.addPhase")}</Btn>
              </div>
              <div className="mt-3 space-y-4">
                {s.phases.map((ph, i) => (
                  <div key={ph.key} data-testid={`tb-phase-${i}`} className="rounded-xl border border-line-strong bg-panel2 p-3">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="shrink-0 rounded-md bg-accent px-2 py-1 text-[10px] font-semibold uppercase tracking-wide text-white">{t("tpl.bld.phase")} {i + 1}</span>
                      <TextField testId={`tb-phase-${i}-title`} value={ph.title} onChange={(title) => upd((x) => patchPhase(x, i, { title }))} placeholder={t("tpl.bld.phaseTitlePh")} className="min-w-[200px] flex-1 !text-sm font-semibold" maxLength={300} />
                      <RowTools id={`tb-phase-${i}`} index={i} count={s.phases.length} onMove={(d) => upd((x) => movePhase(x, i, d))} onRemove={() => { if (s.phases.length > 1 && confirmRemove(lenOfPhase(i))) upd((x) => removePhase(x, i)); }} />
                    </div>
                    <div className="mt-3 space-y-3 border-l-2 border-line-strong pl-3">
                      {ph.workflows.map((w, j) => (
                        <div key={w.key} data-testid={`tb-wf-${i}-${j}`} className="rounded-lg border border-line bg-panel p-3">
                          <div className="flex flex-wrap items-start gap-2">
                            <span className="mt-1.5 shrink-0 rounded-md border border-line-strong px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-mute">{t("tpl.bld.workflow")} {j + 1}</span>
                            <TextField testId={`tb-wf-${i}-${j}-title`} value={w.title} onChange={(title) => upd((x) => patchWorkflow(x, i, j, { title }))} placeholder={t("tpl.bld.workflowTitlePh")} className="min-w-[180px] flex-1 font-semibold" maxLength={300} />
                            <RepeatSelect wf={w} state={s} testId={`tb-wf-${i}-${j}-repeat`} onChange={(repeat) => upd((x) => patchWorkflow(x, i, j, { repeat }))} />
                            <RowTools id={`tb-wf-${i}-${j}`} index={j} count={ph.workflows.length} onMove={(d) => upd((x) => moveWorkflow(x, i, j, d))} onRemove={() => { if (ph.workflows.length > 1 && confirmRemove(w.tasks.length)) upd((x) => removeWorkflow(x, i, j)); }} />
                          </div>
                          <div className="mt-2.5 space-y-2">
                            {w.tasks.map((task, k) => (
                              <TaskEditor key={task.key} task={task} pos={[i, j, k]} count={w.tasks.length} open={open.has(task.key)} onToggle={() => toggle(task.key)} state={s} wf={w} index={index}
                                onPatch={(patch) => upd((x) => patchTask(x, i, j, k, patch))} onMove={(d) => upd((x) => moveTask(x, i, j, k, d))}
                                onRemove={() => { if (w.tasks.length > 1) upd((x) => removeTask(x, i, j, k)); }} />
                            ))}
                          </div>
                          <div className="mt-2.5"><Btn data-testid={`tb-add-task-${i}-${j}`} disabled={!canAddTask(s)} onClick={() => addTaskAt(i, j)}>{t("tpl.bld.addTask")}</Btn></div>
                        </div>
                      ))}
                      <Btn data-testid={`tb-add-workflow-${i}`} disabled={!canAddWorkflow(s)} onClick={() => upd((x) => addWorkflow(x, i))}>{t("tpl.bld.addWorkflow")}</Btn>
                    </div>
                  </div>
                ))}
              </div>
            </section>
          </div>

          {/* side panel */}
          <aside className="min-w-0 space-y-4 lg:sticky lg:top-4">
            <PreviewPanel s={s} />
            <section className={panelCls}>
              <div className="flex items-center justify-between gap-2">
                <h2 className={h2Cls}>{t("tpl.bld.checks")}</h2>
                <Btn data-testid="tb-validate" disabled={checking} onClick={validate}>{checking ? t("tpl.bld.validating") : t("tpl.bld.validate")}</Btn>
              </div>
              {issues && <IssueList issues={issues} s={s} />}
            </section>
            <section className={panelCls}>
              <h2 className={h2Cls}>{t("tpl.bld.placeholders")}</h2>
              <p className="mt-1 text-[11px] text-mute">{t("tpl.bld.placeholdersHint")}</p>
              <div className="mt-2 flex flex-wrap gap-1.5">
                {chips.length === 0 && <span className="text-[11px] text-mute">{t("tpl.bld.placeholdersNone")}</span>}
                {chips.map((ch) => (
                  <button key={ch} type="button" data-testid={`tb-chip-${ch.slice(1, -1)}`} onMouseDown={(e) => e.preventDefault()} onClick={() => inserter.insert(ch)}
                    className="rounded-full border border-accent/30 bg-accent-soft px-2.5 py-0.5 font-mono text-[11px] font-semibold text-accent hover:bg-accent/10">{ch}</button>
                ))}
              </div>
              <p className="mt-2 text-[10px] text-mute">{t("tpl.bld.placeholdersParams")}</p>
            </section>
          </aside>
        </div>
        {history && tplId && <TemplateHistoryModal templateId={tplId} onClose={() => setHistory(false)} onRestored={(tpl) => { apply(tpl); loadTemplates(); }} />}
      </div>
    </FocusCtx.Provider>
  );
}

function IssueList({ issues, s }: { issues: TemplateIssue[]; s: BuilderState }) {
  const { t } = useT();
  const issueText = useIssueText();
  const names = new Map<string, string>();
  s.params.forEach((p) => names.set(p.key, p.label || p.key));
  s.phases.forEach((p) => { names.set(p.key, p.title); p.workflows.forEach((w) => { names.set(w.key, w.title); w.tasks.forEach((x) => names.set(x.key, x.title)); }); });
  if (!issues.length) return <div data-testid="tb-issues" className="mt-3 rounded-lg bg-ok/10 px-3 py-2 text-xs font-semibold text-ok">{t("tpl.bld.noIssues")}</div>;
  return (
    <ul data-testid="tb-issues" className="mt-3 space-y-1.5">
      {issues.map((i, n) => (
        <li key={n} data-severity={i.severity} data-code={i.code} className={`rounded-lg border px-2.5 py-1.5 text-[11px] ${i.severity === "error" ? "border-red-200 bg-red-50 text-red-800" : "border-amber-200 bg-amber-50 text-amber-900"}`}>
          <span className="font-semibold">{i.severity === "error" ? t("tpl.bld.error") : t("tpl.bld.warning")}:</span> {issueText(i.code)}
          {i.node_id && <span className="mt-0.5 block text-[10px] opacity-80">{names.get(i.node_id) || i.node_id}</span>}
        </li>
      ))}
    </ul>
  );
}

/** Parameters of the preview + the expanded size, longest chain, widest level, agents and cost (debounced call with the unsaved template). */
function PreviewPanel({ s }: { s: BuilderState }) {
  const { t, locale } = useT();
  const issueText = useIssueText();
  const agents = useStore((st) => st.agents);
  const [values, setValues] = useState<Record<string, string>>({});
  const [pv, setPv] = useState<TemplatePreview | null>(null);
  const [err, setErr] = useState("");
  const [loading, setLoading] = useState(false);
  const seq = useRef(0);
  const body = useMemo(() => JSON.stringify(toInput(s)), [s]);
  const params = s.params.filter((p) => p.key);
  const valueOf = (key: string, def: string) => values[key] ?? def;
  const overrides = useMemo(() => Object.fromEntries(Object.entries(values).filter(([k]) => params.some((p) => p.key === k))),
  // eslint-disable-next-line react-hooks/exhaustive-deps
  [values, body]);

  useEffect(() => {
    const my = ++seq.current;
    setLoading(true);
    const h = setTimeout(async () => {
      try {
        const r = await projectsApi.previewTemplate({ template: JSON.parse(body), params: overrides, locale });
        if (my === seq.current) { setPv(r); setErr(""); }
      } catch (e) {
        if (my !== seq.current) return;
        const { issues, limit } = issuesOfError(e);
        setErr(limit ? issueText(limit.code, limit.limit) : issues.length ? issues.map((i) => issueText(i.code)).join(" · ") : t("tpl.prev.error"));
      }
      if (my === seq.current) setLoading(false);
    }, 600);
    return () => clearTimeout(h);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [body, overrides, locale]);

  const agentName = (id: string) => agents[id]?.name?.split(" ")[0] ?? id;
  return (
    <section data-testid="tb-preview" className={`${panelCls} ${loading ? "[&_[data-live]]:opacity-60" : ""}`}>
      <h2 className={h2Cls}>{t("tpl.prev.title")}</h2>
      {params.length > 0 && (
        <div className="mt-2 space-y-2">
          <div className="text-[10px] text-mute">{t("tpl.prev.paramsHint")}</div>
          {params.map((p) => {
            const v = valueOf(p.key, p.default);
            const n = p.type === "list" ? splitList(v).length : 0;
            const over = p.type === "list" && n > listMax({ max: parseInt(p.max, 10) || undefined });
            return (
              <label key={p.key} className={labelCls}>{p.label || p.key}
                {p.type === "list"
                  ? <textarea data-testid={`tb-preview-param-${p.key}`} rows={2} value={v} onChange={(e) => setValues({ ...values, [p.key]: e.target.value })} className={`${inputCls} mt-1 resize-y ${over ? "border-red-400" : ""}`} />
                  : <input data-testid={`tb-preview-param-${p.key}`} type={p.type === "number" ? "number" : "text"} value={v} onChange={(e) => setValues({ ...values, [p.key]: e.target.value })} className={`${inputCls} mt-1`} />}
                {p.type === "list" && <span className={`mt-0.5 block text-[10px] font-normal ${over ? "text-red-700" : "text-mute"}`}>{t("tpl.wiz.items", { count: n })}</span>}
              </label>
            );
          })}
        </div>
      )}
      <div data-live className="mt-3 transition-opacity" aria-live="polite">
        {err && <div data-testid="tb-preview-error" className="mb-2 rounded-lg border border-red-200 bg-red-50 px-2.5 py-1.5 text-[11px] text-red-700">{err}</div>}
        {pv ? (
          <div className={err ? "opacity-50" : ""}>
            <div className="font-mono text-3xl font-semibold text-ink"><span data-testid="tb-preview-tasks">{pv.tasks}</span> <span className="font-sans text-xs font-medium text-mute">{t("tpl.prev.tasks")}</span></div>
            <dl className="mt-3 grid grid-cols-3 gap-2 text-center">
              <div className="rounded-lg bg-panel2 p-2"><dd data-testid="tb-preview-gates" className="font-mono text-base font-semibold text-ink">{pv.gates}</dd><dt className="text-[10px] text-mute">{t("tpl.prev.gates")}</dt></div>
              <div className="rounded-lg bg-panel2 p-2"><dd data-testid="tb-preview-parallel" className="font-mono text-base font-semibold text-ink">{pv.parallel}</dd><dt className="text-[10px] text-mute">{t("tpl.prev.parallel")}</dt></div>
              <div className="rounded-lg bg-panel2 p-2"><dd data-testid="tb-preview-levels" className="font-mono text-base font-semibold text-ink">{pv.levels}</dd><dt className="text-[10px] text-mute">{t("tpl.prev.levels")}</dt></div>
            </dl>
            {pv.estimate && (
              <div className="mt-3 text-xs"><span className="text-mute">{t("tpl.prev.cost")}</span>{" "}
                <span data-testid="tb-preview-cost" className="font-mono font-semibold text-ink">{fmtMoney(pv.estimate.total.p50_usd)} – {fmtMoney(pv.estimate.total.p90_usd)}</span></div>
            )}
            {pv.agents.length > 0 && <div data-testid="tb-preview-agents" className="mt-2 text-[11px] text-mute">{t("tpl.prev.agents")}: <span className="text-ink">{pv.agents.map(agentName).join(", ")}</span></div>}
            {pv.sample && pv.sample.length > 0 && (
              <div className="mt-3">
                <div className="text-[10px] font-semibold uppercase tracking-wide text-mute">{t("tpl.prev.sample")}</div>
                <ol data-testid="tb-preview-sample" className="mt-1 space-y-0.5 text-[11px] text-ink">
                  {pv.sample.slice(0, 6).map((x, n) => <li key={n} className="flex gap-1.5" title={x}><span className="w-3 shrink-0 text-right font-mono text-mute">{n + 1}</span><span className="truncate">{x || "—"}</span></li>)}
                </ol>
              </div>
            )}
          </div>
        ) : !err && <div className="text-xs text-mute">{t("tpl.prev.loading")}</div>}
      </div>
    </section>
  );
}
