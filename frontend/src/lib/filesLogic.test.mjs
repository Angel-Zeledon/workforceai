// Unit tests of the Files screen in plain words (run: node --test src/lib/filesLogic.test.mjs).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  sectionOf, officeOfPath, placeRoot, isHiddenPath, visibleFolders, isAttachmentsFolder, folderLabel, humanCrumbs, agentDesks,
  navDepartments, sectionHelp, canCreateHere, readOnlyReason, isProtectedFolder, nameError, moveError, moveWarning, joinPath,
  dateGroup, groupAttachments, dayFolders, folderHref, parentPath, filesErrorKey,
} from "./filesLogic.ts";

test("filesErrorKey: codes become words, never the code itself", () => {
  assert.equal(filesErrorKey({ status: 403, code: "read_only_folder" }), "files.err.readOnly");
  assert.equal(filesErrorKey({ status: 400, code: "protected_folder" }), "files.err.protected");
  assert.equal(filesErrorKey({ status: 404, code: "not_found" }), "files.err.notFound");
  assert.equal(filesErrorKey({ status: 409, code: "whatever" }), "files.err.conflict");
  assert.equal(filesErrorKey({ status: 400, code: "extension_change" }), "files.name.ext");
  assert.equal(filesErrorKey({ status: 413, code: "too_large" }), "files.err.tooLarge");
  assert.equal(filesErrorKey(new Error("boom")), "files.err.generic");
  assert.equal(filesErrorKey(null), "files.err.generic");
});

const L = {
  "files.title": "Archivos", "files.section.depts": "Mis departamentos", "files.section.shared": "Compartido con todos",
  "files.section.projects": "Proyectos", "files.section.clients": "Clientes", "files.section.agents": "Escritorios de los agentes",
  "files.attachments": "Adjuntos del chat", "files.agent.unknown": "Agente que ya no está",
};
const t = (k) => L[k] ?? k;
const offices = [
  { id: "of_acc", name: "Contabilidad", folder_path: "/Offices/Contabilidad", is_member: true, visibility: "private", leads: [{ name: "Laura Méndez" }] },
  { id: "of_leg", name: "Legal", folder_path: "/Offices/Legal", is_member: false, visibility: "open", leads: [] },
  { id: "of_gen", name: "General", general: true, is_member: true },
  { id: "of_sec", name: "Dirección", folder_path: "/Offices/Direccion", is_member: false, visibility: "private" },
];
const agents = { accounting: "Tomás Vidal", sales: "Sofía Ramos" };
const ctx = { offices, agentName: (id) => agents[id], t, locale: "es" };

test("sectionOf: the technical roots become sections", () => {
  assert.equal(sectionOf("/"), "home");
  assert.equal(sectionOf("/Offices"), "offices");
  assert.equal(sectionOf("/Offices/Contabilidad/Bancos"), "dept");
  assert.equal(sectionOf("/offices/contabilidad"), "dept");
  assert.equal(sectionOf("/Shared/Politicas"), "shared");
  assert.equal(sectionOf("/Projects/Lanzamiento"), "projects");
  assert.equal(sectionOf("/Clients"), "clients");
  assert.equal(sectionOf("/Agents/accounting"), "agents");
});

test("officeOfPath and placeRoot: the department a path is in, case-insensitive", () => {
  assert.equal(officeOfPath("/offices/CONTABILIDAD/Bancos", offices)?.id, "of_acc");
  assert.equal(officeOfPath("/Offices/Contabilidad2", offices), undefined);
  assert.equal(placeRoot("/Offices/Contabilidad/Bancos/x", offices), "/Offices/Contabilidad");
  assert.equal(placeRoot("/Shared/Politicas", offices), "/Shared");
  assert.equal(placeRoot("/", offices), "/");
});

test("internal folders are hidden: skills and their versions", () => {
  assert.ok(isHiddenPath("/Shared/Skills"));
  assert.ok(isHiddenPath("/shared/skills/conciliar/v3"));
  assert.ok(!isHiddenPath("/Shared/Skillset"));
  assert.ok(!isHiddenPath("/Shared/Politicas"));
  const kept = visibleFolders([{ path: "/Shared/Skills" }, { path: "/Shared/Politicas" }, { path: "/Shared/Chat" }]);
  assert.deepEqual(kept.map((f) => f.path), ["/Shared/Politicas", "/Shared/Chat"]);
});

test("isAttachmentsFolder: Shared/Chat and a department's Adjuntos in any language", () => {
  assert.ok(isAttachmentsFolder("/Shared/Chat"));
  assert.ok(isAttachmentsFolder("/Offices/Legal/Adjuntos"));
  assert.ok(isAttachmentsFolder("/Offices/Legal/attachments"));
  assert.ok(isAttachmentsFolder("/Offices/Legal/Anexos"));
  assert.ok(!isAttachmentsFolder("/Offices/Legal/Contratos/Adjuntos"));
  assert.ok(!isAttachmentsFolder("/Shared/Chat/2026-11-21"));
});

test("folderLabel: sections, departments and agents by name, never ids", () => {
  assert.equal(folderLabel("/Shared", ctx), "Compartido con todos");
  assert.equal(folderLabel("/Agents", ctx), "Escritorios de los agentes");
  assert.equal(folderLabel("/Offices/Contabilidad", ctx), "Contabilidad");
  assert.equal(folderLabel("/Agents/accounting", ctx), "Tomás Vidal");
  assert.equal(folderLabel("/Agents/ag_9f3c2", ctx), "Agente que ya no está");
  assert.equal(folderLabel("/Offices/Contabilidad/Adjuntos", ctx), "Adjuntos del chat");
  assert.equal(folderLabel("/Shared/Chat", ctx), "Adjuntos del chat");
  assert.match(folderLabel("/Shared/Chat/2026-11-21", ctx), /21.*noviembre.*2026/);
  assert.equal(folderLabel("/Offices/Contabilidad/Bancos y conciliaciones", ctx), "Bancos y conciliaciones");
});

test("humanCrumbs: a department starts at its name; the rest at the section", () => {
  assert.deepEqual(humanCrumbs("/", ctx), []);
  assert.deepEqual(humanCrumbs("/Offices/Contabilidad/Bancos y conciliaciones", ctx).map((c) => c.label), ["Contabilidad", "Bancos y conciliaciones"]);
  assert.deepEqual(humanCrumbs("/Offices/Contabilidad/Bancos y conciliaciones", ctx).map((c) => c.path), ["/Offices/Contabilidad", "/Offices/Contabilidad/Bancos y conciliaciones"]);
  assert.deepEqual(humanCrumbs("/Agents/accounting/borradores", ctx).map((c) => c.label), ["Escritorios de los agentes", "Tomás Vidal", "borradores"]);
  assert.deepEqual(humanCrumbs("/Shared/Politicas", ctx).map((c) => c.label), ["Compartido con todos", "Politicas"]);
  const crumbs = humanCrumbs("/Offices/Contabilidad/Adjuntos", ctx).map((c) => c.label).join(" › ");
  assert.equal(crumbs, "Contabilidad › Adjuntos del chat");
  for (const c of humanCrumbs("/Offices/Contabilidad/Clientes/Grupo Alfa", ctx)) assert.ok(!c.label.startsWith("/"), "no technical path in a crumb");
});

test("agentDesks: by name, sorted, unknown agents last and never by id", () => {
  const d = agentDesks([{ name: "sales", path: "/Agents/sales" }, { name: "ag_x", path: "/Agents/ag_x" }, { name: "accounting", path: "/Agents/accounting" }], ctx);
  assert.deepEqual(d.map((x) => x.name), ["Sofía Ramos", "Tomás Vidal", "Agente que ya no está"]);
  assert.ok(d.every((x) => !x.name.includes("ag_x")));
});

test("navDepartments: mine first, open ones to read, never General nor a sealed one", () => {
  const n = navDepartments(offices);
  assert.deepEqual(n.mine.map((o) => o.name), ["Contabilidad"]);
  assert.deepEqual(n.others.map((o) => o.name), ["Legal"]);
});

test("sectionHelp: one line per place", () => {
  assert.deepEqual(sectionHelp("/Offices/Contabilidad/Bancos", ctx), { key: "files.help.deptPrivate", vars: { name: "Contabilidad" } });
  assert.deepEqual(sectionHelp("/Offices/Legal", ctx), { key: "files.help.deptReader", vars: { name: "Legal" } });
  assert.equal(sectionHelp("/Shared", ctx).key, "files.help.shared");
  assert.equal(sectionHelp("/Shared/Chat", ctx).key, "files.help.attachments");
  assert.equal(sectionHelp("/Agents/sales", ctx).key, "files.help.agents");
  assert.equal(sectionHelp("/", ctx).key, "files.help.home");
});

test("canCreateHere and readOnlyReason: edit where the server says so, never in the platform's own lists", () => {
  assert.ok(canCreateHere("/Offices/Contabilidad", "edit"));
  assert.ok(canCreateHere("/Shared", "edit"));
  assert.ok(canCreateHere("/Agents/sales", "edit"));
  assert.ok(!canCreateHere("/Offices/Legal", "read"));
  for (const p of ["/", "/Offices", "/Agents", "/Projects", "/projects/"]) assert.ok(!canCreateHere(p, "edit"), p);
  assert.equal(readOnlyReason("/Offices/Contabilidad", "edit", ctx), null);
  assert.deepEqual(readOnlyReason("/Offices/Contabilidad/x", "read", ctx), { key: "files.ro.deptLead", vars: { name: "Contabilidad", lead: "Laura Méndez" } });
  assert.deepEqual(readOnlyReason("/Offices/Legal", "read", ctx), { key: "files.ro.dept", vars: { name: "Legal" } });
  assert.deepEqual(readOnlyReason("/Offices/Legal/Contratos", "read", ctx, "Legal"), { key: "files.ro.shared", vars: { office: "Legal" } });
  assert.equal(readOnlyReason("/Shared", "read", ctx).key, "files.ro.role");
  assert.equal(readOnlyReason("/Projects", "edit", ctx).key, "files.ro.projects");
  assert.equal(readOnlyReason("/", undefined, ctx).key, "files.ro.pick");
});

test("isProtectedFolder: the platform's own folders cannot be renamed or moved", () => {
  assert.ok(isProtectedFolder({ path: "/Shared" }));
  assert.ok(isProtectedFolder({ path: "/Offices/Contabilidad" }));
  assert.ok(isProtectedFolder({ path: "/Agents/sales" }));
  assert.ok(isProtectedFolder({ path: "/Projects/Lanzamiento" }));
  assert.ok(isProtectedFolder({ path: "/Clients/Grupo Alfa", ref_id: "cli_1" }));
  assert.ok(isProtectedFolder({ path: "/Offices/Contabilidad/Adjuntos" }));
  assert.ok(!isProtectedFolder({ path: "/Clients/Varios" }));
  assert.ok(!isProtectedFolder({ path: "/Offices/Contabilidad/Bancos" }));
  assert.ok(!isProtectedFolder({ path: "/Shared/Politicas" }));
});

test("nameError: the drive's rules, in words", () => {
  assert.equal(nameError("  Bancos  ", []), null);
  assert.equal(nameError("   ", []), "files.name.empty");
  assert.equal(nameError("a/b", []), "files.name.chars");
  assert.equal(nameError("a\\b", []), "files.name.chars");
  assert.equal(nameError("¿qué?", []), "files.name.chars");
  assert.equal(nameError("x​y", []), "files.name.chars");
  assert.equal(nameError("..", []), "files.name.dots");
  assert.equal(nameError("final.", []), "files.name.dots");
  assert.equal(nameError("x".repeat(121), []), "files.name.long");
  assert.equal(nameError("bancos", ["Bancos"]), "files.name.taken");
  assert.equal(nameError("Bancos", ["Bancos"], { original: "Bancos" }), null, "same name is not a duplicate of itself");
  assert.equal(nameError("BANCOS", ["Bancos"], { original: "Bancos" }), null, "a change of case only");
  assert.equal(nameError("informe.pdf", [], { original: "informe.md", file: true }), "files.name.ext");
  assert.equal(nameError("informe final.md", ["otro.md"], { original: "informe.md", file: true }), null);
});

test("moveError: not into itself, not where it already is, not into the platform's lists", () => {
  const folder = { kind: "folder", path: "/Offices/Contabilidad/Bancos" };
  assert.equal(moveError(folder, "/Offices/Contabilidad/Bancos/Viejos"), "files.move.inside");
  assert.equal(moveError(folder, "/Offices/Contabilidad/Bancos"), "files.move.inside");
  assert.equal(moveError(folder, "/Offices/Contabilidad"), "files.move.same");
  assert.equal(moveError(folder, "/Offices"), "files.move.system");
  assert.equal(moveError(folder, "/Offices/Contabilidad/Impuestos"), null);
  assert.equal(moveError({ kind: "file", path: "/Shared/a.md" }, "/Shared/Politicas"), null);
  assert.equal(moveError({ kind: "file", path: "/Shared/a.md" }, "/shared"), "files.move.same");
});

test("moveWarning: says when a move changes who sees it", () => {
  assert.equal(moveWarning("/Offices/Contabilidad/a.md", "/Offices/Contabilidad/Impuestos", ctx), null);
  assert.deepEqual(moveWarning("/Offices/Contabilidad/a.md", "/Shared", ctx), { key: "files.move.warn.shared" });
  assert.deepEqual(moveWarning("/Shared/a.md", "/Offices/Contabilidad", ctx), { key: "files.move.warn.dept", vars: { name: "Contabilidad" } });
  assert.deepEqual(moveWarning("/Offices/Contabilidad/a.md", "/Clients/Varios", ctx), { key: "files.move.warn.out", vars: { name: "Contabilidad" } });
  assert.equal(moveWarning("/Shared/a.md", "/Shared/Politicas", ctx), null);
});

test("joinPath and parentPath", () => {
  assert.equal(joinPath("/Shared/", "a.md"), "/Shared/a.md");
  assert.equal(joinPath("/", "x"), "/x");
  assert.equal(parentPath("/Shared/a.md"), "/Shared");
  assert.equal(parentPath("/Shared"), "/");
});

test("attachments are grouped by the day they arrived", () => {
  const now = new Date(2026, 10, 22, 15, 0);
  assert.equal(dateGroup(new Date(2026, 10, 22, 0, 5), now), "today");
  assert.equal(dateGroup(new Date(2026, 10, 16), now), "week");
  assert.equal(dateGroup(new Date(2026, 10, 1), now), "month");
  assert.equal(dateGroup(new Date(2026, 8, 30), now), "older");
  const f = (path, at) => ({ path, updated_at: at });
  const g = groupAttachments([
    f("/Shared/Chat/2026-10-01/viejo.pdf", "2026-11-22T10:00:00Z"), // edited today, attached long ago
    f("/Shared/Chat/2026-11-22/hoy.png", "2026-11-22T09:00:00Z"),
    f("/Shared/Chat/2026-11-20/martes.csv", "2026-11-20T09:00:00Z"),
    f("/Shared/Chat/2026-11-22/hoy2.png", "2026-11-22T11:00:00Z"),
  ], now);
  assert.deepEqual(g.map((x) => x.group), ["today", "week", "older"]);
  assert.deepEqual(g[0].files.map((x) => x.path.split("/").pop()), ["hoy2.png", "hoy.png"]);
  const d = dayFolders([{ name: "2026-11-20" }, { name: "Varios" }, { name: "2026-11-22" }, { name: "2026-13-40" }]);
  assert.deepEqual(d.days.map((x) => x.name), ["2026-11-22", "2026-11-20"]);
  assert.deepEqual(d.others.map((x) => x.name), ["Varios", "2026-13-40"]);
});

test("folderHref: a department keeps its direct link, the rest live in /files", () => {
  assert.equal(folderHref("/", offices), "/files");
  assert.equal(folderHref("/Offices/Contabilidad", offices), "/d/of_acc");
  assert.equal(folderHref("/offices/contabilidad/Bancos y conciliaciones", offices), "/d/of_acc?carpeta=Bancos%20y%20conciliaciones");
  assert.equal(folderHref("/Shared/Politicas", offices), "/files?path=%2FShared%2FPoliticas");
  assert.equal(folderHref("/Offices/Desconocida/x", offices), "/files?path=%2FOffices%2FDesconocida%2Fx");
});
