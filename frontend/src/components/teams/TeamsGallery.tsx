"use client";
import { useEffect, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { go } from "@/lib/navBridge";
import { ApiError } from "@/lib/api";
import { useCan } from "@/lib/session";
import { teamsCatalogApi } from "@/lib/teamsCatalogApi";
import { officeTemplatesApi } from "@/lib/officeTemplatesApi";
import { cardState, orderForPack, type TeamCard } from "@/lib/teamsCatalogLogic";
import { checkFile, type OfficeTemplateCard } from "@/lib/officeTemplatesLogic";
import { PageHeader, PageSkeleton } from "../nav/ui2";
import { TeamPreviewPanel } from "./TeamPreviewPanel";
import { OfficeTemplatePreviewPanel } from "./OfficeTemplatePreviewPanel";

/** Equipos: ready-to-hire business teams (F2). A card per team; opening one shows who arrives, what they do the first
 *  week and who is already there, before anything is hired. `team` opens a preview, `pack` puts first the teams
 *  that suit the business type chosen in the onboarding. "Tus plantillas" (F3) are the office templates imported into
 *  this organization, with the same preview and install; `template` opens one. */
export function TeamsGallery({ team, pack, template }: { team?: string; pack?: string; template?: string }) {
  const { t, locale } = useT();
  const canImport = useCan("agents:write");
  const [cards, setCards] = useState<TeamCard[] | null>(null);
  const [mine, setMine] = useState<OfficeTemplateCard[]>([]);
  const [failed, setFailed] = useState(false);
  const [tick, setTick] = useState(0); // reload the cards after hiring: their counts change
  const [importNote, setImportNote] = useState("");
  const [importing, setImporting] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    let alive = true;
    setFailed(false);
    teamsCatalogApi.list(locale).then((c) => { if (alive) setCards(c); }).catch(() => { if (alive) setFailed(true); });
    officeTemplatesApi.list().then((c) => { if (alive) setMine(c); }).catch(() => { if (alive) setMine([]); });
    return () => { alive = false; };
  }, [locale, tick]);

  const open = (key: string) => go(`/teams?team=${encodeURIComponent(key)}${pack ? `&pack=${encodeURIComponent(pack)}` : ""}`);
  const openTemplate = (id: string) => go(`/teams?template=${encodeURIComponent(id)}`);
  const back = () => go(`/teams${pack ? `?pack=${encodeURIComponent(pack)}` : ""}`);

  const importFile = async (f: File | undefined) => {
    if (!f) return;
    setImportNote("");
    const early = checkFile(f.name, f.size);
    if (early) { setImportNote(t(early)); return; }
    const text = await f.text();
    const why = checkFile(f.name, f.size, text);
    if (why) { setImportNote(t(why)); return; }
    setImporting(true);
    try {
      const p = await officeTemplatesApi.importFile(JSON.parse(text));
      setTick((n) => n + 1);
      openTemplate(p.id);
    } catch (e) {
      const s = e instanceof ApiError ? e.status : 0;
      setImportNote(t(s === 413 ? "otpl.import.tooLarge" : s === 400 ? "otpl.import.invalid" : s === 403 ? "teams.err.forbidden" : s === 409 ? "otpl.import.full" : "teams.err.generic"));
    } finally {
      setImporting(false);
      if (fileRef.current) fileRef.current.value = "";
    }
  };

  if (template) return <OfficeTemplatePreviewPanel id={template} onBack={back} onChanged={() => setTick((n) => n + 1)} />;
  if (team) return <TeamPreviewPanel teamKey={team} onBack={back} onInstalled={() => setTick((n) => n + 1)} />;

  const ordered = cards ? orderForPack(cards, pack ?? "") : [];
  const suited = pack && cards ? cards.filter((c) => c.packs.includes(pack)).length : 0;

  return (
    <div data-testid="teams-page" className="flex h-full flex-col overflow-y-auto bg-bg">
      <PageHeader title={t("teams.title")} description={t("teams.subtitle")} testid="teams-header"
        actions={canImport ? (
          <>
            <input ref={fileRef} type="file" accept=".json,application/json" className="hidden" data-testid="otpl-import-file"
              onChange={(e) => void importFile(e.target.files?.[0])} />
            <button type="button" data-testid="otpl-import" disabled={importing} onClick={() => fileRef.current?.click()}
              className="min-h-10 rounded-md border border-line px-3 py-1.5 text-xs font-semibold text-ink2 hover:bg-panel2 disabled:opacity-50 sm:min-h-0">
              {importing ? t("otpl.import.busy") : t("otpl.import.button")}
            </button>
          </>
        ) : undefined} />
      <div className="mx-auto w-full max-w-6xl px-4 py-4 sm:px-6 sm:py-5">
        {importNote && <p role="alert" data-testid="otpl-import-note" className="mb-4 text-[13px] font-semibold text-red-600">{importNote}</p>}
        {mine.length > 0 && (
          <section aria-labelledby="otpl-mine" className="mb-6">
            <h2 id="otpl-mine" className="font-display text-[15px] font-semibold text-ink">{t("otpl.mine.title")}</h2>
            <p className="mb-3 text-[12px] text-mute">{t("otpl.mine.hint")}</p>
            <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {mine.map((c) => (
                <li key={c.id} data-testid={`otpl-card-${c.id}`} className="flex min-w-0 flex-col rounded-[10px] border border-line bg-panel p-4">
                  <div className="flex flex-wrap items-center gap-1.5">
                    <span className="rounded-full bg-accent-soft px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-accent">{t(c.kind === "company" ? "otpl.kind.company" : "otpl.kind.department")}</span>
                    {c.departments.slice(0, 3).map((d) => <span key={d} className="rounded-full border border-line px-2 py-0.5 text-[10px] font-semibold text-ink2">{d}</span>)}
                  </div>
                  <h3 className="mt-2 font-display text-[15px] font-semibold leading-snug text-ink">{c.name}</h3>
                  {c.for && <p className="mt-1 text-[12px] leading-snug text-mute">{c.for}</p>}
                  <p className="mt-2 flex-1 text-[12.5px] leading-snug text-ink2">{t("otpl.mine.counts", { people: c.agents, skills: c.skills, workflows: c.workflows })}</p>
                  <p className="mt-2 text-[11.5px] font-semibold text-mute">
                    {{ all: t("teams.card.all"), some: t("teams.card.some", { new: c.new_count, have: c.existing_count }), none: t("teams.card.none", { count: c.new_count }) }[cardState(c)]}
                  </p>
                  <button type="button" data-testid={`otpl-open-${c.id}`} onClick={() => openTemplate(c.id)}
                    className="mt-3 min-h-10 self-start rounded-md bg-accent px-4 py-1.5 text-xs font-semibold text-white hover:bg-accent-hover sm:min-h-0">{t("teams.see")}</button>
                </li>
              ))}
            </ul>
          </section>
        )}
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
