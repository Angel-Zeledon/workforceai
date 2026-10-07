/*
 * Regenera las capturas reales de la app en landing/img (webp, 1600x1000).
 *
 * Uso (desde la raíz del repo):
 *   node landing/scripts/capture.mjs                       # app en http://localhost:3000
 *   CAPTURE_URL=http://localhost:3100 node landing/scripts/capture.mjs
 *
 * Requisitos: la app corriendo (Docker, o `NEXT_PUBLIC_MOCK=true npm run build && npx next start` en frontend/)
 * y Playwright en e2e/ y sharp en frontend/ (npm install en ambos; `cd e2e && npm install && npx playwright install chromium`).
 */
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import path from "node:path";
import fs from "node:fs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const require = createRequire(path.join(root, "e2e/package.json"));
const { chromium } = require("@playwright/test");
const sharp = createRequire(path.join(root, "frontend/package.json"))("sharp"); // sharp viene con Next en frontend/node_modules

const BASE = process.env.CAPTURE_URL || "http://localhost:3000";
const OUT = path.join(root, "landing/img");
const W = 1600, H = 1000;
fs.mkdirSync(OUT, { recursive: true });

async function save(page, name) {
  const png = await page.screenshot({ type: "png" });
  const info = await sharp(png).webp({ quality: 82, effort: 6 }).toFile(path.join(OUT, `${name}.webp`));
  console.log(name, `${(info.size / 1024).toFixed(0)} KB`);
  return png;
}

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: W, height: H }, deviceScaleFactor: 1, locale: "es-ES" });
page.on("pageerror", (e) => console.log("pageerror:", e.message));
await page.goto(BASE, { waitUntil: "networkidle" });
// Oculta el banner de controles si el modo mock lo deja con la clave sin traducir.
await page.addStyleTag({ content: '[data-testid="ctl-banner"]{display:none!important}' });
await page.waitForSelector('[data-testid="agent-sales"]');

// Lanza el escenario guionado de la propuesta de $50,000 y deja que se vea el trabajo.
await page.getByTestId("command-input").fill("Prepara una propuesta de $50,000 para un cliente nuevo");
await page.getByTestId("command-submit").click();
await page.waitForTimeout(7000);
const officePng = await save(page, "office");

// Aprobaciones: espera a que aparezca una pendiente.
try {
  await page.waitForSelector('[data-testid^="approval-"]', { timeout: 90000 });
  await page.waitForTimeout(800);
} catch { console.log("aviso: no apareció aprobación pendiente"); }
await save(page, "approvals");

// Dashboard
await page.getByTestId("mode-toggle").click();
await page.waitForTimeout(1500);
await save(page, "dashboard");
await page.getByTestId("mode-toggle").click();

// Proyectos
await page.getByTestId("nav-projects").click();
await page.waitForSelector('[data-testid="projects-view"]');
await page.waitForTimeout(1200);
// Abre el primer proyecto (p. ej. "Cierre financiero del trimestre") para mostrar tareas en paralelo.
const card = page.locator('[data-testid^="project-card-"]:not([data-testid^="project-card-progress-"])').first();
if (await card.count()) { await card.click(); await page.waitForTimeout(2000); }
await save(page, "projects");

// Imagen Open Graph (1200x630) a partir de la oficina
await sharp(officePng).resize(1200, 630, { fit: "cover", position: "north" }).png({ compressionLevel: 9 }).toFile(path.join(OUT, "og.png"));
await browser.close();
