"use client";
import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { useStore } from "@/lib/store";
import { useT } from "@/lib/i18n";
import { pendingTotal } from "@/lib/inboxLogic";
import { BlockBoundary } from "./BlockBoundary";
import { TeamRail, TeamStrip } from "./TeamRail";
import { WorkPanel } from "./WorkPanel";
import { WorkThread } from "./WorkThread";
import { useOfficeAgentIds } from "@/lib/currentOffice";
import { MOBILE_TABS, panelLayout, validTarget, type MobileTab } from "./workspaceLogic";

type Bp = "mobile" | "tablet" | "desktop";
const QUERIES = ["(min-width: 1280px)", "(min-width: 768px)"] as const;
function subscribe(cb: () => void) {
  const ms = QUERIES.map((q) => window.matchMedia(q));
  ms.forEach((m) => m.addEventListener("change", cb));
  return () => ms.forEach((m) => m.removeEventListener("change", cb));
}
const snapshot = (): Bp => (window.matchMedia(QUERIES[0]).matches ? "desktop" : window.matchMedia(QUERIES[1]).matches ? "tablet" : "mobile");

/**
 * The list view as a workspace: team on the left, the conversation (with the box to write) in the middle and, on
 * the right, what needs you, what is in motion and what is ready. Tablet: team as a strip. Phone: three tabs.
 * Only the layout that applies is mounted, so there is one box to write in, never two.
 */
export function WorkSpace({ picked, setPicked, onOpenReport, onOpenPanel, openDecisions = false }: { picked: string | null; setPicked: (id: string | null) => void; onOpenReport: () => void; onOpenPanel: (id: string) => void; openDecisions?: boolean }) {
  const { t } = useT();
  const bp = useSyncExternalStore(subscribe, snapshot, () => "desktop" as Bp);
  const agents = useStore((s) => s.agents);
  const [tab, setTab] = useState<MobileTab>("chat");
  const { ids: officeAgents } = useOfficeAgentIds();
  const target = validTarget(picked, agents);
  const openAgent = useStore((s) => s.selectedAgentId);
  const layout = panelLayout(bp, openAgent);
  const approvals = useStore((s) => s.approvals);
  const questions = useStore((s) => s.questions);
  const needs = useMemo(() => pendingTotal(approvals, questions ?? {}), [approvals, questions]);
  useEffect(() => { if (picked && !agents[picked] && Object.keys(agents).length) setPicked(null); }, [picked, agents]);
  // changing office: a colleague who is not in it can no longer be the one written to
  useEffect(() => { if (picked && agents[picked] && officeAgents.length && !officeAgents.includes(picked)) setPicked(null); }, [picked, officeAgents]);
  const pick = (id: string | null) => { setPicked(id); setTab("chat"); };
  const thread = <BlockBoundary name="conversation"><WorkThread target={target} onTarget={pick} autoFocus={bp === "desktop"} layout={layout} openAgent={openAgent} /></BlockBoundary>;
  const live = (
    // new things that wait for the person are announced politely, without moving focus
    <p role="status" aria-live="polite" className="sr-only" data-testid="work-needs-live">{needs > 0 ? t("work.needs.live", { n: needs }) : ""}</p>
  );

  if (bp === "mobile") {
    const label: Record<MobileTab, string> = { chat: t("work.tab.chat"), running: t("work.tab.running"), team: t("work.tab.team") };
    return (
      <div data-testid="workspace" data-bp="mobile" className="flex h-full min-h-0 flex-col">
        {live}
        <div role="tablist" aria-label={t("work.tabs.label")} className="flex shrink-0 border-b border-line bg-panel px-1">
          {MOBILE_TABS.map((k) => (
            <button key={k} type="button" role="tab" id={`work-tab-${k}`} aria-selected={tab === k} aria-controls="work-tabpanel" data-testid={`work-tab-${k}`} onClick={() => setTab(k)}
              className={`relative min-h-11 flex-1 px-1 text-[13px] font-medium transition-colors ${tab === k ? "text-accent" : "text-ink2"}`}>
              {label[k]}
              {k === "running" && needs > 0 && <span className="ml-1 rounded-full bg-warn px-1.5 py-px text-[11px] font-semibold text-white">{needs}</span>}
              {tab === k && <span aria-hidden className="absolute inset-x-3 bottom-0 h-0.5 rounded-full bg-accent" />}
            </button>
          ))}
        </div>
        <div role="tabpanel" id="work-tabpanel" aria-labelledby={`work-tab-${tab}`} className="min-h-0 flex-1">
          {tab === "chat" && thread}
          {tab === "running" && <WorkPanel onOpenReport={onOpenReport} openDecisions={openDecisions} className="h-full" />}
          {tab === "team" && <BlockBoundary name="team"><TeamRail target={target} onPick={pick} onPanel={onOpenPanel} className="h-full" /></BlockBoundary>}
        </div>
      </div>
    );
  }

  if (bp === "tablet") {
    return (
      <div data-testid="workspace" data-bp="tablet" className="grid h-full min-h-0 grid-cols-[minmax(0,1fr)_320px]">
        {live}
        <div className="flex min-h-0 min-w-0 flex-col">
          <BlockBoundary name="team"><TeamStrip target={target} onPick={pick} onPanel={onOpenPanel} /></BlockBoundary>
          {/* under the panel the conversation and its box are not reachable while the panel is open */}
          <div className="min-h-0 flex-1" inert={layout === "overlay"} aria-hidden={layout === "overlay"}>{thread}</div>
        </div>
        <WorkPanel onOpenReport={onOpenReport} openDecisions={openDecisions} compactProposals={target === null} className="border-l border-line bg-panel" />
      </div>
    );
  }

  // with an agent open, the panel's width is left free on the right so the conversation shrinks instead of being covered
  return (
    <div data-testid="workspace" data-bp="desktop" className="grid h-full min-h-0 grid-cols-[260px_minmax(0,1fr)_340px]" style={layout === "gap" ? { gridTemplateColumns: "260px minmax(0,1fr) var(--panel-w, 420px)" } : undefined}>
      {live}
      <BlockBoundary name="team"><TeamRail target={target} onPick={pick} onPanel={onOpenPanel} className="border-r border-line bg-panel" /></BlockBoundary>
      <div className="min-h-0 min-w-0">{thread}</div>
      {layout === "gap" ? <div data-testid="work-panel-gap" aria-hidden className="min-h-0" /> : <WorkPanel onOpenReport={onOpenReport} openDecisions={openDecisions} compactProposals={target === null} className="border-l border-line bg-panel" />}
    </div>
  );
}
