"use client";
import { useState } from "react";
import { fmtDateTime, useT } from "@/lib/i18n";
import { useConnections } from "@/lib/connections/store";
import { useStore } from "@/lib/store";
import { Btn, Empty } from "../ui";
import { inputCls } from "./shared";

const DECISION_COLOR = { allowed: "#2f7d55", needs_approval: "#a86208", denied: "#b4443c" } as const;

/** Usage log: metadata only (never message content). Export is generated locally from what is on screen. */
export function UsageTable({ connectionId }: { connectionId?: string }) {
  const { t } = useT();
  const usage = useConnections((s) => s.usage);
  const connections = useConnections((s) => s.connections);
  const agents = useStore((s) => s.agents);
  const [agent, setAgent] = useState("");
  const [result, setResult] = useState("");
  const rows = usage.filter((u) => (!connectionId || u.connection_id === connectionId) && (!agent || u.agent_id === agent) && (!result || u.decision === result));

  const exportCsv = () => {
    const head = ["time", "connection", "agent", "tool", "capability", "decision", "reason", "resource", "items", "external_origin"];
    const esc = (v: unknown) => `"${String(v ?? "").replace(/"/g, '""')}"`;
    const csv = [head.join(","), ...rows.map((u) => [u.created_at, connections[u.connection_id]?.label, u.agent_id, u.tool, u.capability, u.decision, u.deny_reason, u.resource_ref, u.items_count, u.tainted].map(esc).join(","))].join("\n");
    const url = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
    const a = document.createElement("a"); a.href = url; a.download = "connection-usage.csv"; a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-end gap-2">
        <label className="text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.usage.agent")}
          <select data-testid="conn-usage-filter-agent" className={`${inputCls} mt-1 min-w-[140px]`} value={agent} onChange={(e) => setAgent(e.target.value)}>
            <option value="">{t("conn.usage.all")}</option>
            {Object.values(agents).map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}
          </select>
        </label>
        <label className="text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.usage.result")}
          <select data-testid="conn-usage-filter-result" className={`${inputCls} mt-1 min-w-[140px]`} value={result} onChange={(e) => setResult(e.target.value)}>
            <option value="">{t("conn.usage.all")}</option>
            {(["allowed", "needs_approval", "denied"] as const).map((d) => <option key={d} value={d}>{t(`conn.decision.${d}`)}</option>)}
          </select>
        </label>
        <Btn data-testid="conn-usage-export" onClick={exportCsv} disabled={rows.length === 0}>{t("conn.usage.export")}</Btn>
      </div>
      {rows.length === 0 ? <Empty>{t("conn.usage.empty")}</Empty> : (
        <div className="overflow-x-auto">
          <table data-testid="conn-usage-table" className="w-full border-collapse text-xs">
            <thead>
              <tr className="border-b border-line text-left text-[11px] font-semibold uppercase tracking-wide text-mute">
                {["time", "connection", "employee", "action", "result", "resource", "external"].map((h) => <th key={h} className="px-2 py-1.5">{t(`conn.usage.col.${h}`)}</th>)}
              </tr>
            </thead>
            <tbody>
              {rows.map((u) => (
                <tr key={u.id} data-testid={`conn-usage-row-${u.id}`} data-decision={u.decision} data-tainted={u.tainted} className="border-b border-line/60 last:border-0">
                  <td className="whitespace-nowrap px-2 py-1.5 text-mute">{fmtDateTime(u.created_at)}</td>
                  <td className="px-2 py-1.5">{connections[u.connection_id]?.label ?? u.connection_id}</td>
                  <td className="px-2 py-1.5 font-semibold">{agents[u.agent_id ?? ""]?.name ?? u.agent_id ?? "—"}</td>
                  <td className="px-2 py-1.5 font-mono">{u.tool}</td>
                  <td className="px-2 py-1.5">
                    <span className="rounded px-1.5 py-[1px] text-[10px] font-semibold uppercase" style={{ background: `${DECISION_COLOR[u.decision]}22`, color: DECISION_COLOR[u.decision] }}>{t(`conn.decision.${u.decision}`)}</span>
                    {u.deny_reason && <span className="ml-1 text-mute">{t(`conn.deny.${u.deny_reason}`)}</span>}
                  </td>
                  <td className="px-2 py-1.5 text-mute">{u.resource_ref ?? "—"}{u.items_count != null ? ` · n=${u.items_count}` : ""}</td>
                  <td className="px-2 py-1.5">{u.tainted ? t("conn.usage.externalYes") : "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
