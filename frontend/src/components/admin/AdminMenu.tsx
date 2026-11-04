"use client";
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { api } from "@/lib/api";
import { MOCK } from "@/lib/config";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { go } from "@/lib/navBridge";
import { useConnections } from "@/lib/connections/store";
import { Icon, type IconName } from "../icons";
import { useViewer } from "../connections/shared";
import { useOnboardingEntry } from "../onboarding/OnboardingEntry";
import { useTeamSetupEntry } from "../onboarding/TeamSetupEntry";
import { useBootConnections } from "../security/ControlsChrome";
import { logout, orgLabel, switchOrg, useSession } from "@/lib/session";

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

const badge = (text: string, cls: string) => (
  <span className={`shrink-0 rounded-full px-1.5 py-0.5 text-[9.5px] font-semibold uppercase tracking-wide ${cls}`}>{text}</span>
);

/**
 * The avatar of the top bar (it replaced the gear): Mi cuenta, Detener todo and Salir, plus what only exists here (switch
 * organization, the set-up still pending, reset the demo). Everything else lives in Ajustes and in the menu; the test ids
 * stay (admin-menu, admin-menu-list, account-who, ctl-killswitch, logout…). Dialogs are owned here so they survive the
 * menu closing.
 */
export function AccountMenu() {
  const { t } = useT();
  useBootConnections();
  const [open, setOpen] = useState(false);
  const session = useSession();
  const signedIn = session.status === "authenticated";
  const wrap = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const controls = useConnections((s) => s.controls);
  const { isAdmin } = useViewer();
  const onboarding = useOnboardingEntry();
  const teamSetup = useTeamSetupEntry(); // E1: the responsible's team set-up, until it is done
  const level = controls?.kill_switch_level ?? "none";
  const readOnly = controls?.mode === "read_only";
  // attention dot: safety state wins over the pending-setup hint
  const dot = level !== "none" ? "bg-err" : readOnly ? "bg-warn" : (onboarding.supported && onboarding.pending) || teamSetup.pending ? "bg-accent" : "";
  const initials = (session.user?.name || session.user?.email || "").split(/[s@.]+/).filter(Boolean).slice(0, 2).map((w) => w[0]!.toUpperCase()).join("");

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
      <button ref={trigger} type="button" data-testid="admin-menu" aria-haspopup="menu" aria-expanded={open} aria-controls="admin-menu-list" title={t("ux.account.title")} aria-label={t("ux.account.title")}
        onClick={() => setOpen((o) => !o)} onKeyDown={(e) => { if (!open && (e.key === "ArrowDown" || e.key === "ArrowUp")) { e.preventDefault(); setOpen(true); } }}
        className={`relative flex h-8 w-8 items-center justify-center rounded-full border text-[11px] font-semibold transition ${open ? "border-accent bg-accent-soft text-accent" : "border-line bg-panel2 text-ink2 hover:border-line-strong hover:text-ink"}`}>
        {initials || <Icon name="user" size={16} />}
        {dot && <span data-testid="admin-menu-dot" className={`absolute -right-0.5 -top-0.5 h-2.5 w-2.5 rounded-full ring-2 ring-panel ${dot}`} />}
      </button>
      {open && (
        <div ref={menu} id="admin-menu-list" role="menu" aria-label={t("ux.account.title")} data-testid="admin-menu-list" onKeyDown={onKey}
          className="ac-pop absolute right-0 top-full z-[70] mt-2 w-[280px] max-w-[calc(100vw-24px)] divide-y divide-line rounded-xl border border-line bg-panel p-1.5 text-ink shadow-float">
          {signedIn && (
            <div className="py-1">
              <div data-testid="account-who" className="px-2.5 pb-1.5 pt-1 text-[11.5px] leading-tight text-mute">
                <b className="block truncate text-[12.5px] text-ink">{session.user?.name}</b>
                <span className="block truncate">{session.user?.email} · {t(`members.roles.${session.role}`)}</span>
              </div>
              <Item icon="user" testid="account-open" label={t("ux.account.mine")} onClick={run(() => go("/settings?s=account"))} />
              {session.orgs.length > 1 && session.orgs.filter((o) => o.org_id !== session.orgId).map((o) => (
                <Item key={o.org_id} icon="layout" testid={`org-switch-${o.org_id}`} label={t("admin.switchOrg", { org: orgLabel(o), role: t(`members.roles.${o.role}`) })}
                  onClick={run(() => { switchOrg(o.org_id).catch(() => undefined); })} />
              ))}
            </div>
          )}
          {((onboarding.supported && onboarding.pending) || teamSetup.pending || isAdmin) && (
            <div className="py-1">
              {onboarding.supported && onboarding.pending && (
                <Item icon="flag" testid="onboarding-open" label={t("onb.prompt.title")} trailing={badge(t("admin.pending"), "bg-accent-soft text-accent")} onClick={run(onboarding.open)} />
              )}
              {teamSetup.pending && (
                <Item icon="users" testid="team-setup-open" label={t("e1.setup.menu")} trailing={badge(t("admin.pending"), "bg-accent-soft text-accent")} onClick={run(teamSetup.open)} />
              )}
              {isAdmin && <Item icon="grid" testid="admin-console-open" label={t("adm.title")} onClick={run(() => go("/admin"))} />}
            </div>
          )}
          <div className="py-1">
            <Item icon="power" testid="ctl-killswitch" danger level={level} disabled={level === "none" && !isAdmin}
              label={level === "none" ? t("ctl.killswitch.title") : t(`ctl.level.${level}`)}
              trailing={level !== "none" ? badge(t("admin.active"), "bg-err text-white") : readOnly ? badge(t("ctl.state.readonly"), "bg-warn/15 text-warn") : undefined}
              onClick={run(() => go(level === "none" ? "/settings?s=kill" : "/settings?s=security"))} />
            {(!signedIn || session.role === "admin" || session.role === "owner") && (
              <Item icon="back" testid="demo-reset" label={t("hud.resetDemo")} onClick={run(reset)}
                trailing={MOCK ? badge("Mock", "bg-violet-500/15 text-violet-700") : undefined} />
            )}
            {signedIn && <Item icon="power" testid="logout" label={t("auth.logout")} onClick={run(() => { logout().then(() => { window.location.href = "/login"; }); })} />}
          </div>
        </div>
      )}
      {onboarding.dialogs}
      {teamSetup.dialogs}
    </div>
  );
}
