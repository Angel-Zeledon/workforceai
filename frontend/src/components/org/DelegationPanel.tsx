"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { orgChartApi, type Chart, type Decision, type Rule } from "@/lib/orgChartApi";
import { SELECTOR_NEEDS_ID, ruleIssue, type Ref, type SelectorLite, type SelectorType } from "@/lib/orgChartLogic";
import { RefSelect, allDepts, allMembers, btnGhost, btnPrimary, inputCls, memberOptions, refOf } from "./shared";

const TYPES: SelectorType[] = ["any", "any_person", "any_agent", "person", "agent", "department"];

function SelectorPicker({ value, onChange, chart, testid, label }: { value: SelectorLite; onChange: (v: SelectorLite) => void; chart: Chart; testid: string; label: string }) {
  const { t } = useT();
  const members = allMembers(chart);
  const opts = value.type === "department" ? allDepts(chart).map((d) => ({ id: d.id, name: d.name }))
    : members.filter((m) => m.kind === value.type).map((m) => ({ id: m.id, name: m.name }));
  return (
    <fieldset className="flex flex-col gap-1">
      <legend className="mb-1 text-[12px] font-semibold">{label}</legend>
      <select aria-label={label} data-testid={`${testid}-type`} value={value.type} onChange={(e) => onChange({ type: e.target.value as SelectorType, id: "" })} className={inputCls}>
        {TYPES.map((x) => <option key={x} value={x}>{t(`org.sel.${x}`)}</option>)}
      </select>
      {SELECTOR_NEEDS_ID.includes(value.type) && (
        <select aria-label={t(`org.sel.${value.type}`)} data-testid={`${testid}-id`} value={value.id} onChange={(e) => onChange({ ...value, id: e.target.value })} className={inputCls}>
          <option value="">{t("org.sel.choose")}</option>
          {opts.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}
        </select>
      )}
    </fieldset>
  );
}

/** Delegation rules (who may assign work to whom, who approves) and a checker for a given pair. */
export function DelegationPanel({ chart }: { chart: Chart }) {
  const { t } = useT();
  const [rules, setRules] = useState<Rule[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [from, setFrom] = useState<SelectorLite>({ type: "any_agent", id: "" });
  const [to, setTo] = useState<SelectorLite>({ type: "any_person", id: "" });
  const [effect, setEffect] = useState<"allow" | "deny">("allow");
  const [approval, setApproval] = useState(false);
  const [approver, setApprover] = useState<Ref>({ kind: "", id: "" });
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [cFrom, setCFrom] = useState<Ref>({ kind: "", id: "" });
  const [cTo, setCTo] = useState<Ref>({ kind: "", id: "" });
  const [decision, setDecision] = useState<Decision | null>(null);

  const load = useCallback(() => { orgChartApi.rules().then(setRules).catch(() => setError(t("org.error.load"))); }, [t]);
  useEffect(load, [load]);

  const nameOfSel = (s: SelectorLite) => {
    if (s.type === "department") return allDepts(chart).find((d) => d.id === s.id)?.name ?? s.id;
    if (s.type === "person" || s.type === "agent") return allMembers(chart).find((m) => m.kind === s.type && m.id === s.id)?.name ?? s.id;
    return t(`org.sel.${s.type}`);
  };
  const nameOfRef = (r: Ref) => allMembers(chart).find((m) => m.kind === r.kind && m.id === r.id)?.name ?? (r.id === "owner" ? t("org.owner") : r.id);
  const sentence = (r: Rule) => {
    const base = t(r.effect === "deny" ? "org.rule.deny" : r.require_approval ? "org.rule.approval" : "org.rule.allow", { from: nameOfSel(r.from), to: nameOfSel(r.to) });
    return r.require_approval && r.approver.kind ? `${base} ${t("org.rule.approver", { name: nameOfRef(r.approver) })}` : base;
  };

  const issue = ruleIssue({ from, to, effect, require_approval: approval });
  const create = async () => {
    if (issue) return;
    setBusy(true); setError(null);
    try {
      await orgChartApi.createRule({ from, to, effect, require_approval: effect === "allow" && approval, approver: effect === "allow" && approval ? approver : { kind: "", id: "" }, note: note.trim() });
      setNote(""); setApproval(false); load();
    } catch { setError(t("org.error.save")); } finally { setBusy(false); }
  };
  const remove = async (id: string) => { try { await orgChartApi.deleteRule(id); load(); } catch { setError(t("org.error.save")); } };
  const check = async () => {
    setDecision(null); setError(null);
    try { setDecision(await orgChartApi.check(cFrom, cTo)); } catch { setError(t("org.error.save")); }
  };
  const opts = memberOptions(chart, t);

  return (
    <div className="grid gap-6" data-testid="org-delegation">
      <p className="text-[13px] text-mute">{t("org.delegation.intro")}</p>
      {error && <div role="alert" className="text-sm font-semibold text-red-600">{error}</div>}

      <section aria-label={t("org.delegation.rules")}>
        <h3 className="mb-2 font-display text-[14px] font-semibold">{t("org.delegation.rules")}</h3>
        {rules === null && <div className="text-sm text-mute">{t("org.loading")}</div>}
        {rules && rules.length === 0 && <div data-testid="org-rules-empty" className="rounded-xl border border-dashed border-line-strong p-4 text-[13px] text-mute">{t("org.delegation.none")}</div>}
        <ul className="grid gap-2">
          {(rules ?? []).map((r) => (
            <li key={r.id} data-testid={`org-rule-${r.id}`} className="flex items-start justify-between gap-3 rounded-xl border border-line bg-panel px-3 py-2">
              <div className="min-w-0">
                <span className={`mr-2 rounded px-1.5 py-[1px] text-[10px] font-semibold uppercase ${r.effect === "deny" ? "bg-red-500/15 text-red-700" : r.require_approval ? "bg-amber-500/15 text-amber-700" : "bg-emerald-500/15 text-emerald-700"}`}>
                  {r.effect === "deny" ? t("org.effect.deny") : r.require_approval ? t("org.effect.approval") : t("org.effect.allow")}
                </span>
                <span className="text-[13px]">{sentence(r)}</span>
                {r.note && <div className="text-[11px] text-mute">{r.note}</div>}
              </div>
              <button type="button" data-testid={`org-rule-delete-${r.id}`} onClick={() => void remove(r.id)} className="shrink-0 text-[11px] font-semibold text-red-600 hover:underline">{t("org.delete")}</button>
            </li>
          ))}
        </ul>
      </section>

      <section aria-label={t("org.delegation.add")} className="rounded-xl border border-line p-4">
        <h3 className="mb-3 font-display text-[14px] font-semibold">{t("org.delegation.add")}</h3>
        <div className="grid gap-4 sm:grid-cols-2">
          <SelectorPicker value={from} onChange={setFrom} chart={chart} testid="org-rule-from" label={t("org.rule.from")} />
          <SelectorPicker value={to} onChange={setTo} chart={chart} testid="org-rule-to" label={t("org.rule.to")} />
        </div>
        <div className="mt-3 flex flex-wrap items-center gap-4 text-[12px] font-semibold">
          <label className="flex items-center gap-1.5"><input type="radio" name="effect" data-testid="org-rule-allow" checked={effect === "allow"} onChange={() => setEffect("allow")} />{t("org.effect.allow")}</label>
          <label className="flex items-center gap-1.5"><input type="radio" name="effect" data-testid="org-rule-deny" checked={effect === "deny"} onChange={() => { setEffect("deny"); setApproval(false); }} />{t("org.effect.deny")}</label>
          {effect === "allow" && <label className="flex items-center gap-1.5"><input type="checkbox" data-testid="org-rule-approval" checked={approval} onChange={(e) => setApproval(e.target.checked)} />{t("org.rule.requireApproval")}</label>}
        </div>
        {effect === "allow" && approval && (
          <div className="mt-3 sm:max-w-sm"><RefSelect label={t("org.rule.approverLabel")} value={approver} onChange={setApprover} options={opts} noneLabel={t("org.rule.approverDefault")} testid="org-rule-approver" /></div>
        )}
        <label className="mt-3 flex flex-col gap-1 text-[12px] font-semibold">{t("org.rule.note")}
          <input data-testid="org-rule-note" value={note} maxLength={200} onChange={(e) => setNote(e.target.value)} className={inputCls} /></label>
        {issue && <div role="alert" data-testid="org-rule-issue" className="mt-2 text-[12px] font-semibold text-red-600">{t(`org.rule.issue.${issue}`)}</div>}
        <div className="mt-3 flex justify-end"><button type="button" data-testid="org-rule-create" disabled={busy || !!issue} onClick={() => void create()} className={btnPrimary}>{t("org.delegation.create")}</button></div>
      </section>

      <section aria-label={t("org.check.title")} className="rounded-xl border border-line p-4">
        <h3 className="mb-1 font-display text-[14px] font-semibold">{t("org.check.title")}</h3>
        <p className="mb-3 text-[12px] text-mute">{t("org.check.hint")}</p>
        <div className="grid gap-3 sm:grid-cols-2">
          <RefSelect label={t("org.check.from")} value={cFrom} onChange={setCFrom} options={opts} noneLabel={t("org.sel.choose")} testid="org-check-from" />
          <RefSelect label={t("org.check.to")} value={cTo} onChange={setCTo} options={opts} noneLabel={t("org.sel.choose")} testid="org-check-to" />
        </div>
        <div className="mt-3 flex items-center gap-3">
          <button type="button" data-testid="org-check-run" disabled={!cFrom.kind || !cTo.kind} onClick={() => void check()} className={btnGhost}>{t("org.check.run")}</button>
          {decision && (
            <span data-testid="org-check-result" role="status" className="text-[13px] font-semibold">
              {!decision.allowed ? t("org.check.denied") : decision.needs_approval ? t("org.check.needsApproval", { name: decision.approver.kind ? nameOfRef(decision.approver) : t("org.owner") }) : t("org.check.allowed")}
              <span className="ml-2 text-[11px] font-normal text-mute">({t(`org.check.reason.${decision.reason}`)})</span>
            </span>
          )}
        </div>
      </section>
    </div>
  );
}
