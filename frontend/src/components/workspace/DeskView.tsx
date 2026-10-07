"use client";
import { useEffect, type ReactNode } from "react";
import { isProjectTab, ME_DESK, useArtifacts, type Desk } from "@/lib/artifacts";
import { LOCALES, useT, type Locale } from "@/lib/i18n";
import { usePreferences } from "@/lib/preferences";
import { useStore } from "@/lib/store";
import { useAgentName } from "@/components/ui";
import { ArtifactPane } from "./ArtifactPane";
import { DeliverableNav } from "./DeliverableNav";
import { ProjectSummary } from "./ProjectSummary";
import { WorkspaceTabs } from "./WorkspaceTabs";

const EMPTY_DESK: Desk = { tabs: [], active: null, split: null, focusPane: 0 };

/** The desk of one agent (or "me"): tabs + one or two panes. Tab state lives in the store, so it survives mode changes. */
export function DeskView({ deskId, actions }: { deskId: string; actions?: ReactNode }) {
  const { t, locale } = useT();
  const ensureDesk = useArtifacts((s) => s.ensureDesk);
  const loaded = useArtifacts((s) => s.loaded);
  const storedDesk = useArtifacts((s) => s.desks[deskId]);
  const desk = storedDesk ?? EMPTY_DESK;
  const artifacts = useArtifacts((s) => s.artifacts);
  const toggleSplit = useArtifacts((s) => s.toggleSplit);
  const setFocusPane = useArtifacts((s) => s.setFocusPane);
  const setPref = usePreferences((s) => s.set);
  const name = useAgentName();
  const agents = useStore((s) => s.agents);
  useEffect(() => { if (loaded) ensureDesk(deskId); }, [loaded, deskId, ensureDesk]);
  // load content of the visible tabs
  const ensureContent = useArtifacts((s) => s.ensureContent);
  useEffect(() => { desk?.tabs.forEach((id) => { if (!isProjectTab(id)) ensureContent(id); }); }, [desk?.tabs, ensureContent]); // eslint-disable-line react-hooks/exhaustive-deps

  const proposals = Object.values(artifacts).reduce((n, a) => n + (a.created_by.id === deskId || a.attachments.some((x) => x.agent_id === deskId) ? a.pending_proposals : 0), 0);
  const left = desk?.active ?? null;
  const right = desk?.split ?? null;
  const render = (id: string | null, pane: 0 | 1) => {
    if (!id) return <div className="flex h-full items-center justify-center p-6 text-center text-xs text-mute">{t("workspace.emptyDesk")}</div>;
    if (isProjectTab(id)) return <ProjectSummary pid={id.slice(8)} deskId={deskId} />;
    return <ArtifactPane id={id} deskId={deskId} focused={desk?.focusPane === pane} onFocus={() => setFocusPane(deskId, pane)} />;
  };

  return (
    <div data-testid="ws-desk" data-desk={deskId} className="flex h-full min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex items-center gap-2 border-b border-line px-3 py-2">
        <h2 className="min-w-0 flex-1 truncate font-display text-[15px] font-semibold text-ink">{deskId === ME_DESK ? t("workspace.myDesk") : t("workspace.deskOf", { name: agents[deskId] ? name(deskId) : deskId })}</h2>
        <span data-testid="ws-proposals-count" title={t("workspace.proposals")} className={`rounded-full px-2 py-0.5 text-[10px] font-semibold ${proposals ? "bg-amber-500 text-black" : "bg-mute/[0.12] text-mute"}`}>{t("workspace.proposalsCount", { count: proposals })}</span>
        <select data-testid="ws-locale-select" aria-label={t("workspace.language")} value={locale} onChange={(e) => setPref({ locale: e.target.value as Locale })} className="rounded-md border border-line bg-panel px-2 py-0.5 text-[11px] font-semibold text-ink">
          {LOCALES.map((l) => <option key={l} value={l}>{l.toUpperCase()}</option>)}
        </select>
        <button type="button" data-testid="ws-split-toggle" onClick={() => toggleSplit(deskId)} className={`rounded-md border px-2.5 py-0.5 text-[11px] font-semibold ${desk?.split ? "border-accent bg-accent/15 text-accent" : "border-line bg-panel text-ink hover:border-accent"}`}>{t("workspace.split")}</button>
        {actions}
      </div>
      <WorkspaceTabs deskId={deskId} desk={desk} />
      <div className="border-t-2 border-line" />
      {left && !isProjectTab(left) && <DeliverableNav deskId={deskId} artifactId={left} />}
      <div className={`grid min-h-0 flex-1 ${right ? "grid-cols-2 divide-x-2 divide-line" : "grid-cols-1"}`}>
        <div className="min-h-0 min-w-0">{render(left, 0)}</div>
        {right && <div className="min-h-0 min-w-0">{render(right, 1)}</div>}
      </div>
    </div>
  );
}
