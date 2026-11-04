"use client";
import { useEffect, useState, type ReactNode } from "react";
import { useT } from "@/lib/i18n";
import { Icon, type IconName } from "../icons";
import { conceptOf, type Concept } from "../concepts/ConceptLegend";
import { ConceptHelp } from "../concepts/ConceptHelp";

/** Consistent page header: title (with "¿Qué es esto?" when the page is a concept), what the page is for, the primary action. */
export function PageHeader({ title, description, actions, testid, concept }: { title: string; description?: string; actions?: ReactNode; testid?: string; concept?: Concept }) {
  return (
    <div data-testid={testid} className="flex flex-wrap items-end justify-between gap-x-6 gap-y-3 border-b border-line bg-panel px-4 py-3 sm:px-6 sm:py-4">
      <div className="min-w-0">
        <div className="flex items-center gap-1.5">
          <h1 className="font-display text-[18px] font-semibold leading-tight tracking-tight text-ink sm:text-xl">{title}</h1>
          {concept && <ConceptHelp concept={concept} />}
        </div>
        {description && <p className="mt-0.5 max-w-2xl text-[12.5px] leading-snug text-mute sm:text-[13px]">{description}</p>}
      </div>
      {actions && <div className="flex min-w-0 flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

/**
 * The one empty state (proposal 5.4, point 5), always with the same shape: the icon of the concept, what it is
 * (title + body), how something appears here (how) and a button to do it (action). `compact` is the version that
 * sits inside a card or a list (ui.tsx's Empty uses it, so every list gets the same form).
 */
export function EmptyState({ icon, concept, title, body, how, action, testid, compact = false, className = "" }: {
  icon?: IconName; concept?: Concept; title?: ReactNode; body?: ReactNode; how?: ReactNode; action?: ReactNode; testid?: string; compact?: boolean; className?: string;
}) {
  const c = concept ? conceptOf(concept) : null;
  const glyph = icon ?? c?.icon ?? "layers";
  const tint = c ? c.tint : compact && !icon ? "bg-mute/[0.1] text-mute" : "bg-accent-soft text-accent";
  return (
    <div data-testid={testid} data-concept={concept} className={`ac-pop flex flex-col items-center border border-dashed border-line-strong text-center ${compact
      ? "rounded-lg px-4 py-6"
      : "mx-auto max-w-md rounded-xl bg-panel px-4 py-7 sm:px-6 sm:py-10"} ${className}`}>
      <span aria-hidden className={`flex items-center justify-center rounded-full ${compact ? "h-8 w-8" : "h-11 w-11"} ${tint}`}><Icon name={glyph} size={compact ? 16 : 20} /></span>
      {title && (compact
        ? <p className="mt-2.5 text-[13.5px] font-semibold text-ink">{title}</p>
        : <h3 className="mt-4 text-[15px] font-semibold text-ink">{title}</h3>)}
      {body && <div className={`${title ? "mt-1" : compact ? "mt-2.5" : "mt-4"} max-w-md text-[13px] leading-relaxed text-mute`}>{body}</div>}
      {how && <p className={`mt-1 max-w-md leading-relaxed text-mute ${compact ? "text-[12.5px]" : "text-[13px]"}`}>{how}</p>}
      {action && <div className={`flex flex-wrap items-center justify-center gap-2 ${compact ? "mt-3" : "mt-5"}`}>{action}</div>}
    </div>
  );
}

const hintKey = (id: string) => `wf.hint.${id}`;

/** Dismissible first-run hint; once dismissed it is remembered in localStorage and never shown again. */
export function FirstRunHint({ id, title, body, children, className = "" }: { id: string; title: string; body: string; children?: ReactNode; className?: string }) {
  const { t } = useT();
  const [show, setShow] = useState(false);
  useEffect(() => { try { setShow(localStorage.getItem(hintKey(id)) !== "1"); } catch { setShow(true); } }, [id]);
  if (!show) return null;
  const dismiss = () => { try { localStorage.setItem(hintKey(id), "1"); } catch { /* ignore */ } setShow(false); };
  return (
    <aside data-testid={`hint-${id}`} role="note" className={`ac-pop pointer-events-auto flex items-start gap-3 rounded-xl border border-accent/25 bg-accent-soft px-3 py-2.5 text-ink shadow-pop sm:px-4 sm:py-3 ${className}`}>
      <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-accent text-white"><Icon name="flag" size={13} /></span>
      <div className="min-w-0 flex-1">
        <div className="text-[13px] font-semibold">{title}</div>
        <p className="mt-0.5 line-clamp-3 text-[12.5px] leading-snug text-ink2 sm:line-clamp-none">{body}</p>
        {children && <div className="mt-2">{children}</div>}
      </div>
      <button type="button" data-testid={`hint-${id}-dismiss`} onClick={dismiss} aria-label={t("hint.dismiss")} title={t("hint.dismiss")} className="-m-1.5 flex h-9 w-9 shrink-0 items-center justify-center rounded text-mute transition hover:bg-white/60 hover:text-ink sm:m-0 sm:h-auto sm:w-auto sm:p-1"><Icon name="x" size={14} /></button>
    </aside>
  );
}

/** Pulsing placeholder block (use instead of "Cargando…"). */
export function Skeleton({ className = "" }: { className?: string }) {
  return <div aria-hidden className={`ac-skeleton rounded-md bg-line/70 ${className}`} />;
}

/** Page-sized skeleton: header + a few cards. */
export function PageSkeleton() {
  const { t } = useT();
  return (
    <div role="status" aria-label={t("auth.loading")} data-testid="page-skeleton" className="p-4 sm:p-6">
      <Skeleton className="h-6 w-48" />
      <Skeleton className="mt-3 h-4 w-80 max-w-full" />
      <div className="mt-8 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {[0, 1, 2].map((i) => <Skeleton key={i} className="h-32" />)}
      </div>
    </div>
  );
}
