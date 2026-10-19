"use client";
import { useT } from "@/lib/i18n";
import { go } from "@/lib/navBridge";
import { Btn } from "../ui";
import { gov, type RetentionPolicy } from "./governanceApi";
import { Notice, Panel, useLoad } from "./AdminParts";

/** Flat key/value rendering of a policy object: primitives and short lists only (nothing is edited here). */
function Facts({ value }: { value: unknown }) {
  const { t } = useT();
  if (!value || typeof value !== "object") return <p className="text-mute">{t("adm.pol.none")}</p>;
  const rows = Object.entries(value as Record<string, unknown>).filter(([, v]) => v === null || ["string", "number", "boolean"].includes(typeof v) || (Array.isArray(v) && v.every((x) => typeof x === "string" || typeof x === "number")));
  if (!rows.length) return <p className="text-mute">{t("adm.pol.none")}</p>;
  return (
    <dl className="grid grid-cols-1 gap-x-6 gap-y-1 sm:grid-cols-2">
      {rows.map(([k, v]) => (
        <div key={k} className="flex justify-between gap-3 border-b border-line/60 py-1 text-[12.5px]">
          <dt className="truncate text-mute">{k}</dt>
          <dd className="min-w-0 truncate text-right text-ink">{Array.isArray(v) ? v.join(", ") || "-" : String(v)}</dd>
        </div>
      ))}
    </dl>
  );
}

/** Read-only summary of the policies in force. */
export function AdminPolicies() {
  const { t } = useT();
  const { data, err } = useLoad(() => gov.policies());
  const ret = data?.retention as RetentionPolicy | undefined;
  const days = (n: number) => (n > 0 ? t("adm.pol.days", { n }) : t("adm.pol.keep"));
  return (
    <div className="space-y-5" data-testid="adm-policies">
      {err && <Notice kind="err">{err}</Notice>}
      <Panel title={t("adm.pol.title")} body={t("adm.pol.body")}>
        <div><Btn onClick={() => go("/settings?s=security")}>{t("adm.pol.security")}</Btn></div>
      </Panel>
      <Panel title={t("adm.pol.approvals")}><Facts value={data?.approval_rules} /></Panel>
      <Panel title={t("adm.pol.model")}><Facts value={data?.model_policy} /></Panel>
      <Panel title={t("adm.pol.retention")}>
        {ret ? (
          <ul className="space-y-1 text-[12.5px]">
            <li>{ret.enabled ? t("adm.pol.on") : t("adm.pol.off")}</li>
            <li>{t("adm.ret.chats")}: {days(ret.chats_days)}</li>
            <li>{t("adm.ret.files")}: {days(ret.files_days)}</li>
            <li>{t("adm.ret.audit")}: {days(ret.audit_days)}</li>
          </ul>
        ) : <p className="text-mute">{t("adm.pol.none")}</p>}
      </Panel>
    </div>
  );
}
