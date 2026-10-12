"use client";
import { useEffect, useState } from "react";
import { fmtDateTime, useT } from "@/lib/i18n";
import { errorDetail, httpStatus, projectsApi } from "@/lib/projects/api";
import { ISSUE_CODES, issuesFromError, limitFromDetail } from "@/lib/projects/templateLogic";
import type { ProjectTemplate, TemplateIssue, TemplateParam, TemplateVersionInfo } from "@/lib/projects/types";
import { Btn } from "../ui";
import { Modal } from "./shared";

export const inputCls = "w-full rounded-lg border border-line-strong bg-panel px-2.5 py-1.5 text-xs text-ink outline-none focus:border-accent disabled:opacity-50";
export const labelCls = "block text-[11px] font-semibold text-mute";

/** texts of a template in the current language (built-ins use i18n keys, organization templates literals) */
export function useTplText() {
  const { t } = useT();
  const defaults = (tpl: ProjectTemplate) => Object.fromEntries(tpl.params.map((p) => [p.key, p.default || (p.default_key ? t(p.default_key) : "") || "…"]));
  return {
    name: (tpl: ProjectTemplate) => (tpl.name_key ? t(tpl.name_key, defaults(tpl)) : tpl.name),
    description: (tpl: ProjectTemplate) => (tpl.description_key ? t(tpl.description_key) : tpl.description),
    paramLabel: (p: TemplateParam) => p.label || (p.label_key ? t(p.label_key) : p.key),
    paramDefault: (p: TemplateParam) => p.default || (p.default_key ? t(p.default_key) : ""),
  };
}

/** translated message of a validation / limit code (unknown codes are shown as they come) */
export function useIssueText() {
  const { t } = useT();
  return (code: string, limit?: number) => {
    if ((ISSUE_CODES as readonly string[]).includes(code)) return t(`tpl.issue.${code}`, { limit: limit ?? "" });
    return code;
  };
}

/** issues carried by a failed call: "invalid template: ..." codes, or a limit */
export function issuesOfError(err: unknown): { issues: TemplateIssue[]; limit?: { code: string; limit?: number } } {
  const d = errorDetail(err);
  return { issues: issuesFromError(d), limit: limitFromDetail(d) ?? undefined };
}
export { httpStatus };

export const fmtCost = (n: number) => `$${n < 1 ? n.toFixed(3) : n.toFixed(2)}`;

/** Versions of an organization template with restore. */
export function TemplateHistoryModal({ templateId, onClose, onRestored }: { templateId: string; onClose: () => void; onRestored: (tpl: ProjectTemplate) => void }) {
  const { t } = useT();
  const [versions, setVersions] = useState<TemplateVersionInfo[] | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState<number | null>(null);
  useEffect(() => {
    let alive = true;
    projectsApi.templateVersions(templateId).then((v) => { if (alive) setVersions(v); }).catch(() => { if (alive) { setVersions([]); setErr(t("tpl.hist.error")); } });
    return () => { alive = false; };
  }, [templateId, t]);
  const restore = async (n: number) => {
    if (busy !== null) return;
    if (!window.confirm(t("tpl.hist.restoreConfirm", { version: n }))) return;
    setBusy(n); setErr("");
    try { onRestored(await projectsApi.restoreTemplate(templateId, n)); onClose(); } catch { setErr(t("tpl.hist.restoreError")); setBusy(null); }
  };
  const latest = versions?.[0]?.version;
  return (
    <Modal onClose={onClose} testId="tpl-history-modal" wide>
      <h2 className="font-display text-lg font-semibold text-ink">{t("tpl.hist.title")}</h2>
      <p className="mt-0.5 text-[11px] text-mute">{t("tpl.hist.subtitle")}</p>
      <div className="mt-3 overflow-hidden rounded-xl border border-line">
        {versions === null && <div className="p-4 text-xs text-mute">{t("tpl.lib.loading")}</div>}
        {versions?.length === 0 && !err && <div className="p-4 text-xs text-mute">{t("tpl.hist.empty")}</div>}
        {versions?.map((v) => (
          <div key={v.version} data-testid={`tpl-version-${v.version}`} className="flex items-center gap-3 border-b border-line px-3 py-2.5 last:border-b-0">
            <span className="w-10 shrink-0 font-mono text-xs font-semibold text-ink">v{v.version}</span>
            <div className="min-w-0 flex-1">
              <div className="truncate text-xs text-ink">{v.note || v.name}</div>
              <div className="text-[10px] text-mute">{t("tpl.hist.by", { who: v.saved_by || "—", when: fmtDateTime(v.saved_at) })}</div>
            </div>
            {v.version === latest
              ? <span className="rounded-full bg-accent-soft px-2 py-0.5 text-[10px] font-semibold text-accent">{t("tpl.hist.current")}</span>
              : <Btn data-testid={`tb-restore-${v.version}`} disabled={busy !== null} onClick={() => restore(v.version)}>{busy === v.version ? t("tpl.hist.restoring") : t("tpl.hist.restore")}</Btn>}
          </div>
        ))}
      </div>
      {err && <div className="mt-2 text-[11px] text-red-700">{err}</div>}
      <div className="mt-4 flex justify-end"><Btn onClick={onClose}>{t("common.close")}</Btn></div>
    </Modal>
  );
}
