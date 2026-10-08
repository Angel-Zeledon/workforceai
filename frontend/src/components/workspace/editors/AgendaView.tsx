"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { useArtifacts, type StoredArtifact } from "@/lib/artifacts";
import { fmtTime, getLocale, useT } from "@/lib/i18n";

interface Ev { id: string; title: string; start: string; end: string; attendees?: string[]; location?: string; status: "proposed" | "confirmed"; source?: string }
interface AgendaContent { schema: "aiw.agenda/1"; events: Ev[] }

const DAY_MS = 86_400_000;
const pad = (n: number) => String(n).padStart(2, "0");
/** ISO instant -> value of an <input type="datetime-local"> (local time). */
const toLocalInput = (iso: string) => { const d = new Date(iso); return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`; };
const fromLocalInput = (v: string) => { const d = new Date(v); return Number.isNaN(+d) ? null : d.toISOString(); };
const shiftDays = (iso: string, days: number) => new Date(+new Date(iso) + days * DAY_MS).toISOString();
const dayKey = (iso: string) => iso.slice(0, 10);

/**
 * Agenda grouped by day. Editable unless locked or read-only: change title, times and status; add or delete
 * events; move an event to another day by dragging it onto that day or with the day buttons / Alt+Left and
 * Alt+Right on a focused event. Proposed events (dashed) are confirmed by the person. Saves are versioned
 * (optimistic concurrency, merged per event id).
 */
export function AgendaView({ art, readOnly }: { art: StoredArtifact; readOnly?: boolean }) {
  const { t } = useT();
  const c = art.content as AgendaContent | undefined;
  const edit = useArtifacts((s) => s.edit);
  const root = useRef<HTMLDivElement>(null);
  const [dragId, setDragId] = useState<string | null>(null);
  const [overDay, setOverDay] = useState<string | null>(null);
  const [focusId, setFocusId] = useState<string | null>(null);
  const [announce, setAnnounce] = useState("");
  const locked = !!readOnly || art.locked;
  const days = useMemo(() => {
    const m = new Map<string, Ev[]>();
    [...(c?.events ?? [])].sort((a, b) => +new Date(a.start) - +new Date(b.start)).forEach((e) => { const k = dayKey(e.start); m.set(k, [...(m.get(k) ?? []), e]); });
    return [...m.entries()];
  }, [c]);

  useEffect(() => {
    if (!focusId) return;
    root.current?.querySelector<HTMLElement>(`[data-event="${CSS.escape(focusId)}"]`)?.focus();
    setFocusId(null);
  }, [focusId, c]);

  if (!c) return <div className="p-4 text-xs text-mute">…</div>;
  const tag = getLocale() === "en" ? "en-US" : "es-MX";
  const dayLabel = (iso: string) => new Intl.DateTimeFormat(tag, { weekday: "long", day: "numeric", month: "short" }).format(new Date(iso));
  const save = (events: Ev[]) => edit(art.id, { ...c, events });
  const patch = (id: string, p: Partial<Ev>) => save(c.events.map((e) => (e.id === id ? { ...e, ...p } : e)));
  const moveDays = (e: Ev, days: number) => {
    if (!days) return;
    patch(e.id, { start: shiftDays(e.start, days), end: shiftDays(e.end, days) });
    setFocusId(e.id);
    setAnnounce(t("agenda.moved", { title: e.title, day: dayLabel(shiftDays(e.start, days)) }));
  };
  const add = () => {
    const start = new Date(); start.setMinutes(0, 0, 0); start.setHours(start.getHours() + 1);
    const id = `ev${Date.now().toString(36)}${Math.random().toString(36).slice(2, 5)}`;
    save([...c.events, { id, title: t("agenda.newEvent"), start: start.toISOString(), end: new Date(+start + 3_600_000).toISOString(), status: "confirmed" }]);
    setFocusId(id);
  };
  const setStart = (e: Ev, v: string) => {
    const iso = fromLocalInput(v);
    if (iso) patch(e.id, { start: iso, end: new Date(+new Date(iso) + (+new Date(e.end) - +new Date(e.start))).toISOString() });
  };
  const setEnd = (e: Ev, v: string) => { const iso = fromLocalInput(v); if (iso && +new Date(iso) >= +new Date(e.start)) patch(e.id, { end: iso }); };
  const onKey = (ev: React.KeyboardEvent, e: Ev) => {
    if (locked || (ev.target as HTMLElement).tagName === "INPUT") return;
    if (ev.altKey && ev.key === "ArrowLeft") { ev.preventDefault(); moveDays(e, -1); }
    else if (ev.altKey && ev.key === "ArrowRight") { ev.preventDefault(); moveDays(e, 1); }
    else if (ev.key === "Delete") { ev.preventDefault(); save(c.events.filter((x) => x.id !== e.id)); setAnnounce(t("agenda.removed", { title: e.title })); }
  };
  const btn = "rounded border border-line px-1.5 text-[11px] text-mute hover:text-ink";
  const inp = "rounded border border-line bg-panel px-1.5 py-0.5 text-[11px] text-ink outline-none focus:border-accent";

  return (
    <div ref={root} data-testid="art-agenda" className="h-full overflow-auto bg-panel p-4">
      <p className="sr-only" role="status" aria-live="polite" data-testid="agenda-live">{announce}</p>
      {!locked && (
        <div className="mb-3 flex items-center gap-3">
          <button type="button" data-testid="agenda-add" onClick={add} className="rounded-md border border-accent bg-accent px-3 py-0.5 text-[11px] font-semibold text-white hover:bg-accent-hover">{t("agenda.add")}</button>
          <span className="text-[10.5px] text-mute">{t("agenda.keyboardHint")}</span>
        </div>
      )}
      {days.length === 0 && <div className="p-6 text-center text-xs text-mute">{t("agenda.empty")}</div>}
      {days.map(([day, evs]) => (
        <section
          key={day}
          data-testid={`agenda-day-${day}`}
          aria-label={dayLabel(evs[0].start)}
          onDragOver={(e) => { if (!locked && dragId) { e.preventDefault(); setOverDay(day); } }}
          onDragLeave={() => setOverDay((o) => (o === day ? null : o))}
          onDrop={(e) => {
            e.preventDefault();
            const ev = c.events.find((x) => x.id === dragId);
            if (!locked && ev) moveDays(ev, Math.round((Date.parse(`${day}T00:00:00Z`) - Date.parse(`${dayKey(ev.start)}T00:00:00Z`)) / DAY_MS));
            setDragId(null); setOverDay(null);
          }}
          className={`mb-4 rounded-xl p-1 ${overDay === day ? "ring-2 ring-accent" : ""}`}
        >
          <h4 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wide text-mute">{dayLabel(evs[0].start)}</h4>
          <ul className="space-y-2">
            {evs.map((e) => (
              <li
                key={e.id}
                data-event={e.id}
                data-testid="agenda-event"
                tabIndex={0}
                draggable={!locked}
                aria-label={`${e.title} · ${fmtTime(e.start).slice(0, 5)}`}
                onKeyDown={(ev) => onKey(ev, e)}
                onDragStart={(ev) => { if (locked) return; ev.dataTransfer.setData("text/plain", e.id); ev.dataTransfer.effectAllowed = "move"; setDragId(e.id); }}
                onDragEnd={() => { setDragId(null); setOverDay(null); }}
                className={`rounded-xl border bg-panel2/50 px-3 py-2 outline-none focus-visible:ring-2 focus-visible:ring-accent ${e.status === "proposed" ? "border-dashed border-amber-400" : "border-line"} ${dragId === e.id ? "opacity-40" : ""}`}
              >
                <div className="flex items-center justify-between gap-2">
                  {locked
                    ? <span className="text-[13px] font-semibold text-ink">{e.title}</span>
                    : <input data-testid="agenda-title" aria-label={t("agenda.eventTitle")} value={e.title} onChange={(ev) => patch(e.id, { title: ev.target.value })} className="min-w-0 flex-1 bg-transparent text-[13px] font-semibold text-ink outline-none" />}
                  <span className="font-mono text-[11px] text-mute">{fmtTime(e.start).slice(0, 5)}&ndash;{fmtTime(e.end).slice(0, 5)}</span>
                </div>
                <div className="text-[11px] text-mute">{e.status === "proposed" ? t("agenda.proposed") : t("agenda.confirmed")}{e.attendees?.length ? ` · ${e.attendees.join(", ")}` : ""}</div>
                {!locked && (
                  <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
                    <input type="datetime-local" data-testid="agenda-start" aria-label={t("agenda.start")} value={toLocalInput(e.start)} onChange={(ev) => setStart(e, ev.target.value)} className={inp} />
                    <input type="datetime-local" data-testid="agenda-end" aria-label={t("agenda.end")} value={toLocalInput(e.end)} onChange={(ev) => setEnd(e, ev.target.value)} className={inp} />
                    <button type="button" data-testid="agenda-earlier" aria-label={t("agenda.earlierDay")} onClick={() => moveDays(e, -1)} className={btn}>&larr;</button>
                    <button type="button" data-testid="agenda-later" aria-label={t("agenda.laterDay")} onClick={() => moveDays(e, 1)} className={btn}>&rarr;</button>
                    {e.status === "proposed" && <button type="button" data-testid="agenda-confirm" onClick={() => patch(e.id, { status: "confirmed" })} className={btn}>{t("agenda.confirm")}</button>}
                    <button type="button" data-testid="agenda-delete" aria-label={t("agenda.delete")} onClick={() => save(c.events.filter((x) => x.id !== e.id))} className={`${btn} ml-auto`}>&times;</button>
                  </div>
                )}
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}
