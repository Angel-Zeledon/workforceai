"use client";
import { useMemo } from "react";
import { useT } from "@/lib/i18n";
import { describeError, type ErrContext } from "@/lib/errors";

/**
 * The one way a failed call reads on screen: what is happening, what to do, a "Reintentar" when trying again can help,
 * and the reference to share when nothing more specific is known. Calm red (not alarming) on the app's white.
 * `err` may be anything a catch block holds; pass `null` for "no error" (renders nothing).
 */
export function ErrorNote({ err, onRetry, context, className = "" }: {
  err: unknown;
  onRetry?: () => void;
  context?: ErrContext;
  className?: string;
}) {
  const { t, locale } = useT();
  const d = useMemo(() => (err == null || err === "" ? null : describeError(err, context ?? {}, locale)), [err, context?.who, context?.fallbackKey, locale]); // eslint-disable-line react-hooks/exhaustive-deps
  if (!d) return null;
  return (
    <div role="alert" data-testid="error-note" className={`rounded-[10px] border border-err/25 bg-err/5 px-3 py-2 text-xs text-ink2 ${className}`}>
      <div className="font-medium text-err">{d.title}</div>
      {(d.detail || d.action) && <div className="mt-0.5">{[d.detail, d.action].filter(Boolean).join(" ")}</div>}
      {d.ref && <div className="mt-0.5 text-[11px] text-mute">{t("err.ref", { ref: d.ref })}</div>}
      {onRetry && d.retry && (
        <button type="button" onClick={onRetry} className="mt-1.5 rounded-md border border-accent/40 px-2 py-0.5 text-[11px] font-medium text-accent hover:bg-accent/5">{t("err.retry")}</button>
      )}
    </div>
  );
}
