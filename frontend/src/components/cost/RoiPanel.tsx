"use client";
import { useCallback, useEffect, useMemo, useState } from "react";
import { MOCK } from "@/lib/config";
import { fmtNumber, useT } from "@/lib/i18n";
import { roleMeta } from "@/lib/meta";
import { roiApi } from "@/lib/roiApi";
import { parseRoiForm, type RoiReport, type RoiSettingsView } from "@/lib/roiLogic";
import { useCan } from "@/lib/session";
import { useStore } from "@/lib/store";
import { Btn, Card, Empty, useAgentColor, useAgentName } from "../ui";
import { usd } from "./format";

const WINDOWS = [7, 30, 90] as const;

/** B15 "AI payroll and return": AI cost per agent (measured) next to the ESTIMATED human time of its completed work,
 * with the methodology always visible. Money for the human time only appears with the org's own hourly rate. */
export function RoiPanel() {
  const { t } = useT();
  const name = useAgentName();
  const color = useAgentColor();
  const canEdit = useCan("org:manage");
  const totalCost = useStore((s) => s.metrics?.cost_usd ?? 0);
  const agents = useStore((s) => s.agents);
  const [days, setDays] = useState<number>(30);
  const [rep, setRep] = useState<RoiReport | null>(null);
  const [cfg, setCfg] = useState<RoiSettingsView | null>(null);
  const [failed, setFailed] = useState(false);
  const [editing, setEditing] = useState(false);

  const load = useCallback(async () => {
    try {
      const [r, s] = await Promise.all([roiApi.report(days), roiApi.settings()]);
      setRep(r); setCfg(s); setFailed(false);
    } catch { setFailed(true); }
  }, [days]);
  useEffect(() => { void load(); }, [load, totalCost]);

  if (failed && !rep) return null; // backend without B15: the panel simply does not appear
  const m = rep?.methodology;
  const hasRate = m?.rate_source === "org" && m.hourly_rate_usd != null;
  const hrs = (h: number) => t("roi.hours", { value: fmtNumber(h, 1) });

  return (
    <Card title={<span className="flex items-center gap-2">{t("roi.title")}<span className="rounded bg-amber-500/15 px-1.5 py-[1px] text-[10px] font-semibold uppercase text-amber-800">{t("roi.estimateChip")}</span></span>}
      right={
        <select data-testid="roi-window" value={days} onChange={(e) => setDays(Number(e.target.value))} aria-label={t("roi.window")}
          className="rounded-md border border-line bg-panel2 px-2 py-1 text-[11px] text-ink">
          {WINDOWS.map((d) => <option key={d} value={d}>{t("roi.windowDays", { count: d })}</option>)}
        </select>
      }>
      <div data-testid="roi-panel" className="space-y-3 p-4 text-[12px]">
        {MOCK && <p className="text-[11px] text-mute">{t("roi.demoNote")}</p>}
        {!rep ? <Empty>{t("roi.loading")}</Empty> : (
          <>
            <div className="grid grid-cols-2 gap-2 lg:grid-cols-4">
              <Tile label={t("roi.aiCost")} value={usd(rep.org.ai_cost_usd)} sub={t("roi.measured")} testId="roi-org-cost" />
              <Tile label={t("roi.estHours")} value={hrs(rep.org.est_hours)} sub={t("roi.tasksCounted", { count: rep.org.tasks_counted })} testId="roi-org-hours" accent />
              <Tile label={t("roi.costPerHour")} value={rep.org.ai_cost_per_est_hour_usd != null ? usd(rep.org.ai_cost_per_est_hour_usd) : "—"} sub={t("roi.costPerHourSub")} testId="roi-org-per-hour" />
              {hasRate
                ? <Tile label={t("roi.humanCost")} value={rep.org.est_human_cost_usd != null ? usd(rep.org.est_human_cost_usd) : "—"} sub={t("roi.atRate", { rate: usd(m!.hourly_rate_usd!) })} testId="roi-human-cost" accent />
                : <Tile label={t("roi.humanCost")} value="—" sub={t("roi.noRate")} testId="roi-human-cost" />}
            </div>
            {hasRate && rep.org.est_human_cost_usd != null && (
              <p data-testid="roi-comparison" className="rounded-md bg-panel2 px-3 py-2 text-ink2">
                {t("roi.comparison", { ai: usd(rep.org.ai_cost_usd), human: usd(rep.org.est_human_cost_usd), rate: usd(m!.hourly_rate_usd!) })}
              </p>
            )}
            {!hasRate && <p data-testid="roi-hours-only" className="text-[11px] text-mute">{t(canEdit ? "roi.hoursOnlyAdmin" : "roi.hoursOnly")}</p>}

            {rep.agents.length === 0 ? <Empty>{t("roi.empty")}</Empty> : (
              <div className="overflow-x-auto"><table className="w-full">
                <thead>
                  <tr className="text-left text-[10px] uppercase tracking-wide text-mute">
                    <th className="pb-1 font-semibold">{t("roi.col.agent")}</th>
                    <th className="pb-1 text-right font-semibold">{t("roi.col.counted")}</th>
                    <th className="pb-1 text-right font-semibold">{t("roi.col.excluded")}</th>
                    <th className="pb-1 text-right font-semibold">{t("roi.col.minutes")}</th>
                    <th className="pb-1 text-right font-semibold">{t("roi.col.hours")}</th>
                    <th className="pb-1 text-right font-semibold">{t("roi.col.aiCost")}</th>
                    {hasRate && <th className="pb-1 text-right font-semibold">{t("roi.col.human")}</th>}
                  </tr>
                </thead>
                <tbody>
                  {rep.agents.map((a) => {
                    const ex = a.excluded_rework + a.excluded_failed + a.excluded_skipped;
                    return (
                      <tr key={a.agent_id} data-testid={`roi-row-${a.agent_id}`} className="border-t border-line/60">
                        <td className="py-1.5 pr-2"><span className="mr-1.5 inline-block h-2 w-2 rounded-full" style={{ background: color(a.agent_id) }} /><span className="text-ink">{name(a.agent_id)}</span></td>
                        <td className="py-1.5 text-right font-mono text-ink">{a.tasks_counted}</td>
                        <td className="py-1.5 text-right font-mono text-mute" title={t("roi.excludedTitle", { rework: a.excluded_rework, failed: a.excluded_failed, skipped: a.excluded_skipped })}>{ex}</td>
                        <td className="py-1.5 text-right font-mono text-mute" title={t(`roi.source.${a.minutes_source}`)}>{a.minutes_per_task}{a.minutes_source === "assumption" ? "*" : ""}</td>
                        <td className="py-1.5 text-right font-mono text-ink">{fmtNumber(a.est_hours, 1)}</td>
                        <td className="py-1.5 text-right font-mono text-ink">{usd(a.ai_cost_usd)}</td>
                        {hasRate && <td className="py-1.5 text-right font-mono text-ink">{a.est_human_cost_usd != null ? usd(a.est_human_cost_usd) : "—"}</td>}
                      </tr>
                    );
                  })}
                </tbody>
              </table></div>
            )}
            <Methodology rep={rep} />
          </>
        )}
        {canEdit && cfg && (
          <div className="border-t border-line/60 pt-2">
            <Btn data-testid="roi-settings-toggle" kind="ghost" onClick={() => setEditing((v) => !v)}>{t(editing ? "roi.settings.close" : "roi.settings.open")}</Btn>
            {editing && <RoiSettingsForm cfg={cfg} roles={Array.from(new Set(Object.values(agents).map((a) => a.role.toLowerCase())))} onSaved={() => { setEditing(false); void load(); }} />}
          </div>
        )}
      </div>
    </Card>
  );
}

function Tile({ label, value, sub, testId, accent }: { label: string; value: string; sub: string; testId: string; accent?: boolean }) {
  return (
    <div className={`rounded-xl px-3 py-2 ${accent ? "bg-accent/5" : "bg-panel2"}`}>
      <div className="text-[9px] font-semibold uppercase tracking-wide text-mute">{label}</div>
      <div data-testid={testId} className="font-mono text-lg font-semibold text-ink">{value}</div>
      <div className="text-[10px] text-mute">{sub}</div>
    </div>
  );
}

/** The assumptions, always next to the figures (never behind a click). */
function Methodology({ rep }: { rep: RoiReport }) {
  const { t } = useT();
  const m = rep.methodology;
  const o = rep.org;
  const roles = Object.entries(m.role_minutes);
  return (
    <div data-testid="roi-methodology" className="rounded-md border border-line bg-panel px-3 py-2 text-[11px] text-ink2">
      <div className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-mute">{t("roi.method.title")}</div>
      <ul className="list-disc space-y-0.5 pl-4">
        <li>{t("roi.method.counted", { days: m.window_days })}</li>
        <li>{t("roi.method.excluded", { rework: o.excluded_rework, failed: o.excluded_failed, skipped: o.excluded_skipped })}</li>
        <li data-testid="roi-method-minutes">{t(m.default_minutes_source === "org" ? "roi.method.minutesOrg" : "roi.method.minutesAssumed", { minutes: m.default_minutes })}</li>
        {roles.length > 0 && <li>{t("roi.method.roles", { list: roles.map(([r, n]) => `${roleMeta(r).label}: ${n}`).join(" · ") })}</li>}
        <li>{t("roi.method.formula")}</li>
        <li>{t("roi.method.aiCost")}</li>
        <li data-testid="roi-method-rate">{m.rate_source === "org" && m.hourly_rate_usd != null ? t("roi.method.rateOrg", { rate: usd(m.hourly_rate_usd) }) : t("roi.method.rateUnset")}</li>
      </ul>
      <p className="mt-1 font-semibold text-amber-800">{t("roi.method.notMeasured")}</p>
    </div>
  );
}

function RoiSettingsForm({ cfg, roles, onSaved }: { cfg: RoiSettingsView; roles: string[]; onSaved: () => void }) {
  const { t } = useT();
  const [rate, setRate] = useState(cfg.hourly_rate_usd != null ? String(cfg.hourly_rate_usd) : "");
  const [def, setDef] = useState(String(cfg.saved ? cfg.default_minutes : cfg.default_assumed_minutes));
  const allRoles = useMemo(() => Array.from(new Set([...roles, ...Object.keys(cfg.role_minutes)])).filter(Boolean).sort(), [roles, cfg.role_minutes]);
  const [perRole, setPerRole] = useState<Record<string, string>>(() => Object.fromEntries(allRoles.map((r) => [r, r in cfg.role_minutes ? String(cfg.role_minutes[r]) : ""])));
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const save = async () => {
    const p = parseRoiForm(rate, def, perRole);
    if (!p.ok) { setErr(t(p.error)); return; }
    setBusy(true); setErr("");
    try { await roiApi.save(p.body); onSaved(); } catch { setErr(t("roi.settings.error")); }
    setBusy(false);
  };
  const input = "w-24 rounded-md border border-line-strong bg-panel px-2 py-1 text-right font-mono text-[12px] text-ink outline-none focus:border-accent";
  return (
    <div data-testid="roi-settings" className="mt-2 space-y-2">
      <label className="flex flex-wrap items-center justify-between gap-2">
        <span><span className="font-semibold text-ink">{t("roi.settings.rate")}</span><span className="block text-[10px] text-mute">{t("roi.settings.rateHint")}</span></span>
        <input data-testid="roi-rate-input" inputMode="decimal" value={rate} placeholder={t("roi.settings.ratePlaceholder")} onChange={(e) => setRate(e.target.value)} className={input} />
      </label>
      <label className="flex flex-wrap items-center justify-between gap-2">
        <span><span className="font-semibold text-ink">{t("roi.settings.default")}</span><span className="block text-[10px] text-mute">{t("roi.settings.defaultHint", { max: cfg.max_minutes })}</span></span>
        <input data-testid="roi-default-input" inputMode="numeric" value={def} onChange={(e) => setDef(e.target.value)} className={input} />
      </label>
      {allRoles.length > 0 && (
        <div>
          <div className="text-[10px] font-semibold uppercase tracking-wide text-mute">{t("roi.settings.roles")}</div>
          <div className="mt-1 grid gap-1 sm:grid-cols-2">
            {allRoles.map((r) => (
              <label key={r} className="flex items-center justify-between gap-2 rounded-md bg-panel2 px-2 py-1">
                <span className="truncate text-ink">{roleMeta(r).label}</span>
                <input data-testid={`roi-role-input-${r}`} inputMode="numeric" value={perRole[r] ?? ""} placeholder={def} aria-label={t("roi.settings.roleFor", { role: roleMeta(r).label })}
                  onChange={(e) => setPerRole((s) => ({ ...s, [r]: e.target.value }))} className={input} />
              </label>
            ))}
          </div>
          <p className="mt-1 text-[10px] text-mute">{t("roi.settings.rolesHint")}</p>
        </div>
      )}
      {err && <div role="alert" className="text-red-700">{err}</div>}
      <Btn data-testid="roi-save" kind="primary" disabled={busy} onClick={save}>{t("roi.settings.save")}</Btn>
    </div>
  );
}
