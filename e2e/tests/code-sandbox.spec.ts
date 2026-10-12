import { test, expect } from "@playwright/test";
import { api, pendingApprovals, resetDemo, sleep } from "../support/api";

/**
 * F3: agents run code only with a person's consent. Needs the stack with the sandbox profile
 * (`make up-sandbox`); skipped when the server reports that no runner is installed.
 * The simulated analyst asks for code when the request carries the [sim:code] marker.
 */

interface Settings { enabled: boolean; available: boolean }
interface Run { id: string; status: string; code: string; stdout: string; outputs: { name: string; file_ref?: string }[] }

test.beforeEach(async () => {
  await resetDemo();
});

test("an agent asks to run code, a person reads it, approves, and the result lands in Files", async ({ page }) => {
  test.setTimeout(420_000);
  const st = await api.get<Settings>("/settings/code-execution");
  test.skip(!st.available, "sandbox profile not running (make up-sandbox)");
  await fetch(`${process.env.API_URL ?? "http://localhost:8080/api/v1"}/settings/code-execution`, {
    method: "PUT", headers: { "content-type": "application/json" }, body: JSON.stringify({ enabled: false, max_runs_per_day: 0, max_runtime_seconds: 0 }),
  });

  // Off by default: a person turns it on in Security
  await page.goto("/");
  await page.getByTestId("admin-menu").click();
  await page.getByTestId("nav-security").click();
  const toggle = page.getByTestId("ctl-code-enabled");
  await expect(toggle).not.toBeChecked();
  await toggle.click(); // controlled: it flips once the server accepted the change
  await expect(toggle).toBeChecked();
  await expect(page.getByTestId("ctl-code-saved")).toBeVisible();

  // The agent asks for code: an approval with the FULL code appears and nothing has run yet
  const { request_id } = await api.post<{ request_id: string }>("/requests", { text: "Analiza la rentabilidad por región con un análisis de datos [sim:code]" });
  let ap: { id: string } | undefined;
  for (let i = 0; i < 90 && !ap; i++) {
    ap = (await pendingApprovals()).find((a) => a.action === "code.run");
    if (!ap) await sleep(1000);
  }
  expect(ap, "a code.run approval").toBeTruthy();
  const before = await api.get<Run[]>(`/code-runs?approval_id=${ap!.id}`);
  expect(before[0].status).toBe("awaiting_approval");
  expect(before[0].outputs).toHaveLength(0);

  await page.goto("/");
  const card = page.getByTestId(`approval-${ap!.id}`).or(page.getByTestId(`office-approval-card-${ap!.id}`)).first();
  await expect(card).toBeVisible();
  await expect(card.getByTestId("code-run-warning")).toContainText(/contenedor aislado sin red|isolated container with no network/);
  await expect(card.getByTestId("code-listing")).toContainText("out/resumen.csv");
  await expect(card.getByTestId("code-listing")).toContainText("groupby");
  await expect(card.getByTestId("code-run-limits")).toBeVisible();
  await card.getByRole("button", { name: /Aprobar|Approve/ }).click();

  // The container runs pandas; the output is a new file on the agent's desk
  let run: Run | undefined;
  for (let i = 0; i < 300; i++) {
    run = (await api.get<Run[]>(`/code-runs?approval_id=${ap!.id}`))[0];
    if (["succeeded", "failed", "timeout", "aborted", "denied"].includes(run.status)) break;
    await sleep(1000);
  }
  expect(run?.status).toBe("succeeded");
  expect(run!.stdout).toContain("norte");
  const out = run!.outputs.find((o) => o.name === "resumen.csv");
  expect(out?.file_ref).toMatch(/^\/Agents\/analyst\/outputs\/run_/);

  // The request finishes (the agent got the delimited result)
  const done = await api.get<{ status: string }>(`/requests/${request_id}`);
  expect(["running", "done"]).toContain(done.status);

  // Result view + link to the Files screen
  await page.goto("/");
  await page.getByTestId("admin-menu").click();
  await page.getByTestId("nav-security").click();
  const row = page.getByTestId("ctl-code-run").first();
  await row.locator("button").first().click();
  await expect(row.getByTestId("code-run-status")).toBeVisible();
  await expect(row.getByTestId("code-run-stdout")).toContainText("norte");
  await row.getByTestId("code-output-open").first().click();
  await expect(page.getByTestId("drive-view")).toBeVisible();
  await expect(page.getByTestId("drive-file-resumen.csv")).toBeVisible();
});
