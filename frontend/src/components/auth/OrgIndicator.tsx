"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { orgLabel, switchOrg, useSession } from "@/lib/session";

/**
 * Current organization for the header. Open demo (auth off): the "demo office" caption, as before.
 * Signed in: the organization name, and a switcher when the user belongs to several organizations
 * (POST /auth/switch-org, then the app reloads with that tenant's data).
 */
export function OrgIndicator({ className = "" }: { className?: string }) {
  const { t } = useT();
  const status = useSession((s) => s.status);
  const orgId = useSession((s) => s.orgId);
  const orgs = useSession((s) => s.orgs);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState(false);

  if (status !== "authenticated") return <div className={className}>{t("app.demoOffice")}</div>;
  const current = orgs.find((o) => o.org_id === orgId);
  const name = orgLabel(current, orgId ?? "");

  if (orgs.length < 2) {
    return <div className={`truncate ${className}`} data-testid="header-org-name" title={t("org.current")}>{name}</div>;
  }
  const change = async (id: string) => {
    if (!id || id === orgId) return;
    setBusy(true); setErr(false);
    try { await switchOrg(id); } catch { setErr(true); setBusy(false); }
  };
  return (
    <div className={className}>
      <label className="sr-only" htmlFor="org-switcher">{t("org.switch")}</label>
      <select id="org-switcher" data-testid="org-switcher" value={orgId ?? ""} disabled={busy} title={t("org.switch")}
        onChange={(e) => change(e.target.value)}
        className="max-w-[180px] cursor-pointer truncate rounded border-none bg-transparent p-0 pr-4 text-inherit focus:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 disabled:opacity-50">
        {orgs.map((o) => (
          <option key={o.org_id} value={o.org_id} data-testid={`org-option-${o.org_id}`}>
            {orgLabel(o)} · {t(`members.roles.${o.role}`)}
          </option>
        ))}
      </select>
      <span className="sr-only" data-testid="header-org-name">{name}</span>
      {err && <span role="alert" className="ml-1 text-err">{t("org.switchError")}</span>}
    </div>
  );
}
