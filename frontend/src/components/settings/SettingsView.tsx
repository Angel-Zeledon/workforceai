"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { useConnections } from "@/lib/connections/store";
import { logout, useCan, useSession } from "@/lib/session";
import { useViewer } from "../connections/shared";
import { Icon, type IconName } from "../icons";
import { OfficeSettingsPanel } from "../OfficeSettings";
import { TemplateGallery } from "../templates/TemplateGallery";
import { useOnboardingEntry } from "../onboarding/OnboardingEntry";
import { HireFromTemplateDialog } from "../admin/HireFromTemplateDialog";
import { KillSwitchDialog } from "../security/KillSwitchDialog";
import { useBootConnections } from "../security/ControlsChrome";
import { MembersDialog } from "../auth/MembersDialog";
import { PageHeader } from "../nav/ui2";

type Dlg = null | "kill" | "templates" | "settings" | "members" | "hire";

function Row({ icon, title, body, onClick, testid, disabled, danger }: { icon: IconName; title: string; body: string; onClick: () => void; testid: string; disabled?: boolean; danger?: boolean }) {
  return (
    <button type="button" data-testid={testid} disabled={disabled} onClick={onClick}
      className={`ac-rise group flex w-full items-start gap-3.5 rounded-xl border border-line bg-panel p-4 text-left transition duration-150 hover:-translate-y-0.5 hover:border-line-strong hover:shadow-soft active:translate-y-0 disabled:opacity-50 disabled:hover:translate-y-0 ${danger ? "hover:border-err/40" : ""}`}>
      <span className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-lg ${danger ? "bg-err/10 text-err" : "bg-accent-soft text-accent"}`}><Icon name={icon} size={18} /></span>
      <span className="min-w-0 flex-1">
        <span className="block text-[13.5px] font-semibold text-ink">{title}</span>
        <span className="mt-0.5 block text-[12.5px] leading-snug text-mute">{body}</span>
      </span>
      <Icon name="chevron" size={16} className="mt-1 shrink-0 text-mute transition-transform group-hover:translate-x-0.5" />
    </button>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="mt-7 first:mt-0">
      <h2 className="mb-3 text-[11px] font-semibold uppercase tracking-[0.08em] text-mute">{title}</h2>
      <div className="grid gap-3 md:grid-cols-2">{children}</div>
    </section>
  );
}

/** Settings hub: everything that used to hide in the gear menu, explained and one click away. */
export function SettingsView() {
  const { t } = useT();
  useBootConnections();
  const [dlg, setDlg] = useState<Dlg>(null);
  const session = useSession();
  const signedIn = session.status === "authenticated";
  const canMembers = useCan("members:read");
  const { isAdmin } = useViewer();
  const setView = useConnections((s) => s.setView);
  const controls = useConnections((s) => s.controls);
  const onboarding = useOnboardingEntry();
  const level = controls?.kill_switch_level ?? "none";
  return (
    <div data-testid="settings-view" className="h-full overflow-y-auto bg-bg">
      <PageHeader title={t("settings.page.title")} description={t("settings.page.desc")} />
      <div className="mx-auto max-w-4xl px-5 py-6 sm:px-6">
        <Section title={t("admin.group.workspace")}>
          <Row testid="settings-office" icon="edit" title={t("settings.title")} body={t("settings.page.office")} onClick={() => setDlg("settings")} />
          <Row testid="settings-hire" icon="plus" title={t("hire.open")} body={t("settings.page.hire")} disabled={!isAdmin} onClick={() => setDlg("hire")} />
          <Row testid="settings-templates" icon="layout" title={t("tpl.open")} body={t("settings.page.templates")} onClick={() => setDlg("templates")} />
          <Row testid="settings-connections" icon="plug" title={t("nav.connections")} body={t("settings.page.connections")} onClick={() => setView("connections")} />
          {onboarding.supported && <Row testid="settings-onboarding" icon="flag" title={onboarding.pending ? t("onb.prompt.title") : t("cfg.open")} body={t("settings.page.onboarding")} onClick={onboarding.open} />}
        </Section>
        <Section title={t("admin.group.safety")}>
          <Row testid="settings-security" icon="shield" title={t("nav.security")} body={t("settings.page.security")} onClick={() => setView("security")} />
          <Row testid="settings-killswitch" icon="power" danger title={level === "none" ? t("ctl.killswitch.title") : t(`ctl.level.${level}`)} body={t("settings.page.kill")} disabled={level === "none" && !isAdmin}
            onClick={() => (level === "none" ? setDlg("kill") : setView("security"))} />
        </Section>
        {signedIn && (
          <Section title={t("admin.group.account")}>
            <div className="rounded-xl border border-line bg-panel p-4 text-[12.5px] text-mute">
              <b className="block truncate text-[13.5px] text-ink">{session.user?.name}</b>
              <span className="block truncate">{session.user?.email} · {t(`members.roles.${session.role}`)}</span>
            </div>
            {canMembers && <Row testid="settings-members" icon="users" title={t("members.title")} body={t("settings.page.members")} onClick={() => setDlg("members")} />}
            <Row testid="settings-logout" icon="power" title={t("auth.logout")} body={t("settings.page.logout")} onClick={() => { logout().then(() => { window.location.href = "/login"; }); }} />
          </Section>
        )}
      </div>
      {dlg === "kill" && <KillSwitchDialog onClose={() => setDlg(null)} />}
      {dlg === "templates" && <TemplateGallery onClose={() => setDlg(null)} />}
      {dlg === "hire" && <HireFromTemplateDialog onClose={() => setDlg(null)} />}
      {dlg === "settings" && <OfficeSettingsPanel onClose={() => setDlg(null)} />}
      {dlg === "members" && <MembersDialog onClose={() => setDlg(null)} />}
      {onboarding.dialogs}
    </div>
  );
}
