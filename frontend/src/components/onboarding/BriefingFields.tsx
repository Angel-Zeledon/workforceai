"use client";
import { useT } from "@/lib/i18n";

export interface BriefingValue { enabled: boolean; hour: number; minute: number; timezone: string; weekdays: number[] }

const pad = (n: number) => String(n).padStart(2, "0");

/** Time, time zone and weekday picker for the daily briefing (shared by the wizard and the settings panel). */
export function BriefingFields({ value, onChange, testid = "onb-brief" }: { value: BriefingValue; onChange: (v: BriefingValue) => void; testid?: string }) {
  const { t } = useT();
  const toggleDay = (d: number) => {
    const has = value.weekdays.includes(d);
    onChange({ ...value, weekdays: has ? value.weekdays.filter((x) => x !== d) : [...value.weekdays, d].sort() });
  };
  return (
    <div className="grid gap-3">
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="flex flex-col gap-1 text-[12px] font-semibold">
          {t("onb.brief.time")}
          <input type="time" data-testid={`${testid}-time`} value={`${pad(value.hour)}:${pad(value.minute)}`}
            onChange={(e) => {
              const [h, m] = e.target.value.split(":").map(Number);
              if (Number.isFinite(h) && Number.isFinite(m)) onChange({ ...value, hour: h, minute: m });
            }}
            className="rounded-xl border border-line-strong bg-panel px-3 py-2 text-sm font-normal outline-none focus:border-accent" />
        </label>
        <label className="flex flex-col gap-1 text-[12px] font-semibold">
          {t("onb.brief.timezone")}
          <input data-testid={`${testid}-timezone`} value={value.timezone} onChange={(e) => onChange({ ...value, timezone: e.target.value })}
            className="rounded-xl border border-line-strong bg-panel px-3 py-2 text-sm font-normal outline-none focus:border-accent" />
        </label>
      </div>
      <div>
        <div className="mb-1 text-[12px] font-semibold">{t("onb.brief.weekdays")}{value.weekdays.length === 0 && <span className="font-normal text-mute"> ({t("onb.brief.weekdaysAll")})</span>}</div>
        <div className="flex flex-wrap gap-1.5">
          {[1, 2, 3, 4, 5, 6, 0].map((d) => (
            <button key={d} type="button" aria-pressed={value.weekdays.includes(d)} data-testid={`${testid}-day-${d}`} onClick={() => toggleDay(d)}
              className={`rounded-md border px-3 py-1 text-[11px] font-semibold ${value.weekdays.includes(d) ? "border-accent/30 bg-accent-soft text-accent" : "border-line text-mute hover:text-ink"}`}>
              {t(`onb.day.${d}`)}
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
