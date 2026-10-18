"use client";
import { useToasts, type ToastKind } from "@/lib/toast";
import { useT } from "@/lib/i18n";
import { Icon, type IconName } from "../icons";

const STYLE: Record<ToastKind, { border: string; text: string; icon: IconName }> = {
  ok: { border: "border-ok/30", text: "text-ok", icon: "check" },
  info: { border: "border-accent/30", text: "text-accent", icon: "alert" },
  warn: { border: "border-warn/40", text: "text-warn", icon: "alert" },
  err: { border: "border-err/40", text: "text-err", icon: "alert" },
};

/** Stack of short confirmations (bottom center). Announced politely to screen readers. */
export function Toaster() {
  const { t } = useT();
  const items = useToasts((s) => s.items);
  const dismiss = useToasts((s) => s.dismiss);
  return (
    <div aria-live="polite" role="status" data-testid="toaster" className="pointer-events-none fixed inset-x-0 bottom-5 z-[120] flex flex-col items-center gap-2 px-3 pb-[env(safe-area-inset-bottom)]">
      {items.map((it) => (
        <div key={it.id} data-testid="toast" data-kind={it.kind} className={`ac-toast pointer-events-auto flex w-full max-w-md items-center sm:w-auto gap-2.5 rounded-lg border bg-panel px-4 py-2.5 text-[13px] font-medium text-ink shadow-float ${STYLE[it.kind].border}`}>
          <Icon name={STYLE[it.kind].icon} size={16} className={STYLE[it.kind].text} />
          <span>{it.text}</span>
          <button type="button" aria-label={t("common.close")} onClick={() => dismiss(it.id)} className="-my-2 -mr-2 ml-1 p-2.5 text-mute hover:text-ink"><Icon name="x" size={14} /></button>
        </div>
      ))}
    </div>
  );
}
