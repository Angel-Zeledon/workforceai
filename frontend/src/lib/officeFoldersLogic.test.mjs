// Unit tests of the office folders helpers (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import { attachDest, rootLabel, privateOfficeFolders, officeFolderOfAgent } from "./officeFoldersLogic.ts";

const t = (k) => ({ "drive.root.offices": "Oficinas", "drive.rootName.Shared": "Compartido" })[k] ?? k;

test("attachDest: no office folder falls back to Shared/Chat", () => {
  assert.equal(attachDest(undefined, "2026-11-09", "Adjuntos"), "/Shared/Chat/2026-11-09");
  assert.equal(attachDest("", "2026-11-09", "Adjuntos"), "/Shared/Chat/2026-11-09");
});
test("attachDest: office folder + the translated word + the day", () => {
  assert.equal(attachDest("/Offices/Legal", "2026-11-09", "Adjuntos"), "/Offices/Legal/Adjuntos/2026-11-09");
  assert.ok(attachDest("/Offices/Legal", "2026-11-09", "Attachments").endsWith("/Attachments/2026-11-09"));
  assert.equal(attachDest("/Offices/Legal/", "2026-11-09", "Anexos"), "/Offices/Legal/Anexos/2026-11-09");
});
test("rootLabel: Offices uses its key, the others what they used", () => {
  assert.equal(rootLabel("Offices", t), "Oficinas");
  assert.equal(rootLabel("Shared", t), "Compartido");
  assert.equal(rootLabel("Contabilidad", t), "Contabilidad");
});
test("privateOfficeFolders: only private non-general offices with a folder", () => {
  const s = privateOfficeFolders([
    { visibility: "private", general: false, folder_path: "/Offices/Contabilidad" },
    { visibility: "open", general: false, folder_path: "/Offices/Ventas" },
    { visibility: "private", general: false },
    { visibility: "private", general: true, folder_path: "/Offices/General" },
  ]);
  assert.deepEqual([...s], ["/Offices/Contabilidad"]);
});
test("officeFolderOfAgent: agent conversation -> folder of its office", () => {
  const dir = [{ id: "a1", office_id: "o1" }, { id: "a2", office_id: "o2" }];
  const offices = [{ id: "o1", folder_path: "/Offices/Legal" }, { id: "o2" }];
  assert.equal(officeFolderOfAgent("agent:a1", dir, offices), "/Offices/Legal");
  assert.equal(officeFolderOfAgent("agent:a2", dir, offices), undefined);
  assert.equal(officeFolderOfAgent("office", dir, offices), undefined);
  assert.equal(officeFolderOfAgent("agent:zz", dir, offices), undefined);
});
