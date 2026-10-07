"use client";
import { useCallback } from "react";
import { usePreferences } from "./preferences";
import { tr, type Locale, type Vars } from "./i18n-core";

export { tr, getLocale, fmtUsd, fmtNumber, fmtPercent, fmtTime, fmtDateTime, trOr, LOCALES } from "./i18n-core";
export type { Locale } from "./i18n-core";

/** Hook de traducción: re-renderiza al cambiar el idioma en Ajustes de oficina. */
export function useT() {
  const locale = usePreferences((s) => s.prefs.locale) as Locale;
  const t = useCallback((key: string, vars?: Vars) => tr(key, vars, locale), [locale]);
  return { t, locale };
}
