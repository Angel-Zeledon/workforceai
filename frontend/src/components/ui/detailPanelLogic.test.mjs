// Unit tests of the single side detail panel (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { detailHref, detailParam, historyStep, releaseAction, topPanel, trapIndex } from "./detailPanelLogic.ts";

test("detailParam reads the open item, with or without the leading ?", () => {
  assert.equal(detailParam("?tarea=n1&p=x", "tarea"), "n1");
  assert.equal(detailParam("tarea=n%201", "tarea"), "n 1");
  assert.equal(detailParam("", "tarea"), "");
  assert.equal(detailParam("?cliente=", "cliente"), "");
});

test("detailHref opens, switches and closes without touching other parameters", () => {
  assert.equal(detailHref("/projects", "?tab=plan", "tarea", "n1"), "/projects?tab=plan&tarea=n1");
  assert.equal(detailHref("/projects", "?tab=plan&tarea=n1", "tarea", "n2"), "/projects?tab=plan&tarea=n2");
  assert.equal(detailHref("/projects", "?tab=plan&tarea=n1", "tarea", null), "/projects?tab=plan");
  assert.equal(detailHref("/clients", "?cliente=c1", "cliente", null), "/clients");
  assert.equal(detailHref("/chats", "?conv=1", "archivo", "/A/b c.md", ["conv"]), "/chats?archivo=%2FA%2Fb+c.md");
  assert.equal(detailParam(detailHref("/x", "", "archivo", "/A/b c.md").split("?")[1], "archivo"), "/A/b c.md");
});

test("historyStep: open pushes, switching replaces, closing removes, same is nothing", () => {
  assert.equal(historyStep("", "n1"), "push");
  assert.equal(historyStep(null, "n1"), "push");
  assert.equal(historyStep("n1", "n2"), "replace");
  assert.equal(historyStep("n1", null), "remove");
  assert.equal(historyStep("n1", "n1"), "none");
  assert.equal(historyStep("", null), "none");
});

test("releaseAction: Back only on the entry the panel added, once", () => {
  assert.equal(releaseAction({ current: "n1", here: "/projects?tarea=n1", pushedHref: "/projects?tarea=n1" }), "back");
  assert.equal(releaseAction({ current: "n1", here: "/projects?tarea=n1&tab=x", pushedHref: "/projects?tarea=n1" }), "strip");
  assert.equal(releaseAction({ current: "n1", here: "/projects?tarea=n1" }), "strip"); // opened from a link
  assert.equal(releaseAction({ current: "", here: "/projects", pushedHref: "/projects?tarea=n1" }), "none");
  assert.equal(releaseAction({ current: "n1", here: "/projects?tarea=n1", pushedHref: "/projects?tarea=n1", backPending: true }), "none");
});

test("topPanel: the file viewer above the rest, then the last opened", () => {
  assert.equal(topPanel([]), null);
  assert.equal(topPanel([{ id: "pedido", layer: 1, seq: 1 }, { id: "tarea", layer: 1, seq: 2 }]), "tarea");
  assert.equal(topPanel([{ id: "file", layer: 2, seq: 1 }, { id: "pedido", layer: 1, seq: 5 }]), "file");
});

test("trapIndex cycles at both ends and leaves the middle to the browser", () => {
  assert.equal(trapIndex(0, -1, false), -1);
  assert.equal(trapIndex(3, 2, false), 0);
  assert.equal(trapIndex(3, 0, true), 2);
  assert.equal(trapIndex(3, 1, false), -1);
  assert.equal(trapIndex(3, -1, false), 0);
  assert.equal(trapIndex(3, -1, true), 2);
});
