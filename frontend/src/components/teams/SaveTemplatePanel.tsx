"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { ApiError } from "@/lib/api";
import { downloadTemplate, officeTemplatesApi } from "@/lib/officeTemplatesApi";
import { EXCLUDED_KEYS, fileText, includesOf, type OfficeTemplateExport } from "@/lib/officeTemplatesLogic";
import { Icon } from "../icons";

/** "Guardar como plantilla" (F3): before anything is downloaded, what the file includes and what it never includes,
 *  in plain words. The file is built by the server from an allow-list; here it is only shown and saved.
 *  `officeId` = one department ("general" = General); `company` = the whole company. */
export function SaveTemplatePanel({ officeId, company, officeName, onClose }: { officeId?: string; company?: boolean; officeName?: string; onClose?: () => void }) {
  const { t } = useT();
  const [ex, setEx] = useState<OfficeTemplateExport | null>(null);
  const [err, setErr] = useState("");
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    let alive = true;
    setEx(null); setErr(""); setSaved(false);
    officeTemplatesApi.exportFile({ officeId, company }).then((r) => { if (alive) setEx(r); }).catch((e) => {
      if (!alive) return;
      const s = e instanceof ApiError ? e.status : 0;
      setErr(t(s === 403 ? "otpl.save.forbidden" : s === 400 ? "otpl.save.empty" : "otpl.save.error"));
    });
    return () => { alive = false; };
  }, [officeId, company, t]);

  const save = () => {
    if (!ex) return;
    downloadTemplate(ex.file_name, fileText(ex.template));
    setSaved(true);
  };

  return (
    <section data-testid="otpl-save" aria-labelledby="otpl-save-title" className="rounded-[10px] border border-line bg-panel p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 id="otpl-save-title" className="font-display text-[15px] font-semibold text-ink">
            {company ? t("otpl.save.titleCompany") : t("otpl.save.title", { name: officeName ?? ex?.template.name ?? "" })}
          </h2>
          <p className="mt-1 text-[12.5px] leading-snug text-mute">{t("otpl.save.lead")}</p>
        </div>
        {onClose && <button type="button" data-testid="otpl-save-close" aria-label={t("otpl.close")} onClick={onClose} className="rounded-md p-1 text-mute hover:bg-panel2 hover:text-ink"><Icon name="x" size={14} /></button>}
      </div>
      {err && <p role="alert" data-testid="otpl-save-error" className="mt-3 text-[13px] font-semibold text-red-600">{err}</p>}
      {!err && !ex && <p className="mt-3 text-[12.5px] text-mute">{t("otpl.save.loading")}</p>}
      {ex && (
        <div className="mt-3 grid gap-3 sm:grid-cols-2">
          <div className="rounded-[10px] border border-line bg-panel2 p-3">
            <h3 className="text-[12px] font-semibold uppercase tracking-wide text-accent">{t("otpl.save.includes")}</h3>
            <ul data-testid="otpl-includes" className="mt-2 grid gap-1 text-[13px] text-ink">
              {includesOf(ex.template).map((l) => <li key={l.key} className="flex gap-2"><Icon name="check" size={13} className="mt-0.5 shrink-0 text-accent" />{t(l.key, { count: l.count })}</li>)}
            </ul>
          </div>
          <div className="rounded-[10px] border border-line bg-panel2 p-3">
            <h3 className="text-[12px] font-semibold uppercase tracking-wide text-ink2">{t("otpl.save.excludes")}</h3>
            <ul data-testid="otpl-excludes" className="mt-2 grid gap-1 text-[13px] text-ink2">
              {EXCLUDED_KEYS.map((k) => <li key={k} className="flex gap-2"><Icon name="x" size={12} className="mt-1 shrink-0 text-mute" />{t(k)}</li>)}
            </ul>
          </div>
          {ex.left_out.length > 0 && (
            <div data-testid="otpl-left-out" className="sm:col-span-2 rounded-[10px] border border-line p-3 text-[12.5px] text-ink2">
              <p className="font-semibold text-ink">{t("otpl.save.leftOut")}</p>
              <ul className="mt-1 list-disc pl-5">{ex.left_out.map((n, i) => <li key={i}>{n}</li>)}</ul>
            </div>
          )}
          <p className="sm:col-span-2 text-[12px] text-mute">{t("otpl.save.trust")}</p>
          <div className="sm:col-span-2 flex flex-wrap items-center gap-2">
            <button type="button" data-testid="otpl-download" onClick={save}
              className="min-h-10 rounded-md bg-accent px-5 py-2 text-xs font-semibold text-white hover:bg-accent-hover sm:min-h-0">{t("otpl.save.download")}</button>
            {saved && <span role="status" data-testid="otpl-saved" className="text-[12.5px] font-semibold text-ink">{t("otpl.save.done", { file: ex.file_name })}</span>}
          </div>
        </div>
      )}
    </section>
  );
}
