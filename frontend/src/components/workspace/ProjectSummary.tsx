"use client";
import { useEffect } from "react";
import { useArtifacts } from "@/lib/artifacts";
import { useT } from "@/lib/i18n";
import { Progress, useAgentColor, useAgentName } from "@/components/ui";
import { KindIcon } from "./kindMeta";

/** Project summary tab (sec. 2.3b): deliverables with progress/build state, and who references whom. */
export function ProjectSummary({ pid, deskId }: { pid: string; deskId: string }) {
  const { t } = useT();
  const project = useArtifacts((s) => s.projects[pid]);
  const load = useArtifacts((s) => s.loadProject);
  const openTab = useArtifacts((s) => s.openTab);
  const artifacts = useArtifacts((s) => s.artifacts);
  const color = useAgentColor();
  const name = useAgentName();
  useEffect(() => { load(pid); }, [pid, load]);
  if (!project) return <div className="p-6 text-center text-xs text-mute">…</div>;
  const total = project.deliverables.length ? Math.round(project.deliverables.reduce((s, d) => s + d.progress, 0) / project.deliverables.length) : 0;

  return (
    <div data-testid="ws-project-summary" data-progress={total} className="h-full overflow-y-auto bg-panel p-5">
      <div className="mx-auto max-w-[760px] space-y-4">
        <div>
          <div className="text-[10px] font-semibold uppercase tracking-wide text-mute">{t("workspace.project")}</div>
          <h2 className="font-display text-xl font-semibold text-ink">{project.name}</h2>
          <div className="mt-2 flex items-center gap-3"><div className="flex-1"><Progress value={total} color="#3451b2" /></div><span className="font-mono text-xs text-ink">{total}%</span></div>
        </div>
        <ul className="space-y-2">
          {project.deliverables.map((d) => (
            <li key={d.deliverable_id}>
              <button type="button" data-testid={`ws-deliverable-${d.deliverable_id}`} data-progress={d.progress} data-build-state={d.build_state}
                onClick={() => { const a = d.artifacts[0]; if (a) openTab(deskId, a.id); }}
                className="block w-full rounded-xl border border-line bg-panel2/50 p-3 text-left shadow-pop hover:border-accent">
                <div className="flex items-center justify-between gap-2">
                  <span className="flex items-center gap-1.5 text-[13px] font-semibold text-ink">{d.artifacts[0] && <KindIcon kind={d.artifacts[0].kind} size={14} />}{d.title}</span>
                  <span className="rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase" style={{ background: `${color(d.agent_id)}22`, color: color(d.agent_id) }}>{name(d.agent_id)}</span>
                </div>
                <div className="mt-2 flex items-center gap-3">
                  <div className="flex-1"><Progress value={d.progress} color={color(d.agent_id)} /></div>
                  <span className="font-mono text-[11px] text-mute">{d.progress}%</span>
                  <span className="text-[10px] font-semibold uppercase text-mute">{t(`artifact.build.${d.build_state}`)}</span>
                </div>
              </button>
            </li>
          ))}
        </ul>
        {project.links.length > 0 && (
          <div>
            <h3 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("workspace.linkedArtifacts")}</h3>
            <ul className="space-y-1">
              {project.links.map((l) => (
                <li key={l.id} className="flex flex-wrap items-center gap-1.5 text-[12px] text-ink">
                  <button type="button" onClick={() => openTab(deskId, l.from)} className="font-semibold text-accent hover:underline">{artifacts[l.from]?.title ?? l.from}</button>
                  <span className="text-mute">{t(`art.relation.${l.relation}`)}</span>
                  <button type="button" onClick={() => openTab(deskId, l.to, l.anchor ?? null)} className="font-semibold text-accent hover:underline">{artifacts[l.to]?.title ?? l.to}</button>
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
    </div>
  );
}
