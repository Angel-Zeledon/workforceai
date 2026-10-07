"use client";
import { isProjectTab, useArtifacts, type Desk } from "@/lib/artifacts";
import { useT } from "@/lib/i18n";
import { useAgentColor } from "@/components/ui";
import { KindIcon, KIND_COLOR } from "./kindMeta";
import { NewArtifactMenu } from "./NewArtifactMenu";
import { TabProgressRing } from "./TabProgressRing";

/** Folder-style tabs: kind icon, per-tab progress ring (deliverables), amber dot for pending proposals, "+" menu. */
export function WorkspaceTabs({ deskId, desk }: { deskId: string; desk: Desk }) {
  const { t } = useT();
  const artifacts = useArtifacts((s) => s.artifacts);
  const projects = useArtifacts((s) => s.projects);
  const setActive = useArtifacts((s) => s.setActive);
  const closeTab = useArtifacts((s) => s.closeTab);
  const focus = useArtifacts((s) => s.focus);
  const color = useAgentColor();
  const shown = (id: string) => (desk.split && desk.focusPane === 1 ? desk.split === id : desk.active === id);

  return (
    <div role="tablist" className="flex min-w-0 items-end gap-1 overflow-x-auto px-2 pt-2">
      {desk.tabs.map((id) => {
        if (isProjectTab(id)) {
          const pid = id.slice(8);
          const on = shown(id);
          return (
            <div key={id} role="tab" aria-selected={on} data-testid={`ws-tab-${id.replace(":", "-")}`} data-active={on} onClick={() => setActive(deskId, id)}
              className={`flex max-w-[190px] cursor-pointer items-center gap-1.5 rounded-t-xl border border-b-0 px-3 py-1.5 text-[12px] font-semibold ${on ? "border-line bg-panel text-ink" : "border-transparent text-mute hover:text-ink"}`}>
              <svg width={15} height={15} viewBox="0 0 20 20" fill="none" stroke="#5b6678" strokeWidth={1.7} strokeLinejoin="round"><path d="M3 5h5l2 2h7v9H3z" /></svg>
              <span className="truncate">{projects[pid]?.name ?? t("workspace.project")}</span>
            </div>
          );
        }
        const a = artifacts[id];
        if (!a) return null;
        const on = shown(id);
        const f = focus[id];
        const showRing = !!a.deliverable_id;
        return (
          <div key={id} role="tab" aria-selected={on} data-testid={`ws-tab-${id}`} data-kind={a.kind} data-active={on} onClick={() => setActive(deskId, id)}
            className={`group flex max-w-[210px] shrink-0 cursor-pointer items-center gap-1.5 rounded-t-xl border border-b-0 px-3 py-1.5 text-[12px] font-semibold ${on ? "border-line bg-panel text-ink" : "border-transparent text-mute hover:text-ink"}`}
            style={on ? { boxShadow: `inset 0 -3px 0 ${KIND_COLOR[a.kind]}` } : undefined}>
            <KindIcon kind={a.kind} size={14} />
            <span className="truncate">{a.title}</span>
            {showRing && <TabProgressRing id={id} progress={a.progress} buildState={a.build_state} />}
            {a.pending_proposals > 0 && <span title={t("workspace.proposals")} className="h-2 w-2 rounded-full bg-amber-500" />}
            {f && <span className="h-2 w-2 animate-pulse rounded-full" style={{ background: color(f.agent_id) }} />}
            <button type="button" data-testid={`ws-tab-close-${id}`} aria-label={t("common.close")} onClick={(e) => { e.stopPropagation(); closeTab(deskId, id); }} className="rounded-full px-1 text-[10px] text-mute opacity-0 hover:text-red-600 group-hover:opacity-100">&#10005;</button>
          </div>
        );
      })}
      <div className="shrink-0 self-center pl-1"><NewArtifactMenu deskId={deskId} /></div>
    </div>
  );
}
