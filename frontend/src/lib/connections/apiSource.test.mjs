// Unit tests of the custom_api source form helpers (run: npm run test:unit).
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  draftToSource, emptyEndpoint, emptySourceDraft, hasErrors, normalizeEndpointName, parseParams, pathParams, sourceErrorCode, sourceToDraft, validateSource,
} from "./apiSource.ts";

const draft = (over = {}) => ({ ...emptySourceDraft(), base_url: "https://api.example.com/v1", ...over });
const ep = (over = {}) => ({ ...emptyEndpoint(), name: "stock", path: "/products/{sku}/stock", ...over });

test("a minimal source (base URL, bearer, no endpoints) is valid", () => {
  const e = validateSource(draft());
  assert.equal(hasErrors(e), false);
  assert.deepEqual(draftToSource(draft()), { base_url: "https://api.example.com/v1", auth: { style: "bearer" }, endpoints: [] });
});

test("base URL must be an absolute http(s) URL", () => {
  assert.equal(validateSource(draft({ base_url: "" })).base_url, "base_url_required");
  assert.equal(validateSource(draft({ base_url: "api.example.com" })).base_url, "base_url_scheme");
  assert.equal(validateSource(draft({ base_url: "ftp://api.example.com" })).base_url, "base_url_scheme");
  assert.equal(validateSource(draft({ base_url: "https://api example.com" })).base_url, "base_url_invalid");
  assert.equal(validateSource(draft({ base_url: "  http://localhost:8080/api  " })).base_url, undefined);
});

test("header and query styles need a name; bearer ignores it", () => {
  assert.equal(validateSource(draft({ auth_style: "header" })).auth_name, "auth_name_required");
  assert.equal(validateSource(draft({ auth_style: "query", auth_name: "api key" })).auth_name, "auth_name_invalid");
  assert.equal(validateSource(draft({ auth_style: "header", auth_name: "X-API-Key" })).auth_name, undefined);
  assert.deepEqual(draftToSource(draft({ auth_style: "bearer", auth_name: "leftover" })).auth, { style: "bearer" });
  assert.deepEqual(draftToSource(draft({ auth_style: "query", auth_name: " api_key " })).auth, { style: "query", name: "api_key" });
  // the step that does not show the auth fields can ignore their error
  assert.equal(hasErrors(validateSource(draft({ auth_style: "header" })), { ignoreAuth: true }), false);
});

test("description and endpoint description limits count characters", () => {
  assert.equal(validateSource(draft({ description: "á".repeat(500) })).description, undefined);
  assert.equal(validateSource(draft({ description: "a".repeat(501) })).description, "description_too_long");
  assert.equal(validateSource(draft({ endpoints: [ep({ description: "x".repeat(301) })] })).rows[0].description, "endpoint_description_too_long");
});

test("endpoint names follow ^[a-z][a-z0-9_]{0,39}$ and are unique", () => {
  const rows = validateSource(draft({ endpoints: [ep(), ep({ path: "/x" }), ep({ name: "Stock" }), ep({ name: "1st" }), ep({ name: "" }), ep({ name: "a".repeat(41) })] })).rows;
  assert.deepEqual(rows.map((r) => r.name), [undefined, "name_duplicate", "name_invalid", "name_invalid", "name_required", "name_invalid"]);
  assert.equal(normalizeEndpointName("Stock por SKU"), "stock_por_sku");
  assert.equal(normalizeEndpointName("open-orders"), "open_orders");
});

test("endpoint paths start with / and have well-formed placeholders", () => {
  const paths = ["", "products", "/a b", "/items/{id", "/items/{}", "/items/{ok}/x}", "/items/{id}/sub/{sub_id}"];
  assert.deepEqual(validateSource(draft({ endpoints: paths.map((p, i) => ep({ name: `e${i}`, path: p })) })).rows.map((r) => r.path),
    ["path_required", "path_slash", "path_invalid", "path_invalid", "path_invalid", "path_invalid", undefined]);
});

test("blank endpoint rows are ignored and dropped; more than 30 is an error", () => {
  const e = validateSource(draft({ endpoints: [emptyEndpoint(), ep()] }));
  assert.equal(hasErrors(e), false);
  assert.equal(draftToSource(draft({ endpoints: [emptyEndpoint(), ep()] })).endpoints.length, 1);
  const many = Array.from({ length: 31 }, (_, i) => ep({ name: `e${i}`, path: `/e/${i}` }));
  assert.equal(validateSource(draft({ endpoints: many })).endpoints, "too_many_endpoints");
  assert.equal(validateSource(draft({ endpoints: many.slice(0, 30) })).endpoints, undefined);
});

test("params: comma separated, deduplicated, and path placeholders always included", () => {
  assert.deepEqual(parseParams(" sku, warehouse,,sku ,"), ["sku", "warehouse"]);
  assert.deepEqual(pathParams("/a/{x}/b/{y}/{x}"), ["x", "y"]);
  const src = draftToSource(draft({ endpoints: [ep({ params: "warehouse, sku", description: "  Stock de un producto  " })] }));
  assert.deepEqual(src.endpoints[0], { name: "stock", path: "/products/{sku}/stock", description: "Stock de un producto", params: ["sku", "warehouse"] });
});

test("source round-trips through the draft", () => {
  const src = {
    base_url: "https://erp.example.com/api", auth: { style: "header", name: "X-API-Key" }, description: "Inventario, pedidos y clientes del ERP",
    endpoints: [{ name: "stock", path: "/products/{sku}/stock", description: "Stock por SKU", params: ["sku", "warehouse"] }],
  };
  assert.deepEqual(draftToSource(sourceToDraft(src)), src);
  assert.deepEqual(sourceToDraft(undefined), emptySourceDraft());
});

test("sourceErrorCode (mock server check) mirrors the contract", () => {
  assert.equal(sourceErrorCode(undefined), "source_required");
  assert.equal(sourceErrorCode({ base_url: "https://x.test", auth: { style: "bearer" } }), null);
  assert.equal(sourceErrorCode({ base_url: "https://x.test", auth: { style: "cookie" } }), "auth_name_invalid");
  assert.equal(sourceErrorCode({ base_url: "https://x.test", auth: { style: "query" } }), "auth_name_required");
  assert.equal(sourceErrorCode({ base_url: "https://x.test", auth: { style: "bearer" }, endpoints: [{ name: "", path: "" }] }), "name_required");
  assert.equal(sourceErrorCode({ base_url: "x.test", auth: { style: "bearer" } }), "base_url_scheme");
});
