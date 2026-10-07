/*
 * Opcional: hornea el español del diccionario (i18n.js) dentro de index.html para SEO,
 * vista previa en redes y carga sin JavaScript. Idempotente. Ejecutar tras editar textos:
 *   node landing/scripts/prerender.mjs
 * No requiere dependencias. El sitio funciona igual sin ejecutarlo (app.js rellena los textos).
 */
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";

const dir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const ctx = { window: {} };
vm.runInNewContext(fs.readFileSync(path.join(dir, "i18n.js"), "utf8"), ctx);
const es = ctx.window.I18N.es;
const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
const escAttr = (s) => s.replace(/&/g, "&amp;").replace(/"/g, "&quot;");
const get = (k) => { if (!(k in es)) throw new Error("Falta la clave: " + k); return es[k]; };

let html = fs.readFileSync(path.join(dir, "index.html"), "utf8");

// Elementos con contenido: <tag ... data-i18n="k" ...>...</tag>
html = html.replace(/(<([a-z0-9]+)\b[^>]*\bdata-i18n="([^"]+)"[^>]*>)([\s\S]*?)(<\/\2>)/g, (_, open, _t, k, _c, close) => open + esc(get(k)) + close);
html = html.replace(/(<([a-z0-9]+)\b[^>]*\bdata-i18n-html="([^"]+)"[^>]*>)([\s\S]*?)(<\/\2>)/g, (_, open, _t, k, _c, close) => open + get(k) + close);

// Atributos: reemplaza o inserta el valor del atributo destino.
function setAttr(tag, attr, value) {
  const re = new RegExp(`(\\s${attr}=")[^"]*(")`);
  return re.test(tag) ? tag.replace(re, `$1${escAttr(value)}$2`) : tag.replace(/\s*\/?>$/, (m) => ` ${attr}="${escAttr(value)}"${m.trim()}`);
}
html = html.replace(/<[a-z0-9]+\b[^>]*\bdata-i18n-(alt|aria|title)="([^"]+)"[^>]*>/g, (tag, kind, k) => setAttr(tag, kind === "aria" ? "aria-label" : kind, get(k)));
html = html.replace(/<meta\b[^>]*\bdata-i18n-attr="([^"]+)"[^>]*>/g, (tag, spec) => {
  for (const pair of spec.split(",")) { const [attr, k] = pair.split(":"); tag = setAttr(tag, attr, get(k)); }
  return tag;
});

fs.writeFileSync(path.join(dir, "index.html"), html);
console.log("index.html actualizado con el español del diccionario.");
