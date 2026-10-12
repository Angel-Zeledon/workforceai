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

/** Thin client wrappers so every route renders one existing screen; query strings carry the deep links. */
export const OfficePage = () => <OfficeView />;
export const DashboardPage = () => <Dashboard />;
export const ShopPage = () => <Hinted id="shop"><ShopView /></Hinted>;
export const SettingsPage = () => <SettingsView />;
export const ChatsPage = ({ agentId }: { agentId?: string }) => <ChatsView agentId={agentId} />;

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

/** /office is an alias of the home (the office lives at "/"). */
export function OfficeRedirect() {
  const router = useRouter();
  useEffect(() => { router.replace("/"); }, [router]);
  return null;
}
