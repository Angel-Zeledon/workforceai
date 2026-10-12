import { expect, test } from "@playwright/test";
import { api, resetDemo, sleep } from "../support/api";
import { SCENARIO_TEXT } from "../support/env";

/**
 * Standing per-project approvals (A1): "always allow this kind of action in this project" for INTERNAL,
 * REVERSIBLE actions only. Runs on the demo stack (simulated runtime):
 *  - a hired project manager works in approve-each mode, so each drive write of its project tasks needs a
 *    person; the person ticks "always allow" once and the next write is approved automatically (audited,
 *    revocable from the project screen);
 *  - an external action (the $50,000 proposal email) is never offered the choice and the server refuses it
 *    even when a client forces it.
 */

interface Approval {
  id: string; status: string; action: string; tool?: string; project_id?: string; standing_eligible?: boolean;
  decisions?: { by: string }[];
}
interface Standing { id: string; status: string; tool: string; action: string; use_count: number }

const list = <T,>(x: T[] | { items?: T[] } | null): T[] => (Array.isArray(x) ? x : x?.items ?? []);

test.beforeEach(async () => {
  await resetDemo();
});

test("always allow an internal action: the next one is automatic and the permission can be revoked", async ({ page }) => {
  test.setTimeout(300_000);
  const hired = await api.post<{ agent: { id: string } }>("/agents/from-template", { template_id: "project_manager", locale: "es" });
  const agent = hired.agent.id;

  const tpl = await api.post<{ id: string }>("/project-templates", {
    name: `E2E permiso permanente ${Date.now()}`, description: "Dos tareas seguidas del mismo agente", category: "operations", shared: true,
    params: [],
    objectives: [{ key: "o1", title: "Preparar", workflows: [{ key: "w1", title: "Borradores", nodes: [
      { key: "a", title: "Redactar el plan semanal", agent, complexity: "S" },
      { key: "b", title: "Redactar el resumen semanal", agent, complexity: "S", deps: ["a"] },
    ] }] }],
  });
  const { project_id } = await api.post<{ project_id: string }>("/projects/draft", { template_id: tpl.id, params: {}, locale: "es" });
  await api.post(`/projects/${project_id}/launch`, { approved_budget_usd: 50, acknowledge_underbudget: true });

  // The first drive write of the project asks a person, and the card offers the permanent choice.
  await page.goto("/");
  const always = page.locator('[data-testid^="approval-always-"]:not([data-testid^="approval-always-any-"])').first();
  await expect(always).toBeVisible({ timeout: 120_000 });
  const approvalId = (await always.getAttribute("data-testid"))!.replace("approval-always-", "");
  await always.check();
  await expect(page.getByTestId(`approval-always-any-${approvalId}`)).toBeVisible();
  await page.getByTestId(`approval-${approvalId}`).getByRole("button", { name: /aprobar|approve/i }).click();

  // The second write is approved by the standing permission: nobody is asked, and it says who decided.
  let auto: Approval | undefined;
  await expect
    .poll(async () => {
      const all = list(await api.get<Approval[] | { items: Approval[] }>("/approvals"));
      auto = all.find((a) => a.id !== approvalId && a.status === "approved" && a.decisions?.[0]?.by.startsWith("standing:"));
      return auto?.id ?? "";
    }, { timeout: 120_000, intervals: [1000] })
    .not.toBe("");
  expect(auto!.tool).toBe("fs");
  expect(auto!.project_id).toBe(project_id);
  expect(list(await api.get<Approval[] | { items: Approval[] }>("/approvals?status=pending")).filter((a) => a.id !== approvalId)).toHaveLength(0);

  // The project screen lists it with its use count and revokes it.
  const standing = list(await api.get<Standing[] | null>(`/projects/${project_id}/standing-approvals`));
  expect(standing).toHaveLength(1);
  expect(standing[0]).toMatchObject({ status: "active", tool: "fs", action: "write" });
  expect(standing[0].use_count).toBeGreaterThanOrEqual(1);

  await page.getByTestId("nav-projects").click();
  await page.getByTestId(`project-card-${project_id}`).click();
  await page.getByTestId("pv-tab-approvals").click();
  await expect(page.getByTestId(`standing-${standing[0].id}`)).toHaveAttribute("data-status", "active");
  await page.getByTestId(`standing-revoke-${standing[0].id}`).click();
  await expect(page.getByTestId(`standing-${standing[0].id}`)).toHaveAttribute("data-status", "revoked");
  const after = list(await api.get<Standing[] | null>(`/projects/${project_id}/standing-approvals`));
  expect(after[0].status).toBe("revoked");

  // The audit trail records the creation, the automatic approval(s) and the revocation.
  const audit = list(await api.get<{ action: string }[] | { items: { action: string }[] }>("/audit?limit=500")).map((e) => e.action);
  expect(audit).toEqual(expect.arrayContaining(["standing_approval.created", "approval.auto_approved", "standing_approval.revoked"]));
  await sleep(500);
});

test("an external action is never offered the permanent choice and the server refuses it when forced", async ({ page }) => {
  test.setTimeout(180_000);
  await page.goto("/");
  await page.getByTestId("command-input").fill(SCENARIO_TEXT);
  await page.getByTestId("command-submit").click();
  const card = page.locator('[data-testid^="approval-"]:not([data-testid^="approval-always"])').first();
  await expect(card).toBeVisible({ timeout: 90_000 });
  // No checkbox, whatever the action is (send_proposal is external).
  await expect(page.locator('[data-testid^="approval-always-"]')).toHaveCount(0);

  const pending = list(await api.get<Approval[] | { items: Approval[] }>("/approvals?status=pending"));
  expect(pending.length).toBeGreaterThan(0);
  expect(pending.every((a) => !a.standing_eligible)).toBe(true);
  // A forged client asking for "always allow" gets the approval but no permission.
  const res = await api.post<{ status: string; standing?: unknown; standing_error?: string }>(`/approvals/${pending[0].id}/decision`, { decision: "approve", always_allow: true });
  expect(res.status).toBe("approved");
  expect(res.standing).toBeUndefined();
  expect(res.standing_error).toBeTruthy();
});
