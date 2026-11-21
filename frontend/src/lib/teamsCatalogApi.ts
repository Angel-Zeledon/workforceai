import { call } from "./api";
import { MOCK } from "./config";
import type { Locale } from "./i18n-core";
import type { TeamCard, TeamInstallResult, TeamPreview } from "./teamsCatalogLogic";

/** Contract: backend/internal/api/teams_catalog.go (F2, /api/v1). The catalog texts come in Spanish or English; any
 *  other interface language gets Spanish (the server normalizes the locale). */
const loc = (l?: Locale) => (l === "en" ? "en" : "es");
const query = (p: Record<string, string | undefined>) => {
  const q = Object.entries(p).filter(([, v]) => v).map(([k, v]) => `${k}=${encodeURIComponent(v as string)}`).join("&");
  return q ? `?${q}` : "";
};
const mock = async () => (await import("./mock/teams-catalog-mock")).teamsCatalogMock;

export const teamsCatalogApi = {
  list: async (locale?: Locale, pack?: string): Promise<TeamCard[]> =>
    MOCK ? (await mock()).list(loc(locale), pack) : call<TeamCard[]>("GET", `/teams${query({ locale: loc(locale), pack })}`),
  get: async (key: string, locale?: Locale, officeId?: string): Promise<TeamPreview> =>
    MOCK ? (await mock()).get(key, loc(locale)) : call<TeamPreview>("GET", `/teams/${encodeURIComponent(key)}${query({ locale: loc(locale), office_id: officeId })}`),
  install: async (key: string, locale?: Locale, officeId?: string): Promise<TeamInstallResult> =>
    MOCK ? (await mock()).install(key, loc(locale)) : call<TeamInstallResult>("POST", `/teams/${encodeURIComponent(key)}/install${query({ locale: loc(locale) })}`, officeId ? { office_id: officeId } : {}),
};
