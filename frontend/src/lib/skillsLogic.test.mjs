// Unit tests of the skill library UI logic (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  countByStatus, emptyDraft, exampleFile, filterSkills, fromTask, joinFiles, parseExample, parseQuality, pathIssue, qualityFile, slugify, splitFiles, validateDraft,
} from "./skillsLogic.ts";

test("slugify makes valid skill names", () => {
  assert.equal(slugify("Conciliación bancaria del mes"), "conciliacion-bancaria-del-mes");
  assert.equal(slugify("  Hello, World!  "), "hello-world");
  assert.equal(slugify("x".repeat(100)).length, 64);
  assert.equal(slugify("!!!"), "");
});

test("path rules: traversal, extensions, depth", () => {
  assert.equal(pathIssue("templates/a.csv"), null);
  assert.equal(pathIssue("scripts/run.py"), null);
  assert.equal(pathIssue("../x.md"), "invalid");
  assert.equal(pathIssue("/etc/x.md"), "invalid");
  assert.equal(pathIssue("a\\b.md"), "invalid");
  assert.equal(pathIssue("my file.md"), "invalid");
  assert.equal(pathIssue("a/b/c/d/e.md"), "invalid");
  assert.equal(pathIssue("SKILL.md"), "invalid");
  assert.equal(pathIssue("logo.png"), "ext");
  assert.equal(pathIssue("tool.exe"), "ext");
  assert.equal(pathIssue(""), "empty");
});

test("examples and quality criteria round trip through resource files", () => {
  const f = exampleFile(0, { input: "Extracto de enero", output: "Tabla de diferencias" });
  assert.equal(f.path, "examples/example-1.md");
  assert.deepEqual(parseExample(f.content), { input: "Extracto de enero", output: "Tabla de diferencias" });
  assert.deepEqual(parseQuality(qualityFile(["Cita la fila", " Suma cuadra "]).content), ["Cita la fila", "Suma cuadra"]);
  assert.equal(qualityFile(["", "  "]), null);
  const parts = { files: [{ path: "templates/a.csv", content: "a,b\n" }], examples: [{ input: "i1", output: "o1" }, { input: "i2", output: "o2" }], quality: ["q1"] };
  const files = joinFiles(parts);
  assert.deepEqual(files.map((x) => x.path), ["templates/a.csv", "examples/example-1.md", "examples/example-2.md", "quality-criteria.md"]);
  assert.deepEqual(splitFiles(files), parts);
  // an example with only one side filled is kept; a blank one is dropped
  assert.equal(joinFiles({ files: [], examples: [{ input: "", output: "" }], quality: [] }).length, 0);
});

test("draft validation mirrors the backend limits", () => {
  const ok = { ...emptyDraft(), name: "my-skill", description: "Use when needed", instructions: "# Steps" };
  assert.deepEqual(validateDraft(ok), []);
  assert.equal(validateDraft({ ...ok, name: "Bad Name" })[0].code, "name_invalid");
  assert.deepEqual(validateDraft({ ...ok, name: "Bad Name" }, true), []); // the name of an existing skill is not edited
  assert.equal(validateDraft({ ...ok, description: " " })[0].code, "description_required");
  assert.equal(validateDraft({ ...ok, description: "x".repeat(1025) })[0].code, "description_too_long");
  assert.equal(validateDraft({ ...ok, instructions: "" })[0].code, "instructions_required");
  const bad = { ...ok, parts: { files: [{ path: "../x.md", content: "x" }, { path: "a.md", content: "x" }, { path: "A.md", content: "y" }, { path: "e.md", content: " " }], examples: [], quality: [] } };
  const codes = validateDraft(bad).map((i) => i.code);
  assert.ok(codes.includes("file_path") && codes.includes("file_duplicate") && codes.includes("file_empty"));
  const big = { ...ok, parts: { files: [{ path: "big.md", content: "x".repeat(512 * 1024 + 1) }], examples: [], quality: [] } };
  assert.ok(validateDraft(big).some((i) => i.code === "file_too_large"));
});

test("save as skill: prefilled from the task, deliverable attached as an example", () => {
  const L = { whenToUse: "When to use it", context: "Context", approach: "Approach", steps: "Steps", quality: "Quality", descriptionPrefix: "Use when you must:", example: "Saved from a task" };
  const d = fromTask({ title: "Cuadrar la caja de marzo", description: "Cuadra el extracto", output: { summary: "Margen real 24%", findings: ["Costo de capacidad"], recommendations: ["Ajustar precio", "Revisar contrato"] } }, L);
  assert.equal(d.name, "cuadrar-la-caja-de-marzo");
  assert.equal(d.source, "from_work");
  assert.match(d.description, /^Use when you must: Cuadrar/);
  assert.match(d.instructions, /## When to use it\nCuadra el extracto/);
  assert.match(d.instructions, /1\. Ajustar precio\n2\. Revisar contrato/);
  assert.equal(d.parts.examples.length, 1);
  assert.match(d.parts.examples[0].output, /Margen real 24%/);
  assert.deepEqual(validateDraft(d), []);
  assert.equal(fromTask({ title: "Sin salida", output: null }, L).parts.examples.length, 0);
});

test("filters and counters", () => {
  const list = [
    { name: "bank-reconciliation", description: "Concilia", status: "active" },
    { name: "b2b-proposal", description: "Propuesta", status: "in_review" },
    { name: "draft-one", description: "x", status: "draft" },
  ];
  assert.deepEqual(countByStatus(list), { all: 3, active: 1, in_review: 1, draft: 1, disabled: 0 });
  assert.equal(filterSkills(list, "active", "").length, 1);
  assert.equal(filterSkills(list, "all", "PROP").length, 1);
  assert.equal(filterSkills(list, "disabled", "").length, 0);
});
