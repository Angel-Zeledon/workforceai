"use client";
import { createContext, useContext } from "react";
import { useT } from "@/lib/i18n";

/** Provided by a screen that shows a dialog's content as a PAGE (breadcrumb + inline body) instead of a modal
 *  that covers the sidebar. `back` returns to the screen it was opened from. */
export const PageModeCtx = createContext<{ back: () => void } | null>(null);
export const usePageMode = () => useContext(PageModeCtx);

/** "Cerrar" for a modal, "Volver" when the same content is shown as a page. */
export function useCloseLabel() {
  const { t } = useT();
  return usePageMode() ? t("common.back") : t("common.close");
}
