"use client";
import { useEffect, useRef, type ReactNode } from "react";
import { useT } from "@/lib/i18n";

/** Accessible modal shell shared by the template gallery and the onboarding wizard. */
export function Modal({ title, onClose, children, testid, wide = false }: {
  title: string; onClose: () => void; children: ReactNode; testid: string; wide?: boolean;
}) {
  const { t } = useT();
  const ref = useRef<HTMLDivElement>(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose; // keep the latest handler without re-running the focus effect
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    ref.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      // Only the top-most dialog reacts, so Escape closes one layer at a time.
      const all = document.querySelectorAll('[role="dialog"]');
      if (all[all.length - 1] === ref.current) closeRef.current();
    };
    window.addEventListener("keydown", onKey);
    return () => { window.removeEventListener("keydown", onKey); prev?.focus?.(); };
  }, []);
  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center bg-ink/35 p-4" onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}>
      <div ref={ref} tabIndex={-1} role="dialog" aria-modal="true" aria-label={title} data-testid={testid}
        className={`pointer-events-auto flex max-h-[88vh] w-full flex-col overflow-hidden rounded-xl border border-line bg-panel text-ink shadow-float outline-none ${wide ? "max-w-4xl" : "max-w-xl"}`}>
        <div className="flex items-center justify-between border-b border-line px-5 py-3">
          <h2 className="text-[15px] font-semibold tracking-tight">{title}</h2>
          <button type="button" data-testid={`${testid}-close`} onClick={onClose} aria-label={t("common.close")}
            className="rounded-md border border-line px-2.5 py-1 text-xs font-medium text-ink2 hover:bg-panel2 hover:text-ink">{t("common.close")}</button>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">{children}</div>
      </div>
    </div>
  );
}
