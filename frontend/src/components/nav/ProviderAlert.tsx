"use client";
import Link from "next/link";
import { useT } from "@/lib/i18n";
import { useProviderAlert } from "@/lib/store";

/** Banner when the AI provider account has no balance left: what happened and the two ways out. */
export function ProviderAlert() {
  const { t } = useT();
  const on = useProviderAlert((s) => s.noBalance);
  const dismiss = useProviderAlert((s) => s.dismiss);
  if (!on) return null;
  return (
    <div role="alert" data-testid="provider-no-balance" className="ac-pop flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-red-200 bg-red-50 px-4 py-2 text-[12.5px] text-red-800">
      <b className="font-semibold">{t("provider.noBalance.title")}</b>
      <span>{t("provider.noBalance.body")}</span>
      <Link href="/settings" className="font-semibold underline underline-offset-2">{t("provider.noBalance.cta")}</Link>
      <button type="button" onClick={dismiss} className="ml-auto rounded px-2 py-0.5 text-red-700 hover:bg-red-100" aria-label={t("hint.dismiss")}>✕</button>
    </div>
  );
}
