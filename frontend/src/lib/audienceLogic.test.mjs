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

import { audienceKey, initialAudience } from "./audienceLogic.ts";
test("initial audience: saved value per org, validated; demo is company", () => {
  assert.equal(initialAudience("company", false), "company");
  assert.equal(initialAudience("team", false), "team");
  assert.equal(initialAudience(null, false), null);
  assert.equal(initialAudience("junk", false), null);
  assert.equal(initialAudience(null, true), "company");
  assert.notEqual(audienceKey("a"), audienceKey("b"));
});
