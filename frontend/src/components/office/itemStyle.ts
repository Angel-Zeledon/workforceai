/**
 * Shape specs for the character shop items. The catalog (backend/internal/rewards/catalog.json) only carries an id, a
 * rarity and one or two colors; the SHAPE of every hat, pair of glasses, headset, scarf, badge and outfit lives here so
 * the 3D character (Worn.tsx) and the flat shop preview (ItemPreview.tsx) draw the same thing from one description.
 * An id missing from a table falls back to a shape derived from its hash, never to a single "default" look.
 */

export function hash(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619); }
  return h >>> 0;
}

const clamp255 = (n: number) => Math.max(0, Math.min(255, Math.round(n)));
function rgb(hex: string): [number, number, number] {
  const h = hex.replace("#", "");
  const f = h.length === 3 ? h.split("").map((c) => c + c).join("") : h;
  return [parseInt(f.slice(0, 2), 16), parseInt(f.slice(2, 4), 16), parseInt(f.slice(4, 6), 16)];
}
const toHex = (r: number, g: number, b: number) => "#" + [r, g, b].map((v) => clamp255(v).toString(16).padStart(2, "0")).join("");
/** Mixes two colors: t=0 is a, t=1 is b. */
export function mix(a: string, b: string, t: number): string {
  const x = rgb(a), y = rgb(b);
  return toHex(x[0] + (y[0] - x[0]) * t, x[1] + (y[1] - x[1]) * t, x[2] + (y[2] - x[2]) * t);
}
export const darken = (c: string, t: number) => mix(c, "#000000", t);
export const lighten = (c: string, t: number) => mix(c, "#ffffff", t);
/** Perceived brightness 0..1, to pick a readable trim over a color. */
export function luma(c: string): number { const [r, g, b] = rgb(c); return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255; }

// ---------------------------------------------------------------- hats

export type Crown = "dome" | "cyl" | "trunc" | "cone" | "puff" | "flat" | "none" | "pumpkin" | "mushroom" | "board" | "crown" | "tiara" | "wreath" | "turban" | "jester" | "bicorne" | "lobes" | "nemes" | "cloud" | "donut" | "cupcake" | "berries" | "shako";
export type Deco =
  | "pompom" | "feather" | "plume" | "fan" | "horns" | "bunny" | "cat" | "stars" | "flower" | "leaf" | "stem" | "sprout" | "wings"
  | "ball" | "halo" | "spikes" | "tassel" | "dots" | "bow" | "antenna" | "goggles" | "flaps" | "moon" | "crest" | "bolt" | "gem" | "shield" | "lava" | "slit" | "stripes" | "back";
export interface HatSpec {
  crown: Crown;
  h: number;          // crown height
  r?: number;         // base radius (default .19)
  rTop?: number;      // top radius for trunc/cyl
  brim?: number;      // brim outer radius (0 = none)
  front?: number;     // peak length forward (caps, visors)
  curl?: number;      // brim upturn 0..1 (negative: droops)
  band?: boolean;     // band in color2
  deco?: Deco[];
  finish?: "matte" | "knit" | "metal" | "glow" | "glass";
  swap?: boolean;     // draw the crown in color2 and the trims in color
}
const S = (crown: Crown, h: number, o: Partial<HatSpec> = {}): HatSpec => ({ crown, h, ...o });

const HATS: Record<string, HatSpec> = {
  cap: S("dome", 0.12, { front: 0.15 }),
  beanie: S("dome", 0.17, { finish: "knit", band: true, deco: ["pompom"] }),
  party: S("cone", 0.3, { r: 0.1, band: true, deco: ["pompom", "dots"] }),
  chef: S("puff", 0.24, { r: 0.165 }),
  graduate: S("board", 0.07, { band: true, deco: ["tassel"] }),
  top: S("trunc", 0.22, { r: 0.135, rTop: 0.125, brim: 0.215, band: true }),
  crown: S("crown", 0.1, { finish: "metal", deco: ["gem"] }),
  beret: S("dome", 0.09, { r: 0.21, deco: ["stem"], finish: "knit" }),
  straw: S("dome", 0.1, { brim: 0.31, band: true }),
  bucket: S("trunc", 0.13, { r: 0.19, rTop: 0.155, brim: 0.26, curl: -0.4 }),
  panama: S("dome", 0.12, { brim: 0.27, band: true }),
  toque: S("puff", 0.2, { r: 0.17, band: true }),
  sailor: S("flat", 0.08, { r: 0.17, brim: 0.24, curl: 0.9, band: true }),
  fez: S("trunc", 0.17, { r: 0.15, rTop: 0.115, deco: ["tassel"] }),
  bunny: S("none", 0.0, { deco: ["bunny"], band: true }),
  pumpkin: S("pumpkin", 0.17, { deco: ["stem"] }),
  leaf: S("dome", 0.06, { r: 0.16, deco: ["leaf"], band: true }),
  flower: S("dome", 0.08, { r: 0.17, band: true, deco: ["flower"] }),
  hardhat: S("dome", 0.14, { brim: 0.2, front: 0.12, deco: ["stripes"] }),
  santa: S("cone", 0.27, { r: 0.19, curl: -1, band: true, deco: ["pompom"], finish: "knit" }),
  safari: S("dome", 0.11, { brim: 0.27, band: true }),
  sunhat: S("dome", 0.08, { brim: 0.36, band: false, deco: ["bow"] }),
  turban: S("turban", 0.15, { band: true }),
  nightcap: S("cone", 0.3, { r: 0.18, curl: -1.4, finish: "knit", band: true, deco: ["pompom"] }),
  bandana: S("dome", 0.06, { r: 0.19, band: true, deco: ["back"] }),
  visor: S("none", 0, { front: 0.19, band: true, deco: ["back"] }),
  snow: S("dome", 0.18, { finish: "knit", band: true, deco: ["pompom", "stripes"] }),
  newsboy: S("dome", 0.08, { r: 0.21, front: 0.1, deco: ["stem"] }),
  explorer: S("dome", 0.15, { brim: 0.24, band: true }),
  shower: S("dome", 0.12, { r: 0.205, band: true, finish: "glass" }),
  headband: S("none", 0, { band: true, deco: ["bow"] }),
  cowboy: S("dome", 0.14, { brim: 0.34, curl: 0.55, band: true }),
  tricorn: S("flat", 0.1, { r: 0.17, brim: 0.27, curl: 1.0, band: true }),
  detective: S("dome", 0.13, { brim: 0.2, front: 0.12, deco: ["flaps", "back"] }),
  bowler: S("dome", 0.16, { brim: 0.225, curl: 0.3, band: true }),
  viking: S("dome", 0.14, { finish: "metal", deco: ["horns"], band: true }),
  wizard: S("cone", 0.4, { r: 0.17, brim: 0.3, band: true, deco: ["stars"] }),
  plumed: S("dome", 0.12, { brim: 0.27, curl: 0.7, band: true, deco: ["plume"] }),
  jester: S("jester", 0.3, { deco: ["ball"] }),
  laurel: S("wreath", 0.03, { r: 0.19 }),
  samurai: S("dome", 0.14, { brim: 0.27, curl: -0.8, finish: "metal", deco: ["crest"] }),
  knight: S("dome", 0.17, { finish: "metal", deco: ["slit", "plume"] }),
  fedora: S("trunc", 0.14, { r: 0.18, rTop: 0.14, brim: 0.275, band: true }),
  halo: S("none", 0, { deco: ["halo"], finish: "glow" }),
  astronaut: S("dome", 0.25, { r: 0.21, finish: "glass", band: true, deco: ["antenna"] }),
  dragon: S("dome", 0.12, { deco: ["horns", "spikes"] }),
  tiara: S("tiara", 0.1, { finish: "glass", deco: ["gem"] }),
  pilot: S("dome", 0.12, { r: 0.2, deco: ["goggles", "flaps"] }),
  mechanic: S("dome", 0.11, { front: 0.12, deco: ["stripes"] }),
  rainbow: S("dome", 0.17, { deco: ["stripes"] }),
  mintknit: S("dome", 0.17, { finish: "knit", band: false, deco: ["pompom"] }),
  blueberet: S("dome", 0.09, { r: 0.21, deco: ["stem"], finish: "knit" }),
  sunflower: S("dome", 0.06, { r: 0.17, deco: ["flower"], swap: true }),
  cactus: S("trunc", 0.2, { r: 0.1, rTop: 0.085, deco: ["flower", "spikes"] }),
  mushroom: S("mushroom", 0.16, { deco: ["dots"] }),
  rainhat: S("dome", 0.13, { brim: 0.26, curl: -0.6, deco: ["back"] }),
  catears: S("dome", 0.08, { r: 0.19, deco: ["cat"], band: true }),
  firefighter: S("dome", 0.15, { brim: 0.22, front: 0.1, deco: ["shield", "back"] }),
  wreath: S("wreath", 0.04, { r: 0.19, deco: ["gem"] }),
  easter: S("none", 0, { deco: ["bunny", "dots"], band: true }),
  boater: S("cyl", 0.1, { r: 0.15, rTop: 0.15, brim: 0.26, band: true }),
  donut: S("donut", 0.07, { deco: ["dots"] }),
  cupcake: S("cupcake", 0.2, { deco: ["gem"] }),
  glow: S("dome", 0.16, { finish: "glow", band: true }),
  monsoon: S("dome", 0.1, { brim: 0.33, curl: -0.9 }),
  hood: S("dome", 0.2, { r: 0.22, band: true, deco: ["back"] }),
  polkadot: S("dome", 0.14, { deco: ["dots", "bow"] }),
  lemon: S("dome", 0.1, { r: 0.18, deco: ["leaf"], band: true }),
  moonlight: S("cone", 0.26, { r: 0.18, curl: -1.1, finish: "knit", deco: ["moon", "pompom"] }),
  berry: S("berries", 0.1, { deco: ["leaf"] }),
  sprout: S("dome", 0.05, { r: 0.16, deco: ["sprout"], swap: true }),
  twilight: S("cone", 0.32, { r: 0.18, curl: -0.7, brim: 0.24, band: true, deco: ["stars"] }),
  charro: S("dome", 0.12, { brim: 0.37, curl: 0.5, band: true, deco: ["dots"] }),
  peacock: S("dome", 0.09, { r: 0.18, deco: ["fan"] }),
  pharaoh: S("nemes", 0.16, { band: true, deco: ["stripes"] }),
  astronomer: S("cone", 0.34, { r: 0.17, deco: ["moon", "stars"], band: true }),
  gladiator: S("dome", 0.15, { finish: "metal", deco: ["crest", "slit"] }),
  geisha: S("dome", 0.05, { r: 0.17, deco: ["flower", "tassel"], swap: true }),
  hussar: S("shako", 0.22, { r: 0.15, deco: ["plume", "gem"] }),
  alpine: S("trunc", 0.15, { r: 0.18, rTop: 0.14, brim: 0.22, curl: 0.4, band: true, deco: ["feather"] }),
  minstrel: S("dome", 0.12, { r: 0.2, curl: 0.4, deco: ["feather"], band: true }),
  disco: S("dome", 0.16, { deco: ["ball"], finish: "glass" }),
  robot: S("dome", 0.15, { finish: "metal", deco: ["antenna", "slit"] }),
  storm: S("cloud", 0.16, { deco: ["bolt"] }),
  matador: S("lobes", 0.14, { deco: ["tassel"] }),
  admiral: S("bicorne", 0.15, { finish: "matte", band: true, deco: ["gem"] }),
  royalturban: S("turban", 0.17, { band: true, deco: ["gem", "feather"] }),
  volcanic: S("dome", 0.16, { deco: ["spikes", "lava"], finish: "glow" }),
  imperial: S("trunc", 0.2, { r: 0.17, rTop: 0.14, band: true, deco: ["gem", "wings"], finish: "metal" }),
  mooncrown: S("crown", 0.12, { finish: "metal", deco: ["moon"] }),
  comet: S("dome", 0.13, { deco: ["stars", "wings"] }),
  obsidianhelm: S("dome", 0.17, { finish: "metal", deco: ["spikes", "slit"] }),
};

const HAT_FALLBACK: HatSpec[] = [
  S("dome", 0.12, { brim: 0.26, band: true }), S("dome", 0.17, { finish: "knit", deco: ["pompom"] }), S("trunc", 0.17, { r: 0.16, rTop: 0.13, band: true }),
];
/** The spec as drawn: crowns are raised a little so they clear the hair dome (which rises ~0.15 above the hat base). */
export function hatShape(id: string): HatSpec {
  const sp = hatSpec(id);
  return ["dome", "cyl", "trunc", "puff", "cone", "flat"].includes(sp.crown) ? { ...sp, h: sp.h + (sp.crown === "cone" ? 0.03 : 0.055) } : sp;
}
export function hatSpec(id: string): HatSpec { return HATS[id.replace(/^hat_/, "")] ?? HAT_FALLBACK[hash(id) % HAT_FALLBACK.length]; }

// ---------------------------------------------------------------- glasses

export type Frame = "round" | "square" | "aviator" | "cat" | "star" | "heart" | "visor" | "goggles" | "monocle" | "shutter" | "pixel" | "hex" | "ski" | "rimless" | "d3" | "welder" | "diamond";
export interface GlassesSpec { frame: Frame; tint: number; metal?: boolean; glow?: boolean }
const GL: Record<string, GlassesSpec> = {
  sun: { frame: "square", tint: 0.92 }, star: { frame: "star", tint: 0.8, metal: true }, visor: { frame: "visor", tint: 0.8, glow: true },
  heart: { frame: "heart", tint: 0.8 }, round: { frame: "round", tint: 0.25, metal: true }, aviator: { frame: "aviator", tint: 0.7, metal: true },
  cateye: { frame: "cat", tint: 0.55 }, tortoise: { frame: "round", tint: 0.5 }, goggles: { frame: "goggles", tint: 0.7 }, ski: { frame: "ski", tint: 0.75 },
  monocle: { frame: "monocle", tint: 0.2, metal: true }, threed: { frame: "d3", tint: 0.65 }, pixel: { frame: "pixel", tint: 0.95 }, rimless: { frame: "rimless", tint: 0.2 },
  reading: { frame: "round", tint: 0.18 }, ice: { frame: "hex", tint: 0.5 }, sunset: { frame: "aviator", tint: 0.7 }, lime: { frame: "shutter", tint: 0.85 },
  welder: { frame: "welder", tint: 0.95 }, steampunk: { frame: "goggles", tint: 0.6, metal: true }, ruby: { frame: "diamond", tint: 0.75, metal: true },
  emerald: { frame: "hex", tint: 0.75, metal: true }, sapphire: { frame: "square", tint: 0.75, metal: true }, amber: { frame: "round", tint: 0.7, metal: true },
  laser: { frame: "visor", tint: 0.85, glow: true }, crystal: { frame: "hex", tint: 0.4 }, onyx: { frame: "cat", tint: 0.95, metal: true },
  diamond: { frame: "diamond", tint: 0.55, metal: true }, nebula: { frame: "cat", tint: 0.8, glow: true }, bamboo: { frame: "round", tint: 0.3 },
  chili: { frame: "heart", tint: 0.8 }, mint: { frame: "round", tint: 0.45 }, lavender: { frame: "cat", tint: 0.45 }, cocoa: { frame: "square", tint: 0.8 },
  salmon: { frame: "cat", tint: 0.45 }, jade: { frame: "hex", tint: 0.55 }, butter: { frame: "round", tint: 0.4 }, denim: { frame: "square", tint: 0.6 },
  silver: { frame: "aviator", tint: 0.65, metal: true }, ochre: { frame: "square", tint: 0.55 }, storm: { frame: "shutter", tint: 0.85 }, fern: { frame: "round", tint: 0.5 },
  wine: { frame: "cat", tint: 0.7 }, retro: { frame: "cat", tint: 0.55 }, reef: { frame: "goggles", tint: 0.6 }, neon: { frame: "shutter", tint: 0.9, glow: true },
  topaz: { frame: "diamond", tint: 0.7, metal: true }, amethyst: { frame: "hex", tint: 0.7, metal: true }, magma: { frame: "visor", tint: 0.85, glow: true },
  plasma: { frame: "visor", tint: 0.8, glow: true }, cobalt: { frame: "square", tint: 0.8, metal: true }, arcade: { frame: "pixel", tint: 0.9, glow: true },
  glacier: { frame: "ski", tint: 0.55 }, polar: { frame: "ski", tint: 0.8 }, tourmaline: { frame: "diamond", tint: 0.7, metal: true }, liquidgold: { frame: "aviator", tint: 0.85, metal: true },
  aurora: { frame: "visor", tint: 0.7, glow: true }, hologram: { frame: "visor", tint: 0.5, glow: true }, ember: { frame: "welder", tint: 0.9, glow: true },
};
const FRAMES: Frame[] = ["round", "square", "aviator", "cat", "hex"];
export function glassesSpec(id: string): GlassesSpec { return GL[id.replace(/^glasses_/, "")] ?? { frame: FRAMES[hash(id) % FRAMES.length], tint: 0.6 }; }

// ---------------------------------------------------------------- headphones

export type Cans = "over" | "muffs" | "retro" | "buds" | "cat" | "dj" | "neon";
export interface CansSpec { kind: Cans; metal?: boolean; glow?: boolean }
const HP: Record<string, CansSpec> = {
  neon: { kind: "neon", glow: true }, gold: { kind: "over", metal: true }, retro: { kind: "retro" }, chrome: { kind: "retro", metal: true }, midnight: { kind: "dj" },
  turntable: { kind: "dj" }, tokyo: { kind: "cat", glow: true }, bebop: { kind: "retro" }, cosmic: { kind: "neon", glow: true }, aurora: { kind: "neon", glow: true },
  onyx: { kind: "dj", metal: true }, samba: { kind: "cat" }, flamingo: { kind: "cat" }, coconut: { kind: "muffs" }, sorbet: { kind: "muffs" }, frost: { kind: "muffs" },
  sahara: { kind: "retro" }, tanzanite: { kind: "over", metal: true }, tundra: { kind: "muffs" }, pearl: { kind: "over", metal: true }, quartz: { kind: "over", metal: true },
  ruby: { kind: "over", metal: true }, emerald: { kind: "over", metal: true }, reef: { kind: "buds" }, cola: { kind: "buds" }, pebble: { kind: "buds" },
};
const CANS: Cans[] = ["over", "muffs", "buds", "dj"];
export function cansSpec(id: string): CansSpec {
  if (id === "earmuffs_pink") return { kind: "muffs" };
  const k = id.replace(/^headphones_/, "");
  return HP[k] ?? { kind: CANS[hash(id) % CANS.length] };
}

// ---------------------------------------------------------------- scarves

export type ScarfPattern = "plain" | "stripes" | "tartan" | "rainbow" | "bands" | "dots";
export interface ScarfSpec { pattern: ScarfPattern; fringe: boolean; knot: boolean; bow?: boolean }
export function scarfSpec(id: string, hasColor2: boolean): ScarfSpec {
  if (id === "bowtie_gold") return { pattern: "plain", fringe: false, knot: false, bow: true };
  if (id === "scarf_stripes") return { pattern: "stripes", fringe: true, knot: true };
  if (id === "scarf_rainbow" || id === "scarf_aurora") return { pattern: "rainbow", fringe: true, knot: true };
  if (id === "scarf_tartan" || id === "scarf_mosaic" || id === "scarf_jazz") return { pattern: "tartan", fringe: true, knot: true };
  const h = hash(id);
  const patterns: ScarfPattern[] = hasColor2 ? ["stripes", "bands", "dots"] : ["plain", "plain", "bands", "dots"];
  return { pattern: patterns[h % patterns.length], fringe: h % 3 !== 0, knot: true };
}

// ---------------------------------------------------------------- badges

export type BadgeFrame = "circle" | "star" | "hex" | "shield" | "diamond" | "ribbon" | "heart";
export interface BadgeSpec { frame: BadgeFrame; ring: boolean }
const BG: Record<string, BadgeFrame> = {
  star: "star", heart: "heart", rocket: "circle", medal: "ribbon", shield: "shield", crown: "shield", crystal: "diamond", opal: "diamond", platinum: "hex", tile: "hex",
  galaxy: "circle", comet: "star", sun: "star", snowflake: "hex", compass: "circle", key: "shield", ticket: "diamond", vinyl: "circle", phoenix: "shield",
};
const BFR: BadgeFrame[] = ["circle", "hex", "shield", "diamond"];
export function badgeSpec(id: string): BadgeSpec {
  const k = id.replace(/^badge_/, "");
  return { frame: BG[k] ?? BFR[hash(id) % BFR.length], ring: hash(id + "r") % 2 === 0 };
}

// ---------------------------------------------------------------- outfits

export type OutfitStyle = "blazer" | "hoodie" | "cardigan" | "vest" | "jersey" | "overalls" | "trench";
const OUTFITS: OutfitStyle[] = ["blazer", "hoodie", "cardigan", "vest", "jersey", "overalls", "trench"];
export interface OutfitSpec { style: OutfitStyle; trim: string; shirt: string; pants: string; stripe: boolean; shiny: boolean }
export function outfitSpec(id: string, color: string, color2: string | undefined, rarity: string): OutfitSpec {
  const h = hash(id);
  const style = OUTFITS[h % OUTFITS.length];
  const dark = luma(color) < 0.45;
  const trim = color2 || (dark ? lighten(color, 0.55) : darken(color, 0.4));
  const shirt = style === "vest" || style === "overalls" ? (dark ? "#f6f1e7" : lighten(mix(color, "#f6f1e7", 0.7), 0.1)) : "#fffaf0";
  const pants = style === "overalls" ? darken(color, 0.15) : dark ? darken(mix(color, "#3b4252", 0.55), 0.1) : darken(mix(color, "#6b5a4a", 0.6), 0.25);
  return { style, trim, shirt, pants, stripe: (h >> 4) % 3 === 0, shiny: rarity === "epic" };
}
