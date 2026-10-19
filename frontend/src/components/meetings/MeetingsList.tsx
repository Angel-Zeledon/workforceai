"use client";
import { useT } from "@/lib/i18n";
import { Icon } from "../icons";
import type { CalEvent, Meeting, MeetingSettings } from "./api";
import { dayKey, groupByDay, phaseOf } from "./logic";
import { Attendees, fmtDayLong, fmtTimeShort, PhaseBadge, ProviderChip, useNow, useRoomName } from "./shared";

/** Meetings grouped by day. Events read from the connected calendar are shown dimmed between them (not meetings of the office). */
export function MeetingsList({ meetings, calEvents, settings, onOpen }: { meetings: Meeting[]; calEvents: CalEvent[]; settings: MeetingSettings | null; onOpen: (id: string) => void }) {
  const { t } = useT();
  const now = useNow();
  const roomName = useRoomName(settings?.rooms);
  const days = new Map<string, { meetings: Meeting[]; events: CalEvent[] }>();
  for (const g of groupByDay(meetings)) days.set(g.day, { meetings: g.items, events: [] });
  for (const e of calEvents) {
    const k = dayKey(e.start);
    const d = days.get(k) ?? { meetings: [], events: [] };
    d.events.push(e);
    days.set(k, d);
  }
  const keys = [...days.keys()].sort();
  return (
    <div data-testid="meetings-list" className="space-y-5">
      {keys.map((k) => {
        const d = days.get(k)!;
        return (
          <section key={k} data-testid="meetings-day">
            <h2 className="mb-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-mute">{fmtDayLong(k)}</h2>
            <ul className="space-y-2">
              {d.meetings.map((m) => {
                const phase = phaseOf(m, now);
                const joinable = (m.status === "live" || phase === "soon") && !!m.join_url;
                return (
                  <li key={m.id}>
                    <div data-testid="meeting-row" data-id={m.id} data-status={m.status}
                      className={`flex flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border bg-panel px-4 py-3 transition hover:border-line-strong hover:shadow-soft ${m.status === "live" ? "border-ok/50" : "border-line"} ${m.status === "cancelled" ? "opacity-60" : ""}`}>
                      <div className="w-24 shrink-0 text-xs tabular-nums text-ink2">{fmtTimeShort(m.start)}<span className="text-mute"> – {fmtTimeShort(m.end)}</span></div>
                      <button type="button" onClick={() => onOpen(m.id)} className="min-w-0 flex-1 text-left">
                        <span className={`block truncate text-[13.5px] font-semibold text-ink ${m.status === "cancelled" ? "line-through" : ""}`}>{m.title}</span>
                        <span className="mt-1 flex flex-wrap items-center gap-1.5">
                          <PhaseBadge phase={phase} testId="meeting-row-phase" />
                          <ProviderChip provider={m.provider} />
                          {m.room && <span className="text-[11px] text-mute">{roomName(m.room)}</span>}
                          {m.has_minutes && <span className="inline-flex items-center gap-0.5 text-[11px] text-ok"><Icon name="doc" size={11} />{t("meetings.list.minutes")}</span>}
                          {m.external.event_id && <span className="inline-flex items-center gap-0.5 text-[11px] text-mute"><Icon name="clock" size={11} />{t("meetings.list.synced")}</span>}
                        </span>
                      </button>
                      <div className="hidden max-w-[16rem] md:block"><Attendees list={m.attendees} max={4} /></div>
                      <div className="flex shrink-0 items-center gap-2">
                        {joinable && (
                          <a data-testid="meeting-join" href={m.join_url} target="_blank" rel="noopener noreferrer"
                            className="rounded-md border border-ok/40 bg-ok/10 px-3 py-1 text-xs font-medium text-ok hover:bg-ok/15">{t("meetings.action.join")}</a>
                        )}
                        <button type="button" aria-label={t("meetings.action.open")} onClick={() => onOpen(m.id)} className="text-mute hover:text-ink"><Icon name="chevron" size={16} /></button>
                      </div>
                    </div>
                  </li>
                );
              })}
              {d.events.map((e) => (
                <li key={e.id} data-testid="calendar-event" className="flex items-center gap-4 rounded-xl border border-dashed border-line px-4 py-2 text-xs text-mute">
                  <div className="w-24 shrink-0 tabular-nums">{e.all_day ? t("meetings.list.allDay") : `${fmtTimeShort(e.start)} – ${fmtTimeShort(e.end)}`}</div>
                  <span className="min-w-0 flex-1 truncate">{e.title || t("meetings.list.busy")}</span>
                  <span className="shrink-0 text-[10px] uppercase tracking-wide">{t("meetings.list.fromCalendar")}</span>
                </li>
              ))}
            </ul>
          </section>
        );
      })}
    </div>
  );
}
