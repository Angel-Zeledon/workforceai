"use client";
import dynamic from "next/dynamic";
import { useEffect, useState } from "react";
import { useStore } from "@/lib/store";
import { Icon } from "../icons";
import { useT } from "@/lib/i18n";
import { useFileViewer } from "@/lib/fileView";
import { ActivityFeed } from "../ActivityFeed";
import { WorkspaceHost } from "../workspace/WorkspaceHost";
import { ApprovalsTray } from "../ApprovalsTray";
import { CommandBar } from "../CommandBar";
import { ProjectPill } from "../projects/ProjectPill";
import { FirstRunHint } from "../nav/ui2";
import { EditOfficeButton, LightButton, OfficeEditor } from "./OfficeEditor";
import { useOfficeLayout } from "@/lib/officeLayout";

const OfficeScene = dynamic(() => import("./OfficeScene"), {
  ssr: false,
  loading: () => <div className="flex h-full items-center justify-center text-sm font-medium text-mute">…</div>,
});

const ACTIVITY_KEY = "aiw.office.activity.collapsed";
/** The live-activity window can be folded into a small pill; the choice is remembered in this browser. */
function useActivityCollapsed(): [boolean, (v: boolean) => void] {
  const [collapsed, setCollapsed] = useState(false);
  useEffect(() => { try { setCollapsed(localStorage.getItem(ACTIVITY_KEY) === "1"); } catch { /* private mode */ } }, []);
  return [collapsed, (v) => { setCollapsed(v); try { localStorage.setItem(ACTIVITY_KEY, v ? "1" : "0"); } catch { /* private mode */ } }];
}

/** The 3D office with its overlays (activity, approvals, command bar, agent panel). */
export function OfficeView() {
  const { t } = useT();
  const selected = useStore((s) => s.selectedAgentId);
  const pendingApprovals = useStore((s) => Object.values(s.approvals).some((a) => a.status === "pending"));
  const viewerOpen = useFileViewer((s) => !!s.ref);
  const [folded, setFolded] = useActivityCollapsed();
  const editing = useOfficeLayout((s) => s.editing);
  if (editing) {
    // the editor owns the stage: the activity feed, trays and chat bar would only get in the way
    return (
      <>
        <div className="absolute inset-0 isolate"><OfficeScene /></div>
        <OfficeEditor />
      </>
    );
  }
  return (
    <>
      <div data-tour="office.scene" className="absolute inset-0 isolate"><OfficeScene /></div>
      <div className="pointer-events-none absolute left-4 top-4 w-[330px] max-w-[calc(100vw-32px)] sm:max-w-[40vw]">
        {folded ? (
          <button type="button" data-testid="activity-expand" onClick={() => setFolded(false)}
            className="pointer-events-auto hidden items-center gap-2 rounded-full border border-line bg-panel/95 px-3.5 py-1.5 text-[11.5px] font-semibold text-ink2 shadow-soft backdrop-blur transition hover:bg-panel2 sm:inline-flex">
            <Icon name="pulse" size={14} />{t("hud.liveActivity")}<Icon name="chevron" size={13} className="rotate-90 text-mute" />
          </button>
        ) : (
          <div data-tour="office.activity" className="hidden rounded-xl border border-line bg-panel/95 p-3 shadow-soft backdrop-blur sm:block">
            <div className="mb-2 flex items-center justify-between">
              <div className="text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute">{t("hud.liveActivity")}</div>
              <button type="button" data-testid="activity-collapse" onClick={() => setFolded(true)} aria-label={t("hud.activityHide")} title={t("hud.activityHide")}
                className="pointer-events-auto -mr-1 rounded-md p-1 text-mute transition hover:bg-panel2 hover:text-ink"><Icon name="chevron" size={14} className="-rotate-90" /></button>
            </div>
            <ActivityFeed limit={7} compact />
          </div>
        )}
        <ProjectPill />
      </div>
      {/* Right above the command bar it talks about, clear of the lighting controls at the top. */}
      <div className="pointer-events-none absolute bottom-[140px] left-1/2 z-30 hidden w-[min(460px,40vw)] -translate-x-1/2 lg:block">
        <FirstRunHint id="office" title={t("hint.office.title")} body={t("hint.office.body")} />
      </div>
      <div data-tour="office.approvals" className="pointer-events-none absolute bottom-[96px] left-4"><ApprovalsTray /></div>
      <div className="pointer-events-none absolute inset-x-0 bottom-4 z-40 flex justify-center px-4" style={{ paddingRight: `calc(${selected ? "var(--panel-w, 420px)" : "16px"} + ${viewerOpen ? "min(560px, 100vw)" : "0px"})`, paddingLeft: pendingApprovals ? 352 : 16 }}>
        <CommandBar />
      </div>
      {/* Same box as the office scene but above the chat: in-scene popups that must stay clickable (approvals desk card). */}
      <div id="office-popups" className="pointer-events-none absolute inset-0 z-20" />
      <div data-tour="office.lighting" className="pointer-events-none absolute right-4 top-4 z-40 flex items-start gap-2" style={{ right: `calc(${selected ? "var(--panel-w, 420px) + 16px" : "16px"} + ${viewerOpen ? "min(560px, 100vw)" : "0px"})` }}>
        <EditOfficeButton />
        <LightButton />
      </div>
      {selected && <div data-tour="office.agentPanel" className="pointer-events-none absolute bottom-0 right-0 top-0 z-30"><WorkspaceHost id={selected} /></div>}
      {!selected && <div className="pointer-events-none absolute bottom-4 right-4 hidden rounded-md border border-line bg-panel/80 px-3 py-1.5 text-[10px] font-semibold text-mute md:block">{t("hud.cameraHint")}</div>}
    </>
  );
}
