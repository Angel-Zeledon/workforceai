"use client";
import { useEffect, useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import { Btn } from "../ui";
import { Icon } from "../icons";
import { Field, Modal } from "../connections/shared";
import { meetingsApi, type AgendaItem, type Attendee, type CreateBody, type Meeting, type MeetingSettings, type Provider, type RoomState } from "./api";
import { durationLabel, durationMin, extractWhen, fromLocalInput, nextQuarter, parseEmails, pickRoom, toLocalInput } from "./logic";
import { ErrorBar, errText, fmtDateTimeShort, inputCls, PROVIDERS, useAgentList, useRoomName } from "./shared";

export interface Prefill { title?: string; agentIds?: string[]; room?: string; instant?: boolean }

const DURATIONS = [15, 30, 45, 60, 90, 120];

/**
 * Schedule (or edit) a meeting: title, date and time in the browser's time zone, room, video provider, invited
 * people and agents, agenda, optional calendar sync, and a "find a time" helper that reads free/busy.
 * With `prefill.instant` it opens a room right now instead.
 */
export function ScheduleDialog({ meeting, prefill, settings, rooms, onClose, onSaved }: {
  meeting?: Meeting; prefill?: Prefill; settings: MeetingSettings | null; rooms: RoomState[]; onClose: () => void; onSaved: (m: Meeting) => void;
}) {
  const { t, locale } = useT();
  const agents = useAgentList();
  const roomName = useRoomName(settings?.rooms);
  const editing = !!meeting;
  const instant = !!prefill?.instant && !editing;

  const init = useMemo(() => {
    if (meeting) return { title: meeting.title, start: toLocalInput(new Date(meeting.start)), dur: durationMin(meeting.start, meeting.end) };
    const w = prefill?.title ? extractWhen(prefill.title, new Date()) : { title: "", start: null as Date | null };
    const start = w.start ?? nextQuarter(new Date());
    return { title: w.start ? w.title : prefill?.title ?? "", start: toLocalInput(instant ? new Date() : start), dur: 60 };
  }, [meeting, prefill, instant]);

  const [title, setTitle] = useState(init.title);
  const [description, setDescription] = useState(meeting?.description ?? "");
  const [start, setStart] = useState(init.start);
  const [dur, setDur] = useState(init.dur);
  const [room, setRoom] = useState(meeting?.room ?? prefill?.room ?? "");
  const [provider, setProvider] = useState<Provider>(meeting?.provider ?? settings?.default_provider ?? "jitsi");
  const [link, setLink] = useState(meeting && meeting.provider !== "jitsi" ? meeting.join_url : "");
  const [picked, setPicked] = useState<string[]>((meeting?.attendees ?? []).filter((a) => a.kind === "agent").map((a) => a.id!).concat(meeting ? [] : prefill?.agentIds ?? []));
  const [others, setOthers] = useState<Attendee[]>((meeting?.attendees ?? []).filter((a) => a.kind !== "agent"));
  const [emailText, setEmailText] = useState("");
  const [agenda, setAgenda] = useState<AgendaItem[]>(meeting?.agenda ?? []);
  const [sync, setSync] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const canSync = !!settings?.calendar_connection_id && !editing;
  const canFind = !!(settings?.free_busy_connection_id || settings?.calendar_connection_id);
  const needsLink = provider === "zoom" || provider === "custom" || ((provider === "meet" || provider === "teams") && !sync);
  const startIso = fromLocalInput(start);
  const endIso = startIso ? new Date(new Date(startIso).getTime() + dur * 60000).toISOString() : "";

  // A room that is taken now is not offered when opening a room right away.
  useEffect(() => {
    if (!instant || room || !rooms.length) return;
    setRoom(pickRoom(rooms.map((r) => ({ id: r.id, capacity: r.capacity, current: r.current })), picked.length + 1));
  }, [instant, room, rooms, picked.length]);

  const toggleAgent = (id: string) => setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id]));
  const addEmails = () => {
    const { valid, invalid } = parseEmails(emailText);
    if (invalid.length) { setErr(t("meetings.error.badEmail", { list: invalid.join(", ") })); return; }
    setErr("");
    setOthers((o) => [...o, ...valid.filter((e) => !o.some((x) => x.email === e)).map((e) => ({ kind: "external" as const, name: e, email: e }))]);
    setEmailText("");
  };

  const attendees: Attendee[] = [...picked.map((id) => ({ kind: "agent" as const, id, name: agents.find((a) => a.id === id)?.name ?? id })), ...others];
  const owners = attendees.filter((a) => a.id);

  const valid = title.trim() !== "" && !!startIso && (!needsLink || link.trim() !== "");

  const submit = async () => {
    if (!valid) return;
    setBusy(true); setErr("");
    try {
      const body: CreateBody = { title: title.trim(), description, room, start: startIso, end: endIso, provider, attendees,
        agenda: agenda.filter((a) => a.title.trim()).map(({ title: tt, minutes, owner, notes }) => ({ title: tt, minutes, owner: owner || undefined, notes })),
        ...(provider === "jitsi" || provider === "none" ? {} : link.trim() ? { join_url: link.trim() } : {}) };
      let saved: Meeting;
      if (editing) {
        // A Jitsi room keeps its link on edit (the API only generates a new one when the provider changes to Jitsi).
        saved = await meetingsApi.patch(meeting!.id, body);
      } else {
        saved = await meetingsApi.create({ ...body, sync_calendar: sync && canSync, start_now: instant, lang: locale });
      }
      onSaved(saved);
    } catch (e) { setErr(errText(e, t)); } finally { setBusy(false); }
  };

  // ---- free time finder ----
  const [finder, setFinder] = useState(false);
  const [slots, setSlots] = useState<{ start: string; end: string }[] | null>(null);
  const [finding, setFinding] = useState(false);
  const find = async () => {
    setFinding(true); setErr("");
    try {
      const from = new Date(Math.max(Date.now(), new Date(startIso || Date.now()).getTime()));
      const to = new Date(from.getTime() + 7 * 24 * 3600 * 1000);
      const r = await meetingsApi.freeBusy({ from: from.toISOString(), to: to.toISOString(), emails: others.map((o) => o.email!).filter(Boolean), duration_min: dur,
        tz_offset_min: -new Date().getTimezoneOffset() });
      setSlots(r.slots ?? []);
    } catch (e) { setErr(errText(e, t)); } finally { setFinding(false); }
  };

  return (
    <Modal onClose={onClose} testId="schedule-dialog" wide>
      <h2 className="font-display text-lg font-semibold text-ink">{editing ? t("meetings.dialog.editTitle") : instant ? t("meetings.dialog.instantTitle") : t("meetings.dialog.title")}</h2>
      <p className="mt-0.5 text-[12px] text-mute">{instant ? t("meetings.dialog.instantBody") : t("meetings.dialog.body")}</p>

      <div className="mt-4 space-y-3.5">
        <Field label={t("meetings.field.title")}>
          <input data-testid="meeting-title" autoFocus value={title} maxLength={200} onChange={(e) => setTitle(e.target.value)} placeholder={t("meetings.field.titlePh")} className={inputCls} />
        </Field>

        {!instant && (
          <div className="grid gap-3 sm:grid-cols-3">
            <Field label={t("meetings.field.start")}><input data-testid="meeting-start" type="datetime-local" value={start} onChange={(e) => setStart(e.target.value)} className={inputCls} /></Field>
            <Field label={t("meetings.field.duration")}>
              <select data-testid="meeting-duration" value={dur} onChange={(e) => setDur(parseInt(e.target.value, 10))} className={inputCls}>
                {(DURATIONS.includes(dur) ? DURATIONS : [...DURATIONS, dur].sort((a, b) => a - b)).map((d) => <option key={d} value={d}>{durationLabel(d)}</option>)}
              </select>
            </Field>
            <Field label={t("meetings.field.room")}>
              <select data-testid="meeting-room" value={room} onChange={(e) => setRoom(e.target.value)} className={inputCls}>
                <option value="">{t("meetings.room.none")}</option>
                {(settings?.rooms ?? []).map((r) => <option key={r.id} value={r.id}>{roomName(r.id)}{r.capacity ? ` (${r.capacity})` : ""}</option>)}
              </select>
            </Field>
          </div>
        )}
        {instant && (
          <Field label={t("meetings.field.room")}>
            <select data-testid="meeting-room" value={room} onChange={(e) => setRoom(e.target.value)} className={inputCls}>
              <option value="">{t("meetings.room.none")}</option>
              {rooms.map((r) => <option key={r.id} value={r.id} disabled={!!r.current}>{roomName(r.id)}{r.current ? ` - ${t("meetings.rooms.inUse")}` : ""}</option>)}
            </select>
          </Field>
        )}

        {!instant && canFind && (
          <div>
            <button type="button" data-testid="meeting-find" onClick={() => { setFinder((f) => !f); if (!finder && slots === null) void find(); }} className="inline-flex items-center gap-1 text-[12px] font-medium text-accent hover:underline">
              <Icon name="clock" size={13} />{t("meetings.finder.open")}
            </button>
            {finder && (
              <div className="mt-2 rounded-lg border border-line bg-panel2 p-3" data-testid="meeting-finder">
                <p className="text-[11px] text-mute">{t("meetings.finder.hint")}</p>
                {finding && <p className="mt-2 text-xs text-mute">{t("meetings.finder.loading")}</p>}
                {slots && slots.length === 0 && !finding && <p className="mt-2 text-xs text-mute">{t("meetings.finder.none")}</p>}
                {slots && slots.length > 0 && (
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {slots.map((s) => (
                      <button key={s.start} type="button" data-testid="meeting-slot" onClick={() => setStart(toLocalInput(new Date(s.start)))}
                        className="rounded-md border border-line-strong bg-panel px-2 py-1 text-[11px] text-ink hover:border-accent hover:text-accent">{fmtDateTimeShort(s.start)}</button>
                    ))}
                  </div>
                )}
                <button type="button" onClick={() => void find()} className="mt-2 text-[11px] text-mute underline">{t("meetings.finder.refresh")}</button>
              </div>
            )}
          </div>
        )}

        <div>
          <span className="mb-1 block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("meetings.field.provider")}</span>
          <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label={t("meetings.field.provider")}>
            {PROVIDERS.map((p) => (
              <button key={p} type="button" role="radio" aria-checked={provider === p} data-testid={`meeting-provider-${p}`} onClick={() => setProvider(p)}
                className={`rounded-md border px-2.5 py-1 text-[11px] font-medium ${provider === p ? "border-accent bg-accent-soft text-accent" : "border-line-strong bg-panel text-ink hover:bg-panel2"}`}>{t(`meetings.provider.${p}`)}</button>
            ))}
          </div>
          <p className="mt-1 text-[11px] text-mute">{t(`meetings.provider.${provider}.hint`)}</p>
          {(provider === "zoom" || provider === "custom" || provider === "meet" || provider === "teams") && (
            <input data-testid="meeting-link" value={link} onChange={(e) => setLink(e.target.value)} placeholder={t("meetings.field.linkPh")} className={`${inputCls} mt-1.5`} />
          )}
          {(provider === "meet" || provider === "teams") && canSync && (
            <p className="mt-1 text-[11px] text-mute">{t("meetings.field.linkFromCalendar")}</p>
          )}
        </div>

        <div>
          <span className="mb-1 block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("meetings.field.agents")}</span>
          <div className="flex flex-wrap gap-1.5">
            {agents.map((a) => (
              <button key={a.id} type="button" aria-pressed={picked.includes(a.id)} data-testid={`meeting-agent-${a.id}`} onClick={() => toggleAgent(a.id)}
                className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11px] ${picked.includes(a.id) ? "border-accent bg-accent-soft text-accent" : "border-line-strong bg-panel text-ink2 hover:bg-panel2"}`}>
                <span className="h-2 w-2 rounded-full" style={{ background: a.color }} />{a.name}
              </button>
            ))}
          </div>
        </div>

        <div>
          <span className="mb-1 block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("meetings.field.people")}</span>
          {others.length > 0 && (
            <div className="mb-1.5 flex flex-wrap gap-1.5">
              {others.map((o, i) => (
                <span key={`${o.email ?? o.name}${i}`} className="inline-flex items-center gap-1 rounded-full border border-line-strong bg-panel px-2 py-[2px] text-[11px]">
                  {o.name}
                  <button type="button" aria-label={t("meetings.action.remove")} onClick={() => setOthers((l) => l.filter((_, k) => k !== i))} className="text-mute hover:text-err"><Icon name="x" size={11} /></button>
                </span>
              ))}
            </div>
          )}
          <div className="flex gap-2">
            <input data-testid="meeting-emails" value={emailText} onChange={(e) => setEmailText(e.target.value)} onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); addEmails(); } }}
              placeholder={t("meetings.field.emailsPh")} className={inputCls} />
            <Btn onClick={addEmails} disabled={!emailText.trim()}>{t("meetings.action.add")}</Btn>
          </div>
        </div>

        <div>
          <div className="mb-1 flex items-center justify-between">
            <span className="text-[11px] font-semibold uppercase tracking-wide text-mute">{t("meetings.field.agenda")}</span>
            <button type="button" data-testid="agenda-add" onClick={() => setAgenda((a) => [...a, { title: "", minutes: 10 }])} className="inline-flex items-center gap-1 text-[11px] font-medium text-accent hover:underline"><Icon name="plus" size={12} />{t("meetings.action.addItem")}</button>
          </div>
          {agenda.length === 0 && <p className="text-[11px] text-mute">{t("meetings.field.agendaHint")}</p>}
          <div className="space-y-1.5">
            {agenda.map((a, i) => (
              <div key={i} className="flex flex-wrap items-center gap-1.5" data-testid="agenda-row">
                <span className="w-4 text-right text-[11px] text-mute">{i + 1}</span>
                <input value={a.title} maxLength={200} onChange={(e) => setAgenda((l) => l.map((x, k) => (k === i ? { ...x, title: e.target.value } : x)))} placeholder={t("meetings.field.agendaPh")} className={`${inputCls} min-w-[10rem] flex-1`} />
                <input type="number" min={0} max={480} value={a.minutes ?? 0} aria-label={t("meetings.field.minutes")} onChange={(e) => setAgenda((l) => l.map((x, k) => (k === i ? { ...x, minutes: parseInt(e.target.value, 10) || 0 } : x)))} className={`${inputCls} w-16`} />
                <select value={a.owner ?? ""} aria-label={t("meetings.field.owner")} onChange={(e) => setAgenda((l) => l.map((x, k) => (k === i ? { ...x, owner: e.target.value } : x)))} className={`${inputCls} w-36`}>
                  <option value="">{t("meetings.field.noOwner")}</option>
                  {owners.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}
                </select>
                <button type="button" aria-label={t("meetings.action.remove")} onClick={() => setAgenda((l) => l.filter((_, k) => k !== i))} className="text-mute hover:text-err"><Icon name="x" size={13} /></button>
              </div>
            ))}
          </div>
        </div>

        <Field label={t("meetings.field.description")}>
          <textarea value={description} maxLength={8000} rows={2} onChange={(e) => setDescription(e.target.value)} className={inputCls} />
        </Field>

        {canSync && !instant && (
          <label className="flex items-start gap-2 text-xs text-ink2">
            <input data-testid="meeting-sync" type="checkbox" checked={sync} onChange={(e) => setSync(e.target.checked)} className="mt-0.5" />
            <span>{t("meetings.field.sync")}<span className="block text-[11px] text-mute">{t("meetings.field.syncHint")}</span></span>
          </label>
        )}
        <ErrorBar msg={err} />
      </div>

      <div className="mt-5 flex items-center justify-between gap-2">
        <span className="text-[11px] text-mute">{!instant && endIso ? `${fmtDateTimeShort(startIso)} – ${fmtDateTimeShort(endIso)}` : ""}</span>
        <div className="flex gap-2">
          <Btn onClick={onClose}>{t("meetings.action.cancel")}</Btn>
          <Btn kind="primary" data-testid="meeting-save" disabled={!valid || busy} onClick={submit}>{busy ? t("meetings.action.saving") : editing ? t("meetings.action.save") : instant ? t("meetings.action.openRoom") : t("meetings.action.schedule")}</Btn>
        </div>
      </div>
    </Modal>
  );
}
