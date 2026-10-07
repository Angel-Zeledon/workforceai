"use client";
import { useMemo } from "react";
import { agentArtifacts, useArtifacts } from "@/lib/artifacts";
import { useT } from "@/lib/i18n";
import { Empty } from "../ui";
import { KindIcon } from "./kindMeta";
import { TabProgressRing } from "./TabProgressRing";

/** "Files" tab of the agent dock: artifacts created by or attached to the agent, plus the "open desk" button. */
export function AgentFilesTab({ id }: { id: string }) {
  const { t } = useT();
  const all = useArtifacts((s) => s.artifacts);
  const openDesk = useArtifacts((s) => s.openDesk);
  const openTab = useArtifacts((s) => s.openTab);
  const files = useMemo(() => agentArtifacts(all, id), [all, id]);
  const building = files.filter((f) => f.build_state === "building").length;
  const open = (artId?: string) => { openDesk(id, "expanded"); if (artId) openTab(id, artId); };
  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs text-mute">{t("files.count", { count: files.length })}{building > 0 ? ` · ${t("files.building", { count: building })}` : ""}</span>
        <button type="button" data-testid="ws-open-desk" onClick={() => open()} className="rounded-md border border-accent bg-accent px-3 py-1 text-xs font-semibold text-white hover:bg-accent-hover">{t("workspace.openDesk")}</button>
      </div>
      {files.length === 0 ? <Empty>{t("files.empty")}</Empty> : (
        <ul className="space-y-2">
          {files.map((f) => (
            <li key={f.id}>
              <button type="button" data-testid={`files-item-${f.id}`} onClick={() => open(f.id)} className="flex w-full items-center gap-2 rounded-xl border border-line bg-panel2 px-3 py-2 text-left shadow-pop hover:border-accent">
                <KindIcon kind={f.kind} size={18} />
                <span className="min-w-0 flex-1"><span className="block truncate text-[13px] font-semibold text-ink">{f.title}</span><span className="block text-[10px] text-mute">{t(`artifact.kind.${f.kind}`)} &middot; {t(`artifact.status.${f.status}`)} &middot; v{f.head_version}</span></span>
                {f.deliverable_id && <TabProgressRing id={`dock-${f.id}`} progress={f.progress} buildState={f.build_state} />}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
