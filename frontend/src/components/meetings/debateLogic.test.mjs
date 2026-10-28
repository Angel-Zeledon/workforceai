// Unit tests of the debates panel helpers (B5; run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { debateErrorCode, parseOptions } from "./debateLogic.ts";

test("parseOptions: one per line or ';', trimmed, no duplicates", () => {
  assert.deepEqual(parseOptions(" Subir el precio \n\nMantener;  Subir el precio ; Bajar"), ["Subir el precio", "Mantener", "Bajar"]);
  assert.deepEqual(parseOptions(""), []);
});

test("debateErrorCode maps the HTTP status to a stable code", () => {
  assert.equal(debateErrorCode(new Error("POST /debates -> 503")), "unavailable");
  assert.equal(debateErrorCode(new Error("POST /debates -> 423")), "blocked");
  assert.equal(debateErrorCode(new Error("POST /debates -> 402")), "budget_exceeded");
  assert.equal(debateErrorCode(new Error("POST /debates -> 400")), "invalid");
  assert.equal(debateErrorCode(new Error("network")), "generic");
});
