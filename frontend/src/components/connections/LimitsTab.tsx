"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { connApi } from "@/lib/connections/api";
import { useConnections } from "@/lib/connections/store";
import type { Connection } from "@/lib/connections/types";
import { Btn, Card, Empty } from "../ui";
import { ErrorLine, errText, Field, inputCls, useViewer } from "./shared";

function Row({ c }: { c: Connection }) {
  const { t } = useT();
  const { isAdmin } = useViewer();
  const [perDay, setPerDay] = useState(String(c.limits.per_day ?? ""));
  const [writes, setWrites] = useState(String(c.limits.write_per_day ?? ""));
  const [budget, setBudget] = useState(String(c.limits.monthly_budget_usd ?? ""));
  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const num = (v: string) => (v.trim() === "" ? undefined : Math.max(0, Number(v) || 0));
  const save = async () => {
    setErr(null); setSaved(false);
    try {
      await connApi.patch(c.id, { limits: { per_day: num(perDay), write_per_day: num(writes), monthly_budget_usd: num(budget) } });
      await useConnections.getState().loadConnections(); setSaved(true);
    } catch (e) { setErr(errText(e)); }
  };
  return (
    <Card title={c.label}>
      <div className="grid gap-3 sm:grid-cols-3">
        <Field label={t("conn.limits.perDay")}><input data-testid="conn-limit-per-day" type="number" min={0} className={inputCls} value={perDay} onChange={(e) => setPerDay(e.target.value)} disabled={!isAdmin} /></Field>
        <Field label={t("conn.limits.writePerDay")}><input data-testid="conn-limit-write-per-day" type="number" min={0} className={inputCls} value={writes} onChange={(e) => setWrites(e.target.value)} disabled={!isAdmin} /></Field>
        <Field label={t("conn.limits.budget")}><input data-testid="conn-limit-budget" type="number" min={0} step="0.5" className={inputCls} value={budget} onChange={(e) => setBudget(e.target.value)} disabled={!isAdmin} /></Field>
      </div>
      <div className="mt-3 flex items-center gap-3">
        <Btn data-testid="conn-limits-save" kind="primary" disabled={!isAdmin} onClick={save}>{t("conn.limits.save")}</Btn>
        {saved && <span className="text-xs font-semibold text-emerald-700">{t("conn.limits.saved")}</span>}
      </div>
      <div className="mt-2"><ErrorLine msg={err} /></div>
    </Card>
  );
}

export function LimitsTab() {
  const { t } = useT();
  const connections = useConnections((s) => s.connections);
  const list = Object.values(connections).filter((c) => c.status !== "revoked");
  if (!list.length) return <Empty>{t("conn.empty")}</Empty>;
  return <div className="grid gap-3 lg:grid-cols-2">{list.map((c) => <Row key={c.id} c={c} />)}</div>;
}
