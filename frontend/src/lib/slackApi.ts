import { call } from "./api";

/** Contract: backend/internal/slackapp (docs/architecture/channels.md). The two secrets are write-only. */

export type SlackRisk = "low" | "medium" | "high";
export type SlackLocale = "es" | "en" | "pt";

export interface SlackSettings {
  enabled: boolean; configured: boolean; signing_secret_set: boolean; bot_token_set: boolean; bot_token_last4?: string;
  team_id?: string; team_name?: string; channel_id: string; min_risk: SlackRisk; locale: SlackLocale;
  email_matching: boolean; high_risk_in_app: boolean; verified_at?: string; updated_by?: string; updated_at?: string;
  /** Where to point the Slack app: Interactivity & Shortcuts, and the slash command. */
  interactions_url: string; commands_url: string; command: string; scopes: string[];
  /** The deployment can seal secrets (vault key) / map Slack users to accounts (auth on). */
  vault_available: boolean; accounts_available: boolean;
}

export interface SlackInput {
  enabled?: boolean; signing_secret?: string; bot_token?: string; channel_id?: string; min_risk?: SlackRisk; locale?: SlackLocale;
  email_matching?: boolean; high_risk_in_app?: boolean;
}

export interface SlackTestResult { ok: boolean; team_id?: string; team_name?: string; posted: boolean; error?: string }
export interface SlackLink { team_id: string; slack_user_id: string; user_id: string; method: "code" | "email"; created_at: string }

export const slackApi = {
  settings: () => call<SlackSettings>("GET", "/channels/slack/settings"),
  save: (b: SlackInput) => call<SlackSettings>("PUT", "/channels/slack/settings", b),
  disconnect: () => call<{ disconnected: boolean }>("DELETE", "/channels/slack/settings"),
  test: () => call<SlackTestResult>("POST", "/channels/slack/test"),
  myLink: () => call<{ linked: boolean; links: SlackLink[] }>("GET", "/channels/slack/link"),
  linkCode: () => call<{ code: string; expires_at: string; command: string }>("POST", "/channels/slack/link-code"),
  unlink: () => call<{ removed: number }>("DELETE", "/channels/slack/link"),
};

/** HTTP status of an error thrown by `call` ("PUT /x -> 409"), or 0. */
export function statusOf(e: unknown): number {
  const m = /-> (\d{3})$/.exec(String((e as Error)?.message ?? ""));
  return m ? Number(m[1]) : 0;
}
