"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import type { Locale } from "@/lib/i18n-core";
import { orgChartApi, type Industry } from "@/lib/orgChartApi";

/** Optional "company template" picker of the onboarding wizard: departments, a starting team and sample projects. "" = none. */
export function IndustryField({ value, onChange, locale }: { value: string; onChange: (key: string) => void; locale: Locale }) {
  const { t } = useT();
  const [items, setItems] = useState<Industry[] | null>(null);
  useEffect(() => {
    let alive = true;
    orgChartApi.industries(locale).then((r) => { if (alive) setItems(r); }).catch(() => { if (alive) setItems([]); });
    return () => { alive = false; };
  }, [locale]);
  if (items && items.length === 0) return null; // the backend has no organization model: nothing to offer
  const picked = items?.find((i) => i.key === value);
  return (
    <div className="grid gap-1.5" data-testid="onb-industry">
      <label className="flex flex-col gap-1 text-[12px] font-semibold">
        {t("org.onb.industry")}
        <select data-testid="onb-industry-select" value={value} onChange={(e) => onChange(e.target.value)} disabled={!items}
          className="rounded-xl border border-line-strong bg-panel px-3 py-2 text-sm font-semibold outline-none focus:border-accent">
          <option value="">{t("org.onb.industryNone")}</option>
          {(items ?? []).map((i) => <option key={i.key} value={i.key}>{i.name}</option>)}
        </select>
      </label>
      <span className="text-[11px] text-mute">{picked ? t("org.onb.industryPicked", { departments: picked.departments.length, agents: picked.agents.length, projects: picked.projects.length }) : t("org.onb.industryHint")}</span>
      {picked && <span className="text-[11px] italic text-mute">{picked.disclaimer}</span>}
    </div>
  );
}
