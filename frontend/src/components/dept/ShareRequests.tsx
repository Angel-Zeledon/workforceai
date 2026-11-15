"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { driveShares, type DriveShare } from "@/lib/drive";
import { shareErrorKey, splitRequests } from "@/lib/deptLogic";

/**
 * Above the department's list: the requests to share its files that the person can decide (Aprobar / Rechazar), and
 * the ones the person made that still wait for a lead ("Pendiente de aprobación"). Renders nothing when there are none.
 */
export function ShareRequests({ shares, meId, nameOfPerson, nameOfOffice, onChanged }: {
  shares: DriveShare[]; meId: string | undefined; nameOfPerson: (id: string) => string; nameOfOffice: (id: string) => string; onChanged: () => void;
}) {
  const { t } = useT();
  const { toDecide, mine } = splitRequests(shares, meId);
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  if (!toDecide.length && !mine.length) return null;

  const decide = async (s: DriveShare, d: "approve" | "reject") => {
    if (busy) return;
    setBusy(s.id); setErr(null);
    try { await driveShares.decide(s.id, d); } catch (e) { setErr(shareErrorKey(e)); }
    setBusy(null); onChanged();
  };
  const btn = "min-h-10 rounded-lg px-3 text-[12.5px] font-semibold disabled:opacity-50 sm:min-h-8";
  return (
    <section data-testid="dept-requests" className="mb-3 overflow-hidden rounded-[10px] border border-line bg-panel">
      {toDecide.length > 0 && (
        <div>
          <h2 className="border-b border-line bg-panel2/60 px-3 py-2 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("dept.requests.title")}</h2>
          <ul>
            {toDecide.map((s) => (
              <li key={s.id} data-testid={`dept-request-${s.name}`} className="flex flex-wrap items-center gap-2 border-t border-line px-3 py-2 first:border-t-0">
                <span className="min-w-0 flex-1 break-words text-[13px] text-ink [overflow-wrap:anywhere]">
                  {t("dept.requests.item", { who: nameOfPerson(s.requested_by), name: s.name, office: nameOfOffice(s.target_office_id) })}
                </span>
                <span className="flex shrink-0 gap-1.5">
                  <button type="button" data-testid="dept-request-approve" disabled={busy === s.id} onClick={() => void decide(s, "approve")}
                    className={`${btn} bg-accent text-white hover:bg-accent-hover`}>{t("dept.requests.approve")}</button>
                  <button type="button" data-testid="dept-request-reject" disabled={busy === s.id} onClick={() => void decide(s, "reject")}
                    className={`${btn} border border-line-strong bg-panel text-ink hover:bg-panel2`}>{t("dept.requests.reject")}</button>
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
      {mine.length > 0 && (
        <ul>
          {mine.map((s) => (
            <li key={s.id} data-testid={`dept-request-mine-${s.name}`} className="flex flex-wrap items-center gap-2 border-t border-line px-3 py-2 first:border-t-0">
              <span className="min-w-0 flex-1 break-words text-[13px] text-ink [overflow-wrap:anywhere]">{t("dept.requests.mineItem", { name: s.name, office: nameOfOffice(s.target_office_id) })}</span>
              <span className="inline-flex shrink-0 items-center whitespace-nowrap rounded-[6px] bg-warn/10 px-1.5 py-[1px] text-[10.5px] font-semibold text-warn">{t("dept.requests.mine")}</span>
            </li>
          ))}
        </ul>
      )}
      {err && <div role="alert" data-testid="dept-requests-err" className="border-t border-line bg-err/5 px-3 py-2 text-[12px] text-err">{t(err)}</div>}
    </section>
  );
}
