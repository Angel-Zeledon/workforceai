"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import type { Customer, CustomerInput } from "@/lib/customersApi";
import { customerColor, validateCustomer } from "@/lib/customersLogic";
import { Btn } from "../ui";

const field = "w-full rounded-md border border-line bg-panel2 px-2.5 py-1.5 text-[16px] text-ink md:text-[13px]";

/** Create or edit a customer (name, optional tax id, color of its building, notes). */
export function CustomerForm({ initial, onSave, onCancel }: { initial?: Customer; onSave: (v: CustomerInput) => Promise<void>; onCancel: () => void }) {
  const { t } = useT();
  const [name, setName] = useState(initial?.name ?? "");
  const [taxId, setTaxId] = useState(initial?.tax_id ?? "");
  const [color, setColor] = useState(customerColor(initial?.color, initial?.id ?? "new"));
  const [notes, setNotes] = useState(initial?.notes ?? "");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const submit = async () => {
    const v = validateCustomer({ name, color });
    if (v) { setErr(t(`customers.form.error.${v}`)); return; }
    setBusy(true); setErr(null);
    try { await onSave({ name: name.trim(), tax_id: taxId.trim(), color, notes: notes.trim() }); }
    catch (e) { setErr(e instanceof Error && e.message ? e.message : t("customers.error.save")); }
    finally { setBusy(false); }
  };
  return (
    <form data-testid="customers-form" className="space-y-3" onSubmit={(e) => { e.preventDefault(); void submit(); }}>
      <label className="block text-[11px] font-medium text-mute">{t("customers.form.name")}
        <input data-testid="customers-form-name" className={`${field} mt-1`} value={name} maxLength={120} onChange={(e) => setName(e.target.value)} placeholder={t("customers.form.namePlaceholder")} />
      </label>
      <div className="grid gap-3 sm:grid-cols-[1fr_auto]">
        <label className="block text-[11px] font-medium text-mute">{t("customers.form.taxId")}
          <input data-testid="customers-form-tax" className={`${field} mt-1`} value={taxId} maxLength={40} onChange={(e) => setTaxId(e.target.value)} placeholder={t("customers.form.optional")} />
        </label>
        <label className="block text-[11px] font-medium text-mute">{t("customers.form.color")}
          <input data-testid="customers-form-color" type="color" className="mt-1 block h-[34px] w-16 cursor-pointer rounded-md border border-line bg-panel2" value={color} onChange={(e) => setColor(e.target.value)} />
        </label>
      </div>
      <label className="block text-[11px] font-medium text-mute">{t("customers.form.notes")}
        <textarea data-testid="customers-form-notes" className={`${field} mt-1 min-h-[64px]`} value={notes} maxLength={2000} onChange={(e) => setNotes(e.target.value)} placeholder={t("customers.form.optional")} />
      </label>
      {err && <p role="alert" className="text-[12px] text-err">{err}</p>}
      <div className="flex justify-end gap-2">
        <Btn onClick={onCancel}>{t("customers.form.cancel")}</Btn>
        <Btn data-testid="customers-form-save" kind="primary" type="submit" disabled={busy}>{initial ? t("customers.form.save") : t("customers.form.create")}</Btn>
      </div>
    </form>
  );
}
