"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { ctlApi } from "@/lib/connections/api";
import { useConnections } from "@/lib/connections/store";
import { Btn } from "../ui";
import { ErrorLine, errText, inputCls, Modal } from "../connections/shared";

/** Confirmation dialog of the global kill switch (freeze / lockdown). Shared by the admin menu and the Security screen. */
export function KillSwitchDialog({ onClose }: { onClose: () => void }) {
  const { t } = useT();
  const [level, setLevel] = useState<"freeze" | "lockdown">("freeze");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const go = async () => {
    setBusy(true); setErr(null);
    try { await ctlApi.killSwitch(level, reason.trim()); await useConnections.getState().loadControls(); await useConnections.getState().loadConnections(); onClose(); }
    catch (e) { setErr(errText(e)); setBusy(false); }
  };
  return (
    <Modal onClose={onClose} testId="ctl-killswitch-dialog">
      <h2 className="font-display text-lg font-semibold text-red-700">{t("ctl.killswitch.title")}</h2>
      <p className="mt-1 text-xs text-mute">{t("ctl.killswitch.body")}</p>
      <div className="mt-3 space-y-2">
        {(["freeze", "lockdown"] as const).map((l) => (
          <label key={l} className={`flex cursor-pointer items-start gap-2 rounded-xl border px-3 py-2 text-xs ${level === l ? "border-red-400 bg-red-50" : "border-line bg-panel2"}`}>
            <input type="radio" name="ks-level" data-testid={`ctl-ks-level-${l}`} checked={level === l} onChange={() => setLevel(l)} className="mt-0.5" />
            <span><b className="block text-[13px]">{t(`ctl.level.${l}`)}</b><span className="text-mute">{t(`ctl.level.${l}.desc`)}</span></span>
          </label>
        ))}
        <input data-testid="ctl-killswitch-reason" className={inputCls} placeholder={t("ctl.killswitch.reason")} value={reason} onChange={(e) => setReason(e.target.value)} />
        <p className="text-[11px] text-mute">{t("ctl.killswitch.ownerLifts")}</p>
        <ErrorLine msg={err} />
      </div>
      <div className="mt-4 flex justify-end gap-2">
        <Btn onClick={onClose}>{t("common.close")}</Btn>
        <Btn data-testid="ctl-killswitch-confirm" kind="danger" disabled={busy} onClick={go}>{t("ctl.killswitch.confirm")}</Btn>
      </div>
    </Modal>
  );
}
