"use client";
import { useT } from "@/lib/i18n";
import { connOffice, officeChoices } from "@/lib/connections/officeLogic";
import { Field, inputCls } from "./shared";
import { useConnOffices } from "./useConnOffices";

/** D3: which department a connection belongs to. Hidden while the organization has no departments. */
export function OfficeField({ value, onChange, isAdmin, disabled }: { value: string; onChange: (id: string) => void; isAdmin: boolean; disabled?: boolean }) {
  const { t } = useT();
  const { offices, departments } = useConnOffices();
  if (departments.length === 0) return null;
  const choices = officeChoices(offices, isAdmin, value);
  return (
    <Field label={t("conn.office.label")} hint={t("conn.office.hint")}>
      <select data-testid="conn-office-select" className={inputCls} value={value} disabled={disabled || choices.length < 2} onChange={(e) => onChange(e.target.value)}>
        {choices.map((o) => <option key={o.id || "company"} value={o.id}>{o.id ? o.name : t("conn.office.company")}</option>)}
      </select>
    </Field>
  );
}

/** D3: the department of a connection, as a small tag (nothing for the whole company). */
export function OfficeTag({ c, className = "" }: { c: { office_id?: string }; className?: string }) {
  const { t } = useT();
  const { name } = useConnOffices();
  const id = connOffice(c);
  if (!id) return null;
  return (
    <span data-testid="conn-office-tag" data-office={id} className={`${className} inline-flex max-w-full items-center truncate rounded-full border border-accent/30 bg-accent-soft px-2 py-[1px] text-[10.5px] font-semibold text-accent`}>
      {t("conn.office.tag", { name: name(id) })}
    </span>
  );
}
