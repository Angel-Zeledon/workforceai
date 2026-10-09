"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { ctlApi } from "@/lib/connections/api";
import { Btn } from "../ui";

/** Visible Pause/Resume of one agent (existing POST /agents/{id}/control). */
export function AgentPauseToggle({ id }: { id: string }) {
  const { t } = useT();
  const [paused, setPaused] = useState<boolean | null>(null);
  const [err, setErr] = useState(false);
  const [busy, setBusy] = useState(false);
  const load = useCallback(async () => {
    try { const all = await ctlApi.agentControls(); setPaused(all.find((c) => c.agent_id === id)?.control === "paused"); } catch { setPaused(null); }
  }, [id]);
  useEffect(() => { setPaused(null); load(); }, [load]);
  const toggle = async () => {
    setBusy(true); setErr(false);
    try { const r = await ctlApi.agentControl(id, paused ? "resume" : "pause", paused ? undefined : "manual"); setPaused(r.control === "paused"); } catch { setErr(true); } finally { setBusy(false); }
  };
  return (
    <div className="flex items-center gap-2">
      <Btn data-testid="agent-pause-toggle" kind={paused ? "ok" : "ghost"} onClick={toggle} disabled={busy || paused === null}>{paused ? t("intervene.resumeAgent") : t("intervene.pauseAgent")}</Btn>
      {paused && <span className="text-[11px] text-err">{t("intervene.agentPaused")}</span>}
      {err && <span role="alert" className="text-[11px] text-err">{t("intervene.error")}</span>}
    </div>
  );
}
