/* Shared by prerender.mjs (links from the home page) and build-guide.mjs (guide + technical docs pages). */
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";

export const dir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
export const SITE = "https://workforceai.es";

/** Two collections of static pages built from JSON sources. `order` is the reading order (ids). */
export const COLLECTIONS = {
  guide: {
    src: "guide-src",
    root: { es: "/guia/", en: "/en/guide/" },
    order: ["getting-started", "the-office", "projects", "skills", "shop-and-coins", "files-and-chats", "approvals-and-security", "settings"],
  },
  docs: {
    src: "docs-src",
    root: { es: "/docs/", en: "/en/docs/" },
    order: ["architecture", "api-reference", "auth-and-tenancy", "agent-runtime", "skills-format", "security-model", "deployment", "configuration"],
  },
};

export function loadPages(coll = "guide") {
  const { src, order } = COLLECTIONS[coll];
  const srcDir = path.join(dir, src);
  const byId = {};
  for (const f of fs.existsSync(srcDir) ? fs.readdirSync(srcDir) : []) {
    if (!f.endsWith(".json")) continue;
    const p = JSON.parse(fs.readFileSync(path.join(srcDir, f), "utf8"));
    if (p.id !== f.replace(/\.json$/, "")) throw new Error(`${src}/${f}: id "${p.id}" must match the file name`);
    for (const l of ["es", "en"]) {
      if (!p.slug?.[l] || !p[l]?.title || !p[l]?.description || !Array.isArray(p[l]?.sections)) throw new Error(`${src}/${f}: incomplete (${l})`);
    }
    if (p.es.sections.length !== p.en.sections.length) throw new Error(`${src}/${f}: es and en must have the same number of sections`);
    byId[p.id] = p;
  }
  const pages = order.filter((id) => byId[id]).map((id) => byId[id]);
  for (const id of Object.keys(byId)) if (!order.includes(id)) pages.push(byId[id]);
  return pages;
}

/** Root-absolute URL path of a page ("index" = the collection's table of contents). */
export function pagePath(id, lang, pages, coll = "guide") {
  const root = COLLECTIONS[coll].root[lang];
  if (id === "index") return root;
  const p = pages.find((x) => x.id === id);
  if (!p) throw new Error(`Unknown ${coll} page: ${id}`);
  return `${root}${p.slug[lang]}/`;
}
export const guidePath = (id, lang, pages) => pagePath(id, lang, pages, "guide");

/** LANDING_CONFIG from config.js (CTA targets). */
export function loadConfig() {
  const ctx = { window: {} };
  vm.runInNewContext(fs.readFileSync(path.join(dir, "config.js"), "utf8"), ctx);
  return ctx.window.LANDING_CONFIG;
}
