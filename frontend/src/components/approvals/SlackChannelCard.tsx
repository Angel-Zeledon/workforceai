"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { slackApi, statusOf, type SlackInput, type SlackLink, type SlackSettings } from "@/lib/slackApi";
import { Btn, Card } from "../ui";
import { ErrorLine, inputCls } from "../connections/shared";

const label = "block text-[11px] font-semibold uppercase tracking-wide text-mute";

/** A read-only value with a copy button (the request URLs the admin pastes into the Slack app). */
function CopyField({ value, testId }: { value: string; testId: string }) {
  const { t } = useT();
  const [done, setDone] = useState(false);
  const copy = async () => {
    try { await navigator.clipboard.writeText(value); setDone(true); setTimeout(() => setDone(false), 1500); } catch { /* clipboard blocked: the field is selectable */ }
  };
  return (
    <span className="mt-1 flex items-center gap-2">
      <input readOnly data-testid={testId} value={value} className={`${inputCls} font-mono text-xs normal-case`} onFocus={(e) => e.currentTarget.select()} />
      <Btn onClick={copy}>{done ? t("slack.copied") : t("slack.copy")}</Btn>
    </span>
  );
}

/**
 * Two-way Slack (backend/internal/slackapp): the organization connects its own Slack app (signing secret + bot token,
 * stored sealed, never shown again), approval requests arrive with Approve / Reject buttons and people query the office
 * with a slash command. Admins see the setup; every member can link their own Slack account with a one-time code.
 * Hidden when the server does not offer the channel.
 */
export function SlackChannelCard() {
  const { t } = useT();
  const [cfg, setCfg] = useState<SlackSettings | null>(null);
  const [links, setLinks] = useState<SlackLink[] | null>(null);
  const [signing, setSigning] = useState("");
  const [token, setToken] = useState("");
  const [code, setCode] = useState<{ code: string; command: string; expires_at: string } | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const apply = useCallback((s: SlackSettings) => { setCfg(s); setSigning(""); setToken(""); }, []);
  const loadLinks = useCallback(() => slackApi.myLink().then((r) => setLinks(r.links)).catch(() => setLinks(null)), []);
  useEffect(() => {
    // admins only (org:manage). {available:false}: vendor integrations are off in this deployment (v1) -> no card.
    slackApi.settings().then((s) => ((s as { available?: boolean }).available === false ? setCfg(null) : apply(s))).catch(() => setCfg(null));
    loadLinks();
  }, [apply, loadLinks]);
  if (!cfg && !links) return null;

  const run = async (fn: () => Promise<void>) => {
    setBusy(true); setErr(null); setNote(null);
    try { await fn(); } catch (e) {
      const st = statusOf(e);
      setErr(st === 409 ? t(cfg && !cfg.vault_available ? "slack.err.vault" : "slack.err.notConfigured") : st === 400 ? t("slack.err.rejected") : t("slack.err.generic"));
    }
    setBusy(false);
  };
  const save = (patch: SlackInput) => run(async () => { apply(await slackApi.save(patch)); setNote(t("slack.saved")); });
  const submit = () => cfg && save({
    channel_id: cfg.channel_id, min_risk: cfg.min_risk, locale: cfg.locale, email_matching: cfg.email_matching, high_risk_in_app: cfg.high_risk_in_app,
    ...(signing.trim() ? { signing_secret: signing.trim() } : {}), ...(token.trim() ? { bot_token: token.trim() } : {}),
  });
  const test = () => run(async () => {
    const r = await slackApi.test();
    if (!r.ok) setErr(t("slack.test.failed", { code: r.error ?? "?" }));
    else if (r.error) setErr(t("slack.test.postFailed", { code: r.error }));
    else setNote(r.posted ? t("slack.test.posted") : t("slack.test.ok", { team: r.team_name ?? r.team_id ?? "" }));
    apply(await slackApi.settings());
  });
  const disconnect = () => { if (window.confirm(t("slack.disconnect.confirm"))) run(async () => { await slackApi.disconnect(); apply(await slackApi.settings()); setNote(t("slack.disconnected")); }); };
  const toggle = (enabled: boolean) => save({ enabled });
  const getCode = () => run(async () => setCode(await slackApi.linkCode()));
  const unlink = () => run(async () => { await slackApi.unlink(); setCode(null); await loadLinks(); setNote(t("slack.link.removed")); });

  return (
    <div data-testid="slack-card">
      <Card title={t("slack.title")}>
        <div className="space-y-3 text-xs">
          <p className="text-mute">{t("slack.help")}</p>

          {cfg && (
            <div className="space-y-3" data-testid="slack-admin">
              <p data-testid="slack-status" className={cfg.configured ? "text-ok" : "text-mute"}>
                {cfg.configured ? t(cfg.enabled ? "slack.status.on" : "slack.status.off", { team: cfg.team_name || cfg.team_id || "" }) : t("slack.status.none")}
              </p>
              {!cfg.vault_available && <p data-testid="slack-no-vault" className="text-err">{t("slack.noVault")}</p>}
              {!cfg.accounts_available && <p data-testid="slack-no-accounts" className="text-mute">{t("slack.noAccounts")}</p>}

              <ol className="list-decimal space-y-1 pl-4 text-mute">
                <li>{t("slack.step.create")}</li>
                <li>{t("slack.step.scopes", { scopes: cfg.scopes.join(", ") })}</li>
                <li>{t("slack.step.interactivity")}<CopyField value={cfg.interactions_url} testId="slack-interactions-url" /></li>
                <li>{t("slack.step.command", { command: cfg.command })}<CopyField value={cfg.commands_url} testId="slack-commands-url" /></li>
                <li>{t("slack.step.install")}</li>
                <li>{t("slack.step.channel")}</li>
              </ol>

              <div className="grid gap-3 sm:grid-cols-2">
                <label className={label}>{t("slack.signing")}
                  <input data-testid="slack-signing" type="password" autoComplete="off" className={`${inputCls} mt-1 font-mono font-normal normal-case`} value={signing}
                    placeholder={cfg.signing_secret_set ? t("slack.secret.set") : t("slack.secret.ph")} disabled={busy} onChange={(e) => setSigning(e.target.value)} />
                </label>
                <label className={label}>{t("slack.token")}
                  <input data-testid="slack-token" type="password" autoComplete="off" className={`${inputCls} mt-1 font-mono font-normal normal-case`} value={token}
                    placeholder={cfg.bot_token_set ? t("slack.token.set", { last4: cfg.bot_token_last4 ?? "" }) : "xoxb-…"} disabled={busy} onChange={(e) => setToken(e.target.value)} />
                </label>
                <label className={label}>{t("slack.channel")}
                  <input data-testid="slack-channel" className={`${inputCls} mt-1 font-mono font-normal normal-case`} value={cfg.channel_id} placeholder="C0123456789"
                    disabled={busy} onChange={(e) => setCfg({ ...cfg, channel_id: e.target.value })} />
                </label>
                <label className={label}>{t("slack.minRisk")}
                  <select data-testid="slack-min-risk" className={`${inputCls} mt-1 font-normal normal-case`} value={cfg.min_risk} disabled={busy}
                    onChange={(e) => setCfg({ ...cfg, min_risk: e.target.value as SlackSettings["min_risk"] })}>
                    <option value="low">{t("risk.low")}</option>
                    <option value="medium">{t("risk.medium")}</option>
                    <option value="high">{t("risk.high")}</option>
                  </select>
                </label>
                <label className={label}>{t("slack.locale")}
                  <select data-testid="slack-locale" className={`${inputCls} mt-1 font-normal normal-case`} value={cfg.locale} disabled={busy}
                    onChange={(e) => setCfg({ ...cfg, locale: e.target.value as SlackSettings["locale"] })}>
                    <option value="es">Español</option>
                    <option value="en">English</option>
                    <option value="pt">Português</option>
                  </select>
                </label>
              </div>

              <label className="flex items-start gap-2">
                <input type="checkbox" data-testid="slack-email-matching" checked={cfg.email_matching} disabled={busy}
                  onChange={(e) => setCfg({ ...cfg, email_matching: e.target.checked })} />
                <span><span className="font-semibold">{t("slack.emailMatching")}</span> <span className="text-mute">{t("slack.emailMatching.help")}</span></span>
              </label>
              <label className="flex items-start gap-2">
                <input type="checkbox" data-testid="slack-high-risk" checked={cfg.high_risk_in_app} disabled={busy}
                  onChange={(e) => setCfg({ ...cfg, high_risk_in_app: e.target.checked })} />
                <span><span className="font-semibold">{t("slack.highRisk")}</span> <span className="text-mute">{t("slack.highRisk.help")}</span></span>
              </label>
              <p className="text-mute">{t("slack.rules")}</p>

              <div className="flex flex-wrap items-center gap-2">
                <Btn kind="primary" disabled={busy} data-testid="slack-save" onClick={submit}>{t("slack.save")}</Btn>
                <Btn disabled={busy || !cfg.bot_token_set} data-testid="slack-test" onClick={test}>{t("slack.test")}</Btn>
                {cfg.configured && (
                  <Btn kind={cfg.enabled ? "danger" : "ok"} disabled={busy} data-testid="slack-enabled" onClick={() => toggle(!cfg.enabled)}>
                    {cfg.enabled ? t("slack.turnOff") : t("slack.turnOn")}
                  </Btn>
                )}
                {(cfg.signing_secret_set || cfg.bot_token_set) && (
                  <Btn kind="danger" disabled={busy} data-testid="slack-disconnect" onClick={disconnect}>{t("slack.disconnect")}</Btn>
                )}
              </div>
            </div>
          )}

          {links && (
            <div className="space-y-2 border-t border-line pt-3" data-testid="slack-link">
              <p className="font-semibold">{t("slack.link.title")}</p>
              {links.length > 0 ? (
                <p data-testid="slack-linked" className="flex flex-wrap items-center gap-2">
                  <span className="text-ok">{t("slack.link.linked", { user: links.map((l) => l.slack_user_id).join(", ") })}</span>
                  <Btn kind="danger" disabled={busy} data-testid="slack-unlink" onClick={unlink}>{t("slack.link.unlink")}</Btn>
                </p>
              ) : (
                <p className="text-mute">{t("slack.link.help")}</p>
              )}
              <Btn disabled={busy} data-testid="slack-link-code" onClick={getCode}>{t("slack.link.getCode")}</Btn>
              {code && (
                <div data-testid="slack-code" className="space-y-1">
                  <p>{t("slack.link.type")}</p>
                  <CopyField value={code.command} testId="slack-code-command" />
                  <p className="text-mute">{t("slack.link.expires", { time: new Date(code.expires_at).toLocaleTimeString() })}</p>
                </div>
              )}
            </div>
          )}

          <div className="flex flex-wrap items-center gap-2">
            {note && <span data-testid="slack-note" className="text-ok">{note}</span>}
          </div>
          <ErrorLine msg={err} />
        </div>
      </Card>
    </div>
  );
}
