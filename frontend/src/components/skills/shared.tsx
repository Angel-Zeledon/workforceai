"use client";
import { useT } from "@/lib/i18n";
import type { SkillSource, SkillStatus } from "@/lib/skills";
import type { SkillIssue } from "@/lib/skillsLogic";

const STATUS_CLS: Record<SkillStatus, string> = {
  active: "bg-ok/10 text-ok", in_review: "bg-warn/15 text-warn", draft: "bg-mute/10 text-mute", disabled: "bg-err/10 text-err",
};
export function StatusBadge({ status, testId }: { status: SkillStatus; testId?: string }) {
  const { t } = useT();
  return <span data-testid={testId} data-status={status} className={`inline-flex shrink-0 items-center rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${STATUS_CLS[status]}`}>{t(`skills.status.${status}`)}</span>;
}

export function SourceChip({ source, builtin }: { source: SkillSource; builtin?: boolean }) {
  const { t } = useT();
  return <span className="rounded border border-line px-1.5 py-0.5 text-[10px] text-mute">{builtin ? t("skills.source.builtin") : t(`skills.source.${source}`)}</span>;
}

export function issueText(t: (k: string, v?: Record<string, string | number>) => string, i: SkillIssue): string {
  switch (i.code) {
    case "file_path": return t(`skills.issue.file_path.${i.why}`, { path: i.path });
    case "file_duplicate": case "file_empty": case "file_too_large": return t(`skills.issue.${i.code}`, { path: i.path });
    case "description_too_long": case "too_many_files": case "too_many_tools": return t(`skills.issue.${i.code}`, { max: i.max });
    default: return t(`skills.issue.${i.code}`);
  }
}

export const fmtSize = (n: number) => (n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(1)} KB` : `${(n / (1 << 20)).toFixed(1)} MB`);
export const label = "mb-1 block text-[10px] font-semibold uppercase tracking-wide text-mute";
export const field = "w-full rounded-lg border border-line bg-panel2 px-3 py-2 text-[13px] text-ink outline-none focus:border-accent";
