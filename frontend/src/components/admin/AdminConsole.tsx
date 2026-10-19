"use client";
import { Suspense } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { PageHeader, PageSkeleton } from "../nav/ui2";
import { ApiAccess } from "../settings/ApiAccess";
import { AdminAudit } from "./AdminAudit";
import { AdminMembers } from "./AdminMembers";
import { AdminPolicies } from "./AdminPolicies";
import { AdminReports } from "./AdminReports";
import { AdminRetention } from "./AdminRetention";
import { AdminSpend } from "./AdminSpend";
import { Notice } from "./AdminParts";

const TABS = ["spend", "members", "reports", "audit", "retention", "api", "policies"] as const;
type Tab = (typeof TABS)[number];

function Inner() {
  const { t } = useT();
  const router = useRouter();
  const raw = useSearchParams().get("tab");
  const tab: Tab = (TABS as readonly string[]).includes(raw ?? "") ? (raw as Tab) : "spend";
  const status = useSession((s) => s.status);
  const role = useSession((s) => s.role);
  // Demo mode (no accounts) behaves as before: everything is reachable. With accounts, owner/admin only.
  const allowed = status !== "authenticated" || role === "owner" || role === "admin";
  return (
    <div data-testid="admin-console" className="flex h-full flex-col bg-bg">
      <PageHeader testid="admin-header" title={t("adm.title")} description={t("adm.desc")} />
      {!allowed ? (
        <div className="p-5"><Notice kind="warn">{t("adm.denied")}</Notice></div>
      ) : (
        <>
          <div role="tablist" aria-label={t("adm.title")} className="flex shrink-0 gap-1 overflow-x-auto border-b border-line bg-panel px-4 sm:px-6">
            {TABS.map((id) => (
              <button key={id} type="button" role="tab" aria-selected={tab === id} data-testid={`admin-tab-${id}`}
                onClick={() => router.push(`/admin?tab=${id}`)}
                className={`whitespace-nowrap border-b-2 px-3 py-2.5 text-[12.5px] font-medium transition ${tab === id ? "border-accent text-accent" : "border-transparent text-mute hover:text-ink"}`}>
                {t(`adm.tab.${id}`)}
              </button>
            ))}
          </div>
          <div role="tabpanel" className="min-h-0 flex-1 overflow-y-auto p-4 sm:p-6">
            <div className="mx-auto max-w-5xl">
              {tab === "spend" && <AdminSpend />}
              {tab === "members" && <AdminMembers />}
              {tab === "reports" && <AdminReports />}
              {tab === "audit" && <AdminAudit />}
              {tab === "retention" && <AdminRetention />}
              {tab === "api" && <ApiAccess />}
              {tab === "policies" && <AdminPolicies />}
            </div>
          </div>
        </>
      )}
    </div>
  );
}

/** Administration console: spend and limits, members, reports, audit, retention, API access and policies. */
export function AdminConsole() {
  return <Suspense fallback={<PageSkeleton />}><Inner /></Suspense>;
}
