"use client";
import { useT } from "@/lib/i18n";
import { approvalReason } from "@/lib/permissionsLogic";
import type { Approval } from "@/lib/types";

/** "Why it asks you" and "next time" on an approval card: every approval says which rule asked for it and how to avoid
 *  it (docs/architecture/06-permisos-autonomia.md section 11). Nothing shown when the server sent no rule. */
export function ApprovalReason({ a, className = "" }: { a: Pick<Approval, "id" | "policy_rule" | "context">; className?: string }) {
  const { t } = useT();
  const flags = Array.isArray(a.context?.flags) ? (a.context!.flags as unknown[]).map(String) : [];
  const r = approvalReason(a.policy_rule, flags);
  if (!r) return null;
  const cat = r.category ? t(`perm.cat.${r.category}`) : "";
  return (
    <div data-testid={`approval-reason-${a.id}`} className={`mt-1.5 rounded-md bg-panel px-2 py-1.5 text-[11px] leading-snug text-ink2 ${className}`}>
      <div><span className="font-semibold text-ink">{t("perm.why.label")}: </span>{t(r.why, { cat })}</div>
      <div className="text-mute"><span className="font-semibold">{t("perm.avoid.label")}: </span>{t(r.avoid, { cat })}</div>
    </div>
  );
}
