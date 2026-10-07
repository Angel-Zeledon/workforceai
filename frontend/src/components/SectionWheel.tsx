"use client";
import { useCallback, useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { useT } from "@/lib/i18n";
import { usePreferences } from "@/lib/preferences";
import { Icon, type IconName } from "./icons";

export interface WheelItem<T extends string> { id: T; icon: IconName; label: string; testid: string }

const R = 98;      // ring radius (px)
const ITEM = 44;   // item diameter (px)

function useNarrow() {
  const [narrow, setNarrow] = useState(false);
  useEffect(() => {
    const m = window.matchMedia("(max-width: 639px)");
    const f = () => setNarrow(m.matches);
    f();
    m.addEventListener("change", f);
    return () => m.removeEventListener("change", f);
  }, []);
  return narrow;
}

/**
 * Section switcher of the agent panel: a radial "wheel" around the agent avatar.
 *
 * Closed: a compact pill (icon + current section) that always shows where you are.
 * Open: the avatar becomes the hub and the sections fan out on a ring (first item at
 * 12 o'clock); the hovered / focused section is named in the hub. On phones the same
 * items render as a bottom sheet list. Items keep `role="tab"` + `aria-selected` and
 * their `data-testid`s, and stay in the DOM while closed.
 *
 * Keyboard: Enter/Space/Arrow opens; arrows, Tab and Home/End move; Enter selects; Esc closes.
 * It positions itself against the nearest positioned ancestor (the panel `<aside>`).
 */
export function SectionWheel<T extends string>({ items, value, onChange, hubAvatar, ariaLabel }: {
  items: WheelItem<T>[]; value: T; onChange: (id: T) => void; hubAvatar: ReactNode; ariaLabel: string;
}) {
  const { t } = useT();
  const reduce = usePreferences((s) => s.prefs.reduceMotion);
  const narrow = useNarrow();
  const [open, setOpen] = useState(false);
  const [peek, setPeek] = useState<number | null>(null);
  const [focus, setFocus] = useState(0);
  const trigger = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const cur = Math.max(0, items.findIndex((i) => i.id === value));
  const n = items.length;

  const openWheel = useCallback(() => { setFocus(cur); setPeek(null); setOpen(true); }, [cur]);
  const close = useCallback(() => { setOpen(false); setPeek(null); trigger.current?.focus(); }, []);
  const pick = (i: number) => { onChange(items[i].id); close(); };

  // move DOM focus to the roving item whenever the wheel is open
  useEffect(() => {
    if (!open) return;
    list.current?.querySelectorAll<HTMLElement>('[role="tab"]')[focus]?.focus();
  }, [open, focus]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: globalThis.KeyboardEvent) => { if (e.key === "Escape") { e.stopPropagation(); close(); } };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [open, close]);

  const onListKey = (e: KeyboardEvent) => {
    let next = focus;
    if (e.key === "ArrowRight" || e.key === "ArrowDown" || (e.key === "Tab" && !e.shiftKey)) next = (focus + 1) % n;
    else if (e.key === "ArrowLeft" || e.key === "ArrowUp" || (e.key === "Tab" && e.shiftKey)) next = (focus - 1 + n) % n;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = n - 1;
    else return;
    e.preventDefault();
    setFocus(next); setPeek(next);
  };

  const shown = items[peek ?? focus] ?? items[cur];
  const anim = reduce ? "" : "transition-[transform,opacity] duration-200 ease-out motion-reduce:transition-none";

  const wheelItem = (it: WheelItem<T>, i: number) => {
    const active = i === cur;
    const ang = (-90 + (360 / n) * i) * (Math.PI / 180);
    const x = Math.round(Math.cos(ang) * R), y = Math.round(Math.sin(ang) * R);
    const base = `flex items-center justify-center border outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-panel`;
    const tone = active ? "border-accent bg-accent text-white" : "border-line bg-panel text-ink2 hover:border-accent hover:text-accent";
    const common = {
      role: "tab" as const, "aria-selected": active, "aria-label": it.label, "data-testid": it.testid, tabIndex: open && focus === i ? 0 : -1,
      onClick: () => pick(i), onMouseEnter: () => setPeek(i), onMouseLeave: () => setPeek(null), onFocus: () => { setFocus(i); setPeek(i); }, onBlur: () => setPeek(null),
    };
    if (narrow) {
      return (
        <button key={it.id} type="button" {...common} title={it.label}
          className={`${base} w-full justify-start gap-3 rounded-lg px-3 py-2.5 text-left text-[13px] font-medium ${active ? "border-accent/30 bg-accent-soft text-accent" : "border-transparent text-ink hover:bg-panel2"}`}>
          <Icon name={it.icon} size={17} /><span className="flex-1">{it.label}</span>{active && <Icon name="check" size={15} />}
        </button>
      );
    }
    return (
      <button key={it.id} type="button" {...common} title={it.label}
        className={`${base} ${tone} ${anim} absolute left-1/2 top-1/2 rounded-full shadow-soft`}
        style={{ width: ITEM, height: ITEM, marginLeft: -ITEM / 2, marginTop: -ITEM / 2, transform: open ? `translate(${x}px, ${y}px)` : "translate(0,0) scale(0.4)", opacity: open ? 1 : 0, transitionDelay: open && !reduce ? `${i * 18}ms` : "0ms" }}>
        <Icon name={it.icon} size={18} />
      </button>
    );
  };

  return (
    <>
      <button ref={trigger} type="button" data-testid="panel-nav" aria-haspopup="true" aria-expanded={open} aria-controls="panel-section-nav"
        aria-label={`${ariaLabel}: ${items[cur].label}`} title={ariaLabel}
        onClick={() => (open ? close() : openWheel())}
        onKeyDown={(e) => { if (!open && (e.key === "ArrowDown" || e.key === "ArrowUp" || e.key === "ArrowRight" || e.key === "ArrowLeft")) { e.preventDefault(); openWheel(); } }}
        className={`relative z-[42] flex shrink-0 items-center gap-1.5 rounded-full border py-1 pl-1 pr-2.5 text-[11.5px] font-semibold transition ${open ? "border-accent bg-accent-soft text-accent" : "border-line bg-panel2 text-ink2 hover:border-accent hover:text-accent"}`}>
        <span className={`flex h-6 w-6 items-center justify-center rounded-full ${open ? "bg-accent text-white" : "bg-panel text-accent ring-1 ring-line"}`}><Icon name={items[cur].icon} size={13} /></span>
        <span data-testid="panel-nav-current" className="max-w-[96px] truncate pl-1">{items[cur].label}</span>
        <Icon name="down" size={12} className={open ? "rotate-180" : ""} />
      </button>

      {/* overlay: always mounted so the tabs stay in the DOM; hidden (not focusable) while closed */}
      <div className={`absolute inset-0 z-40 ${open ? "" : "invisible pointer-events-none"}`} aria-hidden={!open}>
        <div className={`absolute inset-0 bg-panel/85 backdrop-blur-[3px] ${reduce ? "" : "transition-opacity duration-150 motion-reduce:transition-none"} ${open ? "opacity-100" : "opacity-0"}`} onClick={() => close()} />
        {narrow ? (
          <div className={`absolute inset-x-0 bottom-0 max-h-[80%] overflow-y-auto rounded-t-2xl border-t border-line bg-panel p-2 pb-4 shadow-float ${open && !reduce ? "ac-pop" : ""}`}>
            <div className="mx-auto mb-2 h-1 w-9 rounded-full bg-line-strong" aria-hidden />
            <div id="panel-section-nav" ref={list} role="tablist" aria-label={ariaLabel} aria-orientation="vertical" onKeyDown={onListKey} className="space-y-0.5">
              {items.map(wheelItem)}
            </div>
          </div>
        ) : (
          <div className="absolute left-1/2 top-[218px] -translate-x-1/2 -translate-y-1/2" style={{ width: R * 2 + ITEM, height: R * 2 + ITEM }}>
            <div aria-hidden className={`absolute rounded-full border border-line/80 bg-panel shadow-soft ${anim} ${open ? "scale-100 opacity-100" : "scale-50 opacity-0"}`} style={{ inset: ITEM / 2 - 6 }} />
            <div id="panel-section-nav" ref={list} role="tablist" aria-label={ariaLabel} onKeyDown={onListKey} className="absolute inset-0">
              {items.map(wheelItem)}
            </div>
            <div aria-hidden className="pointer-events-none absolute left-1/2 top-1/2 flex w-[120px] -translate-x-1/2 -translate-y-1/2 flex-col items-center gap-1.5 text-center">
              {hubAvatar}
              <span data-testid="panel-nav-hub-label" className="flex items-center gap-1 text-[12.5px] font-semibold text-ink"><Icon name={shown.icon} size={13} className="text-accent" />{shown.label}</span>
              <span className="text-[10px] text-mute">{peek !== null && peek !== cur ? t("panel.nav.go") : t("panel.nav.current")}</span>
            </div>
          </div>
        )}
      </div>
    </>
  );
}
