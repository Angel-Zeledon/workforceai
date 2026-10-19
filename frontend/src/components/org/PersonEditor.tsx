"use client";
import { useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import { orgChartApi, type Chart } from "@/lib/orgChartApi";
import { bossCandidates, refKey, type Ref } from "@/lib/orgChartLogic";
import { Modal } from "../templates/Modal";
import { RefSelect, allDepts, allMembers, btnGhost, btnPrimary, inputCls, memberOptions, refOf } from "./shared";

/** Places a person in the chart: department, title and boss. */
export function PersonEditor({ userId, chart, onClose, onSaved }: { userId: string; chart: Chart; onClose: () => void; onSaved: () => void }) {
  const { t } = useT();
  const members = useMemo(() => allMembers(chart), [chart]);
  const me = members.find((m) => m.kind === "person" && m.id === userId);
  const [dept, setDept] = useState(me?.dept_id ?? "");
  const [title, setTitle] = useState(me?.title ?? "");
  const [boss, setBoss] = useState<Ref>(me?.reports_to ?? { kind: "", id: "" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const self: Ref = { kind: "person", id: userId };
  const edges: Record<string, string> = {};
  for (const m of members) if (m.reports_to && m.reports_to.kind) edges[refKey(refOf(m))] = refKey(m.reports_to);
  const ok = new Set(bossCandidates(edges, refKey(self), members.map((m) => refKey(refOf(m)))));
  const options = memberOptions(chart, t, self).filter((o) => ok.has(refKey(o.ref)));

  const save = async () => {
    setBusy(true); setError(null);
    try { await orgChartApi.placePerson(userId, { department_id: dept, title: title.trim(), reports_to: boss }); onSaved(); } catch { setError(t("org.error.save")); } finally { setBusy(false); }
  };
  return (
    <Modal title={t("org.person.title", { name: me?.name ?? userId })} onClose={onClose} testid="org-person-editor">
      <div className="grid gap-4">
        <label className="flex flex-col gap-1 text-[12px] font-semibold">{t("org.role.department")}
          <select data-testid="org-person-dept" value={dept} onChange={(e) => setDept(e.target.value)} className={inputCls}>
            <option value="">{t("org.unassigned")}</option>
            {allDepts(chart).map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-[12px] font-semibold">{t("org.person.jobTitle")}
          <input data-testid="org-person-title" value={title} onChange={(e) => setTitle(e.target.value)} maxLength={80} className={inputCls} />
        </label>
        <RefSelect label={t("org.role.reportsTo")} value={boss} onChange={setBoss} options={options} noneLabel={t("org.none")} testid="org-person-boss" />
        {error && <div role="alert" className="text-[12px] font-semibold text-red-600">{error}</div>}
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className={btnGhost}>{t("org.cancel")}</button>
          <button type="button" data-testid="org-person-save" disabled={busy} onClick={() => void save()} className={btnPrimary}>{busy ? t("org.saving") : t("org.save")}</button>
        </div>
      </div>
    </Modal>
  );
}
