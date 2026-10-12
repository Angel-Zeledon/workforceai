/*
 * Sanity checks for the built site (run after build.mjs): every internal link resolves to a file, each page has one h1,
 * a canonical, reciprocal hreflang, valid JSON-LD, sane title/description lengths, and everything in the sitemap exists.
 *   node landing/scripts/check.mjs
 */
import fs from "node:fs";
import path from "node:path";
import { dir, SITE } from "./guide-lib.mjs";

const problems = [];
const bad = (f, m) => problems.push(`${f}: ${m}`);
const pages = [];
const walk = (d) => { for (const e of fs.readdirSync(d, { withFileTypes: true })) { if (["scripts", "guide-src", "node_modules", "fonts"].includes(e.name)) continue; const p = path.join(d, e.name); e.isDirectory() ? walk(p) : p.endsWith(".html") && !e.name.startsWith("google") && pages.push(p); } };
walk(dir);

const fileFor = (urlPath) => { // "/guia/x/" -> landing/guia/x/index.html ; "/img/a.webp" -> landing/img/a.webp
  const clean = decodeURIComponent(urlPath.split("#")[0].split("?")[0]);
  const f = path.join(dir, clean);
  if (clean.endsWith("/")) return path.join(f, "index.html");
  return f;
};
const toPath = (u) => (u.startsWith(SITE) ? u.slice(SITE.length) || "/" : u);
const attr = (html, re) => [...html.matchAll(re)].map((m) => m[1]);
const info = {};

for (const f of pages) {
  const rel = path.relative(dir, f).replace(/\\/g, "/");
  const html = fs.readFileSync(f, "utf8");
  const urlPath = "/" + rel.replace(/index\.html$/, "");
  const title = (html.match(/<title[^>]*>([^<]*)/) || [])[1] || "";
  const desc = (html.match(/<meta name="description" content="([^"]*)"/) || [])[1] || "";
  const canon = (html.match(/<link rel="canonical" href="([^"]*)"/) || [])[1];
  info[urlPath] = { title, desc, canon, html };
  if (!title) bad(rel, "no <title>");
  if (title.length > 75) bad(rel, `title ${title.length} chars`);
  if (!desc) bad(rel, "no description"); else if (desc.length > 175) bad(rel, `description ${desc.length} chars`);
  if (canon !== SITE + urlPath) bad(rel, `canonical ${canon} != ${SITE + urlPath}`);
  if ((html.match(/<h1\b/g) || []).length !== 1) bad(rel, "must have exactly one <h1>");
  if (/<meta name="robots"[^>]*noindex/i.test(html)) bad(rel, "has noindex");
  for (const m of html.matchAll(/<script type="application\/ld\+json"[^>]*>([\s\S]*?)<\/script>/g)) { try { JSON.parse(m[1]); } catch { bad(rel, "invalid JSON-LD"); } }
  // internal links and assets
  for (const u of [...attr(html, /\b(?:href|src)="([^"]+)"/g)]) {
    if (/^(https?:|mailto:|#|data:|javascript:)/.test(u)) { if (u.startsWith(SITE)) { if (!fs.existsSync(fileFor(toPath(u)))) bad(rel, `broken absolute link ${u}`); } continue; }
    const target = u.startsWith("/") ? u : path.posix.join(path.posix.dirname(urlPath + "x"), u);
    if (!fs.existsSync(fileFor(target))) bad(rel, `broken link ${u}`);
  }
  // fragment links point at ids on the same page
  for (const id of attr(html, /\bhref="#([^"]+)"/g)) if (!html.includes(`id="${id}"`)) bad(rel, `#${id} has no target`);
  for (const img of html.matchAll(/<img\b[^>]*>/g)) if (!/\balt="/.test(img[0])) bad(rel, "img without alt");
}

// hreflang reciprocity: A says B is its alternate -> B must say A
for (const [p, v] of Object.entries(info)) {
  const alts = [...v.html.matchAll(/<link rel="alternate" hreflang="(es|en)" href="([^"]+)"/g)].map((m) => [m[1], toPath(m[2])]);
  if (alts.length !== 2) { bad(p, "needs hreflang es+en"); continue; }
  if (!alts.some(([, u]) => u === p)) bad(p, "hreflang does not include itself");
  for (const [, u] of alts) {
    const other = info[u];
    if (!other) { bad(p, `hreflang target ${u} does not exist`); continue; }
    const back = [...other.html.matchAll(/<link rel="alternate" hreflang="(es|en)" href="([^"]+)"/g)].map((m) => toPath(m[2]));
    if (!back.includes(p)) bad(p, `${u} does not link back`);
  }
}

// titles/descriptions unique
const seen = { t: new Map(), d: new Map() };
for (const [p, v] of Object.entries(info)) for (const [k, val] of [["t", v.title], ["d", v.desc]]) { if (seen[k].has(val)) bad(p, `duplicate ${k === "t" ? "title" : "description"} with ${seen[k].get(val)}`); else seen[k].set(val, p); }

// sitemap
const sm = fs.readFileSync(path.join(dir, "sitemap.xml"), "utf8");
const locs = [...sm.matchAll(/<loc>([^<]+)<\/loc>/g)].map((m) => toPath(m[1]));
for (const l of locs) if (!info[l]) bad("sitemap.xml", `${l} is listed but has no page`);
for (const p of Object.keys(info)) if (!locs.includes(p)) bad("sitemap.xml", `${p} is missing`);

console.log(`${pages.length} pages checked, ${locs.length} sitemap URLs.`);
for (const [p, v] of Object.entries(info)) console.log(`${p.padEnd(46)} title ${String(v.title.length).padStart(2)}  desc ${String(v.desc.length).padStart(3)}`);
if (problems.length) { console.log("\nPROBLEMS:\n" + problems.join("\n")); process.exit(1); }
console.log("\nAll good.");
