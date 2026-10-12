import { test, expect } from "@playwright/test";
import { api, resetDemo, sleep } from "../support/api";

/**
 * H1 on the simulated stack (no LLM): a group consolidated close of 3 subsidiaries asks for its
 * data first ("Antes de empezar"), launch is blocked until it is there, the sample data lands in
 * /Projects/<project>/Entradas and a task output cites one of the input files.
 */

interface Detail { project: { id: string; name: string; status: string; tasks_done: number; tasks_total: number }; nodes: { kind: string; task_id?: string; title: string }[] }
interface Listing { folders: { name: string; path: string }[]; files: { name: string; path: string }[] }

test.beforeEach(async () => {
  await resetDemo();
});

test("group close: required inputs, sample data, launch, files in Entradas and a task that cites them", async ({ page }) => {
  test.setTimeout(420_000);
  await page.goto("/projects");
  await page.getByTestId("project-new").click();
  await page.getByTestId("wizard-template").selectOption({ value: "tpl-group-consolidated-close" });
  await page.getByTestId("wizard-param-subsidiaries").fill("3");
  await page.getByTestId("wizard-create").click();
  await expect(page.getByTestId("draft-editor")).toBeVisible();

  // the step lists the required inputs and launch is blocked with a clear message
  const card = page.getByTestId("inputs-card");
  await expect(card).toBeVisible();
  await expect(page.getByTestId("inputs-status")).toHaveAttribute("data-missing", "4");
  for (const key of ["tb", "intercompany", "fx_rates", "coa_mapping"]) {
    await expect(page.getByTestId(`input-row-${key}`)).toHaveAttribute("data-satisfied", "0");
  }
  await expect(page.getByTestId("input-count-tb")).toContainText(/0 (de|of) 3/);
  await expect(page.getByTestId("launch-blocked-inputs")).toBeVisible();
  await expect(page.getByTestId("launch-confirm")).toBeDisabled();

  // one real upload through the UI counts towards its input
  await page.getByTestId("input-upload-fx_rates").setInputFiles({ name: "mis_tipos.csv", mimeType: "text/csv", buffer: Buffer.from("currency,closing_rate,average_rate\nMXN,17.85,17.76\n") });
  await expect(page.getByTestId("input-row-fx_rates")).toHaveAttribute("data-satisfied", "1");
  // a file type the input does not accept is refused with a message
  await page.getByTestId("input-upload-coa_mapping").setInputFiles({ name: "malware.exe", mimeType: "application/octet-stream", buffer: Buffer.from("x") });
  await expect(page.getByTestId("inputs-error")).toBeVisible();

  // sample data fills only what is still missing
  await page.getByTestId("inputs-use-demo").click();
  await expect(page.getByTestId("inputs-status")).toHaveAttribute("data-ready", "1");
  await expect(page.getByTestId("input-count-tb")).toContainText(/3 (de|of) 3/);
  await expect(page.getByTestId("inputs-demo-used")).toBeVisible();
  await expect(page.getByTestId("input-row-fx_rates").getByTestId("input-file-fx_rates")).toHaveCount(1); // the user's file was kept
  await expect(page.getByTestId("launch-blocked-inputs")).toHaveCount(0);

  await page.getByTestId("launch-confirm").click();
  await expect(page.getByTestId("project-status")).not.toHaveAttribute("data-status", "draft", { timeout: 30_000 });

  const list = await api.get<{ id: string; name: string }[]>("/projects");
  const mine = list[0];
  expect(mine).toBeTruthy();
  const projectId = mine.id;

  // the files are in the drive: /Projects/<project>/Entradas
  const projects = await api.get<Listing>(`/drive/list?path=${encodeURIComponent("/Projects")}`);
  const folder = projects.folders.find((f) => f.name === `${mine.name} (${projectId.slice(-6)})`) ?? projects.folders.find((f) => f.name === mine.name);
  expect(folder).toBeTruthy();
  const entradas = await api.get<Listing>(`/drive/list?path=${encodeURIComponent(folder!.path + "/Entradas")}`);
  const names = entradas.files.map((f) => f.name);
  expect(names.filter((n) => n.startsWith("tb_S0"))).toHaveLength(3);
  expect(names).toEqual(expect.arrayContaining(["intercompany_matrix.csv", "mis_tipos.csv", "coa_mapping.csv", "materiality_policy.md"]));
  expect(names).not.toContain("fx_rates.csv"); // the user's own file satisfied it: the sample did not overwrite it
  const tbFile = entradas.files.find((f) => f.name === "tb_S01_MX.csv")!;
  expect(tbFile).toBeTruthy();

  // the project runs to the end (the CFO gate approved) and a task output cites an input file
  let status = "";
  const deadline = Date.now() + 300_000;
  while (Date.now() < deadline) {
    for (const a of (await api.get<{ id: string }[] | null>("/approvals?status=pending")) ?? []) {
      await api.post(`/approvals/${a.id}/decision`, { decision: "approve", comment: "e2e" });
    }
    status = (await api.get<Detail>(`/projects/${projectId}`)).project.status;
    if (["done", "failed", "cancelled"].includes(status)) break;
    await sleep(2000);
  }
  expect(status).toBe("done");

  const detail = await api.get<Detail>(`/projects/${projectId}`);
  const taskIds = detail.nodes.filter((n) => n.task_id && n.kind === "task").map((n) => n.task_id!);
  let cited = 0;
  for (const id of taskIds) {
    const task = await api.get<{ output: { evidence?: string[] } | null }>(`/tasks/${id}`);
    if ((task.output?.evidence ?? []).some((e) => /Entradas\/(tb_S0\d_\w+|intercompany_matrix|fx_rates|coa_mapping)\.csv/.test(e))) cited++;
  }
  expect(cited).toBeGreaterThanOrEqual(3);
});

test("launching without the required inputs is an explicit choice that is recorded", async () => {
  const { project_id } = await api.post<{ project_id: string }>("/projects/draft", { template_id: "tpl-group-consolidated-close", params: { subsidiaries: "2" }, locale: "es" });
  const blocked = await fetch(`${process.env.API_URL ?? "http://localhost:8080/api/v1"}/projects/${project_id}/launch`, {
    method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ approved_budget_usd: 100, acknowledge_underbudget: true }),
  });
  expect(blocked.status).toBe(409);
  expect(await blocked.text()).toContain("inputs_required");
  await api.post(`/projects/${project_id}/launch`, { approved_budget_usd: 100, acknowledge_underbudget: true, proceed_without_inputs: true });
  const inputs = await api.get<{ waived?: { missing: string[] } }>(`/projects/${project_id}/inputs`);
  expect(inputs.waived?.missing).toEqual(expect.arrayContaining(["tb", "fx_rates"]));
});
