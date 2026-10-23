"use client";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { Btn, Loading } from "../ui";
import { Icon } from "../icons";
import { EmptyState, PageHeader } from "../nav/ui2";
import { meetingsApi, type CalEvent, type Meeting, type MeetingSettings, type RoomState } from "./api";
import { CalendarPanel } from "./CalendarPanel";
import { DebatesPanel } from "./DebatesPanel";
import { filterMeetings, type MeetingFilter } from "./logic";
import { MeetingsList } from "./MeetingsList";
import { RoomsBoard } from "./RoomsBoard";
import { RoomView } from "./RoomView";
import { ScheduleDialog, type Prefill } from "./ScheduleDialog";
import { ErrorBar, errText } from "./shared";

type Tab = "meetings" | "rooms" | "calendar" | "debates";
const FILTERS: MeetingFilter[] = ["upcoming", "live", "past", "all"];
const DAY = 24 * 3600 * 1000;

/**
 * Meetings: the list, the rooms of the office and the calendar settings. The URL carries the state
 * (?open=<id>&tab=minutes opens a meeting, ?new=1&title=...&agents=a,b opens the schedule dialog), so deep links
 * from the chat commands (/room, /schedule, /minutes) and the browser's Back button just work.
 */
export function MeetingsView() {
  const { t } = useT();
  const router = useRouter();
  const params = useSearchParams();
  const canWrite = useCan("requests:create");
  const [tab, setTab] = useState<Tab>(() => (params.get("view") === "debates" ? "debates" : "meetings")); // ?view=debates (from /debatir)
  const [filter, setFilter] = useState<MeetingFilter>("upcoming");
  const [mine, setMine] = useState(false);
  const [list, setList] = useState<Meeting[] | null>(null);
  const [rooms, setRooms] = useState<RoomState[]>([]);
  const [settings, setSettings] = useState<MeetingSettings | null>(null);
  const [calEvents, setCalEvents] = useState<CalEvent[]>([]);
  const [err, setErr] = useState("");
  const [dialog, setDialog] = useState<Prefill | null>(null);

  const open = params.get("open");
  const openTab = params.get("tab") === "minutes" ? "minutes" : "overview";

  const load = useCallback(async () => {
    const from = new Date(Date.now() - 30 * DAY).toISOString(), to = new Date(Date.now() + 120 * DAY).toISOString();
    try {
      const [ms, rs] = await Promise.all([meetingsApi.list({ from, to, mine }), meetingsApi.rooms().catch(() => [] as RoomState[])]);
      setList(ms); setRooms(rs); setErr("");
    } catch (e) { setList((l) => l ?? []); setErr(errText(e, t)); }
  }, [mine, t]);

  useEffect(() => { void load(); const i = setInterval(() => void load(), 30000); return () => clearInterval(i); }, [load]);
  useEffect(() => { void meetingsApi.settings().then(setSettings).catch(() => undefined); }, []);

  // read-only overlay of the connected calendar (best effort: nothing is shown when no calendar is connected)
  useEffect(() => {
    if (!settings?.calendar_connection_id && !settings?.free_busy_connection_id) { setCalEvents([]); return; }
    meetingsApi.calendarEvents(new Date(Date.now() - DAY).toISOString(), new Date(Date.now() + 30 * DAY).toISOString()).then(setCalEvents).catch(() => setCalEvents([]));
  }, [settings?.calendar_connection_id, settings?.free_busy_connection_id]);

  // ?new=1 opens the schedule dialog once (from the /schedule command)
  useEffect(() => {
    if (params.get("new") !== "1" || !canWrite) return;
    setDialog({ title: params.get("title") ?? "", agentIds: (params.get("agents") ?? "").split(",").filter(Boolean) });
    router.replace("/meetings");
  }, [params, canWrite, router]);

  const go = (q: string) => router.push(q ? `/meetings?${q}` : "/meetings");
  const shown = useMemo(() => filterMeetings(list ?? [], filter, new Date()), [list, filter]);
  const visibleEvents = useMemo(() => (filter === "upcoming" || filter === "all" ? calEvents.filter((e) => new Date(e.end).getTime() > Date.now()) : []), [calEvents, filter]);

  if (open) {
    return (
      <RoomView key={open} id={open} initialTab={openTab} settings={settings} rooms={rooms} onBack={() => go("")} onChanged={() => void load()} />
    );
  }

  const liveCount = (list ?? []).filter((m) => m.status === "live").length;
  return (
    <div data-testid="meetings-view" className="flex h-full min-h-0 flex-col bg-bg">
      <PageHeader testid="meetings-header" title={t("meetings.title")} description={t("meetings.subtitle")}
        actions={canWrite ? (
          <>
            <Btn data-testid="meetings-instant" onClick={() => setDialog({ instant: true })}>{t("meetings.action.instant")}</Btn>
            <Btn kind="primary" data-testid="meetings-new" className="inline-flex items-center gap-1" onClick={() => setDialog({})}><Icon name="plus" size={13} />{t("meetings.action.new")}</Btn>
          </>
        ) : undefined} />

      <div className="flex flex-wrap items-center gap-1 border-b border-line bg-panel px-5 py-2" role="tablist">
        {(["meetings", "rooms", "calendar", "debates"] as Tab[]).map((k) => (
          <button key={k} type="button" role="tab" aria-selected={tab === k} data-testid={`meetings-tab-${k}`} onClick={() => setTab(k)}
            className={`rounded-md px-3 py-1.5 text-xs font-medium ${tab === k ? "bg-accent-soft text-accent" : "text-mute hover:bg-panel2 hover:text-ink"}`}>
            {t(`meetings.tab.${k}`)}{k === "rooms" && liveCount > 0 ? <span className="ml-1.5 rounded-full bg-ok/15 px-1.5 text-[10px] text-ok">{liveCount}</span> : null}
          </button>
        ))}
        {tab === "meetings" && (
          <div className="ml-auto flex flex-wrap items-center gap-1.5">
            {FILTERS.map((f) => (
              <button key={f} type="button" aria-pressed={filter === f} data-testid={`meetings-filter-${f}`} onClick={() => setFilter(f)}
                className={`rounded-full border px-2.5 py-1 text-[11px] ${filter === f ? "border-accent bg-accent-soft text-accent" : "border-line-strong bg-panel text-ink2 hover:bg-panel2"}`}>{t(`meetings.filter.${f}`)}</button>
            ))}
            <label className="ml-1 flex items-center gap-1.5 text-[11px] text-ink2"><input type="checkbox" data-testid="meetings-mine" checked={mine} onChange={(e) => setMine(e.target.checked)} />{t("meetings.filter.mine")}</label>
          </div>
        )}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto p-5">
        <div className="mx-auto max-w-5xl space-y-3">
          <ErrorBar msg={err} />
          {tab === "meetings" && (
            list === null ? <Loading rows={4} /> :
            shown.length === 0 && visibleEvents.length === 0 ? (
              <EmptyState icon="users" testid="meetings-empty" title={t(`meetings.empty.${filter}.title`)} body={t("meetings.empty.body")}
                action={canWrite ? <Btn kind="primary" onClick={() => setDialog({})}>{t("meetings.action.new")}</Btn> : undefined} />
            ) : <MeetingsList meetings={shown} calEvents={visibleEvents} settings={settings} onOpen={(id) => go(`open=${encodeURIComponent(id)}`)} />
          )}
          {tab === "rooms" && <RoomsBoard rooms={rooms} settings={settings} onOpenMeeting={(id) => go(`open=${encodeURIComponent(id)}`)} onOpenRoom={(room) => setDialog({ instant: true, room })} />}
          {tab === "calendar" && <CalendarPanel settings={settings} onSettings={setSettings} />}
          {tab === "debates" && <DebatesPanel />}
        </div>
      </div>

      {dialog && (
        <ScheduleDialog prefill={dialog} settings={settings} rooms={rooms} onClose={() => setDialog(null)}
          onSaved={(m) => { setDialog(null); void load(); go(`open=${encodeURIComponent(m.id)}`); }} />
      )}
    </div>
  );
}
