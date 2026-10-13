"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { fmtBytes, type CodeSettings, type CodeSettingsInput } from "@/lib/codeApi";
import { Btn } from "../ui";
import { inputCls } from "../connections/shared";

/**
 * Opt-in "desk mode" for code execution (docs/architecture/code-sandbox.md): off by default; turning it on
 * needs the risk text read and an explicit acknowledgement (the server refuses the change without it).
 */
export function DeskModeSetting({ cfg, isAdmin, save }: { cfg: CodeSettings; isAdmin: boolean; save: (patch: Partial<CodeSettingsInput>) => Promise<void> }) {
  const { t } = useT();
  const on = cfg.auto_run === "desk";
  const [pick, setPick] = useState<"off" | "desk">(on ? "desk" : "off");
  const [ack, setAck] = useState(false);
  const [perTask, setPerTask] = useState<number>(cfg.auto_max_runs_per_task ?? 0);
  useEffect(() => { setPick(cfg.auto_run === "desk" ? "desk" : "off"); setAck(false); }, [cfg.auto_run]);
  useEffect(() => setPerTask(cfg.auto_max_runs_per_task ?? 0), [cfg.auto_max_runs_per_task]);
  if (cfg.auto_run === undefined) return null; // older backend: no desk mode

  const choose = (v: "off" | "desk") => {
    setPick(v); setAck(false);
    if (v === "off" && on) void save({ auto_run: "off" }); // turning it off never needs an acknowledgement
  };
  const canPick = isAdmin && cfg.enabled;

  return (
    <fieldset data-testid="ctl-code-desk" className="space-y-2 rounded-md border border-line p-3">
      <legend className="px-1 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("code.auto.title")}</legend>
      {!cfg.enabled && <p className="text-mute">{t("code.auto.needsEnabled")}</p>}
      <label className="flex items-start gap-2">
        <input type="radio" name="code-auto" data-testid="ctl-code-auto-off" className="mt-0.5" checked={pick === "off"} disabled={!isAdmin} onChange={() => choose("off")} />
        <span><span className="font-semibold">{t("code.auto.off")}</span><br /><span className="text-mute">{t("code.auto.offHelp")}</span></span>
      </label>
      <label className="flex items-start gap-2">
        <input type="radio" name="code-auto" data-testid="ctl-code-auto-desk" className="mt-0.5" checked={pick === "desk"} disabled={!canPick && !on} onChange={() => choose("desk")} />
        <span><span className="font-semibold">{t("code.auto.desk")}</span><br /><span className="text-mute">{t("code.auto.deskHelp")}</span></span>
      </label>
      {pick === "desk" && (
        <div className="space-y-2 pl-6">
          <p role="note" data-testid="ctl-code-auto-risk" className="rounded-md border border-amber-500/50 bg-amber-50 p-2 leading-snug text-amber-900">{t("code.auto.risk")}</p>
          {!on && (
            <>
              <label className="flex items-start gap-2 font-semibold">
                <input type="checkbox" data-testid="ctl-code-auto-ack" className="mt-0.5" checked={ack} disabled={!canPick} onChange={(e) => setAck(e.target.checked)} />
                <span>{t("code.auto.ack")}</span>
              </label>
              <Btn kind="danger" data-testid="ctl-code-auto-enable" disabled={!canPick || !ack} onClick={() => void save({ auto_run: "desk", acknowledge_risk: true })}>{t("code.auto.enable")}</Btn>
            </>
          )}
          {on && (
            <div className="space-y-2">
              <p data-testid="ctl-code-auto-on" className="font-semibold text-amber-900">{t("code.auto.on")}</p>
              <p className="text-mute">{t("code.auto.caps", { code: fmtBytes(cfg.auto_effective_code_bytes ?? 0), input: fmtBytes(cfg.auto_effective_input_bytes ?? 0) })}</p>
              <label className="block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("code.auto.perTask", { max: 20 })}
                <input type="number" min={0} max={20} data-testid="ctl-code-auto-per-task" className={`${inputCls} mt-1 font-normal normal-case`} value={perTask} disabled={!isAdmin}
                  onChange={(e) => setPerTask(Math.min(20, Math.max(0, Math.floor(Number(e.target.value) || 0))))}
                  onBlur={() => { if (perTask !== (cfg.auto_max_runs_per_task ?? 0)) void save({ auto_max_runs_per_task: perTask }); }} />
              </label>
            </div>
          )}
        </div>
      )}
    </fieldset>
  );
}
