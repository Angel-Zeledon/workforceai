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

const campaign = (name: string) => ({
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
