"use client";
import { useT } from "@/lib/i18n";
import { go } from "@/lib/navBridge";
import { Icon, type IconName } from "../icons";

/**
 * The concepts of the glossary (docs/superpowers/specs/2026-11-03-ux-simplificacion-propuesta.md, 5.1): each one has a
 * fixed icon and a soft tone used only on its icon (the color of a STATE is another thing, see lib/statusLogic.ts), a
 * sentence (concepts.<id>.body), an example and the screen where it lives. "¿Qué es esto?" (ConceptHelp) reads them.
 *
 * Agents, skills and templates are three different things that people mixed up (they were all "templates" somewhere):
 *  - an agent is WHO does the work (an AI with a role),
 *  - a skill is HOW it is done (your company's know-how, used in any task),
 *  - a template is WHAT gets done and in which order (a saved project, for one customer or many).
 * The legend strip sits on top of those three screens, marks the one you are on and links to the other two.
 */
export type Concept =
  | "agents" | "person" | "office" | "pedido" | "task" | "project" | "batch" | "templates" | "skills" | "pending"
  | "file" | "report" | "customer" | "connection";

/** href = where the legend button goes; where = "Ver dónde se usa" when it differs (the screen of the concept). */
export const CONCEPTS: { id: Concept; icon: IconName; tint: string; href: string; where?: string }[] = [
  { id: "agents", icon: "bot", tint: "bg-accent-soft text-accent", href: "/settings?s=hire", where: "/org" },
  { id: "skills", icon: "layers", tint: "bg-amber-100 text-amber-700", href: "/skills" },
  { id: "templates", icon: "template", tint: "bg-violet-100 text-violet-500", href: "/projects?view=library" },
  { id: "person", icon: "user", tint: "bg-slate-500/10 text-slate-700", href: "/settings?s=members" },
  { id: "office", icon: "users", tint: "bg-slate-500/10 text-slate-700", href: "/org" },
  { id: "pedido", icon: "clipboard", tint: "bg-indigo-500/10 text-indigo-700", href: "/chats" },
  { id: "task", icon: "checkbox", tint: "bg-indigo-400/10 text-indigo-500", href: "/dashboard" },
  { id: "project", icon: "flag", tint: "bg-violet-500/10 text-violet-700", href: "/projects" },
  { id: "batch", icon: "copies", tint: "bg-violet-500/10 text-violet-600", href: "/projects" },
  { id: "pending", icon: "bell", tint: "bg-amber-500/15 text-amber-800", href: "/approvals/inbox" },
  { id: "file", icon: "folder", tint: "bg-sky-500/10 text-sky-700", href: "/files" },
  { id: "report", icon: "doc", tint: "bg-blue-500/10 text-blue-700", href: "/dashboard" },
  { id: "customer", icon: "briefcase", tint: "bg-emerald-500/10 text-emerald-700", href: "/clients" },
  { id: "connection", icon: "plug", tint: "bg-slate-400/15 text-slate-600", href: "/connections" },
];

export const conceptOf = (id: Concept) => CONCEPTS.find((c) => c.id === id) ?? CONCEPTS[0];

/** The strip shows the three concepts people mixed up unless told otherwise. */
const LEGEND_DEFAULT: Concept[] = ["agents", "skills", "templates"];

export function ConceptLegend({ current, ids = LEGEND_DEFAULT, className = "" }: { current: Concept; ids?: Concept[]; className?: string }) {
  const { t } = useT();
  const shown = ids.map(conceptOf);
  return (
    <section aria-label={t("concepts.aria")} data-testid="concept-legend" className={`grid gap-2 ${shown.length === 3 ? "sm:grid-cols-3" : "sm:grid-cols-2 lg:grid-cols-3"} ${className}`}>
      {shown.map((c) => {
        const here = c.id === current;
        return (
          <div key={c.id} data-testid={`concept-${c.id}`} data-current={here ? "1" : "0"} aria-current={here ? "page" : undefined}
            className={`flex items-start gap-2.5 rounded-xl border p-2.5 sm:p-3 ${here ? "border-line-strong bg-panel shadow-soft" : "border-line bg-panel2"}`}>
            <span className={`flex h-7 w-7 shrink-0 items-center justify-center rounded-lg sm:h-8 sm:w-8 ${c.tint}`}><Icon name={c.icon} size={16} /></span>
            <span className="min-w-0 flex-1">
              <span className="flex items-baseline gap-1.5">
                <b className="text-[12.5px] text-ink">{t(`concepts.${c.id}.name`)}</b>
                <span className="text-[11px] font-semibold uppercase tracking-wide text-mute">{t(`concepts.${c.id}.what`)}</span>
              </span>
              <span className="mt-0.5 block text-[11.5px] leading-snug text-ink2">{t(`concepts.${c.id}.body`)}</span>
              <span className="mt-1 hidden text-[11px] leading-snug text-mute sm:block">{t(`concepts.${c.id}.example`)}</span>
              {!here && (
                <button type="button" data-testid={`concept-go-${c.id}`} onClick={() => go(c.href)} className="-mb-2 mt-0 min-h-10 text-[11px] font-semibold text-accent hover:underline sm:mb-0 sm:mt-1.5 sm:min-h-0">
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
