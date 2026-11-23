"use client";
import { useT } from "@/lib/i18n";
import { useOffice3dNotice } from "@/lib/officeView";

/** The first time the 3D opens: it uses more of the device, and "Lista" brings you back. Dismissed once, remembered here. */
export function Office3dNotice() {
  const { t } = useT();
  const { show, dismiss } = useOffice3dNotice();
  if (!show) return null;
  return (
    <div role="status" data-testid="office3d-notice" className="pointer-events-none absolute inset-x-0 top-3 z-[60] flex justify-center px-3">
      <div className="pointer-events-auto flex max-w-md items-start gap-3 rounded-[10px] border border-line-strong bg-panel px-3.5 py-2.5 text-[13px] leading-snug text-ink shadow-float">
        <span className="min-w-0 flex-1">{t("mode.notice3d")}</span>
        <button type="button" data-testid="office3d-notice-ok" onClick={dismiss} className="-my-1 -mr-1.5 min-h-10 shrink-0 rounded-md px-3 text-[13px] font-semibold text-accent hover:bg-accent-soft">{t("mode.notice3d.ok")}</button>
      </div>
    </div>
  );
}
