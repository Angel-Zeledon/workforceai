"use client";
import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useT } from "@/lib/i18n";
import { go } from "@/lib/navBridge";
import { Icon } from "../icons";
import { conceptOf, type Concept } from "./ConceptLegend";

const POP_W = 320;

/**
 * "¿Qué es esto?" (proposal 5.4, point 4): a small "?" next to a title. It opens the sentence of the glossary, an example
 * and "Ver dónde se usa" (the screen of the concept): a popover under the button on a computer, a sheet from the bottom
 * on a phone. Closes with Esc, the X or a tap outside, and gives the focus back to the button.
 */
export function ConceptHelp({ concept, className = "" }: { concept: Concept; className?: string }) {
  const { t } = useT();
  const c = conceptOf(concept);
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null); // null = bottom sheet (phone)
  const btn = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const uid = useId();
  const panelId = `concept-help-${uid}`;
  const name = t(`concepts.${concept}.name`);

  const place = useCallback(() => {
    const b = btn.current;
    if (!b) return;
    if (window.matchMedia("(max-width: 639px)").matches) { setPos(null); return; }
    const r = b.getBoundingClientRect();
    setPos({ top: r.bottom + 8, left: Math.max(8, Math.min(r.left - 12, window.innerWidth - POP_W - 8)) });
  }, []);

  const close = useCallback((focusBack: boolean) => {
    setOpen(false);
    if (focusBack) btn.current?.focus();
  }, []);

  useLayoutEffect(() => { if (open) place(); }, [open, place]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") { e.stopPropagation(); close(true); } };
    const onDown = (e: PointerEvent) => {
      const target = e.target as Node;
      if (panel.current?.contains(target) || btn.current?.contains(target)) return;
      close(false);
    };
    document.addEventListener("keydown", onKey, true);
    document.addEventListener("pointerdown", onDown, true);
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    panel.current?.focus();
    return () => {
      document.removeEventListener("keydown", onKey, true);
      document.removeEventListener("pointerdown", onDown, true);
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
    };
  }, [open, place, close]);

  const sheet = pos === null;
  const where = c.where ?? c.href;

  return (
    <>
      <button ref={btn} type="button" data-testid={`concept-help-${concept}`} aria-haspopup="dialog" aria-expanded={open}
        aria-controls={open ? panelId : undefined} aria-label={t("concepts.help.aria", { name })} title={t("concepts.help.button")}
        onClick={() => setOpen((v) => !v)}
        className={`-my-2 -mx-1.5 inline-flex h-10 w-10 shrink-0 items-center justify-center rounded-full text-mute transition hover:bg-accent-soft hover:text-accent focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent sm:m-0 sm:h-6 sm:w-6 ${open ? "bg-accent-soft text-accent" : ""} ${className}`}>
        <Icon name="help" size={15} />
      </button>
      {open && typeof document !== "undefined" && createPortal(
        <>
          {sheet && <div aria-hidden className="fixed inset-0 z-[70] bg-ink/30" />}
          <div ref={panel} id={panelId} role="dialog" aria-modal={sheet ? true : undefined} aria-labelledby={`${panelId}-t`} tabIndex={-1}
            data-testid="concept-help-panel" data-concept={concept}
            style={sheet ? undefined : { top: pos.top, left: pos.left, width: POP_W }}
            className={`ac-pop fixed z-[71] border border-line bg-panel text-left shadow-pop outline-none ${sheet
              ? "inset-x-0 bottom-0 max-h-[80dvh] overflow-y-auto rounded-t-2xl px-4 pb-[max(1rem,env(safe-area-inset-bottom))] pt-3"
              : "rounded-xl p-4"}`}>
            {sheet && <div aria-hidden className="mx-auto mb-3 h-1 w-10 rounded-full bg-line-strong" />}
            <div className="flex items-start gap-2.5">
              <span className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg ${c.tint}`}><Icon name={c.icon} size={16} /></span>
              <div className="min-w-0 flex-1">
                <h2 id={`${panelId}-t`} className="text-[14px] font-semibold leading-tight text-ink">{name}</h2>
                <div className="mt-0.5 text-[11px] font-semibold uppercase tracking-wide text-mute">{t(`concepts.${concept}.what`)}</div>
              </div>
              <button type="button" data-testid="concept-help-close" onClick={() => close(true)} aria-label={t("concepts.help.close")}
                className="-m-1.5 flex h-10 w-10 shrink-0 items-center justify-center rounded text-mute transition hover:bg-panel2 hover:text-ink sm:m-0 sm:h-7 sm:w-7">
                <Icon name="x" size={14} />
              </button>
            </div>
            <p className="mt-2.5 text-[13px] leading-relaxed text-ink2">{t(`concepts.${concept}.body`)}</p>
            <p className="mt-1.5 text-[12px] leading-snug text-mute">{t(`concepts.${concept}.example`)}</p>
            <button type="button" data-testid="concept-help-where" onClick={() => { close(false); go(where); }}
              className="mt-3 inline-flex min-h-10 items-center gap-1 text-[12.5px] font-semibold text-accent hover:underline sm:min-h-0">
              {t("concepts.help.where")} <Icon name="chevron" size={13} />
            </button>
          </div>
        </>,
        document.body,
      )}
    </>
  );
}
