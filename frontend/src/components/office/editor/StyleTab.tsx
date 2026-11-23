"use client";
// The "Estilo" tab: a live gallery. The chosen style is a large preview with its name and a line; the other four are
// large windows two by two. All five show the same scene from the same camera, so the difference between them is the
// style and nothing else. The chosen one and the one under the pointer or the keyboard focus play their loop.
import { useEffect, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { useOfficeLayout } from "@/lib/officeLayout";
import { OFFICE_STYLES, normalizeStyle, type Layout, type OfficeStyle } from "@/lib/officeLayoutModel";
import { FOCUS } from "./parts";
import { StyleClip } from "./StyleClip";

export function StyleTab() {
  const { t } = useT();
  const layout = useOfficeLayout((s) => s.draft) as Layout;
  const mutate = useOfficeLayout((s) => s.mutate);
  const cur = normalizeStyle(layout.style);
  const [hot, setHot] = useState<OfficeStyle | null>(null); // under the pointer or holding the keyboard focus
  const [focusId, setFocusId] = useState<OfficeStyle>(cur);
  const group = useRef<HTMLDivElement>(null);
  const keep = useRef<OfficeStyle | null>(null); // who must hold the focus after the order changes
  const pick = (id: OfficeStyle) => {
    if (id === cur) return;
    keep.current = id;
    mutate((l) => {
      const { style: _drop, ...rest } = l; // eslint-disable-line @typescript-eslint/no-unused-vars
      return id === "classic" ? rest : { ...rest, style: id };
    });
  };
  const order = [cur, ...OFFICE_STYLES.filter((s) => s !== cur)];
  useEffect(() => {
    setFocusId(cur);
    if (keep.current) { group.current?.querySelector<HTMLElement>(`[data-style="${keep.current}"]`)?.focus(); keep.current = null; }
  }, [cur]);
  // arrows move between the windows (the first one is the chosen style); Space or Enter chooses
  const onKey = (e: React.KeyboardEvent) => {
    const i = order.indexOf(focusId);
    let to = -1;
    if (e.key === "ArrowRight" || e.key === "ArrowDown") to = (i + 1) % order.length;
    else if (e.key === "ArrowLeft" || e.key === "ArrowUp") to = (i - 1 + order.length) % order.length;
    else if (e.key === "Home") to = 0;
    else if (e.key === "End") to = order.length - 1;
    if (to < 0) return;
    e.preventDefault();
    setFocusId(order[to]);
    group.current?.querySelector<HTMLElement>(`[data-style="${order[to]}"]`)?.focus();
  };
  const win = (id: OfficeStyle, big: boolean) => {
    const on = cur === id;
    return (
      <button key={id} type="button" role="radio" data-testid={`oe-style-${id}`} data-style={id} aria-checked={on} tabIndex={focusId === id ? 0 : -1}
        onClick={() => pick(id)} onFocus={() => { setFocusId(id); setHot(id); }} onBlur={() => setHot((h) => (h === id ? null : h))}
        onPointerEnter={(e) => { if (e.pointerType !== "touch") setHot(id); }} onPointerLeave={() => setHot((h) => (h === id ? null : h))}
        className={`group block w-full min-w-0 overflow-hidden rounded-[10px] border bg-panel text-left transition ${FOCUS} ${on ? "border-accent ring-2 ring-accent/30" : "border-line hover:border-line-strong hover:shadow-soft"}`}>
        <StyleClip id={id} playing={on || hot === id} priority={big} />
        <span className={big ? "block px-3 py-2.5" : "block px-2.5 py-2"}>
          <span className="flex items-center justify-between gap-2">
            <span className={`${big ? "text-[14px]" : "text-[12.5px]"} font-semibold text-ink`}>{t(`oe.style.${id}`)}</span>
            {on && <span className="shrink-0 rounded-full bg-accent-soft px-2 py-0.5 text-[11px] font-semibold text-accent">{t("oe.style.current")}</span>}
          </span>
          <span className={`mt-0.5 block text-mute ${big ? "text-[12px] leading-snug" : "text-[11.5px] leading-snug line-clamp-2"}`}>{t(`oe.style.${id}.d`)}</span>
        </span>
      </button>
    );
  };
  return (
    <div className="space-y-3.5">
      <p className="text-[12px] leading-snug text-mute">{t("oe.style.hint")}</p>
      <div ref={group} role="radiogroup" aria-label={t("oe.style.title")} onKeyDown={onKey} className="space-y-3">
        {win(order[0], true)}
        <div className="grid grid-cols-2 gap-2.5">{order.slice(1).map((id) => win(id, false))}</div>
      </div>
      <p className="text-[11.5px] leading-snug text-mute">{t("oe.style.same")}</p>
    </div>
  );
}
