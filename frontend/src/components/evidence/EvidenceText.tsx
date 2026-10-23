"use client";
import { useMemo } from "react";
import { useT } from "@/lib/i18n";
import { linkFigures, type ReportCitation } from "@/lib/evidenceLogic";

/**
 * Text of a report with its traced figures as buttons (B6). Only figures that match a VERIFIED citation are
 * clickable; everything else stays plain text. Text is rendered as text, never as HTML.
 */
export function EvidenceText({ text, cites, onOpen }: { text: string; cites: ReportCitation[]; onOpen: (value: string, cites: ReportCitation[]) => void }) {
  const { t } = useT();
  const segs = useMemo(() => linkFigures(text, cites), [text, cites]);
  return (
    <>
      {segs.map((s, i) => s.cites ? (
        <button key={i} type="button" data-testid="evidence-figure" data-value={s.text}
          aria-label={t("evidence.openSource", { value: s.text })} title={t("evidence.openSource", { value: s.text })}
          onClick={() => onOpen(s.text, s.cites!)}
          className="rounded-sm px-0.5 font-semibold text-accent underline decoration-dotted decoration-2 underline-offset-2 hover:bg-accent/10 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent">
          {s.text}
        </button>
      ) : <span key={i}>{s.text}</span>)}
    </>
  );
}
