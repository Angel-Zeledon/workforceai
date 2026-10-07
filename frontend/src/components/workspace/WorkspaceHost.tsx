"use client";
import { useEffect } from "react";
import { useArtifacts, type DeskSize } from "@/lib/artifacts";
import { useT } from "@/lib/i18n";
import { stateMeta } from "@/lib/meta";
import { useStore } from "@/lib/store";
import { useAgentView } from "@/lib/preferences";
import { AgentPanel } from "../AgentPanel";
import { DeskView } from "./DeskView";
import { Icon } from "../icons";

const WIDTH: Record<DeskSize, string> = { dock: "420px", expanded: "min(1180px, 82vw)", full: "100vw" };

/**
 * Container of the selected agent's side panel. `dock` is the existing AgentPanel unchanged; `expanded`/`full` swap in the
 * desk (tabs of artifacts). Publishes --panel-w so the command bar and floating controls keep their offset.
 */
export function WorkspaceHost({ id }: { id: string }) {
  const { t } = useT();
  const size = useArtifacts((s) => s.size);
  const deskId = useArtifacts((s) => s.deskId);
  const setSize = useArtifacts((s) => s.setSize);
  const select = useStore((s) => s.select);
  const agent = useStore((s) => s.agents[id]);
  const view = useAgentView(id);

  // a newly selected agent always starts docked
  useEffect(() => { if (useArtifacts.getState().deskId !== id) useArtifacts.setState({ deskId: id, size: "dock" }); }, [id]);
  const current: DeskSize = deskId === id ? size : "dock";

  useEffect(() => {
    const root = document.documentElement;
    root.style.setProperty("--panel-w", current === "full" ? "0px" : WIDTH[current]);
    return () => { root.style.removeProperty("--panel-w"); };
  }, [current]);

  const sm = stateMeta(agent?.state ?? "idle");
  return (
    <div data-testid="ws-host" data-size={current} data-desk={id} className="pointer-events-auto h-full max-w-full" style={{ width: WIDTH[current], transition: "width 360ms cubic-bezier(.2,.8,.2,1)" }}>
      {current === "dock" ? <AgentPanel id={id} /> : (
        <aside className="flex h-full w-full border-l border-line bg-panel shadow-float">
          <div className="ac-pop flex w-16 shrink-0 flex-col items-center gap-3 border-r border-line bg-panel2 py-3">
            <div title={view.name} className="flex h-10 w-10 items-center justify-center rounded-lg font-display text-base font-semibold text-white" style={{ background: view.color }}>{view.name.slice(0, 1)}</div>
            <div className="max-w-[56px] truncate text-center text-[10px] font-semibold text-ink">{view.name.split(" ")[0]}</div>
            <span title={sm.label} className="h-2.5 w-2.5 rounded-full" style={{ background: sm.color }} />
            <div className="mt-auto flex flex-col items-center gap-2">
              <button type="button" data-testid="ws-size-full" title={t("workspace.fullscreen")} aria-label={t("workspace.fullscreen")} onClick={() => setSize(current === "full" ? "expanded" : "full")} className="rounded-md border border-line bg-panel p-1.5 text-ink hover:border-accent"><Icon name={current === "full" ? "shrink" : "expand"} size={14} /></button>
              <button type="button" data-testid="ws-size-dock" title={t("workspace.collapse")} aria-label={t("workspace.collapse")} onClick={() => setSize("dock")} className="rounded-md border border-line bg-panel p-1.5 text-ink hover:border-accent"><Icon name="chevron" size={14} /></button>
              <button type="button" data-testid="ws-close" title={t("common.close")} aria-label={t("common.close")} onClick={() => select(null)} className="rounded-md border border-line bg-panel px-2 text-sm font-semibold text-ink hover:text-err"><Icon name="x" size={14} /></button>
            </div>
          </div>
          <DeskView deskId={id} />
        </aside>
      )}
    </div>
  );
}
