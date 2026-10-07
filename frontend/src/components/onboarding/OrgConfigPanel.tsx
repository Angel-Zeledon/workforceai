"use client";
import { useCallback, useEffect, useState } from "react";
import { fmtDateTime, useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { browserTimeZone, orgApi, type OrgSettings, type Schedule, type TaxTemplate, type ToneCode } from "@/lib/orgConfigApi";
import { Modal } from "@/components/templates/Modal";
import { useAgentName } from "@/components/ui";
import { BriefingFields, type BriefingValue } from "./BriefingFields";
import { ToneSelect } from "./ToneSelect";

const toValue = (s: Schedule): BriefingValue => ({ enabled: s.enabled, hour: s.hour, minute: s.minute, timezone: s.timezone, weekdays: s.weekdays });

/** Team settings after onboarding: regional tone (org and per agent), daily briefing and fiscal-template examples. */
export function OrgConfigPanel({ onClose }: { onClose: () => void }) {
  const { t, locale } = useT();
  const agentName = useAgentName();
  const order = useStore((s) => s.agentOrder);
  const [settings, setSettings] = useState<OrgSettings | null>(null);
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [tax, setTax] = useState<TaxTemplate[]>([]);
  const [draft, setDraft] = useState<BriefingValue>({ enabled: true, hour: 8, minute: 0, timezone: browserTimeZone(), weekdays: [] });
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

  const load = useCallback(async () => {
    try {
      const [s, sc] = await Promise.all([orgApi.settings(), orgApi.schedules()]);
      setSettings(s); setSchedules(sc);
    } catch { setMsg({ ok: false, text: t("cfg.loadError") }); }
    orgApi.taxTemplates(locale).then(setTax).catch(() => setTax([]));
  }, [locale, t]);
  useEffect(() => { void load(); }, [load]);

  const run = async (fn: () => Promise<unknown>) => {
    try { await fn(); setMsg({ ok: true, text: t("cfg.saved") }); await load(); }
    catch { setMsg({ ok: false, text: t("cfg.saveError") }); }
  };
  const briefing = schedules.find((s) => s.template_key === "daily_briefing");
  const form = briefing ? toValue(briefing) : draft;
  const setForm = (v: BriefingValue) => (briefing ? setSchedules(schedules.map((s) => (s.id === briefing.id ? { ...s, ...v } : s))) : setDraft(v));

  return (
    <Modal title={t("cfg.title")} onClose={onClose} testid="cfg-panel" wide>
      {msg && <div role="status" data-testid="cfg-message" className={`mb-3 text-sm font-semibold ${msg.ok ? "text-emerald-700" : "text-red-600"}`}>{msg.text}</div>}

      <section className="mb-6">
        <h3 className="mb-1 font-display text-[15px] font-semibold">{t("cfg.tone.title")}</h3>
        <p className="mb-3 text-[12px] text-mute">{t("onb.style.toneHint")}</p>
        {settings && (
          <div className="grid gap-2">
            <label className="flex items-center justify-between gap-3 text-[13px] font-semibold">
              {t("cfg.tone.org")}
              <ToneSelect value={settings.tone} testid="cfg-tone-org" onChange={(v) => void run(() => orgApi.updateSettings({ tone: (v || "neutral") as ToneCode }))} />
            </label>
            {order.map((id) => (
              <label key={id} className="flex items-center justify-between gap-3 text-[13px]">
                <span>{agentName(id)}</span>
                <ToneSelect allowInherit value={settings.agent_tones[id] ?? ""} testid={`cfg-tone-agent-${id}`} label={agentName(id)}
                  onChange={(v) => void run(() => orgApi.setAgentTone(id, v))} />
              </label>
            ))}
          </div>
        )}
      </section>

      <section className="mb-6">
        <h3 className="mb-1 font-display text-[15px] font-semibold">{t("cfg.brief.title")}</h3>
        {!briefing && <p className="mb-3 text-[12px] text-mute">{t("cfg.brief.none")}</p>}
        <div className="grid gap-3">
          <BriefingFields value={form} onChange={setForm} testid="cfg-brief" />
          {briefing && (
            <label className="flex items-center gap-2 text-sm font-semibold">
              <input type="checkbox" data-testid="cfg-brief-enabled" checked={briefing.enabled}
                onChange={(e) => setSchedules(schedules.map((s) => (s.id === briefing.id ? { ...s, enabled: e.target.checked } : s)))} />
              {t("cfg.brief.enabled")}
            </label>
          )}
          {briefing?.next_run_at && briefing.enabled && <p className="text-[12px] text-mute">{t("cfg.brief.next", { when: fmtDateTime(briefing.next_run_at) })}</p>}
          <div className="flex gap-2">
            {briefing ? (
              <>
                <button type="button" data-testid="cfg-brief-save" className="rounded-md bg-accent px-4 py-1.5 text-xs font-semibold text-white"
                  onClick={() => void run(() => orgApi.updateSchedule(briefing.id, { hour: briefing.hour, minute: briefing.minute, timezone: briefing.timezone, weekdays: briefing.weekdays, enabled: briefing.enabled }))}>{t("cfg.brief.save")}</button>
                <button type="button" data-testid="cfg-brief-delete" className="rounded-md border border-line px-4 py-1.5 text-xs font-semibold text-mute hover:text-ink"
                  onClick={() => void run(() => orgApi.deleteSchedule(briefing.id))}>{t("cfg.brief.delete")}</button>
              </>
            ) : (
              <button type="button" data-testid="cfg-brief-create" className="rounded-md bg-accent px-4 py-1.5 text-xs font-semibold text-white"
                onClick={() => void run(() => orgApi.createSchedule({ hour: draft.hour, minute: draft.minute, timezone: draft.timezone || "UTC", weekdays: draft.weekdays }))}>{t("cfg.brief.create")}</button>
            )}
          </div>
        </div>
      </section>

      <section data-testid="cfg-tax">
        <h3 className="mb-1 flex items-center gap-2 font-display text-[15px] font-semibold">{t("cfg.tax.title")}
          <span className="rounded bg-amber-500/20 px-1.5 py-[2px] text-[10px] font-semibold uppercase tracking-wide text-amber-800">{t("cfg.tax.badge")}</span>
        </h3>
        <div className="grid gap-3 sm:grid-cols-2">
          {tax.map((x) => (
            <div key={x.key} data-testid={`cfg-tax-${x.key}`} className="rounded-xl border border-line bg-bg p-3">
              <div className="text-[13px] font-semibold">{x.country} · {x.name}</div>
              <p className="my-1.5 text-[11px] font-semibold text-amber-800">{x.disclaimer}</p>
              <div className="text-[11px] font-semibold uppercase tracking-wide text-mute">{t("cfg.tax.fields")}</div>
              <ul className="mb-1.5 text-[12px]">{x.fields.map((f) => <li key={f.key}>{f.label}: <span className="text-mute">{f.example}</span></li>)}</ul>
              <div className="text-[11px] font-semibold uppercase tracking-wide text-mute">{t("cfg.tax.checklist")}</div>
              <ul className="list-disc pl-4 text-[12px]">{x.checklist.map((c) => <li key={c.key}>{c.text}</li>)}</ul>
            </div>
          ))}
        </div>
      </section>
    </Modal>
  );
}
