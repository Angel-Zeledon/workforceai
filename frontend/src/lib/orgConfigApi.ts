import { call } from "./api";
import type { Locale } from "./i18n-core";

/** Contract: docs/architecture/08-api.md section 11 (additive, /api/v1). */

export interface TemplateParam { key: string; label_key: string; label: string; required: boolean; default: string }
export interface TemplateStep {
  key: string; title_key: string; title: string; description_key: string; description: string;
  agent_id: string; depends_on: string[];
  /** 1-based longest-path level: steps sharing a stage run in parallel. */
  stage: number;
}
export interface WorkflowTemplate {
  key: string; version: number; category: string;
  name_key: string; name: string; description_key: string; description: string;
  params: TemplateParam[]; steps: TemplateStep[]; parallel_steps: number;
}

export interface PackAgent { id: string; autonomy: string }
export interface PackMemory { agent_id: string; scope: string; key: string; value_key: string; value: string }
export interface OnboardingPack {
  key: string; version: number; name_key: string; name: string; description_key: string; description: string;
  agents: PackAgent[]; memory: PackMemory[];
  rules: { approval_amount_usd: number; always_approve: string[] };
  templates: string[]; briefing: { hour: number; minute: number };
}

export type ToneCode = "neutral" | "mx" | "co" | "ar" | "cl" | "es";
export const TONE_CODES: ToneCode[] = ["neutral", "mx", "co", "ar", "cl", "es"];

export interface OrgSettings {
  configured: boolean; locale: Locale; tone: ToneCode; business_type: string; pack_version: number;
  onboarding_completed: boolean; onboarding_completed_at: string | null;
  rules: { approval_amount_usd: number; always_approve: string[] };
  agent_tones: Record<string, ToneCode>;
  recommended_templates: string[];
  available_tones: ToneCode[]; available_locales: Locale[];
}

export interface Schedule {
  id: string; template_key: string; params: Record<string, string>;
  hour: number; minute: number; timezone: string; weekdays: number[]; enabled: boolean;
  next_run_at: string | null; last_run_at: string | null; created_at: string;
}
export interface ScheduleInput { hour: number; minute: number; timezone: string; weekdays?: number[]; enabled?: boolean }

export interface OnboardInput {
  pack_key: string; locale?: Locale; tone?: ToneCode; force?: boolean;
  briefing?: { enabled: boolean; hour: number; minute: number; timezone: string; weekdays?: number[] };
}
export interface OnboardResult {
  settings: OrgSettings; agents_configured: string[]; memory_seeded: number; schedule: Schedule | null;
}

export interface TaxTemplate {
  key: string; country: string; example: boolean; name: string; disclaimer: string;
  fields: { key: string; label: string; kind: string; example: string }[];
  checklist: { key: string; text: string }[];
}

const arr = <T,>(x: unknown): T[] => (Array.isArray(x) ? (x as T[]) : []);
const q = (locale?: Locale) => (locale ? `?locale=${locale}` : "");

export const orgApi = {
  templates: async (locale?: Locale) => arr<WorkflowTemplate>(await call("GET", `/workflow-templates${q(locale)}`)),
  instantiate: (key: string, params: Record<string, string>, locale?: Locale) =>
    call<{ request_id: string }>("POST", `/workflow-templates/${encodeURIComponent(key)}/instantiate`, { params, locale }),
  packs: async (locale?: Locale) => arr<OnboardingPack>(await call("GET", `/onboarding/packs${q(locale)}`)),
  settings: () => call<OrgSettings>("GET", "/org/settings"),
  onboard: (body: OnboardInput) => call<OnboardResult>("POST", "/onboarding", body),
  updateSettings: (body: { locale?: Locale; tone?: ToneCode }) => call<OrgSettings>("PUT", "/org/settings", body),
  setAgentTone: (agentId: string, tone: ToneCode | "") => call<OrgSettings>("PUT", `/agents/${encodeURIComponent(agentId)}/tone`, { tone }),
  schedules: async () => arr<Schedule>(await call("GET", "/schedules")),
  createSchedule: (body: ScheduleInput) => call<Schedule>("POST", "/schedules", body),
  updateSchedule: (id: string, body: ScheduleInput) => call<Schedule>("PUT", `/schedules/${encodeURIComponent(id)}`, body),
  deleteSchedule: (id: string) => call<unknown>("DELETE", `/schedules/${encodeURIComponent(id)}`),
  taxTemplates: async (locale?: Locale) => arr<TaxTemplate>(await call("GET", `/tax-templates${q(locale)}`)),
};

/** Browser time zone (IANA), falling back to UTC. */
export function browserTimeZone(): string {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC"; } catch { return "UTC"; }
}
