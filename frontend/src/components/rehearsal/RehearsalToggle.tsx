"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { rehearsalApi, useRehearsal } from "@/lib/rehearsal";

/**
 * B9: the agent's rehearsal switch ("Modo ensayo"). On: the agent keeps working, but nothing it would send, pay or
 * write is executed; each one lands in the rehearsal log for a person to judge. Turning it on needs controls:pause;
 * turning it off (real effects again) needs controls:release (owner), enforced by the API.
 */
export function RehearsalToggle({ id }: { id: string }) {
  const { t } = useT();
  const on = useRehearsal((s) => !!s.enabled[id]);
  const loaded = useRehearsal((s) => s.loaded);
  const canOn = useCan("controls:pause");
  const canOff = useCan("controls:release");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState(false);
  useEffect(() => { if (!useRehearsal.getState().loaded) void useRehearsal.getState().load(); }, []);
  const toggle = async () => {
    setBusy(true); setErr(false);
    try { const r = await rehearsalApi.setAgent(id, !on); useRehearsal.getState().setLocal(id, r.enabled); } catch { setErr(true); } finally { setBusy(false); }
  };
  return (
    <div className="flex flex-wrap items-center gap-2">
      <button type="button" data-testid="agent-rehearsal-toggle" data-on={on ? "true" : "false"} aria-pressed={on} onClick={toggle}
        disabled={busy || !loaded || (on ? !canOff : !canOn)} title={t("rehearsal.toggle.help")}
        className={`rounded-md border px-3 py-1 text-xs font-medium transition disabled:opacity-40 ${on ? "border-accent bg-accent-soft text-accent" : "border-line-strong bg-panel text-ink hover:bg-panel2"}`}>
        {on ? t("rehearsal.toggle.off") : t("rehearsal.toggle.on")}
      </button>
      {on && <span data-testid="agent-rehearsal-badge" className="rounded-full bg-accent-soft px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-accent">{t("rehearsal.badge")}</span>}
      {err && <span role="alert" className="text-[11px] text-err">{t("rehearsal.error")}</span>}
    </div>
  );
}
