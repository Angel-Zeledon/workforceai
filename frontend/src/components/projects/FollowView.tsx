"use client";
import Link from "next/link";
import { useT } from "@/lib/i18n";
import { useProjects } from "@/lib/projects/store";
import { useStore } from "@/lib/store";
import type { ProjectNode } from "@/lib/projects/types";
import { QuestionCard, WaitingLabel } from "../approvals/QuestionCard";
import { AgentChip, StatePill, useNodeTitle } from "./shared";

/** Order of the simple tracking list: what needs a person first, then what is moving, then the rest. */
const ORDER: Record<string, number> = {
  needs_input: 0, awaiting_approval: 1, failed: 2, running: 3, ready: 4, waiting: 5, paused: 6, pending: 7, blocked: 8, done: 9, skipped: 10, cancelled: 11, draft: 12,
};

const baseName = (p: string) => p.split("/").filter(Boolean).pop() || p;

/** "Seguimiento": every task with its agent, its state, since when and the files it produced; the ones that need
 * your help on top with their question. The advanced views (map, timeline, kanban...) stay one tab away. */
export function FollowView() {
  const { t } = useT();
  const detail = useProjects((s) => s.detail)!;
  const select = useProjects((s) => s.selectNode);
  const questions = useStore((s) => s.questions);
  const title = useNodeTitle();
  const leaves = detail.nodes.filter((n) => n.kind !== "group" && !n.superseded)
    .sort((a, b) => (ORDER[a.state] ?? 20) - (ORDER[b.state] ?? 20) || a.wbs_path.localeCompare(b.wbs_path));
  const open = Object.values(questions).filter((q) => q.status === "open");
  const since = (n: ProjectNode) => {
    const at = n.finished_at ?? n.started_at;
    return at ? Math.max(0, (Date.now() - +new Date(at)) / 1000) : null;
  };
  return (
    <div data-testid="follow-view" className="space-y-2">
      {leaves.map((n) => {
        const q = n.task_id ? open.find((x) => x.task_id === n.task_id) : undefined;
        const s = since(n);
        return (
          <div key={n.id} data-testid={`follow-row-${n.id}`} data-state={n.state}
            className={`rounded-[10px] border bg-white p-3 ${n.state === "needs_input" ? "border-accent/40" : "border-line"}`}>
            <div className="flex flex-wrap items-center gap-2">
              <button type="button" onClick={() => select(n.id)} className="min-w-0 basis-full break-words text-left text-[13px] font-semibold text-ink hover:text-accent sm:basis-auto sm:flex-1 sm:truncate">{title(n)}</button>
              <AgentChip id={n.agent_id} />
              <StatePill state={n.state} />
              {s !== null && <span className="text-[11px] text-mute">{n.finished_at ? t("pv.follow.finished") : t("pv.follow.since")} <WaitingLabel seconds={s} /></span>}
            </div>
            {n.files && n.files.length > 0 && (
              <ul className="mt-1.5 flex flex-wrap gap-1.5" aria-label={t("pv.follow.files")}>
                {n.files.map((f) => (
                  <li key={f}><Link href={`/files?path=${encodeURIComponent(f)}`} title={f}
                    className="inline-flex items-center rounded-md border border-line bg-panel2 px-2 py-0.5 text-[11px] text-accent hover:border-accent">{baseName(f)}</Link></li>
                ))}
              </ul>
            )}
            {q && <div className="mt-2"><QuestionCard q={q} /></div>}
          </div>
        );
      })}
      {!leaves.length && <p className="py-6 text-center text-[12px] text-mute">{t("pv.follow.empty")}</p>}
    </div>
  );
}
