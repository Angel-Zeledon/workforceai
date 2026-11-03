"use client";
import { useT } from "@/lib/i18n";
import { go } from "@/lib/navBridge";
import { Icon, type IconName } from "../icons";

/**
 * Agents, skills and templates are three different things that people mixed up (they were all "templates" somewhere):
 *  - an agent is WHO does the work (a team member with a profession),
 *  - a skill is HOW it is done (your company's know-how, used in any task),
 *  - a template is WHAT gets done and in which order (a recipe of steps, who does each one, where you approve).
 * This strip sits on top of the three screens with the same icon and tint for each concept everywhere, marks the one
 * you are on and links to the other two.
 */
export type Concept = "agents" | "skills" | "templates";

export const CONCEPTS: { id: Concept; icon: IconName; tint: string; href: string }[] = [
  { id: "agents", icon: "user", tint: "bg-accent-soft text-accent", href: "/settings?s=hire" },
  { id: "skills", icon: "layers", tint: "bg-emerald-500/10 text-emerald-700", href: "/skills" },
  { id: "templates", icon: "list", tint: "bg-amber-500/10 text-amber-700", href: "/settings?s=templates" },
];

export function ConceptLegend({ current, className = "" }: { current: Concept; className?: string }) {
  const { t } = useT();
  return (
    <section aria-label={t("concepts.aria")} data-testid="concept-legend" className={`grid gap-2 sm:grid-cols-3 ${className}`}>
      {CONCEPTS.map((c) => {
        const here = c.id === current;
        return (
          <div key={c.id} data-testid={`concept-${c.id}`} data-current={here ? "1" : "0"} aria-current={here ? "page" : undefined}
            className={`flex items-start gap-2.5 rounded-xl border p-3 ${here ? "border-line-strong bg-panel shadow-soft" : "border-line bg-panel2"}`}>
            <span className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg ${c.tint}`}><Icon name={c.icon} size={16} /></span>
            <span className="min-w-0 flex-1">
              <span className="flex items-baseline gap-1.5">
                <b className="text-[12.5px] text-ink">{t(`concepts.${c.id}.name`)}</b>
                <span className="text-[11px] font-semibold uppercase tracking-wide text-mute">{t(`concepts.${c.id}.what`)}</span>
              </span>
              <span className="mt-0.5 block text-[11.5px] leading-snug text-ink2">{t(`concepts.${c.id}.body`)}</span>
              <span className="mt-1 block text-[11px] leading-snug text-mute">{t(`concepts.${c.id}.example`)}</span>
              {!here && (
                <button type="button" data-testid={`concept-go-${c.id}`} onClick={() => go(c.href)} className="mt-1.5 text-[11px] font-semibold text-accent hover:underline">
                  {t(`concepts.${c.id}.go`)} ›
                </button>
              )}
            </span>
          </div>
        );
      })}
    </section>
  );
}
