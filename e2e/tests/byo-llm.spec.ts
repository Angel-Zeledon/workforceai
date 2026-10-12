import { expect, test } from "@playwright/test";
import { api, resetDemo } from "../support/api";
import { API_URL, FRONTEND_URL, SCENARIO_TEXT } from "../support/env";
import { adminClick } from "../support/ui";

/**
 * Bring-your-own-model (G3), on the SIMULATION stack: the runtime answers the connection test with a
 * deterministic test double (a made-up key is rejected as invalid_key, `sim-ok-*` is accepted).
 * Checks the whole path: configure OpenAI with a fake key -> the test fails gracefully -> save -> select it ->
 * and the key never appears in any API response, in the page source or in the DOM.
 */
const FAKE = "sk-proj-E2EFAKEKEY-abcdefghijklmnop-1234567890";

test.beforeEach(async () => {
  await resetDemo();
  // The gear-menu entry is "Team settings" once onboarding is completed.
  const packs = await api.get<{ key: string }[] | { items: { key: string }[] }>("/onboarding/packs");
  const list = Array.isArray(packs) ? packs : packs.items;
  await api.post("/onboarding", { pack_key: list[0].key, force: true });
});

test("configure OpenAI with a fake key: invalid_key is explained, selection works, the key never leaks", async ({ page }) => {
  const seen: string[] = [];
  page.on("response", async (r) => {
    if (r.url().includes("/api/v1/")) { try { seen.push(await r.text()); } catch { /* streaming */ } }
  });

  await page.goto("/");
  await adminClick(page, "org-config-open");
  const section = page.getByTestId("llm-section");
  await expect(section).toBeVisible();
  await expect(page.getByTestId("llm-default-badge")).toContainText(/DeepSeek/);
  await expect(page.getByTestId("llm-active-deepseek")).toBeVisible();

  // The key field is a password field with a show/hide toggle.
  await page.getByTestId("llm-config-openai").click();
  const key = page.getByTestId("llm-key-openai");
  await expect(key).toHaveAttribute("type", "password");
  await key.fill(FAKE);
  await page.getByTestId("llm-key-toggle-openai").click();
  await expect(key).toHaveAttribute("type", "text");
  await page.getByTestId("llm-key-toggle-openai").click();
  await expect(page.getByTestId("llm-keylink-openai")).toHaveAttribute("href", /platform\.openai\.com/);

  // "Probar conexion": a made-up key is rejected, gracefully.
  await page.getByTestId("llm-test-openai").click();
  const result = page.getByTestId("llm-test-result-openai");
  await expect(result).toHaveAttribute("data-code", "invalid_key");
  await expect(page.getByTestId("llm-section")).toBeVisible();

  // Save it, then choose it.
  await page.getByTestId("llm-save-openai").click();
  await expect(page.getByTestId("llm-saved-openai")).toContainText("7890");
  await page.getByTestId("llm-use-openai").click();
  await expect(page.getByTestId("llm-active-openai")).toBeVisible();
  await expect(page.getByTestId("llm-default-badge")).toHaveCount(0);

  // The server remembers the choice, and reports only the last 4 characters.
  const view = await api.get<{ active_provider: string; providers: { id: string; configured: boolean; key_last4?: string }[] }>("/settings/llm");
  expect(view.active_provider).toBe("openai");
  expect(view.providers.find((p) => p.id === "openai")).toMatchObject({ configured: true, key_last4: "7890" });

  // The key is nowhere: not in any response the browser saw, not in the DOM or the page source, not in the audit trail.
  expect(seen.join("\n")).not.toContain("E2EFAKEKEY");
  expect(await page.content()).not.toContain("E2EFAKEKEY");
  expect(await page.locator("input").evaluateAll((els) => els.map((e) => (e as HTMLInputElement).value).join("|"))).not.toContain("E2EFAKEKEY");
  const audit = await (await fetch(`${API_URL}/audit?limit=200`)).text();
  expect(audit).toContain("settings.llm_credential_set");
  expect(audit).not.toContain("E2EFAKEKEY");
  expect(await (await fetch(`${API_URL}/settings/llm`)).text()).not.toContain("E2EFAKEKEY");
  const html = await (await fetch(FRONTEND_URL)).text();
  expect(html).not.toContain("E2EFAKEKEY");

  // Back to the included model.
  await page.getByTestId("llm-use-deepseek").click();
  await expect(page.getByTestId("llm-default-badge")).toBeVisible();
  await page.getByTestId("llm-config-openai").click();
  await page.getByTestId("llm-delete-openai").click();
  await expect(page.getByTestId("llm-saved-openai")).toHaveCount(0);
});

test("a good key passes the test and a request still completes on the simulated runtime", async ({ page }) => {
  await page.goto("/");
  await adminClick(page, "org-config-open");
  await page.getByTestId("llm-config-kimi").click();
  await page.getByTestId("llm-key-kimi").fill("sim-ok-kimi-0123456789");
  await page.getByTestId("llm-test-kimi").click();
  await expect(page.getByTestId("llm-test-result-kimi")).toHaveAttribute("data-code", "ok");
  await page.getByTestId("llm-save-kimi").click();
  await page.getByTestId("llm-use-kimi").click();
  await expect(page.getByTestId("llm-active-kimi")).toBeVisible();
  // With the organization's own key selected, work still flows end to end (the key rides the internal hop).
  const { request_id } = await api.post<{ request_id: string }>("/requests", { text: SCENARIO_TEXT });
  await expect.poll(async () => {
    const rows = await api.get<{ id: string; status: string }[] | { items: { id: string; status: string }[] }>("/requests");
    const list = Array.isArray(rows) ? rows : rows.items;
    return list.find((x) => x.id === request_id)?.status;
  }, { timeout: 90_000 }).toMatch(/done|awaiting_approval/);
});
