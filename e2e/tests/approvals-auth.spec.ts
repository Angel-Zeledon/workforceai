import { devices, expect, test } from "@playwright/test";
import { API_URL, SCENARIO_TEXT } from "../support/env";

/**
 * PWA approvals with accounts on (A6 + A2). Needs a stack with AUTH_ENABLED=true, so it only runs with E2E_AUTH=1:
 *   E2E_AUTH=1 FRONTEND_URL=http://localhost:3000 API_URL=http://localhost:8080/api/v1 npx playwright test approvals-auth.spec.ts
 * The open demo (auth off) path of /approvals is unchanged and covered without login.
 */
test.skip(process.env.E2E_AUTH !== "1", "requires a stack with AUTH_ENABLED=true (E2E_AUTH=1)");
test.use({ ...devices["Pixel 7"] });

const pw = "Correct-Horse-9-e2e";
const uniq = () => Math.random().toString(36).slice(2, 8);
const SEED_ROLES = ["sales", "hr", "legal", "accounting", "analyst", "operations", "assistant"];

interface Session { access_token: string; user: { id: string; email: string }; org_id: string }
interface Approval { id: string; status: string; decisions?: { by: string; role?: string }[] }

async function call<T>(method: string, path: string, token?: string, body?: unknown): Promise<T> {
  const res = await fetch(`${API_URL}${path}`, {
    method,
    headers: { "content-type": "application/json", ...(token ? { authorization: `Bearer ${token}` } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  if (!res.ok) throw new Error(`${method} ${path} -> ${res.status} ${text}`);
  return (text ? JSON.parse(text) : {}) as T;
}

const list = <T>(x: T[] | { items?: T[] } | null): T[] => (Array.isArray(x) ? x : x?.items ?? []);

/** A fresh organization with the seed office and one pending approval (simulated runtime). */
async function orgWithPendingApproval(orgName: string) {
  const email = `pwa-${uniq()}@example.com`;
  const s = await call<Session>("POST", "/auth/register", undefined, { email, password: pw, name: "Approver E2E", org_name: orgName });
  for (const id of SEED_ROLES) {
    await call("POST", "/agents/from-template", s.access_token, { template_id: id, locale: "es" }).catch(() => undefined);
  }
  await call("POST", "/requests", s.access_token, { text: SCENARIO_TEXT });
  const deadline = Date.now() + 120_000;
  while (Date.now() < deadline) {
    const pending = list(await call<Approval[] | { items: Approval[] }>("GET", "/approvals?status=pending", s.access_token));
    if (pending.length) return { email, session: s, approval: pending[0] };
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error("no pending approval appeared");
}

test("approve from /approvals/<id> with auth on: login keeps the return path and records the real user", async ({ page }) => {
  const orgName = `Org PWA ${uniq()}`;
  const { email, session, approval } = await orgWithPendingApproval(orgName);

  // A notification link opened without a session goes to the login and comes back afterwards.
  await page.goto(`/approvals/${approval.id}`);
  await expect(page).toHaveURL(new RegExp(`/login\\?next=${encodeURIComponent(`/approvals/${approval.id}`)}$`));
  await page.getByTestId("auth-email").fill(email);
  await page.getByTestId("auth-password").fill(pw);
  await page.getByTestId("auth-submit").click();
  await expect(page).toHaveURL(new RegExp(`/approvals/${approval.id}$`));

  // The screen loads with the bearer token and shows the organization.
  await expect(page.getByTestId("header-org-name")).toHaveText(orgName);
  const card = page.getByTestId(`approval-card-${approval.id}`);
  await expect(card).toBeVisible();
  await expect(card).toHaveAttribute("aria-current", "true");
  await page.getByTestId(`approval-approve-${approval.id}`).click();
  await expect(card).toHaveCount(0);

  // The decision carries the signed-in user, not a generic "user".
  const all = list(await call<Approval[] | { items: Approval[] }>("GET", "/approvals", session.access_token));
  const decided = all.find((a) => a.id === approval.id);
  expect(decided?.status).not.toBe("pending");
  expect(decided?.decisions?.[0]?.by).toBe(session.user.id);

  // A reload restores the session (refresh flow) instead of sending the user to the login.
  await page.reload();
  await expect(page.getByTestId("approvals-page")).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`/approvals/${approval.id}$`));
});

test("the login ignores return paths outside the app", async ({ page }) => {
  const email = `next-${uniq()}@example.com`;
  await call("POST", "/auth/register", undefined, { email, password: pw, name: "Next E2E", org_name: `Org ${uniq()}` });
  await page.goto(`/login?next=${encodeURIComponent("//evil.example.com/approvals")}`);
  await page.getByTestId("auth-email").fill(email);
  await page.getByTestId("auth-password").fill(pw);
  await page.getByTestId("auth-submit").click();
  await expect.poll(() => new URL(page.url()).pathname).toBe("/");
  expect(new URL(page.url()).host).not.toContain("evil.example.com");
});
