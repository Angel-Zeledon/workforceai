"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { go } from "@/lib/navBridge";
import { ApiError } from "@/lib/api";
import { officesApi } from "@/lib/officesApi";
import { teamsCatalogApi } from "@/lib/teamsCatalogApi";
import { blockedKey, destinationChoices, installOutcome, splitAgents, weekByDay, type TeamPreview } from "@/lib/teamsCatalogLogic";
import { PageHeader, PageSkeleton } from "../nav/ui2";

/** Preview of one team for THIS organization: who arrives, what they do the first week, who is already there, and
 *  "Contratar este equipo" with the department to hire it into. Nothing is hired until that button. */
export function TeamPreviewPanel({ teamKey, onBack, onInstalled }: { teamKey: string; onBack: () => void; onInstalled: () => void }) {
  const { t, locale } = useT();
  const [p, setP] = useState<TeamPreview | null>(null);
  const [failed, setFailed] = useState(false);
  const [dest, setDest] = useState(""); // "" = the server's default (the suggested department or General)
  const [choices, setChoices] = useState<{ id: string; name: string }[]>([]);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<{ ok: boolean; text: string } | null>(null);

  const load = useCallback((office: string) => {
    setFailed(false);
    return teamsCatalogApi.get(teamKey, locale, office || undefined).then(setP).catch(() => setFailed(true));
  }, [teamKey, locale]);

  useEffect(() => { setP(null); setNote(null); void load(dest); }, [load, dest]);
  useEffect(() => { officesApi.list().then((r) => setChoices(destinationChoices(r.offices))).catch(() => setChoices(destinationChoices(undefined))); }, []);

  const install = async () => {
    if (!p) return;
    setBusy(true); setNote(null);
    try {
      const r = await teamsCatalogApi.install(teamKey, locale, p.destination.office_id);
      const out = installOutcome(r);
      setP(r.preview);
      setNote({ ok: true, text: t(out.key, { count: out.count, office: r.preview.destination.office_name }) });
      onInstalled();
    } catch (e) {
      const status = e instanceof ApiError ? e.status : 0;
      setNote({ ok: false, text: t(status === 403 ? "teams.err.forbidden" : status === 409 ? "teams.blocked.officeFull" : "teams.err.generic") });
    } finally { setBusy(false); }
  };

  if (failed) return (
    <div className="p-4"><p role="alert" data-testid="team-preview-error" className="text-sm font-semibold text-red-600">{t("teams.loadError")}</p>
      <button type="button" onClick={onBack} className="mt-3 rounded-md border border-line px-3 py-1.5 text-xs font-semibold text-ink2">{t("teams.back")}</button></div>
  );
  if (!p) return <PageSkeleton />;

  const { arriving, already } = splitAgents(p);
  const why = blockedKey(p);
  const selected = dest || p.destination.office_id;
  const options = choices.length > 1 ? choices : [];

  return (
    <div data-testid="team-preview" className="flex h-full flex-col overflow-y-auto bg-bg">
      <PageHeader title={p.team.name} description={p.team.solves} testid="team-preview-header"
        actions={<button type="button" data-testid="team-back" onClick={onBack} className="min-h-10 rounded-md border border-line px-3 py-1.5 text-xs font-semibold text-ink2 hover:bg-panel2 sm:min-h-0">{t("teams.back")}</button>} />
      <div className="mx-auto grid w-full max-w-4xl gap-5 px-4 py-4 sm:px-6 sm:py-5">
        <p className="text-[13px] text-mute"><span className="font-semibold text-ink">{t("teams.forWho")}</span> {p.team.for}</p>

        <section aria-labelledby="team-arrive">
          <h2 id="team-arrive" className="mb-2 font-display text-[15px] font-semibold text-ink">{t("teams.arrive")}</h2>
          {arriving.length === 0
            ? <p data-testid="team-arrive-none" className="text-[13px] text-mute">{t("teams.arrive.none")}</p>
            : <ul className="grid gap-2 sm:grid-cols-2">
              {arriving.map((a) => (
                <li key={a.role} data-testid={`team-new-${a.role}`} className="rounded-[10px] border border-line bg-panel p-3">
                  <div className="flex items-baseline justify-between gap-2">
                    <span className="text-[14px] font-semibold text-ink">{a.name}</span>
                    <span className="text-[11px] font-semibold uppercase tracking-wide text-accent">{a.title}</span>
                  </div>
                  <p className="mt-1 text-[12.5px] leading-snug text-ink2">{a.does}</p>
                </li>
              ))}
            </ul>}
        </section>

        {already.length > 0 && (
          <section aria-labelledby="team-have">
            <h2 id="team-have" className="mb-1 font-display text-[15px] font-semibold text-ink">{t("teams.have")}</h2>
            <p className="mb-2 text-[12px] text-mute">{t("teams.have.hint", { office: p.destination.office_name })}</p>
            <ul className="flex flex-wrap gap-2">
              {already.map((a) => (
                <li key={a.role} data-testid={`team-have-${a.role}`} className="rounded-full border border-line bg-panel2 px-3 py-1 text-[12.5px] text-ink">
                  {a.existing?.name ? <><span className="font-semibold">{a.existing.name}</span> · {a.title}</> : a.title}
                </li>
              ))}
            </ul>
          </section>
        )}

        <section aria-labelledby="team-week">
          <h2 id="team-week" className="mb-2 font-display text-[15px] font-semibold text-ink">{t("teams.week")}</h2>
          <ol className="grid gap-2">
            {weekByDay(p.week).map((d) => (
              <li key={d.day} data-testid={`team-day-${d.day}`} className="flex gap-3 rounded-[10px] border border-line bg-panel p-3">
                <span className="w-14 shrink-0 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("teams.day", { day: d.day })}</span>
                <div className="min-w-0 flex-1">
                  {d.items.map((w) => (
                    <div key={w.template}>
                      <div className="text-[13.5px] font-semibold text-ink">{w.name}</div>
                      <p className="text-[12.5px] leading-snug text-ink2">{w.why}</p>
                    </div>
                  ))}
                </div>
              </li>
            ))}
          </ol>
        </section>

        {p.skills.length > 0 && (
          <section aria-labelledby="team-skills">
            <h2 id="team-skills" className="mb-1 font-display text-[15px] font-semibold text-ink">{t("teams.skills")}</h2>
            <p className="mb-2 text-[12px] text-mute">{t("teams.skills.hint")}</p>
            <ul className="flex flex-wrap gap-1.5">
              {p.skills.map((s) => <li key={s} className="rounded-full border border-line bg-panel2 px-2 py-0.5 text-[11px] text-ink2">{s}</li>)}
            </ul>
          </section>
        )}

        <section aria-labelledby="team-hire" className="rounded-[10px] border border-line bg-panel p-4">
          <h2 id="team-hire" className="font-display text-[15px] font-semibold text-ink">{t("teams.hire.title")}</h2>
          {options.length > 0 && (
            <label className="mt-2 flex flex-col gap-1 text-[12px] font-semibold text-ink">
              {t("teams.hire.where")}
              <select data-testid="team-dest" value={selected} onChange={(e) => setDest(e.target.value)} disabled={busy}
                className="max-w-xs rounded-[10px] border border-line-strong bg-panel px-3 py-2 text-sm font-semibold outline-none focus:border-accent">
                {options.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}
              </select>
            </label>
          )}
          {options.length === 0 && <p className="mt-1 text-[12.5px] text-mute">{t("teams.hire.in", { office: p.destination.office_name })}</p>}
          <p className="mt-2 text-[12px] text-mute">{t("teams.hire.rules")}</p>
          {why && <p data-testid="team-blocked" className="mt-2 text-[12.5px] font-semibold text-ink2">{t(why)}</p>}
          {note && <p role={note.ok ? "status" : "alert"} data-testid="team-note" className={`mt-2 text-[13px] font-semibold ${note.ok ? "text-ink" : "text-red-600"}`}>{note.text}</p>}
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <button type="button" data-testid="team-install" disabled={busy || !!why} onClick={() => void install()}
              className="min-h-10 rounded-md bg-accent px-5 py-2 text-xs font-semibold text-white hover:bg-accent-hover disabled:opacity-50 sm:min-h-0">
              {busy ? t("teams.hire.busy") : t("teams.hire.button")}
            </button>
            {note?.ok && <button type="button" data-testid="team-see-agents" onClick={() => go("/org")}
              className="min-h-10 rounded-md border border-line px-4 py-2 text-xs font-semibold text-ink2 hover:bg-panel2 sm:min-h-0">{t("teams.seeAgents")}</button>}
          </div>
        </section>
      </div>
    </div>
  );
}
