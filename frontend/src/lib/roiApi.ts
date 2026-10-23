import { call } from "./api";
import type { RoiReport, RoiSettings, RoiSettingsView } from "./roiLogic";

/** B15 endpoints (docs/architecture/08-api.md section 28). */
export const roiApi = {
  report: (windowDays: number) => call<RoiReport>("GET", `/roi?window_days=${windowDays}`),
  settings: () => call<RoiSettingsView>("GET", "/settings/roi"),
  save: (s: RoiSettings) => call<RoiSettingsView>("PUT", "/settings/roi", s),
};
