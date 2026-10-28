/** Evidence chain API (B6). An older backend without the endpoint just means "no linked figures". */
import { call } from "./api";
import type { ReportEvidence } from "./evidenceLogic";

export async function fetchReportEvidence(reportId: string): Promise<ReportEvidence | null> {
  try {
    const ev = await call<ReportEvidence>("GET", `/reports/${encodeURIComponent(reportId)}/evidence`);
    return ev && Array.isArray(ev.citations) ? ev : null;
  } catch {
    return null;
  }
}
