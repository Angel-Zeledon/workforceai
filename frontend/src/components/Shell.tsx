"use client";
import dynamic from "next/dynamic";
import { useEffect } from "react";
import { MOCK } from "@/lib/config";
import { useStore } from "@/lib/store";
import { startRealtime } from "@/lib/ws";
import { ActivityFeed } from "./ActivityFeed";
import { WorkspaceHost } from "./workspace/WorkspaceHost";
import { ApprovalsTray } from "./ApprovalsTray";
import { CommandBar } from "./CommandBar";
import { Dashboard } from "./dashboard/Dashboard";
import { ProjectsView } from "./projects/ProjectsView";
import { ProjectPill } from "./projects/ProjectPill";
import { useProjects } from "@/lib/projects/store";
import { fmtUsd } from "@/lib/meta";
import { useT } from "@/lib/i18n";
import { usePreferences } from "@/lib/preferences";
import { LightingToggle } from "./OfficeSettings";
import { AdminMenu } from "./admin/AdminMenu";
import { OrgIndicator } from "./auth/OrgIndicator";
import { ControlsBanner, ControlsOverlay } from "./security/ControlsChrome";
import { CostOverlays } from "./cost/CostOverlays";
import { Icon } from "./icons";
import { navBtn } from "./navStyles";

const OfficeScene = dynamic(() => import("./office/OfficeScene"), {
  ssr: false,
  loading: () => <div className="flex h-full items-center justify-center text-sm font-medium text-mute">…</div>,
});

export function Shell() {
  const { t } = useT();
  const hydratePrefs = usePreferences((s) => s.hydrate);
  const mode = useStore((s) => s.mode);
  const setMode = useStore((s) => s.setMode);
  const connected = useStore((s) => s.connected);
  const metrics = useStore((s) => s.metrics);
  const selected = useStore((s) => s.selectedAgentId);
  const pendingApprovals = useStore((s) => Object.values(s.approvals).some((a) => a.status === "pending"));
  const projectsOpen = useProjects((s) => s.open);
  const setProjectsOpen = useProjects((s) => s.setOpen);

  useEffect(() => startRealtime(), []);
  useEffect(() => { hydratePrefs(); }, [hydratePrefs]);

  return (
    <div className="flex h-screen w-screen flex-col overflow-hidden bg-bg text-ink">
      <header className="relative z-[55] flex shrink-0 flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-line bg-panel px-4 py-2 md:min-h-[56px]">
        <div className="flex min-w-0 max-w-full flex-wrap items-center gap-x-4 gap-y-2 xl:flex-nowrap">
          <div className="flex items-center gap-2.5">
            <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-accent text-white"><Icon name="office" size={17} /></div>
            <div className="leading-tight">
              <div className="text-[13px] font-semibold tracking-tight">AI Workforce OS</div>
              <OrgIndicator className="text-[10.5px] text-mute" />
            </div>
          </div>
          <div className="hidden h-6 w-px bg-line md:block" />
          <div className="flex max-w-full flex-nowrap items-center gap-1.5 overflow-x-auto pb-0.5 [&>*]:shrink-0 [&>*]:whitespace-nowrap sm:pb-0">
            <button type="button" data-testid="mode-toggle" data-mode={mode} onClick={() => setMode(mode === "office" ? "dashboard" : "office")} title={t("mode.switchTitle")} className="flex rounded-lg border border-line bg-panel2 p-0.5 text-xs font-medium">
              {(["office", "dashboard"] as const).map((m) => (
                <span key={m} className={`flex items-center gap-1.5 rounded-md px-3 py-1.5 transition ${mode === m ? "bg-panel text-ink shadow-pop ring-1 ring-line" : "text-mute hover:text-ink"}`}>
                  <Icon name={m === "office" ? "office" : "grid"} size={14} />
                  {m === "office" ? t("mode.office") : t("mode.dashboard")}
                </span>
              ))}
            </button>
            <button type="button" data-testid="nav-projects" data-active={projectsOpen} onClick={() => setProjectsOpen(!projectsOpen)} className={navBtn(projectsOpen)}>
              <Icon name="folder" size={14} />{t("nav.projects")}
            </button>
          </div>
        </div>
        <div className="flex items-center gap-x-3 text-[11px] text-mute">
          {metrics && (
            <div className="flex items-center gap-3 border-r border-line pr-3 sm:gap-4 sm:pr-4">
              <span className="hidden 2xl:inline">{t("hud.active")} <b className="font-mono font-medium text-ink">{metrics.tasks_active}</b></span>
              <span data-testid="hud-approvals">{t("hud.approvalsShort")} <b className={`font-mono font-medium ${metrics.approvals_pending ? "text-warn" : "text-ink"}`}>{metrics.approvals_pending}</b></span>
              <span data-testid="hud-cost">{t("common.cost")} <b className="font-mono font-medium text-ink">{fmtUsd(metrics.cost_usd)}</b></span>
            </div>
          )}
          {MOCK && <span data-testid="mock-badge" className="rounded bg-violet-500/15 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-violet-700">Mock</span>}
          <span className="flex items-center gap-1.5 whitespace-nowrap font-medium text-ink2">
            <span className={`h-1.5 w-1.5 rounded-full ${connected ? "bg-ok" : "animate-pulse bg-err"}`} />
            {connected ? t("hud.live") : t("hud.reconnecting")}
          </span>
          <AdminMenu />
        </div>
      </header>

      <ControlsBanner />
      <CostOverlays />
      <main className="relative min-h-0 flex-1">
        <ControlsOverlay />
        {projectsOpen ? <ProjectsView /> : mode === "office" ? (
          <>
            <div className="absolute inset-0 isolate"><OfficeScene /></div>
            <div className="pointer-events-none absolute left-4 top-4 w-[330px] max-w-[calc(100vw-32px)] sm:max-w-[40vw]">
              <div className="hidden rounded-xl border border-line bg-panel/95 p-3 shadow-soft backdrop-blur sm:block">
                <div className="mb-2 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute">{t("hud.liveActivity")}</div>
                <ActivityFeed limit={7} compact />
              </div>
              <ProjectPill />
            </div>
            <div className="pointer-events-none absolute bottom-[96px] left-4"><ApprovalsTray /></div>
            <div className="pointer-events-none absolute inset-x-0 bottom-4 flex justify-center px-4" style={{ paddingRight: selected ? "var(--panel-w, 420px)" : 16, paddingLeft: pendingApprovals ? 352 : 16 }}>
              <CommandBar />
            </div>
            <div className="pointer-events-none absolute right-4 top-4 z-40 flex items-start gap-2" style={{ right: selected ? "calc(var(--panel-w, 420px) + 16px)" : 16 }}>
              <LightingToggle />
            </div>
            {selected && <div className="pointer-events-none absolute bottom-0 right-0 top-0 z-30"><WorkspaceHost id={selected} /></div>}
            {!selected && <div className="pointer-events-none absolute bottom-4 right-4 hidden rounded-md border border-line bg-panel/80 px-3 py-1.5 text-[10px] font-semibold text-mute md:block">{t("hud.cameraHint")}</div>}
          </>
        ) : (
          <Dashboard />
        )}
      </main>
    </div>
  );
}
