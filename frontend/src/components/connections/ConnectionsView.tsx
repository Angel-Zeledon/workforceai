"use client";
import { useState } from "react";
import { useT, fmtDateTime } from "@/lib/i18n";
import { useConnections, type ConnTab } from "@/lib/connections/store";
import type { Connection } from "@/lib/connections/types";
import { Btn, Card, Empty } from "../ui";
import { CapabilitiesTab } from "./CapabilitiesTab";
import { ConnectionDrawer } from "./ConnectionDrawer";
import { ConnectWizard } from "./ConnectWizard";
import { GrantMatrix } from "./GrantMatrix";
import { LimitsTab } from "./LimitsTab";
import { CapChip, StatusBadge, useViewer } from "./shared";
import { UsageTable } from "./UsageTable";

const TABS: ConnTab[] = ["connections", "matrix", "capabilities", "usage", "limits"];
const AGE_WARNING_DAYS = 180;

function ConnectionCard({ c, onOpen }: { c: Connection; onOpen: () => void }) {
  const { t } = useT();
  const providers = useConnections((s) => s.providers);
  const defs = providers.find((p) => p.id === c.provider)?.capabilities ?? [];
  const ageDays = c.credential ? (Date.now() - new Date(c.credential.created_at).getTime()) / 86400000 : 0;
  return (
    <button type="button" data-testid={`conn-card-${c.id}`} data-status={c.status} data-provider={c.provider} onClick={onOpen}
      className="ac-pop w-full rounded-xl border border-line bg-panel p-4 text-left shadow-pop transition hover:border-accent">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="truncate text-sm font-semibold">{c.label}</div>
          <div className="truncate text-[11px] text-mute">{t(`conn.provider.${c.provider}`)} · <span data-testid="conn-account-label">{c.account_label ?? "—"}</span></div>
        </div>
        <StatusBadge status={c.status} />
      </div>
      <div className="mt-2 flex flex-wrap gap-1">
        {c.granted_capabilities.map((cap) => <CapChip key={cap} cap={cap} risk={defs.find((d) => d.id === cap)?.risk ?? "low"} />)}
        {c.granted_capabilities.length === 0 && <span className="text-[11px] text-mute">{t("conn.noCaps")}</span>}
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-mute">
        {c.mode === "simulated" && <span className="rounded-full bg-violet-500/15 px-2 font-semibold text-violet-700">{t("conn.mode.simulated")}</span>}
        {c.read_only && <span className="rounded-full bg-amber-500/15 px-2 font-semibold text-amber-700">{t("conn.readOnlyBadge")}</span>}
        <span>{t("conn.grantsCount", { count: c.grants_count })}</span>
        {c.credential?.hint && <span data-testid="conn-credential-hint">…{c.credential.hint}</span>}
        {c.last_used_at && <span>{t("conn.lastUsed")} {fmtDateTime(c.last_used_at)}</span>}
      </div>
      {c.grants_count === 0 && c.status === "active" && <p className="mt-2 text-[11px] font-semibold text-amber-700">{t("conn.not_granted_note")}</p>}
      {c.status_reason && c.status !== "active" && <p className="mt-1 text-[11px] text-mute">{t(`conn.reason.${c.status_reason}`)}</p>}
      {ageDays > AGE_WARNING_DAYS && <p data-testid="conn-age-warning" className="mt-1 text-[11px] font-semibold text-amber-700">{t("conn.ageWarning", { days: Math.floor(ageDays) })}</p>}
    </button>
  );
}

export function ConnectionsView() {
  const { t } = useT();
  const tab = useConnections((s) => s.tab);
  const setTab = useConnections((s) => s.setTab);
  const connections = useConnections((s) => s.connections);
  const { isAdmin } = useViewer();
  const [wizard, setWizard] = useState(false);
  const [openId, setOpenId] = useState<string | null>(null);
  const list = Object.values(connections).sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at));

  return (
    <div className="mx-auto flex h-full max-w-6xl flex-col px-5 py-4">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div>
          <h1 className="font-display text-xl font-semibold">{t("conn.title")}</h1>
          <p className="text-xs text-mute">{t("conn.subtitle")}</p>
        </div>
        <Btn data-testid="conn-add" kind="primary" disabled={!isAdmin} onClick={() => setWizard(true)}>{t("conn.add")}</Btn>
      </div>
      <div role="tablist" className="mb-4 flex flex-wrap gap-1 border-b border-line pb-2">
        {TABS.map((k) => (
          <button key={k} role="tab" aria-selected={tab === k} data-testid={`conn-tab-${k}`} onClick={() => setTab(k)}
            className={`rounded-md px-4 py-1.5 text-xs font-medium transition ${tab === k ? "bg-accent-soft text-accent" : "text-mute hover:text-ink"}`}>
            {t(`conn.tab.${k}`)}
          </button>
        ))}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto pb-6">
        {tab === "connections" && (
          list.length === 0 ? <Empty>{t("conn.empty")}</Empty> : (
            <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
              {list.map((c) => <ConnectionCard key={c.id} c={c} onOpen={() => setOpenId(c.id)} />)}
            </div>
          )
        )}
        {tab === "matrix" && <GrantMatrix />}
        {tab === "capabilities" && <CapabilitiesTab />}
        {tab === "usage" && <Card title={t("conn.tab.usage")}><UsageTable /></Card>}
        {tab === "limits" && <LimitsTab />}
      </div>
      {wizard && <ConnectWizard onClose={() => setWizard(false)} />}
      {openId && connections[openId] && <ConnectionDrawer id={openId} onClose={() => setOpenId(null)} />}
    </div>
  );
}
