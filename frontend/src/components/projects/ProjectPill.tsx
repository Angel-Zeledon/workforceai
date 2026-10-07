"use client";
import { useEffect } from "react";
import { fmtNumber, useT } from "@/lib/i18n";
import { startProjectsRealtime, useProjects } from "@/lib/projects/store";
import { StatusPill } from "./shared";

/** Small pill for the office HUD: "Project · 37% · hand 2". Opens the Projects screen on that project. */
export function ProjectPill() {
  const { t } = useT();
  const projects = useProjects((s) => s.projects);
  const setOpen = useProjects((s) => s.setOpen);
  const openProject = useProjects((s) => s.openProject);
  useEffect(() => startProjectsRealtime(), []);
  const live = Object.values(projects).filter((p) => ["running", "waiting_human", "paused"].includes(p.status))
    .sort((a, b) => (b.started_at ?? "").localeCompare(a.started_at ?? ""))[0];
  if (!live) return null;
  const pct = live.tasks_total ? (live.tasks_done / live.tasks_total) * 100 : 0;
  return (
    <button type="button" data-testid="project-pill" data-progress={Math.round(pct)} onClick={() => { setOpen(true); openProject(live.id); }}
      className="pointer-events-auto mt-2 flex max-w-full items-center gap-2 rounded-md border border-line bg-panel/90 px-3 py-1.5 text-left text-[11px] font-semibold text-ink shadow-pop backdrop-blur transition hover:border-line-strong">
      <span className="truncate">{live.name_key ? t(live.name_key) : live.name}</span>
      <span className="font-mono text-mute">{fmtNumber(pct, 0)}%</span>
      {live.awaiting > 0 && <span className="text-amber-700">✋{live.awaiting}</span>}
      <StatusPill status={live.status} />
    </button>
  );
}
