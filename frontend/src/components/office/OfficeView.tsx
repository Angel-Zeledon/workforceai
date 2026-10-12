"use client";
import dynamic from "next/dynamic";
import { useStore } from "@/lib/store";
import { useT } from "@/lib/i18n";
import { useFileViewer } from "@/lib/fileView";
import { ActivityFeed } from "../ActivityFeed";
import { WorkspaceHost } from "../workspace/WorkspaceHost";
import { ApprovalsTray } from "../ApprovalsTray";
import { CommandBar } from "../CommandBar";
import { ProjectPill } from "../projects/ProjectPill";
import { LightingToggle } from "../OfficeSettings";
import { FirstRunHint } from "../nav/ui2";

const OfficeScene = dynamic(() => import("./OfficeScene"), {
  ssr: false,
  loading: () => <div className="flex h-full items-center justify-center text-sm font-medium text-mute">…</div>,
});

/** The 3D office with its overlays (activity, approvals, command bar, agent panel). */
export function OfficeView() {
  const { t } = useT();
  const selected = useStore((s) => s.selectedAgentId);
  const pendingApprovals = useStore((s) => Object.values(s.approvals).some((a) => a.status === "pending"));
  const viewerOpen = useFileViewer((s) => !!s.ref);
  return (
    <>
      <div data-tour="office.scene" className="absolute inset-0 isolate"><OfficeScene /></div>
      <div className="pointer-events-none absolute left-4 top-4 w-[330px] max-w-[calc(100vw-32px)] sm:max-w-[40vw]">
        <div data-tour="office.activity" className="hidden rounded-xl border border-line bg-panel/95 p-3 shadow-soft backdrop-blur sm:block">
          <div className="mb-2 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute">{t("hud.liveActivity")}</div>
          <ActivityFeed limit={7} compact />
        </div>
        <ProjectPill />
      </div>
      {/* Right above the command bar it talks about, clear of the lighting controls at the top. */}
      <div className="pointer-events-none absolute bottom-[140px] left-1/2 z-30 hidden w-[min(460px,40vw)] -translate-x-1/2 lg:block">
        <FirstRunHint id="office" title={t("hint.office.title")} body={t("hint.office.body")} />
      </div>
      <div data-tour="office.approvals" className="pointer-events-none absolute bottom-[96px] left-4"><ApprovalsTray /></div>
      <div className="pointer-events-none absolute inset-x-0 bottom-4 flex justify-center px-4" style={{ paddingRight: `calc(${selected ? "var(--panel-w, 420px)" : "16px"} + ${viewerOpen ? "min(560px, 100vw)" : "0px"})`, paddingLeft: pendingApprovals ? 352 : 16 }}>
        <CommandBar />
      </div>
      {/* Same box as the office scene but above the chat: in-scene popups that must stay clickable (approvals desk card). */}
      <div id="office-popups" className="pointer-events-none absolute inset-0 z-20" />
      <div data-tour="office.lighting" className="pointer-events-none absolute right-4 top-4 z-40 flex items-start gap-2" style={{ right: `calc(${selected ? "var(--panel-w, 420px) + 16px" : "16px"} + ${viewerOpen ? "min(560px, 100vw)" : "0px"})` }}>
        <LightingToggle />
      </div>
      {selected && <div data-tour="office.agentPanel" className="pointer-events-none absolute bottom-0 right-0 top-0 z-30"><WorkspaceHost id={selected} /></div>}
      {!selected && <div className="pointer-events-none absolute bottom-4 right-4 hidden rounded-md border border-line bg-panel/80 px-3 py-1.5 text-[10px] font-semibold text-mute md:block">{t("hud.cameraHint")}</div>}
    </>
  );
}
