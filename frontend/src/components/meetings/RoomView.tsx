"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { Btn, Loading } from "../ui";
import { Icon } from "../icons";
import { meetingsApi, type Meeting, type MeetingSettings, type RoomState } from "./api";
import { durationLabel, durationMin, phaseOf } from "./logic";
import { MinutesView } from "./MinutesView";
import { ScheduleDialog } from "./ScheduleDialog";
import { Attendees, copyText, ErrorBar, errText, fmtRange, PhaseBadge, ProviderChip, Section, useNow, useRoomName } from "./shared";

type Tab = "overview" | "minutes";

/** One meeting: join the video (Jitsi inside the app, other providers in a new tab), run its lifecycle, agenda, invited agents and minutes. */
export function RoomView({ id, initialTab, settings, rooms, onBack, onChanged }: {
  id: string; initialTab?: Tab; settings: MeetingSettings | null; rooms: RoomState[]; onBack: () => void; onChanged: () => void;
}) {
  const { t, locale } = useT();
  const canWrite = useCan("requests:create");
  const now = useNow();
  const roomName = useRoomName(settings?.rooms);
  const [m, setM] = useState<Meeting | null | undefined>(undefined);
  const [tab, setTab] = useState<Tab>(initialTab ?? "overview");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [embed, setEmbed] = useState(false);
  const [edit, setEdit] = useState(false);
  const [brief, setBrief] = useState(true);
  const [copied, setCopied] = useState(false);
  const [confirmCancel, setConfirmCancel] = useState(false);

  const load = useCallback(async () => {
    try { setM(await meetingsApi.get(id)); setErr(""); } catch (e) { setM(null); setErr(errText(e, t)); }
  }, [id, t]);
  useEffect(() => { void load(); const i = setInterval(() => void load(), 20000); return () => clearInterval(i); }, [load]);

  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true); setErr("");
    try { await fn(); await load(); onChanged(); } catch (e) { setErr(errText(e, t)); } finally { setBusy(false); setConfirmCancel(false); }
  };

  if (m === undefined) return <div className="p-5"><Loading rows={3} /></div>;
  if (m === null) {
    return (
      <div className="p-5">
        <Btn onClick={onBack}>{t("meetings.back")}</Btn>
        <div className="mt-3"><ErrorBar msg={err || t("meetings.error.not_found")} /></div>
      </div>
    );
  }

  const phase = phaseOf(m, now);
  const agentsIn = m.attendees.filter((a) => a.kind === "agent");
  const open = m.status === "scheduled" || m.status === "live";
  const canJoin = open && !!m.join_url;

  const copy = async () => { if (await copyText(m.join_url)) { setCopied(true); setTimeout(() => setCopied(false), 1500); } };
  const syncOn = !!m.external.event_id;

  return (
    <div data-testid="room-view" data-status={m.status} className="flex h-full min-h-0 flex-col bg-bg">
      <div className="flex flex-wrap items-center gap-3 border-b border-line bg-panel px-5 py-3">
        <button type="button" data-testid="room-back" onClick={onBack} className="inline-flex items-center gap-1 text-[12px] font-medium text-mute hover:text-ink"><Icon name="back" size={14} />{t("meetings.back")}</button>
        <div className="min-w-0 flex-1">
          <h1 className="truncate font-display text-lg font-semibold text-ink" data-testid="room-title">{m.title}</h1>
          <p className="text-[12px] text-mute">{fmtRange(m.start, m.end)} · {durationLabel(durationMin(m.start, m.end))} · {roomName(m.room)}</p>
        </div>
        <PhaseBadge phase={phase} />
        <ProviderChip provider={m.provider} />
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto p-5">
        <div className="mx-auto max-w-5xl space-y-4">
          <ErrorBar msg={err} />
          {m.external.sync_error && <div data-testid="sync-error" className="rounded-lg bg-warn/10 px-3 py-2 text-xs text-warn">{t("meetings.sync.failed", { reason: t(`meetings.sync.code.${m.external.sync_error}`) })}</div>}

          <div className="flex flex-wrap items-center gap-2" role="tablist">
            {(["overview", "minutes"] as Tab[]).map((k) => (
              <button key={k} type="button" role="tab" aria-selected={tab === k} data-testid={`room-tab-${k}`} onClick={() => setTab(k)}
                className={`rounded-md px-3 py-1.5 text-xs font-medium ${tab === k ? "bg-accent-soft text-accent" : "text-mute hover:bg-panel2 hover:text-ink"}`}>
                {t(`meetings.tab.${k}`)}{k === "minutes" && m.has_minutes ? <span className="ml-1 text-ok">●</span> : null}
              </button>
            ))}
            <span className="flex-1" />
            {canWrite && m.status === "scheduled" && (
              <>
                {agentsIn.length > 0 && !m.external.brief_request_id && (
                  <label className="mr-1 flex items-center gap-1.5 text-[11px] text-ink2"><input type="checkbox" data-testid="start-brief" checked={brief} onChange={(e) => setBrief(e.target.checked)} />{t("meetings.start.brief")}</label>
                )}
                <Btn kind="primary" data-testid="room-start" disabled={busy} onClick={() => act(() => meetingsApi.start(m.id, { brief_agents: brief && agentsIn.length > 0 && !m.external.brief_request_id, lang: locale }))}>{t("meetings.action.start")}</Btn>
                <Btn data-testid="room-edit" disabled={busy} onClick={() => setEdit(true)}>{t("meetings.action.edit")}</Btn>
                {confirmCancel ? (
                  <><Btn kind="danger" data-testid="room-cancel-confirm" disabled={busy} onClick={() => act(() => meetingsApi.cancel(m.id))}>{t("meetings.action.confirmCancel")}</Btn><Btn onClick={() => setConfirmCancel(false)}>{t("meetings.action.keep")}</Btn></>
                ) : <Btn data-testid="room-cancel" disabled={busy} onClick={() => setConfirmCancel(true)}>{t("meetings.action.cancelMeeting")}</Btn>}
              </>
            )}
            {canWrite && m.status === "live" && <Btn kind="danger" data-testid="room-end" disabled={busy} onClick={() => act(() => meetingsApi.end(m.id))}>{t("meetings.action.end")}</Btn>}
          </div>

          {tab === "minutes" ? <MinutesView meeting={m} onChanged={() => { void load(); onChanged(); }} /> : (
            <div className="grid gap-4 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
              <div className="space-y-4">
                <Section title={t("meetings.video.title")}>
                  {!canJoin ? (
                    <p className="text-xs text-mute">{m.provider === "none" ? t("meetings.video.none") : open ? t("meetings.video.pending") : t("meetings.video.closed")}</p>
                  ) : (
                    <div className="space-y-2">
                      <div className="flex flex-wrap items-center gap-2">
                        <a data-testid="join-link" href={m.join_url} target="_blank" rel="noopener noreferrer"
                          className="inline-flex items-center gap-1.5 rounded-md border border-accent bg-accent px-3.5 py-1.5 text-xs font-medium text-white hover:bg-accent-hover">
                          <Icon name="play" size={12} />{t("meetings.video.open", { provider: t(`meetings.provider.${m.provider}`) })}
                        </a>
                        {m.embeddable && m.status === "live" && !embed && <Btn data-testid="join-embed" onClick={() => setEmbed(true)}>{t("meetings.video.joinHere")}</Btn>}
                        <Btn data-testid="copy-link" onClick={copy}>{copied ? t("meetings.action.copied") : t("meetings.action.copyLink")}</Btn>
                      </div>
                      <p className="break-all text-[11px] text-mute" data-testid="join-url">{m.join_url}</p>
                      {!m.embeddable && <p className="text-[11px] text-mute">{t("meetings.video.newTab")}</p>}
                      {embed && m.embeddable && (
                        <div className="overflow-hidden rounded-lg border border-line bg-black">
                          <iframe data-testid="room-embed" title={m.title} src={m.join_url} allow="camera; microphone; display-capture; fullscreen; autoplay; clipboard-write"
                            sandbox="allow-scripts allow-same-origin allow-popups allow-forms allow-modals" referrerPolicy="no-referrer" className="h-[480px] w-full" />
                        </div>
                      )}
                    </div>
                  )}
                </Section>

                <Section title={t("meetings.agenda.title")}>
                  {m.agenda.length === 0 ? <p className="text-xs text-mute">{t("meetings.agenda.none")}</p> : (
                    <ol data-testid="room-agenda" className="space-y-1.5 text-[13px]">
                      {m.agenda.map((a, i) => {
                        const owner = m.attendees.find((x) => x.id && x.id === a.owner);
                        return (
                          <li key={a.id ?? i} className="flex items-baseline gap-2">
                            <span className="w-4 text-right text-[11px] text-mute">{i + 1}</span>
                            <span className="flex-1 text-ink">{a.title}</span>
                            {owner && <span className="text-[11px] text-mute">{owner.name}</span>}
                            {!!a.minutes && <span className="text-[11px] text-mute">{a.minutes} min</span>}
                          </li>
                        );
                      })}
                    </ol>
                  )}
                </Section>

                {m.description && <Section title={t("meetings.field.description")}><p className="whitespace-pre-wrap text-[13px] text-ink">{m.description}</p></Section>}
              </div>

              <div className="space-y-4">
                <Section title={t("meetings.attendees.title", { n: m.attendees.length })}>
                  {m.attendees.length === 0 ? <p className="text-xs text-mute">{t("meetings.attendees.none")}</p> : <Attendees list={m.attendees} />}
                  {canWrite && open && agentsIn.length > 0 && (
                    <div className="mt-3 border-t border-line pt-3">
                      {m.external.brief_request_id ? <p data-testid="briefed" className="text-[11px] text-ok">{t("meetings.brief.done")}</p> : (
                        <>
                          <p className="mb-1.5 text-[11px] text-mute">{t("meetings.brief.hint")}</p>
                          <Btn data-testid="brief-agents" disabled={busy} onClick={() => act(() => meetingsApi.brief(m.id, locale))}>{t("meetings.brief.action")}</Btn>
                        </>
                      )}
                    </div>
                  )}
                </Section>

                <Section title={t("meetings.calendar.title")}>
                  <div className="space-y-2 text-xs text-ink2">
                    <div data-testid="sync-state">
                      {syncOn ? <span className="text-ok">{t("meetings.sync.on", { provider: t(`meetings.calendar.${m.external.calendar_provider ?? "google_calendar_sync"}`) })}</span> : <span className="text-mute">{t("meetings.sync.off")}</span>}
                    </div>
                    <div className="flex flex-wrap gap-2">
                      {canWrite && open && settings?.calendar_connection_id && (!syncOn || m.external.sync_error) && (
                        <Btn data-testid="sync-now" disabled={busy} onClick={() => act(() => meetingsApi.sync(m.id))}>{syncOn ? t("meetings.sync.retry") : t("meetings.sync.now")}</Btn>
                      )}
                      <Btn data-testid="download-ics" onClick={() => void meetingsApi.downloadIcs(m.id, "meeting.ics").catch((e) => setErr(errText(e, t)))}>{t("meetings.ics.download")}</Btn>
                    </div>
                  </div>
                </Section>

                <Section title={t("meetings.details.title")}>
                  <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
                    <dt className="text-mute">{t("meetings.field.room")}</dt><dd className="text-ink">{roomName(m.room)}</dd>
                    <dt className="text-mute">{t("meetings.details.organizer")}</dt><dd className="truncate text-ink">{m.organizer || "—"}</dd>
                    {m.started_at && <><dt className="text-mute">{t("meetings.details.started")}</dt><dd className="text-ink">{fmtRange(m.started_at, m.ended_at ?? m.started_at)}</dd></>}
                  </dl>
                </Section>
              </div>
            </div>
          )}
        </div>
      </div>

      {edit && (
        <ScheduleDialog meeting={m} settings={settings} rooms={rooms} onClose={() => setEdit(false)} onSaved={(saved) => { setEdit(false); setM(saved); onChanged(); }} />
      )}
    </div>
  );
}
