"use client";
import Link from "next/link";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { officesApi } from "@/lib/officesApi";
import { officeBadge, sortOffices, type OfficesResponse } from "@/lib/officesLogic";
import { Icon } from "../icons";

/** `/offices`: one plain card per office (name, open or private, who leads it, how many people and agents). A closed private
 *  office is shown locked and does not open. A click goes to the office's own page (`/offices/<id>`). */
export function OfficeCards({ refresh = 0 }: { refresh?: number }) {
  const { t } = useT();
  const [data, setData] = useState<OfficesResponse | null>(null);
  const [error, setError] = useState(false);
  const load = () => { setError(false); officesApi.list().then(setData).catch(() => setError(true)); };
  useEffect(load, [refresh]);

  if (error) return <div role="alert" className="text-sm font-semibold text-red-600">{t("offices.error.load")} <button type="button" onClick={load} className="underline">{t("org.retry")}</button></div>;
  if (!data) return <div className="text-sm text-mute">{t("org.loading")}</div>;
  const list = sortOffices(data.offices);

  return (
    <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3" data-testid="office-cards">
      {list.map((o) => {
        const badge = officeBadge(o);
        const body = (
          <>
            <span className="flex items-center justify-between gap-2">
              <span className="flex min-w-0 items-center gap-2 text-[14px] font-semibold"><Icon name={o.can_enter ? "office" : "shield"} size={14} /><span className="truncate">{o.general ? t("offices.general") : o.name}</span></span>
              <span data-testid={`office-badge-${o.id}`} className="shrink-0 text-[12px] text-mute">{t(`offices.badge.${badge}`)}</span>
            </span>
            <span className="mt-1 block text-[12px] text-mute">{t("offices.leads", { names: o.leads.map((l) => l.name || "?").join(", ") || t("org.noHead") })}</span>
            <span className="mt-0.5 block text-[12px] text-mute">{o.can_enter ? t("offices.counts", { people: t("count.people", { count: o.member_count }), agents: t("count.agents", { count: o.agents.length }) }) : t("count.people", { count: o.member_count })}</span>
            {!o.can_enter && <span data-testid={`office-door-${o.id}`} className="mt-1 block text-[12px] text-mute">{t("offices.closedDoor")}</span>}
          </>
        );
        return (
          <li key={o.id} data-testid={`office-${o.id}`}>
            {o.can_enter
              ? <Link href={`/offices/${encodeURIComponent(o.id)}`} prefetch={false} className="block min-h-[88px] rounded-[10px] border border-line bg-panel p-4 hover:border-accent/60 focus-visible:outline-2 focus-visible:outline-accent">{body}</Link>
              : <div aria-disabled="true" className="block min-h-[88px] rounded-[10px] border border-dashed border-line bg-panel2 p-4 opacity-80">{body}</div>}
          </li>
        );
      })}
    </ul>
  );
}
