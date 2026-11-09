"use client";
import { useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { tr, useT } from "@/lib/i18n";
import { useOfficeLayout } from "@/lib/officeLayout";
import { useConnections } from "@/lib/connections/store";
import { logout, useCan, useSession } from "@/lib/session";
import { useViewer } from "../connections/shared";
import { Icon, type IconName } from "../icons";
import { OfficeSettingsPanel } from "../OfficeSettings";
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
import { PlanBilling } from "./PlanBilling";
import { notifyApi } from "@/lib/notifyApi";
import { IdentitySettings } from "./IdentitySettings";
import { PortalBranding } from "./PortalBranding";
import { MyCosts } from "./MyCosts";
import { ApprovalSettings } from "./ApprovalSettings";
import { useViewMode } from "@/lib/viewMode";

/** Sub-pages of Settings. The URL (/settings?s=...) says which one is open, so the browser's Back button and the
 *  sidebar both just work: nothing needs a "Close" button. */
const SUBS = ["office", "hire", "templates", "members", "kill", "connections", "security", "account", "data", "identity", "portal", "plan", "costs", "approvals"] as const;
type Sub = (typeof SUBS)[number];
const SUB_TITLE: Record<Sub, string> = {
  office: "settings.title", hire: "hire.open", templates: "tpl.open", members: "members.title",
  kill: "ctl.killswitch.title", connections: "nav.connections", security: "nav.security", account: "mfa.rowTitle", data: "data.title", identity: "idn.title", portal: "portal.brand.title", plan: "planpage.title", costs: "mycosts.title", approvals: "perm.settings.title",
};

/** where the templates live now: the single library of Proyectos (quick requests + projects) */
const TEMPLATES_HOME = "/projects?view=library";

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

/** Members, sign-in rules and the second factor need accounts: the open demo has none, so a direct link explains it instead of failing. */
function NeedsSession() {
  const { t } = useT();
  return (
    <div data-testid="settings-needs-session" className="mx-auto max-w-lg p-6 text-center">
      <p className="text-[13px] font-semibold text-ink">{t("settings.needsSession.title")}</p>
      <p className="mt-1 text-[12.5px] leading-relaxed text-mute">{t("settings.needsSession.body")}</p>
    </div>
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
  const noSession = session.status === "disabled"; // the open demo: no accounts, so no members nor sign-in rules
  const canMembers = useCan("members:read");
  const canPlan = useCan("metrics:read");
  // The Plan row only where the deployment has plans (every plan of the catalog has a hard AI cap).
  const [hasPlan, setHasPlan] = useState(false);
  useEffect(() => {
    if (!canPlan) return;
    let live = true;
    notifyApi.planUsage().then((u) => { if (live) setHasPlan(u.hard); }).catch(() => { /* no plans here */ });
    return () => { live = false; };
  }, [canPlan]);
  const { isAdmin } = useViewer();
  const controls = useConnections((s) => s.controls);
  const loaded = useConnections((s) => s.loaded);
  const onboarding = useOnboardingEntry();
  const [q, setQ] = useState("");
  const level = controls?.kill_switch_level ?? "none";
  // Conexiones moved to the main menu (/connections); the old link keeps working.
  useEffect(() => { if (sub === "connections") router.replace("/connections"); }, [sub, router]);
  // Plantillas de trabajo joined the single template library of Proyectos; the old link keeps working.
  useEffect(() => { if (sub === "templates") router.replace(TEMPLATES_HOME); }, [sub, router]);
  if (sub) {
    const full = sub === "connections" || sub === "security"; // these two manage their own scrolling
    return (
      <div data-testid="settings-view" className="flex h-full flex-col bg-bg">
        <Crumbs root={t("settings.page.title")} title={t(SUB_TITLE[sub])} onBack={() => open(null)} backTestId={full ? "conn-close" : undefined} />
        <div data-testid={sub === "connections" ? "conn-screen" : sub === "security" ? "ctl-screen" : undefined} className={`min-h-0 flex-1 ${full ? "overflow-hidden" : "overflow-y-auto"}`}>
          <PageModeCtx.Provider value={{ back: () => open(null) }}>
            {sub === "connections" && (loaded ? <ConnectionsView /> : <PageSkeleton />)}
            {sub === "security" && (loaded ? <SecurityCenter /> : <PageSkeleton />)}
            {sub === "account" && (noSession ? <NeedsSession /> : <AccountSecurity />)}
            {sub === "data" && <DataPrivacy />}
            {sub === "costs" && <MyCosts />}
            {sub === "approvals" && <ApprovalSettings />}
            {sub === "plan" && <PlanBilling />}
            {sub === "identity" && (noSession ? <NeedsSession /> : <IdentitySettings />)}
            {sub === "portal" && <PortalBranding />}
            {sub === "office" && <OfficeSettingsPanel onClose={() => open(null)} />}
            {sub === "hire" && <HireFromTemplateDialog onClose={() => open(null)} />}
            {sub === "members" && (noSession ? <NeedsSession /> : <MembersDialog onClose={() => open(null)} />)}
            {sub === "kill" && <KillSwitchDialog onClose={() => open(null)} />}
          </PageModeCtx.Provider>
        </div>
        {onboarding.dialogs}
      </div>
    );
  }
  type Group = "workspace" | "safety" | "account";
  interface Entry { id: string; group: Group; icon: IconName; title: string; body: string; run: () => void; testid?: string; tour?: string; disabled?: boolean; danger?: boolean; hidden?: boolean; searchOnly?: boolean }
  const goOffice = (edit: boolean) => { router.push("/"); if (edit) setTimeout(() => { void useOfficeLayout.getState().startEdit(); }, 350); };
  // Three groups, ~11 rows (proposal 5.2 #7). What left the list (hire, templates, connections, sign out) still opens
  // from the search box and from its new home (Organización, Proyectos, the menu, the avatar).
  const all: Entry[] = [
    { id: "onboarding", group: "workspace", tour: "settings.onboarding", testid: "settings-onboarding", icon: "flag", title: onboarding.pending ? t("onb.prompt.title") : t("cfg.open"), body: t("settings.page.onboarding"), hidden: !onboarding.supported, run: onboarding.open },
    { id: "office", group: "workspace", tour: "settings.office", testid: "settings-office", icon: "edit", title: t("settings.title"), body: t("settings.page.office"), run: () => open("office") },
    { id: "members", group: "workspace", testid: "settings-members", icon: "users", title: t("members.title"), body: t("settings.page.members"), hidden: !signedIn || !canMembers, run: () => open("members") },
    { id: "plan", group: "workspace", testid: "settings-plan", icon: "bag", title: t("planpage.title"), body: t("settings.page.plan"), hidden: !hasPlan, run: () => open("plan") },
    { id: "portal", group: "workspace", testid: "settings-portal", icon: "layout", title: t("portal.brand.title"), body: t("portal.brand.rowBody"), run: () => open("portal") },
    { id: "approvals", group: "safety", tour: "settings.approvals", testid: "settings-approvals", icon: "shield", title: t("perm.settings.title"), body: t("perm.settings.rowBody"), run: () => open("approvals") },
    { id: "security", group: "safety", tour: "settings.security", testid: "settings-security", icon: "shield", title: t("nav.security"), body: t("settings.page.security"), run: () => open("security") },
    { id: "identity", group: "safety", testid: "settings-identity", icon: "users", title: t("idn.title"), body: t("idn.rowBody"), disabled: !isAdmin, hidden: noSession, run: () => open("identity") },
    { id: "kill", group: "safety", tour: "settings.killswitch", testid: "settings-killswitch", icon: "power", danger: true, title: level === "none" ? t("ctl.killswitch.title") : t(`ctl.level.${level}`), body: t("settings.page.kill"), disabled: level === "none" && !isAdmin, run: () => open(level === "none" ? "kill" : "security") },
    { id: "account", group: "account", testid: "settings-account", icon: "shield", title: t("mfa.rowTitle"), body: t("mfa.rowBody"), hidden: !signedIn, run: () => open("account") },
    { id: "costs", group: "account", testid: "settings-costs", icon: "bag", title: t("mycosts.title"), body: t("mycosts.rowBody"), hidden: !signedIn || !canPlan, run: () => open("costs") },
    { id: "data", group: "account", testid: "settings-data", icon: "layout", title: t("data.title"), body: t("settings.page.data"), hidden: !signedIn, run: () => open("data") },
    // only found by searching: things that moved to another home or live inside another screen
    { id: "hire", group: "workspace", searchOnly: true, testid: "settings-hire", icon: "plus", title: t("hire.open"), body: t("settings.page.hire"), disabled: !isAdmin, run: () => open("hire") },
    { id: "templates", group: "workspace", searchOnly: true, testid: "settings-templates", icon: "layout", title: t("tpl.open"), body: t("settings.page.templates"), run: () => router.push(TEMPLATES_HOME) },
    { id: "connections", group: "workspace", searchOnly: true, testid: "settings-connections", icon: "plug", title: t("nav.connections"), body: t("settings.page.connections"), run: () => router.push("/connections") },
    { id: "logout", group: "account", searchOnly: true, testid: "settings-logout", icon: "power", title: t("auth.logout"), body: t("settings.page.logout"), hidden: !signedIn, run: () => { logout().then(() => { window.location.href = "/login"; }); } },
    { id: "viewAll", group: "workspace", searchOnly: true, icon: "grid", title: t("ux.viewAll.label"), body: t("ux.viewAll.find"), run: () => useViewMode.getState().setViewAll(!useViewMode.getState().viewAll) },
    { id: "llm", group: "workspace", searchOnly: true, icon: "settings", title: t("llm.title"), body: t("settings.find.llm"), hidden: !onboarding.supported, run: onboarding.open },
    { id: "editOffice", group: "workspace", searchOnly: true, icon: "edit", title: t("settings.find.editOffice.t"), body: t("settings.find.editOffice"), run: () => goOffice(true) },
    { id: "language", group: "workspace", searchOnly: true, icon: "settings", title: t("settings.language"), body: t("settings.find.language"), run: () => open("office") },
    { id: "skills", group: "workspace", searchOnly: true, icon: "layers", title: t("nav.skills"), body: t("settings.find.skills"), run: () => router.push("/skills") },
    { id: "projects", group: "workspace", searchOnly: true, icon: "list", title: t("tpl.lib.title"), body: t("settings.find.projects"), run: () => router.push("/projects") },
  ];
  const entries = all.filter((e) => !e.hidden);
  const hits = q.trim() ? searchSettings(entries, q) : null;
  const rowOf = (e: Entry) => <Row key={e.id} tour={hits ? undefined : e.tour} testid={e.testid ?? `settings-find-${e.id}`} icon={e.icon} title={e.title} body={e.body} disabled={e.disabled} danger={e.danger} onClick={e.run} />;
  const group = (g: Group) => entries.filter((e) => e.group === g && !e.searchOnly).map(rowOf);
  return (
    <div data-testid="settings-view" className="h-full overflow-y-auto bg-bg">
      <PageHeader title={t("settings.page.title")} description={t("settings.page.desc")} />
      <div className="mx-auto max-w-4xl px-4 py-4 sm:px-6 sm:py-6">
        <div className="relative mb-6">
          <span className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-mute" aria-hidden>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></svg>
          </span>
          <input type="search" data-testid="settings-search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("settings.find.placeholder")} aria-label={t("settings.find.placeholder")}
            onKeyDown={(e) => { if (e.key === "Enter" && hits && hits[0] && !hits[0].disabled) hits[0].run(); if (e.key === "Escape") setQ(""); }}
            className="w-full rounded-xl border border-line bg-panel py-2.5 pl-10 pr-3 text-[13.5px] text-ink outline-none transition placeholder:text-mute focus:border-accent focus:shadow-soft" />
        </div>
        {hits ? (
          <section aria-live="polite">
            <h2 className="mb-3 text-[11px] font-semibold uppercase tracking-[0.08em] text-mute">{t("settings.find.results", { count: hits.length })}</h2>
            {hits.length ? <div className="grid gap-3 md:grid-cols-2">{hits.map(rowOf)}</div>
              : <p data-testid="settings-search-empty" className="rounded-xl border border-dashed border-line px-4 py-6 text-center text-[12.5px] text-mute">{t("settings.find.empty", { q: q.trim() })}</p>}
          </section>
        ) : (
          <>
            <Section title={t("admin.group.workspace")}>{group("workspace")}</Section>
            <Section title={t("admin.group.safety")}>{group("safety")}</Section>
            {signedIn && (
              <Section title={t("admin.group.account")}>
                <div data-tour="settings.account" className="rounded-xl border border-line bg-panel p-4 text-[12.5px] text-mute">
                  <b className="block truncate text-[13.5px] text-ink">{session.user?.name}</b>
                  <span className="block truncate">{session.user?.email} · {t(`members.roles.${session.role}`)}</span>
                </div>
                {group("account")}
              </Section>
            )}
          </>
        )}
      </div>
      {onboarding.dialogs}
    </div>
  );
}

/** Lower case, without accents: "contraseña" and "Contrasena" match. */
const norm = (s: string) => s.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase();

/**
 * Settings that match every word of the query, best first: a word in the title beats one in the description, which
 * beats a keyword. Keywords come in Spanish, English and Portuguese (settings.kw.<id>), so "password", "contraseña" and
 * "senha" all find the account page whatever the app's language.
 */
function searchSettings<E extends { id: string; title: string; body: string }>(entries: E[], q: string): E[] {
  const words = norm(q).split(/\s+/).filter(Boolean);
  const scored: [E, number][] = [];
  for (const e of entries) {
    const title = norm(e.title), body = norm(e.body);
    const kw = norm((["es", "en", "pt"] as const).map((l) => tr(`settings.kw.${e.id}`, undefined, l)).join(" "));
    let score = 0;
    for (const w of words) {
      if (title.includes(w)) score += 3; else if (body.includes(w)) score += 2; else if (kw.includes(w)) score += 1; else { score = -1; break; }
    }
    if (score > 0) scored.push([e, score]);
  }
  return scored.sort((a, b) => b[1] - a[1]).map(([e]) => e);
}
