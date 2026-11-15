"use client";
import { useEffect, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { driveShares, type DriveShare } from "@/lib/drive";
import { shareErrorKey, shareTargets } from "@/lib/deptLogic";
import type { OfficeView } from "@/lib/officesLogic";

/**
 * "Compartir con otro departamento…": the person picks the department that will read a file or folder (read only).
 * When a lead of the owner department asks, the share is born active; otherwise it waits for a lead to approve it.
 */
export function ShareDialog({ name, path, owner, offices, onClose, onShared }: {
  name: string; path: string; owner: OfficeView; offices: OfficeView[]; onClose: () => void; onShared: (s: DriveShare) => void;
}) {
  const { t } = useT();
  const targets = shareTargets(offices, owner.id);
  const [target, setTarget] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [done, setDone] = useState<DriveShare | null>(null);
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    box.current?.querySelector<HTMLElement>("select, button")?.focus({ preventScroll: true });
    const key = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [onClose]);

  const send = async () => {
    if (!target || busy) return;
    setBusy(true); setErr(null);
    try { const s = await driveShares.create(path, target); setDone(s); onShared(s); }
    catch (e) { setErr(shareErrorKey(e)); }
    setBusy(false);
  };
  const targetName = (id: string) => offices.find((o) => o.id === id)?.name ?? id;

  return (
    <div className="fixed inset-0 z-[60] flex items-end justify-center bg-black/25 sm:items-center sm:p-6" onMouseDown={onClose}>
      <div ref={box} role="dialog" aria-modal="true" aria-labelledby="dept-share-title" data-testid="dept-share-dialog" onMouseDown={(e) => e.stopPropagation()}
        className="w-full max-w-[440px] rounded-t-2xl border border-line bg-panel p-4 shadow-pop sm:rounded-[10px] sm:p-5">
        <h2 id="dept-share-title" className="break-words text-[15px] font-semibold text-ink">{t("dept.share.title", { name })}</h2>
        {done ? (
          <>
            <p data-testid="dept-share-done" className="mt-2 text-[13px] leading-relaxed text-ink2">
              {done.status === "active" ? t("dept.share.doneActive", { office: targetName(done.target_office_id), name }) : t("dept.share.donePending", { owner: owner.name })}
            </p>
            <div className="mt-4 flex justify-end">
              <button type="button" onClick={onClose} className="min-h-10 rounded-lg bg-accent px-4 text-[13px] font-semibold text-white hover:bg-accent-hover sm:min-h-9">{t("drive.close")}</button>
            </div>
          </>
        ) : (
          <form onSubmit={(e) => { e.preventDefault(); void send(); }}>
            <p className="mt-2 text-[12.5px] leading-relaxed text-mute">{t("dept.share.body", { owner: owner.name })}</p>
            {targets.length === 0 ? (
              <p data-testid="dept-share-none" className="mt-3 text-[13px] text-ink2">{t("dept.share.noTargets")}</p>
            ) : (
              <label className="mt-3 block text-[12px] font-medium text-ink2">{t("dept.share.target")}
                <select data-testid="dept-share-target" value={target} onChange={(e) => setTarget(e.target.value)}
                  className="mt-1 min-h-10 w-full rounded-lg border border-line bg-panel px-2 text-base text-ink outline-none focus:border-accent sm:min-h-9 sm:text-[13px]">
                  <option value="">{t("dept.share.choose")}</option>
                  {targets.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}
                </select>
              </label>
            )}
            {err && <div role="alert" data-testid="dept-share-err" className="mt-3 rounded-lg border border-err/30 bg-err/5 px-3 py-2 text-[12.5px] text-err">{t(err)}</div>}
            <div className="mt-4 flex justify-end gap-2">
              <button type="button" onClick={onClose} className="min-h-10 rounded-lg border border-line-strong bg-panel px-3 text-[13px] font-medium text-ink hover:bg-panel2 sm:min-h-9">{t("dept.share.cancel")}</button>
              <button type="submit" data-testid="dept-share-send" disabled={!target || busy}
                className="min-h-10 rounded-lg bg-accent px-4 text-[13px] font-semibold text-white hover:bg-accent-hover disabled:opacity-50 sm:min-h-9">{t("dept.share.send")}</button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
