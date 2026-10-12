"use client";
import { useEffect, useState, type ReactNode } from "react";
import { useT } from "@/lib/i18n";
import { Icon, type IconName } from "../icons";

/** Consistent page header: title, one line that says what the page is for, and the primary action. */
export function PageHeader({ title, description, actions, testid }: { title: string; description?: string; actions?: ReactNode; testid?: string }) {
  return (
    <div data-testid={testid} className="flex flex-wrap items-end justify-between gap-x-6 gap-y-3 border-b border-line bg-panel px-5 py-4 sm:px-6">
      <div className="min-w-0">
        <h1 className="font-display text-xl font-semibold tracking-tight text-ink">{title}</h1>
        {description && <p className="mt-0.5 max-w-2xl text-[13px] leading-snug text-mute">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

/** Empty state that says exactly what to do next. */
export function EmptyState({ icon = "layers", title, body, action, testid }: { icon?: IconName; title: string; body: string; action?: ReactNode; testid?: string }) {
  return (
    <div data-testid={testid} className="ac-pop mx-auto flex max-w-md flex-col items-center rounded-xl border border-dashed border-line-strong bg-panel px-6 py-10 text-center">
      <span className="flex h-11 w-11 items-center justify-center rounded-full bg-accent-soft text-accent"><Icon name={icon} size={20} /></span>
      <h3 className="mt-4 text-[15px] font-semibold text-ink">{title}</h3>
      <p className="mt-1.5 text-[13px] leading-relaxed text-mute">{body}</p>
      {action && <div className="mt-5">{action}</div>}
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
    <aside data-testid={`hint-${id}`} role="note" className={`ac-pop pointer-events-auto flex items-start gap-3 rounded-xl border border-accent/25 bg-accent-soft px-4 py-3 text-ink shadow-pop ${className}`}>
      <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-accent text-white"><Icon name="flag" size={13} /></span>
      <div className="min-w-0 flex-1">
        <div className="text-[13px] font-semibold">{title}</div>
        <p className="mt-0.5 text-[12.5px] leading-snug text-ink2">{body}</p>
        {children && <div className="mt-2">{children}</div>}
      </div>
      <button type="button" data-testid={`hint-${id}-dismiss`} onClick={dismiss} aria-label={t("hint.dismiss")} title={t("hint.dismiss")} className="rounded p-1 text-mute transition hover:bg-white/60 hover:text-ink"><Icon name="x" size={14} /></button>
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
    <div role="status" aria-label={t("auth.loading")} data-testid="page-skeleton" className="p-6">
      <Skeleton className="h-6 w-48" />
      <Skeleton className="mt-3 h-4 w-80 max-w-full" />
      <div className="mt-8 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {[0, 1, 2].map((i) => <Skeleton key={i} className="h-32" />)}
      </div>
    </div>
  );
}
