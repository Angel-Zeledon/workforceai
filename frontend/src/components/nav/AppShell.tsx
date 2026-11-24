"use client";
import { AgentMark } from "../brand/AgentMark";
import { ProviderAlert } from "./ProviderAlert";
import { useEffect, useRef, useState, type ReactNode } from "react";
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
import { usePreferences, useOffice3d } from "@/lib/preferences";
import { useOfficeView } from "@/lib/officeView";
import { isListShown } from "@/lib/officeViewLogic";
import { orgApi } from "@/lib/orgConfigApi";
import { navGroups, setNavigator, SECTIONS, sectionOf } from "@/lib/navBridge";
import { usePendingCount } from "@/lib/pending";
import { useNeedsYouBadge } from "@/lib/useNeedsYouBadge";
import { useViewMode } from "@/lib/viewMode";
import { useAudience } from "@/lib/audience";
import { useDepartmentLanding } from "../dept/useDepartmentLanding";
import { useSession } from "@/lib/session";
import { ViewAllSwitch } from "./ViewAll";
import { AccountMenu } from "../admin/AdminMenu";
import { OrgIndicator } from "../auth/OrgIndicator";
import { ControlsBanner } from "../security/ControlsChrome";
import { CostOverlays } from "../cost/CostOverlays";
import { Icon, type IconName } from "../icons";
import { Toaster } from "./Toaster";
import { ShieldAlerts } from "../security/ShieldAlerts";
import { TourLauncher } from "../tour/TourLauncher";
import { TourOverlay } from "../tour/TourOverlay";
import { WelcomeOverlay } from "../onboarding/WelcomeOverlay";
import { RouteCurtain } from "./RouteCurtain";
import { useAwayHeartbeat } from "../away/AwayHost";
import { TrialBanner } from "./TrialBanner";
import { ActivationChecklist } from "./ActivationChecklist";
import { OfficeSelect } from "./OfficeSelect";
import { useCurrentOffice, useCurrentOfficeBoot } from "@/lib/currentOffice";

const COLLAPSE_KEY = "wf.sidebar.collapsed";
const MORE_KEY = "wf.sidebar.more";

function NavLink({ href, icon, label, testid, active, collapsed, onNavigate, badge }: {
  href: string; icon: IconName; label: string; testid: string; active: boolean; collapsed: boolean; onNavigate?: () => void; badge?: number | string;
}) {
  return (
    <Link href={href} prefetch={false} data-testid={testid} data-active={active} aria-current={active ? "page" : undefined} title={collapsed ? label : undefined} onClick={onNavigate}
      className={`group relative flex min-h-9 items-center gap-3 rounded-md px-3 py-1.5 text-[13px] font-medium transition-colors duration-150 ${active ? "bg-accent-soft font-semibold text-accent" : "text-ink2 hover:bg-panel2 hover:text-ink"} ${collapsed ? "justify-center px-0" : ""}`}>
      {active && <span aria-hidden className="absolute inset-y-1.5 left-0 w-[3px] rounded-r bg-accent" />}
      <Icon name={icon} size={18} className="shrink-0 transition-transform duration-150 group-hover:scale-110" />
      {!collapsed && <span className="min-w-0 flex-1 truncate">{label}</span>}
      {!collapsed && badge ? <span className="rounded-full bg-accent px-1.5 text-[10px] font-semibold text-white">{badge}</span> : null}
    </Link>
  );
}

function Sidebar({ section, collapsed, onToggle, onNavigate }: { section: string; collapsed: boolean; onToggle?: () => void; onNavigate?: () => void }) {
  const { t } = useT();
  // the shop decorates the animated office: it is in the menu only while that is on (and the server has coins)
  const office3d = useOffice3d();
  const shopAvailable = useRewards((s) => s.available) && office3d;
  const pending = useNeedsYouBadge(); // "Te necesitan": everything that waits for this person, from the server
  const company = useAudience() === "company";
  const groups = navGroups({ shopAvailable, company });
  const [moreOpen, setMoreOpen] = useState(false);
  useEffect(() => { try { setMoreOpen(localStorage.getItem(MORE_KEY) === "1"); } catch { /* private mode */ } }, []);
  const toggleMore = () => setMoreOpen((o) => { try { localStorage.setItem(MORE_KEY, o ? "0" : "1"); } catch { /* ignore */ } return !o; });
  const isActive = (id: string) => section === id || (id === "office" && section === "dashboard");
  const link = (s: (typeof groups)[number]["items"][number]) => (
    <NavLink key={s.id} href={s.href} icon={s.icon} label={t(s.label)} testid={`nav-${s.id}`} active={isActive(s.id)} collapsed={collapsed} onNavigate={onNavigate} badge={s.id === "pending" ? pending : undefined} />
  );
  return (
    <nav aria-label={t("nav.main")} data-testid="sidebar" data-collapsed={collapsed} className="wf-nav flex h-full flex-col border-r border-line bg-panel text-ink">
      {collapsed && onToggle ? (
        // collapsed: the logo is also the way back to the full menu, so it never depends on the footer being in view
        <button type="button" onClick={onToggle} data-testid="sidebar-expand-logo" title={t("nav.expand")} aria-label={t("nav.expand")}
          className="flex h-14 w-full shrink-0 items-center justify-center border-b border-line transition hover:bg-panel2">
          <AgentMark size={32} tone="dark" className="shrink-0" />
        </button>
      ) : (
        <div className={`flex h-14 shrink-0 items-center gap-2.5 border-b border-line ${collapsed ? "justify-center" : "px-4"}`}>
          <AgentMark size={32} tone="dark" className="shrink-0" />
          {!collapsed && <span className="truncate text-[14px] font-semibold tracking-tight">AI Workforce OS</span>}
        </div>
      )}
      <div className="wf-nav-main min-h-0 flex-1 overflow-y-auto p-2.5">
        {groups.filter((g) => g.id !== "bottom").map((g) => (
          <div key={g.id} role="group" aria-label={g.label ? t(g.label) : undefined} data-testid={`nav-group-${g.id}`} className="mb-2 space-y-0.5 last:mb-0">
            {g.label && g.id !== "more" && (collapsed
              ? <div aria-hidden className="mx-3 my-2 border-t border-line first:hidden" />
              : <div className="px-3 pb-0.5 pt-1 text-[10px] font-semibold uppercase tracking-[0.1em] text-mute">{t(g.label)}</div>)}
            {g.id === "more" ? (
              <>
                {!collapsed && (
                  <button type="button" data-testid="nav-more-toggle" aria-expanded={moreOpen} onClick={toggleMore}
                    className="flex min-h-9 w-full items-center gap-3 rounded-md px-3 py-1.5 text-[13px] font-medium text-ink2 transition hover:bg-panel2 hover:text-ink">
                    <Icon name="chevron" size={16} className={`shrink-0 transition-transform duration-150 ${moreOpen ? "rotate-90" : ""}`} />
                    <span className="min-w-0 flex-1 truncate text-left">{t("nav.group.more")}</span>
                  </button>
                )}
                {(moreOpen || collapsed || g.items.some((s) => isActive(s.id))) && g.items.map(link)}
              </>
            ) : g.items.map(link)}
          </div>
        ))}
      </div>
      <div className="wf-nav-foot shrink-0 space-y-0.5 border-t border-line p-2.5">
        {(groups.find((g) => g.id === "bottom")?.items ?? []).map(link)}
        <ViewAllSwitch tone="light" compact={collapsed} />
        {onToggle && (
          <button type="button" onClick={onToggle} data-testid="sidebar-toggle" title={collapsed ? t("nav.expand") : t("nav.collapse")} aria-label={collapsed ? t("nav.expand") : t("nav.collapse")}
            className={`flex min-h-9 w-full items-center gap-3 rounded-md px-3 py-1.5 text-[12px] text-mute transition hover:bg-panel2 hover:text-ink ${collapsed ? "justify-center px-0" : ""}`}>
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
  const office3d = useOffice3d();
  const section = sectionOf(pathname);
  // the view Oficina is in (Lista or Oficina 3D, remembered on this device): tours and the store follow what is on screen
  const { view, setView } = useOfficeView();
  const listShown = isListShown(section, view);
  const shown = section === "office" && view === "list" ? "dashboard" : section;
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
  useAwayHeartbeat(); // last seen, for "while you were away" (B3)
  useDepartmentLanding(pathname); // D2: a company account enters at its department's Drive (once per tab)
  useEffect(() => { hydratePrefs(); }, [hydratePrefs]);
  // the organization's language wins over the browser's until the person picks one in Settings
  // and the organization says whether new people start in simple mode (decision 3 of the owner)
  const uid = useSession((s) => s.user?.id ?? null);
  useEffect(() => {
    useViewMode.getState().init(uid);
    if (MOCK) return;
    orgApi.settings().then((o) => {
      usePreferences.getState().followOrgLocale(o.locale);
      useViewMode.getState().init(uid, o.view_mode_default ?? null);
    }).catch(() => { /* older backend or no permission */ });
  }, [uid]);
  useEffect(() => { try { setCollapsed(localStorage.getItem(COLLAPSE_KEY) === "1"); } catch { /* private mode */ } }, []);
  useEffect(() => { setDrawer(false); }, [pathname]);
  // Old stores keep reporting "is this screen open?" for code that still reads them: derive it from the URL.
  useEffect(() => {
    useDriveNav.setState({ open: section === "drive" });
    useSkillsNav.setState({ open: section === "skills" });
    useShopNav.setState({ open: section === "shop" });
    useProjects.setState({ open: section === "projects" });
    useStore.getState().setMode(shown === "dashboard" ? "dashboard" : "office");
  }, [section, shown]);

  const toggleCollapsed = () => setCollapsed((c) => { try { localStorage.setItem(COLLAPSE_KEY, c ? "0" : "1"); } catch { /* ignore */ } return !c; });
  // "Lista | Oficina 3D" is always there in Oficina: the 3D is chosen here, not in Ajustes
  const modeToggle = section === "office" || section === "dashboard";
  // the list is a view of the Office ("Lista | Oficina 3D"), so the title stays "Oficina"
  const title = t(SECTIONS.find((s) => s.id === section)?.label ?? "nav.office");
  const pendingCount = usePendingCount();
  // the office the screens show: in the header next to Lista | Oficina 3D (own row on the phone)
  useCurrentOfficeBoot();
  const { hasSelector: hasOffices, current: currentOffice, offices: officeList, choose: chooseOffice } = useCurrentOffice();
  // "Archivos de la oficina" (/d/<id>) and the header say the same office: a link into one picks it, picking another goes to it
  const deptId = section === "department" ? decodeURIComponent(pathname.split("/")[2] ?? "") : "";
  useEffect(() => { if (deptId && deptId !== currentOffice && officeList?.some((o) => o.id === deptId && o.can_enter)) chooseOffice(deptId); }, [deptId, officeList]); // eslint-disable-line react-hooks/exhaustive-deps
  const lastOffice = useRef(currentOffice);
  useEffect(() => {
    const changed = lastOffice.current !== currentOffice; lastOffice.current = currentOffice;
    if (changed && section === "department" && currentOffice !== "all" && currentOffice !== deptId) router.push(`/d/${encodeURIComponent(currentOffice)}`);
  }, [currentOffice]); // eslint-disable-line react-hooks/exhaustive-deps
  const officeSelect = hasOffices && ["office", "dashboard", "pending", "agents", "projects", "department"].includes(section);

  return (
    <div data-reduce={reduce ? "1" : "0"} className="flex h-dvh w-full overflow-hidden bg-bg text-ink">
      <aside className={`hidden shrink-0 transition-[width] duration-200 ease-out md:block ${collapsed ? "w-[64px]" : "w-[224px]"}`}>
        <Sidebar section={section} collapsed={collapsed} onToggle={toggleCollapsed} />
      </aside>
      {drawer && (
        <div className="fixed inset-0 z-[80] md:hidden" data-testid="nav-drawer">
          <button type="button" aria-label={t("nav.close")} className="absolute inset-0 bg-navy/50 ac-fade" onClick={() => setDrawer(false)} />
          <aside className="ac-slide-left absolute inset-y-0 left-0 w-[260px] max-w-[85vw] bg-panel pb-[env(safe-area-inset-bottom)] pt-[env(safe-area-inset-top)] shadow-float"><Sidebar section={section} collapsed={false} onNavigate={() => setDrawer(false)} /></aside>
        </div>
      )}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="relative z-[55] flex h-14 shrink-0 items-center justify-between gap-3 border-b border-line bg-panel px-3 sm:px-4">
          <div className="flex min-w-0 items-center gap-2 sm:gap-3">
            <button type="button" data-testid="nav-open" aria-label={t("nav.open")} title={t("nav.open")} onClick={() => setDrawer(true)} className="-ml-1 inline-flex h-10 w-10 shrink-0 items-center justify-center rounded-md text-ink2 transition hover:bg-panel2 md:hidden"><Icon name="menu" size={20} /></button>
            {/* on the phone the Office title makes room for the "Vista 3D / Vista lista" selector (the menu already marks Oficina) */}
            <div data-tour="shell.title" className={`min-w-[72px] leading-tight ${modeToggle ? "hidden sm:block" : ""}`}>
              <div className="truncate text-[14px] font-semibold tracking-tight">{title}</div>
              <OrgIndicator className="truncate text-[10.5px] text-mute" />
            </div>
            {modeToggle && (
              <div role="group" aria-label={t("mode.switchTitle")} data-tour="shell.mode" data-testid="mode-toggle" data-mode={listShown ? "dashboard" : "office"}
                className="ml-0.5 flex shrink-0 rounded-lg border border-line bg-panel2 p-0.5 text-[13px] font-medium sm:ml-1">
                {([["list", "mode.list", "list"], ["3d", "mode.office3d", "office"]] as const).map(([v, key, icon]) => {
                  const on = (v === "list") === listShown;
                  return (
                    <button key={v} type="button" data-testid={v === "list" ? "mode-list" : "mode-3d"} aria-pressed={on} title={t(`${key}.title`)}
                      onClick={() => { setView(v); if (v === "3d" && section === "dashboard") router.push("/"); }}
                      className={`flex min-h-10 items-center gap-1.5 rounded-md px-2.5 py-1.5 transition-colors sm:min-h-9 sm:px-3 ${on ? "bg-panel text-accent shadow-pop ring-1 ring-line" : "text-ink2 hover:text-ink"}`}>
                      <Icon name={icon} size={14} />
                      <span className="sm:hidden">{t(`${key}.short`)}</span>
                      <span className="hidden sm:inline">{t(key)}</span>
                    </button>
                  );
                })}
              </div>
            )}
            {officeSelect && <span data-testid="office-select-desktop" className="hidden sm:block"><OfficeSelect /></span>}
          </div>
          <div className="flex shrink-0 items-center gap-x-2 text-[11px] text-mute sm:gap-x-3">
            {metrics && (
              <div data-tour="shell.metrics" className="hidden items-center gap-3 border-r border-line pr-3 2xl:flex 2xl:gap-4 2xl:pr-4">
                <span className="hidden 2xl:inline">{t("hud.active")} <b className="font-mono font-medium text-ink">{metrics.tasks_active}</b></span>
                <span data-testid="hud-approvals">{t("hud.approvalsShort")} <b className={`font-mono font-medium ${pendingCount ? "text-warn" : "text-ink"}`}>{pendingCount}</b></span>
                <span data-testid="hud-cost">{t("common.cost")} <b className="font-mono font-medium text-ink">{fmtUsd(metrics.cost_usd)}</b></span>
              </div>
            )}
            <ActivationChecklist />
            <span className="contents max-[399px]:hidden"><TourLauncher section={shown === "shop" && !office3d ? null : shown} /></span>
            {/* phone: the List / 3D selector needs the room; the coins are in the shop */}
            {office3d && <span data-tour="shell.coins" className="hidden sm:inline-flex"><CoinCounter /></span>}
            {MOCK && <span data-testid="mock-badge" className="hidden rounded border border-accent/30 bg-accent-soft px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-accent sm:inline">Mock</span>}
            <span data-testid="hud-connection" data-connected={connected ? "1" : "0"} className={`hidden items-center gap-1.5 whitespace-nowrap sm:flex ${connected ? "font-medium text-ink2" : "text-[10.5px] font-normal text-mute"}`}>
              <span className={`rounded-full ${connected ? "h-1.5 w-1.5 bg-ok" : "h-1 w-1 bg-line-strong"}`} />
              {connected ? t("hud.live") : t("hud.reconnecting")}
            </span>
            <AccountMenu />
          </div>
        </header>

        {officeSelect && <div className="flex shrink-0 items-center gap-2 border-b border-line bg-panel px-3 py-1.5 sm:hidden"><OfficeSelect block /></div>}
        <TrialBanner />
        <ControlsBanner />
        <ProviderAlert />
        <CostOverlays />
        <main className="relative min-h-0 flex-1">
          <RouteCurtain section={section} />
          <div key={section === "dashboard" ? "office" : section} className="ac-page absolute inset-0">{children}</div>
          {/* Files in the conversation: viewer next to the chat (left of the agent panel when it is open) */}
          {section !== "drive" && section !== "chats" && viewerOpen && ( /* Chats mounts its own viewer beside the conversation */
            <div data-testid="file-viewer-host" className="pointer-events-none absolute bottom-0 top-0 z-[35] max-w-full" style={{ right: section === "office" && selected ? "var(--panel-w, 420px)" : 0 }}>
              <FileViewerPanel />
            </div>
          )}
        </main>
      </div>
      <Toaster />
      <ShieldAlerts />
      <TourOverlay />
      {section === "office" && <WelcomeOverlay />}
    </div>
  );
}
