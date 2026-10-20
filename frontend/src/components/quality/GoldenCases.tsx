"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { qualityApi, type GoldenCase, type GoldenRun } from "@/lib/qualityApi";
import { parseChecks } from "@/lib/qualityLogic";
import { useCan } from "@/lib/session";
import { Btn } from "../ui";

const field = "w-full rounded-lg border border-line bg-panel2 px-2 py-1.5 text-xs";

/** Golden cases of one agent: define them, record a run (the output is checked by the server and never stored). */
export function GoldenCases({ agentId, onChange }: { agentId: string; onChange: () => void }) {
  const { t } = useT();
  const canWrite = useCan("agents:write");
  const [cases, setCases] = useState<GoldenCase[] | null>(null);
  const [adding, setAdding] = useState(false);
  const [err, setErr] = useState("");

  const load = useCallback(async () => {
    try { setCases(await qualityApi.cases(agentId)); } catch { setCases([]); setErr(t("quality.error.load")); }
  }, [agentId, t]);
  useEffect(() => { load(); }, [load]);
  const changed = () => { load(); onChange(); };

  return (
    <div data-testid="golden-cases" className="mt-3 border-t border-line pt-3">
      <div className="flex items-center justify-between gap-2">
        <div className="text-[12px] font-semibold text-ink">{t("quality.golden.title")}</div>
        {canWrite && !adding && <button type="button" data-testid="golden-add" className="text-[11px] font-medium text-accent" onClick={() => setAdding(true)}>{t("quality.golden.add")}</button>}
      </div>
      <p className="mt-0.5 text-[10.5px] text-mute">{t("quality.golden.help")}</p>
      {adding && <CaseForm onCancel={() => setAdding(false)} onSave={async (b) => { await qualityApi.createCase(agentId, b); setAdding(false); changed(); }} />}
      {cases && cases.length === 0 && !adding && <div className="mt-2 text-xs text-mute">{t("quality.golden.none")}</div>}
      <ul className="mt-2 space-y-1.5">
        {(cases ?? []).map((c) => <CaseRow key={c.id} c={c} canWrite={canWrite} onChange={changed} />)}
      </ul>
      {err && <div className="mt-2 text-xs text-err">{err}</div>}
    </div>
  );
}

function CaseForm({ initial, onSave, onCancel }: { initial?: GoldenCase; onSave: (b: { name: string; input: string; checks: GoldenCase["checks"]; active?: boolean }) => Promise<void>; onCancel: () => void }) {
  const { t } = useT();
  const [name, setName] = useState(initial?.name ?? "");
  const [input, setInput] = useState(initial?.input ?? "");
  const [checks, setChecks] = useState(initial ? initial.checks.map((c) => `${c.kind}: ${c.value}`).join("\n") : "");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    const parsed = parseChecks(checks);
    if (!parsed || parsed.length === 0) { setErr(t("quality.golden.invalidChecks")); return; }
    setBusy(true); setErr("");
    try { await onSave({ name: name.trim(), input: input.trim(), checks: parsed, active: initial?.active }); } catch { setErr(t("quality.golden.error")); setBusy(false); }
  };

  return (
    <div data-testid="golden-form" className="mt-2 space-y-2 rounded-lg bg-panel2 p-2.5">
      <label className="block text-[11px] text-mute">{t("quality.golden.name")}
        <input data-testid="golden-name" value={name} maxLength={120} onChange={(e) => setName(e.target.value)} className={`mt-0.5 ${field}`} />
      </label>
      <label className="block text-[11px] text-mute">{t("quality.golden.input")}
        <textarea data-testid="golden-input" rows={3} value={input} maxLength={4000} onChange={(e) => setInput(e.target.value)} className={`mt-0.5 ${field}`} />
      </label>
      <label className="block text-[11px] text-mute">{t("quality.golden.checks")}
        <textarea data-testid="golden-checks" rows={3} value={checks} onChange={(e) => setChecks(e.target.value)} className={`mt-0.5 font-mono ${field}`} />
        <span className="text-[10.5px]">{t("quality.golden.checksHelp")}</span>
      </label>
      {err && <div className="text-xs text-err">{err}</div>}
      <div className="flex justify-end gap-2">
        <Btn onClick={onCancel}>{t("quality.golden.cancel")}</Btn>
        <Btn kind="primary" data-testid="golden-save" disabled={busy || !name.trim() || !input.trim() || !checks.trim()} onClick={submit}>{t("quality.golden.save")}</Btn>
      </div>
    </div>
  );
}

function CaseRow({ c, canWrite, onChange }: { c: GoldenCase; canWrite: boolean; onChange: () => void }) {
  const { t } = useT();
  const [last, setLast] = useState<GoldenRun | null>(null);
  const [mode, setMode] = useState<"" | "edit" | "run" | "delete">("");
  const [output, setOutput] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const loadLast = useCallback(() => { qualityApi.runs(c.id, 1).then((r) => setLast(r[0] ?? null)).catch(() => setLast(null)); }, [c.id]);
  useEffect(() => { loadLast(); }, [loadLast]);

  const act = async (fn: () => Promise<unknown>, done?: () => void) => {
    setBusy(true); setErr("");
    try { await fn(); done?.(); onChange(); } catch { setErr(t("quality.golden.error")); } finally { setBusy(false); }
  };

  return (
    <li data-testid={`golden-case-${c.id}`} data-active={c.active} className="rounded-md bg-panel2 px-2.5 py-1.5">
      <div className="flex items-center gap-2">
        <span className={`min-w-0 flex-1 truncate text-[12px] font-semibold ${c.active ? "text-ink" : "text-mute"}`}>{c.name}</span>
        {!c.active && <span className="shrink-0 rounded border border-line px-1.5 py-0.5 text-[9.5px] text-mute">{t("quality.golden.inactive")}</span>}
        {last && <span data-testid={`golden-last-${c.id}`} className={`shrink-0 text-[10.5px] font-medium ${last.passed ? "text-ok" : "text-err"}`}>{last.passed ? t("quality.golden.runPassed") : t("quality.golden.runFailed")} · {t("quality.golden.runResult", { passed: last.checks_passed, total: last.checks_total })}</span>}
      </div>
      {canWrite && mode === "" && (
        <div className="mt-1 flex flex-wrap gap-3 text-[11px]">
          {c.active && <button type="button" data-testid={`golden-run-${c.id}`} className="text-accent" onClick={() => setMode("run")}>{t("quality.golden.run")}</button>}
          <button type="button" className="text-mute hover:text-ink" onClick={() => act(() => qualityApi.updateCase(c.id, { name: c.name, input: c.input, checks: c.checks, active: !c.active }))}>{c.active ? t("quality.golden.deactivate") : t("quality.golden.activate")}</button>
          <button type="button" className="text-mute hover:text-ink" onClick={() => setMode("edit")}>{t("quality.golden.edit")}</button>
          <button type="button" className="text-mute hover:text-err" onClick={() => setMode("delete")}>{t("quality.golden.delete")}</button>
        </div>
      )}
      {mode === "run" && (
        <div className="mt-2 space-y-2">
          <label className="block text-[11px] text-mute">{t("quality.golden.runOutput")}
            <textarea data-testid={`golden-output-${c.id}`} rows={3} value={output} onChange={(e) => setOutput(e.target.value)} className={`mt-0.5 ${field}`} />
          </label>
          <div className="flex justify-end gap-2">
            <Btn onClick={() => { setMode(""); setOutput(""); }}>{t("quality.golden.cancel")}</Btn>
            <Btn kind="primary" data-testid={`golden-record-${c.id}`} disabled={busy || !output.trim()} onClick={() => act(() => qualityApi.recordRun(c.id, output), () => { setMode(""); setOutput(""); loadLast(); })}>{t("quality.golden.runRecord")}</Btn>
          </div>
        </div>
      )}
      {mode === "edit" && <CaseForm initial={c} onCancel={() => setMode("")} onSave={async (b) => { await qualityApi.updateCase(c.id, b); setMode(""); onChange(); }} />}
      {mode === "delete" && (
        <div className="mt-1.5 flex flex-wrap items-center gap-2 text-[11px]">
          <span className="text-ink2">{t("quality.golden.deleteConfirm")}</span>
          <button type="button" data-testid={`golden-delete-confirm-${c.id}`} disabled={busy} className="font-medium text-err" onClick={() => act(() => qualityApi.deleteCase(c.id))}>{t("quality.golden.delete")}</button>
          <button type="button" className="text-mute" onClick={() => setMode("")}>{t("quality.golden.cancel")}</button>
        </div>
      )}
      {err && <div className="mt-1 text-xs text-err">{err}</div>}
    </li>
  );
}
