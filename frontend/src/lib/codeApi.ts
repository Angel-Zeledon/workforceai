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
}
export interface CodeSettings {
  enabled: boolean; max_runs_per_day: number; max_runtime_seconds: number;
  available: boolean; ceiling_runs_per_day: number; ceiling_runtime_seconds: number;
}

const TERMINAL: CodeRunStatus[] = ["succeeded", "failed", "timeout", "aborted", "rejected", "denied"];
export const isFinished = (s: CodeRunStatus) => TERMINAL.includes(s);

export const codeApi = {
  settings: () => call<CodeSettings>("GET", "/settings/code-execution"),
  putSettings: (s: Pick<CodeSettings, "enabled" | "max_runs_per_day" | "max_runtime_seconds">) => call<CodeSettings>("PUT", "/settings/code-execution", s),
  runs: async (limit = 20) => {
    const x = await call<CodeRun[]>("GET", `/code-runs?limit=${limit}`);
    return Array.isArray(x) ? x : [];
  },
  byApproval: async (approvalId: string): Promise<CodeRun | null> => {
    const x = await call<CodeRun[]>("GET", `/code-runs?approval_id=${encodeURIComponent(approvalId)}`);
    return Array.isArray(x) && x.length ? x[0] : null;
  },
};

export function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
