"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { Btn } from "../ui";
import { gov, type AgentSpend } from "./governanceApi";
import { Field, inputCls, Notice, Panel, tdCls, thCls, useErrText, useLoad, usd } from "./AdminParts";

function AgentRow({ a, onDone, onError }: { a: AgentSpend; onDone: () => void; onError: (e: unknown) => void }) {
  const { t } = useT();
  const [dept, setDept] = useState(a.department);
  const [cap, setCap] = useState(a.cap_usd > 0 ? String(a.cap_usd) : "");
  const dirty = dept.trim() !== a.department || Number(cap || 0) !== a.cap_usd;
  const save = async () => {
    try {
      if (dept.trim() !== a.department) await gov.setDepartment(a.id, dept.trim());
      if (Number(cap || 0) !== a.cap_usd) await gov.setAgentCap(a.id, Number(cap || 0));
      onDone();
    } catch (e) { onError(e); }
  };
  return (
    <tr data-testid="adm-agent-row" className="border-t border-line">
      <td className={tdCls}><b>{a.name}</b><span className="block text-[11px] text-mute">{a.role}</span></td>
      <td className={tdCls}><input aria-label={t("adm.spend.department")} className={inputCls} maxLength={60} value={dept} onChange={(e) => setDept(e.target.value)} /></td>
      <td className={`${tdCls} whitespace-nowrap`}>{usd(a.month_spend_usd)}</td>
      <td className={tdCls}><input aria-label={t("adm.spend.cap")} className={inputCls} type="number" min={0} step="0.5" placeholder={t("adm.spend.noCap")} value={cap} onChange={(e) => setCap(e.target.value)} /></td>
      <td className={tdCls}><Btn kind="primary" disabled={!dirty} onClick={save}>{t("adm.spend.save")}</Btn></td>
    </tr>
  );
}

/** Spend of the month by agent and department, department tags and spend limits. */
export function AdminSpend() {
  const { t } = useT();
  const errText = useErrText();
  const { data, err, reload } = useLoad(() => gov.overview());
  const [msg, setMsg] = useState<string | null>(null);
  const [bad, setBad] = useState<string | null>(null);
  const [newName, setNewName] = useState("");
  const [newLimit, setNewLimit] = useState("");
  const fail = (e: unknown) => { setMsg(null); setBad(errText(e)); };
  const done = () => { setBad(null); setMsg(t("adm.saved")); reload(); };
  const saveLimit = async (department: string, value: string) => {
    try { await gov.setDepartmentLimit(department, Number(value || 0)); done(); } catch (e) { fail(e); }
  };
  return (
    <div className="space-y-5" data-testid="adm-spend">
      {(err || bad) && <Notice kind="err">{err || bad}</Notice>}
      {msg && <Notice kind="ok">{msg}</Notice>}
      <Panel title={t("adm.spend.deptTitle")} body={t("adm.spend.enforceNote")}>
        <ul className="grid gap-3 md:grid-cols-2" data-testid="adm-depts">
          {(data?.departments ?? []).map((d) => (
            <DeptCard key={d.name} name={d.name} agents={d.agents} spend={d.month_spend_usd} limit={d.limit_usd} pct={d.used_pct} over={d.over} onSave={(v) => saveLimit(d.name, v)} />
          ))}
        </ul>
        <div className="flex flex-wrap items-end gap-3 border-t border-line pt-3">
          <Field label={t("adm.spend.deptName")}><input className={inputCls} maxLength={60} value={newName} onChange={(e) => setNewName(e.target.value)} /></Field>
          <Field label={t("adm.spend.cap")}><input className={inputCls} type="number" min={0} step="1" value={newLimit} onChange={(e) => setNewLimit(e.target.value)} /></Field>
          <Btn kind="primary" disabled={!newName.trim() || !(Number(newLimit) > 0)} onClick={() => { saveLimit(newName.trim(), newLimit); setNewName(""); setNewLimit(""); }}>{t("adm.spend.newDept")}</Btn>
        </div>
      </Panel>
      <Panel title={t("adm.spend.title")} body={t("adm.spend.body")}>
        <div className="overflow-x-auto rounded-lg border border-line">
          <table className="w-full min-w-[640px] border-collapse">
            <thead className="bg-panel2"><tr>
              <th className={thCls}>{t("adm.spend.agent")}</th><th className={thCls}>{t("adm.spend.department")}</th>
              <th className={thCls}>{t("adm.spend.spent")}</th><th className={thCls}>{t("adm.spend.cap")}</th><th className={thCls} />
            </tr></thead>
            <tbody>{(data?.agents ?? []).map((a) => <AgentRow key={`${a.id}:${a.department}:${a.cap_usd}`} a={a} onDone={done} onError={fail} />)}</tbody>
          </table>
        </div>
      </Panel>
    </div>
  );
}

function DeptCard({ name, agents, spend, limit, pct, over, onSave }: { name: string; agents: number; spend: number; limit: number; pct: number; over: boolean; onSave: (v: string) => void }) {
  const { t } = useT();
  const [v, setV] = useState(limit > 0 ? String(limit) : "");
  return (
    <li data-testid="adm-dept" data-over={over} className={`rounded-lg border p-3 ${over ? "border-err/40 bg-err/5" : "border-line bg-panel2/40"}`}>
      <div className="flex items-baseline justify-between gap-2">
        <b className="truncate text-[13px] text-ink">{name || t("adm.spend.unassigned")}</b>
        <span className="text-[11px] text-mute">{t("adm.spend.agentsN", { n: agents })}</span>
      </div>
      <div className="mt-1 text-[12.5px] text-ink">{usd(spend)}{limit > 0 && <span className="text-mute"> / {usd(limit)}</span>}</div>
      {limit > 0 && (
        <div className="mt-1.5" aria-hidden>
          <div className="h-1.5 overflow-hidden rounded-full bg-line"><div className={`h-full ${over ? "bg-err" : "bg-accent"}`} style={{ width: `${Math.min(100, pct)}%` }} /></div>
        </div>
      )}
      {limit > 0 && <div className={`mt-1 text-[11px] ${over ? "font-semibold text-err" : "text-mute"}`}>{over ? t("adm.spend.over") : t("adm.spend.used", { pct })}</div>}
      {name && (
        <div className="mt-2 flex items-center gap-2">
          <input aria-label={t("adm.spend.cap")} className={inputCls} type="number" min={0} step="1" placeholder={t("adm.spend.noCap")} value={v} onChange={(e) => setV(e.target.value)} />
          <Btn disabled={Number(v || 0) === limit} onClick={() => onSave(v)}>{t("adm.spend.save")}</Btn>
        </div>
      )}
    </li>
  );
}
