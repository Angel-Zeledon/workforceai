"use client";
import { useCallback, useEffect, useState } from "react";
import { teamsApi } from "@/lib/teamsApi";
import type { Welcome } from "@/lib/teamsLogic";
import { TeamSetupWizard } from "./TeamSetupWizard";

/**
 * Admin-menu entry of the responsible's team set-up (E1): shown while the organization's teams are not set up yet
 * (the welcome offers it once; this keeps it reachable). `dialogs` must be rendered outside the menu.
 */
export function useTeamSetupEntry() {
  const [w, setW] = useState<Welcome | null>(null);
  const [open, setOpen] = useState(false);
  const load = useCallback(() => { void teamsApi.welcome().then(setW); }, []);
  useEffect(() => { load(); }, [load]);
  const pending = !!w && w.kind === "owner" && w.setup_pending;
  return {
    pending,
    open: () => setOpen(true),
    dialogs: open && w ? <TeamSetupWizard welcome={w} onClose={() => setOpen(false)} onDone={() => { setOpen(false); load(); }} /> : null,
  };
}
