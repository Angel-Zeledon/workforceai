// Unit tests of the agent panel's 5 tabs and the translation of old sections (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { AGENT_TABS, OLD_SECTIONS, TAB_SECTIONS, resolvePanelTarget } from "./panelTabs.ts";

test("five visible tabs, Chat first", () => {
  assert.deepEqual([...AGENT_TABS], ["chat", "work", "can", "knows", "profile"]);
});

test("no old section is lost: each of the 11 opens some tab", () => {
  for (const s of OLD_SECTIONS) assert.ok(resolvePanelTarget(s), s);
});

test("each old section lands where the proposal puts it", () => {
  const where = Object.fromEntries(OLD_SECTIONS.map((s) => [s, resolvePanelTarget(s).tab]));
  assert.deepEqual(where, {
    chat: "chat", state: "work", tasks: "work", reports: "work", activity: "work", files: "work", thoughts: "work", chats: "work",
    can: "can", memory: "knows", profile: "profile",
  });
});

test("the thought audit (the shield's link) and 'with other agents' open their folded block", () => {
  assert.deepEqual(resolvePanelTarget("thoughts"), { tab: "work", section: "thoughts", unfold: "thoughts" });
  assert.deepEqual(resolvePanelTarget("chats"), { tab: "work", section: "chats", unfold: "chats" });
  assert.equal(resolvePanelTarget("tasks").unfold, null);
});

test("the sections of Trabajo scroll to their place; old tabs that are new tabs open at the top", () => {
  assert.deepEqual(resolvePanelTarget("reports"), { tab: "work", section: "reports", unfold: null });
  assert.deepEqual(resolvePanelTarget("memory"), { tab: "knows", section: "memory", unfold: null });
  assert.deepEqual(resolvePanelTarget("can"), { tab: "can", section: null, unfold: null });
  assert.deepEqual(resolvePanelTarget("profile"), { tab: "profile", section: null, unfold: null });
});

test("new tab ids, inner sections and aliases work as link values, ignoring case and spaces", () => {
  assert.deepEqual(resolvePanelTarget(" Work "), { tab: "work", section: null, unfold: null });
  assert.equal(resolvePanelTarget("knowledge").tab, "knows");
  assert.equal(resolvePanelTarget("skills").tab, "knows");
  assert.equal(resolvePanelTarget("folders").tab, "can");
  assert.equal(resolvePanelTarget("audit").unfold, "thoughts");
  assert.equal(resolvePanelTarget("team").unfold, "chats");
});

test("unknown or empty values change nothing", () => {
  assert.equal(resolvePanelTarget(""), null);
  assert.equal(resolvePanelTarget(null), null);
  assert.equal(resolvePanelTarget(undefined), null);
  assert.equal(resolvePanelTarget("nope"), null);
});

test("every inner section belongs to exactly one tab", () => {
  const seen = new Map();
  for (const tab of AGENT_TABS) for (const s of TAB_SECTIONS[tab]) {
    assert.ok(!seen.has(s), `${s} in ${seen.get(s)} and ${tab}`);
    seen.set(s, tab);
  }
});
