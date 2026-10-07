"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { ctlApi } from "@/lib/connections/api";
import { useConnections } from "@/lib/connections/store";
import { Btn } from "../ui";
import { Icon } from "../icons";
import { navBtn } from "../navStyles";
import { ErrorLine, errText, inputCls, Modal, useViewer } from "../connections/shared";
import { ConnectionsView } from "../connections/ConnectionsView";
import { SecurityCenter } from "./SecurityCenter";

/** Loads connections/controls state once (also refreshed by WS frames routed from store.ts). */
function useBootConnections() {
  const loaded = useConnections((s) => s.loaded);
  useEffect(() => { if (!loaded) void useConnections.getState().load(); }, [loaded]);
}

function KillSwitchDialog({ onClose }: { onClose: () => void }) {
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

/** Header controls: Connections + Security navigation and the permanent red kill-switch button. */
export function ControlsBar() {
  const { t } = useT();
  useBootConnections();
  const view = useConnections((s) => s.view);
  const setView = useConnections((s) => s.setView);
  const controls = useConnections((s) => s.controls);
  const { isAdmin } = useViewer();
  const [dialog, setDialog] = useState(false);
  const level = controls?.kill_switch_level ?? "none";
  const pill = navBtn;
  return (
    <>
      <button type="button" data-testid="nav-connections" data-active={view === "connections"} onClick={() => setView(view === "connections" ? null : "connections")} className={pill(view === "connections")}><Icon name="plug" size={14} />{t("nav.connections")}</button>
      <button type="button" data-testid="nav-security" data-active={view === "security"} onClick={() => setView(view === "security" ? null : "security")} className={pill(view === "security")}><Icon name="shield" size={14} />{t("nav.security")}</button>
      <button type="button" data-testid="ctl-killswitch" data-level={level} disabled={level === "none" && !isAdmin}
        onClick={() => (level === "none" ? setDialog(true) : setView("security"))}
        className={`inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs font-medium transition disabled:opacity-40 ${level === "none" ? "border-err/40 text-err hover:bg-err/10" : "border-err bg-err text-white hover:bg-err/90"}`}>
        <Icon name="power" size={14} />
        {level === "none" ? t("ctl.killswitch.title") : t(`ctl.level.${level}`)}
      </button>
      {dialog && <KillSwitchDialog onClose={() => setDialog(false)} />}
    </>
  );
}

/** Persistent banner under the header while the kill switch or read-only mode is on. */
export function ControlsBanner() {
  const { t } = useT();
  const controls = useConnections((s) => s.controls);
  const setView = useConnections((s) => s.setView);
  if (!controls || !controls.kill_switch_level) return null;
  if (controls.kill_switch_level === "none" && controls.mode !== "read_only") return null;
  const level = controls.kill_switch_level;
  const text = level !== "none" ? t(`ctl.banner.${level}`) : t("ctl.readonly.banner");
  return (
    <div data-testid="ctl-banner" data-level={level !== "none" ? level : "read_only"} role="status"
      className={`flex shrink-0 items-center justify-center gap-3 px-4 py-1.5 text-xs font-semibold text-white ${level !== "none" ? "bg-err" : "bg-warn"}`}>
      <span>{text}</span>
      <button type="button" onClick={() => setView("security")} className="rounded-md border border-white/70 px-3 py-0.5 text-[11px] hover:bg-white/15">{t("ctl.banner.open")}</button>
    </div>
  );
}

/** Full-area overlay for the Connections and Security screens (rendered inside <main>). */
export function ControlsOverlay() {
  const { t } = useT();
  const view = useConnections((s) => s.view);
  const setView = useConnections((s) => s.setView);
  if (!view) return null;
  return (
    <div data-testid={view === "connections" ? "conn-screen" : "ctl-screen"} className="absolute inset-0 z-50 overflow-hidden bg-bg">
      <button type="button" data-testid="conn-close" onClick={() => setView(null)} className="absolute right-4 top-2 z-10 inline-flex items-center gap-1 rounded-md border border-line bg-panel px-2.5 py-1 text-xs font-medium text-ink2 hover:bg-panel2 hover:text-ink">{t("common.close")}</button>
      <div className="h-full pt-9">{view === "connections" ? <ConnectionsView /> : <SecurityCenter />}</div>
    </div>
  );
}
