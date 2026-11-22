"use client";
import { Suspense, useEffect, type ReactNode } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useT } from "@/lib/i18n";
import { useSkillsNav } from "@/lib/skills";
import { useProjects } from "@/lib/projects/store";
import { OfficeView } from "../office/OfficeView";
import { Dashboard } from "../dashboard/Dashboard";
import { DriveView } from "../drive/DriveView";
import { SkillsView } from "../skills/SkillsView";
import { ProjectsView } from "../projects/ProjectsView";
import { ShopView } from "../shop/ShopView";
import { ChatsView } from "../chats/ChatsView";
import { SettingsView } from "../settings/SettingsView";
import { FirstRunHint, PageSkeleton } from "./ui2";
import { ApprovalsInbox } from "../approvals/ApprovalsInbox";
import { ApprovalsMobile } from "../approvals/ApprovalsMobile";
import { CustomerDistrict } from "../customers/CustomerDistrict";
import { GoalsView } from "../goals/GoalsView";
import { ConnectionsView } from "../connections/ConnectionsView";
import { TeamsGallery } from "../teams/TeamsGallery";
import { useConnections } from "@/lib/connections/store";
import { useBootConnections } from "../security/ControlsChrome";
import { useStore } from "@/lib/store";
import { useOffice3d, useOffice3dReady } from "@/lib/preferences";
import { WorkspaceHost } from "../workspace/WorkspaceHost";
import { ShopOff } from "../shop/ShopOff";

/** Page body with a first-run hint strip on top (dismissed once, remembered in localStorage). */
function Hinted({ id, children }: { id: string; children: ReactNode }) {
  const { t } = useT();
  return (
    <div className="flex h-full flex-col">
      <FirstRunHint id={id} title={t(`hint.${id}.title`)} body={t(`hint.${id}.body`)} className="mx-4 mt-3 shrink-0" />
      <div className="relative min-h-0 flex-1">{children}</div>
    </div>
  );
}

/**
 * The office without the animation: the list view, with the agent's panel on the right when one is picked (every
 * "open this agent" in the app selects it and goes home). The 3D scene is never loaded here.
 */
function ListHome() {
  const selected = useStore((s) => s.selectedAgentId);
  return (
    <div data-testid="list-home" className="relative h-full">
      <Dashboard />
      {selected && <div className="pointer-events-none absolute bottom-0 right-0 top-0 z-30"><WorkspaceHost id={selected} /></div>}
    </div>
  );
}

/** Thin client wrappers so every route renders one existing screen; query strings carry the deep links. */
export function OfficePage() {
  const ready = useOffice3dReady();
  const on = useOffice3d();
  if (!ready) return <PageSkeleton />;
  return on ? <OfficeView /> : <ListHome />;
}
export function DashboardPage() {
  const on = useOffice3d();
  return on ? <Dashboard /> : <ListHome />;
}
/** The shop decorates the 3D office: without the animation it only explains how to turn it on. */
export function ShopPage() {
  const ready = useOffice3dReady();
  const on = useOffice3d();
  if (!ready) return <PageSkeleton />;
  return on ? <Hinted id="shop"><ShopView /></Hinted> : <ShopOff />;
}
export const SettingsPage = () => <Suspense fallback={<PageSkeleton />}><SettingsView /></Suspense>;
export const ChatsPage = ({ agentId }: { agentId?: string }) => <ChatsView agentId={agentId ? decodeURIComponent(agentId) : undefined} />;

function FilesInner() {
  const path = useSearchParams().get("path") || "/";
  return <Hinted id="files"><DriveView key={path} initialPath={path} /></Hinted>;
}
export const FilesPage = () => <Suspense fallback={<PageSkeleton />}><FilesInner /></Suspense>;

function SkillsInner() {
  const id = useSearchParams().get("id");
  const select = useSkillsNav((s) => s.select);
  useEffect(() => { if (id) select(id); }, [id, select]);
  return <Hinted id="skills"><SkillsView /></Hinted>;
}
export const SkillsPage = () => <Suspense fallback={<PageSkeleton />}><SkillsInner /></Suspense>;

function ProjectsInner() {
  const p = useSearchParams().get("p");
  const openProject = useProjects((s) => s.openProject);
  useEffect(() => { if (p) openProject(p); }, [p, openProject]);
  return <Hinted id="projects"><ProjectsView /></Hinted>;
}
export const ProjectsPage = () => <Suspense fallback={<PageSkeleton />}><ProjectsInner /></Suspense>;

/** Pendientes inside the app frame (sidebar, top bar, Back works): one card at a time or the list. */
export const PendingPage = ({ view, focusId }: { view: "inbox" | "list"; focusId?: string }) => (
  <div data-testid="pending-page" data-view={view} className="h-full overflow-y-auto bg-bg">
    {view === "list" ? <ApprovalsMobile focusId={focusId} /> : <ApprovalsInbox />}
  </div>
);

/** Clientes: the customer district, now with its own place in the menu. */
export const ClientsPage = () => (
  <div className="h-full overflow-y-auto bg-bg"><div className="mx-auto w-full max-w-6xl p-4 md:p-6"><CustomerDistrict /></div></div>
);

function TeamsInner() {
  const q = useSearchParams();
  return <TeamsGallery team={q.get("team") || undefined} pack={q.get("pack") || undefined} template={q.get("template") || undefined} />;
}
/** Equipos: ready-to-hire business teams with a preview (F2). `?team=` opens one, `?pack=` suits an onboarding pack. */
export const TeamsPage = () => <Suspense fallback={<PageSkeleton />}><TeamsInner /></Suspense>;
/** Objetivos: the company goals that requests and projects can point to (F1). */
export const GoalsPage = () => (
  <div className="h-full overflow-y-auto bg-bg"><div className="mx-auto w-full max-w-4xl p-4 md:p-6"><GoalsView /></div></div>
);

/** Conexiones: the same screen that lived in Ajustes, unchanged (it manages its own scrolling). */
export function ConnectionsPage() {
  useBootConnections();
  const loaded = useConnections((s) => s.loaded);
  return <div data-testid="conn-screen" className="h-full overflow-hidden bg-bg">{loaded ? <ConnectionsView /> : <PageSkeleton />}</div>;
}

/** /office is an alias of the home (the office lives at "/"). */
export function OfficeRedirect() {
  const router = useRouter();
  useEffect(() => { router.replace("/"); }, [router]);
  return null;
}
