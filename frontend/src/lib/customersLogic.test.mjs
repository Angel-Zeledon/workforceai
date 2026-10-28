// Unit tests of the customer district helpers (B12; run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  customerColor, defaultCloneName, districtColumns, districtLots, floorsFor, normalizeStatus, shade, sortDistrict, statusRank, validateCustomer, worstStatus,
} from "./customersLogic.ts";

test("status priority: blocked > waiting_approval > on_track > idle; unknown is idle", () => {
  assert.ok(statusRank("blocked") < statusRank("waiting_approval"));
  assert.ok(statusRank("waiting_approval") < statusRank("on_track"));
  assert.ok(statusRank("on_track") < statusRank("idle"));
  assert.equal(normalizeStatus("weird"), "idle");
  assert.equal(normalizeStatus(undefined), "idle");
  assert.equal(worstStatus(["on_track", "waiting_approval", "idle"]), "waiting_approval");
  assert.equal(worstStatus(["on_track", "blocked"]), "blocked");
  assert.equal(worstStatus([]), "idle");
});

test("sortDistrict puts the most urgent first, then by name ignoring accents", () => {
  const mk = (name, status) => ({ status, customer: { name } });
  const out = sortDistrict([mk("Zeta", "idle"), mk("Álamo", "on_track"), mk("Beta", "blocked"), mk("alfa", "on_track")]);
  assert.deepEqual(out.map((x) => x.customer.name), ["Beta", "Álamo", "alfa", "Zeta"]);
});

test("customerColor keeps a valid hex and derives a stable fallback", () => {
  assert.equal(customerColor("#AABBCC", "x"), "#aabbcc");
  const a = customerColor("", "cus_1"), b = customerColor("nope", "cus_1");
  assert.match(a, /^#[0-9a-f]{6}$/);
  assert.equal(a, b);
  assert.equal(shade("#000000", 0.5), "#808080");
  assert.equal(shade("#808080", -0.5), "#404040");
});

test("defaultCloneName", () => {
  assert.equal(defaultCloneName("Contador", "Ferretería López"), "Contador · Ferretería López");
  assert.equal(defaultCloneName(" ", "Ferretería López"), "Ferretería López");
  assert.equal(defaultCloneName("Contador", ""), "Contador");
});

test("district grid: columns by width, lots inside the canvas, no overlap", () => {
  assert.equal(districtColumns(1, 1200), 1);
  assert.equal(districtColumns(4, 360), 2);
  assert.equal(districtColumns(9, 1200), 3);
  assert.equal(districtColumns(30, 1200), 4);
  const { lots, width, height } = districtLots(5, 2, 160);
  assert.equal(lots.length, 5);
  const keys = new Set(lots.map((l) => `${l.x},${l.y}`));
  assert.equal(keys.size, 5);
  for (const l of lots) {
    assert.ok(l.x - 80 >= 0 && l.x + 80 <= width, `x inside: ${l.x}`);
    assert.ok(l.y - 40 >= 0 && l.y + 40 <= height, `y inside: ${l.y}`);
  }
});

test("floors and form validation", () => {
  assert.equal(floorsFor(0, 0), 2);
  assert.equal(floorsFor(10, 10), 5);
  assert.equal(validateCustomer({ name: "  " }), "name_required");
  assert.equal(validateCustomer({ name: "x".repeat(121) }), "name_too_long");
  assert.equal(validateCustomer({ name: "López", color: "red" }), "color_invalid");
  assert.equal(validateCustomer({ name: "López", color: "#112233" }), null);
});
