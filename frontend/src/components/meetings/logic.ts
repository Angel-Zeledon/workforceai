// Pure helpers of the Meetings screens (no React, no imports: node --test runs them directly).

export type MeetingStatus = "scheduled" | "live" | "ended" | "cancelled";
export type MeetingFilter = "upcoming" | "live" | "past" | "all";

export interface MeetingLike { id: string; status: MeetingStatus; start: string; end: string; organizer?: string; attendees?: { kind: string; id?: string; email?: string }[] }

const pad = (n: number) => String(n).padStart(2, "0");
const fold = (s: string) => s.toLowerCase().normalize("NFD").replace(/[̀-ͯ]/g, "");

/** "2026-10-20T15:30" in the browser's time zone (value of <input type="datetime-local">). */
export function toLocalInput(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** The ISO instant of a datetime-local value (interpreted in the browser's time zone). */
export function fromLocalInput(v: string): string {
  const d = new Date(v);
  return isNaN(d.getTime()) ? "" : d.toISOString();
}

/** Rounds up to the next 15 minutes (the default start of a new meeting). */
export function nextQuarter(now: Date): Date {
  const d = new Date(now.getTime());
  d.setSeconds(0, 0);
  const m = d.getMinutes();
  d.setMinutes(m + ((15 - (m % 15)) % 15 || 15));
  return d;
}

const DAY_WORDS: Record<string, number> = { today: 0, hoy: 0, hoje: 0, tomorrow: 1, manana: 1, amanha: 1 };

/**
 * Takes a date and time typed at the end of a title ("review pricing tomorrow 10:00", "revisar precios 2026-10-21 15:30",
 * "sync today 3pm") out of the text. Returns the clean title and the start (local time) when something was understood.
 * Only a trailing expression counts, so a title like "Q4 plan" is left alone.
 */
export function extractWhen(text: string, now: Date): { title: string; start: Date | null } {
  const original = text.trim();
  let t = original;
  let day: Date | null = null;
  let hh: number | null = null;
  let mm = 0;

  // time: 10:30, 10:30pm, 3pm, 15h30
  const time = /(?:\s+(?:at|a las|a la|as|às|@))?\s+(\d{1,2})(?::|h)(\d{2})\s*(am|pm)?\s*$|(?:\s+(?:at|a las|a la|as|às|@))?\s+(\d{1,2})\s*(am|pm)\s*$/i.exec(t);
  if (time) {
    const h = parseInt(time[1] ?? time[4], 10);
    const m = time[2] ? parseInt(time[2], 10) : 0;
    const ap = (time[3] ?? time[5] ?? "").toLowerCase();
    let hour = h;
    if (ap === "pm" && h < 12) hour = h + 12;
    if (ap === "am" && h === 12) hour = 0;
    if (hour <= 23 && m <= 59) { hh = hour; mm = m; t = t.slice(0, time.index).trim(); }
  }
  // day: today / tomorrow words or an ISO date
  const iso = /(?:\s+|^)(\d{4})-(\d{2})-(\d{2})\s*$/.exec(t);
  const word = /(?:\s+|^)(today|hoy|hoje|tomorrow|ma[ñn]ana|amanh[ãa])\s*$/i.exec(t);
  if (iso) {
    const d = new Date(parseInt(iso[1], 10), parseInt(iso[2], 10) - 1, parseInt(iso[3], 10));
    if (!isNaN(d.getTime())) { day = d; t = t.slice(0, iso.index).trim(); }
  } else if (word) {
    day = new Date(now.getFullYear(), now.getMonth(), now.getDate() + (DAY_WORDS[fold(word[1])] ?? 0));
    t = t.slice(0, word.index).trim();
  }
  // "10:00 tomorrow" order: the day may also come first and the time last (handled above); try the reverse once
  if (hh !== null && !day) {
    const w2 = /(?:\s+|^)(today|hoy|hoje|tomorrow|ma[ñn]ana|amanh[ãa])\s*$/i.exec(t);
    if (w2) { day = new Date(now.getFullYear(), now.getMonth(), now.getDate() + (DAY_WORDS[fold(w2[1])] ?? 0)); t = t.slice(0, w2.index).trim(); }
  }
  if (hh === null && !day) return { title: original, start: null };
  const base = day ?? new Date(now.getFullYear(), now.getMonth(), now.getDate());
  let start = new Date(base.getFullYear(), base.getMonth(), base.getDate(), hh ?? 9, hh === null ? 0 : mm);
  if (!day && start.getTime() < now.getTime()) start = new Date(start.getTime() + 24 * 3600 * 1000); // a bare time means the next one
  return { title: t.replace(/[\s,:;-]+$/, ""), start };
}

/** Local day key "YYYY-MM-DD" of an instant. */
export function dayKey(iso: string): string {
  const d = new Date(iso);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** Meetings grouped by local day, days and meetings in time order. */
export function groupByDay<T extends { start: string; id: string }>(list: T[]): { day: string; items: T[] }[] {
  const by = new Map<string, T[]>();
  for (const m of [...list].sort((a, b) => a.start.localeCompare(b.start) || a.id.localeCompare(b.id))) {
    const k = dayKey(m.start);
    by.set(k, [...(by.get(k) ?? []), m]);
  }
  return [...by.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([day, items]) => ({ day, items }));
}

export type Phase = "live" | "soon" | "scheduled" | "overdue" | "ended" | "cancelled";

/** The state to show: "soon" = starts within 15 minutes, "overdue" = still scheduled 15 minutes after its start. */
export function phaseOf(m: Pick<MeetingLike, "status" | "start" | "end">, now: Date): Phase {
  if (m.status !== "scheduled") return m.status === "live" ? "live" : m.status;
  const start = new Date(m.start).getTime();
  const t = now.getTime();
  if (t > start + 15 * 60000) return "overdue";
  if (start - t <= 15 * 60000) return "soon";
  return "scheduled";
}

export function filterMeetings<T extends MeetingLike>(list: T[], f: MeetingFilter, now: Date, mine?: string): T[] {
  const t = now.getTime();
  return list.filter((m) => {
    if (mine && m.organizer !== mine && !(m.attendees ?? []).some((a) => a.kind !== "agent" && a.id === mine)) return false;
    switch (f) {
      case "live": return m.status === "live";
      case "upcoming": return m.status === "live" || (m.status === "scheduled" && new Date(m.end).getTime() >= t);
      case "past": return m.status === "ended" || m.status === "cancelled" || (m.status === "scheduled" && new Date(m.end).getTime() < t);
      default: return true;
    }
  });
}

export function durationMin(start: string, end: string): number {
  return Math.max(0, Math.round((new Date(end).getTime() - new Date(start).getTime()) / 60000));
}

/** "1 h 30 min", "45 min". */
export function durationLabel(min: number): string {
  if (min < 60) return `${min} min`;
  const h = Math.floor(min / 60), r = min % 60;
  return r ? `${h} h ${r} min` : `${h} h`;
}

export function overlaps(aStart: string, aEnd: string, bStart: string, bEnd: string): boolean {
  return new Date(aStart).getTime() < new Date(bEnd).getTime() && new Date(bStart).getTime() < new Date(aEnd).getTime();
}

/** The absolute URL of a feed path returned by the API ("/api/v1/calendar/feed/<token>.ics"). */
export function feedUrl(path: string, apiUrl: string, origin: string): string {
  const rest = path.replace(/^\/api\/v1/, "");
  const base = apiUrl.replace(/\/$/, "");
  if (/^https?:\/\//.test(base)) return base + rest;
  return origin.replace(/\/$/, "") + base + rest;
}

/** webcal:// variant, which opens the subscription dialog of desktop calendar apps. */
export function webcalUrl(httpUrl: string): string {
  return httpUrl.replace(/^https?:\/\//, "webcal://");
}

/** Google Calendar's "add by URL" deep link. */
export function googleSubscribeUrl(httpUrl: string): string {
  return `https://calendar.google.com/calendar/r?cid=${encodeURIComponent(webcalUrl(httpUrl))}`;
}

export const TEXT_FILE_RE = /\.(txt|md|markdown|vtt|srt|csv|log)$/i;

/** True when the browser can read the file as notes by itself (otherwise pick it from the drive). */
export function isNotesFile(name: string): boolean {
  return TEXT_FILE_RE.test(name);
}

/** Splits "a@x.com, b@y.org; c@z.io" into valid e-mail addresses (lower case, unique). */
export function parseEmails(text: string): { valid: string[]; invalid: string[] } {
  const valid: string[] = [], invalid: string[] = [];
  for (const raw of text.split(/[\s,;]+/)) {
    const e = raw.trim().toLowerCase();
    if (!e) continue;
    if (/^[^\s@<>,;]+@[^\s@<>,;]+\.[^\s@<>,;]{2,}$/.test(e)) { if (!valid.includes(e)) valid.push(e); } else invalid.push(raw.trim());
  }
  return { valid, invalid };
}

/** Rooms ordered for the picker: free ones first, then by capacity. */
export function pickRoom(rooms: { id: string; capacity: number; current?: unknown }[], headcount: number): string {
  const fits = rooms.filter((r) => !r.current && (r.capacity === 0 || r.capacity >= headcount)).sort((a, b) => (a.capacity || 999) - (b.capacity || 999));
  return fits[0]?.id ?? "";
}
