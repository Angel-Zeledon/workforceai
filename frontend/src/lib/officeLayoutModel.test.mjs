// Unit tests of the movable objects of the rooms (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  DECOR_IDS, DEFAULT_LAYOUT, FIXTURES, FIXTURE_IDS, ROOM_PARTS, ZONE_DEFAULTS, ZONE_NAMES, carriedBy, fixturePose, fixtureWorld, hideFixture, isRoomPart,
  moveFixture, normalizeLayout, partFootprint, partNeighbors, restOn, roomRect, showFixture, zoneCenterOf,
} from "./officeLayoutModel.ts";
import { bounds, overlap } from "./officeGeometry.ts";

const near = (a, b, eps = 1e-9) => Math.abs(a - b) < eps;
const L = (patch = {}) => ({ ...normalizeLayout(null), ...patch });

test("every room part belongs to exactly one room and the decor to none", () => {
  const listed = ZONE_NAMES.flatMap((z) => ROOM_PARTS[z]);
  assert.equal(new Set(listed).size, listed.length);
  for (const id of FIXTURE_IDS) assert.equal(isRoomPart(id), listed.includes(id), id);
  assert.deepEqual(DECOR_IDS, ["plant_a", "plant_b", "plant_c", "plant_d", "rack"]);
  assert.equal(ROOM_PARTS.meeting.filter((id) => id.startsWith("meeting_seat_")).length, 6);
  for (const id of ["meeting_table", "meeting_screen", "approvals_desk", "approvals_chair", "cafe_counter", "cafe_machine", "cafe_mug_1", "cafe_mug_2", "cafe_mug_3", "cafe_pot", "cafe_table", "cafe_table_mug", "cafe_plant"]) {
    assert.ok(listed.includes(id), id);
  }
});

test("a layout saved before (no room parts) puts every part where it always was", () => {
  const l = normalizeLayout({ v: 1, zones: {}, fixtures: { rack: { x: 1, z: 2 } } });
  const c = ZONE_DEFAULTS.meeting;
  for (let k = 0; k < 6; k++) {
    const a = (k / 6) * Math.PI * 2 + 0.3;
    const w = fixtureWorld(l, `meeting_seat_${k + 1}`);
    assert.ok(near(w.x, c[0] + Math.sin(a) * 2.25) && near(w.z, c[1] + Math.cos(a) * 2.25), `seat ${k + 1}`);
  }
  assert.deepEqual(fixtureWorld(l, "meeting_table"), { x: c[0], z: c[1], rot: 0 });
  const ap = ZONE_DEFAULTS.approvals;
  assert.ok(near(fixtureWorld(l, "approvals_desk").z, ap[1] + 0.1));
  // the decor keeps its absolute coordinates
  assert.deepEqual(fixtureWorld(l, "rack"), { x: 1, z: 2, rot: Math.PI / 2 });
});

test("room parts are stored relative to their room: moving the room carries them", () => {
  const l = L({ zones: { cafe: { x: 0, z: 5 } }, fixtures: { cafe_machine: { x: 1.8, z: 0.5 } } });
  assert.deepEqual(zoneCenterOf(l, "cafe"), [0, 5]);
  assert.deepEqual(fixtureWorld(l, "cafe_machine"), { x: 1.8, z: 5.5, rot: 0 });
  // while the room is dragged its ghost center is used
  assert.deepEqual(fixtureWorld(l, "cafe_machine", [10, 0]), { x: 11.8, z: 0.5, rot: 0 });
});

test("small things rest on the counter or the high table, or on the floor when nothing is under them", () => {
  const l = L();
  assert.deepEqual(restOn(l, "cafe_machine"), { on: "cafe_counter", y: 0.95 });
  assert.deepEqual(restOn(l, "cafe_mug_2"), { on: "cafe_counter", y: 0.95 });
  assert.deepEqual(restOn(l, "cafe_pot"), { on: "cafe_counter", y: 0.95 });
  assert.deepEqual(restOn(l, "cafe_table_mug"), { on: "cafe_table", y: 0.97 });
  assert.deepEqual(restOn(l, "cafe_plant"), { on: null, y: 0 }); // floor objects never rest on anything
  assert.deepEqual(restOn(l, "cafe_mug_1", { x: -0.6, z: 0 }), { on: "cafe_table", y: 0.97 });
  assert.deepEqual(restOn(l, "cafe_mug_1", { x: 0, z: 1.5 }), { on: null, y: 0 });
  // the counter taken away: the machine stands on the floor (nothing floats)
  assert.deepEqual(restOn(hideFixture(l, "cafe_counter"), "cafe_machine"), { on: null, y: 0 });
});

test("moving or turning a surface carries what rests on it; other things stay", () => {
  const l = L();
  assert.deepEqual(carriedBy(l, "cafe_counter").sort(), ["cafe_machine", "cafe_mug_1", "cafe_mug_2", "cafe_mug_3", "cafe_pot"]);
  const moved = moveFixture(l, "cafe_counter", { x: 0.75, z: 0, rot: 0 });
  assert.ok(near(fixturePose(moved, "cafe_machine").x, 0.8) && near(fixturePose(moved, "cafe_machine").z, -0.9));
  assert.deepEqual(fixturePose(moved, "cafe_table"), { ...fixturePose(l, "cafe_table") }); // not on the counter
  assert.deepEqual(restOn(moved, "cafe_machine"), { on: "cafe_counter", y: 0.95 });
  // a quarter turn about its own center: (1.8, -0.9) relative (0.05, -0.9) -> (-0.9, -0.05)
  const turned = moveFixture(l, "cafe_counter", { x: 1.75, z: 0, rot: Math.PI / 2 });
  const m = fixturePose(turned, "cafe_machine");
  assert.ok(near(m.x, 1.75 - 0.9) && near(m.z, -0.05) && near(m.rot, Math.PI / 2), JSON.stringify(m));
  // moving a mug moves only the mug
  const one = moveFixture(l, "cafe_mug_1", { x: -0.6, z: 0.2, rot: 0 });
  assert.deepEqual(fixturePose(one, "cafe_mug_2"), fixturePose(l, "cafe_mug_2"));
  assert.deepEqual(one.fixtures.cafe_mug_1, { x: -0.6, z: 0.2 });
});

test("take away and put back keeps the spot; the layout stays minimal", () => {
  const l = L();
  const h = hideFixture(l, "meeting_seat_3");
  assert.equal(fixturePose(h, "meeting_seat_3").hidden, true);
  assert.equal(fixtureWorld(h, "meeting_seat_3"), null);
  const back = showFixture(h, "meeting_seat_3");
  assert.equal(back.fixtures.meeting_seat_3, undefined); // back at its built-in spot: nothing stored
  const mv = moveFixture(l, "meeting_seat_3", { x: 1, z: 1, rot: 0 });
  assert.deepEqual(showFixture(hideFixture(mv, "meeting_seat_3"), "meeting_seat_3").fixtures.meeting_seat_3, { x: 1, z: 1 });
});

test("the built-in arrangement fits: inside its room, no two parts of the same level overlapping", () => {
  const l = L();
  for (const z of ZONE_NAMES) {
    const r = bounds(roomRect(z));
    for (const id of ROOM_PARTS[z]) {
      const box = partFootprint(id, fixturePose(l, id));
      const b = bounds(box);
      assert.ok(b.x0 >= r.x0 - 0.3 && b.x1 <= r.x1 + 0.3 && b.z0 >= r.z0 - 0.3 && b.z1 <= r.z1 + 0.3, `${id} inside ${z}`);
      for (const o of partNeighbors(l, id, fixturePose(l, id))) assert.ok(!overlap(box, o), `${id} overlaps`);
    }
  }
});

test("collisions only between things on the same level", () => {
  const l = L();
  const mugAt = { x: 1.8, z: -0.9, rot: 0 }; // onto the coffee machine
  assert.ok(partNeighbors(l, "cafe_mug_1", mugAt).some((b) => overlap(partFootprint("cafe_mug_1", mugAt), b)));
  // a mug on the counter never collides with the counter itself nor with floor things
  const onCounter = partNeighbors(l, "cafe_mug_1", { x: 1.7, z: -0.3, rot: 0 });
  assert.equal(onCounter.length, 4); // the other mugs, the pot and the machine
  // the counter does not collide with what it carries, but does with the high table
  const counter = partNeighbors(l, "cafe_counter", { x: -0.6, z: 0, rot: 0 });
  assert.ok(counter.some((b) => overlap(partFootprint("cafe_counter", { x: -0.6, z: 0, rot: 0 }), b)));
  assert.ok(!partNeighbors(l, "cafe_counter", fixturePose(l, "cafe_counter")).some((b) => overlap(partFootprint("cafe_counter", fixturePose(l, "cafe_counter")), b)));
  // the wall screen collides with nothing
  assert.deepEqual(partNeighbors(l, "meeting_screen", { x: 0, z: 0, rot: 0 }), []);
  // a removed seat is not an obstacle
  const h = hideFixture(l, "meeting_seat_1");
  const s1 = fixturePose(l, "meeting_seat_1");
  assert.ok(!partNeighbors(h, "meeting_seat_2", s1).some((b) => overlap(partFootprint("meeting_seat_2", s1), b)));
});

test("normalizeLayout keeps known parts and drops unknown ids", () => {
  const l = normalizeLayout({ v: 1, fixtures: { meeting_table: { x: 1, z: -1, rot: 1.5, hidden: true }, statue: { x: 0, z: 0 }, cafe_mug_9: { x: 0, z: 0 } } });
  assert.deepEqual(l.fixtures, { meeting_table: { x: 1, z: -1, rot: 1.5, hidden: true } });
  assert.deepEqual(DEFAULT_LAYOUT.fixtures, {});
  assert.equal(FIXTURES.cafe_counter.surface, 0.95);
});
