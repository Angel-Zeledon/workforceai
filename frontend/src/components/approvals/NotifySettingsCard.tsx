"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { notifyApi, type NotifyInput, type NotifySettings } from "@/lib/notifyApi";
import { Btn, Card } from "../ui";
import { ErrorLine, inputCls, useViewer } from "../connections/shared";

const emails = (v: string) => v.split(/[\n,;]+/).map((x) => x.trim()).filter(Boolean);

/**
 * Where approvals reach you outside the app (backend/internal/notify): e-mail with signed approve / reject links,
 * and Slack / Microsoft Teams incoming webhooks. Webhook addresses are secrets: they are written here, never shown
 * again (only where they point). Hidden when the server does not offer the endpoint or the viewer cannot decide approvals.
 */
export function NotifySettingsCard() {
  const { t } = useT();
  const { isAdmin } = useViewer();
  const [cfg, setCfg] = useState<NotifySettings | null>(null);
  const [to, setTo] = useState("");
  const [slack, setSlack] = useState("");
  const [teams, setTeams] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const apply = useCallback((s: NotifySettings) => { setCfg(s); setTo(s.email_to.join("\n")); setSlack(""); setTeams(""); }, []);
  useEffect(() => { notifyApi.settings().then(apply).catch(() => setCfg(null)); }, [apply]);
  if (!cfg) return null;

  const save = async (patch: NotifyInput) => {
    setBusy(true); setErr(null); setNote(null);
    try { apply(await notifyApi.save(patch)); setNote(t("notify.saved")); } catch { setErr(t("notify.err")); }
    setBusy(false);
  };
  const submit = () => save({
    email_enabled: cfg.email_enabled, email_to: emails(to), min_risk: cfg.min_risk, links_enabled: cfg.links_enabled, locale: cfg.locale,
    ...(slack.trim() ? { slack_webhook: slack.trim() } : {}), ...(teams.trim() ? { teams_webhook: teams.trim() } : {}),
  });
  const test = async () => {
    setBusy(true); setErr(null); setNote(null);
    try {
      const { sent } = await notifyApi.test();
      const failed = Object.entries(sent).filter(([, v]) => v !== "ok").map(([k]) => k);
      if (!Object.keys(sent).length) setNote(t("notify.test.none"));
      else if (failed.length) setErr(t("notify.test.failed", { where: failed.join(", ") }));
      else setNote(t("notify.test.ok"));
    } catch { setErr(t("notify.err")); }
    setBusy(false);
  };
  const dis = !isAdmin || busy;

  return (
    <div data-testid="notify-card">
      <Card title={t("notify.title")}>
        <div className="space-y-3 text-xs">
          <p className="text-mute">{t("notify.help")}</p>

          <label className="flex items-center gap-2 font-semibold">
            <input type="checkbox" data-testid="notify-email-enabled" checked={cfg.email_enabled} disabled={dis || !cfg.email_available}
              onChange={(e) => setCfg({ ...cfg, email_enabled: e.target.checked })} />
            {t("notify.email.enable")}
          </label>
          {!cfg.email_available && <p className="text-mute">{t("notify.email.unavailable")}</p>}
          <label className="block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("notify.email.to")}
            <textarea data-testid="notify-email-to" rows={2} className={`${inputCls} mt-1 font-mono font-normal normal-case`} value={to} disabled={dis || !cfg.email_available}
              onChange={(e) => setTo(e.target.value)} />
          </label>

          <div className="grid gap-3 sm:grid-cols-2">
            {([["slack", slack, setSlack, cfg.slack_configured, cfg.slack_masked], ["teams", teams, setTeams, cfg.teams_configured, cfg.teams_masked]] as const).map(([k, val, set, on, masked]) => (
              <div key={k} className="block text-[11px] font-semibold uppercase tracking-wide text-mute">
                <label htmlFor={`notify-${k}`}>{t(`notify.${k}`)}</label>
                <input id={`notify-${k}`} data-testid={`notify-${k}`} type="password" autoComplete="off" className={`${inputCls} mt-1 font-mono font-normal normal-case`}
                  value={val} placeholder={t("notify.webhook.ph")} disabled={dis} onChange={(e) => set(e.target.value)} />
                {on && (
                  <span className="mt-1 flex items-center gap-2 font-normal normal-case">
                    <span data-testid={`notify-${k}-set`} className="text-mute">{t("notify.webhook.set", { where: masked ?? "" })}</span>
                    <button type="button" disabled={dis} className="text-err underline" onClick={() => save({ [k === "slack" ? "slack_webhook" : "teams_webhook"]: "" })}>{t("notify.webhook.remove")}</button>
                  </span>
                )}
              </div>
            ))}
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <label className="block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("notify.minRisk")}
              <select data-testid="notify-min-risk" className={`${inputCls} mt-1 font-normal normal-case`} value={cfg.min_risk} disabled={dis}
                onChange={(e) => setCfg({ ...cfg, min_risk: e.target.value as NotifySettings["min_risk"] })}>
                <option value="low">{t("risk.low")}</option>
                <option value="medium">{t("risk.medium")}</option>
                <option value="high">{t("risk.high")}</option>
              </select>
            </label>
            <label className="block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("notify.locale")}
              <select data-testid="notify-locale" className={`${inputCls} mt-1 font-normal normal-case`} value={cfg.locale} disabled={dis}
                onChange={(e) => setCfg({ ...cfg, locale: e.target.value as NotifySettings["locale"] })}>
                <option value="es">Español</option>
                <option value="en">English</option>
                <option value="pt">Português</option>
              </select>
            </label>
          </div>

          <label className="flex items-center gap-2 font-semibold">
            <input type="checkbox" data-testid="notify-links" checked={cfg.links_enabled && cfg.links_available} disabled={dis || !cfg.links_available}
              onChange={(e) => setCfg({ ...cfg, links_enabled: e.target.checked })} />
            {t("notify.links.enable")}
          </label>
          {!cfg.links_available && <p data-testid="notify-links-unavailable" className="text-mute">{t("notify.links.unavailable")}</p>}

          <div className="flex flex-wrap items-center gap-2">
            <Btn kind="primary" disabled={dis} data-testid="notify-save" onClick={submit}>{t("notify.save")}</Btn>
            <Btn disabled={dis} data-testid="notify-test" onClick={test}>{t("notify.test")}</Btn>
            {note && <span data-testid="notify-note" className="text-ok">{note}</span>}
          </div>
          <ErrorLine msg={err} />
        </div>
      </Card>
    </div>
  );
}
