// Unit tests of the mock coin system and of its catalog copy (run: npm run test:unit).
import assert from "node:assert/strict";
import fs from "node:fs";
import { test } from "node:test";
import { MockRewards } from "./rewards-mock.ts";

const mk = () => {
  const events = [];
  const r = new MockRewards((type, payload, agent) => events.push({ type, payload, agent }), 0);
  return { r, events };
};
const get = (r, path) => r.handle("GET", path.split("/").filter(Boolean), new URLSearchParams(), undefined);
const call = (r, method, path, body) => r.handle(method, path.split("/").filter(Boolean), new URLSearchParams(), body);

test("the mock catalog is a copy of the backend catalog (single source of truth)", () => {
  const backend = new URL("../../../../backend/internal/rewards/catalog.json", import.meta.url);
  if (!fs.existsSync(backend)) return; // frontend checked out alone
  assert.deepEqual(JSON.parse(fs.readFileSync(new URL("./shopCatalog.json", import.meta.url), "utf8")), JSON.parse(fs.readFileSync(backend, "utf8")));
});

test("every item has a price, es/en names and a slot", async () => {
  const { r } = mk();
  const cat = await get(r, "/shop/catalog");
  assert.ok(cat.items.length >= 40);
  for (const it of cat.items) {
    assert.ok(it.price > 0 && it.slot && it.name.es && it.name.en && it.description.es && it.description.en, it.id);
    assert.ok(["character", "office"].includes(it.kind));
  }
});

test("coins are earned only when a task finishes, once per task", async () => {
  const { r, events } = mk();
  assert.equal((await get(r, "/rewards/wallet")).wallet.balance, 0);
  r.onTaskDone({ id: "t1", title: "Analizar", agent_id: "analyst" });
  r.onTaskDone({ id: "t1", title: "Analizar", agent_id: "analyst" }); // replay
  r.onTaskDone({ id: "t2", title: "Vender", agent_id: "sales" }, "L");
  const w = await get(r, "/rewards/wallet");
  assert.equal(w.wallet.balance, 30);
  assert.deepEqual(w.earned_by_agent, { analyst: 10, sales: 20 });
  assert.equal(events.filter((e) => e.type === "coins.earned").length, 2);
  assert.equal(events[0].agent, "analyst");
  const ledger = await get(r, "/rewards/ledger");
  assert.equal(ledger.length, 2);
  assert.equal(ledger[0].ref_id, "t2");
});

test("purchases never overspend, items are owned once, equip needs ownership", async () => {
  const { r, events } = mk();
  await assert.rejects(call(r, "POST", "/shop/purchases", { item_id: "hat_cap" }), /409/);
  for (let i = 0; i < 6; i++) r.onTaskDone({ id: `t${i}`, title: "x", agent_id: "analyst" }); // 60
  const bought = await call(r, "POST", "/shop/purchases", { item_id: "hat_cap" }); // 50
  assert.equal(bought.wallet.balance, 10);
  await assert.rejects(call(r, "POST", "/shop/purchases", { item_id: "hat_cap" }), /409/); // owned
  await assert.rejects(call(r, "POST", "/shop/purchases", { item_id: "hat_top" }), /409/); // too expensive
  await assert.rejects(call(r, "PUT", "/agents/analyst/equipment/hat", { item_id: "hat_beanie" }), /409/); // not owned
  await assert.rejects(call(r, "PUT", "/agents/analyst/equipment/face", { item_id: "hat_cap" }), /400/); // wrong slot
  await call(r, "PUT", "/agents/analyst/equipment/hat", { item_id: "hat_cap" });
  assert.deepEqual((await get(r, "/shop/state")).equipment, [{ agent_id: "analyst", slot: "hat", item_id: "hat_cap" }]);
  await call(r, "DELETE", "/agents/analyst/equipment/hat");
  assert.equal((await get(r, "/shop/state")).equipment.length, 0);
  assert.deepEqual(events.map((e) => e.type).filter((t) => t !== "coins.earned"), ["shop.purchased", "item.equipped", "item.unequipped"]);
});

test("office items go to their slot", async () => {
  const { r } = mk();
  for (let i = 0; i < 5; i++) r.onTaskDone({ id: `t${i}`, title: "x", agent_id: "ops" });
  await call(r, "POST", "/shop/purchases", { item_id: "plant_cactus" });
  await call(r, "PUT", "/office/items/plant_1", { item_id: "plant_cactus" });
  assert.deepEqual((await get(r, "/shop/state")).office, [{ slot: "plant_1", item_id: "plant_cactus" }]);
  await assert.rejects(call(r, "PUT", "/office/items/lamp_1", { item_id: "plant_cactus" }), /400/);
  await call(r, "DELETE", "/office/items/plant_1");
  assert.equal((await get(r, "/shop/state")).office.length, 0);
});
