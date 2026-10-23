/*
 * Builds the static documentation site from JSON sources, in Spanish and English:
 *   docs-src/*.json  -> /docs/ and /en/docs/        (technical documentation)
 *   guide-src/*.json -> /guia/ and /en/guide/       (user guide)
 *   trust-src/*.json -> /confianza/ and /en/trust/  (security, data protection, subprocessors)
 * plus one search index per language (/docs-search.es.json, /docs-search.en.json) and sitemap.xml for the whole site.
 *
 * Every page works without JavaScript: the sidebar is a real list of links (a :target drawer on phones), the
 * "On this page" rail is a <details>, code is highlighted here at build time. /docs.js only adds search
 * (Ctrl/Cmd+K, "/"), copy buttons, heading-link copy, scroll-spy and a nicer drawer.
 *   node landing/scripts/build-guide.mjs     (build.mjs runs prerender first, then this)
 */
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { dir, SITE, COLLECTIONS, loadPages, pagePath, loadConfig } from "./guide-lib.mjs";

const CFG = loadConfig();
const esc = (s) => String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
const plain = (s) => String(s ?? "").replace(/`([^`]+)`/g, "$1");
/** Body text: escaped, with `backticks` as inline code. */
const inline = (s) => esc(String(s).normalize("NFC")).replace(/`([^`]+)`/g, "<code>$1</code>");
const url = (p) => SITE + p;
const home = { es: "/", en: "/en/" };
const today = new Date().toISOString().slice(0, 10);
const COLL = Object.fromEntries(Object.keys(COLLECTIONS).map((k) => [k, loadPages(k)]));
const KINDS = Object.keys(COLLECTIONS);
const ORDERED_KINDS = ["docs", "guide", "trust"].filter((k) => KINDS.includes(k));

// Brand mark: reuse the one in the home page header so a logo change lands everywhere (fallback: the favicon).
const BRAND = (fs.readFileSync(path.join(dir, "index.html"), "utf8").match(/<svg class="brand-mark"[\s\S]*?<\/svg>/) || [])[0]
  || fs.readFileSync(path.join(dir, "favicon.svg"), "utf8").replace("<svg ", '<svg class="brand-mark" width="24" height="24" aria-hidden="true" ');

// Cache-busting for the two assets every page loads.
const hashOf = (f) => crypto.createHash("sha256").update(fs.readFileSync(path.join(dir, f))).digest("hex").slice(0, 10);
const V = { css: hashOf("guide.css"), js: hashOf("docs.js") };

// ---------- icons (hand-drawn 24x24 strokes) ----------
const ICON_PATHS = {
  book: '<path d="M4 5.5A2.5 2.5 0 0 1 6.5 3H20v14H6.5A2.5 2.5 0 0 0 4 19.5v-14Z"/><path d="M4 19.5A2.5 2.5 0 0 0 6.5 22H20v-5M8.5 7.5h7"/>',
  compass: '<circle cx="12" cy="12" r="9"/><path d="m15.8 8.2-2.3 5.3-5.3 2.3 2.3-5.3 5.3-2.3Z"/>',
  shield: '<path d="M12 3 4.5 6v5.5c0 4.6 3.2 8.1 7.5 9.5 4.3-1.4 7.5-4.9 7.5-9.5V6L12 3Z"/>',
  shieldCheck: '<path d="M12 3 4.5 6v5.5c0 4.6 3.2 8.1 7.5 9.5 4.3-1.4 7.5-4.9 7.5-9.5V6L12 3Z"/><path d="m9 12 2.2 2.2L15.5 10"/>',
  layers: '<path d="M12 3 3 7.5l9 4.5 9-4.5L12 3Z"/><path d="m3 12 9 4.5 9-4.5"/><path d="m3 16.5 9 4.5 9-4.5"/>',
  cpu: '<rect x="6" y="6" width="12" height="12" rx="2"/><rect x="9.5" y="9.5" width="5" height="5" rx="1"/><path d="M9.5 2.5V6M14.5 2.5V6M9.5 18v3.5M14.5 18v3.5M2.5 9.5H6M2.5 14.5H6M18 9.5h3.5M18 14.5h3.5"/>',
  code: '<path d="m8 7-5 5 5 5M16 7l5 5-5 5M13.5 4.5l-3 15"/>',
  key: '<circle cx="8" cy="15.5" r="4"/><path d="m10.9 12.6 8.6-8.6M16.5 7l2.5 2.5M14 9.5l2 2"/>',
  blocks: '<rect x="3" y="3" width="7.5" height="7.5" rx="1.6"/><rect x="13.5" y="3" width="7.5" height="7.5" rx="1.6"/><rect x="3" y="13.5" width="7.5" height="7.5" rx="1.6"/><path d="M17.25 13.5v7.5M13.5 17.25H21"/>',
  server: '<rect x="3" y="4" width="18" height="7" rx="2"/><rect x="3" y="13" width="18" height="7" rx="2"/><path d="M7 7.5h.01M7 16.5h.01M11 7.5h6M11 16.5h6"/>',
  sliders: '<path d="M4 6h9M17 6h3M4 12h3M11 12h9M4 18h11M19 18h1"/><circle cx="15" cy="6" r="2"/><circle cx="9" cy="12" r="2"/><circle cx="17" cy="18" r="2"/>',
  play: '<circle cx="12" cy="12" r="9"/><path d="m10 8.5 5.2 3.5-5.2 3.5v-7Z"/>',
  building: '<path d="M4.5 21V5a2 2 0 0 1 2-2h7a2 2 0 0 1 2 2v16"/><path d="M15.5 9h2a2 2 0 0 1 2 2v10M3 21h18M8.5 7h3M8.5 11h3M8.5 15h3"/>',
  board: '<rect x="3" y="3" width="18" height="18" rx="2.5"/><path d="M8 7.5v7M12 7.5v10M16 7.5v4"/>',
  chat: '<path d="M20 15a2 2 0 0 1-2 2H8.5L4 21V5a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v10Z"/><path d="M8 8h8M8 12h5"/>',
  sparkle: '<path d="M11 3.5 12.9 8l4.6 1.9-4.6 1.9L11 16.4l-1.9-4.6L4.5 9.9 9.1 8 11 3.5Z"/><path d="m18.5 14.5.9 2.1 2.1.9-2.1.9-.9 2.1-.9-2.1-2.1-.9 2.1-.9.9-2.1Z"/>',
  coins: '<ellipse cx="9" cy="7" rx="6" ry="3"/><path d="M3 7v4.5c0 1.7 2.7 3 6 3 .7 0 1.4-.1 2-.2M15 7v2"/><ellipse cx="15" cy="13.5" rx="6" ry="3"/><path d="M9 13.5V18c0 1.7 2.7 3 6 3s6-1.3 6-3v-4.5"/>',
  gear: '<circle cx="12" cy="12" r="3.2"/><circle cx="12" cy="12" r="7"/><path d="M12 2.5V5M12 19v2.5M2.5 12H5M19 12h2.5M5.3 5.3l1.8 1.8M16.9 16.9l1.8 1.8M5.3 18.7l1.8-1.8M16.9 7.1l1.8-1.8"/>',
  lock: '<rect x="4.5" y="10.5" width="15" height="10.5" rx="2.2"/><path d="M8 10.5v-3a4 4 0 0 1 8 0v3M12 14.5V17"/>',
  database: '<ellipse cx="12" cy="5.5" rx="7.5" ry="2.8"/><path d="M4.5 5.5v13c0 1.5 3.4 2.8 7.5 2.8s7.5-1.3 7.5-2.8v-13M4.5 12c0 1.5 3.4 2.8 7.5 2.8s7.5-1.3 7.5-2.8"/>',
  network: '<circle cx="12" cy="5" r="2.5"/><circle cx="5" cy="19" r="2.5"/><circle cx="19" cy="19" r="2.5"/><path d="M12 7.5V12M12 12l-5.3 5.2M12 12l5.3 5.2"/>',
  bug: '<rect x="7.5" y="8" width="9" height="12.5" rx="4.5"/><path d="M9.5 8V6.5a2.5 2.5 0 0 1 5 0V8M3.5 13.5h4M16.5 13.5h4M4.5 8.5l3 2M19.5 8.5l-3 2M4.5 19l3-2M19.5 19l-3-2M12 11.5v9"/>',
  search: '<circle cx="11" cy="11" r="6.5"/><path d="m20 20-4.2-4.2"/>',
  menu: '<path d="M4 7h16M4 12h16M4 17h16"/>',
  close: '<path d="M6 6l12 12M18 6 6 18"/>',
  arrowRight: '<path d="M5 12h14M13 6l6 6-6 6"/>',
  arrowLeft: '<path d="M19 12H5M11 6l-6 6 6 6"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5.5M12 7.8h.01"/>',
  bulb: '<path d="M9 18h6M10 21h4M12 3a6 6 0 0 0-3.6 10.8c.7.5 1.1 1.3 1.1 2.1v.1h5v-.1c0-.8.4-1.6 1.1-2.1A6 6 0 0 0 12 3Z"/>',
  alert: '<path d="M10.3 4.2 2.7 17.4a2 2 0 0 0 1.7 3h15.2a2 2 0 0 0 1.7-3L13.7 4.2a2 2 0 0 0-3.4 0Z"/><path d="M12 9.5v4.2M12 17h.01"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  globe: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.5 2.6 3.8 5.6 3.8 9s-1.3 6.4-3.8 9c-2.5-2.6-3.8-5.6-3.8-9S9.5 5.6 12 3Z"/>',
};
const icon = (name, cls = "dx-i") => `<svg class="${cls}" viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${ICON_PATHS[name]}</svg>`;
const PAGE_ICON = {
  architecture: "layers", "agent-runtime": "cpu", "api-reference": "code", "auth-and-tenancy": "key", "skills-format": "blocks",
  deployment: "server", configuration: "sliders", "security-model": "shield",
  "getting-started": "play", "the-office": "building", projects: "board", "files-and-chats": "chat", "approvals-and-security": "shieldCheck",
  skills: "sparkle", "shop-and-coins": "coins", settings: "gear",
  "security-overview": "lock", "vulnerability-disclosure": "bug", "data-protection": "database", subprocessors: "network",
};
/** Shorter sidebar labels where the H1 is long (falls back to the page title). */
const NAV_LABEL = {
  architecture: { es: "Arquitectura", en: "Architecture" },
  "auth-and-tenancy": { es: "Autenticación y cuentas", en: "Auth and tenancy" },
  deployment: { es: "Despliegue", en: "Deployment" },
  configuration: { es: "Configuración", en: "Configuration" },
  "security-overview": { es: "Resumen de seguridad", en: "Security overview" },
};
const navLabel = (p, lang) => NAV_LABEL[p.id]?.[lang] || plain(p[lang].title).split(/:\s/)[0];
const COLL_ICON = { docs: "book", guide: "compass", trust: "shield" };

// ---------- words ----------
const UI = {
  es: {
    skip: "Saltar al contenido", crumbLabel: "Ruta de navegación", home: "Inicio", langLabel: "Idioma", sections: "Secciones del sitio",
    menu: "Menú", closeMenu: "Cerrar menú", search: "Buscar", searchPh: "Buscar en la documentación…", overview: "Introducción",
    onThisPage: "En esta página", steps: "Pasos", note: "Nota", tip: "Consejo", warning: "Atención", related: "Sigue leyendo",
    prev: "Anterior", next: "Siguiente", pager: "Paginación", readTime: (m) => `${m} min de lectura`, pagesCount: (n) => `${n} páginas`,
    anchor: (h) => `Enlace a la sección «${h}»`, codeLabel: (l) => `Código ${l}`, tableLabel: (h) => `Tabla: ${h}`, top: "Volver arriba",
    explore: "Explora también", quick: "Accesos rápidos", startHere: "Empieza aquí",
    ctaTitle: "Pruébalo en tu propia oficina", ctaBody: "Crea tu cuenta y llegas a una oficina con tu equipo de IA. Por ahora la IA corre en modo simulación: respuestas guionadas, sin costo y sin conectar nada.", signup: "Crear cuenta", login: "Entrar", trial: "Probar sin registrarte",
    footer: "AI Workforce OS es un proyecto en desarrollo. Esta documentación describe su estado a la fecha; las capacidades listadas como simulación usan respuestas guionadas, sin IA real.",
    locale: "es_ES",
    guide: {
      name: "Guía de uso", short: "Guía", title: "Guía de uso de AI Workforce OS", meta: "Guía de uso de AI Workforce OS | Cómo usar la app",
      desc: "Aprende a usar AI Workforce OS paso a paso: pedir trabajo, aprobar, proyectos, habilidades, tienda, archivos y seguridad.",
      intro: "Todo lo que necesitas para sacarle partido a tu oficina de empleados de IA, pantalla por pantalla. Empieza por «Primeros pasos» y sigue el orden, o salta a lo que te interese.",
      blurb: "Cómo usar la app, pantalla por pantalla.",
      groups: { start: ["Empieza", "Tu primera sesión y cómo se lee la oficina."], work: ["Trabajo diario", "Pedir trabajo, seguirlo y decidir qué se ejecuta."], tune: ["Personaliza", "Habilidades, tienda y ajustes de tu cuenta."] },
      quick: ["getting-started", "the-office", "approvals-and-security"],
    },
    docs: {
      name: "Documentación", short: "Docs", title: "Documentación técnica de AI Workforce OS", meta: "Documentación técnica | AI Workforce OS",
      desc: "Arquitectura, referencia de la API, autenticación y multitenencia, runtime de agentes, habilidades, seguridad, despliegue y configuración.",
      intro: "Para quien quiere entender, desplegar o integrar AI Workforce OS: cómo está construido, qué expone la API, cómo se aíslan las cuentas y qué se puede configurar. Todo describe lo que el código hace hoy.",
      blurb: "Arquitectura, API, despliegue y configuración.",
      groups: { concepts: ["Conceptos", "Cómo está construido y cómo razonan los agentes."], integrate: ["Integra", "La API, la autenticación y el formato de habilidades."], operate: ["Opera", "Despliegue, configuración y modelo de seguridad."] },
      quick: ["architecture", "api-reference", "deployment"],
    },
    trust: {
      name: "Confianza", short: "Confianza", title: "Centro de confianza de AI Workforce OS", meta: "Centro de confianza | AI Workforce OS",
      desc: "Seguridad, protección de datos, subprocesadores y divulgación de vulnerabilidades, descritos tal como existen hoy y sin certificaciones declaradas.",
      intro: "Lo que revisa un equipo de seguridad o de compras antes de confiarnos datos: qué controles existen, cómo tratamos los datos, qué proveedores intervienen y cómo reportar una vulnerabilidad. Describimos lo que hay hoy, incluidas las brechas.",
      blurb: "Seguridad, datos y proveedores, sin adornos.",
      groups: { security: ["Seguridad", "Controles que existen hoy y cómo reportar un fallo."], data: ["Datos", "Qué datos tratamos, dónde y con qué proveedores."] },
      quick: ["security-overview", "data-protection", "vulnerability-disclosure"],
    },
  },
  en: {
    skip: "Skip to content", crumbLabel: "Breadcrumb", home: "Home", langLabel: "Language", sections: "Site sections",
    menu: "Menu", closeMenu: "Close menu", search: "Search", searchPh: "Search the docs…", overview: "Overview",
    onThisPage: "On this page", steps: "Steps", note: "Note", tip: "Tip", warning: "Warning", related: "Keep reading",
    prev: "Previous", next: "Next", pager: "Pagination", readTime: (m) => `${m} min read`, pagesCount: (n) => `${n} pages`,
    anchor: (h) => `Link to the “${h}” section`, codeLabel: (l) => `${l} code`, tableLabel: (h) => `Table: ${h}`, top: "Back to top",
    explore: "Explore more", quick: "Quick links", startHere: "Start here",
    ctaTitle: "Try it in your own office", ctaBody: "Create your account and land in an office with your AI team. For now the AI runs in simulation mode: scripted responses, no cost and nothing to connect.", signup: "Create account", login: "Sign in", trial: "Try it without signing up",
    footer: "AI Workforce OS is a project in development. This documentation describes its state to date; capabilities listed as simulation use scripted responses, with no real AI.",
    locale: "en_US",
    guide: {
      name: "User guide", short: "Guide", title: "AI Workforce OS user guide", meta: "AI Workforce OS user guide | How to use the app",
      desc: "Learn AI Workforce OS step by step: ask for work, approve, projects, skills, shop, files and security.",
      intro: "Everything you need to get the most out of your office of AI employees, screen by screen. Start with “Getting started” and follow the order, or jump to what interests you.",
      blurb: "How to use the app, screen by screen.",
      groups: { start: ["Get started", "Your first session and how to read the office."], work: ["Everyday work", "Ask for work, follow it and decide what runs."], tune: ["Customize", "Skills, the shop and your account settings."] },
      quick: ["getting-started", "the-office", "approvals-and-security"],
    },
    docs: {
      name: "Documentation", short: "Docs", title: "AI Workforce OS technical documentation", meta: "Technical documentation | AI Workforce OS",
      desc: "Architecture, API reference, authentication and multi-tenancy, agent runtime, skills, security, deployment and configuration.",
      intro: "For anyone who wants to understand, deploy or integrate AI Workforce OS: how it is built, what the API exposes, how accounts are isolated and what can be configured. Everything describes what the code does today.",
      blurb: "Architecture, API, deployment and configuration.",
      groups: { concepts: ["Concepts", "How it is built and how the agents reason."], integrate: ["Integrate", "The API, authentication and the skill format."], operate: ["Operate", "Deployment, configuration and the security model."] },
      quick: ["architecture", "api-reference", "deployment"],
    },
    trust: {
      name: "Trust", short: "Trust", title: "AI Workforce OS trust center", meta: "Trust center | AI Workforce OS",
      desc: "Security, data protection, subprocessors and vulnerability disclosure, described as they exist today with no certifications claimed.",
      intro: "What a security or procurement team reviews before trusting us with data: which controls exist, how we handle data, which providers are involved and how to report a vulnerability. We describe what exists today, gaps included.",
      blurb: "Security, data and providers, no embellishment.",
      groups: { security: ["Security", "Controls that exist today and how to report a flaw."], data: ["Data", "What data we process, where and with which providers."] },
      quick: ["security-overview", "data-protection", "vulnerability-disclosure"],
    },
  },
};
const rootOf = (coll, lang) => COLLECTIONS[coll].root[lang];
const other = (lang) => (lang === "es" ? "en" : "es");

// ---------- build-time syntax highlighting ----------
function tokenize(src, rules) {
  let out = "", i = 0, buf = "";
  const flush = () => { if (buf) { out += esc(buf); buf = ""; } };
  outer: while (i < src.length) {
    for (const [cls, re] of rules) {
      re.lastIndex = i;
      const m = re.exec(src);
      if (m && m[0].length) {
        flush();
        out += cls ? `<span class="tk-${cls}">${esc(m[0])}</span>` : esc(m[0]);
        i += m[0].length;
        continue outer;
      }
    }
    buf += src[i++];
  }
  flush();
  return out;
}
const STR = /"(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*'/y;
const NUM = /\b-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b/y;
const LANG_RULES = {
  bash: [
    ["com", /(?<=^|\s)#[^\n]*/my],
    ["str", /"(?:\\.|[^"\\])*"|'[^']*'/y],
    ["var", /\$\{[^}\n]*\}|\$[A-Za-z_]\w*|\$\d/y],
    ["var", /(?<=^\s*|\s)[A-Za-z_][A-Za-z0-9_]*(?==)/my],
    ["fn", /(?<=^\s*(?:\$\s+)?|[|;&(]\s*|\b(?:sudo|then|do|exec)\s+)[A-Za-z_.\/][\w.\/-]*/my],
    ["flag", /(?<=\s)--?[A-Za-z][\w-]*/y],
    ["punct", /&&|\|\||[|;\\]/y],
    [null, /[\w.\/:@%+,=-]+/y],
  ],
  json: [
    ["key", /"(?:\\.|[^"\\\n])*"(?=\s*:)/y],
    ["str", /"(?:\\.|[^"\\\n])*"/y],
    ["num", /-?\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b/y],
    ["lit", /\b(?:true|false|null)\b/y],
    ["punct", /[{}[\],:]/y],
    [null, /\w+/y],
  ],
  yaml: [
    ["com", /(?<=^|\s)#[^\n]*/my],
    ["punct", /^---$/my],
    ["key", /(?<=^[ \t]*(?:-[ \t]+)?)[A-Za-z_"'][\w.\/"' -]*?(?=[ \t]*:(?:[ \t]|$))/my],
    ["str", STR],
    ["var", /\$\{[^}\n]*\}/y],
    ["lit", /\b(?:true|false|null|yes|no|on|off)\b/y],
    ["num", NUM],
    ["punct", /(?<=^[ \t]*)-(?=[ \t])|[|>](?=[ \t]*$)/my],
    [null, /[\w.\/@-]+/y],
  ],
  go: [
    ["com", /\/\/[^\n]*|\/\*[\s\S]*?\*\//y],
    ["str", /"(?:\\.|[^"\\\n])*"|`[^`]*`|'(?:\\.|[^'\\\n])'/y],
    ["kw", /\b(?:break|case|chan|const|continue|default|defer|else|fallthrough|for|func|go|goto|if|import|interface|map|package|range|return|select|struct|switch|type|var)\b/y],
    ["type", /\b(?:bool|byte|complex64|complex128|error|float32|float64|int|int8|int16|int32|int64|rune|string|uint|uint8|uint16|uint32|uint64|uintptr|any)\b/y],
    ["lit", /\b(?:true|false|nil|iota)\b/y],
    ["num", NUM],
    ["fn", /\b[A-Za-z_]\w*(?=\()/y],
    [null, /\w+/y],
  ],
  python: [
    ["com", /#[^\n]*/y],
    ["str", /"""[\s\S]*?"""|'''[\s\S]*?'''|[rbfu]?"(?:\\.|[^"\\\n])*"|[rbfu]?'(?:\\.|[^'\\\n])*'/y],
    ["kw", /\b(?:and|as|assert|async|await|break|class|continue|def|del|elif|else|except|finally|for|from|global|if|import|in|is|lambda|nonlocal|not|or|pass|raise|return|try|while|with|yield)\b/y],
    ["lit", /\b(?:True|False|None)\b/y],
    ["num", NUM],
    ["fn", /\b[A-Za-z_]\w*(?=\()/y],
    [null, /\w+/y],
  ],
  sql: [
    ["com", /--[^\n]*/y],
    ["str", /'(?:''|[^'])*'/y],
    ["kw", /\b(?:select|from|where|and|or|not|insert|into|values|update|set|delete|create|alter|drop|table|index|policy|on|using|with|check|enable|row|level|security|for|to|as|join|left|right|inner|outer|order|by|group|limit|returning|grant|revoke|references|primary|key|default|null|is|in|exists|begin|commit)\b/iy],
    ["num", NUM],
    [null, /\w+/y],
  ],
};
function highlightHttp(src) {
  let state = "start";
  const lines = src.split("\n");
  const out = [];
  let body = [];
  const flushBody = () => { if (body.length) { const t = body.join("\n"); out.push(/^\s*[[{]/.test(t) ? tokenize(t, LANG_RULES.json) : esc(t)); body = []; } };
  const startLine = (l) => {
    let m = l.match(/^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)(\s+)(\S+)(.*)$/);
    if (m) {
      const rest = m[4].replace(/(#.*)$/, "\u0000$1").split("\u0000");
      return `<span class="tk-kw">${m[1]}</span>${m[2]}<span class="tk-path">${esc(m[3])}</span>${esc(rest[0])}${rest[1] ? `<span class="tk-com">${esc(rest[1])}</span>` : ""}`;
    }
    m = l.match(/^(HTTP\/[\d.]+\s+)?(\d{3})(\s.*)?$/);
    if (m) return `${m[1] ? `<span class="tk-com">${esc(m[1])}</span>` : ""}<span class="tk-num">${m[2]}</span>${m[3] ? `<span class="tk-str">${esc(m[3])}</span>` : ""}`;
    return null;
  };
  for (const l of lines) {
    const s = startLine(l);
    if (s !== null) { flushBody(); out.push(s); state = "headers"; continue; }
    if (/^\s*#/.test(l)) { flushBody(); out.push(`<span class="tk-com">${esc(l)}</span>`); continue; }
    if (state === "headers") {
      if (!l.trim()) { out.push(""); state = "body"; continue; }
      const h = l.match(/^([A-Za-z][\w-]*)(:)(.*)$/);
      if (h) { out.push(`<span class="tk-key">${esc(h[1])}</span><span class="tk-punct">:</span>${esc(h[3])}`); continue; }
      state = "body";
    }
    if (state === "body" || state === "start") {
      if (!l.trim() && body.length) { flushBody(); out.push(""); continue; }
      body.push(l);
    }
  }
  flushBody();
  return out.join("\n");
}
const highlight = (text, lang) => (lang === "http" ? highlightHttp(text) : LANG_RULES[lang] ? tokenize(text, LANG_RULES[lang]) : esc(text));
const LANG_LABEL = { bash: "Bash", json: "JSON", yaml: "YAML", http: "HTTP", go: "Go", python: "Python", sql: "SQL", text: "Texto" };

// ---------- shared pieces ----------
function head({ lang, title, description, path: pth, altPath, type = "article", image, ld }) {
  const u = UI[lang];
  const pairs = lang === "es" ? [pth, altPath] : [altPath, pth];
  return `<!doctype html>
<html lang="${lang}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>${esc(title)}</title>
  <meta name="description" content="${esc(description)}">
  <meta name="robots" content="index,follow,max-image-preview:large">
  <meta name="theme-color" content="#ffffff" media="(prefers-color-scheme: light)">
  <meta name="theme-color" content="#070d17" media="(prefers-color-scheme: dark)">
  <meta name="color-scheme" content="light dark">
  <link rel="icon" href="/favicon.svg" type="image/svg+xml">
  <link rel="icon" href="/favicon.ico?v=3" sizes="any">
  <link rel="icon" href="/favicon-32.png?v=3" sizes="32x32" type="image/png">
  <link rel="apple-touch-icon" href="/apple-touch-icon.png?v=3">
  <link rel="manifest" href="/manifest.webmanifest?v=3">
  <meta name="apple-mobile-web-app-title" content="Workforce">
  <link rel="canonical" href="${url(pth)}">
  <link rel="alternate" hreflang="es" href="${url(pairs[0])}">
  <link rel="alternate" hreflang="en" href="${url(pairs[1])}">
  <link rel="alternate" hreflang="x-default" href="${url(pairs[0])}">
  <meta property="og:type" content="${type}">
  <meta property="og:site_name" content="AI Workforce OS">
  <meta property="og:locale" content="${u.locale}">
  <meta property="og:locale:alternate" content="${UI[other(lang)].locale}">
  <meta property="og:title" content="${esc(title)}">
  <meta property="og:description" content="${esc(description)}">
  <meta property="og:url" content="${url(pth)}">
  <meta property="og:image" content="${SITE}${image || "/img/og.png"}">
  <meta name="twitter:card" content="summary_large_image">
  <meta name="twitter:title" content="${esc(title)}">
  <meta name="twitter:description" content="${esc(description)}">
  <meta name="twitter:image" content="${SITE}${image || "/img/og.png"}">
  <link rel="preload" href="/fonts/Geist-Variable.woff2" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="/guide.css?v=${V.css}">
  <script src="/docs.js?v=${V.js}" defer></script>
  <script type="application/ld+json">${JSON.stringify(ld).replace(/</g, "\\u003c")}</script>
</head>`;
}

const langSwitch = (lang, selfPath, altPath, cls = "dx-lang") => `<div class="${cls}" role="group" aria-label="${UI[lang].langLabel}">
          <a href="${lang === "es" ? selfPath : altPath}" hreflang="es" lang="es"${lang === "es" ? ' aria-current="true"' : ""}>ES</a>
          <a href="${lang === "en" ? selfPath : altPath}" hreflang="en" lang="en"${lang === "en" ? ' aria-current="true"' : ""}>EN</a>
        </div>`;

const collLinks = (lang, coll, cls) => `<nav class="${cls}" aria-label="${UI[lang].sections}">${ORDERED_KINDS.map((k) => `<a href="${rootOf(k, lang)}"${k === coll ? ' aria-current="true"' : ""}>${icon(COLL_ICON[k])}<span>${esc(UI[lang][k].name)}</span></a>`).join("")}</nav>`;

function sidebar(lang, coll, currentId, selfPath, altPath) {
  const u = UI[lang];
  const k = u[coll];
  const pages = COLL[coll];
  const link = (href, label, ic, current) => `<a href="${href}"${current ? ' aria-current="page"' : ""}>${icon(ic, "dx-i dx-i-sm")}<span>${esc(label)}</span></a>`;
  const groups = Object.entries(COLLECTIONS[coll].groups || { all: pages.map((p) => p.id) });
  return `<aside class="dx-side" id="docs-nav" aria-label="${esc(k.name)}">
      <div class="dx-side-head">
        <span>${u.menu}</span>
        <a class="dx-side-close" href="#top" aria-label="${u.closeMenu}">${icon("close")}</a>
      </div>
      ${collLinks(lang, coll, "dx-colls")}
      <nav class="dx-pages" aria-label="${esc(k.name)}">
        <ul class="dx-list">
          <li>${link(rootOf(coll, lang), u.overview, COLL_ICON[coll], currentId === "index")}</li>
        </ul>
        ${groups.map(([gk, ids]) => {
          const label = k.groups?.[gk]?.[0] || k.name;
          const items = ids.map((id) => pages.find((p) => p.id === id)).filter(Boolean);
          return `<p class="dx-group" id="g-${coll}-${gk}">${esc(label)}</p>
        <ul class="dx-list" aria-labelledby="g-${coll}-${gk}">
          ${items.map((p) => `<li>${link(pagePath(p.id, lang, pages, coll), navLabel(p, lang), PAGE_ICON[p.id] || "book", p.id === currentId)}</li>`).join("\n          ")}
        </ul>`;
        }).join("\n        ")}
      </nav>
      <div class="dx-side-foot">
        ${langSwitch(lang, selfPath, altPath)}
        <a class="dx-btn dx-btn-primary" href="${esc(CFG.SIGNUP_URL)}" rel="noopener">${u.signup}</a>
      </div>
    </aside>
    <a class="dx-scrim" href="#top" tabindex="-1" aria-hidden="true"></a>`;
}

function shell(lang, coll, currentId, selfPath, altPath, mainHtml) {
  const u = UI[lang];
  return `<body class="dx dx-${coll}" id="top">
  <a class="dx-skip" href="#main">${u.skip}</a>
  <header class="dx-top">
    <div class="dx-top-in">
      <a class="dx-menu" href="#docs-nav" aria-controls="docs-nav" aria-label="${u.menu}">${icon("menu")}</a>
      <a class="dx-brand" href="${home[lang]}" aria-label="AI Workforce OS">
        ${BRAND}
        <span>AI Workforce OS</span>
      </a>
      ${collLinks(lang, coll, "dx-tabs")}
      <div class="dx-top-end">
        <button class="dx-search-btn" type="button" data-search hidden aria-label="${u.searchPh}" aria-keyshortcuts="Control+K Meta+K /">
          ${icon("search")}<span class="dx-search-ph">${u.searchPh}</span><kbd class="dx-kbd" data-kbd>Ctrl K</kbd>
        </button>
        ${langSwitch(lang, selfPath, altPath, "dx-lang dx-lang-top")}
        <a class="dx-btn dx-btn-primary dx-top-cta" href="${esc(CFG.SIGNUP_URL)}" rel="noopener">${u.signup}</a>
      </div>
    </div>
  </header>
  <div class="dx-shell">
    ${sidebar(lang, coll, currentId, selfPath, altPath)}
    <main id="main" class="dx-main" tabindex="-1">
${mainHtml}
    </main>
  </div>
  <div class="dx-live" aria-live="polite" data-live></div>
</body>
</html>
`;
}

function footer(lang) {
  const u = UI[lang];
  return `<footer class="dx-foot">
        <p>${u.footer}</p>
        <div class="dx-foot-links">
          <a href="${home[lang]}">AI Workforce OS</a>
          ${ORDERED_KINDS.map((k) => `<a href="${rootOf(k, lang)}">${esc(u[k].name)}</a>`).join("\n          ")}
          <a href="#top">${u.top}</a>
        </div>
      </footer>`;
}

function cta(lang) {
  const u = UI[lang];
  return `<section class="dx-cta" aria-labelledby="cta-h">
        <div>
          <h2 id="cta-h">${u.ctaTitle}</h2>
          <p>${u.ctaBody}</p>
        </div>
        <div class="dx-cta-row">
          <a class="dx-btn dx-btn-primary" href="${esc(CFG.SIGNUP_URL)}" rel="noopener">${u.signup}</a>
          <a class="dx-btn dx-btn-ghost" href="${esc(CFG.TRIAL_URL)}" rel="noopener">${u.trial}</a>
        </div>
      </section>`;
}

const crumbs = (lang, items) => `<nav class="dx-crumbs" aria-label="${UI[lang].crumbLabel}"><ol>${items.map((it, i) => (it.href && i < items.length - 1 ? `<li><a href="${it.href}">${esc(it.name)}</a></li>` : it.section ? `<li><span>${esc(it.name)}</span></li>` : `<li><span aria-current="page">${esc(it.name)}</span></li>`)).join("")}</ol></nav>`;
const crumbLd = (items) => ({ "@type": "BreadcrumbList", itemListElement: items.map((it, i) => ({ "@type": "ListItem", position: i + 1, name: it.name, item: url(it.path) })) });
const write = (rel, html) => { const f = path.join(dir, rel); fs.mkdirSync(path.dirname(f), { recursive: true }); fs.writeFileSync(f, html); };
const publisher = { "@type": "Organization", "@id": SITE + "/#org", name: "AI Workforce OS", url: SITE + "/", logo: SITE + "/icon-512.png" };

// ---------- blocks ----------
const RESERVED = new Set(["top", "main", "docs-nav", "toc", "toc-h", "rel-h", "cta-h", "pager"]);
const slugify = (s) => plain(s).normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 60).replace(/-+$/, "") || "seccion";
function sectionIds(sections) {
  const used = new Set(RESERVED);
  return sections.map((s) => {
    let id = slugify(s.h), n = 2;
    if (/^\d/.test(id)) id = "s-" + id;
    while (used.has(id)) id = `${slugify(s.h)}-${n++}`;
    used.add(id);
    return id;
  });
}

function renderTable(t, lang, label) {
  const tall = t.rows.length > 12 ? " is-tall" : "";
  return `<div class="dx-table${tall}" role="region" tabindex="0" aria-label="${esc(UI[lang].tableLabel(plain(label)))}"><table><thead><tr>${t.head.map((h) => `<th scope="col">${inline(h)}</th>`).join("")}</tr></thead><tbody>${t.rows.map((r) => `<tr>${r.map((c, i) => (i === 0 ? `<th scope="row">${inline(c)}</th>` : `<td>${inline(c)}</td>`)).join("")}</tr>`).join("")}</tbody></table></div>`;
}
function renderCode(c, lang) {
  const l = (c.lang || "text").toLowerCase();
  const label = l === "text" ? (lang === "es" ? "Texto" : "Text") : LANG_LABEL[l] || l;
  const title = c.title ? `<span class="dx-code-title">${esc(c.title)}</span>` : `<span class="dx-code-title">${esc(label)}</span>`;
  return `<figure class="dx-code" data-lang="${esc(l)}">
          <figcaption class="dx-code-bar">${title}${c.title ? `<span class="dx-code-lang">${esc(label)}</span>` : ""}</figcaption>
          <pre tabindex="0" aria-label="${esc(UI[lang].codeLabel(label))}"><code>${highlight(String(c.text).replace(/\s+$/, ""), l)}</code></pre>
        </figure>`;
}
const CALLOUT = { note: "info", tip: "bulb", warning: "alert" };
const renderCallout = (kind, text, lang) => `<div class="dx-callout is-${kind}" role="note">${icon(CALLOUT[kind])}<div><p class="dx-callout-t">${UI[lang][kind]}</p><p>${inline(text)}</p></div></div>`;

function renderSection(s, id, lang) {
  const u = UI[lang];
  const codes = s.code ? (Array.isArray(s.code) ? s.code : [s.code]) : [];
  const parts = [
    ...(s.paras || []).map((t) => `<p>${inline(t)}</p>`),
    s.list?.length ? `<ul class="dx-ul">${s.list.map((t) => `<li>${inline(t)}</li>`).join("")}</ul>` : "",
    s.steps?.length ? `<ol class="dx-steps" aria-label="${u.steps}">${s.steps.map((t) => `<li>${inline(t)}</li>`).join("")}</ol>` : "",
    ...codes.map((c) => renderCode(c, lang)),
    s.table ? renderTable(s.table, lang, s.h) : "",
    ...["note", "tip", "warning"].map((k) => (s[k] ? renderCallout(k, s[k], lang) : "")),
  ].filter(Boolean);
  return `<section class="dx-sec" aria-labelledby="${id}">
        <h2 id="${id}">${inline(s.h)}<a class="dx-anchor" href="#${id}" aria-label="${esc(u.anchor(plain(s.h)))}">#</a></h2>
        ${parts.join("\n        ")}
      </section>`;
}

const words = (c) => [c.intro, ...c.sections.flatMap((s) => [s.h, ...(s.paras || []), ...(s.list || []), ...(s.steps || []), s.note, s.tip, s.warning, ...(s.table ? s.table.rows.flat() : [])])].filter(Boolean).join(" ").split(/\s+/).length;

// ---------- one page ----------
const search = { es: [], en: [] };
const sectionText = (s) => [...(s.paras || []), ...(s.list || []), ...(s.steps || []), s.note, s.tip, s.warning, ...(s.table ? s.table.rows.map((r) => r.join(" · ")) : [])].filter(Boolean).map(plain).join(" ").normalize("NFC");

function renderPage(coll, pages, p, lang) {
  const u = UI[lang];
  const k = u[coll];
  const c = p[lang];
  const pth = pagePath(p.id, lang, pages, coll);
  const altPath = pagePath(p.id, other(lang), pages, coll);
  const title = plain(c.title);
  const trail = [{ name: u.home, path: home[lang], href: home[lang] }, { name: k.name, path: rootOf(coll, lang), href: rootOf(coll, lang) }, { name: title, path: pth }];
  const image = p.image ? `/img/${p.image.file}` : undefined;
  const ld = {
    "@context": "https://schema.org",
    "@graph": [
      crumbLd(trail),
      { "@type": "TechArticle", headline: title, description: c.description, inLanguage: lang, mainEntityOfPage: url(pth), url: url(pth), dateModified: today, publisher, ...(image ? { image: SITE + image } : {}) },
    ],
  };
  const ids = sectionIds(c.sections);
  const group = Object.entries(COLLECTIONS[coll].groups || {}).find(([, list]) => list.includes(p.id))?.[0];
  const groupName = group ? k.groups[group][0] : null;
  const toc = c.sections.length > 1
    ? `<details class="dx-toc" open>
        <summary id="toc-h">${u.onThisPage}</summary>
        <ol>${c.sections.map((s, i) => `<li><a href="#${ids[i]}">${inline(s.h)}</a></li>`).join("")}</ol>
        <a class="dx-toc-top" href="#top">${u.top}</a>
      </details>`
    : "";
  const fig = p.image ? `<figure class="dx-shot"><img src="${image}" width="1600" height="1000" decoding="async" alt="${esc(p.image.alt?.[lang] || title)}"></figure>` : "";
  const related = (p.related || []).map((id) => pages.find((x) => x.id === id)).filter(Boolean);
  const rel = related.length
    ? `<section class="dx-related" aria-labelledby="rel-h"><h2 id="rel-h">${u.related}</h2><ul class="dx-cards dx-cards-sm">${related.map((r) => `<li><a href="${pagePath(r.id, lang, pages, coll)}">${icon(PAGE_ICON[r.id] || "book", "dx-i dx-card-i")}<b>${esc(plain(r[lang].title))}</b><span>${esc(plain(r[lang].description))}</span></a></li>`).join("")}</ul></section>`
    : "";
  const idx = pages.indexOf(p);
  const prev = idx > 0 ? { href: pagePath(pages[idx - 1].id, lang, pages, coll), t: plain(pages[idx - 1][lang].title) } : { href: rootOf(coll, lang), t: k.name };
  const next = idx < pages.length - 1 ? { href: pagePath(pages[idx + 1].id, lang, pages, coll), t: plain(pages[idx + 1][lang].title) } : null;
  const pager = `<nav class="dx-pager" aria-label="${u.pager}">
        <a class="dx-prev" href="${prev.href}" rel="prev"><span>${icon("arrowLeft", "dx-i dx-i-sm")}${u.prev}</span><b>${esc(prev.t)}</b></a>
        ${next ? `<a class="dx-next" href="${next.href}" rel="next"><span>${u.next}${icon("arrowRight", "dx-i dx-i-sm")}</span><b>${esc(next.t)}</b></a>` : ""}
      </nav>`;
  const mins = Math.max(1, Math.round(words(c) / 200));
  const main = `    <article class="dx-article">
      <header class="dx-head">
        ${crumbs(lang, [{ name: k.name, href: rootOf(coll, lang) }, ...(groupName ? [{ name: groupName, section: true }] : [])])}
        <h1>${esc(title)}</h1>
        <p class="dx-lead">${inline(c.intro)}</p>
        <p class="dx-meta">${icon("clock", "dx-i dx-i-sm")}<span>${u.readTime(mins)}</span></p>
      </header>
      ${toc}
      <div class="dx-body">
        ${fig}
        ${c.sections.map((s, i) => renderSection(s, ids[i], lang)).join("\n        ")}
        ${rel}
      </div>
      <div class="dx-end">
      ${pager}
      ${coll === "guide" ? cta(lang) : ""}
      ${footer(lang)}
      </div>
    </article>`;
  write(pth.replace(/^\//, "") + "index.html", head({ lang, title: c.metaTitle || title, description: c.description, path: pth, altPath, image, ld }) + "\n" + shell(lang, coll, p.id, pth, altPath, main));

  search[lang].push({ c: coll, t: title, h: "", u: pth, x: plain(c.intro).normalize("NFC") });
  c.sections.forEach((s, i) => search[lang].push({ c: coll, t: title, h: plain(s.h), u: `${pth}#${ids[i]}`, x: sectionText(s).slice(0, 700) }));
}

// ---------- a collection's home ----------
function renderIndex(coll, pages, lang) {
  const u = UI[lang];
  const k = u[coll];
  const pth = rootOf(coll, lang);
  const altPath = rootOf(coll, other(lang));
  const trail = [{ name: u.home, path: home[lang], href: home[lang] }, { name: k.name, path: pth }];
  const ld = {
    "@context": "https://schema.org",
    "@graph": [
      crumbLd(trail),
      {
        "@type": "CollectionPage", name: k.title, description: k.desc, inLanguage: lang, url: url(pth), publisher,
        mainEntity: { "@type": "ItemList", itemListElement: pages.map((p, i) => ({ "@type": "ListItem", position: i + 1, name: plain(p[lang].title), url: url(pagePath(p.id, lang, pages, coll)) })) },
      },
    ],
  };
  const byId = (id) => pages.find((p) => p.id === id);
  const quick = (k.quick || []).map(byId).filter(Boolean);
  const card = (p, extra = "") => `<li><a href="${pagePath(p.id, lang, pages, coll)}"${extra}>
              <span class="dx-card-ic">${icon(PAGE_ICON[p.id] || "book")}</span>
              <b>${esc(plain(p[lang].title))}</b>
              <span>${esc(plain(p[lang].description))}</span>
              <span class="dx-card-go">${icon("arrowRight", "dx-i dx-i-sm")}</span>
            </a></li>`;
  const groups = Object.entries(COLLECTIONS[coll].groups || { all: pages.map((p) => p.id) });
  const main = `    <div class="dx-home">
      <header class="dx-hero">
        ${crumbs(lang, [{ name: u.home, href: home[lang] }, { name: k.name }])}
        <p class="dx-eyebrow">${icon(COLL_ICON[coll], "dx-i dx-i-sm")}<span>${esc(k.name)}</span><span class="dx-dot" aria-hidden="true">·</span><span>${u.pagesCount(pages.length)}</span></p>
        <h1>${esc(k.title)}</h1>
        <p class="dx-lead">${esc(k.intro)}</p>
        <button class="dx-hero-search" type="button" data-search hidden aria-label="${u.searchPh}" aria-keyshortcuts="Control+K Meta+K /">${icon("search")}<span>${u.searchPh}</span><kbd class="dx-kbd" data-kbd>Ctrl K</kbd></button>
        ${quick.length ? `<div class="dx-hero-actions">
          <a class="dx-btn dx-btn-primary" href="${pagePath(quick[0].id, lang, pages, coll)}">${u.startHere}${icon("arrowRight", "dx-i dx-i-sm")}</a>
          ${quick.slice(1).map((p) => `<a class="dx-btn dx-btn-ghost" href="${pagePath(p.id, lang, pages, coll)}">${esc(navLabel(p, lang))}</a>`).join("\n          ")}
        </div>` : ""}
      </header>
      ${groups.map(([gk, ids]) => {
        const [name, blurb] = k.groups?.[gk] || [k.name, ""];
        const items = ids.map(byId).filter(Boolean);
        return `<section class="dx-group-sec" id="grp-${gk}" aria-labelledby="grp-${gk}-h">
        <div class="dx-group-head"><h2 id="grp-${gk}-h">${esc(name)}</h2>${blurb ? `<p>${esc(blurb)}</p>` : ""}</div>
        <ul class="dx-cards">
            ${items.map((p) => card(p)).join("\n            ")}
        </ul>
      </section>`;
      }).join("\n      ")}
      <section class="dx-group-sec" aria-labelledby="explore-h">
        <div class="dx-group-head"><h2 id="explore-h">${u.explore}</h2></div>
        <ul class="dx-cards dx-cards-coll">
          ${ORDERED_KINDS.filter((x) => x !== coll).map((x) => `<li><a href="${rootOf(x, lang)}"><span class="dx-card-ic">${icon(COLL_ICON[x])}</span><b>${esc(u[x].name)}</b><span>${esc(u[x].blurb)}</span><span class="dx-card-go">${icon("arrowRight", "dx-i dx-i-sm")}</span></a></li>`).join("\n          ")}
        </ul>
      </section>
      ${cta(lang)}
      ${footer(lang)}
    </div>`;
  write(pth.replace(/^\//, "") + "index.html", head({ lang, title: k.meta, description: k.desc, path: pth, altPath, type: "website", ld }) + "\n" + shell(lang, coll, "index", pth, altPath, main));
  search[lang].push({ c: coll, t: k.name, h: "", u: pth, x: k.intro });
}

let total = 0;
for (const coll of ORDERED_KINDS) {
  const pages = COLL[coll];
  if (!pages.length) continue;
  for (const l of ["es", "en"]) { renderIndex(coll, pages, l); for (const p of pages) renderPage(coll, pages, p, l); }
  total += pages.length;
}

// ---------- search index (one per language; docs.js fetches the page's language) ----------
for (const l of ["es", "en"]) {
  const labels = Object.fromEntries(ORDERED_KINDS.map((k) => [k, UI[l][k].short]));
  write(`docs-search.${l}.json`, JSON.stringify({ v: 1, labels, items: search[l] }));
}

// ---------- sitemap for the whole site ----------
const entries = [{ es: "/", en: "/en/" }];
for (const coll of ORDERED_KINDS) {
  const pages = COLL[coll];
  if (!pages.length) continue;
  entries.push({ es: rootOf(coll, "es"), en: rootOf(coll, "en") }, ...pages.map((p) => ({ es: pagePath(p.id, "es", pages, coll), en: pagePath(p.id, "en", pages, coll) })));
}
const alt = (e) => `    <xhtml:link rel="alternate" hreflang="es" href="${url(e.es)}"/>\n    <xhtml:link rel="alternate" hreflang="en" href="${url(e.en)}"/>\n    <xhtml:link rel="alternate" hreflang="x-default" href="${url(e.es)}"/>`;
const urls = entries.flatMap((e) => ["es", "en"].map((l) => `  <url>\n    <loc>${url(e[l])}</loc>\n    <lastmod>${today}</lastmod>\n${alt(e)}\n  </url>`));
write("sitemap.xml", `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">\n${urls.join("\n")}\n</urlset>\n`);
console.log(`Built ${total} pages x 2 languages + indexes; search index ${search.es.length}/${search.en.length} entries; sitemap with ${urls.length} URLs.`);
