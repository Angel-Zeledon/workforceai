"use client";
import { Suspense, useEffect } from "react";
import { useRouter, useSearchParams } from "next/navigation";
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
import { PageSkeleton } from "./ui2";

/** Thin client wrappers so every route renders one existing screen; query strings carry the deep links. */
export const OfficePage = () => <OfficeView />;
export const DashboardPage = () => <Dashboard />;
export const ShopPage = () => <ShopView />;
export const SettingsPage = () => <SettingsView />;
export const ChatsPage = ({ agentId }: { agentId?: string }) => <ChatsView agentId={agentId} />;

function FilesInner() {
  const path = useSearchParams().get("path") || "/";
  return <DriveView key={path} initialPath={path} />;
}
export const FilesPage = () => <Suspense fallback={<PageSkeleton />}><FilesInner /></Suspense>;

function SkillsInner() {
  const id = useSearchParams().get("id");
  const select = useSkillsNav((s) => s.select);
  useEffect(() => { if (id) select(id); }, [id, select]);
  return <SkillsView />;
}
export const SkillsPage = () => <Suspense fallback={<PageSkeleton />}><SkillsInner /></Suspense>;

function ProjectsInner() {
  const p = useSearchParams().get("p");
  const openProject = useProjects((s) => s.openProject);
  useEffect(() => { if (p) openProject(p); }, [p, openProject]);
  return <ProjectsView />;
}
export const ProjectsPage = () => <Suspense fallback={<PageSkeleton />}><ProjectsInner /></Suspense>;

/** /office is an alias of the home (the office lives at "/"). */
export function OfficeRedirect() {
  const router = useRouter();
  useEffect(() => { router.replace("/"); }, [router]);
  return null;
}
