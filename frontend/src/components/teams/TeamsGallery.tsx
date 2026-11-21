"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { go } from "@/lib/navBridge";
import { teamsCatalogApi } from "@/lib/teamsCatalogApi";
import { cardState, orderForPack, type TeamCard } from "@/lib/teamsCatalogLogic";
import { PageHeader, PageSkeleton } from "../nav/ui2";
import { TeamPreviewPanel } from "./TeamPreviewPanel";

/** Equipos: ready-to-hire business teams (F2). A card per team; opening one shows who arrives, what they do the first
 *  week and who is already there, before anything is hired. `team` opens a preview, `pack` puts first the teams
 *  that suit the business type chosen in the onboarding. */
export function TeamsGallery({ team, pack }: { team?: string; pack?: string }) {
  const { t, locale } = useT();
  const [cards, setCards] = useState<TeamCard[] | null>(null);
  const [failed, setFailed] = useState(false);
  const [tick, setTick] = useState(0); // reload the cards after hiring: their counts change

  useEffect(() => {
    let alive = true;
    setFailed(false);
    teamsCatalogApi.list(locale).then((c) => { if (alive) setCards(c); }).catch(() => { if (alive) setFailed(true); });
    return () => { alive = false; };
  }, [locale, tick]);

  const open = (key: string) => go(`/teams?team=${encodeURIComponent(key)}${pack ? `&pack=${encodeURIComponent(pack)}` : ""}`);
  const back = () => go(`/teams${pack ? `?pack=${encodeURIComponent(pack)}` : ""}`);

  if (team) return <TeamPreviewPanel teamKey={team} onBack={back} onInstalled={() => setTick((n) => n + 1)} />;

  const ordered = cards ? orderForPack(cards, pack ?? "") : [];
  const suited = pack && cards ? cards.filter((c) => c.packs.includes(pack)).length : 0;

  return (
    <div data-testid="teams-page" className="flex h-full flex-col overflow-y-auto bg-bg">
      <PageHeader title={t("teams.title")} description={t("teams.subtitle")} testid="teams-header" />
      <div className="mx-auto w-full max-w-6xl px-4 py-4 sm:px-6 sm:py-5">
        {pack && suited > 0 && (
          <p data-testid="teams-pack-note" className="mb-4 rounded-[10px] border border-accent/25 bg-accent-soft px-3 py-2 text-[13px] text-ink">
            {t("teams.packNote", { count: suited })}
          </p>
        )}
        {failed && <p role="alert" data-testid="teams-error" className="text-sm font-semibold text-red-600">{t("teams.loadError")}</p>}
        {!failed && !cards && <PageSkeleton />}
        <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {ordered.map((c) => (
            <li key={c.key} data-testid={`team-card-${c.key}`} className="flex min-w-0 flex-col rounded-[10px] border border-line bg-panel p-4">
              <div className="flex flex-wrap items-center gap-1.5">
                <span className="rounded-full bg-accent-soft px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-accent">{c.department}</span>
                {pack && c.packs.includes(pack) && <span className="rounded-full border border-accent/30 px-2 py-0.5 text-[10px] font-semibold text-accent">{t("teams.suited")}</span>}
              </div>
              <h2 className="mt-2 font-display text-[15px] font-semibold leading-snug text-ink">{c.name}</h2>
              <p className="mt-1 text-[12px] leading-snug text-mute">{c.for}</p>
              <p className="mt-2 flex-1 text-[13px] leading-snug text-ink">{c.solves}</p>
              <ul className="mt-3 flex flex-wrap gap-1.5" aria-label={t("teams.arrive")}>
                {c.agents.map((a) => <li key={a.role} className="rounded-full border border-line bg-panel2 px-2 py-0.5 text-[11px] text-ink2">{a.title}</li>)}
              </ul>
              <p data-testid={`team-card-state-${c.key}`} className="mt-3 text-[11.5px] font-semibold text-mute">
                {{ all: t("teams.card.all"), some: t("teams.card.some", { new: c.new_count, have: c.existing_count }), none: t("teams.card.none", { count: c.new_count }) }[cardState(c)]}
              </p>
              <button type="button" data-testid={`team-open-${c.key}`} onClick={() => open(c.key)}
                className="mt-3 min-h-10 self-start rounded-md bg-accent px-4 py-1.5 text-xs font-semibold text-white hover:bg-accent-hover sm:min-h-0">{t("teams.see")}</button>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
