// Unit tests of the data inbox helpers (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { curlExamples, driveHref, fmtBytes, pushUrlOf, validateInbox } from "./dataInboxLogic.ts";

const ib = { push_url: "", push_path: "/api/v1/inbox/ibx_1" };

test("the push URL prefers the server's, then an absolute API URL, then the page origin", () => {
  assert.equal(pushUrlOf({ ...ib, push_url: "https://api.example.com/api/v1/inbox/ibx_1" }, "/api/v1", "https://x"), "https://api.example.com/api/v1/inbox/ibx_1");
  assert.equal(pushUrlOf(ib, "http://localhost:8080/api/v1/", "http://localhost:3000"), "http://localhost:8080/api/v1/inbox/ibx_1");
  assert.equal(pushUrlOf(ib, "/api/v1", "https://app.example.com/"), "https://app.example.com/api/v1/inbox/ibx_1");
});

test("curl examples carry the token only in the Authorization header", () => {
  const ex = curlExamples("https://a/api/v1/inbox/ibx_1", "aiwi_secret");
  for (const s of [ex.json, ex.csv, ex.file]) {
    assert.match(s, /-H "Authorization: Bearer aiwi_secret"/);
    assert.doesNotMatch(s, /\?token=|inbox\/ibx_1[^"]*aiwi_secret/);
  }
  assert.match(ex.json, /Content-Type: application\/json/);
  assert.match(ex.csv, /Content-Type: text\/csv/);
  assert.match(ex.file, /-F "file=@/);
});

test("form validation", () => {
  const ok = { name: "ERP", folder: "", allowed: ["json"], maxMb: 5 };
  assert.deepEqual(validateInbox(ok), []);
  assert.deepEqual(validateInbox({ ...ok, name: "  " }), ["name_required"]);
  assert.deepEqual(validateInbox({ ...ok, name: "x".repeat(81) }), ["name_too_long"]);
  assert.deepEqual(validateInbox({ ...ok, allowed: [] }), ["kinds_required"]);
  assert.deepEqual(validateInbox({ ...ok, maxMb: 26 }), ["max_size"]);
  assert.deepEqual(validateInbox({ ...ok, maxMb: 0 }), ["max_size"]);
  assert.deepEqual(validateInbox({ ...ok, folder: "/Shared/../Private" }), ["folder"]);
});

test("sizes and drive links", () => {
  assert.equal(fmtBytes(512), "512 B");
  assert.equal(fmtBytes(1536), "1.5 KB");
  assert.equal(fmtBytes(5 << 20), "5.0 MB");
  assert.equal(driveHref("/Shared/Imports/a b.json"), "/files?path=%2FShared%2FImports%2Fa%20b.json");
});
