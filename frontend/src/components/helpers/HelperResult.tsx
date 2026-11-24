"use client";
import { useStore } from "@/lib/store";
import { useT, fmtUsd } from "@/lib/i18n";
import { useHelpers } from "@/lib/helpers";
import { Icon } from "../icons";
import { PartRow } from "./HelperProgress";

/** The folded result of a helpers group in the conversation: "Nadia entregó con 8 ayudantes · costó $X"; open it to see each part. */
export function HelperResult({ groupId }: { groupId: string }) {
  const { t } = useT();
  const g = useHelpers((s) => s.groups[groupId]);
  const owner = useStore((s) => (g ? s.agents[g.owner_agent_id] : undefined));
  if (!g) return null;
  const first = (owner?.name ?? "").split(" ")[0] || t("common.system");
  return (
    <details data-testid="helper-result" data-group={g.id} data-status={g.status} className="group w-full max-w-[86%] rounded-[10px] border border-line bg-panel">
      <summary className="flex min-h-11 cursor-pointer list-none items-center justify-between gap-2 px-3 py-2 text-[12.5px] font-semibold text-ink">
        {t("helpers.result.block", { name: first, n: g.count, cost: fmtUsd(g.spent_usd) })}
        <Icon name="down" size={13} className="shrink-0 text-mute transition-transform group-open:rotate-180" />
      </summary>
      <ul className="space-y-2 border-t border-line px-3 py-2.5">
        {g.parts.map((p) => <PartRow key={p.index} g={g} p={p} busy={false} run={() => undefined} readOnly />)}
      </ul>
    </details>
  );
}
