"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { nameAndRole } from "@/lib/teamsCatalogLogic";
import { go } from "@/lib/navBridge";
import { ApiError } from "@/lib/api";
import { officesApi } from "@/lib/officesApi";
import { officeTemplatesApi } from "@/lib/officeTemplatesApi";
import { blockedKey, destinationChoices } from "@/lib/teamsCatalogLogic";
import { installLines, itemStatusKey, previewPeople, type OfficeTemplateItem, type OfficeTemplatePreview } from "@/lib/officeTemplatesLogic";
import { PageHeader, PageSkeleton } from "../nav/ui2";

/** Preview of an imported office template for THIS organization (F3), with the same shape as a ready-to-hire team:
 *  who arrives in each department, who is already there, the skills (they arrive for review), the work templates,
 *  the schedules (switched off) and the goals. Nothing is created until "Instalar esta plantilla". */
export function OfficeTemplatePreviewPanel({ id, onBack, onChanged }: { id: string; onBack: () => void; onChanged: () => void }) {
  const { t } = useT();
  const [p, setP] = useState<OfficeTemplatePreview | null>(null);
  const [failed, setFailed] = useState(false);
  const [dest, setDest] = useState("");
  const [choices, setChoices] = useState<{ id: string; name: string }[]>([]);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<{ ok: boolean; lines: string[] } | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const load = useCallback((office: string) => {
    setFailed(false);
    return officeTemplatesApi.get(id, office || undefined).then(setP).catch(() => setFailed(true));
  }, [id]);
  useEffect(() => { setP(null); setNote(null); void load(dest); }, [load, dest]);
  useEffect(() => { officesApi.list().then((r) => setChoices(destinationChoices(r.offices))).catch(() => setChoices(destinationChoices(undefined))); }, []);

  const install = async () => {
    if (!p) return;
    setBusy(true); setNote(null);
    try {
      const r = await officeTemplatesApi.install(id, dest || undefined);
      setP(r.preview);
      const lines = installLines(r).map((l) => t(l.key, { count: l.count }));
      setNote({ ok: true, lines: lines.length ? [...lines, ...r.skipped.map((s) => t("otpl.done.skipped", { what: s }))] : [t("otpl.done.nothing")] });
      onChanged();
    } catch (e) {
      const s = e instanceof ApiError ? e.status : 0;
      setNote({ ok: false, lines: [t(s === 403 ? "teams.err.forbidden" : s === 409 ? "teams.blocked.officeFull" : "teams.err.generic")] });
    } finally { setBusy(false); }
  };

  const remove = async () => {
    setBusy(true);
    try { await officeTemplatesApi.remove(id); onChanged(); onBack(); } catch { setNote({ ok: false, lines: [t("teams.err.generic")] }); } finally { setBusy(false); }
  };

  if (failed) return (
    <div className="p-4"><p role="alert" data-testid="otpl-preview-error" className="text-sm font-semibold text-red-600">{t("teams.loadError")}</p>
      <button type="button" onClick={onBack} className="mt-3 rounded-md border border-line px-3 py-1.5 text-xs font-semibold text-ink2">{t("teams.back")}</button></div>
  );
  if (!p) return <PageSkeleton />;

  const why = blockedKey(p);
  const people = previewPeople(p);
  const options = choices.length > 1 ? choices : [];
  const chip = (i: OfficeTemplateItem) => (
    <span className={`rounded-full px-2 py-0.5 text-[10.5px] font-semibold ${i.status === "existing" ? "bg-panel2 text-mute" : i.status === "archived" ? "border border-line text-ink2" : "bg-accent-soft text-accent"}`}>
      {t(itemStatusKey(i))}
    </span>
  );
  const list = (title: string, hint: string, items: OfficeTemplateItem[], testid: string) => items.length === 0 ? null : (
    <section aria-label={title}>
      <h2 className="mb-1 font-display text-[15px] font-semibold text-ink">{title}</h2>
      {hint && <p className="mb-2 text-[12px] text-mute">{hint}</p>}
      <ul data-testid={testid} className="grid gap-1.5">
        {items.map((i, n) => (
          <li key={`${i.name}-${n}`} className="flex items-center justify-between gap-2 rounded-[10px] border border-line bg-panel px-3 py-2 text-[13px] text-ink">
            <span className="min-w-0 truncate">{i.name}{i.detail && testid === "otpl-schedules" ? <span className="ml-2 text-[12px] text-mute">{i.detail}</span> : null}</span>{chip(i)}
          </li>
        ))}
      </ul>
    </section>
  );

  return (
    <div data-testid="otpl-preview" className="flex h-full flex-col overflow-y-auto bg-bg">
      <PageHeader title={p.name} description={p.solves || t("otpl.preview.subtitle")} testid="otpl-preview-header"
        actions={<button type="button" data-testid="otpl-back" onClick={onBack} className="min-h-10 rounded-md border border-line px-3 py-1.5 text-xs font-semibold text-ink2 hover:bg-panel2 sm:min-h-0">{t("teams.back")}</button>} />
      <div className="mx-auto grid w-full max-w-4xl gap-5 px-4 py-4 sm:px-6 sm:py-5">
        {p.for && <p className="text-[13px] text-mute"><span className="font-semibold text-ink">{t("teams.forWho")}</span> {p.for}</p>}
        <p className="rounded-[10px] border border-accent/25 bg-accent-soft px-3 py-2 text-[12.5px] text-ink">{t("otpl.preview.trust")}</p>

        {p.departments.map((d) => {
          const arriving = d.agents.filter((a) => a.status === "new");
          const already = d.agents.filter((a) => a.status === "existing");
          return (
            <section key={d.key} data-testid={`otpl-dept-${d.key}`} aria-label={d.name}>
              <h2 className="font-display text-[15px] font-semibold text-ink">{d.name}</h2>
              <p className="mb-2 text-[12px] text-mute">
                {d.destination.matched ? t("otpl.preview.matched", { office: d.destination.office_name }) : t("otpl.preview.goesTo", { office: d.destination.office_name })}
              </p>
              {arriving.length > 0 && (
                <ul className="grid gap-2 sm:grid-cols-2">
                  {arriving.map((a, i) => (
                    <li key={`${a.role}-${i}`} className="rounded-[10px] border border-line bg-panel p-3">
                      <div className="flex items-baseline justify-between gap-2">
                        <span className="text-[14px] font-semibold text-ink">{nameAndRole(a.name, a.title).primary}</span>
                        {nameAndRole(a.name, a.title).secondary && <span className="text-[12px] font-medium text-accent">{nameAndRole(a.name, a.title).secondary}</span>}
                      </div>
                      {a.does && <p className="mt-1 text-[12.5px] leading-snug text-ink2">{a.does}</p>}
                    </li>
                  ))}
                </ul>
              )}
              {already.length > 0 && (
                <ul className="mt-2 flex flex-wrap gap-2" aria-label={t("teams.have")}>
                  {already.map((a, i) => (
                    <li key={`${a.role}-${i}`} className="rounded-full border border-line bg-panel2 px-3 py-1 text-[12.5px] text-ink">
                      {a.existing?.name ? <><span className="font-semibold">{a.existing.name}</span> · {a.title}</> : a.title}
                    </li>
                  ))}
                </ul>
              )}
            </section>
          );
        })}

        {list(t("otpl.preview.skills"), t("otpl.preview.skillsHint"), p.skills, "otpl-skills")}
        {list(t("otpl.preview.workflows"), "", p.workflows, "otpl-workflows")}
        {list(t("otpl.preview.schedules"), t("otpl.preview.schedulesHint"), p.schedules, "otpl-schedules")}
        {list(t("otpl.preview.goals"), t("otpl.preview.goalsHint"), p.goals, "otpl-goals")}

        <section aria-labelledby="otpl-install" className="rounded-[10px] border border-line bg-panel p-4">
          <h2 id="otpl-install" className="font-display text-[15px] font-semibold text-ink">{t("otpl.install.title")}</h2>
          <p className="mt-1 text-[12.5px] text-mute">{t("otpl.install.people", { arriving: people.arriving, already: people.already })}</p>
          {options.length > 0 && (
            <label className="mt-2 flex flex-col gap-1 text-[12px] font-semibold text-ink">
              {t("otpl.install.where")}
              <select data-testid="otpl-dest" value={dest || p.office_id} onChange={(e) => setDest(e.target.value)} disabled={busy}
                className="max-w-xs rounded-[10px] border border-line-strong bg-panel px-3 py-2 text-sm font-semibold outline-none focus:border-accent">
                {options.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}
              </select>
            </label>
          )}
          <p className="mt-2 text-[12px] text-mute">{t("teams.hire.rules")}</p>
          {why && <p data-testid="otpl-blocked" className="mt-2 text-[12.5px] font-semibold text-ink2">{t(why === "teams.blocked.nothingNew" ? "otpl.install.nothingNew" : why)}</p>}
          {note && (
            <ul role={note.ok ? "status" : "alert"} data-testid="otpl-note" className={`mt-2 grid gap-0.5 text-[13px] font-semibold ${note.ok ? "text-ink" : "text-red-600"}`}>
              {note.lines.map((l, i) => <li key={i}>{l}</li>)}
            </ul>
          )}
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <button type="button" data-testid="otpl-install" disabled={busy || !!why} onClick={() => void install()}
              className="min-h-10 rounded-md bg-accent px-5 py-2 text-xs font-semibold text-white hover:bg-accent-hover disabled:opacity-50 sm:min-h-0">
              {busy ? t("teams.hire.busy") : t("otpl.install.button")}
            </button>
            {note?.ok && <button type="button" onClick={() => go("/agents")} className="min-h-10 rounded-md border border-line px-4 py-2 text-xs font-semibold text-ink2 hover:bg-panel2 sm:min-h-0">{t("teams.seeAgents")}</button>}
            {!confirmDelete
              ? <button type="button" data-testid="otpl-delete" disabled={busy} onClick={() => setConfirmDelete(true)} className="ml-auto text-[12px] text-mute hover:text-red-600 hover:underline">{t("otpl.delete")}</button>
              : <button type="button" data-testid="otpl-delete-confirm" disabled={busy} onClick={() => void remove()} className="ml-auto rounded-md border border-red-300 px-3 py-1.5 text-[12px] font-semibold text-red-600 hover:bg-red-50">{t("otpl.deleteConfirm")}</button>}
          </div>
        </section>
      </div>
    </div>
  );
}
