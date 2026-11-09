"use client";
// The single side detail panel (docs/superpowers/specs/2026-11-03-ux-simplificacion-propuesta.md, 5.4 point 2), taken
// from the pedido panel of Chats: it opens on the right at a fixed width, closes with the X or Esc, on a phone it fills
// the screen and has "Volver", and the URL says what is open so the browser's Back closes it (useDetailUrl).
// Used by the pedido, the task of a project, the customer and the file. The pure part lives in detailPanelLogic.ts.
import { useEffect, useId, useRef, useSyncExternalStore, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { useT } from "@/lib/i18n";
import { Icon } from "../icons";
import { conceptOf, type Concept } from "../concepts/ConceptLegend";
import {
  PHONE_QUERY, detailHref, detailParam, historyStep, releaseAction, topPanel, trapIndex, type OpenPanel,
} from "./detailPanelLogic";

/**
 * Where the panel sits:
 *  - "side": beside the content; from 1280 px it is a column of the page, below it floats over the right side.
 *  - "overlay": always floats over the right side of the page (the page behind keeps its layout).
 *  - "fill": its host already places it (the file viewer layer); the panel fills the host's height.
 * On a phone all three fill the screen.
 */
export type DetailLayout = "side" | "overlay" | "fill";

const LAYOUT: Record<DetailLayout, Record<"md" | "lg", string>> = {
  side: {
    md: "absolute inset-0 z-20 md:left-auto md:w-[460px] md:border-l md:border-line md:shadow-float xl:static xl:z-auto xl:shrink-0 xl:shadow-none",
    lg: "absolute inset-0 z-20 md:left-auto md:w-[560px] md:border-l md:border-line md:shadow-float xl:static xl:z-auto xl:shrink-0 xl:shadow-none",
  },
  overlay: {
    md: "absolute inset-0 z-30 md:left-auto md:w-[460px] md:border-l md:border-line md:shadow-float",
    lg: "absolute inset-0 z-30 md:left-auto md:w-[560px] md:border-l md:border-line md:shadow-float",
  },
  fill: {
    md: "pointer-events-auto relative h-full w-[min(460px,100vw)] md:border-l md:border-line md:shadow-float",
    lg: "pointer-events-auto relative h-full w-[min(560px,100vw)] md:border-l md:border-line md:shadow-float",
  },
};

// ---- which open panel takes Esc (the file viewer sits above the others) ----
const openPanels: OpenPanel[] = [];
let seq = 0;

// ---- phone: full screen, focus kept inside ----
const subscribePhone = (cb: () => void) => {
  if (typeof window === "undefined" || !window.matchMedia) return () => {};
  const mq = window.matchMedia(PHONE_QUERY);
  mq.addEventListener("change", cb);
  return () => mq.removeEventListener("change", cb);
};
const isPhoneNow = () => typeof window !== "undefined" && !!window.matchMedia && window.matchMedia(PHONE_QUERY).matches;
export const useIsPhone = () => useSyncExternalStore(subscribePhone, isPhoneNow, () => false);

const FOCUSABLE = 'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])';

export interface DetailPanelProps {
  /** data-testid of the panel */
  testId: string;
  /** what it is, in one word ("Pedido", "Tarea", "Cliente", "Archivo"): shown small above the title */
  label: string;
  /** the glossary concept: its icon goes next to the label */
  concept?: Concept;
  title: ReactNode;
  titleTestId?: string;
  /** small line under the title (a path, a tax id) */
  subtitle?: ReactNode;
  /** before the title (a file badge, a color mark) */
  leading?: ReactNode;
  /** a row under the title: state, dates, badges */
  meta?: ReactNode;
  /** buttons of the item (Edit, Archive…) under the title */
  actions?: ReactNode;
  /** tabs, toolbars, body and footer: the panel is a column, the body should be `min-h-0 flex-1 overflow-y-auto` */
  children: ReactNode;
  onClose: () => void;
  ariaLabel: string;
  closeLabel?: string;
  closeTestId?: string;
  backTestId?: string;
  layout?: DetailLayout;
  width?: "md" | "lg";
  /** higher layers take Esc first (the file viewer: 2) */
  layer?: number;
  /** false while Esc must not close (an editor with unsaved text) */
  canEscape?: () => boolean;
  className?: string;
  /** extra data-* attributes for tests and styling */
  data?: Record<`data-${string}`, string | number | undefined>;
}

export function DetailPanel({
  testId, label, concept, title, titleTestId, subtitle, leading, meta, actions, children, onClose, ariaLabel, closeLabel,
  closeTestId, backTestId, layout = "side", width = "md", layer = 1, canEscape, className = "", data,
}: DetailPanelProps) {
  const { t } = useT();
  const id = useId();
  const box = useRef<HTMLElement>(null);
  const phone = useIsPhone();
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  const guard = useRef(canEscape);
  guard.current = canEscape;

  // focus goes into the panel when it opens and back to what opened it when it closes
  useEffect(() => {
    const before = document.activeElement as HTMLElement | null;
    const el = box.current;
    if (el && !el.contains(document.activeElement)) el.focus({ preventScroll: true });
    return () => {
      const now = document.activeElement;
      const lost = !now || now === document.body || (el?.contains(now) ?? false) || !now.isConnected;
      if (lost && before?.isConnected && typeof before.focus === "function") before.focus({ preventScroll: true });
    };
  }, []);

  // Esc closes the topmost panel (not while another dialog is open on top, not when someone already handled it)
  useEffect(() => {
    const me: OpenPanel = { id, layer, seq: ++seq };
    openPanels.push(me);
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented) return;
      if (topPanel(openPanels) !== id) return;
      if (document.querySelector('[aria-modal="true"]:not([data-detail-panel])')) return;
      if (guard.current && !guard.current()) return;
      closeRef.current();
    };
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("keydown", onKey);
      const i = openPanels.indexOf(me);
      if (i >= 0) openPanels.splice(i, 1);
    };
  }, [id, layer]);

  // a phone shows nothing else: Tab stays inside the panel
  const onKeyDown = (e: ReactKeyboardEvent<HTMLElement>) => {
    if (!phone || e.key !== "Tab" || !box.current) return;
    const items = [...box.current.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((x) => x.offsetParent !== null || x === document.activeElement);
    const next = trapIndex(items.length, items.indexOf(document.activeElement as HTMLElement), e.shiftKey);
    if (next < 0) return;
    e.preventDefault();
    items[next]?.focus();
  };

  const c = concept ? conceptOf(concept) : null;
  return (
    <aside
      ref={box} tabIndex={-1} role="dialog" aria-modal={phone ? "true" : "false"} aria-label={ariaLabel} data-testid={testId} data-detail-panel=""
      onKeyDown={onKeyDown} {...data}
      className={`${LAYOUT[layout][width]} flex min-h-0 flex-col bg-panel outline-none focus-visible:outline-none ${className}`}
    >
      <header className="shrink-0 border-b border-line px-4 pb-3 pt-2.5">
        <div className="flex items-center gap-2">
          <button type="button" data-testid={backTestId} onClick={onClose}
            className="inline-flex min-h-10 items-center gap-1 rounded-md pr-2 text-[12px] font-semibold text-accent-hover hover:bg-panel2 md:hidden">
            <Icon name="back" size={15} />{t("detail.back")}
          </button>
          <span className="hidden items-center gap-1.5 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute md:inline-flex">
            {c && <span aria-hidden className={`flex h-[18px] w-[18px] items-center justify-center rounded ${c.tint}`}><Icon name={c.icon} size={12} /></span>}
            {label}
          </span>
          <span className="flex-1" />
          <button type="button" data-testid={closeTestId} onClick={onClose} aria-label={closeLabel ?? t("detail.close")} title={closeLabel ?? t("detail.close")}
            className="hidden min-h-9 min-w-9 items-center justify-center rounded-md text-mute hover:bg-panel2 hover:text-ink md:flex">
            <Icon name="x" size={15} />
          </button>
        </div>
        <div className="mt-1 flex items-start gap-2.5">
          {leading && <span className="mt-0.5 shrink-0">{leading}</span>}
          <div className="min-w-0 flex-1">
            <h2 data-testid={titleTestId} className="line-clamp-2 break-words text-[15px] font-semibold leading-snug tracking-tight text-ink">{title}</h2>
            {subtitle && <div className="mt-0.5 text-[11px] text-mute">{subtitle}</div>}
          </div>
        </div>
        {meta && <div className="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11.5px] text-mute">{meta}</div>}
        {actions && <div className="mt-2.5 flex flex-wrap items-center gap-1.5">{actions}</div>}
      </header>
      {children}
    </aside>
  );
}

// ---- the URL says what is open, so Back closes the panel ----
const pushed = new Map<string, string>(); // param -> the href the panel added to the history
const backPending = new Set<string>();
let popWatch = false;

function release(param: string) {
  if (typeof window === "undefined") return;
  const { pathname, search } = window.location;
  const act = releaseAction({ current: detailParam(search, param), here: pathname + search, pushedHref: pushed.get(param), backPending: backPending.has(param) });
  if (act === "none") return;
  pushed.delete(param);
  if (act === "back") { backPending.add(param); window.history.back(); }
  else window.history.replaceState(null, "", detailHref(pathname, search, param, null));
}

/** Before closing a panel and navigating somewhere else at once: drop the parameter without going Back. */
export function dropDetailUrl(param: string) {
  if (typeof window === "undefined") return;
  pushed.delete(param);
  const { pathname, search } = window.location;
  if (detailParam(search, param)) window.history.replaceState(null, "", detailHref(pathname, search, param, null));
}

/**
 * Mirrors the open item in `?<param>=<value>` while the calling panel is mounted: opening adds a history entry, switching
 * to another item replaces it, closing (X, Esc, the item disappearing) goes back. The browser's Back or Forward calls
 * `onUrl` with what the URL now says (null: close it).
 */
export function useDetailUrl(param: string, value: string | null | undefined, onUrl: (value: string | null) => void) {
  const valueRef = useRef(value || null);
  valueRef.current = value || null;
  const onUrlRef = useRef(onUrl);
  onUrlRef.current = onUrl;
  const mounted = useRef(false);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const { pathname, search } = window.location;
    const step = historyStep(detailParam(search, param), value);
    if (step === "push") {
      const href = detailHref(pathname, search, param, value!);
      window.history.pushState(null, "", href);
      pushed.set(param, href);
    } else if (step === "replace") {
      const href = detailHref(pathname, search, param, value!);
      window.history.replaceState(null, "", href);
      if (pushed.has(param)) pushed.set(param, href);
    } else if (step === "remove") release(param);
  }, [param, value]);

  useEffect(() => {
    if (!popWatch) { popWatch = true; window.addEventListener("popstate", () => backPending.clear()); }
    const onPop = () => {
      const cur = detailParam(window.location.search, param) || null;
      if (cur === valueRef.current) return;
      if (!cur) pushed.delete(param);
      onUrlRef.current(cur);
    };
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, [param]);

  // unmounted (closed from anywhere): drop the parameter. Deferred, so React's dev double mount does not close it.
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      setTimeout(() => { if (!mounted.current) release(param); }, 0);
    };
  }, [param]);
}
