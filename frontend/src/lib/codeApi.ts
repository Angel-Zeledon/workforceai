import { call } from "./api";

/** Code sandbox (F3). Contract: backend/internal/api/code.go and docs/architecture/08-api.md section 20. */
export interface CodeLimits { timeout_seconds: number; memory_mb: number; cpus: number; pids: number; max_output_bytes: number; max_output_file_bytes: number; max_output_files: number }
export interface CodeInput { id: string; name: string; size: number; sha256: string }
export interface CodeOutput { name: string; size: number; sha256?: string; file_ref?: string; error?: string }
export type CodeRunStatus = "awaiting_approval" | "running" | "succeeded" | "failed" | "timeout" | "aborted" | "rejected" | "denied";
export interface CodeRun {
  id: string; agent_id: string; task_id: string; approval_id: string;
  language: string; code: string; purpose: string; inputs: CodeInput[]; expected_outputs: string[]; limits: CodeLimits;
  status: CodeRunStatus; deny_reason?: string; approvers?: string[]; exit_code?: number; duration_ms: number;
  stdout: string; stderr: string; stdout_truncated: boolean; stderr_truncated: boolean; outputs: CodeOutput[];
  created_at: string; started_at?: string; finished_at?: string;
  /** Desk mode: the run started without a human approval. auto_fallback: why a desk-mode request still needed one. */
  auto_run?: boolean; auto_fallback?: string;
}
export type CodeAutoRun = "off" | "desk";
export interface CodeSettings {
  enabled: boolean; max_runs_per_day: number; max_runtime_seconds: number;
  available: boolean; ceiling_runs_per_day: number; ceiling_runtime_seconds: number;
  /** "off" (default: every run needs approval) | "desk" (runs on the agent's own files start without approval). */
  auto_run?: CodeAutoRun;
  auto_max_code_bytes?: number; auto_max_input_bytes?: number; auto_max_runs_per_task?: number;
  auto_effective_code_bytes?: number; auto_effective_input_bytes?: number; auto_effective_runs_per_task?: number;
}
/** What a PUT carries. Turning desk mode on needs acknowledge_risk (the server refuses otherwise). */
export type CodeSettingsInput = Pick<CodeSettings, "enabled" | "max_runs_per_day" | "max_runtime_seconds" | "auto_run" | "auto_max_code_bytes" | "auto_max_input_bytes" | "auto_max_runs_per_task"> & { acknowledge_risk?: boolean };

const TERMINAL: CodeRunStatus[] = ["succeeded", "failed", "timeout", "aborted", "rejected", "denied"];
export const isFinished = (s: CodeRunStatus) => TERMINAL.includes(s);

/** Lists can arrive as null from older backends: iterate safely. */
const norm = (r: CodeRun): CodeRun => ({ ...r, inputs: r.inputs ?? [], outputs: r.outputs ?? [], expected_outputs: r.expected_outputs ?? [], stdout: r.stdout ?? "", stderr: r.stderr ?? "" });

export const codeApi = {
  settings: () => call<CodeSettings>("GET", "/settings/code-execution"),
  putSettings: (s: CodeSettingsInput) => call<CodeSettings>("PUT", "/settings/code-execution", s),
  runs: async (limit = 20) => {
    const x = await call<CodeRun[]>("GET", `/code-runs?limit=${limit}`);
    return Array.isArray(x) ? x.map(norm) : [];
  },
  byApproval: async (approvalId: string): Promise<CodeRun | null> => {
    const x = await call<CodeRun[]>("GET", `/code-runs?approval_id=${encodeURIComponent(approvalId)}`);
    return Array.isArray(x) && x.length ? norm(x[0]) : null;
  },
};

export function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
