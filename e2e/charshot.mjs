import { chromium } from "playwright-core";
const tag = process.argv[2];
let b; for (const ch of ["chrome","msedge"]) { try { b = await chromium.launch({ channel: ch }); break; } catch {} }
const p = await b.newPage({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2 });
await p.goto("http://localhost:3111/", { waitUntil: "domcontentloaded" });
await p.waitForSelector("canvas", { timeout: 90000 }); await p.waitForTimeout(4000);
await p.mouse.move(640, 420);
for (let i = 0; i < 9; i++) { await p.mouse.wheel(0, -400); await p.waitForTimeout(120); }
await p.waitForTimeout(1500);
await p.screenshot({ path: "C:/Users/Jahir/AppData/Local/Temp/claude/C--Users-Jahir-Documents-Playground/a2b45346-68ba-4cd2-81a8-e0f72e809f42/scratchpad/char-" + tag + ".png", clip: { x: 224, y: 56, width: 1216, height: 760 } });
await b.close();
