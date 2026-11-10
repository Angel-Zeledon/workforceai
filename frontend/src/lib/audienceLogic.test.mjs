import assert from "node:assert/strict";
import { test } from "node:test";
import { audienceOf } from "./audienceLogic.ts";

test("only the mock demo is company on its own", () => {
  assert.equal(audienceOf(null, true), "company");
  assert.equal(audienceOf(null, false), null);
  assert.equal(audienceOf(undefined, false), null);
});
test("outside the mock the welcome decides, empty means today's menu", () => {
  assert.equal(audienceOf({ audience: "company" }, false), "company");
  assert.equal(audienceOf({ audience: "team" }, false), "team");
  assert.equal(audienceOf({ audience: "" }, false), null);
});
