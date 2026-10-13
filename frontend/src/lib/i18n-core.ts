import es from "./i18n/es.json";
import en from "./i18n/en.json";
import pt from "./i18n/pt.json";

/** i18n ligero (sin rutas /es /en): catálogos planos es.json / en.json / pt.json, español por defecto. */
export type Locale = "es" | "en" | "pt";
export const LOCALES: Locale[] = ["es", "en", "pt"];
const CATALOGS: Record<Locale, Record<string, string>> = { es, en, pt };
const INTL_TAG: Record<Locale, string> = { es: "es-MX", en: "en-US", pt: "pt-BR" };

let current: Locale = "es";
export const getLocale = (): Locale => current;
export const setCurrentLocale = (l: Locale) => { current = l; };

/** Español salvo que el navegador sea claramente inglés o portugués. */
export function detectLocale(): Locale {
  try {
    const langs = (navigator.languages && navigator.languages.length ? navigator.languages : [navigator.language]) || [];
    const first = (langs[0] || "es").toLowerCase();
    return first.startsWith("en") ? "en" : first.startsWith("pt") ? "pt" : "es";
  } catch { return "es"; }
}

export type Vars = Record<string, string | number>;
/** t("clave", {n: 2}). Con `count` busca clave_one / clave_other. Si falta la clave cae a español (pt -> es -> clave), nunca muestra la clave cruda si existe en es. */
export function tr(key: string, vars?: Vars, locale: Locale = current): string {
  const cat = CATALOGS[locale] ?? CATALOGS.es;
  let k = key;
  if (vars && typeof vars.count === "number") {
    const plural = `${key}_${vars.count === 1 ? "one" : "other"}`;
    if (plural in cat || plural in CATALOGS.es) k = plural;
  }
  let s = cat[k] ?? CATALOGS.es[k] ?? key;
  if (vars) s = s.replace(/\{(\w+)\}/g, (_, n) => (n in vars ? String(vars[n]) : `{${n}}`));
  return s;
}
export const hasKey = (key: string, locale: Locale = current) => key in CATALOGS[locale] || key in CATALOGS.es;
/** Traduce por clave si existe (códigos de estado/riesgo del backend); si no, devuelve el valor tal cual. */
export const trOr = (key: string, fallback: string) => (hasKey(key) ? tr(key) : fallback);

const intlTag = () => INTL_TAG[current];
/** BCP-47 tag for Intl of the current UI locale (es-MX, en-US, pt-BR). */
export const intlLocale = () => INTL_TAG[current];
/** Native name of a language ("Español", "English", "Português") regardless of the UI locale. */
export const langName = (l: Locale) => tr(`lang.${l}`, undefined, l);
export const fmtNumber = (n: number, digits = 0) =>
  new Intl.NumberFormat(intlTag(), { minimumFractionDigits: digits, maximumFractionDigits: digits }).format(n);
export const fmtUsd = (n: number | undefined) => `$${fmtNumber(n ?? 0, 2)}`;
export const fmtPercent = (n: number, digits = 1) => `${fmtNumber(n, digits)}%`;
export const fmtTime = (iso?: string | null) => {
  if (!iso) return "—";
  const d = new Date(iso);
  return isNaN(d.getTime()) ? "—" : new Intl.DateTimeFormat(intlTag(), { hour: "2-digit", minute: "2-digit", second: "2-digit" }).format(d);
};
export const fmtDateTime = (iso?: string | null) => {
  if (!iso) return "—";
  const d = new Date(iso);
  return isNaN(d.getTime()) ? "—" : new Intl.DateTimeFormat(intlTag(), { dateStyle: "medium", timeStyle: "short" }).format(d);
};
