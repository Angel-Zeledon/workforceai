"use client";
import { useEffect } from "react";
import { useT } from "@/lib/i18n";

/** What a screen shows when it crashed (React error boundary): never a blank page. The error is logged to the console. */
export function ErrorScreen({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  const { t } = useT();
  useEffect(() => { console.error("[screen error]", error); }, [error]);
  return (
    <div role="alert" data-testid="error-screen" className="mx-auto flex max-w-md flex-col items-center gap-3 px-6 py-16 text-center">
      <h2 className="text-base font-semibold text-ink">{t("err.boundary.title")}</h2>
      <p className="text-sm text-ink2">{t("err.boundary.detail")}</p>
      {error.digest && <p className="text-[11px] text-mute">{t("err.ref", { ref: error.digest })}</p>}
      <div className="mt-1 flex gap-2">
        <button type="button" onClick={reset} className="rounded-[10px] bg-accent px-3.5 py-1.5 text-sm font-medium text-white hover:bg-accent-hover">{t("err.boundary.retry")}</button>
        {/* a full page load on purpose: after a crash the client router may be the broken part */}
        {/* eslint-disable-next-line @next/next/no-html-link-for-pages */}
        <a href="/" className="rounded-[10px] border border-line px-3.5 py-1.5 text-sm font-medium text-ink2 hover:border-accent hover:text-ink">{t("err.boundary.home")}</a>
      </div>
    </div>
  );
}
