"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { useOfficeLayout } from "@/lib/officeLayout";
import { officesApi } from "@/lib/officesApi";
import { layoutChoices, type OfficeView } from "@/lib/officesLogic";

/** E2: which office's 3D layout is shown. Only appears when the company has more than one office; private offices
 *  the person is not in are listed locked (closed door). */
export function OfficePicker() {
  const { t } = useT();
  const [offices, setOffices] = useState<OfficeView[] | null>(null);
  const officeId = useOfficeLayout((s) => s.officeId);
  const editing = useOfficeLayout((s) => s.editing);

  useEffect(() => {
    let alive = true;
    officesApi.list().then((r) => { if (alive) setOffices(r.offices); }).catch(() => { /* older backend: one office */ });
    return () => { alive = false; };
  }, []);

  const choices = offices ? layoutChoices(offices) : null;
  if (!choices || editing) return null;
  const pick = (id: string) => {
    const o = offices!.find((x) => x.id === id);
    useOfficeLayout.setState({ officeEditable: !o || o.general || o.can_manage });
    void useOfficeLayout.getState().setOffice(id);
  };
  return (
    <label className="pointer-events-auto flex min-h-10 items-center gap-2 rounded-lg border border-line bg-panel/95 px-3 py-1.5 text-xs font-semibold text-ink shadow-soft backdrop-blur">
      <span className="text-mute">{t("offices.layout.pick")}</span>
      <select data-testid="office-picker" value={officeId || "general"} onChange={(e) => pick(e.target.value)}
        className="rounded-md border border-line bg-panel px-2 py-1 text-xs font-semibold text-ink outline-none focus:border-accent">
        {choices.map((c) => (
          <option key={c.id} value={c.id} disabled={c.locked}>{c.locked ? t("offices.layout.locked", { name: c.name }) : c.id === "general" ? t("offices.general") : c.name}</option>
        ))}
      </select>
    </label>
  );
}
