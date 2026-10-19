"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import { fmtUsd, useT } from "@/lib/i18n";
import { orgChartApi, type ApplyResponse, type Industry } from "@/lib/orgChartApi";
import { flattenDepartments } from "@/lib/orgChartLogic";
import { Modal } from "./Modal";

const DAY_KEYS = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"];

function IndustryDetail({ ind, onClose }: { ind: Industry; onClose: () => void }) {
  const { t, locale } = useT();
  const [projects, setProjects] = useState(true);
  const [demo, setDemo] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  const [res, setRes] = useState<ApplyResponse | null>(null);
  const agentName = (k: string) => (k === "owner" ? t("org.owner") : ind.agents.find((a) => a.key === k)?.name ?? k);
  const flat = flattenDepartments(ind.departments.map((d) => ({ id: d.key, name: d.name, parent_id: d.parent })));
  const dept = (k: string) => ind.departments.find((d) => d.key === k);

  const apply = async () => {
    setBusy(true); setError(false);
    try { setRes(await orgChartApi.applyIndustry(ind.key, { locale, skip_projects: !projects, demo_data: demo })); } catch { setError(true); } finally { setBusy(false); }
  };
  const sel = (s: string) => (s.startsWith("agent:") ? agentName(s.slice(6)) : s.startsWith("dept:") ? dept(s.slice(5))?.name ?? s : s === "owner" ? t("org.owner") : t(`org.sel.${s}`));

  return (
    <Modal title={ind.name} onClose={onClose} testid="industry-detail" wide>
      <p className="mb-2 text-sm">{ind.description}</p>
      <p className="mb-4 rounded-lg bg-amber-500/10 px-3 py-2 text-[12px] text-amber-800" data-testid="industry-disclaimer">{ind.disclaimer}</p>

      <h3 className="mb-1 text-[12px] font-semibold uppercase tracking-wide text-mute">{t("org.ind.departments")}</h3>
      <ul className="mb-4 text-[13px]">
        {flat.map((f) => <li key={f.id} style={{ paddingLeft: f.depth * 16 }}>{f.name} <span className="text-[11px] text-mute">{dept(f.id)?.cost_center}</span></li>)}
      </ul>

      <h3 className="mb-1 text-[12px] font-semibold uppercase tracking-wide text-mute">{t("org.ind.team")}</h3>
      <ul className="mb-4 grid gap-2 sm:grid-cols-2">
        {ind.agents.map((a) => (
          <li key={a.key} className="rounded-xl border border-line bg-bg p-3 text-[12px]">
            <div className="text-[13px] font-semibold">{a.name} <span className="font-normal text-mute">· {a.title}</span></div>
            <div className="text-mute">{dept(a.department)?.name}</div>
            <ul className="my-1.5 list-disc pl-4">{a.responsibilities.slice(0, 3).map((r) => <li key={r}>{r}</li>)}</ul>
            <div className="text-mute">
              {t("org.ind.schedule", { days: a.schedule.days.map((d) => t(`org.day.${DAY_KEYS[d]}`)).join(" "), start: a.schedule.start, end: a.schedule.end, zone: a.schedule.timezone })}
            </div>
            <div className="text-mute">{t("org.budget.perMonth", { amount: fmtUsd(a.budget_usd) })} · {t("org.ind.escalatesTo", { name: agentName(a.escalation) })}</div>
          </li>
        ))}
      </ul>

      <h3 className="mb-1 text-[12px] font-semibold uppercase tracking-wide text-mute">{t("org.ind.projects")}</h3>
      <ul className="mb-4 grid gap-2">
        {ind.projects.map((p) => (
          <li key={p.key} className="rounded-xl border border-line bg-bg p-3 text-[12px]">
            <div className="text-[13px] font-semibold">{p.name}</div>
            <ol className="mt-1 list-decimal pl-4 text-mute">{p.steps.map((s) => <li key={s.key}>{s.title} <span className="opacity-70">({agentName(s.agent)})</span></li>)}</ol>
          </li>
        ))}
      </ul>

      {ind.delegation.length > 0 && (
        <>
          <h3 className="mb-1 text-[12px] font-semibold uppercase tracking-wide text-mute">{t("org.ind.rules")}</h3>
          <ul className="mb-4 list-disc pl-5 text-[12px]">
            {ind.delegation.map((r, i) => <li key={i}>{r.note || t(r.effect === "deny" ? "org.rule.deny" : r.approval ? "org.rule.approval" : "org.rule.allow", { from: sel(r.from), to: sel(r.to) })}</li>)}
          </ul>
        </>
      )}

      {res ? (
        <div role="status" data-testid="industry-result" className="rounded-xl border border-emerald-400/50 bg-emerald-500/10 p-3 text-sm">
          <p className="font-semibold">{res.result.already_applied && res.result.agents.length === 0 ? t("org.ind.alreadyApplied") : t("org.ind.applied")}</p>
          <p className="text-[12px]">{t("org.ind.resultCounts", { departments: res.result.departments.length, agents: res.result.agents.length, existing: res.result.agents_existing.length, projects: res.result.project_templates.length, rules: res.result.rules })}</p>
          {res.demo_files.length > 0 && <p className="text-[12px]">{t("org.ind.demoFiles", { count: res.demo_files.length })}</p>}
          {res.demo_error && <p className="text-[12px] text-red-600">{t("org.ind.demoError")}</p>}
          <Link href="/org" prefetch={false} data-testid="industry-open-org" className="mt-2 inline-block text-[12px] font-semibold text-accent hover:underline">{t("org.ind.openOrg")}</Link>
        </div>
      ) : (
        <div className="grid gap-2 border-t border-line pt-3">
          <label className="flex items-center gap-2 text-[13px] font-semibold"><input type="checkbox" data-testid="industry-opt-projects" checked={projects} onChange={(e) => setProjects(e.target.checked)} />{t("org.ind.optProjects")}</label>
          <label className="flex items-center gap-2 text-[13px] font-semibold"><input type="checkbox" data-testid="industry-opt-demo" checked={demo} onChange={(e) => setDemo(e.target.checked)} />{t("org.ind.optDemo")}</label>
          <p className="text-[11px] text-mute">{t("org.ind.applyNote")}</p>
          {error && <div role="alert" data-testid="industry-error" className="text-sm font-semibold text-red-600">{t("org.ind.applyError")}</div>}
          <div className="flex justify-end"><button type="button" data-testid="industry-apply" disabled={busy} onClick={() => void apply()} className="rounded-md bg-accent px-5 py-2 text-xs font-semibold text-white disabled:opacity-50">{busy ? t("org.saving") : t("org.ind.apply")}</button></div>
        </div>
      )}
    </Modal>
  );
}

/** Industry templates: a whole company skeleton (departments, team with job descriptions, projects) applied in one click. */
export function IndustryTemplates() {
  const { t, locale } = useT();
  const [items, setItems] = useState<Industry[] | null>(null);
  const [error, setError] = useState(false);
  const [selected, setSelected] = useState<Industry | null>(null);

  useEffect(() => {
    let alive = true;
    setItems(null); setError(false);
    orgChartApi.industries(locale).then((r) => { if (alive) setItems(r); }).catch(() => { if (alive) setError(true); });
    return () => { alive = false; };
  }, [locale]);

  return (
    <div data-testid="industry-templates">
      <p className="mb-4 text-sm text-mute">{t("org.ind.subtitle")}</p>
      {error && <div role="alert" className="text-sm font-semibold text-red-600">{t("org.error.load")}</div>}
      {!error && items === null && <div className="text-sm text-mute">{t("org.loading")}</div>}
      <ul className="grid gap-3 sm:grid-cols-2">
        {(items ?? []).map((ind) => (
          <li key={ind.key} data-testid={`industry-card-${ind.key}`} className="flex flex-col rounded-xl border border-line bg-bg p-4">
            <div className="mb-1 flex items-start justify-between gap-2">
              <h3 className="font-display text-[15px] font-semibold leading-tight">{ind.name}</h3>
              {ind.subsidiaries > 0 && <span className="shrink-0 rounded bg-accent/15 px-1.5 py-[2px] text-[10px] font-semibold uppercase tracking-wide text-accent">{t("org.ind.subsidiaries", { count: ind.subsidiaries })}</span>}
            </div>
            <p className="mb-3 flex-1 text-[12px] leading-snug text-mute">{ind.description}</p>
            <div className="mb-3 flex flex-wrap gap-2 text-[11px] font-semibold text-mute">
              <span>{t("org.ind.nDepartments", { count: ind.departments.length })}</span>
              <span>{t("org.ind.nAgents", { count: ind.agents.length })}</span>
              <span>{t("org.ind.nProjects", { count: ind.projects.length })}</span>
            </div>
            <p className="mb-3 text-[11px] italic text-mute">{ind.disclaimer}</p>
            <button type="button" data-testid={`industry-open-${ind.key}`} onClick={() => setSelected(ind)} className="self-start rounded-md bg-accent px-4 py-1.5 text-xs font-semibold text-white">{t("org.ind.view")}</button>
          </li>
        ))}
      </ul>
      {selected && <IndustryDetail ind={selected} onClose={() => setSelected(null)} />}
    </div>
  );
}
