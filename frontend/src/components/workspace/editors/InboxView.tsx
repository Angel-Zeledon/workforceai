"use client";
import { useState } from "react";
import type { StoredArtifact } from "@/lib/artifacts";
import { fmtDateTime, useT } from "@/lib/i18n";

interface Item { id: string; from: string; subject: string; snippet: string; received_at: string; source: string; triage?: { label: string; priority: string; suggested_action: string } }
interface InboxContent { schema: "aiw.inbox/1"; items: Item[] }

/** Read-only inbox: list + reading pane. Bodies render as plain text only (never HTML or remote images). */
export function InboxView({ art }: { art: StoredArtifact }) {
  const { t } = useT();
  const c = art.content as InboxContent | undefined;
  const [sel, setSel] = useState<string | null>(null);
  if (!c) return <div className="p-4 text-xs text-mute">…</div>;
  const cur = c.items.find((i) => i.id === sel) ?? c.items[0];
  return (
    <div data-testid="art-inbox" className="flex h-full min-h-0 bg-panel">
      <ul className="w-[46%] shrink-0 overflow-y-auto border-r border-line">
        {c.items.map((i) => (
          <li key={i.id}>
            <button type="button" onClick={() => setSel(i.id)} className={`block w-full border-b border-line px-3 py-2 text-left ${cur?.id === i.id ? "bg-accent/10" : "hover:bg-panel2"}`}>
              <div className="flex items-center justify-between gap-2"><span className="truncate text-[12px] font-semibold text-ink">{i.from}</span>{i.triage?.priority === "high" && <span className="rounded-full bg-red-500/15 px-1.5 text-[9px] font-semibold uppercase text-red-700">{t("inbox.high")}</span>}</div>
              <div className="truncate text-[12px] text-ink">{i.subject}</div>
              <div className="truncate text-[11px] text-mute">{i.snippet}</div>
            </button>
          </li>
        ))}
        {c.items.length === 0 && <li className="p-6 text-center text-xs text-mute">{t("inbox.empty")}</li>}
      </ul>
      <div className="min-w-0 flex-1 overflow-y-auto p-4">
        {cur ? (
          <>
            <h3 className="font-display text-base font-semibold text-ink">{cur.subject}</h3>
            <div className="text-[11px] text-mute">{cur.from} &middot; {fmtDateTime(cur.received_at)}</div>
            <p className="mt-3 whitespace-pre-wrap text-[13px] text-ink">{cur.snippet}</p>
            {cur.triage && (
              <div className="mt-4 rounded-xl border border-line bg-panel2/60 p-3 text-[12px] text-ink">
                <div className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-mute">{t("inbox.triage")}</div>
                <div>{t("inbox.label")}: <b>{cur.triage.label}</b></div>
                <div>{t("inbox.suggested")}: <b>{cur.triage.suggested_action}</b></div>
              </div>
            )}
          </>
        ) : null}
      </div>
    </div>
  );
}
