"use client";
import dynamic from "next/dynamic";
import { useEffect, useState } from "react";
import { useStore } from "@/lib/store";
import { useViewAll } from "@/lib/viewMode";
import { Icon } from "../icons";
import { useT } from "@/lib/i18n";
import { useFileViewer } from "@/lib/fileView";
import { ActivityFeed } from "../ActivityFeed";
import { WorkspaceHost } from "../workspace/WorkspaceHost";
import { ApprovalsTray } from "../ApprovalsTray";
import { CommandBar } from "../CommandBar";
import { ProjectPill } from "../projects/ProjectPill";
import { FirstRunHint } from "../nav/ui2";
import { EditOfficeButton, OfficeEditor } from "./OfficeEditor";
import { OfficePicker } from "./OfficePicker";
import { useOfficeLayout } from "@/lib/officeLayout";
import { PresenceHost } from "../presence/PresenceHost";
import { AwayButton, AwayHost } from "../away/AwayHost";

const OfficeScene = dynamic(() => import("./OfficeScene"), {
  ssr: false,
  loading: () => <div className="flex h-full items-center justify-center text-sm font-medium text-mute">…</div>,
});

const ACTIVITY_KEY = "aiw.office.activity.collapsed";
/** The live-activity window can be folded into a small pill; the choice is remembered in this browser. */
function useActivityCollapsed(): [boolean, (v: boolean) => void] {
  const viewAll = useViewAll();
  const [collapsed, setCollapsed] = useState(false);
  // simple mode: "Actividad en vivo" starts folded until the person opens it (their choice is remembered)
  useEffect(() => { try { const v = localStorage.getItem(ACTIVITY_KEY); setCollapsed(v === null ? !viewAll : v === "1"); } catch { /* private mode */ } }, [viewAll]);
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
      <div className="pointer-events-none absolute left-4 top-[max(1rem,env(safe-area-inset-top))] w-[330px] max-w-[calc(100vw-32px)] sm:max-w-[40vw]">
        {folded ? (
          <button type="button" data-testid="activity-expand" onClick={() => setFolded(false)}
            className="pointer-events-auto hidden items-center gap-2 rounded-full border border-line bg-panel/95 px-3.5 py-1.5 text-[11.5px] font-semibold text-ink2 shadow-soft backdrop-blur transition hover:bg-panel2 sm:inline-flex">
            <span className="h-2 w-2 rounded-full bg-emerald-500" aria-hidden />{t("hud.liveActivity")}<Icon name="chevron" size={13} className="rotate-90 text-mute" />
          </button>
        ) : (
          <div data-tour="office.activity" className="pointer-events-auto hidden overflow-hidden rounded-xl border border-line bg-panel/95 shadow-soft backdrop-blur sm:block">
            <div className="flex items-center justify-between border-b border-line px-3 py-2">
              <div className="flex items-center gap-2">
                <span className="relative flex h-2 w-2" aria-hidden>
                  <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-60 motion-reduce:hidden" />
                  <span className="relative inline-flex h-2 w-2 rounded-full bg-emerald-500" />
                </span>
                <span className="text-[12px] font-semibold text-ink">{t("hud.liveActivity")}</span>
              </div>
              <button type="button" data-testid="activity-collapse" onClick={() => setFolded(true)} aria-label={t("hud.activityHide")} title={t("hud.activityHide")}
                className="-mr-1 rounded-md p-1 text-mute transition hover:bg-panel2 hover:text-ink"><Icon name="chevron" size={14} className="-rotate-90" /></button>
            </div>
            <div className="max-h-[min(340px,45vh)] overflow-y-auto px-2 py-1">
              <ActivityFeed limit={7} compact />
            </div>
          </div>
        )}
        <ProjectPill />
      </div>
      {/* Right above the command bar it talks about, clear of the lighting controls at the top. */}
      <div className="pointer-events-none absolute bottom-[140px] left-1/2 z-30 hidden w-[min(460px,40vw)] -translate-x-1/2 lg:block">
        <FirstRunHint id="office" title={t("hint.office.title")} body={t("hint.office.body")} />
      </div>
      <div data-tour="office.approvals" className="pointer-events-none absolute bottom-[calc(96px+env(safe-area-inset-bottom))] left-4 max-w-[calc(100%-2rem)]"><ApprovalsTray /></div>
      {/* One bar to type at a time: with an agent's panel open you write in its chat, so the office bar steps aside
          (hidden, not unmounted: a half-written message is still there when the panel closes). */}
      <div data-testid="office-command-host" data-hidden={selected ? "1" : "0"} aria-hidden={selected ? true : undefined} className={`pointer-events-none absolute inset-x-0 bottom-[max(1rem,env(safe-area-inset-bottom))] z-40 justify-center px-4 md:pl-[var(--cb-l)] md:pr-[var(--cb-r)] ${selected ? "hidden" : "flex"}`} style={{ "--cb-r": `calc(${selected ? "var(--panel-w, 420px)" : "16px"} + ${viewerOpen ? "min(560px, 100vw)" : "0px"})`, "--cb-l": pendingApprovals ? "352px" : "16px" } as React.CSSProperties}>
        <CommandBar />
      </div>
      {/* Same box as the office scene but above the chat: in-scene popups that must stay clickable (approvals desk card). */}
      <div id="office-popups" className="pointer-events-none absolute inset-0 z-20" />
      <div data-tour="office.lighting" className={`pointer-events-none absolute right-[var(--hud-r)] top-[max(1rem,env(safe-area-inset-top))] z-40 flex max-w-[calc(100%-2rem)] items-start gap-2 ${selected ? "max-md:hidden" : ""}`} style={{ "--hud-r": `calc(${selected ? "var(--panel-w, 420px) + 16px" : "16px"} + ${viewerOpen ? "min(560px, 100vw)" : "0px"})` } as React.CSSProperties}>
        <OfficePicker />
        <AwayButton />
        <EditOfficeButton />
      </div>
      <PresenceHost /> {/* human presence: who is online, status, "start conversation" */}
      <AwayHost /> {/* "while you were away" sheet and the office timelapse (B3) */}
      {selected && <div data-tour="office.agentPanel" className="pointer-events-none absolute bottom-0 right-0 top-0 z-30"><WorkspaceHost id={selected} /></div>}
      {!selected && <div className="pointer-events-none absolute bottom-4 right-4 hidden rounded-md border border-line bg-panel/80 px-3 py-1.5 text-[10px] font-semibold text-mute md:block">{t("hud.cameraHint")}</div>}
    </>
  );
}
