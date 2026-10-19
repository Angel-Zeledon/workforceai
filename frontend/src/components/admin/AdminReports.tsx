"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { Btn } from "../ui";
import { download, gov, type UsageReport } from "./governanceApi";
import { Field, inputCls, Notice, Panel, tdCls, thCls, useErrText, usd } from "./AdminParts";

const DIMS = ["agent", "project", "department"] as const;
type Dim = (typeof DIMS)[number];

/** Usage and cost by agent, project or department, with CSV export. */
export function AdminReports() {
  const { t } = useT();
  const errText = useErrText();
  const [by, setBy] = useState<Dim>("agent");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [rep, setRep] = useState<UsageReport | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const run = async () => {
    setBusy(true); setErr(null);
    try { setRep(await gov.usage(by, from || undefined, to || undefined)); } catch (e) { setErr(errText(e)); } finally { setBusy(false); }
  };
  const csv = async () => {
    setErr(null);
    try { await download(gov.usageCsvPath(by, from || undefined, to || undefined), `usage-by-${by}.csv`); } catch (e) { setErr(errText(e)); }
  };
  const label = (key: string, name: string) => (key === "" ? t("adm.rep.none") : name || key);
  return (
    <Panel testid="adm-reports" title={t("adm.rep.title")} body={t("adm.rep.body")}>
      <div className="flex flex-wrap items-end gap-3">
        <Field label={t("adm.rep.by")}>
          <select data-testid="adm-rep-by" className={inputCls} value={by} onChange={(e) => setBy(e.target.value as Dim)}>
            <option value="agent">{t("adm.rep.by.agent")}</option>
            <option value="project">{t("adm.rep.by.project")}</option>
            <option value="department">{t("adm.rep.by.department")}</option>
          </select>
        </Field>
        <Field label={t("adm.rep.from")}><input className={inputCls} type="date" value={from} onChange={(e) => setFrom(e.target.value)} /></Field>
        <Field label={t("adm.rep.to")}><input className={inputCls} type="date" value={to} onChange={(e) => setTo(e.target.value)} /></Field>
        <Btn kind="primary" disabled={busy} onClick={run}>{t("adm.rep.run")}</Btn>
        <Btn onClick={csv}>{t("adm.rep.csv")}</Btn>
      </div>
      {err && <Notice kind="err">{err}</Notice>}
      {rep && (rep.rows.length === 0 ? <p className="text-mute">{t("adm.rep.empty")}</p> : (
        <div className="overflow-x-auto rounded-lg border border-line">
          <table data-testid="adm-rep-table" className="w-full min-w-[640px] border-collapse">
            <thead className="bg-panel2"><tr>
              <th className={thCls}>{t("adm.rep.col.name")}</th><th className={thCls}>{t("adm.rep.col.requests")}</th><th className={thCls}>{t("adm.rep.col.calls")}</th>
              <th className={thCls}>{t("adm.rep.col.tokens")}</th><th className={thCls}>{t("adm.rep.col.cost")}</th><th className={thCls}>{t("adm.rep.col.share")}</th>
            </tr></thead>
            <tbody>
              {rep.rows.map((r) => (
                <tr key={r.key || "_"} className="border-t border-line">
                  <td className={tdCls}>{label(r.key, r.label)}{r.department && rep.by === "agent" && <span className="block text-[11px] text-mute">{r.department}</span>}</td>
                  <td className={tdCls}>{r.requests}</td><td className={tdCls}>{r.calls}</td>
                  <td className={tdCls}>{r.input_tokens.toLocaleString()} / {r.output_tokens.toLocaleString()}</td>
                  <td className={tdCls}>{usd(r.cost_usd)}</td><td className={tdCls}>{r.share_pct}%</td>
                </tr>
              ))}
              <tr className="border-t-2 border-line-strong bg-panel2 font-semibold">
                <td className={tdCls}>{t("adm.rep.total")}</td><td className={tdCls}>{rep.total.requests}</td><td className={tdCls}>{rep.total.calls}</td>
                <td className={tdCls}>{rep.total.input_tokens.toLocaleString()} / {rep.total.output_tokens.toLocaleString()}</td><td className={tdCls}>{usd(rep.total.cost_usd)}</td><td className={tdCls}>100%</td>
              </tr>
            </tbody>
          </table>
        </div>
      ))}
    </Panel>
  );
}
