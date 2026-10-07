"use client";
import { useEffect, useMemo } from "react";
import { useArtifacts } from "@/lib/artifacts";
import { useT } from "@/lib/i18n";

/** Previous/next/select across the deliverables of the active artifact's project; Alt+[ and Alt+] shortcuts. */
export function DeliverableNav({ deskId, artifactId }: { deskId: string; artifactId: string }) {
  const { t } = useT();
  const art = useArtifacts((s) => s.artifacts[artifactId]);
  const project = useArtifacts((s) => (art?.project_id ? s.projects[art.project_id] : undefined));
  const load = useArtifacts((s) => s.loadProject);
  const openTab = useArtifacts((s) => s.openTab);
  useEffect(() => { if (art?.project_id) load(art.project_id); }, [art?.project_id, load]);
  const list = project?.deliverables ?? [];
  const idx = useMemo(() => list.findIndex((d) => d.deliverable_id === art?.deliverable_id), [list, art?.deliverable_id]);
  const go = (i: number) => { const d = list[(i + list.length) % list.length]; if (d?.artifacts[0]) openTab(deskId, d.artifacts[0].id); };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!e.altKey || list.length < 2) return;
      if (e.key === "[") { e.preventDefault(); go(idx - 1); }
      if (e.key === "]") { e.preventDefault(); go(idx + 1); }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }); // eslint-disable-line react-hooks/exhaustive-deps

  if (!art?.project_id || list.length < 2) return null;
  return (
    <div data-testid="ws-deliverable-nav" className="flex items-center gap-1.5 border-b border-line bg-panel2/30 px-3 py-1 text-[11px]">
      <button type="button" data-testid="ws-deliverable-prev" aria-label={t("workspace.prevDeliverable")} title="Alt+[" onClick={() => go(idx - 1)} className="rounded-md border border-line bg-panel px-2 font-semibold text-ink hover:border-accent">&#8249;</button>
      <select data-testid="ws-deliverable-select" aria-label={t("workspace.deliverable")} value={idx >= 0 ? idx : ""} onChange={(e) => go(Number(e.target.value))} className="max-w-[220px] rounded-md border border-line bg-panel px-2 py-0.5 font-semibold text-ink">
        {list.map((d, i) => <option key={d.deliverable_id} value={i}>{d.title} ({d.progress}%)</option>)}
      </select>
      <button type="button" data-testid="ws-deliverable-next" aria-label={t("workspace.nextDeliverable")} title="Alt+]" onClick={() => go(idx + 1)} className="rounded-md border border-line bg-panel px-2 font-semibold text-ink hover:border-accent">&#8250;</button>
    </div>
  );
}
