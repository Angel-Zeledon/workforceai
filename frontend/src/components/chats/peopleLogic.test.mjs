// Unit tests of the people chat logic: unread counters, mentions (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  applyIncoming, applyRead, badge, fromChannels, handleOf, insertMention, mentionQuery, splitMentions, suggest, totalMentions, totalUnread,
} from "./peopleLogic.ts";

const msg = (seq, over = {}) => ({ id: `m${seq}`, channel_id: "c1", seq, author_kind: "user", author_id: "bob", body: "hi", mentions: [], created_at: "", ...over });
const away = { activeId: null, focused: true };

test("incoming messages from others count as unread, once per sequence", () => {
  let s = {};
  s = applyIncoming(s, msg(1), "ana", away);
  s = applyIncoming(s, msg(2), "ana", away);
  s = applyIncoming(s, msg(2), "ana", away); // at-least-once delivery: duplicate
  assert.equal(s.c1.unread, 2);
  assert.equal(s.c1.lastSeq, 2);
});

test("my own messages and the open, focused channel never count", () => {
  let s = applyIncoming({}, msg(1), "bob", away);
  assert.equal(s.c1.unread, 0, "mine");
  s = applyIncoming(s, msg(2, { author_id: "carol" }), "ana", { activeId: "c1", focused: true });
  assert.equal(s.c1.unread, 0, "open and focused");
  s = applyIncoming(s, msg(3, { author_id: "carol" }), "ana", { activeId: "c1", focused: false });
  assert.equal(s.c1.unread, 1, "open but the window is in the background");
});

test("mentions are counted separately and only for me", () => {
  let s = applyIncoming({}, msg(1, { mentions: [{ kind: "user", id: "ana" }] }), "ana", away);
  s = applyIncoming(s, msg(2, { mentions: [{ kind: "user", id: "zed" }] }), "ana", away);
  s = applyIncoming(s, msg(3, { mentions: [{ kind: "agent", id: "ana" }] }), "ana", away);
  assert.equal(s.c1.unread, 3);
  assert.equal(s.c1.mentions, 1);
  assert.equal(totalMentions(s), 1);
});

test("reading clears the counters; reading an old sequence leaves the newer ones unread", () => {
  let s = fromChannels([{ id: "c1", last_seq: 10, unread: 4, mentions_unread: 1 }, { id: "c2", last_seq: 3, unread: 2, mentions_unread: 0 }]);
  assert.equal(totalUnread(s), 6);
  s = applyRead(s, "c1", 10);
  assert.deepEqual(s.c1, { unread: 0, mentions: 0, lastSeq: 10 });
  s = applyRead(s, "c2", 2); // read up to 2 of 3
  assert.equal(s.c2.unread, 1);
  assert.equal(totalUnread(s), 1);
  assert.equal(applyRead(s, "nope", 5), s, "unknown channel: unchanged");
});

test("badge text", () => {
  assert.equal(badge(0), "");
  assert.equal(badge(7), "7");
  assert.equal(badge(150), "99+");
});

test("handleOf folds accents and joins words like the backend", () => {
  assert.equal(handleOf("Ana García"), "ana.garcia");
  assert.equal(handleOf("  Sofía  Sales "), "sofia.sales");
  assert.equal(handleOf("José-Luis Ñandú"), "jose.luis.nandu");
});

test("mentionQuery finds the @word at the caret and ignores e-mail addresses", () => {
  assert.deepEqual(mentionQuery("hola @an", 8), { start: 5, query: "an" });
  assert.deepEqual(mentionQuery("@", 1), { start: 0, query: "" });
  assert.equal(mentionQuery("mail me at x@ana", 16), null);
  assert.equal(mentionQuery("hola @ana ya", 12), null, "the mention was finished by a space");
  assert.equal(mentionQuery("sin nada", 8), null);
});

const people = [
  { kind: "user", id: "u1", name: "Ana García" }, { kind: "user", id: "u2", name: "Bob Ruiz" },
  { kind: "agent", id: "sales", name: "Sofia Sales" }, { kind: "user", id: "u3", name: "Carolina Ana" },
];

test("suggest ranks prefixes first and matches any word of the name", () => {
  assert.deepEqual(suggest("an", people).map((m) => m.id), ["u1", "u3"]);
  assert.deepEqual(suggest("gar", people).map((m) => m.id), ["u1"]);
  assert.equal(suggest("", people).length, 4);
  assert.equal(suggest("zzz", people).length, 0);
  assert.equal(suggest("", people, 2).length, 2);
});

test("insertMention replaces the typed query with the handle", () => {
  const r = insertMention("hola @an y", 8, 5, people[0]);
  assert.equal(r.text, "hola @ana.garcia  y");
  assert.equal(r.caret, 5 + "@ana.garcia ".length);
});

test("splitMentions highlights resolved handles only", () => {
  const parts = splitMentions("hi @ana.garcia and @sofia.sales, ping @nobody a@b.com", people);
  const m = parts.filter((p) => p.mention).map((p) => p.mention.id);
  assert.deepEqual(m, ["u1", "sales"]);
  assert.equal(parts.map((p) => p.text).join(""), "hi @ana.garcia and @sofia.sales, ping @nobody a@b.com");
});
