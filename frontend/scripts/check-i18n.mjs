// Verifies es.json and en.json have identical keys and that every statically used key exists.
import fs from "node:fs";
import path from "node:path";

const dir = path.resolve("src/lib/i18n");
const es = JSON.parse(fs.readFileSync(path.join(dir, "es.json"), "utf8"));
const en = JSON.parse(fs.readFileSync(path.join(dir, "en.json"), "utf8"));
const esK = new Set(Object.keys(es)), enK = new Set(Object.keys(en));
let bad = 0;
for (const k of esK) if (!enK.has(k)) { console.error(`missing in en: ${k}`); bad++; }
for (const k of enK) if (!esK.has(k)) { console.error(`missing in es: ${k}`); bad++; }

function walk(d, out = []) {
  for (const f of fs.readdirSync(d, { withFileTypes: true })) {
    const p = path.join(d, f.name);
    if (f.isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(f.name)) out.push(p);
  }
  return out;
}
const used = new Map();
const re = /\b(?:t|tt|tr|trOr)\(\s*(["'`])([a-zA-Z]+\.[\w.]+)\1/g;
for (const f of walk("src")) {
  const src = fs.readFileSync(f, "utf8");
  for (const m of src.matchAll(re)) if (!used.has(m[2])) used.set(m[2], f);
}
for (const [k, f] of used) {
  const has = esK.has(k) || esK.has(k + "_one") || esK.has(k + "_other");
  if (!has) { console.error(`used but undefined: ${k} (${f})`); bad++; }
}
console.log(`es=${esK.size} en=${enK.size} used=${used.size} problems=${bad}`);
process.exit(bad ? 1 : 0);
