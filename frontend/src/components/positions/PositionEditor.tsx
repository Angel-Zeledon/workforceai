"use client";
import { useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import { agentPositionApi, positionError, positionsApi, type PositionsView } from "@/lib/positions";
import {
  clampToSuperior, exceeds, impactLines, isDirty, levelLabelKey, proposeFromSuperior, relativePath, superiorChoices, toggleSkill,
  type Change, type LibraryEntry, type Package, type Position,
} from "@/lib/positionsLogic";
import { Btn, Card } from "../ui";
import { FolderTreePicker } from "./FolderTreePicker";

const SEARCH_AT = 8;

/** The package of one position: name, who it reports to, skills, library; plus who holds it. Saving a cut for a
 * position that has subordinates first asks the server (dry run) what they would lose and waits for a confirmation. */
export function PositionEditor({ view, position, seed, onSaved, onClose }: {
  view: PositionsView; position: Position | null; seed?: { name: string; reports_to: string; skills: string[]; library: LibraryEntry[]; template: Position["template"] };
  onSaved: (id: string | null) => Promise<void>; onClose: () => void;
}) {
  const { t } = useT();
  const canManage = view.can_manage;
  const init = position ?? seed ?? { name: "", reports_to: "", skills: [], library: [], template: "" as const };
  const [name, setName] = useState(init.name);
  const [reportsTo, setReportsTo] = useState(init.reports_to);
  const [skills, setSkills] = useState<string[]>(init.skills);
  const [library, setLibrary] = useState<LibraryEntry[]>(init.library);
  const [search, setSearch] = useState("");
  const [proposedFrom, setProposedFrom] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const [impact, setImpact] = useState<Change[] | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [pickAgent, setPickAgent] = useState("");

  const bounds = view.bounds;
  const sup = view.positions.find((p) => p.id === reportsTo) ?? null;
  const supPkg: Package | null = sup ? { skills: sup.skills, library: sup.library } : null;
  const draft = { name, reports_to: reportsTo, skills, library };
  const over = supPkg ? exceeds(supPkg, draft, bounds) : { skills: [], folders: [] };
  const hasOver = over.skills.length > 0 || over.folders.length > 0;
  const skillName = (id: string) => view.skills.find((s) => s.id === id)?.name ?? id;
  const folderLabel = (path: string) => relativePath(path, bounds) || t("pos.lib.rootOffice", { name: view.office.name });
  const choices = useMemo(() => superiorChoices(view.positions, position?.id ?? null), [view.positions, position?.id]);

  // the skills one can tick: all of the department's, or only the superior's (plus what is ticked, so it can be unticked)
  const options = view.skills.filter((s) => !sup || sup.skills.includes(s.id) || skills.includes(s.id));
  const shown = options.filter((s) => !search.trim() || `${s.name} ${s.description}`.toLowerCase().includes(search.trim().toLowerCase()));

  const changeSuperior = (id: string) => {
    setReportsTo(id); setImpact(null); setSaved(false);
    const s = view.positions.find((p) => p.id === id);
    if (s && !position && !skills.length && !library.length) {
      const p = proposeFromSuperior({ skills: s.skills, library: s.library }, bounds);
      setLibrary(p.library); setSkills(p.skills); setProposedFrom(s.name);
    }
  };

  const body = () => ({ name: name.trim(), reports_to: reportsTo, skills, library });
  const run = async (fn: () => Promise<void>) => {
    setBusy(true); setError(""); setSaved(false);
    try { await fn(); } catch (e) { setError(positionError(e, t)); }
    setBusy(false);
  };

  const save = () => run(async () => {
    if (!position) {
      const created = await positionsApi.create(view.office.id, { ...body(), ...(init.template ? { template: init.template } : {}) });
      await onSaved(created.id);
      return;
    }
    const dry = await positionsApi.update(view.office.id, position.id, { ...body(), dry_run: true });
    const lines = impactLines(dry.impact);
    if (lines.length) { setImpact(lines); return; }
    await positionsApi.update(view.office.id, position.id, body());
    await onSaved(position.id); setSaved(true);
  });
  const confirmSave = () => run(async () => {
    if (!position) return;
    await positionsApi.update(view.office.id, position.id, body());
    setImpact(null); await onSaved(position.id); setSaved(true);
  });
  const remove = () => run(async () => {
    if (!position) return;
    await positionsApi.remove(view.office.id, position.id);
    await onSaved(null); onClose();
  });
  const assign = () => run(async () => { await agentPositionApi.set(pickAgent, position!.id); setPickAgent(""); await onSaved(position!.id); });
  const unseat = (agentId: string) => run(async () => { await agentPositionApi.clear(agentId); await onSaved(position!.id); });

  const occupants = position ? view.agents.filter((a) => a.position_id === position.id) : [];
  const assignable = position ? view.agents.filter((a) => a.position_id !== position.id) : [];
  const posName = (id: string) => view.positions.find((p) => p.id === id)?.name ?? "";
  const lossLevel = (l: string) => t(levelLabelKey(l as "read" | "edit" | "none" | "default"));
  const dirty = isDirty(position, draft);

  return (
    <Card title={position ? t("pos.ed.edit", { name: position.name }) : t("pos.ed.new")}
      right={<button type="button" onClick={onClose} className="text-[11.5px] text-mute hover:text-ink">{t("pos.cancel")}</button>}>
      <div data-testid="position-editor" className="space-y-5">
        <fieldset disabled={!canManage || busy} className="space-y-5 border-0 p-0">
          <label className="block">
            <span className="mb-1 block text-[12px] font-medium text-ink">{t("pos.name")}</span>
            <input data-testid="pos-name" value={name} maxLength={80} onChange={(e) => { setName(e.target.value); setSaved(false); }} placeholder={t("pos.name.ph")}
              className="min-h-10 w-full rounded-lg border border-line bg-panel px-3 text-[13px] text-ink outline-none focus:border-accent" />
          </label>

          <label className="block">
            <span className="mb-1 block text-[12px] font-medium text-ink">{t("pos.reportsTo")}</span>
            <select data-testid="pos-reports-to" value={reportsTo} onChange={(e) => changeSuperior(e.target.value)}
              className="min-h-10 w-full rounded-lg border border-line bg-panel px-2 text-[13px] text-ink outline-none focus:border-accent">
              <option value="">{t("pos.reportsNone")}</option>
              {choices.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
            <span className="mt-1 block text-[11.5px] leading-snug text-mute">{t("pos.rule")}</span>
            {proposedFrom && <span className="mt-1 block text-[11.5px] leading-snug text-accent">{t("pos.proposed", { name: proposedFrom })}</span>}
          </label>

          <section aria-labelledby="pos-skills-h">
            <h4 id="pos-skills-h" className="text-[12px] font-medium text-ink">{t("pos.skills.title")}</h4>
            <p className="mb-2 text-[11.5px] leading-snug text-mute">{t("pos.skills.help")}{sup ? ` ${t("pos.skills.limited", { name: sup.name })}` : ""}</p>
            {options.length > SEARCH_AT && (
              <input data-testid="pos-skill-search" value={search} onChange={(e) => setSearch(e.target.value)} placeholder={t("pos.skills.search")} aria-label={t("pos.skills.search")}
                className="mb-2 min-h-9 w-full rounded-lg border border-line bg-panel px-3 text-[12.5px] outline-none focus:border-accent" />
            )}
            {!view.skills.length ? <p className="text-[12px] text-mute">{t("pos.skills.none")}</p>
              : sup && !options.length ? <p className="text-[12px] text-mute">{t("pos.skills.supNone", { name: sup.name })}</p>
              : !shown.length ? <p className="text-[12px] text-mute">{t("pos.skills.noMatch")}</p> : (
                <ul className="grid gap-1.5 sm:grid-cols-2">
                  {shown.map((s) => (
                    <li key={s.id}>
                      <label className="flex min-h-10 cursor-pointer items-start gap-2 rounded-lg border border-line px-2.5 py-2 hover:bg-panel2">
                        <input type="checkbox" data-testid={`pos-skill-${s.id}`} checked={skills.includes(s.id)} onChange={() => { setSkills(toggleSkill(skills, s.id)); setSaved(false); }} className="mt-0.5 accent-[#0A66C2]" />
                        <span className="min-w-0">
                          <span className="block break-words text-[12.5px] font-medium text-ink">{s.name}</span>
                          {s.description && <span className="block text-[11px] leading-snug text-mute">{s.description}</span>}
                        </span>
                      </label>
                    </li>
                  ))}
                </ul>
              )}
          </section>

          <section aria-labelledby="pos-lib-h">
            <h4 id="pos-lib-h" className="text-[12px] font-medium text-ink">{t("pos.lib.title")}</h4>
            <p className="mb-2 text-[11.5px] leading-snug text-mute">{t("pos.lib.help")}</p>
            <FolderTreePicker value={library} onChange={(l) => { setLibrary(l); setSaved(false); }} bounds={bounds} officeName={view.office.name} superior={supPkg} readOnly={!canManage} />
          </section>
        </fieldset>

        {hasOver && sup && (
          <div role="alert" data-testid="pos-exceeds" className="rounded-[10px] border border-warn/40 bg-warn/10 px-3 py-2.5 text-[12px] text-ink">
            <p className="font-medium">{t("pos.exceeds.title", { name: sup.name })}</p>
            <ul className="mt-1 list-disc space-y-0.5 pl-4">
              {over.skills.map((s) => <li key={s}>{t("pos.exceeds.skill", { name: skillName(s) })}</li>)}
              {over.folders.map((f) => <li key={f.path}>{t("pos.exceeds.folder", { path: folderLabel(f.path), level: lossLevel(f.level) })}</li>)}
            </ul>
            {canManage && <button type="button" data-testid="pos-clamp" onClick={() => { const c = clampToSuperior(supPkg!, draft, bounds); setSkills(c.skills); setLibrary(c.library); }} className="mt-2 text-[12px] font-medium text-accent hover:underline">{t("pos.exceeds.fix")}</button>}
          </div>
        )}

        {impact && (
          <div role="alert" data-testid="pos-impact" className="rounded-[10px] border border-warn/40 bg-warn/10 px-3 py-2.5 text-[12px] text-ink">
            <p className="font-medium">{t("pos.impact.title")}</p>
            <ul className="mt-1.5 space-y-1.5">
              {impact.map((c) => (
                <li key={c.position_id}>
                  <span className="font-medium">{c.name}</span>
                  <ul className="list-disc pl-4 text-ink2">
                    {c.skills_removed.length > 0 && <li>{t("pos.impact.skills", { list: c.skills_removed.map(skillName).join(", ") })}</li>}
                    {c.folders.map((f) => <li key={f.path}>{t("pos.impact.folder", { path: folderLabel(f.path), from: lossLevel(f.from), to: lossLevel(f.to) })}</li>)}
                  </ul>
                </li>
              ))}
            </ul>
            <div className="mt-2.5 flex flex-wrap gap-2">
              <Btn kind="primary" data-testid="pos-impact-confirm" disabled={busy} onClick={confirmSave}>{t("pos.impact.confirm")}</Btn>
              <Btn onClick={() => setImpact(null)}>{t("pos.impact.back")}</Btn>
            </div>
          </div>
        )}

        {error && <p role="alert" data-testid="pos-error" className="rounded-lg bg-red-500/10 px-2.5 py-2 text-[12px] text-red-700">{error}</p>}

        {canManage && !impact && (
          <div className="flex flex-wrap items-center gap-2">
            <Btn kind="primary" data-testid="pos-save" disabled={busy || !name.trim() || hasOver || (!!position && !dirty)} onClick={save}>
              {busy ? t("pos.saving") : position ? t("pos.save") : t("pos.create")}
            </Btn>
            {saved && <span role="status" className="text-[12px] text-ok">{t("pos.saved")}</span>}
            {position && !confirmDelete && <button type="button" data-testid="pos-delete" onClick={() => setConfirmDelete(true)} className="ml-auto text-[12px] text-mute hover:text-err">{t("pos.delete")}</button>}
          </div>
        )}
        {confirmDelete && position && (
          <div role="alert" className="rounded-[10px] border border-line px-3 py-2.5 text-[12px] text-ink">
            <p>{t("pos.delete.ask", { name: position.name })}</p>
            <div className="mt-2 flex gap-2"><Btn kind="danger" data-testid="pos-delete-yes" disabled={busy} onClick={remove}>{t("pos.delete.yes")}</Btn><Btn onClick={() => setConfirmDelete(false)}>{t("pos.cancel")}</Btn></div>
          </div>
        )}

        <section aria-labelledby="pos-occ-h" className="border-t border-line pt-4">
          <h4 id="pos-occ-h" className="text-[12px] font-medium text-ink">{t("pos.occ.title")}</h4>
          <p className="mb-2 text-[11.5px] text-mute">{t("pos.occ.help")}</p>
          {!position ? <p className="text-[12px] text-mute">{t("pos.occ.unsaved")}</p> : (
            <>
              {occupants.length === 0 ? <p className="mb-2 text-[12px] text-mute">{t("pos.vacant")}</p> : (
                <ul className="mb-2 space-y-1">
                  {occupants.map((a) => (
                    <li key={a.id} className="flex items-center gap-2 rounded-lg bg-panel2 px-2.5 py-1.5 text-[12.5px]">
                      <span className="min-w-0 flex-1 truncate text-ink">{a.name}</span>
                      {canManage && <button type="button" disabled={busy} aria-label={t("pos.occ.remove", { name: a.name })} onClick={() => unseat(a.id)} className="text-[11.5px] text-mute hover:text-err">{t("pos.lib.remove")}</button>}
                    </li>
                  ))}
                </ul>
              )}
              {canManage && (assignable.length === 0 ? <p className="text-[12px] text-mute">{t("pos.occ.none")}</p> : (
                <div className="flex gap-2">
                  <select data-testid="pos-assign-select" aria-label={t("pos.occ.pick")} value={pickAgent} onChange={(e) => setPickAgent(e.target.value)}
                    className="min-h-9 min-w-0 flex-1 rounded-lg border border-line bg-panel px-2 text-[12.5px] outline-none focus:border-accent">
                    <option value="">{t("pos.occ.pick")}</option>
                    {assignable.map((a) => <option key={a.id} value={a.id}>{a.position_id ? t("pos.occ.moves", { name: a.name, position: posName(a.position_id) }) : a.name}</option>)}
                  </select>
                  <Btn kind="primary" data-testid="pos-assign" disabled={!pickAgent || busy} onClick={assign}>{t("pos.occ.assign")}</Btn>
                </div>
              ))}
            </>
          )}
        </section>
      </div>
    </Card>
  );
}
