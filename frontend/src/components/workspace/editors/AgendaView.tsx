"use client";
import { useMemo } from "react";
import type { StoredArtifact } from "@/lib/artifacts";
import { fmtTime, getLocale, useT } from "@/lib/i18n";

interface Ev { id: string; title: string; start: string; end: string; attendees?: string[]; location?: string; status: "proposed" | "confirmed"; source?: string }
interface AgendaContent { schema: "aiw.agenda/1"; events: Ev[] }

/** Read-only agenda grouped by day. Proposed events are dashed (accept/reject comes with proposals, phase 2). */
export function AgendaView({ art }: { art: StoredArtifact }) {
  const { t } = useT();
  const c = art.content as AgendaContent | undefined;
  const days = useMemo(() => {
    const m = new Map<string, Ev[]>();
    [...(c?.events ?? [])].sort((a, b) => +new Date(a.start) - +new Date(b.start)).forEach((e) => { const k = e.start.slice(0, 10); m.set(k, [...(m.get(k) ?? []), e]); });
    return [...m.entries()];
  }, [c]);
  if (!c) return <div className="p-4 text-xs text-mute">…</div>;
  const tag = getLocale() === "en" ? "en-US" : "es-MX";
  return (
    <div data-testid="art-agenda" className="h-full overflow-auto bg-panel p-4">
      {days.length === 0 && <div className="p-6 text-center text-xs text-mute">{t("agenda.empty")}</div>}
      {days.map(([day, evs]) => (
        <section key={day} className="mb-4">
          <h4 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wide text-mute">{new Intl.DateTimeFormat(tag, { weekday: "long", day: "numeric", month: "short" }).format(new Date(evs[0].start))}</h4>
          <ul className="space-y-2">
            {evs.map((e) => (
              <li key={e.id} className={`rounded-xl border bg-panel2/50 px-3 py-2 ${e.status === "proposed" ? "border-dashed border-amber-400" : "border-line"}`}>
                <div className="flex items-center justify-between gap-2">
                  <span className="text-[13px] font-semibold text-ink">{e.title}</span>
                  <span className="font-mono text-[11px] text-mute">{fmtTime(e.start).slice(0, 5)}&ndash;{fmtTime(e.end).slice(0, 5)}</span>
                </div>
                <div className="text-[11px] text-mute">{e.status === "proposed" ? t("agenda.proposed") : t("agenda.confirmed")}{e.attendees?.length ? ` · ${e.attendees.join(", ")}` : ""}</div>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}
