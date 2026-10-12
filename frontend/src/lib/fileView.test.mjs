// Unit tests of the file viewer helpers (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { parseCsv } from "./csv.ts";
import { fileKind, fmtBytes } from "./fileView.ts";

test("fileKind picks the viewer by extension, then mime", () => {
  assert.equal(fileKind("plan.md"), "markdown");
  assert.equal(fileKind("saldos.csv"), "csv");
  assert.equal(fileKind("book.xlsx"), "sheet");
  assert.equal(fileKind("a.json"), "json");
  assert.equal(fileKind("a.PDF"), "pdf");
  assert.equal(fileKind("logo.png"), "image");
  assert.equal(fileKind("run.py"), "code");
  assert.equal(fileKind("notes.txt"), "text");
  assert.equal(fileKind("deck.pptx"), "other");
  assert.equal(fileKind("noext", "text/markdown"), "markdown");
  // html and svg are never rendered: they are not images or documents here
  assert.equal(fileKind("page.html"), "other");
  assert.equal(fileKind("pic.svg"), "other");
});

test("parseCsv handles quotes, escaped quotes, CRLF, tabs and semicolons", () => {
  assert.deepEqual(parseCsv('a,b\r\n1,"x, y"\r\n2,"he said ""hi"""\r\n'), [["a", "b"], ["1", "x, y"], ["2", 'he said "hi"']]);
  assert.deepEqual(parseCsv("a\tb\n1\t2"), [["a", "b"], ["1", "2"]]);
  assert.deepEqual(parseCsv("a;b\n1;2"), [["a", "b"], ["1", "2"]]);
  assert.deepEqual(parseCsv("a,b\n\n1,2"), [["a", "b"], ["1", "2"]]);
  assert.equal(parseCsv("h\n" + "x\n".repeat(5000)).length, 2000, "rows are bounded");
});

test("fmtBytes", () => {
  assert.equal(fmtBytes(512), "512 B");
  assert.equal(fmtBytes(2048), "2.0 KB");
});

test("office files get their own viewers", async () => {
  const { PREVIEWED, isNumericCell, clampSheet } = await import("./fileView.ts");
  assert.equal(fileKind("informe.docx"), "doc");
  assert.deepEqual(PREVIEWED, ["sheet", "doc"]);
  for (const s of ["12", "$1,234.50", "30%", "-5", "(5)", "1.234,50", "€ 7"]) assert.ok(isNumericCell(s), s);
  for (const s of ["", "Lapiz", "=SUM(A1)", "2026-01-31 x", "12abc"]) assert.ok(!isNumericCell(s), s);
  assert.equal(clampSheet(5, 2), 1);
  assert.equal(clampSheet(-1, 2), 0);
  assert.equal(clampSheet(3, 0), 0);
});
