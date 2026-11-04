// Unit tests of the action names in words (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import fs from "node:fs";
import { actionIsUnnamed, actionLabel } from "./actionLabel.ts";

const cat = (l) => JSON.parse(fs.readFileSync(new URL(`./i18n/${l}.json`, import.meta.url), "utf8"));
const es = cat("es");
const look = (c) => (k) => c[k];

test("the codes seen in the audit read as words", () => {
  assert.equal(actionLabel("email.send", look(es)), "Enviar correo");
  assert.equal(actionLabel("draft_reply", look(es)), "Borrador de respuesta");
});

test("a project approval group keeps its own name", () => {
  assert.equal(actionLabel("send_contract", look(es)), es["pv.group.send_contract"]);
});

test("a tool prefix falls back to the verb", () => {
  assert.equal(actionLabel("gmail.send_reply", look(es)), es["action.send_reply"]);
});

test("an unknown code never shows the code: 'Acción del agente'", () => {
  assert.equal(actionLabel("zz.weird_thing", look(es)), es["action.unknown"]);
  assert.ok(actionIsUnnamed("zz.weird_thing", look(es)));
  assert.ok(!actionIsUnnamed("email.send", look(es)));
  assert.equal(actionLabel("", look(es)), es["action.unknown"]);
});

test("every action name exists in the three languages", () => {
  const keys = Object.keys(es).filter((k) => k.startsWith("action."));
  assert.ok(keys.length >= 10);
  for (const l of ["en", "pt"]) { const c = cat(l); for (const k of keys) assert.ok(c[k], `${l}: ${k}`); }
});
