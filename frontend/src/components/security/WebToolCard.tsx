"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { webApi, type WebMode, type WebSettings } from "@/lib/webApi";
import { Btn, Card } from "../ui";
import { ErrorLine, errText, inputCls, useViewer } from "../connections/shared";

const lines = (v: string) => v.split(/[\n,]+/).map((x) => x.trim()).filter(Boolean);

/** Per-organization switch for the web tool (off by default): open/allowlist, domain lists, limits and today's usage. */
export function WebToolCard() {
  const { t } = useT();
  const { isAdmin } = useViewer();
  const [cfg, setCfg] = useState<WebSettings | null>(null);
  const [allowed, setAllowed] = useState("");
  const [blocked, setBlocked] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const apply = useCallback((s: WebSettings) => {
    setCfg(s); setAllowed(s.allowed_domains.join("\n")); setBlocked(s.blocked_domains.join("\n"));
  }, []);
  const load = useCallback(() => {
    webApi.settings().then(apply).catch(() => setCfg(null)); // older backend or no permission: the card hides
  }, [apply]);
  useEffect(load, [load]);
  if (!cfg) return null;

  const save = async (next: WebSettings) => {
    setErr(null); setSaved(false);
    try {
      apply(await webApi.putSettings({ enabled: next.enabled, mode: next.mode, allowed_domains: lines(allowed), blocked_domains: lines(blocked),
        max_calls_per_task: next.max_calls_per_task, max_calls_per_day: next.max_calls_per_day }));
      setSaved(true);
    } catch (e) { setErr(errText(e)); }
  };
  const num = (v: string) => Math.max(0, Math.floor(Number(v) || 0));

  return (
    <div data-testid="ctl-web">
      <Card title={t("web.title")}>
        <div className="space-y-3 text-xs">
          <p className="text-mute">{t("web.help")}</p>
          {!cfg.search_available && <p data-testid="ctl-web-nosearch" className="rounded-md border border-amber-500/50 bg-amber-50 p-2 text-amber-900">{t("web.noSearch")}</p>}
          {cfg.operator_restricted && <p className="rounded-md border border-line bg-panel2 p-2">{t("web.operator")}</p>}
          <label className="flex items-center gap-2 font-semibold">
            <input type="checkbox" data-testid="ctl-web-enabled" checked={cfg.enabled} disabled={!isAdmin} onChange={(e) => save({ ...cfg, enabled: e.target.checked })} />
            {t("web.enable")}
          </label>
          <label className="block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("web.mode")}
            <select data-testid="ctl-web-mode" className={`${inputCls} mt-1 font-normal normal-case`} value={cfg.mode} disabled={!isAdmin}
              onChange={(e) => setCfg({ ...cfg, mode: e.target.value as WebMode })}>
              <option value="allowlist">{t("web.mode.allowlist")}</option>
              <option value="open">{t("web.mode.open")}</option>
            </select>
          </label>
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("web.allowed")}
              <textarea data-testid="ctl-web-allowed" rows={4} className={`${inputCls} mt-1 font-mono font-normal normal-case`} value={allowed} disabled={!isAdmin}
                onChange={(e) => setAllowed(e.target.value)} />
            </label>
            <label className="block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("web.blocked")}
              <textarea data-testid="ctl-web-blocked" rows={4} className={`${inputCls} mt-1 font-mono font-normal normal-case`} value={blocked} disabled={!isAdmin}
                onChange={(e) => setBlocked(e.target.value)} />
            </label>
          </div>
          <p className="text-[11px] text-mute">{t("web.domainsHint")}</p>
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("web.perTask", { n: cfg.default_calls_per_task })}
              <input type="number" min={0} max={50} data-testid="ctl-web-per-task" className={`${inputCls} mt-1 font-normal normal-case`} value={cfg.max_calls_per_task} disabled={!isAdmin}
                onChange={(e) => setCfg({ ...cfg, max_calls_per_task: num(e.target.value) })} />
            </label>
            <label className="block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("web.perDay", { n: cfg.default_calls_per_day, max: cfg.ceiling_calls_per_day })}
              <input type="number" min={0} max={cfg.ceiling_calls_per_day} data-testid="ctl-web-per-day" className={`${inputCls} mt-1 font-normal normal-case`} value={cfg.max_calls_per_day} disabled={!isAdmin}
                onChange={(e) => setCfg({ ...cfg, max_calls_per_day: num(e.target.value) })} />
            </label>
          </div>
          <p data-testid="ctl-web-usage" className="text-[11px] text-mute">
            {t("web.usage", { searches: cfg.searches_today, fetches: cfg.fetches_today, calls: cfg.calls_today, limit: cfg.day_limit })}
          </p>
          <div className="flex items-center gap-2">
            {isAdmin && <Btn onClick={() => save(cfg)}>{t("web.save")}</Btn>}
            <Btn onClick={load}>{t("web.refresh")}</Btn>
            {saved && <span data-testid="ctl-web-saved" className="text-emerald-700">{t("web.saved")}</span>}
          </div>
          <ErrorLine msg={err} />
        </div>
      </Card>
    </div>
  );
}
