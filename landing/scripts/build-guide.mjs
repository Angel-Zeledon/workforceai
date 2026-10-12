/*
 * Builds the static, JS-free content pages from JSON sources, in Spanish and English:
 *   guide-src/*.json -> /guia/ and /en/guide/   (user guide)
 *   docs-src/*.json  -> /docs/ and /en/docs/    (technical documentation: code blocks and tables)
 * and writes sitemap.xml for the whole site (home + both collections, with hreflang alternates).
 *   node landing/scripts/build-guide.mjs     (build.mjs runs prerender first, then this)
 */
import fs from "node:fs";
import path from "node:path";
import { dir, SITE, COLLECTIONS, loadPages, pagePath, loadConfig } from "./guide-lib.mjs";

const CFG = loadConfig();
const esc = (s) => String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
const url = (p) => SITE + p;
const home = { es: "/", en: "/en/" };
const today = new Date().toISOString().slice(0, 10);
const COLL = Object.fromEntries(Object.keys(COLLECTIONS).map((k) => [k, loadPages(k)]));

// Brand mark: reuse the one in the home page header so a logo change lands everywhere.
const BRAND = fs.readFileSync(path.join(dir, "index.html"), "utf8").match(/<svg class="brand-mark"[\s\S]*?<\/svg>/)[0];

const UI = {
  es: {
    skip: "Saltar al contenido", navLabel: "Principal", crumbLabel: "Ruta de navegación", home: "Inicio", langLabel: "Idioma",
    onThisPage: "En esta página", steps: "Pasos", tip: "Consejo", related: "Sigue leyendo",
    ctaTitle: "Empieza ahora", ctaBody: "Crea tu cuenta y llegas a tu propia oficina. Por ahora la IA corre en modo simulación: respuestas guionadas, sin costo y sin conectar nada.", signup: "Crear cuenta", login: "Entrar",
    footer: "AI Workforce OS es un proyecto en desarrollo. Esta página describe su estado a la fecha; las capacidades listadas como simulación usan respuestas guionadas, sin IA real.", top: "Volver arriba",
    locale: "es_ES",
    guide: {
      name: "Guía de uso", title: "Guía de uso de AI Workforce OS", meta: "Guía de uso de AI Workforce OS | Cómo usar la app",
      desc: "Aprende a usar AI Workforce OS paso a paso: pedir trabajo, aprobar, proyectos, habilidades, tienda, archivos y seguridad.",
      intro: "Todo lo que necesitas para sacarle partido a tu oficina de empleados de IA, pantalla por pantalla. Empieza por «Primeros pasos» y sigue el orden, o salta a lo que te interese.",
    },
    docs: {
      name: "Documentación", title: "Documentación técnica de AI Workforce OS", meta: "Documentación técnica | AI Workforce OS",
      desc: "Arquitectura, referencia de la API, autenticación y multitenencia, runtime de agentes, habilidades, seguridad, despliegue y configuración.",
      intro: "Para quien quiere entender, desplegar o integrar AI Workforce OS: cómo está construido, qué expone la API, cómo se aíslan las cuentas, cómo se despliega y qué se puede configurar. Todo describe lo que el código hace hoy.",
    },
  },
  en: {
    skip: "Skip to content", navLabel: "Main", crumbLabel: "Breadcrumb", home: "Home", langLabel: "Language",
    onThisPage: "On this page", steps: "Steps", tip: "Tip", related: "Keep reading",
    ctaTitle: "Start now", ctaBody: "Create your account and land in your own office. For now the AI runs in simulation mode: scripted responses, no cost and nothing to connect.", signup: "Create account", login: "Sign in",
    footer: "AI Workforce OS is a project in development. This page describes its state to date; capabilities listed as simulation use scripted responses, with no real AI.", top: "Back to top",
    locale: "en_US",
    guide: {
      name: "User guide", title: "AI Workforce OS user guide", meta: "AI Workforce OS user guide | How to use the app",
      desc: "Learn AI Workforce OS step by step: ask for work, approve, projects, skills, shop, files and security.",
      intro: "Everything you need to get the most out of your office of AI employees, screen by screen. Start with “Getting started” and follow the order, or jump to what interests you.",
    },
    docs: {
      name: "Documentation", title: "AI Workforce OS technical documentation", meta: "Technical documentation | AI Workforce OS",
      desc: "Architecture, API reference, authentication and multi-tenancy, agent runtime, skills, security, deployment and configuration.",
      intro: "For anyone who wants to understand, deploy or integrate AI Workforce OS: how it is built, what the API exposes, how accounts are isolated, how it is deployed and what can be configured. Everything describes what the code does today.",
    },
  },
};
const rootOf = (coll, lang) => COLLECTIONS[coll].root[lang];
const ctaHref = () => ({ signup: CFG.SIGNUP_URL, login: CFG.LOGIN_URL });

function head({ lang, title, description, path: pth, altPath, type = "article", image, ld }) {
  const u = UI[lang];
  const alt = lang === "es" ? "en" : "es";
  const pairs = lang === "es" ? [pth, altPath] : [altPath, pth];
  return `<!doctype html>
<html lang="${lang}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>${esc(title)}</title>
  <meta name="description" content="${esc(description)}">
  <meta name="robots" content="index,follow,max-image-preview:large">
  <meta name="theme-color" content="#f7f9fb">
  <meta name="color-scheme" content="light dark">
  <link rel="icon" href="/favicon.svg" type="image/svg+xml">
  <link rel="canonical" href="${url(pth)}">
  <link rel="alternate" hreflang="es" href="${url(pairs[0])}">
  <link rel="alternate" hreflang="en" href="${url(pairs[1])}">
  <link rel="alternate" hreflang="x-default" href="${url(pairs[0])}">
  <meta property="og:type" content="${type}">
  <meta property="og:site_name" content="AI Workforce OS">
  <meta property="og:locale" content="${u.locale}">
  <meta property="og:locale:alternate" content="${UI[alt].locale}">
  <meta property="og:title" content="${esc(title)}">
  <meta property="og:description" content="${esc(description)}">
  <meta property="og:url" content="${url(pth)}">
  <meta property="og:image" content="${SITE}${image || "/img/og.png"}">
  <meta name="twitter:card" content="summary_large_image">
  <meta name="twitter:title" content="${esc(title)}">
  <meta name="twitter:description" content="${esc(description)}">
  <meta name="twitter:image" content="${SITE}${image || "/img/og.png"}">
  <link rel="preload" href="/fonts/Geist-Variable.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/styles.css">
  <link rel="stylesheet" href="/guide.css">
  <link rel="stylesheet" href="/theme.css">
  <script type="application/ld+json">${JSON.stringify(ld).replace(/</g, "\\u003c")}</script>
</head>`;
}

function chrome(lang, selfPath, altPath, body) {
  const u = UI[lang];
  const cta = ctaHref();
  return `<body class="guide" id="top">
  <a class="skip" href="#main">${u.skip}</a>
  <header class="site-header">
    <div class="wrap bar-row">
      <a class="brand" href="${home[lang]}" aria-label="AI Workforce OS">
        ${BRAND}
        <span>AI Workforce OS</span>
      </a>
      <nav class="nav" aria-label="${u.navLabel}">
        <a href="${home[lang]}">${u.home}</a>
        <a href="${rootOf("docs", lang)}">${u.docs.name}</a>
        <a href="${rootOf("guide", lang)}">${u.guide.name}</a>
      </nav>
      <div class="bar-end">
        <div class="lang" role="group" aria-label="${u.langLabel}">
          <a href="${lang === "es" ? selfPath : altPath}" hreflang="es" lang="es" ${lang === "es" ? 'aria-current="true"' : ""}>ES</a>
          <a href="${lang === "en" ? selfPath : altPath}" hreflang="en" lang="en" ${lang === "en" ? 'aria-current="true"' : ""}>EN</a>
        </div>
        <a class="btn btn-primary btn-sm" href="${esc(cta.signup)}" rel="noopener"><span>${u.signup}</span></a>
      </div>
    </div>
  </header>
  <main id="main" class="guide-main">
${body}
  </main>
  <section class="guide-cta" aria-labelledby="cta-h">
    <div class="wrap narrow">
      <h2 id="cta-h">${u.ctaTitle}</h2>
      <p>${u.ctaBody}</p>
      <div class="cta-row">
        <a class="btn btn-invert" href="${esc(cta.signup)}" rel="noopener">${u.signup}</a>
        <a class="btn btn-outline-invert" href="${esc(cta.login)}" rel="noopener">${u.login}</a>
      </div>
    </div>
  </section>
  <footer class="site-footer">
    <div class="wrap">
      <p>${u.footer}</p>
      <a href="${rootOf("docs", lang)}">${u.docs.name}</a>
      <a href="${rootOf("guide", lang)}">${u.guide.name}</a>
      <a href="#top">${u.top}</a>
    </div>
  </footer>
</body>
</html>
`;
}

const crumbs = (lang, items) => `<nav class="crumbs" aria-label="${UI[lang].crumbLabel}"><ol>${items.map((it, i) => (it.href && i < items.length - 1 ? `<li><a href="${it.href}">${esc(it.name)}</a></li>` : `<li aria-current="page">${esc(it.name)}</li>`)).join("")}</ol></nav>`;
const crumbLd = (items) => ({ "@type": "BreadcrumbList", itemListElement: items.map((it, i) => ({ "@type": "ListItem", position: i + 1, name: it.name, item: url(it.path) })) });
const write = (rel, html) => { const f = path.join(dir, rel); fs.mkdirSync(path.dirname(f), { recursive: true }); fs.writeFileSync(f, html); };
const publisher = { "@type": "Organization", "@id": SITE + "/#org", name: "AI Workforce OS", url: SITE + "/", logo: SITE + "/favicon.svg" };

const renderTable = (t) => `<div class="tbl" role="region" tabindex="0"><table><thead><tr>${t.head.map((h) => `<th scope="col">${esc(h)}</th>`).join("")}</tr></thead><tbody>${t.rows.map((r) => `<tr>${r.map((c, i) => (i === 0 ? `<th scope="row"><code>${esc(c)}</code></th>` : `<td>${esc(c)}</td>`)).join("")}</tr>`).join("")}</tbody></table></div>`;
const renderCode = (c) => `<pre class="code" tabindex="0"><code class="lang-${esc(c.lang || "text")}">${esc(c.text)}</code></pre>`;

// ---------- one page ----------
function renderPage(coll, pages, p, lang) {
  const u = UI[lang];
  const k = u[coll];
  const c = p[lang];
  const pth = pagePath(p.id, lang, pages, coll);
  const altPath = pagePath(p.id, lang === "es" ? "en" : "es", pages, coll);
  const trail = [{ name: u.home, path: home[lang], href: home[lang] }, { name: k.name, path: rootOf(coll, lang), href: rootOf(coll, lang) }, { name: c.title, path: pth }];
  const image = p.image ? `/img/${p.image.file}` : undefined;
  const ld = {
    "@context": "https://schema.org",
    "@graph": [
      crumbLd(trail),
      { "@type": "TechArticle", headline: c.title, description: c.description, inLanguage: lang, mainEntityOfPage: url(pth), url: url(pth), dateModified: today, publisher, ...(image ? { image: SITE + image } : {}) },
    ],
  };
  const toc = c.sections.length > 2
    ? `<nav class="toc" aria-labelledby="toc-h"><h2 id="toc-h">${u.onThisPage}</h2><ol>${c.sections.map((s, i) => `<li><a href="#s${i + 1}">${esc(s.h)}</a></li>`).join("")}</ol></nav>`
    : "";
  const fig = p.image ? `<figure class="shot"><img src="${image}" width="1600" height="1000" decoding="async" alt="${esc(p.image.alt?.[lang] || c.title)}"></figure>` : "";
  const sections = c.sections.map((s, i) => `
      <section class="g-sec" id="s${i + 1}" aria-labelledby="h${i + 1}">
        <h2 id="h${i + 1}">${esc(s.h)}</h2>
        ${(s.paras || []).map((t) => `<p>${esc(t)}</p>`).join("\n        ")}
        ${s.steps?.length ? `<ol class="steps" aria-label="${u.steps}">${s.steps.map((t) => `<li>${esc(t)}</li>`).join("")}</ol>` : ""}
        ${s.code ? renderCode(s.code) : ""}
        ${s.table ? renderTable(s.table) : ""}
        ${s.tip ? `<aside class="tip"><b>${u.tip}.</b> ${esc(s.tip)}</aside>` : ""}
      </section>`).join("\n");
  const related = (p.related || []).map((id) => pages.find((x) => x.id === id)).filter(Boolean);
  const rel = related.length
    ? `<section class="related" aria-labelledby="rel-h"><h2 id="rel-h">${u.related}</h2><ul class="cards">${related.map((r) => `<li><a href="${pagePath(r.id, lang, pages, coll)}"><b>${esc(r[lang].title)}</b><span>${esc(r[lang].description)}</span></a></li>`).join("")}</ul></section>`
    : "";
  const body = `    <div class="wrap ${coll === "docs" ? "wide" : "narrow"}">
      ${crumbs(lang, trail)}
      <article>
        <h1>${esc(c.title)}</h1>
        <p class="lead">${esc(c.intro)}</p>
        ${fig}
        ${toc}
${sections}
      </article>
      ${rel}
    </div>`;
  write(pth.replace(/^\//, "") + "index.html", head({ lang, title: c.metaTitle || c.title, description: c.description, path: pth, altPath, image, ld }) + "\n" + chrome(lang, pth, altPath, body));
}

// ---------- a collection's table of contents ----------
function renderIndex(coll, pages, lang) {
  const u = UI[lang];
  const k = u[coll];
  const pth = rootOf(coll, lang);
  const altPath = rootOf(coll, lang === "es" ? "en" : "es");
  const trail = [{ name: u.home, path: home[lang], href: home[lang] }, { name: k.name, path: pth }];
  const ld = {
    "@context": "https://schema.org",
    "@graph": [
      crumbLd(trail),
      {
        "@type": "CollectionPage", name: k.title, description: k.desc, inLanguage: lang, url: url(pth), publisher,
        mainEntity: { "@type": "ItemList", itemListElement: pages.map((p, i) => ({ "@type": "ListItem", position: i + 1, name: p[lang].title, url: url(pagePath(p.id, lang, pages, coll)) })) },
      },
    ],
  };
  const body = `    <div class="wrap narrow">
      ${crumbs(lang, trail)}
      <h1>${esc(k.title)}</h1>
      <p class="lead">${esc(k.intro)}</p>
      <ol class="cards index">${pages.map((p, i) => `<li><a href="${pagePath(p.id, lang, pages, coll)}"><span class="n">${i + 1}</span><b>${esc(p[lang].title)}</b><span>${esc(p[lang].description)}</span></a></li>`).join("")}</ol>
    </div>`;
  write(pth.replace(/^\//, "") + "index.html", head({ lang, title: k.meta, description: k.desc, path: pth, altPath, type: "website", ld }) + "\n" + chrome(lang, pth, altPath, body));
}

let total = 0;
for (const coll of Object.keys(COLLECTIONS)) {
  const pages = COLL[coll];
  if (!pages.length) continue;
  for (const l of ["es", "en"]) { renderIndex(coll, pages, l); for (const p of pages) renderPage(coll, pages, p, l); }
  total += pages.length;
}

// ---------- sitemap for the whole site ----------
const entries = [{ es: "/", en: "/en/" }];
for (const coll of Object.keys(COLLECTIONS)) {
  const pages = COLL[coll];
  if (!pages.length) continue;
  entries.push({ es: rootOf(coll, "es"), en: rootOf(coll, "en") }, ...pages.map((p) => ({ es: pagePath(p.id, "es", pages, coll), en: pagePath(p.id, "en", pages, coll) })));
}
const alt = (e) => `    <xhtml:link rel="alternate" hreflang="es" href="${url(e.es)}"/>\n    <xhtml:link rel="alternate" hreflang="en" href="${url(e.en)}"/>\n    <xhtml:link rel="alternate" hreflang="x-default" href="${url(e.es)}"/>`;
const urls = entries.flatMap((e) => ["es", "en"].map((l) => `  <url>\n    <loc>${url(e[l])}</loc>\n    <lastmod>${today}</lastmod>\n${alt(e)}\n  </url>`));
write("sitemap.xml", `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">\n${urls.join("\n")}\n</urlset>\n`);
console.log(`Built ${total} pages x 2 languages + indexes; sitemap with ${urls.length} URLs.`);
