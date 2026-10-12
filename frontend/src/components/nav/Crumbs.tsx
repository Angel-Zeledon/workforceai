"use client";
import { useT } from "@/lib/i18n";
import { Icon } from "../icons";

/** "‹ Ajustes › Pantalla": where you are, and a way back. Leaving through the sidebar needs no "close". */
export function Crumbs({ root, title, onBack, backTestId = "crumb-back" }: { root: string; title: string; onBack: () => void; backTestId?: string }) {
  const { t } = useT();
  return (
    <nav aria-label={t("common.breadcrumb")} data-testid="crumbs" className="flex shrink-0 items-center gap-1.5 border-b border-line bg-panel px-4 py-2.5 text-[13px] sm:px-6">
      <button type="button" data-testid={backTestId} onClick={onBack} aria-label={t("common.back")} title={t("common.back")}
        className="rounded-md p-1 text-ink2 transition hover:bg-panel2 hover:text-ink"><Icon name="back" size={16} /></button>
      <button type="button" onClick={onBack} className="rounded px-1 font-medium text-mute transition hover:text-ink hover:underline">{root}</button>
      <Icon name="chevron" size={14} className="shrink-0 text-mute" />
      <span aria-current="page" className="min-w-0 truncate font-semibold text-ink">{title}</span>
    </nav>
  );
}
