"use client";
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { api } from "@/lib/api";
import { MOCK } from "@/lib/config";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { useConnections } from "@/lib/connections/store";
import { Icon, type IconName } from "../icons";
import { useViewer } from "../connections/shared";
import { OfficeSettingsPanel } from "../OfficeSettings";
import { TemplateGallery } from "../templates/TemplateGallery";
import { useOnboardingEntry } from "../onboarding/OnboardingEntry";
import { KillSwitchDialog } from "../security/KillSwitchDialog";
import { useBootConnections } from "../security/ControlsChrome";

function Item({ icon, label, onClick, testid, disabled, danger, trailing, active, level }: {
  icon: IconName; label: string; onClick: () => void; testid: string; disabled?: boolean; danger?: boolean; trailing?: ReactNode; active?: boolean; level?: string;
}) {
  return (
    <button type="button" role="menuitem" tabIndex={-1} data-testid={testid} data-active={active} data-level={level} disabled={disabled} onClick={onClick}
      className={`flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-left text-[12.5px] font-medium transition disabled:opacity-40 ${danger ? "text-err hover:bg-err/10 focus-visible:bg-err/10" : "text-ink hover:bg-panel2 focus-visible:bg-panel2"} ${active ? "bg-accent-soft text-accent" : ""}`}>
      <Icon name={icon} size={15} className={danger ? "" : "text-mute"} />
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {trailing}
    </button>
  );
}

function Group({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div role="group" aria-label={title} className="py-1">
      <div className="px-2.5 pb-1 pt-1.5 text-[10px] font-semibold uppercase tracking-[0.08em] text-mute">{title}</div>
      {children}
    </div>
  );
}

const badge = (text: string, cls: string) => (
  <span className={`shrink-0 rounded-full px-1.5 py-0.5 text-[9.5px] font-semibold uppercase tracking-wide ${cls}`}>{text}</span>
);

/**
 * The single, discreet administration entry of the header (gear). It keeps the
 * main screen clean: Connections, Security (+ kill switch / read-only), Templates,
 * office setup, office settings and demo reset live here. Dialogs are owned by this
 * component so they survive the menu closing.
 */
export function AdminMenu() {
  const { t } = useT();
  useBootConnections();
  const [open, setOpen] = useState(false);
  const [dialog, setDialog] = useState<null | "kill" | "templates" | "settings">(null);
  const wrap = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const view = useConnections((s) => s.view);
  const setView = useConnections((s) => s.setView);
  const controls = useConnections((s) => s.controls);
  const { isAdmin } = useViewer();
  const onboarding = useOnboardingEntry();
  const level = controls?.kill_switch_level ?? "none";
  const readOnly = controls?.mode === "read_only";
  // attention dot: safety state wins over the pending-setup hint
  const dot = level !== "none" ? "bg-err" : readOnly ? "bg-warn" : onboarding.supported && onboarding.pending ? "bg-accent" : "";

  const items = useCallback(() => Array.from(menu.current?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not([disabled])') ?? []), []);
  const close = useCallback((refocus = true) => { setOpen(false); if (refocus) trigger.current?.focus(); }, []);

  useEffect(() => {
    if (!open) return;
    items()[0]?.focus();
    const onDown = (e: MouseEvent) => { if (!wrap.current?.contains(e.target as Node)) setOpen(false); };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open, items]);

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") { e.stopPropagation(); close(); return; }
    const list = items();
    if (!list.length) return;
    const i = list.indexOf(document.activeElement as HTMLButtonElement);
    let next = -1;
    if (e.key === "ArrowDown") next = (i + 1) % list.length;
    else if (e.key === "ArrowUp") next = (i - 1 + list.length) % list.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = list.length - 1;
    else if (e.key === "Tab") { close(false); return; }
    if (next >= 0) { e.preventDefault(); list[next].focus(); }
  };

  const run = (fn: () => void) => () => { setOpen(false); fn(); };
  const reset = async () => {
    try {
      await api.reset();
      useStore.setState({ tasks: {}, requests: {}, plans: {}, conversations: {}, messages: {}, chat: {}, routes: {}, lastTurnId: null, typing: {}, approvals: {}, reports: {}, activity: [], errors: [], links: [], selectedAgentId: null });
      await useStore.getState().loadAll();
    } catch { /* backend without reset */ }
  };

  return (
    <div ref={wrap} className="relative">
      <button ref={trigger} type="button" data-testid="admin-menu" aria-haspopup="menu" aria-expanded={open} aria-controls="admin-menu-list" title={t("admin.title")} aria-label={t("admin.title")}
        onClick={() => setOpen((o) => !o)} onKeyDown={(e) => { if (!open && (e.key === "ArrowDown" || e.key === "ArrowUp")) { e.preventDefault(); setOpen(true); } }}
        className={`relative flex h-8 w-8 items-center justify-center rounded-md border transition ${open ? "border-accent/30 bg-accent-soft text-accent" : "border-transparent text-ink2 hover:bg-panel2 hover:text-ink"}`}>
        <Icon name="settings" size={17} />
        {dot && <span data-testid="admin-menu-dot" className={`absolute right-1 top-1 h-2 w-2 rounded-full ring-2 ring-panel ${dot}`} />}
      </button>
      {open && (
        <div ref={menu} id="admin-menu-list" role="menu" aria-label={t("admin.title")} data-testid="admin-menu-list" onKeyDown={onKey}
          className="ac-pop absolute right-0 top-full z-[70] mt-2 w-[290px] max-w-[calc(100vw-24px)] divide-y divide-line rounded-xl border border-line bg-panel p-1.5 text-ink shadow-float">
          <Group title={t("admin.group.workspace")}>
            <Item icon="plug" testid="nav-connections" active={view === "connections"} label={t("nav.connections")} onClick={run(() => setView(view === "connections" ? null : "connections"))} />
            <Item icon="layout" testid="templates-open" label={t("tpl.open")} onClick={run(() => setDialog("templates"))} />
            {onboarding.supported && (
              <Item icon="flag" testid={onboarding.pending ? "onboarding-open" : "org-config-open"} label={onboarding.pending ? t("onb.prompt.title") : t("cfg.open")}
                trailing={onboarding.pending ? badge(t("admin.pending"), "bg-accent-soft text-accent") : undefined} onClick={run(onboarding.open)} />
            )}
            <Item icon="edit" testid="office-settings" label={t("settings.title")} onClick={run(() => setDialog("settings"))} />
          </Group>
          <Group title={t("admin.group.safety")}>
            <Item icon="shield" testid="nav-security" active={view === "security"} label={t("nav.security")}
              trailing={readOnly ? badge(t("ctl.state.readonly"), "bg-warn/15 text-warn") : undefined} onClick={run(() => setView(view === "security" ? null : "security"))} />
            <Item icon="power" testid="ctl-killswitch" danger level={level} disabled={level === "none" && !isAdmin}
              label={level === "none" ? t("ctl.killswitch.title") : t(`ctl.level.${level}`)}
              trailing={level !== "none" ? badge(t("admin.active"), "bg-err text-white") : undefined}
              onClick={run(() => (level === "none" ? setDialog("kill") : setView("security")))} />
          </Group>
          <Group title={t("admin.group.demo")}>
            <Item icon="back" testid="demo-reset" label={t("hud.resetDemo")} onClick={run(reset)}
              trailing={MOCK ? badge("Mock", "bg-violet-500/15 text-violet-700") : undefined} />
          </Group>
        </div>
      )}
      {dialog === "kill" && <KillSwitchDialog onClose={() => setDialog(null)} />}
      {dialog === "templates" && <TemplateGallery onClose={() => setDialog(null)} />}
      {dialog === "settings" && <OfficeSettingsPanel onClose={() => setDialog(null)} />}
      {onboarding.dialogs}
    </div>
  );
}
