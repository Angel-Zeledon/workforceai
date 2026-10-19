"use client";
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { API_URL } from "@/lib/config";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { Btn } from "../ui";
import { Icon } from "../icons";
import { meetingsApi, type CalendarConn, type Feed, type MeetingSettings, type Room } from "./api";
import { feedUrl, googleSubscribeUrl, webcalUrl } from "./logic";
import { copyText, ErrorBar, errText, inputCls, PROVIDERS, Section } from "./shared";

/** Calendar connections, video defaults, meeting rooms and the ICS subscription of the signed-in person. */
export function CalendarPanel({ settings, onSettings }: { settings: MeetingSettings | null; onSettings: (s: MeetingSettings) => void }) {
  const { t } = useT();
  const canManage = useCan("org:manage");
  const canSee = useCan("connections:read");
  const [conns, setConns] = useState<CalendarConn[] | null>(null);
  const [feeds, setFeeds] = useState<Feed[]>([]);
  const [fresh, setFresh] = useState<{ url: string } | null>(null);
  const [label, setLabel] = useState("");
  const [err, setErr] = useState("");
  const [saved, setSaved] = useState(false);
  const [draft, setDraft] = useState<MeetingSettings | null>(settings);
  const [copied, setCopied] = useState(false);

  useEffect(() => setDraft(settings), [settings]);
  const load = useCallback(async () => {
    try {
      setFeeds(await meetingsApi.feeds());
      setConns(canSee ? await meetingsApi.calendars() : []);
    } catch (e) { setErr(errText(e, t)); }
  }, [canSee, t]);
  useEffect(() => { void load(); }, [load]);

  const save = async () => {
    if (!draft) return;
    setErr(""); setSaved(false);
    try { const s = await meetingsApi.putSettings(draft); onSettings(s); setSaved(true); setTimeout(() => setSaved(false), 2000); } catch (e) { setErr(errText(e, t)); }
  };
  const setRoom = (i: number, patch: Partial<Room>) => draft && setDraft({ ...draft, rooms: draft.rooms.map((r, k) => (k === i ? { ...r, ...patch } : r)) });

  const newFeed = async () => {
    setErr("");
    try {
      const r = await meetingsApi.createFeed(label.trim());
      setFresh({ url: feedUrl(r.path, API_URL, window.location.origin) });
      setLabel("");
      await load();
    } catch (e) { setErr(errText(e, t)); }
  };
  const revoke = async (id: string) => {
    setErr("");
    try { await meetingsApi.revokeFeed(id); setFresh(null); await load(); } catch (e) { setErr(errText(e, t)); }
  };

  const writeConns = (conns ?? []).filter((c) => c.class === "write");
  return (
    <div className="space-y-4" data-testid="calendar-panel">
      <ErrorBar msg={err} />

      <Section title={t("meetings.cal.connections")}>
        <p className="mb-2 text-[12px] text-mute">{t("meetings.cal.connectionsHint")}</p>
        {conns && conns.length === 0 && <p className="mb-2 text-xs text-mute" data-testid="cal-none">{canSee ? t("meetings.cal.none") : t("meetings.cal.noPerm")}</p>}
        <ul className="space-y-1.5">
          {(conns ?? []).map((c) => (
            <li key={c.connection_id} data-testid="cal-conn" className="flex flex-wrap items-center gap-2 rounded-lg border border-line px-3 py-2 text-xs">
              <Icon name="plug" size={13} className="text-mute" />
              <span className="font-medium text-ink">{c.label}</span>
              <span className="text-mute">{t(`meetings.calendar.${c.provider}`)} · {c.account}</span>
              <span className="rounded bg-panel2 px-1.5 py-[1px] text-[10px] font-semibold uppercase text-ink2">{t(c.class === "write" ? "meetings.cal.canWrite" : "meetings.cal.freeBusyOnly")}</span>
              {c.simulated && <span className="rounded bg-warn/10 px-1.5 py-[1px] text-[10px] font-semibold uppercase text-warn">{t("meetings.cal.simulated")}</span>}
            </li>
          ))}
        </ul>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <Link href="/settings?s=connections" data-testid="cal-connect" className="inline-flex items-center gap-1 rounded-md border border-line-strong bg-panel px-3 py-1.5 text-xs font-medium text-ink hover:bg-panel2"><Icon name="plus" size={12} />{t("meetings.cal.connect")}</Link>
          <span className="text-[11px] text-mute">{t("meetings.cal.byoHint")}</span>
        </div>
        {draft && (
          <div className="mt-3 grid gap-3 sm:grid-cols-2">
            <label className="block text-xs"><span className="mb-1 block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("meetings.cal.writeTo")}</span>
              <select data-testid="cal-write-select" disabled={!canManage} value={draft.calendar_connection_id} onChange={(e) => setDraft({ ...draft, calendar_connection_id: e.target.value })} className={inputCls}>
                <option value="">{t("meetings.cal.off")}</option>
                {writeConns.map((c) => <option key={c.connection_id} value={c.connection_id}>{c.label} ({c.account})</option>)}
              </select></label>
            <label className="block text-xs"><span className="mb-1 block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("meetings.cal.freeBusyFrom")}</span>
              <select disabled={!canManage} value={draft.free_busy_connection_id} onChange={(e) => setDraft({ ...draft, free_busy_connection_id: e.target.value })} className={inputCls}>
                <option value="">{t("meetings.cal.sameAsWrite")}</option>
                {(conns ?? []).map((c) => <option key={c.connection_id} value={c.connection_id}>{c.label} ({c.account})</option>)}
              </select></label>
          </div>
        )}
      </Section>

      {draft && (
        <Section title={t("meetings.settings.title")}>
          {!canManage && <p className="mb-2 text-[11px] text-mute">{t("meetings.settings.adminOnly")}</p>}
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="block text-xs"><span className="mb-1 block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("meetings.settings.defaultProvider")}</span>
              <select data-testid="set-provider" disabled={!canManage} value={draft.default_provider} onChange={(e) => setDraft({ ...draft, default_provider: e.target.value as MeetingSettings["default_provider"] })} className={inputCls}>
                {PROVIDERS.map((p) => <option key={p} value={p}>{t(`meetings.provider.${p}`)}</option>)}
              </select></label>
            <label className="block text-xs"><span className="mb-1 block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("meetings.settings.jitsi")}</span>
              <input data-testid="set-jitsi" disabled={!canManage} value={draft.jitsi_base_url} onChange={(e) => setDraft({ ...draft, jitsi_base_url: e.target.value })} placeholder="https://meet.jit.si" className={inputCls} />
              <span className="mt-1 block text-[11px] text-mute">{t("meetings.settings.jitsiHint")}</span></label>
            <label className="block text-xs"><span className="mb-1 block text-[11px] font-semibold uppercase tracking-wide text-mute">{t("meetings.settings.folder")}</span>
              <input data-testid="set-folder" disabled={!canManage} value={draft.minutes_folder} onChange={(e) => setDraft({ ...draft, minutes_folder: e.target.value })} className={inputCls} /></label>
          </div>
          <div className="mt-3">
            <div className="mb-1 flex items-center justify-between">
              <span className="text-[11px] font-semibold uppercase tracking-wide text-mute">{t("meetings.settings.rooms")}</span>
              {canManage && <button type="button" onClick={() => setDraft({ ...draft, rooms: [...draft.rooms, { id: `room-${draft.rooms.length + 1}`, name: "", capacity: 6 }] })} className="text-[11px] font-medium text-accent hover:underline">{t("meetings.action.addItem")}</button>}
            </div>
            <div className="space-y-1.5">
              {draft.rooms.map((r, i) => (
                <div key={i} className="flex flex-wrap items-center gap-1.5">
                  <input disabled={!canManage} value={r.id} onChange={(e) => setRoom(i, { id: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, "") })} aria-label="id" className={`${inputCls} w-32 font-mono`} />
                  <input disabled={!canManage} value={r.name ?? ""} onChange={(e) => setRoom(i, { name: e.target.value })} placeholder={t("meetings.settings.roomName")} className={`${inputCls} min-w-[8rem] flex-1`} />
                  <input disabled={!canManage} type="number" min={0} max={500} value={r.capacity} onChange={(e) => setRoom(i, { capacity: parseInt(e.target.value, 10) || 0 })} aria-label={t("meetings.settings.capacity")} className={`${inputCls} w-20`} />
                  {canManage && <button type="button" aria-label={t("meetings.action.remove")} onClick={() => setDraft({ ...draft, rooms: draft.rooms.filter((_, k) => k !== i) })} className="text-mute hover:text-err"><Icon name="x" size={13} /></button>}
                </div>
              ))}
            </div>
          </div>
          {canManage && (
            <div className="mt-3 flex items-center justify-end gap-3">
              {saved && <span className="text-[11px] text-ok" role="status">{t("meetings.settings.saved")}</span>}
              <Btn kind="primary" data-testid="set-save" onClick={save}>{t("meetings.action.save")}</Btn>
            </div>
          )}
        </Section>
      )}

      <Section title={t("meetings.ics.title")}>
        <p className="mb-2 text-[12px] text-mute">{t("meetings.ics.hint")}</p>
        {fresh && (
          <div data-testid="feed-fresh" className="mb-3 rounded-lg border border-accent/40 bg-accent-soft p-3 text-xs">
            <p className="font-semibold text-ink">{t("meetings.ics.created")}</p>
            <p className="mt-1 break-all font-mono text-[11px] text-ink2" data-testid="feed-url">{fresh.url}</p>
            <div className="mt-2 flex flex-wrap gap-2">
              <Btn onClick={async () => { if (await copyText(fresh.url)) { setCopied(true); setTimeout(() => setCopied(false), 1500); } }}>{copied ? t("meetings.action.copied") : t("meetings.action.copyLink")}</Btn>
              <a href={googleSubscribeUrl(fresh.url)} target="_blank" rel="noopener noreferrer" className="rounded-md border border-line-strong bg-panel px-3.5 py-1.5 text-xs font-medium text-ink hover:bg-panel2">{t("meetings.ics.google")}</a>
              <a href={webcalUrl(fresh.url)} className="rounded-md border border-line-strong bg-panel px-3.5 py-1.5 text-xs font-medium text-ink hover:bg-panel2">{t("meetings.ics.apple")}</a>
            </div>
            <p className="mt-2 text-[11px] text-warn">{t("meetings.ics.once")}</p>
          </div>
        )}
        <ul className="space-y-1.5">
          {feeds.map((f) => (
            <li key={f.id} data-testid="feed-row" className="flex items-center gap-2 rounded-lg border border-line px-3 py-2 text-xs">
              <Icon name="clock" size={13} className="text-mute" />
              <span className="flex-1 truncate text-ink">{f.label || t("meetings.ics.unnamed")}</span>
              <span className="text-mute">{new Date(f.created_at).toLocaleDateString()}</span>
              <Btn kind="danger" data-testid="feed-revoke" onClick={() => revoke(f.id)}>{t("meetings.ics.revoke")}</Btn>
            </li>
          ))}
        </ul>
        <div className="mt-2 flex gap-2">
          <input data-testid="feed-label" value={label} maxLength={80} onChange={(e) => setLabel(e.target.value)} placeholder={t("meetings.ics.labelPh")} className={`${inputCls} max-w-xs`} />
          <Btn kind="primary" data-testid="feed-create" onClick={newFeed}>{t("meetings.ics.create")}</Btn>
        </div>
      </Section>
    </div>
  );
}
