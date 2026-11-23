// Finishing touches of the editor's item pictures that depend on the office style. Pure canvas work, no React: the
// picture of an item is taken once per style (see shop/ThumbStudio) and cached.

/** Source side of a Pixel picture: displayed at a whole multiple of this (64, 128...) so the pixels stay square and sharp. */
export const PIXEL_SIDE = 64;
const LEVELS = 5; // per channel: a small fixed palette (125 colors) like the Pixel office

/**
 * A render turned into pixel art: reduced to PIXEL_SIDE with box filtering, alpha cut to on/off, colors snapped to a
 * small palette and a one-pixel dark contour on the silhouette. Returns a PNG data URL of PIXEL_SIDE x PIXEL_SIDE.
 */
export function pixelate(src: HTMLCanvasElement): string {
  const n = PIXEL_SIDE;
  const small = document.createElement("canvas");
  small.width = small.height = n;
  const g = small.getContext("2d", { willReadFrequently: true })!;
  g.imageSmoothingEnabled = true; g.imageSmoothingQuality = "high";
  g.drawImage(src, 0, 0, n, n);
  const img = g.getImageData(0, 0, n, n);
  const d = img.data;
  const solid = new Uint8Array(n * n);
  for (let i = 0; i < n * n; i++) solid[i] = d[i * 4 + 3] >= 110 ? 1 : 0;
  const snap = (v: number) => Math.round((Math.round((v / 255) * (LEVELS - 1)) / (LEVELS - 1)) * 255);
  for (let i = 0; i < n * n; i++) {
    const o = i * 4;
    if (!solid[i]) { d[o + 3] = 0; continue; }
    d[o] = snap(d[o]); d[o + 1] = snap(d[o + 1]); d[o + 2] = snap(d[o + 2]); d[o + 3] = 255;
  }
  const edge: number[] = [];
  for (let y = 0; y < n; y++) for (let x = 0; x < n; x++) {
    const i = y * n + x;
    if (!solid[i]) continue;
    const open = (xx: number, yy: number) => xx < 0 || yy < 0 || xx >= n || yy >= n || !solid[yy * n + xx];
    if (open(x - 1, y) || open(x + 1, y) || open(x, y - 1) || open(x, y + 1)) edge.push(i);
  }
  for (const i of edge) { const o = i * 4; d[o] = Math.round(d[o] * 0.38); d[o + 1] = Math.round(d[o + 1] * 0.4); d[o + 2] = Math.round(d[o + 2] * 0.46); }
  g.putImageData(img, 0, 0);
  return small.toDataURL("image/png");
}

/**
 * Crops a render to the piece itself and puts it back at `fill` of a side x side square (centered), so every picture
 * fills its window the same way whatever the piece's size. Returns a new canvas.
 */
export function fitted(src: HTMLCanvasElement, fill = 0.8, side = 256): HTMLCanvasElement {
  const probe = document.createElement("canvas");
  const n = 128;
  probe.width = probe.height = n;
  const pg = probe.getContext("2d", { willReadFrequently: true })!;
  pg.drawImage(src, 0, 0, n, n);
  const d = pg.getImageData(0, 0, n, n).data;
  let x0 = n, y0 = n, x1 = -1, y1 = -1;
  for (let y = 0; y < n; y++) for (let x = 0; x < n; x++) if (d[(y * n + x) * 4 + 3] > 24) { if (x < x0) x0 = x; if (x > x1) x1 = x; if (y < y0) y0 = y; if (y > y1) y1 = y; }
  const out = document.createElement("canvas");
  out.width = out.height = side;
  if (x1 < 0) return out;
  const k = src.width / n;
  const w = (x1 - x0 + 1) * k, h = (y1 - y0 + 1) * k;
  const s = Math.max(w, h) / fill;
  const cx = ((x0 + x1 + 1) / 2) * k, cy = ((y0 + y1 + 1) / 2) * k;
  const g = out.getContext("2d")!;
  g.imageSmoothingQuality = "high";
  g.drawImage(src, cx - s / 2, cy - s / 2, s, s, 0, 0, side, side);
  return out;
}

/** Displayed side of a Pixel picture for a box of `size` px: a whole multiple of PIXEL_SIDE (at least 1x). */
export const pixelDisplay = (size: number) => PIXEL_SIDE * Math.max(1, Math.floor(size / PIXEL_SIDE));
