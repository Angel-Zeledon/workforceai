"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { Btn } from "../ui";
import { download, gov, type AuditFilters, type AuditRecord, type ChainReport } from "./governanceApi";
import { Field, inputCls, Notice, Panel, tdCls, thCls, useErrText, when } from "./AdminParts";

/** Audit trail: filtered preview, chain verification and export (CSV streamed; JSON and ZIP carry the verification). */
export function AdminAudit() {
  const { t, locale } = useT();
  const errText = useErrText();
  const [f, setF] = useState<AuditFilters>({});
  const [items, setItems] = useState<AuditRecord[] | null>(null);
  const [chain, setChain] = useState<ChainReport | null>(null);
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);
  const set = (k: keyof AuditFilters) => (e: React.ChangeEvent<HTMLInputElement>) => setF((p) => ({ ...p, [k]: e.target.value }));
  const guard = async (fn: () => Promise<void>) => { setMsg(null); try { await fn(); } catch (e) { setMsg({ kind: "err", text: errText(e) }); } };
  const preview = () => guard(async () => setItems((await gov.auditPage(f)).items));
  const verify = () => guard(async () => setChain(await gov.auditVerify()));
  const exp = (format: "csv" | "json" | "zip") => guard(async () => {
    const h = await download(gov.auditExportPath(f, format), `audit.${format}`);
    const ok = h.get("X-Audit-Chain-OK");
    if (ok === "false") setMsg({ kind: "err", text: t("adm.aud.exportedBad") });
    else if (ok === "true") setMsg({ kind: "ok", text: t("adm.aud.exportedOk") });
  });
  return (
    <Panel testid="adm-audit" title={t("adm.aud.title")} body={t("adm.aud.body")}>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Field label={t("adm.aud.actor")}><input className={inputCls} value={f.actor ?? ""} onChange={set("actor")} /></Field>
        <Field label={t("adm.aud.type")}><input className={inputCls} value={f.type ?? ""} onChange={set("type")} /></Field>
        <Field label={t("adm.rep.from")}><input className={inputCls} type="date" value={f.from ?? ""} onChange={set("from")} /></Field>
        <Field label={t("adm.rep.to")}><input className={inputCls} type="date" value={f.to ?? ""} onChange={set("to")} /></Field>
      </div>
      <div className="flex flex-wrap gap-2">
        <Btn onClick={preview}>{t("adm.aud.preview")}</Btn>
        <Btn onClick={verify}>{t("adm.aud.verify")}</Btn>
        <Btn kind="primary" onClick={() => exp("csv")}>{t("adm.aud.csv")}</Btn>
        <Btn kind="primary" onClick={() => exp("json")}>{t("adm.aud.json")}</Btn>
        <Btn kind="primary" onClick={() => exp("zip")}>{t("adm.aud.zip")}</Btn>
      </div>
      {msg && <Notice kind={msg.kind}>{msg.text}</Notice>}
      {chain && (chain.ok
        ? <Notice kind="ok">{t("adm.aud.ok", { n: chain.checked, seq: chain.head_seq })}</Notice>
        : <Notice kind="err">{t("adm.aud.broken", { seq: chain.broken_at_seq ?? 0, reason: chain.reason ?? "" })}</Notice>)}
      {items && (items.length === 0 ? <p className="text-mute">{t("adm.aud.none")}</p> : (
        <div className="overflow-x-auto rounded-lg border border-line">
          <table className="w-full min-w-[560px] border-collapse">
            <thead className="bg-panel2"><tr><th className={thCls}>{t("adm.aud.col.time")}</th><th className={thCls}>{t("adm.aud.actor")}</th><th className={thCls}>{t("adm.aud.col.action")}</th><th className={thCls}>{t("adm.aud.col.entity")}</th></tr></thead>
            <tbody>{items.map((r) => (
              <tr key={r.id} className="border-t border-line"><td className={`${tdCls} whitespace-nowrap`}>{when(r.ts, locale)}</td><td className={tdCls}>{r.actor}</td><td className={tdCls}>{r.action}</td><td className={tdCls}>{r.entity}</td></tr>
            ))}</tbody>
          </table>
        </div>
      ))}
    </Panel>
  );
}
