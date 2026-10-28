"use client";
import { useRouter, useSearchParams } from "next/navigation";
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
import { PageHeader, PageSkeleton } from "../nav/ui2";
import { Crumbs } from "../nav/Crumbs";
import { PageModeCtx } from "../nav/pageMode";
import { ConnectionsView } from "../connections/ConnectionsView";
import { SecurityCenter } from "../security/SecurityCenter";
import { AccountSecurity } from "./AccountSecurity";
import { DataPrivacy } from "./DataPrivacy";
import { IdentitySettings } from "./IdentitySettings";
import { PortalBranding } from "./PortalBranding";

/** Sub-pages of Settings. The URL (/settings?s=...) says which one is open, so the browser's Back button and the
 *  sidebar both just work: nothing needs a "Close" button. */
const SUBS = ["office", "hire", "templates", "members", "kill", "connections", "security", "account", "data", "identity", "portal"] as const;
type Sub = (typeof SUBS)[number];
const SUB_TITLE: Record<Sub, string> = {
  office: "settings.title", hire: "hire.open", templates: "tpl.open", members: "members.title",
  kill: "ctl.killswitch.title", connections: "nav.connections", security: "nav.security", account: "mfa.rowTitle", data: "data.title", identity: "idn.title", portal: "portal.brand.title",
};

function Row({ icon, title, body, onClick, testid, disabled, danger, tour }: { tour?: string; icon: IconName; title: string; body: string; onClick: () => void; testid: string; disabled?: boolean; danger?: boolean }) {
  return (
    <button type="button" data-tour={tour} data-testid={testid} disabled={disabled} onClick={onClick}
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
  const router = useRouter();
  const raw = useSearchParams().get("s");
  const sub = (SUBS as readonly string[]).includes(raw ?? "") ? (raw as Sub) : null;
  const open = (s: Sub | null) => router.push(s ? `/settings?s=${s}` : "/settings");
  const session = useSession();
  const signedIn = session.status === "authenticated";
  const canMembers = useCan("members:read");
  const { isAdmin } = useViewer();
  const controls = useConnections((s) => s.controls);
  const loaded = useConnections((s) => s.loaded);
  const onboarding = useOnboardingEntry();
  const level = controls?.kill_switch_level ?? "none";
  if (sub) {
    const full = sub === "connections" || sub === "security"; // these two manage their own scrolling
    return (
      <div data-testid="settings-view" className="flex h-full flex-col bg-bg">
        <Crumbs root={t("settings.page.title")} title={t(SUB_TITLE[sub])} onBack={() => open(null)} backTestId={full ? "conn-close" : undefined} />
        <div data-testid={sub === "connections" ? "conn-screen" : sub === "security" ? "ctl-screen" : undefined} className={`min-h-0 flex-1 ${full ? "overflow-hidden" : "overflow-y-auto"}`}>
          <PageModeCtx.Provider value={{ back: () => open(null) }}>
            {sub === "connections" && (loaded ? <ConnectionsView /> : <PageSkeleton />)}
            {sub === "security" && (loaded ? <SecurityCenter /> : <PageSkeleton />)}
            {sub === "account" && <AccountSecurity />}
            {sub === "data" && <DataPrivacy />}
            {sub === "identity" && <IdentitySettings />}
            {sub === "portal" && <PortalBranding />}
            {sub === "office" && <OfficeSettingsPanel onClose={() => open(null)} />}
            {sub === "hire" && <HireFromTemplateDialog onClose={() => open(null)} />}
            {sub === "templates" && <TemplateGallery onClose={() => open(null)} />}
            {sub === "members" && <MembersDialog onClose={() => open(null)} />}
            {sub === "kill" && <KillSwitchDialog onClose={() => open(null)} />}
          </PageModeCtx.Provider>
        </div>
        {onboarding.dialogs}
      </div>
    );
  }
  return (
    <div data-testid="settings-view" className="h-full overflow-y-auto bg-bg">
      <PageHeader title={t("settings.page.title")} description={t("settings.page.desc")} />
      <div className="mx-auto max-w-4xl px-5 py-6 sm:px-6">
        <Section title={t("admin.group.workspace")}>
          <Row tour="settings.office" testid="settings-office" icon="edit" title={t("settings.title")} body={t("settings.page.office")} onClick={() => open("office")} />
          <Row tour="settings.hire" testid="settings-hire" icon="plus" title={t("hire.open")} body={t("settings.page.hire")} disabled={!isAdmin} onClick={() => open("hire")} />
          <Row tour="settings.templates" testid="settings-templates" icon="layout" title={t("tpl.open")} body={t("settings.page.templates")} onClick={() => open("templates")} />
          <Row testid="settings-portal" icon="layout" title={t("portal.brand.title")} body={t("portal.brand.rowBody")} onClick={() => open("portal")} />
          <Row tour="settings.connections" testid="settings-connections" icon="plug" title={t("nav.connections")} body={t("settings.page.connections")} onClick={() => open("connections")} />
          {onboarding.supported && <Row tour="settings.onboarding" testid="settings-onboarding" icon="flag" title={onboarding.pending ? t("onb.prompt.title") : t("cfg.open")} body={t("settings.page.onboarding")} onClick={onboarding.open} />}
        </Section>
        <Section title={t("admin.group.safety")}>
          <Row tour="settings.security" testid="settings-security" icon="shield" title={t("nav.security")} body={t("settings.page.security")} onClick={() => open("security")} />
          <Row testid="settings-identity" icon="users" title={t("idn.title")} body={t("idn.rowBody")} disabled={!isAdmin} onClick={() => open("identity")} />
          <Row tour="settings.killswitch" testid="settings-killswitch" icon="power" danger title={level === "none" ? t("ctl.killswitch.title") : t(`ctl.level.${level}`)} body={t("settings.page.kill")} disabled={level === "none" && !isAdmin}
            onClick={() => open(level === "none" ? "kill" : "security")} />
        </Section>
        {signedIn && (
          <Section title={t("admin.group.account")}>
            <div data-tour="settings.account" className="rounded-xl border border-line bg-panel p-4 text-[12.5px] text-mute">
              <b className="block truncate text-[13.5px] text-ink">{session.user?.name}</b>
              <span className="block truncate">{session.user?.email} · {t(`members.roles.${session.role}`)}</span>
            </div>
            <Row testid="settings-account" icon="shield" title={t("mfa.rowTitle")} body={t("mfa.rowBody")} onClick={() => open("account")} />
            <Row testid="settings-data" icon="layout" title={t("data.title")} body={t("settings.page.data")} onClick={() => open("data")} />
            {canMembers && <Row testid="settings-members" icon="users" title={t("members.title")} body={t("settings.page.members")} onClick={() => open("members")} />}
            <Row testid="settings-logout" icon="power" title={t("auth.logout")} body={t("settings.page.logout")} onClick={() => { logout().then(() => { window.location.href = "/login"; }); }} />
          </Section>
        )}
      </div>
      {onboarding.dialogs}
    </div>
  );
}
