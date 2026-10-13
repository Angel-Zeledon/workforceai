"use client";
import Link from "next/link";
import { useT } from "@/lib/i18n";
import { useProviderAlert } from "@/lib/store";

/** Banners about the AI provider: no balance left (red), or no allowed provider can see images
 * (amber, runtime code vision_unavailable): what happened and the way out. */
export function ProviderAlert() {
  const { t } = useT();
  const on = useProviderAlert((s) => s.noBalance);
  const noVision = useProviderAlert((s) => s.noVision);
  const dismiss = useProviderAlert((s) => s.dismiss);
  const dismissVision = useProviderAlert((s) => s.dismissVision);
  return (
    <>
      {on && (
        <div role="alert" data-testid="provider-no-balance" className="ac-pop flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-red-200 bg-red-50 px-4 py-2 text-[12.5px] text-red-800">
          <b className="font-semibold">{t("provider.noBalance.title")}</b>
          <span>{t("provider.noBalance.body")}</span>
          <Link href="/settings" className="font-semibold underline underline-offset-2">{t("provider.noBalance.cta")}</Link>
          <button type="button" onClick={dismiss} className="ml-auto rounded px-2 py-0.5 text-red-700 hover:bg-red-100" aria-label={t("hint.dismiss")}>✕</button>
        </div>
      )}
      {noVision && (
        <div role="status" data-testid="provider-no-vision" className="ac-pop flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-amber-200 bg-amber-50 px-4 py-2 text-[12.5px] text-amber-900">
          <b className="font-semibold">{t("provider.noVision.title")}</b>
          <span>{t("provider.noVision.body")}</span>
          <Link href="/settings" className="font-semibold underline underline-offset-2">{t("provider.noVision.cta")}</Link>
          <button type="button" onClick={dismissVision} className="ml-auto rounded px-2 py-0.5 text-amber-800 hover:bg-amber-100" aria-label={t("hint.dismiss")}>✕</button>
        </div>
      )}
    </>
  );
}
