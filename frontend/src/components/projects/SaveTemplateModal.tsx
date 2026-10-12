"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { useProjects } from "@/lib/projects/store";
import { TEMPLATE_CATEGORIES } from "@/lib/projects/templateLogic";
import type { ProjectTemplate } from "@/lib/projects/types";
import { Btn } from "../ui";
import { Modal } from "./shared";
import { inputCls, labelCls } from "./templateUi";

/** "Save as template" of a project (draft or finished): name, description, category and who can see it. */
export function SaveTemplateModal({ defaultName, defaultDescription, onClose }: { defaultName: string; defaultDescription: string; onClose: () => void }) {
  const { t } = useT();
  const saveAsTemplate = useProjects((s) => s.saveAsTemplate);
  const openBuilder = useProjects((s) => s.openBuilder);
  const openLibrary = useProjects((s) => s.openLibrary);
  const [name, setName] = useState(defaultName);
  const [description, setDescription] = useState(defaultDescription);
  const [category, setCategory] = useState("general");
  const [shared, setShared] = useState(true);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [saved, setSaved] = useState<ProjectTemplate | null>(null);

  const confirm = async () => {
    if (busy || !name.trim()) return;
    setBusy(true); setErr("");
    try { setSaved(await saveAsTemplate({ name: name.trim(), description: description.trim(), category, shared })); }
    catch { setErr(t("tpl.save.error")); }
    setBusy(false);
  };

  if (saved) {
    return (
      <Modal onClose={onClose} testId="save-template-modal">
        <h2 className="font-display text-lg font-semibold text-ink">{t("tpl.save.doneTitle")}</h2>
        <p data-testid="save-template-done" className="mt-2 text-sm text-ink">{t("tpl.save.done", { name: saved.name })}</p>
        <p className="mt-1 text-[11px] text-mute">{t("tpl.save.doneHint")}</p>
        <div className="mt-4 flex flex-wrap justify-end gap-2">
          <Btn onClick={onClose}>{t("common.close")}</Btn>
          <Btn data-testid="save-template-open-library" onClick={() => { onClose(); openLibrary(); }}>{t("tpl.save.openLibrary")}</Btn>
          <Btn data-testid="save-template-open-builder" kind="primary" onClick={() => { onClose(); openBuilder(saved.id); }}>{t("tpl.save.openBuilder")}</Btn>
        </div>
      </Modal>
    );
  }
  return (
    <Modal onClose={onClose} testId="save-template-modal">
      <h2 className="font-display text-lg font-semibold text-ink">{t("tpl.save.title")}</h2>
      <p className="mt-0.5 text-[11px] text-mute">{t("tpl.save.subtitle")}</p>
      <div className="mt-3 space-y-3">
        <label className={labelCls}>{t("tpl.bld.name")}
          <input data-testid="save-template-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={120} className={`${inputCls} mt-1`} />
        </label>
        <label className={labelCls}>{t("tpl.bld.description")}
          <textarea data-testid="save-template-description" value={description} onChange={(e) => setDescription(e.target.value)} rows={3} className={`${inputCls} mt-1 resize-none`} />
        </label>
        <label className={labelCls}>{t("tpl.bld.category")}
          <select data-testid="save-template-category" value={category} onChange={(e) => setCategory(e.target.value)} className={`${inputCls} mt-1`}>
            {TEMPLATE_CATEGORIES.map((c) => <option key={c} value={c}>{t(`tpl.cat.${c}`)}</option>)}
          </select>
        </label>
        <label className="flex items-start gap-2 text-xs text-ink">
          <input data-testid="save-template-shared" type="checkbox" checked={shared} onChange={(e) => setShared(e.target.checked)} className="mt-0.5" />
          <span>{t("tpl.bld.shared")}<span className="block text-[10px] text-mute">{t("tpl.bld.sharedHint")}</span></span>
        </label>
        {err && <div className="text-[11px] text-red-700">{err}</div>}
      </div>
      <div className="mt-4 flex justify-end gap-2">
        <Btn onClick={onClose}>{t("common.close")}</Btn>
        <Btn data-testid="save-template-confirm" kind="primary" disabled={busy || !name.trim()} onClick={confirm}>{busy ? t("tpl.save.saving") : t("tpl.save.confirm")}</Btn>
      </div>
    </Modal>
  );
}
