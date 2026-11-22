// Unit tests of the central error translator (run: npm run test:unit).
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "node:test";
import { classifyError, errorFacts, CODE_PHRASE, PHRASE_IDS } from "./errorsCore.ts";

const cat = (l) => JSON.parse(fs.readFileSync(new URL(`./i18n/${l}.json`, import.meta.url), "utf8"));
const CATS = { es: cat("es"), en: cat("en"), pt: cat("pt") };
const render = (p, l = "es") => [p.titleKey, p.detailKey, p.actionKey].filter(Boolean).map((k) => CATS[l][k].replace(/\{(\w+)\}/g, (_, n) => p.vars[n] ?? "")).join(" ");
class Api extends Error { constructor(m, status, code, ref) { super(m); this.status = status; this.code = code; this.ref = ref; } }

test("every phrase of the catalog exists in es, en and pt", () => {
  for (const l of ["es", "en", "pt"]) {
    for (const id of PHRASE_IDS.filter((x) => x !== "forbidden_admin")) assert.ok(CATS[l][`err.c.${id}`], `${l}: err.c.${id}`);
    for (const k of ["network", "timeout", "session", "forbidden", "notfound", "conflict", "invalid", "toolarge", "unsupported", "locked", "ratelimit", "unavailable", "server", "unknown"]) assert.ok(CATS[l][`err.${k}.title`], `${l}: err.${k}.title`);
    for (const k of ["err.ref", "err.retry", "err.boundary.title", "err.boundary.retry", "err.boundary.home", "err.forbidden.ask", "err.c.plan_limit.action"]) assert.ok(CATS[l][k], `${l}: ${k}`);
  }
});

test("the catalog covers the codes the screens used to translate by hand and the main areas", () => {
  for (const c of ["read_only_folder", "version_conflict", "plan_limit", "folder_invalid", "anchor_lost", "change_already_requested", "approval_mismatch", "cascade_changed", "skip_blocks_result",
    "stale_review", "template_invalid", "kill_switch_active", "budget_exceeded", "agent_other_office", "insufficient_coins", "credential_required", "db_unreachable", "name_taken", "zip_bomb"]) {
    assert.ok(CODE_PHRASE[c], c);
  }
});

test("network failure: a TypeError from fetch", () => {
  const p = classifyError(new TypeError("Failed to fetch"));
  assert.equal(p.kind, "network"); assert.equal(p.retry, true);
  assert.equal(render(p), "No hay conexión con el servidor. Revisa tu internet e inténtalo de nuevo.");
});

test("timeouts", () => {
  const e = new Error("x"); e.name = "TimeoutError";
  assert.equal(classifyError(e).kind, "timeout");
});

test("HTTP states by status, also read from 'METHOD path -> status'", () => {
  assert.equal(classifyError(new Api("GET /x -> 401", 401)).kind, "session");
  assert.match(render(classifyError(new Api("", 401))), /Vuelve a entrar/);
  assert.equal(classifyError(new Error("POST /projects/1/skip -> 409")).kind, "conflict");
  assert.match(render(classifyError(new Error("POST /a -> 409"))), /Alguien lo cambió mientras tanto/);
  assert.match(render(classifyError(new Api("", 404))), /Ya no existe o no tienes acceso/);
  assert.equal(classifyError(new Api("", 413)).kind, "toolarge");
  assert.equal(classifyError(new Api("", 422)).kind, "invalid");
  assert.equal(classifyError(new Api("", 429)).retry, true);
  assert.equal(classifyError(new Api("", 503)).kind, "unavailable");
  assert.equal(classifyError(new Api("", 500)).kind, "server");
});

test("403 names who to ask when known", () => {
  assert.match(render(classifyError(new Api("", 403), { who: "Ana" })), /No tienes permiso para esto\. Pídeselo a Ana\./);
  assert.match(render(classifyError(new Api("", 403))), /administra tu empresa/);
});

test("known codes read as a sentence, in the three languages, never the raw code", () => {
  const p = classifyError(new Api("PUT /drive/x -> 423", 423, "read_only_folder"));
  assert.equal(p.kind, "code");
  for (const l of ["es", "en", "pt"]) assert.ok(!render(p, l).includes("read_only_folder"));
  assert.match(render(p), /solo lectura/);
  assert.match(render(classifyError(new Api("", 409, "version_conflict"))), /Recarga/);
});

test("plan_limit offers the upgrade", () => {
  const p = classifyError(new Api("", 409, "plan_limit"));
  assert.equal(p.kind, "limit"); assert.match(render(p), /Mejora tu plan/);
});

test("an unknown code falls back to its status phrase, never the raw code", () => {
  const p = classifyError(new Api("", 409, "totally_new_code"));
  assert.equal(p.kind, "conflict"); assert.ok(!render(p).includes("totally_new_code"));
});

test("an unknown error asks for a reference to share; the backend's ref is kept", () => {
  const u = classifyError(new Error("boom"));
  assert.equal(u.kind, "unknown"); assert.equal(u.needsRef, true);
  const s = classifyError(new Api("", 500, "internal", "ab12cd34"));
  assert.equal(s.kind, "server"); assert.equal(s.ref, "ab12cd34"); assert.equal(s.needsRef, false);
  assert.equal(classifyError(new Api("", 418, "weird", "ff00ff00")).ref, "ff00ff00");
});

test("the screen's own sentence is the title when nothing more is known", () => {
  assert.equal(classifyError(new Error("boom"), { fallbackKey: "customers.error.save" }).titleKey, "customers.error.save");
});

test("demo mode: 'not available in mock mode' reads as a clear demo notice", () => {
  const p = classifyError(Object.assign(new Error("not available in mock mode"), { status: 404 }));
  assert.equal(render(p), "Esta función no está disponible en la demo.");
});

test("a bare code thrown by the mock is understood; a status inside a Response-like too", () => {
  assert.equal(errorFacts(new Error("credential_required")).code, "credential_required");
  assert.equal(errorFacts({ status: 502, ok: false }).status, 502);
});

// Guard: a screen must never print an exception's text, a status line or a raw code.
test("no screen shows e.message / String(e) / a raw {code}", () => {
  const root = path.resolve("src/components");
  const files = [];
  (function walk(d) { for (const n of fs.readdirSync(d, { withFileTypes: true })) { const p = path.join(d, n.name); if (n.isDirectory()) walk(p); else if (/\.tsx$/.test(n.name)) files.push(p); } })(root);
  const bad = [];
  const rules = [
    /\b(set\w*Err\w*|setError|setMsg|setNote|toast)\([^;\n]*(\be\b|\berr\b|\berror\b)(\?)?\.message(?!\.includes)/,
    /String\((e|err|error)\)\)/,
    /\{\s*(e|err|error)(\?)?\.message\s*\}/,
    /\bmessage: *e instanceof Error \? e\.message/,
    /t\("[\w.]+", \{ (code|msg|detail|error): (e|err)/,
  ];
  for (const f of files) {
    fs.readFileSync(f, "utf8").split(/\r?\n/).forEach((line, i) => { if (rules.some((r) => r.test(line))) bad.push(`${path.relative(".", f)}:${i + 1}`); });
  }
  assert.deepEqual(bad, []);
});
