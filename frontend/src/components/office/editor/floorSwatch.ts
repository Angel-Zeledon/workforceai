// Samples of a floor drawn the way the office style draws floors: a small isometric slab per kind and tint. Plano gets
// flat colors with the ink line, Pixel a coarse grid of square pixels with a small palette, Estudio a soft lit slab with a
// shadow, Maqueta a warm rounded matte slab. One tiny 2D canvas shared by all samples; the result is cached as an image.
import type { FloorKind, OfficeStyle } from "@/lib/officeLayoutModel";
import { floorLook } from "../flat/isoMath";

/** What a sample is shown at (px). Pixel samples are drawn at half this and shown at exactly twice their size. */
export const SWATCH_W = 96, SWATCH_H = 56;

const cache = new Map<string, string>();
const hex = (n: number) => "#" + n.toString(16).padStart(6, "0");
const rgb = (n: number): [number, number, number] => [(n >> 16) & 255, (n >> 8) & 255, n & 255];
const mix = (a: number, b: number, t: number) => { const x = rgb(a), y = rgb(b); return hex((Math.round(x[0] + (y[0] - x[0]) * t) << 16) | (Math.round(x[1] + (y[1] - x[1]) * t) << 8) | Math.round(x[2] + (y[2] - x[2]) * t)); };
const mixN = (a: number, b: number, t: number) => parseInt(mix(a, b, t).slice(1), 16);

export function floorSwatch(style: OfficeStyle, kind: FloorKind, tint: string): string {
  const key = `${style}|${kind}|${tint}`;
  const hit = cache.get(key);
  if (hit) return hit;
  if (typeof document === "undefined") return "";
  const px = style === "pixel";
  const k = px ? 1 : 2; // drawing scale: Pixel at half size, everything else at 2x for sharp edges
  const W = (px ? SWATCH_W / 2 : SWATCH_W * k), H = (px ? SWATCH_H / 2 : SWATCH_H * k);
  const c = document.createElement("canvas");
  c.width = W; c.height = H;
  const g = c.getContext("2d")!;
  let look = floorLook(kind, tint);
  if (style === "clay") look = { ...look, base: mixN(look.base, 0xf0d2a8, 0.28), line: mixN(look.line, 0xd9b88c, 0.3) };
  const m = W * 0.06; // margin
  const slab = Math.max(3, Math.round(H * 0.14)); // thickness of the slab
  const cx = W / 2, top = m * 0.6, hh = H - slab - m * 1.2, hw = Math.min(W / 2 - m, hh * 2);
  // (u, v) in 0..1 across the slab -> canvas
  const P = (u: number, v: number): [number, number] => [cx + (u - v) * hw, top + (u + v) * (hh / 2)];
  const diamond = () => { g.beginPath(); g.moveTo(...P(0, 0)); g.lineTo(...P(1, 0)); g.lineTo(...P(1, 1)); g.lineTo(...P(0, 1)); g.closePath(); };
  if (px) g.imageSmoothingEnabled = false;

  // the slab's two sides
  const side = (a: [number, number], b: [number, number], col: string) => { g.beginPath(); g.moveTo(a[0], a[1]); g.lineTo(b[0], b[1]); g.lineTo(b[0], b[1] + slab); g.lineTo(a[0], a[1] + slab); g.closePath(); g.fillStyle = col; g.fill(); };
  if (style === "studio") { // soft contact shadow under the slab
    g.save(); g.filter = `blur(${2.5 * k}px)`; g.fillStyle = "rgba(24,32,48,0.28)"; g.translate(0, slab * 0.9); diamond(); g.fill(); g.restore();
  }
  side(P(0, 1), P(1, 1), mix(look.base, 0x000000, style === "flat" ? 0.12 : 0.2));
  side(P(1, 1), P(1, 0), mix(look.base, 0x000000, style === "flat" ? 0.22 : 0.32));
  // the top
  diamond(); g.fillStyle = hex(look.base); g.fill();
  g.save(); diamond(); g.clip();
  g.strokeStyle = hex(look.line); g.lineWidth = px ? 1 : 1.1 * k; g.lineCap = "butt";
  const line = (u0: number, v0: number, u1: number, v1: number) => { g.beginPath(); g.moveTo(...P(u0, v0)); g.lineTo(...P(u1, v1)); g.stroke(); };
  const n = Math.max(2, Math.round(4 / Math.max(0.5, look.step) * (look.pattern === "planks" ? 1.2 : 1)));
  switch (look.pattern) {
    case "grid": for (let i = 1; i < n; i++) { line(i / n, 0, i / n, 1); line(0, i / n, 1, i / n); } break;
    case "planks": for (let i = 1; i < n * 2; i++) line(0, i / (n * 2), 1, i / (n * 2)); break;
    case "checker": for (let i = 0; i < n; i++) for (let j = 0; j < n; j++) if ((i + j) % 2) { g.beginPath(); g.moveTo(...P(i / n, j / n)); g.lineTo(...P((i + 1) / n, j / n)); g.lineTo(...P((i + 1) / n, (j + 1) / n)); g.lineTo(...P(i / n, (j + 1) / n)); g.closePath(); g.fillStyle = hex(look.line); g.globalAlpha = 0.55; g.fill(); g.globalAlpha = 1; } break;
    case "veins": for (const [a, b] of [[0.25, 0.1], [0.55, 0.45], [0.8, 0.7]]) { g.beginPath(); g.moveTo(...P(0, a)); g.quadraticCurveTo(...P(0.5, a + 0.18), ...P(1, b)); g.stroke(); } break;
    default: if (px) for (let i = 0; i < 9; i++) { const [x, y] = P(0.12 + ((i * 37) % 80) / 100, 0.12 + ((i * 53) % 80) / 100); g.fillStyle = hex(look.line); g.fillRect(Math.round(x), Math.round(y), 1, 1); }
  }
  if (style === "studio" || style === "clay") { // light falling from the top left: a soft gradient over the slab
    const gr = g.createLinearGradient(P(0, 0)[0], P(0, 0)[1], P(1, 1)[0], P(1, 1)[1]);
    gr.addColorStop(0, style === "clay" ? "rgba(255,240,215,0.38)" : "rgba(255,255,255,0.32)"); gr.addColorStop(1, "rgba(30,36,50,0.14)");
    g.fillStyle = gr; diamond(); g.fill();
  }
  g.restore();
  // edges: the ink line of Plano, a thin dark pixel outline for Pixel, a soft rounded rim for Maqueta
  diamond();
  if (style === "flat") { g.strokeStyle = "rgba(31,42,55,0.72)"; g.lineWidth = 1.1 * k; g.lineJoin = "round"; g.stroke(); }
  else if (style === "clay") { g.strokeStyle = "rgba(255,244,226,0.7)"; g.lineWidth = 1.6 * k; g.lineJoin = "round"; g.stroke(); }
  else if (px) { g.strokeStyle = "#2a2f3a"; g.lineWidth = 1; g.stroke(); }
  else { g.strokeStyle = "rgba(255,255,255,0.5)"; g.lineWidth = 1 * k; g.stroke(); }

  if (px) { // a small palette, like the Pixel office
    const img = g.getImageData(0, 0, W, H), d = img.data;
    for (let i = 0; i < d.length; i += 4) {
      if (d[i + 3] < 120) { d[i + 3] = 0; continue; }
      d[i + 3] = 255;
      for (let j = 0; j < 3; j++) d[i + j] = Math.round(Math.round((d[i + j] / 255) * 5) / 5 * 255);
    }
    g.putImageData(img, 0, 0);
  }
  const url = c.toDataURL("image/png");
  cache.set(key, url);
  return url;
}
