// Unit tests of the slash command parser / matcher (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { STATIC_COMMANDS as C } from "./registry.ts";
import { buildArgs, detectLang, matchCommands, mentionAt, parseArgs, parseInput, replaceRange, resolveCommand, sortRows, surfaceOf, tokenize } from "./logic.ts";

const get = (id) => C.find((c) => c.id === id);

test("parseInput splits name and rest", () => {
  assert.deepEqual(parseInput("hola"), { slash: false, name: "", rest: "", hasSpace: false });
  assert.deepEqual(parseInput("/"), { slash: true, name: "", rest: "", hasSpace: false });
  assert.deepEqual(parseInput("/excel ventas por mes"), { slash: true, name: "excel", rest: "ventas por mes", hasSpace: true });
  assert.equal(parseInput("/pausa").hasSpace, false);
});

test("commands resolve in both languages, accents ignored", () => {
  assert.equal(resolveCommand(C, "resumir").id, "compact");
  assert.equal(resolveCommand(C, "compact").id, "compact");
  assert.equal(resolveCommand(C, "RESUMIR").id, "compact");
  assert.equal(resolveCommand(C, "presentacion").id, "presentation");
  assert.equal(resolveCommand(C, "presentación").id, "presentation");
  assert.equal(resolveCommand(C, "costo").id, "usage");
  assert.equal(resolveCommand(C, "resumen-del-dia").id, "daily");
  assert.equal(resolveCommand(C, "nope"), undefined);
});

test("reply language follows the alias typed", () => {
  assert.equal(detectLang(get("compact"), "resumir", "en"), "es");
  assert.equal(detectLang(get("compact"), "compact", "es"), "en");
  assert.equal(detectLang(get("clear"), "limpiar", "en"), "es");
  assert.equal(detectLang(get("clear"), "clear", "es"), "en");
  assert.equal(detectLang(get("usage"), "costo", "en"), "es");
  // neutral names use the UI locale
  assert.equal(detectLang(get("excel"), "excel", "es"), "es");
  assert.equal(detectLang(get("excel"), "excel", "en"), "en");
  assert.equal(detectLang(get("pdf"), "pdf", "en"), "en");
  // an alias shared by both lists
  const both = { ...get("team"), aliases: { es: ["team"], en: ["team"] } };
  assert.equal(detectLang(both, "team", "es"), "es");
  assert.equal(detectLang(both, "team", "en"), "en");
});

test("menu search matches both languages and shows the locale alias", () => {
  const es = matchCommands(C, "comp", "es");
  assert.equal(es[0].cmd.id, "compact");
  assert.equal(es[0].alias, "resumir");
  const en = matchCommands(C, "resum", "en");
  assert.ok(en.some((m) => m.cmd.id === "compact" && m.alias === "compact"));
  assert.ok(en.some((m) => m.cmd.id === "daily"));
  assert.equal(matchCommands(C, "reun", "en")[0].cmd.id, "meeting");
  assert.equal(matchCommands(C, "meet", "es")[0].cmd.id, "meeting");
  const all = matchCommands(C, "", "es");
  assert.equal(all.length, C.length);
  const order = all.map((m) => m.cmd.group);
  assert.deepEqual(order, [...order].sort((a, b) => ["control", "office", "org"].indexOf(a) - ["control", "office", "org"].indexOf(b)));
  assert.equal(matchCommands(C, "uso", "es")[0].cmd.id, "usage");
});

test("registry covers the contract command ids", () => {
  const ids = "clear compact context usage model memory pause resume stop status trace export excel word pdf presentation meeting assign project daily pending search file open email linkedin remember tone language template skill learn undo team hire permissions shop inventory help".split(" ");
  for (const id of ids) assert.ok(get(id), id);
  assert.equal(C.length, ids.length);
});

test("tokenize honors quotes", () => {
  const t = tokenize('a "b c" d');
  assert.deepEqual(t.map((x) => x.text), ["a", "b c", "d"]);
  assert.equal(t[1].quoted, true);
});

test("parseArgs: positional, variadic and mentions", () => {
  const assign = get("assign");
  let st = parseArgs(assign, "@Valeria revisar las facturas");
  assert.deepEqual(buildArgs(assign, st), { agent: "Valeria", task: "revisar las facturas" });
  assert.deepEqual(st.mentions, ["Valeria"]);
  assert.deepEqual(st.missing, []);
  st = parseArgs(assign, "");
  assert.deepEqual(st.missing, ["agent", "task"]);
  assert.equal(st.current, 0);
  st = parseArgs(assign, "@Val");
  assert.equal(st.current, 0);
  assert.equal(st.currentTok.text, "@Val");
  st = parseArgs(assign, "@Valeria ");
  assert.equal(st.current, 1);
  assert.equal(st.currentTok, null);
  assert.deepEqual(st.missing, ["task"]);
  st = parseArgs(get("excel"), "ventas por mes");
  assert.equal(st.values.description, "ventas por mes");
  st = parseArgs(get("memory"), '"Ana María" borrar');
  assert.equal(st.values.agent, "Ana María");
  assert.equal(st.values.action, "borrar");
});

test("mentionAt finds the @ at the caret", () => {
  assert.deepEqual(mentionAt("hola @Val", 9), { query: "Val", start: 5, end: 9 });
  assert.deepEqual(mentionAt("@", 1), { query: "", start: 0, end: 1 });
  assert.equal(mentionAt("correo a@b", 10), null);
  assert.equal(mentionAt("hola @Val y", 11), null);
  assert.equal(mentionAt("hola", 4), null);
});

test("replaceRange inserts completions and quotes values with spaces", () => {
  assert.deepEqual(replaceRange("/pausa @Va", 7, 10, "Valeria"), { text: "/pausa Valeria ", caret: 15 });
  assert.equal(replaceRange("/memoria ", 9, 9, "Ana María").text, '/memoria "Ana María" ');
});

test("surfaceOf and sortRows", () => {
  assert.equal(surfaceOf("office"), "office");
  assert.equal(surfaceOf("office", true), "general");
  assert.equal(surfaceOf("agent:a1"), "agent");
  assert.deepEqual(sortRows([["b", "10"], ["a", "9"], ["c", "100"]], 1, 1).map((r) => r[0]), ["a", "b", "c"]);
  assert.deepEqual(sortRows([["b", 1], ["a", 2]], 0, -1).map((r) => r[0]), ["b", "a"]);
});
