"use client";
import { AgentMark } from "../brand/AgentMark";
import { ProviderAlert } from "./ProviderAlert";
import { useEffect, useState, type ReactNode } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { MOCK } from "@/lib/config";
import { useStore } from "@/lib/store";
import { startRealtime } from "@/lib/ws";
import { FileViewerPanel } from "../files/FileViewerPanel";
import { useFileViewer } from "@/lib/fileView";
import { CoinCounter } from "../shop/Coin";
import { useRewards, useShopNav } from "@/lib/rewards";
import { useDriveNav } from "@/lib/drive";
import { useSkillsNav } from "@/lib/skills";
import { useProjects } from "@/lib/projects/store";
import { fmtUsd } from "@/lib/meta";
import { useT } from "@/lib/i18n";
import { usePreferences } from "@/lib/preferences";
import { setNavigator, SECTIONS, sectionOf } from "@/lib/navBridge";
import { AdminMenu } from "../admin/AdminMenu";
import { OrgIndicator } from "../auth/OrgIndicator";
import { ControlsBanner } from "../security/ControlsChrome";
import { CostOverlays } from "../cost/CostOverlays";
import { Icon, type IconName } from "../icons";
import { Toaster } from "./Toaster";
import { TourLauncher } from "../tour/TourLauncher";
import { TourOverlay } from "../tour/TourOverlay";
import { WelcomeOverlay } from "../onboarding/WelcomeOverlay";
import { RouteCurtain } from "./RouteCurtain";
import { TrialBanner } from "./TrialBanner";
import { ActivationChecklist } from "./ActivationChecklist";

const COLLAPSE_KEY = "wf.sidebar.collapsed";

function NavLink({ href, icon, label, testid, active, collapsed, onNavigate, badge }: {
  href: string; icon: IconName; label: string; testid: string; active: boolean; collapsed: boolean; onNavigate?: () => void; badge?: number;
}) {
  return (
    <Link href={href} prefetch={false} data-testid={testid} data-active={active} aria-current={active ? "page" : undefined} title={collapsed ? label : undefined} onClick={onNavigate}
      className={`group relative flex items-center gap-3 rounded-md px-3 py-2 text-[13px] font-medium transition-colors duration-150 ${active ? "bg-white/10 text-white" : "text-white/70 hover:bg-white/5 hover:text-white"} ${collapsed ? "justify-center px-0" : ""}`}>
      {active && <span aria-hidden className="absolute inset-y-1.5 left-0 w-[3px] rounded-r bg-[#5eead4]" />}
      <Icon name={icon} size={18} className="shrink-0 transition-transform duration-150 group-hover:scale-110" />
      {!collapsed && <span className="min-w-0 flex-1 truncate">{label}</span>}
      {!collapsed && badge ? <span className="rounded-full bg-white/15 px-1.5 text-[10px] font-semibold">{badge}</span> : null}
    </Link>
  );
}

function Sidebar({ section, collapsed, onToggle, onNavigate }: { section: string; collapsed: boolean; onToggle?: () => void; onNavigate?: () => void }) {
  const { t } = useT();
  const shopAvailable = useRewards((s) => s.available);
  const pending = useStore((s) => Object.values(s.approvals).filter((a) => a.status === "pending").length);
  const items = SECTIONS.filter((s) => s.id !== "shop" || shopAvailable);
  const isActive = (id: string) => section === id || (id === "office" && section === "dashboard");
  return (
    <nav aria-label={t("nav.main")} data-testid="sidebar" data-collapsed={collapsed} className="flex h-full flex-col bg-navy text-white">
      <div className={`flex h-14 shrink-0 items-center gap-2.5 border-b border-white/10 ${collapsed ? "justify-center" : "px-4"}`}>
        <AgentMark size={32} tone="light" className="shrink-0" />
        {!collapsed && <span className="truncate text-[14px] font-semibold tracking-tight">AI Workforce OS</span>}
      </div>
      <div className="flex-1 space-y-1 overflow-y-auto p-2.5">
        {items.map((s) => (
          <NavLink key={s.id} href={s.href} icon={s.icon} label={t(s.label)} testid={`nav-${s.id}`} active={isActive(s.id)} collapsed={collapsed} onNavigate={onNavigate} badge={s.id === "office" ? pending : undefined} />
        ))}
      </div>
      <div className="space-y-1 border-t border-white/10 p-2.5">
        <NavLink href="/settings" icon="settings" label={t("nav.settings")} testid="nav-settings" active={section === "settings"} collapsed={collapsed} onNavigate={onNavigate} />
        {onToggle && (
          <button type="button" onClick={onToggle} data-testid="sidebar-toggle" title={collapsed ? t("nav.expand") : t("nav.collapse")} aria-label={collapsed ? t("nav.expand") : t("nav.collapse")}
            className={`flex w-full items-center gap-3 rounded-md px-3 py-2 text-[12px] text-white/50 transition hover:bg-white/5 hover:text-white ${collapsed ? "justify-center px-0" : ""}`}>
            <Icon name="sidebar" size={17} className={`transition-transform duration-200 ${collapsed ? "rotate-180" : ""}`} />
            {!collapsed && <span>{t("nav.collapse")}</span>}
          </button>
        )}
      </div>
    </nav>
  );
}

/** Frame of the signed-in app: collapsible sidebar (drawer on mobile), slim top bar and the routed page. */
export function AppShell({ children }: { children: ReactNode }) {
  const { t } = useT();
  const router = useRouter();
  const pathname = usePathname() || "/";
  const section = sectionOf(pathname);
  const hydratePrefs = usePreferences((s) => s.hydrate);
  const reduce = usePreferences((s) => s.prefs.reduceMotion);
  const connected = useStore((s) => s.connected);
  const metrics = useStore((s) => s.metrics);
  const selected = useStore((s) => s.selectedAgentId);
  const viewerOpen = useFileViewer((s) => !!s.ref);
  const [collapsed, setCollapsed] = useState(false);
  const [drawer, setDrawer] = useState(false);

  useEffect(() => { setNavigator((href) => router.push(href)); return () => setNavigator(null); }, [router]);
  useEffect(() => startRealtime(), []);
  useEffect(() => { hydratePrefs(); }, [hydratePrefs]);
  useEffect(() => { try { setCollapsed(localStorage.getItem(COLLAPSE_KEY) === "1"); } catch { /* private mode */ } }, []);
  useEffect(() => { setDrawer(false); }, [pathname]);
  // Old stores keep reporting "is this screen open?" for code that still reads them: derive it from the URL.
  useEffect(() => {
    useDriveNav.setState({ open: section === "drive" });
    useSkillsNav.setState({ open: section === "skills" });
    useShopNav.setState({ open: section === "shop" });
    useProjects.setState({ open: section === "projects" });
    useStore.getState().setMode(section === "dashboard" ? "dashboard" : "office");
  }, [section]);

  const toggleCollapsed = () => setCollapsed((c) => { try { localStorage.setItem(COLLAPSE_KEY, c ? "0" : "1"); } catch { /* ignore */ } return !c; });
  const modeToggle = section === "office" || section === "dashboard";
  const title = section === "dashboard" ? t("mode.dashboard") : section === "settings" ? t("nav.settings") : t(SECTIONS.find((s) => s.id === section)?.label ?? "nav.office");

  return (
    <div data-reduce={reduce ? "1" : "0"} className="flex h-screen w-screen overflow-hidden bg-bg text-ink">
      <aside className={`hidden shrink-0 transition-[width] duration-200 ease-out md:block ${collapsed ? "w-[64px]" : "w-[224px]"}`}>
        <Sidebar section={section} collapsed={collapsed} onToggle={toggleCollapsed} />
      </aside>
      {drawer && (
        <div className="fixed inset-0 z-[80] md:hidden" data-testid="nav-drawer">
          <button type="button" aria-label={t("nav.close")} className="absolute inset-0 bg-navy/50 ac-fade" onClick={() => setDrawer(false)} />
          <aside className="ac-slide-left absolute inset-y-0 left-0 w-[260px] shadow-float"><Sidebar section={section} collapsed={false} onNavigate={() => setDrawer(false)} /></aside>
        </div>
      )}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="relative z-[55] flex h-14 shrink-0 items-center justify-between gap-3 border-b border-line bg-panel px-3 sm:px-4">
          <div className="flex min-w-0 items-center gap-3">
            <button type="button" data-testid="nav-open" aria-label={t("nav.open")} title={t("nav.open")} onClick={() => setDrawer(true)} className="rounded-md p-1.5 text-ink2 transition hover:bg-panel2 md:hidden"><Icon name="menu" size={20} /></button>
            <div data-tour="shell.title" className="min-w-0 leading-tight">
              <div className="truncate text-[14px] font-semibold tracking-tight">{title}</div>
              <OrgIndicator className="truncate text-[10.5px] text-mute" />
            </div>
            {modeToggle && (
              <button type="button" data-tour="shell.mode" data-testid="mode-toggle" data-mode={section === "dashboard" ? "dashboard" : "office"} onClick={() => router.push(section === "dashboard" ? "/" : "/dashboard")} title={t("mode.switchTitle")}
                className="ml-1 hidden rounded-lg border border-line bg-panel2 p-0.5 text-xs font-medium sm:flex">
                {(["office", "dashboard"] as const).map((m) => (
                  <span key={m} className={`flex items-center gap-1.5 rounded-md px-3 py-1.5 transition ${(section === "dashboard") === (m === "dashboard") ? "bg-panel text-ink shadow-pop ring-1 ring-line" : "text-mute hover:text-ink"}`}>
                    <Icon name={m === "office" ? "office" : "grid"} size={14} />
                    {m === "office" ? t("mode.office") : t("mode.dashboard")}
                  </span>
                ))}
              </button>
            )}
          </div>
          <div className="flex items-center gap-x-3 text-[11px] text-mute">
            {metrics && (
              <div data-tour="shell.metrics" className="hidden items-center gap-3 border-r border-line pr-3 sm:flex sm:gap-4 sm:pr-4">
                <span className="hidden 2xl:inline">{t("hud.active")} <b className="font-mono font-medium text-ink">{metrics.tasks_active}</b></span>
                <span data-testid="hud-approvals">{t("hud.approvalsShort")} <b className={`font-mono font-medium ${metrics.approvals_pending ? "text-warn" : "text-ink"}`}>{metrics.approvals_pending}</b></span>
                <span data-testid="hud-cost">{t("common.cost")} <b className="font-mono font-medium text-ink">{fmtUsd(metrics.cost_usd)}</b></span>
              </div>
            )}
            <ActivationChecklist />
            <TourLauncher section={section} />
            <span data-tour="shell.coins" className="inline-flex"><CoinCounter /></span>
            {MOCK && <span data-testid="mock-badge" className="hidden rounded bg-violet-500/15 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-violet-700 sm:inline">Mock</span>}
            <span className="hidden items-center gap-1.5 whitespace-nowrap font-medium text-ink2 sm:flex">
              <span className={`h-1.5 w-1.5 rounded-full ${connected ? "bg-ok" : "animate-pulse bg-err"}`} />
              {connected ? t("hud.live") : t("hud.reconnecting")}
            </span>
            <AdminMenu />
          </div>
        </header>

        <TrialBanner />
        <ControlsBanner />
        <ProviderAlert />
        <CostOverlays />
        <main className="relative min-h-0 flex-1">
          <RouteCurtain section={section} />
          <div key={section === "dashboard" ? "office" : section} className="ac-page absolute inset-0">{children}</div>
          {/* Files in the conversation: viewer next to the chat (left of the agent panel when it is open) */}
          {section !== "drive" && viewerOpen && (
            <div data-testid="file-viewer-host" className="pointer-events-none absolute bottom-0 top-0 z-[35] max-w-full" style={{ right: section === "office" && selected ? "var(--panel-w, 420px)" : 0 }}>
              <FileViewerPanel />
            </div>
          )}
        </main>
      </div>
      <Toaster />
      <TourOverlay />
      {section === "office" && <WelcomeOverlay />}
    </div>
  );
}
