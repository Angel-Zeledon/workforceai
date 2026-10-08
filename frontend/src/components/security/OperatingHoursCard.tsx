"use client";
import { useEffect, useState } from "react";
import { fmtDateTime, useT } from "@/lib/i18n";
import { hoursApi, type AnomalySettings, type OperatingHours, type OperatingHoursInterval } from "@/lib/connections/api";
import { Btn, Card } from "../ui";
import { ErrorLine, errText, inputCls, useViewer } from "../connections/shared";

const DAYS = [1, 2, 3, 4, 5, 6, 0]; // Monday first; 0 = Sunday (same as the backend)
const ZONES = ["America/Mexico_City", "America/Bogota", "America/Santiago", "America/Argentina/Buenos_Aires", "Europe/Madrid", "UTC"];

/** Operating hours (when agents may START new work) and anomaly detection settings. */
export function OperatingHoursCard() {
  const { t } = useT();
  const { isOwner, isAdmin } = useViewer();
  const [hours, setHours] = useState<OperatingHours | null>(null);
  const [anomaly, setAnomaly] = useState<AnomalySettings | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    hoursApi.get().then(setHours).catch((e) => setErr(errText(e)));
    hoursApi.anomaly().then(setAnomaly).catch((e) => setErr(errText(e)));
  }, []);
  if (!hours || !anomaly) return null;

  const dayInterval = (d: number): OperatingHoursInterval | undefined => hours.weekly.find((w) => w.day === d);
  const setDay = (d: number, on: boolean) => {
    const rest = hours.weekly.filter((w) => w.day !== d);
    setHours({ ...hours, weekly: on ? [...rest, { day: d, open: "09:00", close: "18:00" }] : rest });
  };
  const patchDay = (d: number, p: Partial<OperatingHoursInterval>) =>
    setHours({ ...hours, weekly: hours.weekly.map((w) => (w.day === d ? { ...w, ...p } : w)) });
  const save = async () => {
    setErr(null); setSaved(false);
    try { setHours(await hoursApi.put({ enabled: hours.enabled, timezone: hours.timezone, weekly: hours.weekly })); setSaved(true); } catch (e) { setErr(errText(e)); }
  };
  const saveAnomaly = async (next: Pick<AnomalySettings, "disabled" | "auto_freeze">) => {
    setErr(null);
    try { setAnomaly(await hoursApi.putAnomaly(next)); } catch (e) { setErr(errText(e)); }
  };

  return (
    <div data-testid="ctl-hours">
      <Card title={t("ctl.hours.title")}>
        <div className="space-y-3 text-xs">
          <p className="text-mute">{t("ctl.hours.help")}</p>
          <label className="flex items-center gap-2 font-semibold">
            <input type="checkbox" data-testid="ctl-hours-enabled" checked={hours.enabled} disabled={!isAdmin} onChange={(e) => setHours({ ...hours, enabled: e.target.checked })} />
            {t("ctl.hours.enable")}
          </label>
          {hours.enabled && (
            <>
              <label className="block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("ctl.hours.timezone")}
                <select data-testid="ctl-hours-timezone" className={`${inputCls} mt-1 font-normal normal-case`} value={hours.timezone} disabled={!isAdmin} onChange={(e) => setHours({ ...hours, timezone: e.target.value })}>
                  {(ZONES.includes(hours.timezone) ? ZONES : [hours.timezone, ...ZONES]).map((z) => <option key={z} value={z}>{z}</option>)}
                </select>
              </label>
              <ul className="space-y-1">
                {DAYS.map((d) => {
                  const iv = dayInterval(d);
                  return (
                    <li key={d} data-testid={`ctl-hours-day-${d}`} className="flex flex-wrap items-center gap-2">
                      <label className="flex w-28 items-center gap-2 font-semibold">
                        <input type="checkbox" checked={!!iv} disabled={!isAdmin} onChange={(e) => setDay(d, e.target.checked)} />{t(`ctl.hours.day.${d}`)}
                      </label>
                      {iv ? (
                        <>
                          <input type="time" aria-label={t("ctl.hours.open")} className={`${inputCls} w-28`} value={iv.open} disabled={!isAdmin} onChange={(e) => patchDay(d, { open: e.target.value })} />
                          <span className="text-mute">-</span>
                          <input type="time" aria-label={t("ctl.hours.close")} className={`${inputCls} w-28`} value={iv.close === "24:00" ? "23:59" : iv.close} disabled={!isAdmin} onChange={(e) => patchDay(d, { close: e.target.value })} />
                        </>
                      ) : <span className="text-mute">{t("ctl.hours.closed")}</span>}
                    </li>
                  );
                })}
              </ul>
              <p data-testid="ctl-hours-status" data-open={hours.open_now ?? true} className="font-semibold">
                {hours.open_now === false ? t("ctl.hours.closedNow", { when: hours.next_open_at ? fmtDateTime(hours.next_open_at) : "-" }) : t("ctl.hours.openNow")}
              </p>
              <p className="text-[11px] text-mute">{t("ctl.hours.note")}</p>
            </>
          )}
          <Btn data-testid="ctl-hours-save" kind="primary" disabled={!isAdmin} onClick={save}>{t("ctl.hours.save")}</Btn>
          {saved && <span data-testid="ctl-hours-saved" className="ml-2 text-[11px] font-semibold text-emerald-700">{t("ctl.hours.saved")}</span>}

          <div className="space-y-1 rounded-xl border border-line bg-panel2 p-3">
            <b>{t("ctl.anomaly.title")}</b>
            <p className="text-mute">{t("ctl.anomaly.help")}</p>
            <ul className="list-disc pl-5 text-mute">{(anomaly.rules ?? []).map((r) => <li key={r}>{t(`ctl.anomaly.rule.${r}`)}</li>)}</ul>
            <label className="flex items-center gap-2 font-semibold">
              <input type="checkbox" data-testid="ctl-anomaly-autofreeze" checked={anomaly.auto_freeze} disabled={!isAdmin} onChange={(e) => saveAnomaly({ disabled: anomaly.disabled, auto_freeze: e.target.checked })} />
              {t("ctl.anomaly.autoFreeze")}
            </label>
            <label className="flex items-center gap-2 font-semibold">
              <input type="checkbox" data-testid="ctl-anomaly-disabled" checked={anomaly.disabled} disabled={!isOwner} onChange={(e) => saveAnomaly({ disabled: e.target.checked, auto_freeze: anomaly.auto_freeze })} />
              {t("ctl.anomaly.disable")}
            </label>
          </div>
          <ErrorLine msg={err} />
        </div>
      </Card>
    </div>
  );
}
