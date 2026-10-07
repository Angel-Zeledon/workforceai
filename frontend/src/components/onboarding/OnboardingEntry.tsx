"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { orgApi, type OrgSettings } from "@/lib/orgConfigApi";
import { OnboardingWizard } from "./OnboardingWizard";
import { OrgConfigPanel } from "./OrgConfigPanel";

const DISMISS_KEY = "aiw.onboarding.dismissed";
// Opt-in: open the wizard by itself on first run (off by default so it never
// covers the UI for automated tests or demos). Set NEXT_PUBLIC_ONBOARDING_AUTOOPEN=true.
const AUTO_OPEN = process.env.NEXT_PUBLIC_ONBOARDING_AUTOOPEN === "true";

function readDismissed(): boolean {
  try { return window.localStorage.getItem(DISMISS_KEY) === "1"; } catch { return false; }
}
function writeDismissed() {
  try { window.localStorage.setItem(DISMISS_KEY, "1"); } catch { /* storage unavailable */ }
}

/**
 * Header entry for organization setup. Until onboarding is completed it shows
 * a highlighted "Set up your office" button that opens the wizard; afterwards
 * it becomes "Team settings" (regional tone, daily briefing, fiscal examples).
 * Renders nothing when the backend does not expose the endpoints.
 */
export function OnboardingEntry() {
  const { t } = useT();
  const [settings, setSettings] = useState<OrgSettings | null>(null);
  const [unsupported, setUnsupported] = useState(false);
  const [wizard, setWizard] = useState(false);
  const [panel, setPanel] = useState(false);

  const load = useCallback(() => {
    orgApi.settings().then(setSettings).catch(() => setUnsupported(true));
  }, []);
  useEffect(() => { load(); }, [load]);
  useEffect(() => {
    if (AUTO_OPEN && settings && !settings.onboarding_completed && !readDismissed()) setWizard(true);
  }, [settings]);

  if (unsupported || !settings) return null;
  const pending = !settings.onboarding_completed;
  return (
    <>
      {pending ? (
        <button type="button" data-testid="onboarding-open" onClick={() => setWizard(true)} title={t("onb.prompt.body")}
          className="animate-pulse rounded-md border border-accent bg-accent/10 px-3 py-1 font-semibold text-accent hover:animate-none">{t("onb.prompt.title")}</button>
      ) : (
        <button type="button" data-testid="org-config-open" onClick={() => setPanel(true)}
          className="rounded-md border border-line px-3 py-1 font-semibold hover:text-ink">{t("cfg.open")}</button>
      )}
      {wizard && <OnboardingWizard onClose={() => { writeDismissed(); setWizard(false); load(); }} onDone={(r) => setSettings(r.settings)} />}
      {panel && <OrgConfigPanel onClose={() => { setPanel(false); load(); }} />}
    </>
  );
}
