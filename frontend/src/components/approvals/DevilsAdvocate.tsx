"use client";
import { useState } from "react";
import { api } from "@/lib/api";
import type { Counterargument } from "@/lib/types";
import { useT } from "@/lib/i18n";

/** Devil's advocate (B8): a collapsible second opinion that argues the opposite position of a high-risk approval.
 * Advisory only: it has no approve/reject controls and never blocks the card's own buttons. It is generated on
 * the first expand (the server caches it per approval, so opening it again costs nothing) and every failure is a
 * calm message: the approval can always be decided without it. */
export function DevilsAdvocate({ approvalId }: { approvalId: string }) {
  const { t, locale } = useT();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [data, setData] = useState<Counterargument | null>(null);
  const [err, setErr] = useState<"" | "unavailable" | "blocked" | "budget">("");

  const load = async () => {
    setBusy(true); setErr("");
    try {
      setData(await api.counterargument(approvalId, locale));
    } catch (e) {
      const status = /-> (\d+)/.exec(String((e as Error)?.message ?? ""))?.[1];
      setErr(status === "423" ? "blocked" : status === "402" ? "budget" : "unavailable");
    } finally { setBusy(false); }
  };
  const toggle = () => {
    const next = !open;
    setOpen(next);
    if (next && !data && !busy) void load();
  };

  return (
    <div data-testid={`devils-advocate-${approvalId}`} className="mt-2 rounded-md border border-line bg-panel text-[11px]">
      <button type="button" aria-expanded={open} data-testid={`devils-advocate-toggle-${approvalId}`} onClick={toggle}
        className="flex w-full items-center justify-between gap-2 px-2.5 py-1.5 text-left font-semibold text-ink2 hover:bg-panel2">
        <span>{t("approvals.devil.title")}</span>
        <span className="text-mute">{open ? t("common.hide") : t("common.show")}</span>
      </button>
      {open && (
        <div className="space-y-1.5 border-t border-line px-2.5 py-2 text-ink2" aria-live="polite">
          {busy && <p data-testid={`devils-advocate-loading-${approvalId}`} className="text-mute">{t("approvals.devil.loading")}</p>}
          {!busy && err && (
            <div data-testid={`devils-advocate-error-${approvalId}`} className="space-y-1">
              <p className="text-red-700">{t(`approvals.devil.error.${err}`)}</p>
              {err === "unavailable" && <button type="button" onClick={() => void load()} className="font-semibold text-accent underline">{t("approvals.devil.retry")}</button>}
            </div>
          )}
          {!busy && data && (
            <>
              <p className="text-mute">{t("approvals.devil.advisory")}</p>
              <p data-testid={`devils-advocate-text-${approvalId}`} className="break-words text-ink [overflow-wrap:anywhere]">{data.counterargument}</p>
              {data.risks.length > 0 && (
                <div>
                  <div className="font-semibold">{t("approvals.devil.risks")}</div>
                  <ul className="ml-4 list-disc">{data.risks.map((r, i) => <li key={i} className="break-words [overflow-wrap:anywhere]">{r}</li>)}</ul>
                </div>
              )}
              {data.checks.length > 0 && (
                <div>
                  <div className="font-semibold">{t("approvals.devil.checks")}</div>
                  <ul className="ml-4 list-disc">{data.checks.map((r, i) => <li key={i} className="break-words [overflow-wrap:anywhere]">{r}</li>)}</ul>
                </div>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}
