// Unit tests of the edit-by-chat helpers (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { canUndoEdit, editCounts, pickEditAgent, withOpenFile } from "./fileEdit.ts";

const agents = {
  a1: { id: "a1", role: "sales", title: "Ventas" },
  a2: { id: "a2", role: "analyst", title: "Analista financiero" },
  a3: { id: "a3", role: "legal", title: "Legal" },
};
const order = ["a1", "a2", "a3"];

test("pickEditAgent: last agent that edited the file, else the relevant role, else the first", () => {
  const vs = [{ version: 1, author: { kind: "agent", id: "a3" } }, { version: 2, author: { kind: "human", id: "u" } }];
  assert.equal(pickEditAgent(vs, agents, order, "sheet"), "a3");
  assert.equal(pickEditAgent([{ version: 1, author: { kind: "human", id: "u" } }], agents, order, "sheet"), "a2");
  assert.equal(pickEditAgent([], agents, order, "code"), "a1");
  assert.equal(pickEditAgent([], {}, [], "sheet"), null);
  // an unknown author (deleted agent) is skipped
  assert.equal(pickEditAgent([{ version: 3, author: { kind: "agent", id: "gone" } }], agents, order, "code"), "a1");
});

test("canUndoEdit only while the edit is still the latest version", () => {
  assert.equal(canUndoEdit({ version: 4, from_version: 3 }, 4), true);
  assert.equal(canUndoEdit({ version: 4, from_version: 3 }, 5), false);
  assert.equal(canUndoEdit({ version: 4 }, 4), false);
  assert.equal(canUndoEdit({ version: 3, from_version: 3 }, 3), false);
});

test("withOpenFile adds the open file once and appends the hint", () => {
  const open = { path: "/Shared/a.md", name: "a.md", version: 2 };
  const r = withOpenFile("shorten it", [], open, "HINT");
  assert.equal(r.text, "shorten it\n\nHINT");
  assert.deepEqual(r.refs.map((x) => [x.path, x.version, x.action]), [["/Shared/a.md", 2, "attach"]]);
  const again = withOpenFile("x", r.refs, open, "HINT");
  assert.equal(again.refs.length, 1);
  assert.deepEqual(withOpenFile("x", [], null, "HINT"), { text: "x", refs: [] });
  assert.equal(withOpenFile("", [], open, "HINT").text, "HINT");
});

test("editCounts", () => assert.equal(editCounts(3, 1), "+3 -1"));
