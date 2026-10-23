// Unit tests of the router bridge (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { go, openInOffice, setNavigator } from "./navBridge.ts";

test("go uses the registered router", () => {
  const seen = [];
  setNavigator((href) => seen.push(href));
  try { go("/chats"); } finally { setNavigator(null); }
  assert.deepEqual(seen, ["/chats"]);
});

test("openInOffice selects the agent and then navigates to the office", () => {
  const calls = [];
  setNavigator((href) => calls.push(["nav", href]));
  try { openInOffice("sales", (id) => calls.push(["select", id])); } finally { setNavigator(null); }
  // Selecting first means the office opens already focused on the agent.
  assert.deepEqual(calls, [["select", "sales"], ["nav", "/"]]);
});
