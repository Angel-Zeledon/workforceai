"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { goalsApi, type Goal } from "@/lib/goalsApi";
import { linkableGoals } from "@/lib/goalsLogic";

/** Optional "Objetivo" selector (F1). Renders nothing when the company has no active goals (or the list cannot be read). */
export function GoalPicker({ value, onChange, compact = false, testId = "goal-picker" }: { value: string; onChange: (id: string) => void; compact?: boolean; testId?: string }) {
  const { t } = useT();
  const [goals, setGoals] = useState<Goal[]>([]);
  useEffect(() => {
    let alive = true;
    goalsApi.list().then((r) => { if (alive) setGoals(linkableGoals(r.goals ?? [])); }).catch(() => { /* no goals: nothing to show */ });
    return () => { alive = false; };
  }, []);
  if (!goals.length) return null;
  const select = (
    <select data-testid={testId} value={value} onChange={(e) => onChange(e.target.value)} aria-label={t("goals.picker.label")}
      className={compact ? "min-w-0 flex-1 truncate rounded-md border border-line bg-panel px-1.5 py-0.5 text-[11px] text-ink2" : "mt-1 w-full rounded-xl border border-line-strong bg-panel px-3 py-2 text-sm text-ink"}>
      <option value="">{t("goals.picker.none")}</option>
      {goals.map((g) => <option key={g.id} value={g.id}>{g.office_name ? `${g.title} (${g.office_name})` : g.title}</option>)}
    </select>
  );
  if (compact) {
    return (
      <div className="flex items-center gap-2 text-[11px] text-mute">
        <span className="shrink-0 font-medium">{t("goals.picker.label")}</span>
        {select}
      </div>
    );
  }
  return <label className="block text-[11px] font-medium text-mute">{t("goals.picker.label")}{select}<span className="mt-1 block text-[10px] font-normal">{t("goals.picker.hint")}</span></label>;
}
