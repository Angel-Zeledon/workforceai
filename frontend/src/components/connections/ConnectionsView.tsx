"use client";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useT, fmtDateTime } from "@/lib/i18n";
import { useConnections } from "@/lib/connections/store";
import { DATABASE } from "@/lib/connections/dbSource";
import { CONN_PAGE_TABS, accessViewOf, resolveConnTab, type ConnPageTab, type ConnSection, type ConnTarget } from "@/lib/connections/tabs";
import type { Connection } from "@/lib/connections/types";
import { Btn, Card, Empty } from "../ui";
import { CapabilitiesTab } from "./CapabilitiesTab";
import { DataInboxesTab } from "./DataInboxesTab";
import { ConnectionDrawer } from "./ConnectionDrawer";
import { ConnectWizard } from "./ConnectWizard";
import { GrantMatrix } from "./GrantMatrix";
import { LimitsTab } from "./LimitsTab";
import { LinkImportsSection } from "./LinkImportsSection";
import { CapChip, StatusBadge, useViewer } from "./shared";
import { OfficeTag } from "./OfficeField";
import { useConnOffices } from "./useConnOffices";
import { canCreateConn } from "@/lib/connections/officeLogic";
import { UsageTable } from "./UsageTable";
import { ConceptHelp } from "../concepts/ConceptHelp";

const AGE_WARNING_DAYS = 180;

function ConnectionCard({ c, onOpen }: { c: Connection; onOpen: () => void }) {
  const { t } = useT();
  const providers = useConnections((s) => s.providers);
  const defs = providers.find((p) => p.id === c.provider)?.capabilities ?? [];
  const ageDays = c.credential ? (Date.now() - new Date(c.credential.created_at).getTime()) / 86400000 : 0;
  return (
    <button type="button" data-testid={`conn-card-${c.id}`} data-status={c.status} data-provider={c.provider} onClick={onOpen}
      className="ac-pop w-full min-w-0 rounded-xl border border-line bg-panel p-3 text-left shadow-pop sm:p-4 transition hover:border-accent">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="truncate text-sm font-semibold">{c.label}</div>
          <div className="truncate text-[11px] text-mute">{t(`conn.provider.${c.provider}`)} · <span data-testid="conn-account-label">{c.account_label ?? "—"}</span></div>
          <OfficeTag c={c} className="mt-1" />
        </div>
        {c.provider_disabled
          ? <span data-testid="conn-provider-disabled" title={t("conn.providerDisabledHint")} className="max-w-[48%] shrink-0 rounded-xl bg-slate-500/15 px-2 py-[2px] text-right text-[10px] font-semibold uppercase leading-tight text-ink2">{t("conn.providerDisabled")}</span>
          : <StatusBadge status={c.status} />}
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

const TAB_LABEL: Record<ConnPageTab, string> = { connections: "conn.tab.connections", access: "conn.tab.access", usage: "conn.tab.usageLimits" };
/** sections of the Conexiones tab, in the order of the proposal (5.2 #5): accounts, inboxes, link imports, databases */
const SECTIONS = ["accounts", "inboxes", "links", "databases"] as const;
const SECTION_LABEL: Record<(typeof SECTIONS)[number], string> = { accounts: "conn.section.accounts", inboxes: "dataInbox.tab", links: "conn.section.links", databases: "conn.section.databases" };

function SectionHead({ title, help, right }: { title: string; help?: string; right?: ReactNode }) {
  return (
    <div className="mb-3 flex flex-wrap items-start justify-between gap-2">
      <div className="min-w-0 max-w-2xl">
        <h2 className="font-display text-base font-semibold">{title}</h2>
        {help && <p className="text-xs text-mute">{help}</p>}
      </div>
      {right}
    </div>
  );
}

/**
 * Conexiones in three tabs (UX tanda 2): Conexiones (accounts, data inboxes, link imports and databases as sections of
 * one page; a database is one more kind of connection, same card and same wizard) · Quién puede usar qué (the
 * permissions table and what each agent can do) · Uso y límites. The components inside are the same as before.
 * Old links (`?tab=matrix`, `?tab=limits`…) open the tab that contains them (lib/connections/tabs.ts).
 */
export function ConnectionsView() {
  const { t } = useT();
  const storeTab = useConnections((s) => s.tab);
  const connections = useConnections((s) => s.connections);
  const { isAdmin } = useViewer();
  const { offices } = useConnOffices();
  const canAdd = canCreateConn(offices, isAdmin); // D3: the responsible of a department adds its connections too
  const [target, setTarget] = useState<ConnTarget>(() => resolveConnTab(storeTab));
  const [access, setAccess] = useState<"matrix" | "capabilities">(() => accessViewOf(resolveConnTab(storeTab).section));
  const [wizard, setWizard] = useState(false);
  const [openId, setOpenId] = useState<string | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const tab = target.tab;
  const all = Object.values(connections).sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at));
  const accounts = all.filter((c) => c.provider !== DATABASE);
  const databases = all.filter((c) => c.provider === DATABASE);

  // the link wins over the remembered tab: /connections?tab=<old or new name>
  useEffect(() => {
    const raw = new URLSearchParams(window.location.search).get("tab");
    if (!raw) return;
    const next = resolveConnTab(raw);
    setTarget(next);
    setAccess(accessViewOf(next.section));
  }, []);
  // a section asked for (inboxes, databases, limits…) scrolls into view once its tab is on screen
  useEffect(() => {
    const sec = target.section;
    if (!sec || sec === "matrix" || sec === "capabilities") return;
    const id = window.setTimeout(() => scroller.current?.querySelector(`[data-section="${sec}"]`)?.scrollIntoView({ block: "start", behavior: "smooth" }), 60);
    return () => window.clearTimeout(id);
  }, [target]);

  const choose = (k: ConnPageTab) => {
    setTarget({ tab: k });
    scroller.current?.scrollTo({ top: 0 });
    // keep the address in step, so a copied link or a reload opens the same tab
    const url = new URL(window.location.href);
    if (url.pathname.startsWith("/connections")) { url.searchParams.set("tab", k); window.history.replaceState(window.history.state, "", url.toString()); }
  };
  const jump = (sec: ConnSection) => setTarget({ tab: "connections", section: sec });
  const chip = (on: boolean) => `rounded-full border px-3 py-1 text-[11px] font-semibold transition ${on ? "border-accent/30 bg-accent-soft text-accent" : "border-line bg-panel text-mute hover:text-ink"}`;

  return (
    <div className="mx-auto flex h-full max-w-6xl flex-col px-4 py-3 sm:px-5 sm:py-4">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div>
          <div className="flex items-center gap-1.5"><h1 className="font-display text-[18px] sm:text-xl font-semibold">{t("conn.title")}</h1><ConceptHelp concept="connection" /></div>
          <p className="text-xs text-mute">{t("conn.subtitle")}</p>
        </div>
        <Btn data-testid="conn-add" kind="primary" disabled={!canAdd} onClick={() => setWizard(true)}>{t("conn.add")}</Btn>
      </div>
      <div role="tablist" className="mb-4 flex flex-wrap gap-1 border-b border-line pb-2 wf-tabs">
        {CONN_PAGE_TABS.map((k) => (
          <button key={k} role="tab" aria-selected={tab === k} data-testid={`conn-tab-${k}`} onClick={() => choose(k)}
            className={`rounded-md px-4 py-1.5 text-xs font-medium transition ${tab === k ? "bg-accent-soft text-accent" : "text-mute hover:text-ink"}`}>
            {t(TAB_LABEL[k])}
          </button>
        ))}
      </div>
      <div ref={scroller} data-testid={`conn-panel-${tab}`} className="min-h-0 flex-1 overflow-y-auto pb-6">
        {tab === "connections" && (
          <div className="space-y-6">
            <nav aria-label={t("conn.sections.aria")} className="wf-hscroll flex flex-wrap gap-1.5">
              {SECTIONS.map((s) => (
                <button key={s} type="button" data-testid={`conn-jump-${s}`} aria-current={target.section === s ? "true" : undefined} onClick={() => jump(s)} className={chip(target.section === s)}>{t(SECTION_LABEL[s])}</button>
              ))}
            </nav>
            <section data-section="accounts" data-testid="conn-section-accounts" className="scroll-mt-2">
              <SectionHead title={t("conn.section.accounts")} help={t("conn.section.accountsHelp")} />
              {accounts.length === 0 ? <Empty>{t("conn.empty")}</Empty> : (
                <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
                  {accounts.map((c) => <ConnectionCard key={c.id} c={c} onOpen={() => setOpenId(c.id)} />)}
                </div>
              )}
            </section>
            <section data-section="inboxes" data-testid="conn-section-inboxes" className="scroll-mt-2">
              <DataInboxesTab isAdmin={isAdmin} />
            </section>
            <section data-section="links" data-testid="conn-section-links" className="scroll-mt-2 [&>section]:!mt-0">
              <LinkImportsSection />
            </section>
            <section data-section="databases" data-testid="conn-section-databases" className="scroll-mt-2">
              <SectionHead title={t("conn.section.databases")} help={t("conn.section.databasesHelp")}
                right={<Btn data-testid="conn-db-add" disabled={!canAdd} onClick={() => setWizard(true)}>{t("conn.section.databasesAdd")}</Btn>} />
              {databases.length === 0 ? <Empty>{t("conn.section.databasesEmpty")}</Empty> : (
                <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
                  {databases.map((c) => <ConnectionCard key={c.id} c={c} onOpen={() => setOpenId(c.id)} />)}
                </div>
              )}
            </section>
          </div>
        )}
        {tab === "access" && (
          <div className="space-y-3">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
              <div role="tablist" aria-label={t("conn.access.aria")} className="wf-hscroll inline-flex max-w-full gap-1 rounded-lg border border-line bg-panel2 p-1">
                {(["matrix", "capabilities"] as const).map((v) => (
                  <button key={v} type="button" role="tab" aria-selected={access === v} data-testid={`conn-tab-${v}`} onClick={() => setAccess(v)}
                    className={`rounded-md px-3 py-1.5 text-xs font-semibold transition ${access === v ? "bg-panel text-ink shadow-soft" : "text-mute hover:text-ink"}`}>{t(`conn.tab.${v}`)}</button>
                ))}
              </div>
              <p className="min-w-0 flex-1 basis-60 text-[11px] text-mute">{t("conn.access.help")}</p>
            </div>
            {access === "matrix" ? <GrantMatrix /> : <CapabilitiesTab />}
          </div>
        )}
        {tab === "usage" && (
          <div className="space-y-6">
            <section data-section="log" data-testid="conn-section-usage" className="scroll-mt-2">
              <Card title={t("conn.tab.usage")}><UsageTable /></Card>
            </section>
            <section data-section="limits" data-testid="conn-section-limits" className="scroll-mt-2">
              <SectionHead title={t("conn.tab.limits")} help={t("conn.limits.help")} />
              <LimitsTab />
            </section>
          </div>
        )}
      </div>
      {wizard && <ConnectWizard onClose={() => setWizard(false)} />}
      {openId && connections[openId] && <ConnectionDrawer id={openId} onClose={() => setOpenId(null)} />}
    </div>
  );
}
