// Unit tests of the read-only database form helpers (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { dbToDraft, draftToDatabase, emptyDbDraft, hasDbErrors, isDbErrorCode, looksLikeConnString, parseSchemas, validateDb } from "./dbSource.ts";

const draft = (over = {}) => ({ ...emptyDbDraft(), host: "db.example.test", user: "reader", ...over });

test("a minimal Postgres source is valid and defaults to sslmode require", () => {
  assert.equal(hasDbErrors(validateDb(draft())), false);
  assert.deepEqual(draftToDatabase(draft()), { engine: "postgres", host: "db.example.test", port: 5432, database: "postgres", user: "reader", sslmode: "require", schemas: [] });
});

test("host, port and user are checked like on the server", () => {
  assert.equal(validateDb(draft({ host: "" })).host, "host_required");
  assert.equal(validateDb(draft({ host: "db example" })).host, "host_invalid");
  assert.equal(validateDb(draft({ port: "70000" })).port, "port_invalid");
  assert.equal(validateDb(draft({ port: "abc" })).port, "port_invalid");
  assert.equal(validateDb(draft({ user: " " })).user, "user_required");
  assert.equal(validateDb(draft({ engine: "mysql" })).engine, "engine_soon");
});

test("schemas are a comma list of plain names", () => {
  assert.deepEqual(parseSchemas("public, sales,,public"), ["public", "sales"]);
  assert.equal(validateDb(draft({ schemas: 'public, "x"; drop' })).schemas, "schemas_invalid");
  assert.equal(validateDb(draft({ schemas: Array.from({ length: 21 }, (_, i) => `s${i}`).join(",") })).schemas, "schemas_too_many");
});

test("url mode sends only what a connection string cannot say (the string itself never goes through here)", () => {
  const d = draft({ mode: "url", host: "", user: "", description: " Pedidos ", schemas: "public" });
  assert.equal(hasDbErrors(validateDb(d)), false);
  assert.deepEqual(draftToDatabase(d), { description: "Pedidos", schemas: ["public"] });
  assert.equal(looksLikeConnString("postgresql://u:p@h:5432/db"), true);
  assert.equal(looksLikeConnString("mysql://u:p@h/db"), false);
});

test("round trip of a stored configuration (no password anywhere)", () => {
  const cfg = { engine: "postgres", host: "h", port: 6543, database: "app", user: "u", sslmode: "verify-full", schemas: ["public"] };
  const d = dbToDraft(cfg);
  assert.equal(d.port, "6543");
  assert.equal(JSON.stringify(d).includes("password"), false);
  assert.deepEqual(draftToDatabase(d), cfg);
  assert.equal(isDbErrorCode("sql_not_read_only"), true);
  assert.equal(isDbErrorCode("whatever"), false);
});
