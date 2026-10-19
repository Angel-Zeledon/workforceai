"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { orgChartApi, type Chart } from "@/lib/orgChartApi";
import { parentOptions, validateDepartment, type Ref } from "@/lib/orgChartLogic";
import { Modal } from "../templates/Modal";
import { RefSelect, allDepts, btnDanger, btnGhost, btnPrimary, inputCls, memberOptions } from "./shared";

/** Create / edit / delete a department: name, parent (never a choice that would form a cycle), head, cost center. */
export function DepartmentEditor({ deptId, chart, onClose, onSaved }: { deptId: string | null; chart: Chart; onClose: () => void; onSaved: () => void }) {
  const { t } = useT();
  const deps = allDepts(chart);
  const cur = deptId ? deps.find((d) => d.id === deptId) : undefined;
  const [name, setName] = useState(cur?.name ?? "");
  const [parent, setParent] = useState(cur?.parent_id ?? "");
  const [head, setHead] = useState<Ref>(cur?.head ?? { kind: "", id: "" });
  const [cc, setCc] = useState(cur?.cost_center ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirmDel, setConfirmDel] = useState(false);

  const lite = deps.map((d) => ({ id: d.id, name: d.name, parent_id: d.parent_id }));
  const issue = validateDepartment({ name, parent_id: parent, cost_center: cc }, lite, deptId ?? undefined);
  const hasChildren = !!cur && cur.children.length > 0;

  const save = async () => {
    if (issue) return;
    setBusy(true); setError(null);
    const body = { name: name.trim(), parent_id: parent, head, cost_center: cc.trim() };
    try {
      if (deptId) await orgChartApi.updateDepartment(deptId, body); else await orgChartApi.createDepartment(body);
      onSaved();
    } catch { setError(t("org.error.save")); } finally { setBusy(false); }
  };
  const del = async () => {
    if (!deptId) return;
    setBusy(true); setError(null);
    try { await orgChartApi.deleteDepartment(deptId); onSaved(); } catch { setError(t("org.dept.deleteError")); setConfirmDel(false); } finally { setBusy(false); }
  };

  return (
    <Modal title={deptId ? t("org.dept.edit") : t("org.dept.new")} onClose={onClose} testid="org-dept-editor">
      <div className="grid gap-4">
        <label className="flex flex-col gap-1 text-[12px] font-semibold">
          {t("org.dept.name")}
          <input data-testid="org-dept-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={120} className={inputCls} autoFocus />
        </label>
        <label className="flex flex-col gap-1 text-[12px] font-semibold">
          {t("org.dept.parent")}
          <select data-testid="org-dept-parent" value={parent} onChange={(e) => setParent(e.target.value)} className={inputCls}>
            <option value="">{t("org.dept.noParent")}</option>
            {parentOptions(lite, deptId ?? undefined).map((o) => <option key={o.id} value={o.id}>{"— ".repeat(o.depth)}{o.name}</option>)}
          </select>
          <span className="text-[11px] font-normal text-mute">{t("org.dept.parentHint")}</span>
        </label>
        <RefSelect label={t("org.dept.head")} value={head} onChange={setHead} options={memberOptions(chart, t)} noneLabel={t("org.noHead")} testid="org-dept-head" />
        <label className="flex flex-col gap-1 text-[12px] font-semibold">
          {t("org.costCenter")}
          <input data-testid="org-dept-cc" value={cc} onChange={(e) => setCc(e.target.value)} placeholder="CC-100" maxLength={32} className={inputCls} />
        </label>
        {issue && (name || cc) && <div role="alert" data-testid="org-dept-issue" className="text-[12px] font-semibold text-red-600">{t(`org.dept.issue.${issue}`)}</div>}
        {error && <div role="alert" data-testid="org-dept-error" className="text-[12px] font-semibold text-red-600">{error}</div>}
        {hasChildren && deptId && <p className="text-[11px] text-mute">{t("org.dept.hasChildren")}</p>}
        <div className="flex items-center justify-between gap-2">
          <div>
            {deptId && !confirmDel && <button type="button" data-testid="org-dept-delete" disabled={busy || hasChildren} onClick={() => setConfirmDel(true)} className={btnDanger}>{t("org.dept.delete")}</button>}
            {deptId && confirmDel && <button type="button" data-testid="org-dept-delete-confirm" disabled={busy} onClick={() => void del()} className={btnDanger}>{t("org.dept.deleteConfirm")}</button>}
          </div>
          <div className="flex gap-2">
            <button type="button" onClick={onClose} className={btnGhost}>{t("org.cancel")}</button>
            <button type="button" data-testid="org-dept-save" disabled={busy || !!issue} onClick={() => void save()} className={btnPrimary}>{busy ? t("org.saving") : t("org.save")}</button>
          </div>
        </div>
      </div>
    </Modal>
  );
}
