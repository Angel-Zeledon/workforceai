import { API_URL, MOCK } from "./config";
import { authFetch } from "./session";

/**
 * Bring-your-own-model (owner/admin). Contract: backend/internal/api/llm.go.
 * No response ever contains an API key: only `key_last4`. The key a person types is sent once, in the
 * request body, and is cleared from the form right after.
 */
export interface LLMProvider {
  id: string; label: string; platform: boolean; models: string[]; default_model: string;
  default_base_url?: string; supports_base_url: boolean; requires_base_url: boolean; supports_org_id: boolean; key_url?: string;
  configured: boolean; model: string; base_url?: string; org_id?: string; project_id?: string;
  key_last4?: string; updated_at?: string; last_test_code?: string; last_test_at?: string;
}
export interface LLMView {
  active_provider: string; platform_default: string; using_platform_model: boolean; encryption_available: boolean;
  providers: LLMProvider[]; warnings: string[];
}
export interface LLMInput { api_key?: string; model?: string; base_url?: string; org_id?: string; project_id?: string }
export type LLMTestCode = "ok" | "invalid_key" | "quota" | "model_not_found" | "network" | "unknown" | "invalid_request" | "rate_limited";
export interface LLMTestResult { ok: boolean; code: LLMTestCode; message?: string; provider: string; model?: string; latency_ms: number }

/** An error that keeps the server's stable code and message (the shared `call` helper drops the body). */
export class LLMError extends Error {
  constructor(public status: number, public code: string, message: string) { super(message); }
}

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  if (MOCK) throw new LLMError(404, "mock", "not available in mock mode");
  const res = await authFetch(`${API_URL}${path}`, {
    method,
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    cache: "no-store",
  });
  const text = await res.text();
  let json: Record<string, unknown> = {};
  try { json = text ? JSON.parse(text) : {}; } catch { /* not JSON */ }
  if (!res.ok) throw new LLMError(res.status, String(json.code ?? ""), String(json.error ?? `${method} ${path} -> ${res.status}`));
  return json as T;
}

export const llmApi = {
  get: () => req<LLMView>("GET", "/settings/llm"),
  save: (provider: string, body: LLMInput) => req<LLMView>("PUT", `/settings/llm/providers/${encodeURIComponent(provider)}`, body),
  remove: (provider: string) => req<LLMView>("DELETE", `/settings/llm/providers/${encodeURIComponent(provider)}`),
  test: (provider: string, body: LLMInput) => req<LLMTestResult>("POST", `/settings/llm/providers/${encodeURIComponent(provider)}/test`, body),
  use: (provider: string) => req<LLMView>("PUT", "/settings/llm/active", { provider }),
};
