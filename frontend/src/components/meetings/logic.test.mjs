// Unit tests of the meetings logic (run: node --test src/components/meetings/logic.test.mjs).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  dayKey, durationLabel, extractWhen, feedUrl, filterMeetings, fromLocalInput, googleSubscribeUrl, groupByDay, isNotesFile, nextQuarter, overlaps, parseEmails, phaseOf, pickRoom, toLocalInput, webcalUrl,
} from "./logic.ts";

const now = new Date(2026, 9, 20, 14, 10); // 20 Oct 2026, 14:10 local

test("extractWhen understands day words, ISO dates and times at the end of a title", () => {
  let r = extractWhen("revisar precios mañana 10:00", now);
  assert.equal(r.title, "revisar precios");
  assert.equal(toLocalInput(r.start), "2026-10-21T10:00");
  r = extractWhen("review pricing tomorrow at 3pm", now);
  assert.equal(r.title, "review pricing");
  assert.equal(toLocalInput(r.start), "2026-10-21T15:00");
  r = extractWhen("kickoff 2026-11-02 09:30", now);
  assert.equal(r.title, "kickoff");
  assert.equal(toLocalInput(r.start), "2026-11-02T09:30");
  r = extractWhen("sync hoje às 16:45", now);
  assert.equal(r.title, "sync");
  assert.equal(toLocalInput(r.start), "2026-10-20T16:45");
  r = extractWhen("standup 9:00 am", now); // already past today -> the next one
  assert.equal(toLocalInput(r.start), "2026-10-21T09:00");
  r = extractWhen("planning amanhã", now);
  assert.equal(toLocalInput(r.start), "2026-10-21T09:00", "a day without a time defaults to 09:00");
});

test("extractWhen leaves ordinary titles alone", () => {
  for (const t of ["Q4 plan", "Revisión de 2026 objetivos", "Call with Acme", "Budget 25"]) {
    const r = extractWhen(t, now);
    assert.equal(r.start, null, t);
    assert.equal(r.title, t);
  }
  assert.equal(extractWhen("bad 25:99", now).start, null);
});

test("local input round trip and quarter rounding", () => {
  assert.equal(toLocalInput(new Date(fromLocalInput("2026-10-20T15:30"))), "2026-10-20T15:30");
  assert.equal(fromLocalInput("nope"), "");
  assert.equal(toLocalInput(nextQuarter(new Date(2026, 9, 20, 14, 10))), "2026-10-20T14:15");
  assert.equal(toLocalInput(nextQuarter(new Date(2026, 9, 20, 14, 15))), "2026-10-20T14:30");
  assert.equal(toLocalInput(nextQuarter(new Date(2026, 9, 20, 14, 46))), "2026-10-20T15:00");
});

const iso = (h, m = 0, d = 20) => new Date(2026, 9, d, h, m).toISOString();
const mk = (id, status, sh, eh, extra = {}) => ({ id, status, start: iso(sh), end: iso(eh), ...extra });

test("phaseOf: soon, overdue, live, final states", () => {
  assert.equal(phaseOf(mk("a", "scheduled", 14, 15), now), "soon");
  assert.equal(phaseOf(mk("a", "scheduled", 17, 18), now), "scheduled");
  assert.equal(phaseOf(mk("a", "scheduled", 13, 14), now), "overdue");
  assert.equal(phaseOf(mk("a", "live", 13, 14), now), "live");
  assert.equal(phaseOf(mk("a", "ended", 13, 14), now), "ended");
  assert.equal(phaseOf(mk("a", "cancelled", 13, 14), now), "cancelled");
});

test("filterMeetings and groupByDay", () => {
  const list = [mk("past", "ended", 9, 10), mk("live", "live", 13, 15), mk("next", "scheduled", 16, 17), mk("tomorrow", "scheduled", 10, 11, {}), mk("stale", "scheduled", 8, 9), mk("x", "cancelled", 16, 17)];
  list[3].start = iso(10, 0, 21); list[3].end = iso(11, 0, 21);
  assert.deepEqual(filterMeetings(list, "live", now).map((m) => m.id), ["live"]);
  assert.deepEqual(filterMeetings(list, "upcoming", now).map((m) => m.id), ["live", "next", "tomorrow"]);
  assert.deepEqual(filterMeetings(list, "past", now).map((m) => m.id), ["past", "stale", "x"]);
  assert.equal(filterMeetings(list, "all", now).length, 6);
  const mine = [{ ...mk("m1", "scheduled", 16, 17), organizer: "ana" }, { ...mk("m2", "scheduled", 16, 17), attendees: [{ kind: "person", id: "ana" }] }, { ...mk("m3", "scheduled", 16, 17), attendees: [{ kind: "agent", id: "ana" }] }];
  assert.deepEqual(filterMeetings(mine, "all", now, "ana").map((m) => m.id), ["m1", "m2"]);
  const g = groupByDay(list);
  assert.deepEqual(g.map((x) => x.day), ["2026-10-20", "2026-10-21"]);
  assert.deepEqual(g[0].items.map((m) => m.id), ["stale", "past", "live", "next", "x"]);
  assert.equal(dayKey(iso(23, 30)), "2026-10-20");
});

test("durations and overlap", () => {
  assert.equal(durationLabel(45), "45 min");
  assert.equal(durationLabel(60), "1 h");
  assert.equal(durationLabel(90), "1 h 30 min");
  assert.equal(overlaps(iso(9), iso(10), iso(10), iso(11)), false);
  assert.equal(overlaps(iso(9), iso(10, 30), iso(10), iso(11)), true);
});

test("feed URLs", () => {
  const path = "/api/v1/calendar/feed/org.abc.ics";
  assert.equal(feedUrl(path, "/api/v1", "https://app.example.com/"), "https://app.example.com/api/v1/calendar/feed/org.abc.ics");
  assert.equal(feedUrl(path, "https://api.example.com/api/v1/", "https://app.example.com"), "https://api.example.com/api/v1/calendar/feed/org.abc.ics");
  assert.equal(webcalUrl("https://x.com/a.ics"), "webcal://x.com/a.ics");
  assert.equal(webcalUrl("http://x.com/a.ics"), "webcal://x.com/a.ics");
  assert.ok(googleSubscribeUrl("https://x.com/a.ics").startsWith("https://calendar.google.com/calendar/r?cid=webcal%3A%2F%2Fx.com"));
});

test("notes files, e-mails and rooms", () => {
  assert.ok(isNotesFile("call.VTT") && isNotesFile("notes.md") && !isNotesFile("deck.pptx") && !isNotesFile("scan.pdf"));
  const e = parseEmails("Ana@X.com, bob@y.org; bad@, ana@x.com\n  c@z.io");
  assert.deepEqual(e.valid, ["ana@x.com", "bob@y.org", "c@z.io"]);
  assert.deepEqual(e.invalid, ["bad@"]);
  const rooms = [{ id: "boardroom", capacity: 12 }, { id: "huddle", capacity: 4 }, { id: "focus", capacity: 2, current: { id: "m" } }];
  assert.equal(pickRoom(rooms, 3), "huddle");
  assert.equal(pickRoom(rooms, 6), "boardroom");
  assert.equal(pickRoom(rooms, 1), "huddle", "the busy room is skipped");
  assert.equal(pickRoom(rooms, 40), "");
});
