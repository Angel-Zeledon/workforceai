// Unit tests of batches (one template for many customers): list import and the batch table (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { parseList, filterRows, rowTone, ROW_STATUSES, insertVar } from "./batchLogic.ts";

test("CSV with Spanish headers, quotes and a comma inside a field", () => {
  const { rows, columns, errors } = parseList('Nombre,Correo,Teléfono,Empresa,Notas\n"Pérez, José",jose@x.com,8888,ACME,"dice ""hola"""\nAna,,,,\n');
  assert.deepEqual(columns, ["name", "email", "phone", "company", "notes"]);
  assert.equal(errors.length, 0);
  assert.deepEqual(rows[0], { name: "Pérez, José", email: "jose@x.com", phone: "8888", company: "ACME", notes: 'dice "hola"' });
  assert.deepEqual(rows[1], { name: "Ana" });
  assert.equal(rows.length, 2);
});

test("pasted from Excel (tabs), semicolons and English headers", () => {
  assert.deepEqual(parseList("name\temail\nBo\tbo@b.com").rows, [{ name: "Bo", email: "bo@b.com" }]);
  assert.deepEqual(parseList("company;e-mail;tax id\nBeta;b@b.com;3-101").rows, [{ company: "Beta", email: "b@b.com", tax_id: "3-101" }]);
});

test("a file without a known name column is an error", () => {
  const r = parseList("foo,bar\n1,2");
  assert.equal(r.rows.length, 0);
  assert.deepEqual(r.errors, ["no_name_column"]);
  assert.deepEqual(parseList("").errors, ["empty"]);
});

test("batch table filter and tones", () => {
  const rows = [{ status: "done" }, { status: "needs_help" }, { status: "running" }, { status: "failed" }];
  assert.equal(filterRows(rows, "").length, 4);
  assert.deepEqual(filterRows(rows, "needs_help"), [{ status: "needs_help" }]);
  assert.equal(rowTone("needs_help"), "accent");
  assert.equal(rowTone("failed"), "err");
  assert.equal(rowTone("done"), "ok");
  assert.ok(ROW_STATUSES.includes("partial") && ROW_STATUSES.includes("queued"));
});

test("insert a customer field at the cursor", () => {
  assert.deepEqual(insertVar("Hola ", 5, "nombre"), { text: "Hola {{cliente.nombre}}", cursor: 23 });
  assert.deepEqual(insertVar("ab", 1, "correo"), { text: "a{{cliente.correo}}b", cursor: 19 });
});
