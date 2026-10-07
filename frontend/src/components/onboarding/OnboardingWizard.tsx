"use client";
import { useEffect, useState } from "react";
import { fmtUsd, useT } from "@/lib/i18n";
import { trOr, type Locale } from "@/lib/i18n-core";
import { browserTimeZone, orgApi, type OnboardResult, type OnboardingPack, type ToneCode } from "@/lib/orgConfigApi";
import { Modal } from "@/components/templates/Modal";
import { BriefingFields, type BriefingValue } from "./BriefingFields";
import { ToneSelect } from "./ToneSelect";

const STEPS = 3;

/** Short first-run wizard: business type -> language and tone -> daily briefing. Applies POST /onboarding. */
export function OnboardingWizard({ onClose, onDone }: { onClose: () => void; onDone: (r: OnboardResult) => void }) {
  const { t, locale: uiLocale } = useT();
  const [step, setStep] = useState(0);
  const [packs, setPacks] = useState<OnboardingPack[] | null>(null);
  const [loadError, setLoadError] = useState(false);
  const [packKey, setPackKey] = useState("");
  const [locale, setLocale] = useState<Locale>(uiLocale);
  const [tone, setTone] = useState<ToneCode>("neutral");
  const [brief, setBrief] = useState<BriefingValue>({ enabled: true, hour: 8, minute: 0, timezone: browserTimeZone(), weekdays: [] });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<OnboardResult | null>(null);

  useEffect(() => {
    let alive = true;
    orgApi.packs(uiLocale).then((p) => { if (alive) setPacks(p); }).catch(() => { if (alive) setLoadError(true); });
    return () => { alive = false; };
  }, [uiLocale]);

  const pack = packs?.find((p) => p.key === packKey);
  const pickPack = (p: OnboardingPack) => {
    setPackKey(p.key);
    setBrief((b) => ({ ...b, hour: p.briefing.hour, minute: p.briefing.minute }));
  };

  const finish = async () => {
    setBusy(true); setError(null);
    try {
      const r = await orgApi.onboard({
        pack_key: packKey, locale, tone,
        briefing: { enabled: brief.enabled, hour: brief.hour, minute: brief.minute, timezone: brief.timezone || "UTC", weekdays: brief.weekdays },
      });
      setResult(r);
      onDone(r);
    } catch (e) {
      setError(String((e as Error).message).includes("409") ? t("onb.conflict") : t("onb.error"));
    } finally { setBusy(false); }
  };

  const title = t("onb.wizard.title");
  if (result) {
    return (
      <Modal title={t("onb.done.title")} onClose={onClose} testid="onb-wizard">
        <p data-testid="onb-done" className="mb-4 text-sm">{t("onb.done.body", { agents: result.agents_configured.length, memory: result.memory_seeded })}</p>
        {result.schedule && <p className="mb-4 text-sm text-mute">{t("onb.done.briefing")}</p>}
        <div className="flex justify-end"><button type="button" data-testid="onb-done-close" onClick={onClose} className="rounded-md bg-accent px-5 py-2 text-xs font-semibold text-white">{t("common.close")}</button></div>
      </Modal>
    );
  }

  return (
    <Modal title={title} onClose={onClose} testid="onb-wizard">
      <div className="mb-4 flex items-center justify-between text-[11px] font-semibold uppercase tracking-wide text-mute">
        <span data-testid="onb-step" data-step={step + 1}>{t("onb.step.of", { step: step + 1, total: STEPS })}</span>
        <span>{t(["onb.step.pack", "onb.step.style", "onb.step.briefing"][step])}</span>
      </div>

      {step === 0 && (
        <div>
          <h3 className="mb-1 font-display text-[15px] font-semibold">{t("onb.pack.title")}</h3>
          <p className="mb-3 text-sm text-mute">{t("onb.pack.hint")}</p>
          {loadError && <div role="alert" className="text-sm font-semibold text-red-600">{t("onb.loadError")}</div>}
          {!loadError && !packs && <div className="text-sm text-mute">{t("tpl.loading")}</div>}
          <div role="radiogroup" aria-label={t("onb.pack.title")} className="grid gap-2">
            {(packs ?? []).map((p) => (
              <button key={p.key} type="button" role="radio" aria-checked={packKey === p.key} data-testid={`onb-pack-${p.key}`} onClick={() => pickPack(p)}
                className={`rounded-xl border p-3 text-left transition ${packKey === p.key ? "border-accent bg-accent/10" : "border-line bg-bg hover:border-accent/50"}`}>
                <div className="text-[14px] font-semibold">{trOr(p.name_key, p.name)}</div>
                <div className="mb-1.5 text-[12px] text-mute">{trOr(p.description_key, p.description)}</div>
                <div className="flex flex-wrap gap-x-3 gap-y-0.5 text-[11px] font-semibold text-mute">
                  <span>{t("onb.pack.agents", { count: p.agents.length })}</span>
                  <span>{t("onb.pack.memory", { count: p.memory.length })}</span>
                  <span>{t("onb.pack.approval", { amount: fmtUsd(p.rules.approval_amount_usd) })}</span>
                  <span>{t("onb.pack.templates", { count: p.templates.length })}</span>
                </div>
              </button>
            ))}
          </div>
        </div>
      )}

      {step === 1 && (
        <div className="grid gap-4">
          <h3 className="font-display text-[15px] font-semibold">{t("onb.style.title")}</h3>
          <label className="flex flex-col gap-1 text-[12px] font-semibold">
            {t("onb.style.language")}
            <select data-testid="onb-locale" value={locale} onChange={(e) => setLocale(e.target.value as Locale)}
              className="rounded-xl border border-line-strong bg-panel px-3 py-2 text-sm font-semibold outline-none focus:border-accent">
              <option value="es">{t("lang.es")}</option>
              <option value="en">{t("lang.en")}</option>
            </select>
          </label>
          <label className="flex flex-col gap-1 text-[12px] font-semibold">
            {t("onb.style.tone")}
            <ToneSelect value={tone} onChange={(v) => setTone((v || "neutral") as ToneCode)} testid="onb-tone" />
            <span className="text-[11px] font-normal text-mute">{t("onb.style.toneHint")}</span>
          </label>
        </div>
      )}

      {step === 2 && (
        <div className="grid gap-4">
          <h3 className="font-display text-[15px] font-semibold">{t("onb.brief.title")}</h3>
          <p className="text-sm text-mute">{t("onb.brief.hint")}</p>
          <label className="flex items-center gap-2 text-sm font-semibold">
            <input type="checkbox" data-testid="onb-brief-enabled" checked={brief.enabled} onChange={(e) => setBrief({ ...brief, enabled: e.target.checked })} />
            {t("onb.brief.enable")}
          </label>
          {brief.enabled && <BriefingFields value={brief} onChange={setBrief} />}
          {pack && <p className="text-[11px] text-mute">{t("onb.pack.recommendedHint", { count: pack.templates.length })}</p>}
        </div>
      )}

      {error && <div role="alert" data-testid="onb-error" className="mt-3 text-sm font-semibold text-red-600">{error}</div>}

      <div className="mt-6 flex items-center justify-between">
        <button type="button" data-testid="onb-back" disabled={step === 0 || busy} onClick={() => setStep(step - 1)}
          className="rounded-md border border-line px-4 py-2 text-xs font-semibold text-mute hover:text-ink disabled:opacity-40">{t("onb.back")}</button>
        {step < STEPS - 1 ? (
          <button type="button" data-testid="onb-next" disabled={step === 0 && !packKey} onClick={() => setStep(step + 1)}
            className="rounded-md bg-accent px-5 py-2 text-xs font-semibold text-white disabled:opacity-50">{t("onb.next")}</button>
        ) : (
          <button type="button" data-testid="onb-finish" disabled={busy || !packKey} onClick={() => void finish()}
            className="rounded-md bg-accent px-5 py-2 text-xs font-semibold text-white disabled:opacity-50">{busy ? t("onb.finishing") : t("onb.finish")}</button>
        )}
      </div>
    </Modal>
  );
}
