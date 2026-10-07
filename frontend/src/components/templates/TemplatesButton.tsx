"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { TemplateGallery } from "./TemplateGallery";

/** Header button that opens the workflow template gallery. */
export function TemplatesButton() {
  const { t } = useT();
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" data-testid="templates-open" onClick={() => setOpen(true)} title={t("tpl.open.title")}
        className="rounded-md border border-line px-3 py-1 font-semibold hover:text-ink">{t("tpl.open")}</button>
      {open && <TemplateGallery onClose={() => setOpen(false)} />}
    </>
  );
}
