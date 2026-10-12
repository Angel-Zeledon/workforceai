/*
 * Bakes the dictionary (i18n.js) into the HTML so search engines, social previews and no-JS readers get the
 * real text. Writes TWO pages from the one template (index.html keeps its data-i18n keys, so this is idempotent):
 *   /index.html     Spanish (canonical https://workforceai.es/)
 *   /en/index.html  English (canonical https://workforceai.es/en/), assets as root-absolute paths
 * plus sitemap.xml. Run after editing texts:
 *   node landing/scripts/prerender.mjs
 * No dependencies.
 */
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";

const dir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const SITE = "https://workforceai.es";
const ctx = { window: {} };
vm.runInNewContext(fs.readFileSync(path.join(dir, "i18n.js"), "utf8"), ctx);
const DICT = ctx.window.I18N;
const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
const escAttr = (s) => s.replace(/&/g, "&amp;").replace(/"/g, "&quot;");

const PAGES = {
  es: { file: "index.html", url: SITE + "/", locale: "es_ES" },
  en: { file: "en/index.html", url: SITE + "/en/", locale: "en_US" },
};

function bake(template, lang) {
  const d = DICT[lang];
  const get = (k) => { if (!(k in d)) throw new Error(`Missing key (${lang}): ${k}`); return d[k]; };
  let html = template;

  // Elements with content: <tag ... data-i18n="k" ...>...</tag>
  html = html.replace(/(<([a-z0-9]+)\b[^>]*\bdata-i18n="([^"]+)"[^>]*>)([\s\S]*?)(<\/\2>)/g, (_, open, _t, k, _c, close) => open + esc(get(k)) + close);
  html = html.replace(/(<([a-z0-9]+)\b[^>]*\bdata-i18n-html="([^"]+)"[^>]*>)([\s\S]*?)(<\/\2>)/g, (_, open, _t, k, _c, close) => open + get(k) + close);

  // Attributes: replace or insert the value of the target attribute.
  const setAttr = (tag, attr, value) => {
    const re = new RegExp(`(\\s${attr}=")[^"]*(")`);
    return re.test(tag) ? tag.replace(re, `$1${escAttr(value)}$2`) : tag.replace(/\s*\/?>$/, (m) => ` ${attr}="${escAttr(value)}"${m.trim()}`);
  };
  html = html.replace(/<[a-z0-9]+\b[^>]*\bdata-i18n-(alt|aria|title)="([^"]+)"[^>]*>/g, (tag, kind, k) => setAttr(tag, kind === "aria" ? "aria-label" : kind, get(k)));
  html = html.replace(/<meta\b[^>]*\bdata-i18n-attr="([^"]+)"[^>]*>/g, (tag, spec) => {
    for (const pair of spec.split(",")) { const [attr, k] = pair.split(":"); tag = setAttr(tag, attr, get(k)); }
    return tag;
  });

  // Page identity: language, canonical, social URL and locale, which language link is current.
  const page = PAGES[lang];
  html = html.replace(/<html lang="[^"]*"/, `<html lang="${lang}"`);
  html = html.replace(/(<link rel="canonical" href=")[^"]*(")/, `$1${page.url}$2`);
  html = html.replace(/(<meta property="og:url" content=")[^"]*(")/, `$1${page.url}$2`);
  html = html.replace(/(<meta property="og:locale" content=")[^"]*(")/, `$1${page.locale}$2`);
  html = html.replace(/(<meta property="og:locale:alternate" content=")[^"]*(")/, `$1${lang === "es" ? PAGES.en.locale : PAGES.es.locale}$2`);
  html = html.replace(/(<a href="[^"]*" hreflang="(es|en)"[^>]*?data-lang="(es|en)")( aria-current="true")?/g, (_, head, _h, l) => head + (l === lang ? ' aria-current="true"' : ""));

  // Structured data (no ratings, prices or claims the page does not make).
  const ld = {
    "@context": "https://schema.org",
    "@graph": [
      { "@type": "Organization", "@id": SITE + "/#org", name: "AI Workforce OS", url: SITE + "/", logo: SITE + "/favicon.svg", email: "info@workforceai.es" },
      { "@type": "WebSite", "@id": SITE + "/#website", url: SITE + "/", name: "AI Workforce OS", inLanguage: ["es", "en"], publisher: { "@id": SITE + "/#org" } },
      {
        "@type": "SoftwareApplication", name: "AI Workforce OS", url: page.url, inLanguage: lang,
        applicationCategory: "BusinessApplication", operatingSystem: "Web",
        description: get("meta.description"), image: SITE + "/img/og.png", publisher: { "@id": SITE + "/#org" },
      },
    ],
  };
  html = html.replace(/<script type="application\/ld\+json" data-ld>[\s\S]*?<\/script>/, `<script type="application/ld+json" data-ld>${JSON.stringify(ld).replace(/</g, "\\u003c")}</script>`);
  return html;
}

const template = fs.readFileSync(path.join(dir, "index.html"), "utf8");

// Spanish: in place (relative asset paths, as before).
fs.writeFileSync(path.join(dir, PAGES.es.file), bake(template, "es"));

// English: one directory down, so every relative asset becomes root-absolute.
let en = bake(template, "en");
en = en.replace(/\b(href|src)="(?![a-z][a-z0-9+.-]*:|#|\/)([^"]*)"/g, '$1="/$2"');
fs.mkdirSync(path.join(dir, "en"), { recursive: true });
fs.writeFileSync(path.join(dir, PAGES.en.file), en);

// Sitemap: both languages, each pointing at the other (hreflang).
const today = new Date().toISOString().slice(0, 10);
const alt = `    <xhtml:link rel="alternate" hreflang="es" href="${PAGES.es.url}"/>\n    <xhtml:link rel="alternate" hreflang="en" href="${PAGES.en.url}"/>\n    <xhtml:link rel="alternate" hreflang="x-default" href="${PAGES.es.url}"/>`;
fs.writeFileSync(path.join(dir, "sitemap.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">
  <url>
    <loc>${PAGES.es.url}</loc>
    <lastmod>${today}</lastmod>
${alt}
  </url>
  <url>
    <loc>${PAGES.en.url}</loc>
    <lastmod>${today}</lastmod>
${alt}
  </url>
</urlset>
`);
console.log("Wrote index.html (es), en/index.html (en) and sitemap.xml.");
