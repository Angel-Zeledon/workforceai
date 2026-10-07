"use client";
import { useT } from "@/lib/i18n";
import { TONE_CODES, type ToneCode } from "@/lib/orgConfigApi";

/** Regional tone picker (neutral, MX, CO, AR, CL, ES). `allowInherit` adds "use the office tone". */
export function ToneSelect({ value, onChange, testid, allowInherit = false, label }: {
  value: ToneCode | ""; onChange: (v: ToneCode | "") => void; testid: string; allowInherit?: boolean; label?: string;
}) {
  const { t } = useT();
  return (
    <select data-testid={testid} aria-label={label} value={value} onChange={(e) => onChange(e.target.value as ToneCode | "")}
      className="rounded-xl border border-line-strong bg-panel px-3 py-2 text-sm font-semibold outline-none focus:border-accent">
      {allowInherit && <option value="">{t("tone.inherit")}</option>}
      {TONE_CODES.map((c) => <option key={c} value={c}>{t(`tone.${c}`)}</option>)}
    </select>
  );
}
