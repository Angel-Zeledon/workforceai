"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { connApi } from "@/lib/connections/api";
import { useConnections } from "@/lib/connections/store";
import type { Connection, Grant } from "@/lib/connections/types";
import { useStore } from "@/lib/store";
import { Btn, Empty } from "../ui";
import { CapChip, ErrorLine, errText, Field, inputCls, useViewer } from "./shared";

function GrantDrawer({ conn, agentId, onClose }: { conn: Connection; agentId: string; onClose: () => void }) {
  const { t } = useT();
  const agents = useStore((s) => s.agents);
  const providers = useConnections((s) => s.providers);
  const controls = useConnections((s) => s.controls);
  const grant = useConnections((s) => (s.grants[conn.id] ?? []).find((g) => g.agent_id === agentId));
  const { viewer, isOwner, isAdmin } = useViewer();
  const defs = providers.find((p) => p.id === conn.provider)?.capabilities ?? [];
  const [caps, setCaps] = useState<string[]>(grant?.capabilities ?? []);
  const [domains, setDomains] = useState((grant?.constraints.allowed_recipient_domains ?? []).join(", "));
  const [until, setUntil] = useState(grant?.valid_until?.slice(0, 10) ?? "");
  const [confirm, setConfirm] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const before = grant?.capabilities ?? [];
  const isWrite = (id: string) => !!defs.find((d) => d.id === id)?.side_effects;
  const addsWrite = caps.some((c) => isWrite(c) && !before.includes(c));
  const anyWrite = caps.some(isWrite);
  const secondNeeded = addsWrite && (controls?.admin_count ?? 1) > 1;
  const canApproveSecond = grant?.status === "pending_second_approval" && isAdmin && viewer?.id !== grant.requested_by;

  const save = async () => {
    setBusy(true); setErr(null);
    try {
      await connApi.putGrant(conn.id, agentId, {
        capabilities: caps,
        constraints: domains.trim() ? { allowed_recipient_domains: domains.split(",").map((x) => x.trim()).filter(Boolean) } : {},
        valid_until: until ? new Date(`${until}T23:59:59`).toISOString() : null,
        confirm_name: addsWrite || anyWrite ? confirm : undefined,
      });
      await useConnections.getState().loadConnections();
      onClose();
    } catch (e) { setErr(errText(e)); }
    finally { setBusy(false); }
  };
  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true); setErr(null);
    try { await fn(); await useConnections.getState().loadConnections(); onClose(); } catch (e) { setErr(errText(e)); } finally { setBusy(false); }
  };

  return (
    <aside data-testid="conn-grant-drawer" className="fixed inset-y-0 right-0 z-[70] flex w-[420px] max-w-full flex-col border-l-2 border-line bg-panel shadow-pop">
      <header className="flex items-start justify-between gap-2 border-b border-line p-4">
        <div>
          <h2 className="font-display text-lg font-semibold">{agents[agentId]?.name ?? agentId}</h2>
          <p className="text-[11px] text-mute">{conn.label}</p>
        </div>
        <Btn onClick={onClose}>{t("common.close")}</Btn>
      </header>
      <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4 text-xs">
        <p className="rounded-xl bg-panel2 px-3 py-2 text-mute" data-testid="conn-grant-no-approve-note">{t("conn.grant.noApprove")}</p>
        {grant?.status === "pending_second_approval" && (
          <div className="rounded-xl border border-amber-300 bg-amber-50 px-3 py-2 font-semibold text-amber-800">{t("conn.grant.pendingSecond")}</div>
        )}
        <div>
          <div className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.grant.caps")}</div>
          <div className="space-y-1.5">
            {conn.granted_capabilities.map((id) => {
              const d = defs.find((x) => x.id === id);
              return (
                <label key={id} className={`flex cursor-pointer items-start gap-2 rounded-xl border px-3 py-2 ${caps.includes(id) ? "border-accent bg-accent/10" : "border-line bg-panel2"}`}>
                  <input type="checkbox" data-testid={`conn-grant-cap-${id}`} disabled={!!d?.side_effects && !isOwner} checked={caps.includes(id)}
                    onChange={() => setCaps((c) => (c.includes(id) ? c.filter((x) => x !== id) : [...c, id]))} />
                  <span className="flex-1"><b className="block text-[13px]">{t(`conn.cap.${id}`)}</b>{d?.side_effects && <span className="text-mute">{t(`conn.rev.${d.reversibility ?? "none"}`)}</span>}</span>
                </label>
              );
            })}
          </div>
          {!isOwner && <p className="mt-1 text-[11px] text-mute">{t("conn.grant.ownerOnlyWrite")}</p>}
        </div>
        {caps.includes("mail.send") && (
          <Field label={t("conn.grant.recipients")} hint={t("conn.grant.recipientsHint")}>
            <input data-testid="conn-grant-constraint-recipients" className={inputCls} value={domains} onChange={(e) => setDomains(e.target.value)} placeholder="acme.com, cliente.mx" />
          </Field>
        )}
        <Field label={t("conn.grant.validUntil")}>
          <input data-testid="conn-grant-valid-until" type="date" className={inputCls} value={until} onChange={(e) => setUntil(e.target.value)} />
        </Field>
        {anyWrite && (
          <div className="space-y-1">
            <div data-testid="conn-write-warning" className="rounded-xl border border-red-300 bg-red-50 px-3 py-2 font-semibold text-red-700">{t("conn.writeWarning")}</div>
            {secondNeeded && <p data-testid="conn-grant-second-note" className="text-[11px] font-semibold text-amber-700">{t("conn.grant.secondNeeded")}</p>}
            <Field label={t("conn.grant.confirmName", { name: conn.label })}>
              <input data-testid="conn-grant-confirm-input" className={inputCls} value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="off" />
            </Field>
          </div>
        )}
        <ErrorLine msg={err} />
      </div>
      <footer className="flex flex-wrap justify-between gap-2 border-t border-line p-3">
        <div className="flex gap-2">
          {grant && <Btn data-testid="conn-grant-remove" kind="danger" disabled={busy || !isAdmin} onClick={() => act(() => connApi.deleteGrant(conn.id, agentId))}>{t("conn.grant.remove")}</Btn>}
          {canApproveSecond && <Btn data-testid="conn-grant-approve" kind="ok" disabled={busy} onClick={() => act(() => connApi.approveGrant(conn.id, agentId))}>{t("conn.grant.approveSecond")}</Btn>}
        </div>
        <Btn data-testid="conn-grant-save" kind="primary" disabled={busy || !isAdmin || caps.length === 0 || (anyWrite && confirm !== conn.label)} onClick={save}>{t("conn.grant.save")}</Btn>
      </footer>
    </aside>
  );
}

export function GrantMatrix() {
  const { t } = useT();
  const connections = useConnections((s) => s.connections);
  const grants = useConnections((s) => s.grants);
  const providers = useConnections((s) => s.providers);
  const agentOrder = useStore((s) => s.agentOrder);
  const agents = useStore((s) => s.agents);
  const [sel, setSel] = useState<{ conn: string; agent: string } | null>(null);
  const cols = Object.values(connections).filter((c) => c.status !== "revoked");
  if (cols.length === 0) return <Empty>{t("conn.empty")}</Empty>;
  const cell = (agentId: string, c: Connection): Grant | undefined => (grants[c.id] ?? []).find((g) => g.agent_id === agentId);
  const defs = (c: Connection) => providers.find((p) => p.id === c.provider)?.capabilities ?? [];
  return (
    <div>
      <p className="mb-3 text-xs text-mute">{t("conn.matrix.help")}</p>
      <div className="overflow-x-auto rounded-xl border border-line bg-panel shadow-pop">
        <table data-testid="conn-matrix" className="w-full border-collapse text-xs">
          <thead>
            <tr className="border-b border-line bg-panel2">
              <th className="px-3 py-2 text-left text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.matrix.employee")}</th>
              {cols.map((c) => <th key={c.id} className="px-3 py-2 text-left text-[11px] font-semibold text-ink">{c.label}</th>)}
            </tr>
          </thead>
          <tbody>
            {agentOrder.map((a) => (
              <tr key={a} className="border-b border-line last:border-0">
                <td className="whitespace-nowrap px-3 py-2 font-semibold">{agents[a]?.name ?? a}</td>
                {cols.map((c) => {
                  const g = cell(a, c);
                  return (
                    <td key={c.id} className="px-2 py-1.5">
                      <button type="button" data-testid={`conn-matrix-cell-${a}-${c.id}`} data-caps={g?.capabilities.join(",") ?? ""} data-status={g?.status ?? "none"} onClick={() => setSel({ conn: c.id, agent: a })}
                        className="flex min-h-[32px] w-full flex-wrap items-center gap-1 rounded-xl border border-dashed border-line px-2 py-1 text-left transition hover:border-accent">
                        {g ? g.capabilities.map((cap) => <CapChip key={cap} cap={cap} risk={defs(c).find((d) => d.id === cap)?.risk ?? "low"} dim={g.status !== "active"} />) : <span className="text-mute">{t("conn.matrix.none")}</span>}
                        {g?.status === "pending_second_approval" && <span className="text-[10px] font-semibold text-amber-700">{t("conn.grant.pending")}</span>}
                      </button>
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {sel && connections[sel.conn] && <GrantDrawer key={`${sel.conn}-${sel.agent}`} conn={connections[sel.conn]} agentId={sel.agent} onClose={() => setSel(null)} />}
    </div>
  );
}
