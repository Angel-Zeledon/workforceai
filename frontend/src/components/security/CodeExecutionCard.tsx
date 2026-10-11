"use client";
import { useCallback, useEffect, useState } from "react";
import { fmtDateTime, useT } from "@/lib/i18n";
import { codeApi, type CodeRun, type CodeSettings } from "@/lib/codeApi";
import { useStore } from "@/lib/store";
import { Btn, Card } from "../ui";
import { ErrorLine, errText, inputCls, useViewer } from "../connections/shared";
import { CodeRunResult } from "../code/CodeRunView";

/** Per-organization switch for agents running code (off by default), its limits, and the history of runs. */
export function CodeExecutionCard() {
  const { t } = useT();
  const { isAdmin } = useViewer();
  const agents = useStore((s) => s.agents);
  const [cfg, setCfg] = useState<CodeSettings | null>(null);
  const [runs, setRuns] = useState<CodeRun[]>([]);
  const [open, setOpen] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const load = useCallback(() => {
    codeApi.settings().then(setCfg).catch(() => setCfg(null)); // older backend or no permission: the card hides
    codeApi.runs(10).then(setRuns).catch(() => setRuns([]));
  }, []);
  useEffect(load, [load]);
  if (!cfg) return null;

  const save = async (next: CodeSettings) => {
    setErr(null); setSaved(false);
    try { setCfg(await codeApi.putSettings({ enabled: next.enabled, max_runs_per_day: next.max_runs_per_day, max_runtime_seconds: next.max_runtime_seconds })); setSaved(true); }
    catch (e) { setErr(errText(e)); }
  };
  const num = (v: string) => Math.max(0, Math.floor(Number(v) || 0));

  return (
    <div data-testid="ctl-code">
      <Card title={t("code.settings.title")}>
        <div className="space-y-3 text-xs">
          <p className="text-mute">{t("code.settings.help")}</p>
          {!cfg.available && <p data-testid="ctl-code-unavailable" className="rounded-md border border-amber-500/50 bg-amber-50 p-2 text-amber-900">{t("code.settings.unavailable")}</p>}
          <label className="flex items-center gap-2 font-semibold">
            <input type="checkbox" data-testid="ctl-code-enabled" checked={cfg.enabled} disabled={!isAdmin} onChange={(e) => save({ ...cfg, enabled: e.target.checked })} />
            {t("code.settings.enable")}
          </label>
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("code.settings.maxRuns", { max: cfg.ceiling_runs_per_day })}
              <input type="number" min={0} max={cfg.ceiling_runs_per_day} data-testid="ctl-code-max-runs" className={`${inputCls} mt-1 font-normal normal-case`} value={cfg.max_runs_per_day} disabled={!isAdmin}
                onChange={(e) => setCfg({ ...cfg, max_runs_per_day: num(e.target.value) })} onBlur={() => save(cfg)} />
            </label>
            <label className="block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("code.settings.maxRuntime", { max: cfg.ceiling_runtime_seconds })}
              <input type="number" min={0} max={cfg.ceiling_runtime_seconds} data-testid="ctl-code-max-runtime" className={`${inputCls} mt-1 font-normal normal-case`} value={cfg.max_runtime_seconds} disabled={!isAdmin}
                onChange={(e) => setCfg({ ...cfg, max_runtime_seconds: num(e.target.value) })} onBlur={() => save(cfg)} />
            </label>
          </div>
          <p className="text-[11px] text-mute">{t("code.settings.zero")}</p>
          {saved && <p data-testid="ctl-code-saved" className="text-emerald-700">{t("code.settings.saved")}</p>}
          <ErrorLine msg={err} />
          <div>
            <div className="mb-1 flex items-center justify-between">
              <b>{t("code.settings.history")}</b>
              <Btn onClick={load}>{t("code.settings.refresh")}</Btn>
            </div>
            {runs.length === 0 ? <p className="text-mute">{t("code.settings.noRuns")}</p> : (
              <ul className="space-y-1.5">
                {runs.map((r) => (
                  <li key={r.id} data-testid="ctl-code-run" className="rounded-md border border-line bg-panel2 px-2.5 py-1.5">
                    <button type="button" className="flex w-full items-center justify-between gap-2 text-left" onClick={() => setOpen(open === r.id ? null : r.id)}>
                      <span className="min-w-0 truncate"><span className="font-semibold">{agents[r.agent_id]?.name ?? r.agent_id}</span> · {r.purpose}</span>
                      <span className="shrink-0 text-[11px] text-mute">{t(`code.status.${r.status}`)} · {fmtDateTime(r.created_at)}</span>
                    </button>
                    {open === r.id && <div className="mt-2"><CodeRunResult run={r} /></div>}
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      </Card>
    </div>
  );
}
