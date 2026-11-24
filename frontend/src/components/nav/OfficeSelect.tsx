"use client";
import { useEffect, useId, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { useCurrentOffice } from "@/lib/currentOffice";
import { ALL_OFFICES } from "@/lib/currentOfficeLogic";
import { Icon } from "../icons";

interface Row { id: string; label: string; locked: boolean }

/** The office the screens show: "Todas mis oficinas" or one office. Closed (private, not a member) ones are listed with a
 *  padlock and cannot be entered. Absent with a single office. Button + listbox, keyboard and screen reader friendly. */
export function OfficeSelect({ className = "", block = false }: { className?: string; block?: boolean }) {
  const { t } = useT();
  const { current, choices, office, choose } = useCurrentOffice();
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const root = useRef<HTMLDivElement>(null);
  const btn = useRef<HTMLButtonElement>(null);
  const uid = useId();
  const rows: Row[] = choices ? [{ id: ALL_OFFICES, label: t("office.select.all"), locked: false }, ...choices.map((c) => ({ id: c.id, label: c.general ? t("offices.general") : c.name, locked: c.locked }))] : [];
  const selected = Math.max(0, rows.findIndex((r) => r.id === current));

  useEffect(() => {
    if (!open) return;
    const away = (e: PointerEvent) => { if (!root.current?.contains(e.target as Node)) setOpen(false); };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [open]);

  if (!choices) return null;
  const label = current === ALL_OFFICES ? t("office.select.all") : office?.general ? t("offices.general") : office?.name ?? t("office.select.all");
  const show = () => { setActive(selected); setOpen(true); };
  const pick = (i: number) => {
    const r = rows[i];
    if (!r || r.locked) return;
    choose(r.id); setOpen(false); btn.current?.focus();
  };
  const move = (from: number, step: 1 | -1) => { let i = from; for (let n = 0; n < rows.length; n++) { i = (i + step + rows.length) % rows.length; if (!rows[i].locked) return i; } return from; };
  const onKey = (e: React.KeyboardEvent) => {
    if (!open) { if (["ArrowDown", "ArrowUp", "Enter", " "].includes(e.key)) { e.preventDefault(); show(); } return; }
    if (e.key === "ArrowDown") { e.preventDefault(); setActive((a) => move(a, 1)); }
    else if (e.key === "ArrowUp") { e.preventDefault(); setActive((a) => move(a, -1)); }
    else if (e.key === "Home") { e.preventDefault(); setActive(0); }
    else if (e.key === "End") { e.preventDefault(); setActive(move(0, -1)); }
    else if (e.key === "Enter" || e.key === " ") { e.preventDefault(); pick(active); }
    else if (e.key === "Escape") { e.preventDefault(); setOpen(false); btn.current?.focus(); }
    else if (e.key === "Tab") setOpen(false);
  };

  return (
    <div ref={root} data-testid="office-select" data-current={current} className={`relative ${block ? "w-full" : "shrink-0"} ${className}`}>
      <button ref={btn} type="button" data-testid="office-select-button" aria-haspopup="listbox" aria-expanded={open} aria-controls={`${uid}-list`}
        aria-label={`${t("office.select.label")}: ${label}`} onClick={() => (open ? setOpen(false) : show())} onKeyDown={onKey}
        className={`flex min-h-10 items-center gap-1.5 rounded-lg border border-line bg-panel px-2.5 text-[13px] font-semibold text-ink transition hover:border-line-strong focus-visible:border-accent sm:min-h-9 ${block ? "w-full" : "max-w-[190px]"}`}>
        <Icon name="grid" size={14} className="shrink-0 text-accent" />
        <span className="min-w-0 flex-1 truncate text-left">{label}</span>
        <Icon name="chevron" size={13} className={`shrink-0 text-mute transition-transform ${open ? "-rotate-90" : "rotate-90"}`} />
      </button>
      {open && (
        <ul id={`${uid}-list`} role="listbox" aria-label={t("office.select.label")} aria-activedescendant={`${uid}-o${active}`} tabIndex={-1} data-testid="office-select-list"
          className="absolute left-0 top-[calc(100%+4px)] z-[70] max-h-[60vh] min-w-[220px] max-w-[min(320px,calc(100vw-24px))] overflow-y-auto rounded-[10px] border border-line bg-panel p-1 shadow-float">
          {rows.map((r, i) => {
            const on = r.id === current;
            return (
              <li key={r.id} id={`${uid}-o${i}`} role="option" aria-selected={on} aria-disabled={r.locked || undefined} data-testid={`office-option-${r.id}`}
                onPointerEnter={() => !r.locked && setActive(i)} onClick={() => pick(i)}
                className={`flex min-h-10 items-center gap-2 rounded-md px-2.5 text-[13px] sm:min-h-9 ${r.locked ? "cursor-not-allowed text-mute" : "cursor-pointer text-ink"} ${i === active && !r.locked ? "bg-panel2" : ""} ${on ? "font-semibold text-accent" : ""} ${i === 1 ? "mt-1 border-t border-line pt-1" : ""}`}>
                <span className="min-w-0 flex-1 truncate">{r.label}</span>
                {r.locked && <><Icon name="lock" size={13} className="shrink-0" /><span className="sr-only">{t("office.select.closed")}</span></>}
                {on && <Icon name="check" size={14} className="shrink-0" />}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
