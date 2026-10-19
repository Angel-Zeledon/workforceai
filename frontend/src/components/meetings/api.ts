/**
 * Meetings API client. Contract: backend/internal/api/meetings.go (docs/architecture/meetings.md).
 * In mock mode a small in-memory implementation answers, so the screens work in the offline demo.
 */
import { API_URL, MOCK } from "@/lib/config";
import { authFetch } from "@/lib/session";
import type { MeetingStatus } from "./logic";

export type Provider = "jitsi" | "meet" | "zoom" | "teams" | "custom" | "none";
export interface Attendee { kind: "person" | "agent" | "external"; id?: string; name: string; email?: string }
export interface AgendaItem { id?: string; title: string; minutes?: number; owner?: string; notes?: string }
export interface External { calendar_provider?: string; connection_id?: string; event_id?: string; sync_error?: string; brief_request_id?: string }
export interface Meeting {
  id: string; title: string; description: string; room: string; status: MeetingStatus; start: string; end: string; organizer: string;
  provider: Provider; join_url: string; attendees: Attendee[]; agenda: AgendaItem[]; external: External;
  started_at: string | null; ended_at: string | null; cancelled_at: string | null; created_at: string; updated_at: string;
  has_minutes: boolean; embeddable: boolean;
}
export interface ActionItem { text: string; owner?: string; owner_id?: string; owner_kind?: string; due?: string; task_key?: string }
export interface Minutes {
  meeting_id: string; summary: string; decisions: string[]; action_items: ActionItem[]; lang: string; source: "notes" | "transcript" | "upload";
  summary_source: "runtime" | "local"; truncated?: boolean; file_id?: string; file_path?: string; request_id?: string; created_by: string; created_at: string; updated_at: string;
}
export interface Room { id: string; name?: string; capacity: number }
export interface RoomState extends Room { current: { id: string; title: string; status: MeetingStatus; start: string; end: string } | null; next: { id: string; title: string; status: MeetingStatus; start: string; end: string } | null }
export interface MeetingSettings {
  default_provider: Provider; jitsi_base_url: string; calendar_connection_id: string; free_busy_connection_id: string; minutes_folder: string; rooms: Room[];
}
export interface CalendarConn { connection_id: string; provider: "google_calendar_sync" | "microsoft_calendar"; label: string; account: string; class: "read" | "write"; status: string; simulated: boolean }
export interface CalEvent { id: string; title: string; start: string; end: string; join_url?: string; attendees: number; all_day: boolean }
export interface Busy { start: string; end: string }
export interface FreeBusy { busy: Record<string, Busy[]>; slots: { start: string; end: string }[]; provider: string }
export interface Feed { id: string; user_id: string; label: string; created_at: string }
export interface CreateBody {
  title: string; description?: string; room?: string; start: string; end: string; provider?: Provider; join_url?: string;
  attendees?: Attendee[]; agenda?: AgendaItem[]; sync_calendar?: boolean; start_now?: boolean; brief_agents?: boolean; lang?: string;
}

/** Stable error codes of the API (the UI translates them: meetings.error.<code>). */
export class MeetingsError extends Error {
  constructor(public code: string, public status: number) { super(code); }
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await authFetch(`${API_URL}${path}`, {
    method, cache: "no-store",
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    let code = "generic";
    try { code = (await res.json()).code || code; } catch { /* body is not JSON */ }
    throw new MeetingsError(code, res.status);
  }
  const text = await res.text();
  return (text ? JSON.parse(text) : {}) as T;
}

// ---------------------------------------------------------------- mock (offline demo)
const mock = {
  seq: 1,
  meetings: [] as Meeting[],
  minutes: {} as Record<string, Minutes>,
  feeds: [] as Feed[],
  settings: { default_provider: "jitsi", jitsi_base_url: "https://meet.jit.si", calendar_connection_id: "", free_busy_connection_id: "", minutes_folder: "/Shared/Meetings",
    rooms: [{ id: "boardroom", capacity: 12 }, { id: "huddle", capacity: 4 }, { id: "focus", capacity: 2 }] } as MeetingSettings,
};
const nowIso = () => new Date().toISOString();
function mockFail(code: string, status = 409): never { throw new MeetingsError(code, status); }
function mockGet(id: string): Meeting { return mock.meetings.find((m) => m.id === id) ?? mockFail("not_found", 404); }
function mockExtract(notes: string): { decisions: string[]; actions: ActionItem[] } {
  const decisions: string[] = [], actions: ActionItem[] = [];
  let section = "";
  for (const raw of notes.split("\n")) {
    const l = raw.trim();
    if (!l) continue;
    if (/^(decisions?|decisiones|acuerdos|decis[õo]es):?$/i.test(l)) { section = "d"; continue; }
    if (/^(action items?|acciones|tareas|a[çc][õo]es|next steps):?$/i.test(l)) { section = "a"; continue; }
    const body = l.replace(/^[-*•]\s+/, "");
    if (/^(decision|decisión|acuerdo):/i.test(body)) decisions.push(body.replace(/^[^:]+:\s*/, ""));
    else if (/^(todo|action|acción):/i.test(body)) actions.push({ text: body.replace(/^[^:]+:\s*/, "") });
    else if (section === "d" && /^[-*•]/.test(l)) decisions.push(body);
    else if (section === "a" && /^[-*•]/.test(l)) {
      const m = /^([\p{L}][\p{L} ]{0,30}):\s+(.*)$/u.exec(body);
      actions.push(m ? { text: m[2], owner: m[1] } : { text: body });
    }
  }
  return { decisions, actions };
}

async function mockCall<T>(method: string, path: string, body: any): Promise<T> {
  const [p, qs] = path.split("?");
  const q = new URLSearchParams(qs ?? "");
  const seg = p.replace(/^\/meetings\/?/, "").split("/").filter(Boolean);
  const out = (v: unknown) => JSON.parse(JSON.stringify(v ?? {})) as T;
  if (seg.length === 0) {
    if (method === "GET") {
      const st = (q.get("status") ?? "").split(",").filter(Boolean);
      return out(mock.meetings.filter((m) => !st.length || st.includes(m.status)).sort((a, b) => a.start.localeCompare(b.start)));
    }
    const room = body.room ?? "";
    if (room && mock.meetings.some((m) => m.room === room && (m.status === "scheduled" || m.status === "live") && new Date(m.start) < new Date(body.end) && new Date(body.start) < new Date(m.end))) mockFail("room_busy");
    const id = `mt_demo${mock.seq++}`;
    const provider: Provider = body.provider ?? "jitsi";
    const m: Meeting = { id, title: body.title, description: body.description ?? "", room, status: "scheduled", start: body.start, end: body.end, organizer: "user", provider,
      join_url: provider === "jitsi" ? `${mock.settings.jitsi_base_url}/${id}` : body.join_url ?? "", attendees: body.attendees ?? [],
      agenda: (body.agenda ?? []).map((a: AgendaItem, i: number) => ({ ...a, id: `ag${i + 1}` })), external: {}, started_at: null, ended_at: null, cancelled_at: null,
      created_at: nowIso(), updated_at: nowIso(), has_minutes: false, embeddable: provider === "jitsi" };
    mock.meetings.push(m);
    if (body.start_now) { m.status = "live"; m.started_at = nowIso(); }
    return out(m);
  }
  if (seg[0] === "rooms") {
    const now = Date.now();
    return out(mock.settings.rooms.map((r) => ({
      ...r,
      current: mock.meetings.find((m) => m.room === r.id && (m.status === "live" || (m.status === "scheduled" && new Date(m.start).getTime() <= now && new Date(m.end).getTime() > now))) ?? null,
      next: mock.meetings.find((m) => m.room === r.id && m.status === "scheduled" && new Date(m.start).getTime() > now) ?? null,
    })));
  }
  if (seg[0] === "settings") { if (method === "PUT") Object.assign(mock.settings, body); return out(mock.settings); }
  if (seg[0] === "calendars") return out([]);
  if (seg[0] === "calendar-events") return out([]);
  if (seg[0] === "free-busy") mockFail("not_available", 501);
  if (seg[0] === "feeds") {
    if (method === "GET") return out(mock.feeds);
    if (method === "POST") {
      const f: Feed = { id: `cf_${mock.seq++}`, user_id: "user", label: body?.label ?? "", created_at: nowIso() };
      mock.feeds.push(f);
      return out({ feed: f, token: `demo.${f.id}`, path: `/api/v1/calendar/feed/demo.${f.id}.ics` });
    }
    mock.feeds = mock.feeds.filter((f) => f.id !== seg[1]);
    return out({});
  }
  const m = mockGet(seg[0]);
  const act = seg[1] ?? "";
  if (!act) {
    if (method === "PATCH") {
      if (m.status !== "scheduled") mockFail("invalid_state");
      Object.assign(m, body, { updated_at: nowIso() });
    }
    return out(m);
  }
  if (act === "start") { if (m.status !== "scheduled") mockFail("invalid_state"); m.status = "live"; m.started_at = nowIso(); return out(m); }
  if (act === "end") { if (m.status !== "live") mockFail("invalid_state"); m.status = "ended"; m.ended_at = nowIso(); return out(m); }
  if (act === "cancel") { if (m.status !== "scheduled") mockFail("invalid_state"); m.status = "cancelled"; m.cancelled_at = nowIso(); return out(m); }
  if (act === "brief") { if (m.external.brief_request_id) mockFail("invalid_state"); m.external.brief_request_id = "req_demo"; return out({ request_id: "req_demo" }); }
  if (act === "sync") mockFail("not_available", 501);
  if (act === "minutes" && seg[2] === "tasks") {
    const min = mock.minutes[m.id] ?? mockFail("not_found", 404);
    const pick: number[] = body?.indexes?.length ? body.indexes : min.action_items.map((_, i) => i).filter((i) => !min.action_items[i].task_key);
    if (!pick.length) mockFail("nothing_to_do", 422);
    pick.forEach((i, n) => { min.action_items[i].task_key = `a${n + 1}`; });
    min.request_id = "req_demo_tasks";
    return out({ minutes: min, request_id: min.request_id });
  }
  if (act === "minutes") {
    if (method === "GET") return out(mock.minutes[m.id] ?? mockFail("not_found", 404));
    if (method === "POST") {
      if (m.status !== "live" && m.status !== "ended") mockFail("invalid_state");
      const x = mockExtract(body.notes ?? "");
      const min: Minutes = { meeting_id: m.id, summary: (body.notes ?? "").split("\n").find((l: string) => l.trim() && !/:$/.test(l)) ?? "", decisions: x.decisions, action_items: x.actions,
        lang: body.lang ?? "en", source: body.source ?? "notes", summary_source: "local", created_by: "user", created_at: nowIso(), updated_at: nowIso(),
        file_path: `${mock.settings.minutes_folder}/${m.start.slice(0, 10)} ${m.title.toLowerCase().replace(/[^a-z0-9]+/g, "-")}.md` };
      mock.minutes[m.id] = min; m.has_minutes = true;
      return out(min);
    }
    const min = mock.minutes[m.id] ?? mockFail("not_found", 404);
    if (min.request_id) mockFail("invalid_state");
    if (body.summary !== undefined) min.summary = body.summary;
    if (body.decisions) min.decisions = body.decisions;
    if (body.action_items) min.action_items = body.action_items;
    return out(min);
  }
  mockFail("not_found", 404);
}

const go = <T>(method: string, path: string, body?: unknown): Promise<T> => (MOCK ? mockCall<T>(method, path, body) : call<T>(method, path, body));
const q = encodeURIComponent;

export const meetingsApi = {
  list: (o: { from?: string; to?: string; status?: string; mine?: boolean } = {}) => {
    const p = new URLSearchParams();
    if (o.from) p.set("from", o.from);
    if (o.to) p.set("to", o.to);
    if (o.status) p.set("status", o.status);
    if (o.mine) p.set("mine", "1");
    const s = p.toString();
    return go<Meeting[]>("GET", `/meetings${s ? `?${s}` : ""}`);
  },
  get: (id: string) => go<Meeting>("GET", `/meetings/${q(id)}`),
  create: (b: CreateBody) => go<Meeting>("POST", "/meetings", b),
  patch: (id: string, b: Partial<CreateBody>) => go<Meeting>("PATCH", `/meetings/${q(id)}`, b),
  start: (id: string, o: { brief_agents?: boolean; lang?: string } = {}) => go<Meeting>("POST", `/meetings/${q(id)}/start`, o),
  end: (id: string) => go<Meeting>("POST", `/meetings/${q(id)}/end`, {}),
  cancel: (id: string) => go<Meeting>("POST", `/meetings/${q(id)}/cancel`, {}),
  brief: (id: string, lang: string) => go<{ request_id: string }>("POST", `/meetings/${q(id)}/brief`, { lang }),
  sync: (id: string) => go<Meeting>("POST", `/meetings/${q(id)}/sync`, {}),
  rooms: () => go<RoomState[]>("GET", "/meetings/rooms"),
  settings: () => go<MeetingSettings>("GET", "/meetings/settings"),
  putSettings: (s: Partial<MeetingSettings>) => go<MeetingSettings>("PUT", "/meetings/settings", s),
  calendars: () => go<CalendarConn[]>("GET", "/meetings/calendars"),
  calendarEvents: (from: string, to: string) => go<CalEvent[]>("GET", `/meetings/calendar-events?from=${q(from)}&to=${q(to)}`),
  freeBusy: (b: { from: string; to: string; emails: string[]; duration_min: number; work_start_hour?: number; work_end_hour?: number; tz_offset_min?: number }) => go<FreeBusy>("POST", "/meetings/free-busy", b),
  feeds: () => go<Feed[]>("GET", "/meetings/feeds"),
  createFeed: (label: string) => go<{ feed: Feed; token: string; path: string }>("POST", "/meetings/feeds", { label }),
  revokeFeed: (id: string) => go<unknown>("DELETE", `/meetings/feeds/${q(id)}`),
  minutes: (id: string) => go<Minutes>("GET", `/meetings/${q(id)}/minutes`),
  generateMinutes: (id: string, b: { notes?: string; source?: string; lang: string; file_path?: string }) => go<Minutes>("POST", `/meetings/${q(id)}/minutes`, b),
  editMinutes: (id: string, b: { summary?: string; decisions?: string[]; action_items?: ActionItem[] }) => go<Minutes>("PUT", `/meetings/${q(id)}/minutes`, b),
  createTasks: (id: string, indexes: number[], lang: string) => go<{ minutes: Minutes; request_id: string }>("POST", `/meetings/${q(id)}/minutes/tasks`, { indexes, lang }),
  /** Downloads the .ics of one meeting through the authenticated API. */
  downloadIcs: async (id: string, filename: string) => {
    if (MOCK) return;
    const res = await authFetch(`${API_URL}/meetings/${q(id)}/ics`, { cache: "no-store" });
    if (!res.ok) throw new MeetingsError("generic", res.status);
    const url = URL.createObjectURL(await res.blob());
    const a = document.createElement("a");
    a.href = url; a.download = filename; document.body.appendChild(a); a.click(); a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  },
};
