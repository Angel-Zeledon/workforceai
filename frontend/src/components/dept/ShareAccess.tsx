"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { driveShares, type DriveShare } from "@/lib/drive";
import { shareErrorKey } from "@/lib/deptLogic";

/** "Quién puede verlo" (panel of a department file): this department and the ones it shares the file with. `tick` reloads it. */
export function ShareAccess({ path, ownerName, nameOfOffice, tick }: { path: string; ownerName: string; nameOfOffice: (id: string) => string; tick: number }) {
  const { t } = useT();
  const [shares, setShares] = useState<DriveShare[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const load = useCallback(() => { driveShares.forPath(path).then(setShares).catch(() => setShares([])); }, [path]);
  useEffect(() => { setShares(null); setErr(null); load(); }, [load, tick]);

  const stop = async (s: DriveShare) => {
    if (busy) return;
    setBusy(s.id); setErr(null);
    try { await driveShares.revoke(s.id); } catch (e) { setErr(shareErrorKey(e)); }
    setBusy(null); load();
  };
  return (
    <section data-testid="dept-access" aria-label={t("dept.access.title")} className="shrink-0 border-b border-line px-4 py-2.5">
      <h3 className="text-[11px] font-semibold uppercase tracking-wide text-mute">{t("dept.access.title")}</h3>
      <ul className="mt-1.5 space-y-1">
        <li className="text-[12.5px] text-ink">{t("dept.access.own", { office: ownerName })}</li>
        {(shares ?? []).map((s) => (
          <li key={s.id} data-testid={`dept-access-${s.target_office_id}`} className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[12.5px] text-ink">
            <span className="min-w-0 break-words [overflow-wrap:anywhere]">{nameOfOffice(s.target_office_id)}</span>
            {s.status === "pending" && <span className="inline-flex items-center whitespace-nowrap rounded-[6px] bg-warn/10 px-1.5 py-[1px] text-[10.5px] font-semibold text-warn">{t("dept.access.pending")}</span>}
            {s.can_revoke && (
              <button type="button" data-testid="dept-access-stop" disabled={busy === s.id} onClick={() => void stop(s)}
                className="ml-auto min-h-9 rounded-lg px-2 text-[12px] font-medium text-accent hover:bg-accent-soft disabled:opacity-50">
                {s.status === "pending" ? t("dept.access.cancel") : t("dept.access.stop")}
              </button>
            )}
          </li>
        ))}
      </ul>
      {err && <div role="alert" className="mt-1.5 text-[12px] text-err">{t(err)}</div>}
    </section>
  );
}
