"use client";
import { useEffect, useMemo, useState } from "react";
import { ARTIFACT_KINDS, ME_DESK, useArtifacts, type ArtifactKind, type ArtifactMeta } from "@/lib/artifacts";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { Empty, Progress, useAgentName } from "../ui";
import { DeskView } from "./DeskView";
import { KindIcon } from "./kindMeta";

/** "Files" section of the dashboard (sec. 2.3): library grouped by project; opening a card mounts the desk full size. */
export function DashboardFiles() {
  const { t } = useT();
  const all = useArtifacts((s) => s.artifacts);
  const openTab = useArtifacts((s) => s.openTab);
  const projects = useArtifacts((s) => s.projects);
  const loadProject = useArtifacts((s) => s.loadProject);
  const agents = useStore((s) => s.agents);
  const order = useStore((s) => s.agentOrder);
  const name = useAgentName();
  const [kind, setKind] = useState<ArtifactKind | "">("");
  const [agent, setAgent] = useState("");
  const [q, setQ] = useState("");
  const [desk, setDesk] = useState<string | null>(null);

  const files = useMemo(() => Object.values(all)
    .filter((a) => (!kind || a.kind === kind) && (!agent || a.created_by.id === agent || a.attachments.some((x) => x.agent_id === agent)) && (!q || a.title.toLowerCase().includes(q.toLowerCase())))
    .sort((a, b) => +new Date(b.updated_at) - +new Date(a.updated_at)), [all, kind, agent, q]);
  const groups = useMemo(() => {
    const m = new Map<string, ArtifactMeta[]>();
    files.forEach((f) => { const k = f.project_id ?? ""; m.set(k, [...(m.get(k) ?? []), f]); });
    return [...m.entries()].sort(([a], [b]) => (a === "" ? 1 : b === "" ? -1 : 0));
  }, [files]);

  const enter = (d: string) => {
    useArtifacts.setState({ deskId: d, size: "full" });
    useArtifacts.getState().ensureDesk(d);
    setDesk(d);
  };
  const open = (a: ArtifactMeta) => {
    const d = agent || (a.created_by.kind === "agent" ? a.created_by.id : ME_DESK);
    enter(d);
    openTab(d, a.id);
  };
  const projectIds = useMemo(() => Array.from(new Set(Object.values(all).map((a) => a.project_id).filter(Boolean) as string[])), [all]);
  useEffect(() => { projectIds.forEach((p) => loadProject(p)); }, [projectIds, loadProject]);

  if (desk) {
    return (
      <div data-testid="ws-host" data-size="full" data-desk={desk} className="-m-6 flex h-[calc(100%+3rem)] min-h-0 flex-col bg-panel">
        <div className="border-b border-line px-4 py-2"><button type="button" onClick={() => setDesk(null)} className="text-xs font-semibold text-accent hover:underline">&larr; {t("dash.files.backToLibrary")}</button></div>
        <DeskView deskId={desk} actions={
          <select aria-label={t("dash.files.deskOf")} value={desk} onChange={(e) => { setDesk(e.target.value); useArtifacts.getState().ensureDesk(e.target.value); useArtifacts.setState({ deskId: e.target.value }); }} className="rounded-md border border-line bg-panel px-2 py-0.5 text-[11px] font-semibold text-ink">
            <option value={ME_DESK}>{t("workspace.myDesk")}</option>
            {order.filter((id) => agents[id]).map((id) => <option key={id} value={id}>{name(id)}</option>)}
          </select>} />
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("dash.files.search")} className="w-56 rounded-md border border-line bg-panel px-3 py-1.5 text-xs outline-none focus:border-accent" />
        <select aria-label={t("dash.files.kind")} value={kind} onChange={(e) => setKind(e.target.value as ArtifactKind | "")} className="rounded-md border border-line bg-panel px-2 py-1.5 text-xs font-semibold text-ink">
          <option value="">{t("dash.files.allKinds")}</option>
          {ARTIFACT_KINDS.map((k) => <option key={k} value={k}>{t(`artifact.kind.${k}`)}</option>)}
        </select>
        <select aria-label={t("dash.files.agent")} value={agent} onChange={(e) => setAgent(e.target.value)} className="rounded-md border border-line bg-panel px-2 py-1.5 text-xs font-semibold text-ink">
          <option value="">{t("dash.files.allAgents")}</option>
          {order.filter((id) => agents[id]).map((id) => <option key={id} value={id}>{name(id)}</option>)}
        </select>
        <button type="button" onClick={() => enter(ME_DESK)} className="ml-auto rounded-md border border-line bg-panel px-3 py-1.5 text-xs font-semibold text-ink hover:border-accent">{t("workspace.myDesk")}</button>
      </div>
      {groups.length === 0 && <Empty>{t("files.empty")}</Empty>}
      {groups.map(([pid, list]) => {
        const avg = Math.round(list.reduce((s, f) => s + f.progress, 0) / list.length);
        return (
          <details key={pid || "none"} open data-testid={pid ? `dash-project-${pid}` : undefined} data-progress={pid ? avg : undefined} className="rounded-xl border border-line bg-panel shadow-pop">
            <summary className="flex cursor-pointer items-center gap-3 px-4 py-2.5 text-[13px] font-semibold text-ink">
              <span>{pid ? (projects[pid]?.name ?? t("workspace.project")) : t("dash.files.noProject")}</span>
              <span className="text-[11px] font-normal text-mute">{t("files.count", { count: list.length })}</span>
              {pid && <span className="ml-auto flex w-40 items-center gap-2"><span className="flex-1"><Progress value={avg} color="#3451b2" /></span><span className="font-mono text-[11px]">{avg}%</span></span>}
            </summary>
            <div className="grid gap-3 p-4 pt-1 sm:grid-cols-2 xl:grid-cols-3">
              {list.map((a) => (
                <button key={a.id} type="button" data-testid={`dash-file-${a.id}`} onClick={() => open(a)} className="rounded-xl border border-line bg-panel2/60 p-3 text-left hover:border-accent">
                  <div className="flex items-center gap-2"><KindIcon kind={a.kind} size={18} /><span className="truncate text-[13px] font-semibold text-ink">{a.title}</span></div>
                  <div className="mt-1 text-[11px] text-mute">{t(`artifact.kind.${a.kind}`)} &middot; {t(`artifact.status.${a.status}`)} &middot; v{a.head_version}</div>
                  <div className="mt-0.5 text-[11px] text-mute">{a.last_author.kind === "agent" ? name(a.last_author.id) : t("common.you")}{a.pending_proposals > 0 ? ` · ${t("workspace.proposalsCount", { count: a.pending_proposals })}` : ""}</div>
                  {a.deliverable_id && <div className="mt-2 flex items-center gap-2"><div className="flex-1"><Progress value={a.progress} color="#3451b2" /></div><span className="font-mono text-[10px] text-mute">{a.progress}%</span></div>}
                </button>
              ))}
            </div>
          </details>
        );
      })}
    </div>
  );
}
