import { test, expect } from "@playwright/test";
import { api, resetDemo, sleep } from "../support/api";

/**
 * Customizable work templates on the real stack (simulated runtime): an organization template with a
 * block repeated per country, a human gate and its own parameters is created, previewed, versioned,
 * used to create a project that runs to completion, and every agent task leaves its deliverable as a
 * file in the project folder of the drive.
 */

interface ProjectSummary { id: string; name: string; status: string; tasks_done: number; tasks_total: number; failed?: number }
interface Listing { folders: { name: string; path: string }[]; files: { name: string; updated_by: { kind: string; id: string } }[] }
interface Tpl { id: string; name: string; version: number; stats?: { tasks: number; tasks_max: number; gates: number } }

const campaign = (name: string): Record<string, any> => ({
  name, description: "E2E: una campaña por país con revisión legal y compuerta", category: "sales", shared: true,
  params: [
    { key: "countries", label: "Países", type: "list", item_name: "country", default: "México, Chile, Perú", max: 10 },
    { key: "product", label: "Producto", type: "text", default: "Plan Pro" },
  ],
  objectives: [
    { key: "o1", title: "Preparar {product}", workflows: [{ key: "w1", title: "Base", nodes: [
      { key: "brief", title: "Definir el mensaje de {product}", agent: "sales", complexity: "M", acceptance: ["Incluye la propuesta de valor"] },
    ] }] },
    { key: "o2", title: "Campaña por país", workflows: [{ key: "wc", title: "{country}", repeat: ["countries"], nodes: [
      { key: "copy", title: "Redactar la campaña de {product} para {country}", agent: "assistant", complexity: "S", deps: ["brief"] },
      { key: "law", title: "Revisar las normas de publicidad en {country}", agent: "legal", complexity: "S", deps: ["brief"] },
      { key: "ok", kind: "gate", title: "Aprobación de la campaña en {country}", deps: ["copy", "law"], approval: { action: "approve_campaign", risk: "medium" } },
    ] }] },
    { key: "o3", title: "Cierre", workflows: [{ key: "wf", title: "Informe", nodes: [
      { key: "report", title: "Informe consolidado de {product}", agent: "assistant", complexity: "M", deps: ["ok"] },
    ] }] },
  ],
});

async function runToDone(projectId: string, timeoutMs = 240_000): Promise<ProjectSummary> {
  let p: ProjectSummary | null = null;
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    for (const a of (await api.get<{ id: string }[] | null>("/approvals?status=pending")) ?? []) {
      await api.post(`/approvals/${a.id}/decision`, { decision: "approve", comment: "e2e" });
    }
    p = (await api.get<{ project: ProjectSummary }>(`/projects/${projectId}`)).project;
    if (["done", "failed", "cancelled"].includes(p.status)) break;
    await sleep(2000);
  }
  return p!;
}

async function projectFolder(p: ProjectSummary, projectId: string) {
  const projects = await api.get<Listing>(`/drive/list?path=${encodeURIComponent("/Projects")}`);
  // another project with the same name (an earlier run) makes the drive disambiguate with the end of the id
  const folder = projects.folders.find((f) => f.name === `${p.name} (${projectId.slice(-6)})`) ?? projects.folders.find((f) => f.name === p.name);
  expect(folder).toBeTruthy();
  return (await api.get<Listing>(`/drive/list?path=${encodeURIComponent(folder!.path)}`)).files;
}

test.beforeEach(async () => {
  await resetDemo();
});

test("an organization template: validate, preview, version, run to completion, deliverables in the drive", async () => {
  test.setTimeout(300_000);
  const name = `E2E campaña ${Date.now()}`;

  // an invalid template is explained, not saved
  const bad = campaign(name);
  bad.objectives[1].workflows[0].nodes[0].deps = ["nope"];
  const v = await api.post<{ ok: boolean; issues: { code: string }[] }>("/project-templates/validate", bad);
  expect(v.ok).toBe(false);
  expect(v.issues.map((i) => i.code)).toContain("unknown_dependency");

  // create it; the library knows its size at the default and at the maximum parameters
  const tpl = await api.post<Tpl>("/project-templates", campaign(name));
  expect(tpl.version).toBe(1);
  expect(tpl.stats).toMatchObject({ tasks: 1 + 3 * 3 + 1, tasks_max: 1 + 3 * 10 + 1, gates: 3 });
  const lib = await api.get<Tpl[]>("/project-templates");
  expect(lib.map((t) => t.id)).toEqual(expect.arrayContaining([tpl.id, "tpl-annual-budget", "tpl-erp-migration", "tpl-year-end-audit"]));

  // preview before creating anything
  const params = { countries: "Chile, Perú, Colombia, España", product: "Zeta" };
  const pv = await api.post<{ tasks: number; gates: number; estimate: { total: { p50_usd: number } } }>("/project-templates/preview", { template_id: tpl.id, params });
  expect(pv.tasks).toBe(1 + 3 * 4 + 1);
  expect(pv.gates).toBe(4);
  expect(pv.estimate.total.p50_usd).toBeGreaterThan(0);

  // a second version, then the first one is restored
  const v2 = await api.put<Tpl>(`/project-templates/${tpl.id}`, { ...campaign(name + " v2"), base_version: 1, note: "rename" });
  expect(v2.version).toBe(2);
  const versions = (await api.get<{ items?: { version: number }[] } | { version: number }[]>(`/project-templates/${tpl.id}/versions`));
  expect((Array.isArray(versions) ? versions : versions.items ?? []).map((x) => x.version)).toEqual([2, 1]);
  const back = await api.post<Tpl>(`/project-templates/${tpl.id}/versions/1/restore`);
  expect(back.version).toBe(3);
  expect(back.name).toBe(name);

  // a project from the template, launched and run to completion with the gates approved
  const { project_id } = await api.post<{ project_id: string }>("/projects/draft", { template_id: tpl.id, params, locale: "es" });
  await api.post(`/projects/${project_id}/launch`, { approved_budget_usd: 100, acknowledge_underbudget: true, max_parallel: 12 });
  const p = await runToDone(project_id);
  expect(p.status).toBe("done");
  expect(p.failed ?? 0).toBe(0);
  expect(p.tasks_done).toBe(p.tasks_total);
  expect(p.tasks_total).toBe(14);

  // the customized titles are the ones that ran, and each agent task left its file (gates have none)
  const detail = await api.get<{ nodes: { title: string; kind: string }[] }>(`/projects/${project_id}`);
  const titles = detail.nodes.map((n) => n.title);
  expect(titles).toContain("Redactar la campaña de Zeta para Colombia");
  expect(titles).toContain("Revisar las normas de publicidad en España");
  const files = (await projectFolder(p, project_id)).filter((f) => f.updated_by.kind === "agent");
  expect(files.length).toBeGreaterThanOrEqual(1 + 2 * 4 + 1);

  // the finished project can be saved as a new template, and an admin deletes both
  const saved = await api.post<Tpl>(`/projects/${project_id}/save-as-template`, { name: `${name} (guardada)`, category: "sales", shared: true });
  expect(saved.version).toBe(1);
  await api.del(`/project-templates/${saved.id}`);
  await api.del(`/project-templates/${tpl.id}`);
  const after = await api.get<Tpl[]>("/project-templates");
  expect(after.some((t) => t.id === tpl.id || t.id === saved.id)).toBe(false);
});

test("a giant built-in template at a reduced size runs to completion", async () => {
  test.setTimeout(300_000);
  const { project_id } = await api.post<{ project_id: string }>("/projects/draft", {
    template_id: "tpl-erp-migration", locale: "es",
    params: { streams: "Finanzas, Compras", waves: "Piloto, Ola 1" },
  });
  await api.post(`/projects/${project_id}/launch`, { approved_budget_usd: 200, acknowledge_underbudget: true, max_parallel: 16 });
  const p = await runToDone(project_id, 270_000);
  expect(p.status).toBe("done");
  expect(p.failed ?? 0).toBe(0);
  expect(p.tasks_done).toBe(p.tasks_total);
  const files = (await projectFolder(p, project_id)).filter((f) => f.updated_by.kind === "agent");
  expect(files.length).toBeGreaterThanOrEqual(30);
});

test("UI: duplicate a built-in, customize it in the builder, preview it, run it and save the result", async ({ page }) => {
  test.setTimeout(420_000);
  const name = `E2E lanzamiento ${Date.now()}`;

  await page.goto("/");
  await page.getByTestId("nav-projects").click();
  await page.getByTestId("template-library-open").click();
  await expect(page.getByTestId("template-library")).toBeVisible();
  // search and category narrow the library
  await page.getByTestId("tpl-search").fill("lanzamiento");
  await expect(page.getByTestId("template-card-tpl-product-launch")).toBeVisible();
  await expect(page.getByTestId("template-card-tpl-year-end-audit")).toHaveCount(0);
  await page.getByTestId("tpl-search").fill("");
  await page.getByTestId("tpl-cat-compliance").click();
  await expect(page.getByTestId("template-card-tpl-compliance-program")).toBeVisible();
  await expect(page.getByTestId("template-card-tpl-product-launch")).toHaveCount(0);
  await page.getByTestId("tpl-cat-all").click();

  // duplicate the built-in: an editable copy opens in the builder
  await page.getByTestId("tpl-duplicate-tpl-product-launch").click();
  await expect(page.getByTestId("template-builder")).toBeVisible();
  await page.getByTestId("tb-name").fill(name);
  // add one more task to the per-market block (phase 2, workflow 1) and rename the first one
  const markets = page.getByTestId("tb-wf-1-0-title");
  await expect(markets).toBeVisible();
  await page.getByTestId("tb-add-task-1-0").click();
  const extra = "tb-task-1-0-7";
  await page.getByTestId(`${extra}-title`).fill("Revisión extra de {market}");
  await page.getByTestId(`${extra}-agent`).selectOption("legal");
  await page.getByTestId("tb-task-0-0-0-title").fill("Definir el posicionamiento personalizado");
  // the live preview of the unsaved draft: 4 markets and 3 channels by default + the extra task per market
  await expect(page.getByTestId("tb-preview-tasks")).toHaveText(String(67 + 4), { timeout: 20_000 });
  await page.getByTestId("tb-validate").click();
  await page.getByTestId("tb-save").click();
  await expect(page.getByTestId("tb-saved")).toBeVisible();

  // use it: two markets and one channel
  await page.getByTestId("tb-back").click();
  await page.getByTestId("tpl-search").fill(name);
  const card = page.locator('[data-testid^="template-card-"]').filter({ hasText: name });
  await expect(card).toHaveCount(1);
  await card.locator('[data-testid^="tpl-use-"]').click();
  await page.getByTestId("wizard-param-markets").fill("Chile, Perú");
  await page.getByTestId("wizard-param-channels").fill("Tienda en línea");
  // global 5 + 2 x (7 + 1 extra) + 2 x 1 x 2 channel tasks + go/no-go 1 + 2 x 2 launch tasks + report 1
  await expect(page.getByTestId("wizard-preview-tasks")).toHaveText("31", { timeout: 20_000 });
  await page.getByTestId("wizard-create").click();
  await expect(page.getByTestId("draft-editor")).toBeVisible();
  await expect(page.getByText("Revisión extra de Perú").first()).toBeVisible();
  await page.getByTestId("launch-confirm").click();
  await expect(page.getByTestId("project-status")).not.toHaveAttribute("data-status", "draft", { timeout: 30_000 });

  // it runs to completion with the go/no-go gate approved by a person
  const list = await api.get<ProjectSummary[]>("/projects");
  const mine = list.find((p) => p.name.includes(name));
  expect(mine).toBeTruthy();
  const p = await runToDone(mine!.id);
  expect(p.status).toBe("done");
  expect(p.failed ?? 0).toBe(0);
  expect(p.tasks_done).toBe(31);
  const files = (await projectFolder(p, mine!.id)).filter((f) => f.updated_by.kind === "agent");
  expect(files.length).toBeGreaterThanOrEqual(30);
  const detail = await api.get<{ nodes: { title: string }[] }>(`/projects/${mine!.id}`);
  expect(detail.nodes.map((n) => n.title)).toContain("Revisión extra de Chile");

  // save the finished project as a template, from the project screen
  await page.getByTestId(`project-card-${mine!.id}`).click().catch(() => {});
  await page.getByTestId("save-as-template").click();
  await page.getByTestId("save-template-name").fill(`${name} (guardada)`);
  await page.getByTestId("save-template-confirm").click();
  await expect(page.getByTestId("save-template-done")).toBeVisible();

  // cleanup: an admin deletes the templates this test created
  for (const t of (await api.get<Tpl[]>("/project-templates")).filter((x) => x.name.startsWith(name))) await api.del(`/project-templates/${t.id}`);
});
