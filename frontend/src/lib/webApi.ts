import { call } from "./api";

/** Web tool settings (capability 4). Contract: backend/internal/api/web.go, docs/architecture/agent-web.md. */
export type WebMode = "open" | "allowlist";
export interface WebSettings {
  enabled: boolean; mode: WebMode;
  allowed_domains: string[]; blocked_domains: string[];
  max_calls_per_task: number; max_calls_per_day: number;
  search_available: boolean; operator_restricted: boolean;
  ceiling_calls_per_day: number; default_calls_per_day: number; default_calls_per_task: number;
  searches_today: number; fetches_today: number; calls_today: number; day_limit: number;
}

type Editable = Pick<WebSettings, "enabled" | "mode" | "allowed_domains" | "blocked_domains" | "max_calls_per_task" | "max_calls_per_day">;

/** Lists can arrive as null from older backends: iterate safely. */
const norm = (s: WebSettings): WebSettings => ({ ...s, allowed_domains: s.allowed_domains ?? [], blocked_domains: s.blocked_domains ?? [] });

export const webApi = {
  settings: async () => norm(await call<WebSettings>("GET", "/settings/web")),
  putSettings: async (s: Editable) => norm(await call<WebSettings>("PUT", "/settings/web", s)),
};
