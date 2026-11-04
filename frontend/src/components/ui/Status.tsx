"use client";
import type { ReactNode } from "react";
import { KIND_COLOR, kindIcon, kindOf, type KindIcon, type StatusDomain, type StatusKind } from "@/lib/statusLogic";

/**
 * The single status indicator (proposal 5.4, point 1): the color says the state and a small icon makes it readable
 * without color. Built on lib/statusLogic.ts, so every pill of the app (projects, tasks, pedidos, agents) draws the same
 * empty circle, spinning circle, bell, two bars, check, half check, triangle or dash. Inherits `currentColor`.
 */
const SHAPES: Record<KindIcon, ReactNode> = {
  circle: <circle cx="12" cy="12" r="7.5" />,
  dashed: <circle cx="12" cy="12" r="7.5" strokeDasharray="3.2 3" />,
  spinner: (<><circle cx="12" cy="12" r="7.5" opacity="0.28" /><path d="M12 4.5a7.5 7.5 0 0 1 7.5 7.5" /></>),
  bell: <path d="M6.5 10a5.5 5.5 0 0 1 11 0c0 5 2 6.5 2 6.5h-15s2-1.5 2-6.5M10.3 20a2 2 0 0 0 3.4 0" />,
  pause: <path d="M9 6.5v11M15 6.5v11" />,
  check: <path d="M5 12.5l4.5 4.5L19 7.5" />,
  halfcheck: (<><path d="M5 12.5l4.5 4.5 4.2-4.2" /><path d="M13.7 12.8L19 7.5" opacity="0.3" /></>),
  triangle: <path d="M12 4.5l8.5 15h-17zM12 10.5v3.5M12 16.8h.01" />,
  dash: <path d="M6.5 12h11" />,
};

/** Just the icon of a kind (decorative: the pill next to it carries the words). */
export function StatusIcon({ kind, size = 11, className = "" }: { kind: StatusKind; size?: number; className?: string }) {
  const icon = kindIcon(kind);
  return (
    <svg data-status-icon={icon} width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.6}
      strokeLinecap="round" strokeLinejoin="round" aria-hidden focusable="false"
      className={`shrink-0 ${icon === "spinner" ? "motion-safe:animate-spin" : ""} ${className}`}>
      {SHAPES[icon]}
    </svg>
  );
}

/** Icon + words in the color of the kind, for places that do not need a pill background. */
export function Status({ domain, state, label, size, className = "" }: { domain: StatusDomain; state: string | null | undefined; label: ReactNode; size?: number; className?: string }) {
  const kind = kindOf(domain, state);
  return (
    <span data-kind={kind} className={`inline-flex items-center gap-1 whitespace-nowrap ${className}`} style={{ color: KIND_COLOR[kind] }}>
      <StatusIcon kind={kind} size={size} />{label}
    </span>
  );
}
