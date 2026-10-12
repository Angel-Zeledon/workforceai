/* Builds the whole static site: bakes the home pages (es + en), then the guide pages and sitemap.xml. */
import { execFileSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
const here = path.dirname(fileURLToPath(import.meta.url));
for (const s of ["prerender.mjs", "build-guide.mjs"]) execFileSync(process.execPath, [path.join(here, s)], { stdio: "inherit" });
