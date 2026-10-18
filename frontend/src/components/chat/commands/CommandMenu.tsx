"use client";
import { useEffect, useRef } from "react";
import { Icon, type IconName } from "../../icons";
import { usePreferences } from "@/lib/preferences";

export interface MenuItem {
  key: string;
  label: string;
  /** secondary text on the same line (description / hint) */
  hint?: string;
  /** example shown under the hint */
  example?: string;
  group?: string;
  icon?: string;
  disabled?: boolean;
  disabledReason?: string;
}

const ICONS = new Set<string>(["office", "grid", "folder", "plug", "shield", "power", "layout", "flag", "settings", "sun", "moon", "x", "check", "play", "mail", "list", "arrow", "back", "alert", "hand", "edit", "plus", "chevron", "down", "expand", "chat", "pulse", "users", "layers", "doc", "user", "clock", "bag", "shrink"]);
export const safeIcon = (n?: string): IconName => (n && ICONS.has(n) ? (n as IconName) : "chevron");

/** Listbox popover shown above the composer. The input keeps focus (combobox pattern: aria-activedescendant). */
export function CommandMenu({ id, items, active, onPick, onHover, label, emptyText }: {
  id: string; items: MenuItem[]; active: number; onPick: (i: number) => void; onHover: (i: number) => void; label: string; emptyText?: string;
}) {
  const reduce = usePreferences((s) => s.prefs.reduceMotion);
  const ref = useRef<HTMLUListElement>(null);
  useEffect(() => {
    const el = ref.current?.querySelector<HTMLElement>(`[data-idx="${active}"]`);
    el?.scrollIntoView?.({ block: "nearest" });
  }, [active, items]);
  let lastGroup = "";
  return (
    <div data-reduce={reduce ? "1" : "0"} className="absolute inset-x-0 bottom-full z-30 mb-2">
      <ul
        ref={ref} id={id} role="listbox" aria-label={label} data-testid="cmd-menu"
        className="ac-pop max-h-[min(340px,48dvh)] overflow-y-auto overscroll-contain rounded-xl border border-line-strong bg-panel p-1 shadow-float"
      >
        {items.length === 0 && emptyText && <li role="presentation" className="px-3 py-2 text-[12px] text-mute">{emptyText}</li>}
        {items.map((it, i) => {
          const head = it.group && it.group !== lastGroup ? it.group : "";
          if (it.group) lastGroup = it.group;
          return (
            <li key={it.key} role="presentation">
              {head && <div className="px-2.5 pb-0.5 pt-1.5 text-[10px] font-semibold uppercase tracking-[0.08em] text-mute">{head}</div>}
              <div
                id={`${id}-${i}`} data-idx={i} role="option" aria-selected={i === active} aria-disabled={it.disabled || undefined} data-testid="cmd-option" data-key={it.key}
                title={it.disabled ? it.disabledReason : undefined}
                onMouseMove={() => i !== active && onHover(i)}
                onMouseDown={(e) => { e.preventDefault(); if (!it.disabled) onPick(i); }}
                className={`flex cursor-pointer items-start gap-2.5 rounded-lg px-2.5 py-1.5 ${i === active ? "bg-accent-soft" : ""} ${it.disabled ? "cursor-not-allowed opacity-45" : ""}`}
              >
                {it.icon && <span aria-hidden className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-md bg-panel2 text-accent-hover"><Icon name={safeIcon(it.icon)} size={13} /></span>}
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[12.5px] font-medium text-ink">{it.label}</span>
                  {it.hint && <span className="block truncate text-[11.5px] text-ink2">{it.hint}</span>}
                  {it.example && <span className="block truncate font-mono text-[10.5px] text-mute">{it.example}</span>}
                </span>
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
