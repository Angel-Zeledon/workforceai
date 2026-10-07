"use client";
import { trOr } from "@/lib/i18n-core";
import { useT } from "@/lib/i18n";
import { useAgentColor, useAgentName } from "@/components/ui";
import type { TemplateStep } from "@/lib/orgConfigApi";

/**
 * Shows a template's steps grouped by stage (left to right). Steps in the same
 * stage have no dependency between them, so they run in parallel.
 */
export function StepFlow({ steps }: { steps: TemplateStep[] }) {
  const { t } = useT();
  const agentName = useAgentName();
  const agentColor = useAgentColor();
  const stages = new Map<number, TemplateStep[]>();
  for (const s of steps) stages.set(s.stage, [...(stages.get(s.stage) ?? []), s]);
  const ordered = [...stages.entries()].sort((a, b) => a[0] - b[0]);
  return (
    <div data-testid="wf-template-flow">
      <div className="mb-2 text-[11px] text-mute">{t("tpl.flow.parallelHint")}</div>
      <ol className="flex items-stretch gap-2 overflow-x-auto pb-2">
        {ordered.map(([stage, list], i) => (
          <li key={stage} data-testid={`wf-template-stage-${stage}`} data-parallel={list.length > 1 ? "true" : "false"} className="flex shrink-0 items-center gap-2">
            {i > 0 && <span aria-hidden className="text-mute">→</span>}
            <div className="flex w-[200px] flex-col gap-2">
              <div className="flex items-center justify-between text-[10px] font-semibold uppercase tracking-wide text-mute">
                <span>{t("tpl.flow.stage", { n: stage })}</span>
                {list.length > 1 && <span className="rounded bg-accent/15 px-1.5 py-[1px] text-accent">{t("tpl.parallel", { count: list.length })}</span>}
              </div>
              {list.map((s) => (
                <div key={s.key} data-testid={`wf-template-step-${s.key}`} className="rounded-xl border border-line bg-bg px-2.5 py-2" style={{ borderLeftColor: agentColor(s.agent_id), borderLeftWidth: 4 }}>
                  <div className="text-[12px] font-semibold leading-snug">{trOr(s.title_key, s.title)}</div>
                  <div className="mt-0.5 text-[11px] text-mute">{agentName(s.agent_id)}</div>
                </div>
              ))}
            </div>
          </li>
        ))}
      </ol>
    </div>
  );
}
