"use client";
import { useCallback, useEffect, useState } from "react";
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
 * Organization setup entry, owned by the admin menu. Until onboarding is
 * completed the menu item opens the wizard ("Set up your office", data-testid
 * `onboarding-open`); afterwards it opens "Team settings" (regional tone, daily
 * briefing, fiscal examples; `org-config-open`). `supported` is false when the
 * backend does not expose the endpoints. Render `dialogs` once, outside the menu,
 * so they outlive it closing.
 */
export function useOnboardingEntry() {
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

  const pending = !!settings && !settings.onboarding_completed;
  const dialogs = (
    <>
      {wizard && <OnboardingWizard onClose={() => { writeDismissed(); setWizard(false); load(); }} onDone={(r) => setSettings(r.settings)} />}
      {panel && <OrgConfigPanel onClose={() => { setPanel(false); load(); }} />}
    </>
  );
  return {
    supported: !unsupported && !!settings,
    pending,
    open: () => (pending ? setWizard(true) : setPanel(true)),
    dialogs,
  };
}
