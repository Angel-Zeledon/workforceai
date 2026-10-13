import { useId } from "react";
import type { ShopItem } from "@/lib/rewards";
import { FurnitureArt } from "./FurnitureArt";
import { mountOf } from "../office/furnitureStyle";
import { badgeSpec, cansSpec, darken, glassesSpec, hash, hatShape, lighten, luma, mix, outfitSpec, scarfSpec, type HatSpec } from "../office/itemStyle";

const SKIN = "#e8b894", HAIR = "#4a3020", INK = "#2c323b";
const F = 72; // px per scene unit: the 3D head has radius 0.165 ~ 12px here
const HX = 32, HY = 31, HR = 12.5; // head centre and radius in the 64x64 tile
const BASE_Y = 22; // y of the hat base (forehead line)

const RARITY_GLOW: Record<string, string> = { common: "#8f9bb0", rare: "#4a90e2", epic: "#9b6bff" };

/** A small bust to hang the item on: shoulders, neck, head, hair and a face. */
function Bust({ shirt = "#8da2bd", showHair = true, mouth = true }: { shirt?: string; showHair?: boolean; mouth?: boolean }) {
  return (
    <g>
      <path d="M10 64c0-12 8-17 22-17s22 5 22 17z" fill={shirt} stroke={INK} strokeWidth="1.2" strokeLinejoin="round" />
      <rect x="28" y="40" width="8" height="9" rx="3" fill={SKIN} stroke={INK} strokeWidth="1.1" />
      <circle cx={HX} cy={HY} r={HR} fill={SKIN} stroke={INK} strokeWidth="1.3" />
      {showHair && <path d="M19.5 29a12.5 12.5 0 0 1 25 0c-3-5-7-7-12.5-7S22.5 24 19.5 29z" fill={HAIR} />}
      <ellipse cx={HX - 4.6} cy={HY + 1.5} rx="1.3" ry="1.8" fill="#4a3626" /><ellipse cx={HX + 4.6} cy={HY + 1.5} rx="1.3" ry="1.8" fill="#4a3626" />
      {mouth && <path d={`M${HX - 2.6} ${HY + 7}q2.6 2 5.2 0`} fill="none" stroke="#8a3b3b" strokeWidth="1.2" strokeLinecap="round" />}
    </g>
  );
}

function HatArt({ id, c1: base, c2: alt }: { id: string; c1: string; c2: string }) {
  const s: HatSpec = hatShape(id);
  const c1 = s.swap ? alt : base, c2 = s.swap ? base : alt;
  const r = (s.r ?? 0.19) * F * 0.92, h = s.h * F, y0 = BASE_Y;
  const st = { stroke: INK, strokeWidth: 1.2, strokeLinejoin: "round" as const, strokeLinecap: "round" as const };
  const top = y0 - h;
  let crown: React.ReactNode = null;
  switch (s.crown) {
    case "dome": crown = <path d={`M${HX - r} ${y0}A${r} ${h} 0 0 1 ${HX + r} ${y0}z`} fill={c1} {...st} />; break;
    case "cyl": case "trunc": case "flat": { const rt = (s.rTop ?? s.r ?? 0.19) * F * 0.92; crown = <path d={`M${HX - r} ${y0}L${HX - rt} ${top}L${HX + rt} ${top}L${HX + r} ${y0}z`} fill={c1} {...st} />; break; }
    case "cone": crown = <path d={`M${HX - r} ${y0}L${HX + (s.curl ?? 0) * -3} ${top}L${HX + r} ${y0}z`} fill={c1} {...st} />; break;
    case "puff": crown = <g {...st}><rect x={HX - r * 0.9} y={y0 - h * 0.4} width={r * 1.8} height={h * 0.4} fill={c1} /><ellipse cx={HX} cy={y0 - h * 0.62} rx={r * 1.02} ry={h * 0.42} fill={c1} /></g>; break;
    case "pumpkin": crown = <g {...st}><ellipse cx={HX} cy={y0 - h * 0.4} rx={r} ry={h * 0.5} fill={c1} /><path d={`M${HX - 4} ${y0 - h * 0.85}Q${HX} ${y0 - h * 0.1} ${HX + 4} ${y0 - h * 0.85}`} fill="none" /></g>; break;
    case "mushroom": crown = <path d={`M${HX - r * 1.25} ${y0}A${r * 1.25} ${h} 0 0 1 ${HX + r * 1.25} ${y0}z`} fill={c1} {...st} />; break;
    case "board": crown = <g {...st}><rect x={HX - r * 0.8} y={y0 - 5} width={r * 1.6} height={5} fill={c1} /><path d={`M${HX - 17} ${y0 - 6}L${HX} ${y0 - 11}L${HX + 17} ${y0 - 6}L${HX} ${y0 - 1}z`} fill={c1} /></g>; break;
    case "crown": crown = <path d={`M${HX - 11} ${y0}V${y0 - 8}l5.5 4 5.5-8 5.5 8 5.5-4V${y0}z`} fill={c1} {...st} />; break;
    case "tiara": crown = <path d={`M${HX - 10} ${y0 + 1}Q${HX} ${y0 - 5} ${HX + 10} ${y0 + 1}l-3-1-3-6-4 -2-4 2-3 6z`} fill={c1} {...st} opacity={0.9} />; break;
    case "wreath": crown = <g>{Array.from({ length: 9 }, (_, i) => { const a = Math.PI + (i / 8) * Math.PI; return <ellipse key={i} cx={HX + Math.cos(a) * 12.5} cy={y0 + 1 + Math.sin(a) * 7} rx="3" ry="1.6" fill={i % 3 === 0 ? lighten(c1, 0.15) : c1} stroke={INK} strokeWidth="0.6" transform={`rotate(${(a * 180) / Math.PI + 90} ${HX + Math.cos(a) * 12.5} ${y0 + 1 + Math.sin(a) * 7})`} />; })}</g>; break;
    case "turban": crown = <g {...st}><path d={`M${HX - r * 1.05} ${y0}A${r * 1.05} ${h} 0 0 1 ${HX + r * 1.05} ${y0}z`} fill={c1} /><path d={`M${HX - r} ${y0 - 3}Q${HX} ${y0 - 8} ${HX + r} ${y0 - 3}M${HX - r * 0.9} ${y0 - 7}Q${HX} ${y0 - 12} ${HX + r * 0.9} ${y0 - 7}`} fill="none" stroke={c2} strokeWidth="1.6" /></g>; break;
    case "jester": crown = <g {...st}><path d={`M${HX - 10} ${y0}Q${HX - 18} ${y0 - 12} ${HX - 15} ${y0 - 17}Q${HX - 6} ${y0 - 8} ${HX} ${y0}z`} fill={c1} /><path d={`M${HX + 10} ${y0}Q${HX + 18} ${y0 - 12} ${HX + 15} ${y0 - 17}Q${HX + 6} ${y0 - 8} ${HX} ${y0}z`} fill={c2} /><circle cx={HX - 15} cy={y0 - 17} r="1.8" fill={c2} /><circle cx={HX + 15} cy={y0 - 17} r="1.8" fill={c1} /></g>; break;
    case "bicorne": crown = <path d={`M${HX - 20} ${y0 - 3}Q${HX - 10} ${y0 - 12} ${HX} ${y0 - 7}Q${HX + 10} ${y0 - 12} ${HX + 20} ${y0 - 3}Q${HX + 10} ${y0 + 1} ${HX} ${y0}Q${HX - 10} ${y0 + 1} ${HX - 20} ${y0 - 3}z`} fill={c1} {...st} />; break;
    case "lobes": crown = <g {...st}><path d={`M${HX - 14} ${y0}A7 ${h} 0 0 1 ${HX} ${y0}z`} fill={c1} /><path d={`M${HX} ${y0}A7 ${h} 0 0 1 ${HX + 14} ${y0}z`} fill={c1} /></g>; break;
    case "nemes": crown = <g {...st}><path d={`M${HX - r} ${y0}A${r} ${h} 0 0 1 ${HX + r} ${y0}z`} fill={c1} /><rect x={HX - r - 3} y={y0} width="5" height="16" fill={c1} /><rect x={HX + r - 2} y={y0} width="5" height="16" fill={c1} /></g>; break;
    case "cloud": crown = <g {...st}>{[[-8, 4, 6], [0, 1, 7], [8, 4, 6], [-3, -2, 5], [4, -1, 5]].map(([x, y, rr], i) => <circle key={i} cx={HX + x} cy={y0 - 4 + y - 3} r={rr} fill={i % 2 ? lighten(c1, 0.15) : c1} />)}</g>; break;
    case "donut": crown = <g {...st}><ellipse cx={HX} cy={y0 - 3} rx="13" ry="6.5" fill={c1} /><ellipse cx={HX} cy={y0 - 4.5} rx="11" ry="4.8" fill={c2} /><ellipse cx={HX} cy={y0 - 4.5} rx="3.2" ry="1.6" fill="#e8b894" stroke="none" /></g>; break;
    case "cupcake": crown = <g {...st}><path d={`M${HX - 10} ${y0}l2-6h16l2 6z`} fill={mix(c1, "#c0a080", 0.5)} /><path d={`M${HX - 9} ${y0 - 6}Q${HX} ${y0 - 17} ${HX + 9} ${y0 - 6}z`} fill={c1} /><circle cx={HX} cy={y0 - 14} r="1.6" fill="#e63946" /></g>; break;
    case "berries": crown = <g {...st}>{[[0, -8, 5], [-7, -4, 5], [7, -4, 5], [-3, -3, 5], [3, -3, 5]].map(([x, y, rr], i) => <circle key={i} cx={HX + x} cy={y0 + y} r={rr} fill={i % 2 ? lighten(c1, 0.12) : c1} />)}</g>; break;
    case "shako": crown = <g {...st}><path d={`M${HX - r * 0.9} ${y0}L${HX - r * 0.8} ${top}H${HX + r * 0.8}L${HX + r * 0.9} ${y0}z`} fill={c1} /><rect x={HX - r} y={top - 1.5} width={r * 2} height="3" fill={mix(c1, c2, 0.25)} /></g>; break;
  }
  const brim = s.brim ? <ellipse cx={HX} cy={y0 + (s.curl && s.curl < 0 ? 1.5 : 0.5)} rx={s.brim * F * 0.9} ry={2.3} fill={c1} {...st} /> : null;
  const peak = s.front ? <path d={`M${HX} ${y0 - 1}h${s.front * F * 0.9 + 5}a5 2 0 0 1-3 2.6H${HX}z`} fill={c1} {...st} /> : null;
  const band = s.band && !["cone", "wreath", "none", "pumpkin", "mushroom", "board", "cupcake", "turban", "tiara", "crown", "jester", "bicorne", "lobes", "nemes", "cloud", "donut", "berries"].includes(s.crown)
    ? <path d={`M${HX - r + 0.5} ${y0 - 1.6}h${r * 2 - 1}`} stroke={c2} strokeWidth="2.4" strokeLinecap="butt" /> : s.band && s.crown === "none" ? <path d={`M${HX - 12} ${y0 + 1}Q${HX} ${y0 - 3} ${HX + 12} ${y0 + 1}`} fill="none" stroke={c1} strokeWidth="3" /> : null;
  const deco = (s.deco ?? []).map((d) => {
    switch (d) {
      case "pompom": return <circle key={d} cx={HX} cy={top - 2} r="3.2" fill={mix(c2, "#fff", 0.2)} {...st} />;
      case "horns": return <g key={d} {...st}><path d={`M${HX - r} ${y0 - 3}l-6-9 7 4z`} fill={c2} /><path d={`M${HX + r} ${y0 - 3}l6-9-7 4z`} fill={c2} /></g>;
      case "bunny": return <g key={d} {...st}><ellipse cx={HX - 5} cy={y0 - 13} rx="3" ry="9" fill={c1} transform={`rotate(-10 ${HX - 5} ${y0 - 13})`} /><ellipse cx={HX + 5} cy={y0 - 13} rx="3" ry="9" fill={c1} transform={`rotate(10 ${HX + 5} ${y0 - 13})`} /></g>;
      case "cat": return <g key={d} {...st}><path d={`M${HX - 12} ${y0 - 3}l1-9 7 5z`} fill={c1} /><path d={`M${HX + 12} ${y0 - 3}l-1-9-7 5z`} fill={c1} /></g>;
      case "feather": case "plume": return <path key={d} d={`M${HX + r - 2} ${y0 - 3}q8-12 3-18-2 8-8 14z`} fill={c2} {...st} />;
      case "fan": return <g key={d} {...st}>{[-30, -15, 0, 15, 30].map((a, i) => <ellipse key={i} cx={HX} cy={top - 8} rx="2.6" ry="8" fill={i % 2 ? c1 : c2} transform={`rotate(${a} ${HX} ${top})`} />)}</g>;
      case "flower": return <g key={d}>{[0, 72, 144, 216, 288].map((a) => <ellipse key={a} cx={HX + 7} cy={top - 4} rx="2.4" ry="3.2" fill={c1} stroke={INK} strokeWidth="0.6" transform={`rotate(${a} ${HX + 7} ${top - 1})`} />)}<circle cx={HX + 7} cy={top - 1} r="2" fill={c2} /></g>;
      case "leaf": return <g key={d} {...st}><ellipse cx={HX + 4} cy={top - 2} rx="5" ry="2" fill={c2} transform={`rotate(-30 ${HX + 4} ${top - 2})`} /><ellipse cx={HX - 4} cy={top - 2} rx="5" ry="2" fill={lighten(c2, 0.15)} transform={`rotate(30 ${HX - 4} ${top - 2})`} /></g>;
      case "stem": return <rect key={d} x={HX - 1} y={top - 4} width="2.4" height="4" fill={c2} stroke={INK} strokeWidth="0.8" />;
      case "sprout": return <g key={d} {...st}><path d={`M${HX} ${top} v-6`} fill="none" /><ellipse cx={HX + 4} cy={top - 7} rx="4" ry="1.8" fill={c1} transform={`rotate(-25 ${HX + 4} ${top - 7})`} /><ellipse cx={HX - 4} cy={top - 7} rx="4" ry="1.8" fill={lighten(c1, 0.15)} transform={`rotate(25 ${HX - 4} ${top - 7})`} /></g>;
      case "halo": return <ellipse key={d} cx={HX} cy={y0 - 10} rx="11" ry="3" fill="none" stroke={c1} strokeWidth="2.6" />;
      case "stars": return <g key={d} fill={c2}><circle cx={HX - 3} cy={top + h * 0.45} r="1.4" /><circle cx={HX + 4} cy={top + h * 0.7} r="1.3" /></g>;
      case "antenna": return <g key={d}><path d={`M${HX} ${top} v-6`} stroke={INK} strokeWidth="1" /><circle cx={HX} cy={top - 7} r="1.8" fill={c2} /></g>;
      case "spikes": return <g key={d} fill={c2} stroke={INK} strokeWidth="0.8">{[-6, 0, 6].map((x) => <path key={x} d={`M${HX + x - 2} ${top + 1}l2-5 2 5z`} />)}</g>;
      case "gem": return <path key={d} d={`M${HX} ${y0 - 5}l2.4 2.4L${HX} ${y0}l-2.4-2.6z`} fill={c2} stroke={INK} strokeWidth="0.8" />;
      case "dots": return <g key={d} fill={c2}>{[[-6, -3], [3, -5], [8, -2], [-2, -8]].map(([x, y], i) => <circle key={i} cx={HX + x} cy={y0 + y} r="1.5" />)}</g>;
      case "stripes": return s.front ? <path key={d} d={`M${HX} ${y0}V${top}`} stroke={c2} strokeWidth="2.4" /> : <path key={d} d={`M${HX - r + 1} ${y0 - 4}h${r * 2 - 2}`} stroke={c2} strokeWidth="1.6" />;
      case "tassel": return <path key={d} d={`M${HX + r - 3} ${y0 - 5}q8 2 8 9`} fill="none" stroke={c2} strokeWidth="1.6" strokeLinecap="round" />;
      case "bow": return <g key={d} fill={c2} stroke={INK} strokeWidth="0.8"><path d={`M${HX + 8} ${y0 - 2}l-4-3v6zM${HX + 8} ${y0 - 2}l4-3v6z`} /></g>;
      case "wings": return <g key={d} fill={c2} stroke={INK} strokeWidth="0.8"><path d={`M${HX - r} ${y0 - 3}l-7-6 1 7zM${HX + r} ${y0 - 3}l7-6-1 7z`} /></g>;
      case "moon": return <path key={d} d={`M${HX + 5} ${top + 2}a4 4 0 1 0 4 5 3.4 3.4 0 1 1-4-5z`} fill={c2} stroke={INK} strokeWidth="0.6" />;
      case "goggles": return <g key={d} fill={c2} stroke={INK} strokeWidth="0.8"><circle cx={HX - 4.5} cy={y0 - 3} r="2.6" /><circle cx={HX + 4.5} cy={y0 - 3} r="2.6" /></g>;
      case "crest": return <path key={d} d={`M${HX - 10} ${top + 1}Q${HX} ${top - 8} ${HX + 10} ${top + 1}`} fill="none" stroke={c2} strokeWidth="3" strokeLinecap="round" />;
      case "slit": return <path key={d} d={`M${HX - 7} ${y0 - 2}h14`} stroke="#10131a" strokeWidth="1.6" />;
      case "shield": return <path key={d} d={`M${HX - 3} ${y0 - 6}h6v5l-3 3-3-3z`} fill={c2} stroke={INK} strokeWidth="0.8" />;
      case "lava": return <g key={d} fill={c2}><circle cx={HX - 4} cy={y0 - 6} r="1.3" /><circle cx={HX + 5} cy={y0 - 8} r="1.1" /></g>;
      case "bolt": return <path key={d} d={`M${HX + 1} ${y0 - 9}l-3 5h3l-2 5 5-7h-3z`} fill={c2} />;
      case "flaps": return <g key={d} fill={mix(c1, "#000", 0.12)} stroke={INK} strokeWidth="0.9"><rect x={HX - r - 2} y={y0 - 1} width="4" height="9" rx="2" /><rect x={HX + r - 2} y={y0 - 1} width="4" height="9" rx="2" /></g>;
      case "ball": return s.crown === "jester" ? null : <circle key={d} cx={HX} cy={top - 3} r="3" fill="#dfe7ef" stroke={INK} strokeWidth="1" />;
      default: return null;
    }
  });
  return <g>{brim}{crown}{peak}{band}{deco}</g>;
}

function GlassesArt({ id, c }: { id: string; c: string }) {
  const sp = glassesSpec(id);
  const dark = luma(c) < 0.18;
  const frame = sp.metal ? mix(c, "#f0d9a0", 0.35) : dark ? "#14171c" : darken(c, 0.25);
  const lens = sp.tint > 0.6 ? (dark ? "#0b0d11" : mix(c, "#10202c", 0.45)) : lighten(c, 0.6);
  const op = 0.35 + sp.tint * 0.55;
  const y = HY + 0.5, L = HX - 5.2, R = HX + 5.2;
  const st = { stroke: frame, strokeWidth: 1.5, fill: lens, fillOpacity: op };
  const bridge = <path d={`M${HX - 1.6} ${y - 1}h3.2`} stroke={frame} strokeWidth="1.2" />;
  switch (sp.frame) {
    case "round": case "monocle": return <g>{(sp.frame === "monocle" ? [L] : [L, R]).map((x) => <circle key={x} cx={x} cy={y} r="4.2" {...st} />)}{bridge}</g>;
    case "square": case "pixel": case "d3": return <g>{[L, R].map((x, i) => <rect key={x} x={x - 4.4} y={y - 3} width="8.8" height={sp.frame === "pixel" ? 4.4 : 6} rx="1" {...st} fill={sp.frame === "d3" ? (i ? "#2ec4ff" : "#e63946") : lens} />)}{bridge}</g>;
    case "aviator": return <g>{[L, R].map((x) => <path key={x} d={`M${x - 4.6} ${y - 3}h9.2q0 8-4.6 8t-4.6-8z`} {...st} />)}{bridge}</g>;
    case "cat": return <g>{[L, R].map((x, i) => <path key={x} d={`M${x - 4.6} ${y - 1.5}l${i ? 0 : 0} 3a4.6 3.4 0 0 0 9.2 0v-3.4q-4.6-2.4-9.2 0z`} {...st} />)}{[L - 4.6, R + 4.6].map((x, i) => <path key={x} d={`M${x} ${y - 1.5}l${i ? 2 : -2}-2.6`} stroke={frame} strokeWidth="1.6" />)}{bridge}</g>;
    case "star": return <g>{[L, R].map((x) => <path key={x} d={`M${x} ${y - 4.6}l1.3 3 3.2.3-2.4 2.1.8 3.2-2.9-1.7-2.9 1.7.8-3.2-2.4-2.1 3.2-.3z`} {...st} fill={c} fillOpacity="0.9" />)}</g>;
    case "heart": return <g>{[L, R].map((x) => <path key={x} d={`M${x} ${y + 4}c-6-3.6-6-8-2.6-8a2.6 2.6 0 0 1 2.6 1.6 2.6 2.6 0 0 1 2.6-1.6c3.4 0 3.4 4.4-2.6 8z`} {...st} fill={c} fillOpacity="0.9" />)}</g>;
    case "visor": return <rect x={HX - 12} y={y - 3} width="24" height="6" rx="3" fill={c} fillOpacity="0.85" stroke={frame} strokeWidth="1" />;
    case "goggles": case "welder": return <g>{[L, R].map((x) => <circle key={x} cx={x} cy={y} r="4.8" {...st} strokeWidth="2.2" />)}<path d={`M${HX - 12.5} ${y}h-1M${HX + 12.5} ${y}h1`} stroke={frame} strokeWidth="1.4" /></g>;
    case "ski": return <rect x={HX - 12} y={y - 4} width="24" height="8.5" rx="4" {...st} strokeWidth="1.8" />;
    case "shutter": return <g>{[L, R].map((x) => <g key={x}><rect x={x - 4.6} y={y - 3} width="9.2" height="6" fill={frame} fillOpacity="0.5" />{[-2, 0, 2].map((dy) => <rect key={dy} x={x - 4.4} y={y + dy - 0.7} width="8.8" height="1.4" fill={c} />)}</g>)}{bridge}</g>;
    case "hex": return <g>{[L, R].map((x) => <path key={x} d={`M${x - 4.6} ${y}l2.3-4h4.6l2.3 4-2.3 4h-4.6z`} {...st} />)}{bridge}</g>;
    case "diamond": return <g>{[L, R].map((x) => <path key={x} d={`M${x} ${y - 4.6}l5 4.6-5 4.6-5-4.6z`} {...st} />)}{bridge}</g>;
    default: return <g>{[L, R].map((x) => <circle key={x} cx={x} cy={y} r="4.2" fill={lens} fillOpacity={op} stroke="none" />)}{bridge}</g>;
  }
}

function CansArt({ id, c }: { id: string; c: string }) {
  const sp = cansSpec(id);
  const fill = sp.metal ? mix(c, "#f4e3b0", 0.25) : c;
  const st = { stroke: INK, strokeWidth: 1.2, strokeLinejoin: "round" as const };
  if (sp.kind === "buds") return <g>{[-1, 1].map((sd) => <g key={sd}><circle cx={HX + sd * (HR + 1)} cy={HY + 4} r="2.8" fill="#f4f1ea" {...st} /><path d={`M${HX + sd * (HR + 1)} ${HY + 6.5}v6`} stroke={INK} strokeWidth="1.8" strokeLinecap="round" /></g>)}</g>;
  const band = sp.kind === "muffs" ? "#f4f1ea" : sp.kind === "retro" ? mix(c, "#2a1d12", 0.7) : "#2c323b";
  return (
    <g>
      <path d={`M${HX - HR - 1} ${HY + 1}a${HR + 1} ${HR + 2} 0 0 1 ${(HR + 1) * 2} 0`} fill="none" stroke={band} strokeWidth={sp.kind === "retro" ? 3.6 : 2.4} strokeLinecap="round" />
      {sp.kind === "cat" && <g fill={fill} {...st}><path d={`M${HX - 8} ${HY - 11}l2-6 4 5z`} /><path d={`M${HX + 8} ${HY - 11}l-2-6-4 5z`} /></g>}
      {[-1, 1].map((sd) => sp.kind === "muffs"
        ? <ellipse key={sd} cx={HX + sd * (HR + 1.5)} cy={HY + 2} rx="4.6" ry="6.2" fill={c} {...st} />
        : <rect key={sd} x={HX + sd * (HR + 1.5) - 3.2} y={HY - 3.5} width="6.4" height={sp.kind === "retro" ? 13 : 11} rx="3" fill={fill} {...st} />)}
    </g>
  );
}

function ScarfArt({ id, c, c2 }: { id: string; c: string; c2: string }) {
  const sp = scarfSpec(id, c2 !== "");
  const col2 = c2 || (luma(c) > 0.55 ? darken(c, 0.4) : lighten(c, 0.55));
  const st = { stroke: INK, strokeWidth: 1.2, strokeLinejoin: "round" as const };
  if (sp.bow) return <g {...st}><path d={`M${HX} 47L${HX - 11} 41v12zM${HX} 47l11-6v12z`} fill={c} /><circle cx={HX} cy="47" r="3" fill={c} /></g>;
  const rings = sp.pattern === "stripes" ? [col2, col2] : sp.pattern === "rainbow" ? ["#f4895a", "#e8b53a", "#4cb987", "#5a95e8"] : sp.pattern === "bands" ? [col2] : [];
  return (
    <g {...st}>
      <path d="M17 44c9 7 21 7 30 0v7c-9 7-21 7-30 0z" fill={c} />
      {rings.map((rc, i) => <path key={i} d={`M${20 + i} ${46 + i * 2.4}q12 5 24 0`} fill="none" stroke={rc} strokeWidth="1.8" />)}
      <path d="M38 51l8 14h-8l-5-12z" fill={c} />
      {sp.pattern === "stripes" && <path d="M36 59l6 .2" stroke={col2} strokeWidth="2.4" />}
      {sp.pattern === "tartan" && <path d="M39 53l5 9M42 52l4 8" stroke={col2} strokeWidth="1.8" />}
      {sp.fringe && <path d="M38 65.5v2M40.5 65.5v2M43 65.5v2" stroke={INK} strokeWidth="1" />}
    </g>
  );
}

function BadgeArt({ id, c, c2 }: { id: string; c: string; c2: string }) {
  const sp = badgeSpec(id);
  const inner = c2 || (luma(c) > 0.6 ? darken(c, 0.4) : "#fffaf0");
  const st = { stroke: INK, strokeWidth: 1.3, strokeLinejoin: "round" as const };
  const x = 21, y = 55; // on the chest
  const frame = (() => {
    switch (sp.frame) {
      case "star": return <path d={`M${x} ${y - 8}l2.3 5 5.3.7-3.9 3.7 1 5.3-4.7-2.6-4.7 2.6 1-5.3-3.9-3.7 5.3-.7z`} fill={c} {...st} />;
      case "heart": return <path d={`M${x} ${y + 7}c-10-6-10-13-4.6-13a4.4 4.4 0 0 1 4.6 2.8 4.4 4.4 0 0 1 4.6-2.8c5.4 0 5.4 7-4.6 13z`} fill={c} {...st} />;
      case "hex": return <path d={`M${x - 8} ${y}l4-7h8l4 7-4 7h-8z`} fill={c} {...st} />;
      case "shield": return <path d={`M${x - 7} ${y - 7}h14v8c0 5-7 8-7 8s-7-3-7-8z`} fill={c} {...st} />;
      case "diamond": return <path d={`M${x} ${y - 9}l8 9-8 9-8-9z`} fill={c} {...st} />;
      default: return <circle cx={x} cy={y} r="8" fill={c} {...st} />;
    }
  })();
  return <g>{id === "badge_medal" && <path d={`M${x - 3} ${y - 8}l-2-8h10l-2 8`} fill={inner} {...st} />}{frame}{sp.ring && <circle cx={x} cy={y} r="5.2" fill="none" stroke={inner} strokeWidth="1" />}<circle cx={x} cy={y} r="2.6" fill={inner} /></g>;
}

function OutfitArt({ item, c }: { item: ShopItem; c: string }) {
  const o = outfitSpec(item.id, c, item.color2, item.rarity);
  const st = { stroke: INK, strokeWidth: 1.3, strokeLinejoin: "round" as const };
  const body = o.style === "overalls" ? o.shirt : c;
  const sleeve = o.style === "vest" || o.style === "overalls" ? o.shirt : c;
  const trim = o.trim;
  return (
    <g {...st}>
      <path d="M9 64c0-12 6-19 14-21l9 3 9-3c8 2 14 9 14 21z" fill={body} />
      <path d="M9 64c-1-9 1-15 5-18l4 3c-3 4-4 9-3 15zM55 64c1-9-1-15-5-18l-4 3c3 4 4 9 3 15z" fill={sleeve} />
      <rect x="28" y="38" width="8" height="9" rx="3" fill={SKIN} />
      <circle cx={HX} cy="29" r="11" fill={SKIN} /><path d="M21 27a11 11 0 0 1 22 0c-3-4-7-5.5-11-5.5S24 23 21 27z" fill={HAIR} stroke="none" />
      <ellipse cx="28" cy="30" rx="1.2" ry="1.7" fill="#4a3626" stroke="none" /><ellipse cx="36" cy="30" rx="1.2" ry="1.7" fill="#4a3626" stroke="none" />
      {o.style === "blazer" && <g><path d="M32 46l-8 4 3 10 5-9z" fill={darken(c, 0.16)} /><path d="M32 46l8 4-3 10-5-9z" fill={darken(c, 0.16)} /><path d="M29 47l3 8 3-8" fill="#fffaf0" stroke="none" /><circle cx="32" cy="58" r="1" fill={trim} stroke="none" /></g>}
      {o.style === "cardigan" && <g><path d="M27 46l5 12 5-12z" fill={o.shirt} /><path d="M32 52v12" stroke={darken(c, 0.2)} /><g fill={trim} stroke="none">{[54, 58, 62].map((y) => <circle key={y} cx="32" cy={y} r="1" />)}</g><path d="M10 64h44" stroke={darken(c, 0.2)} strokeWidth="2.4" /></g>}
      {o.style === "hoodie" && <g><path d="M22 44q10 8 20 0" fill={darken(c, 0.12)} /><rect x="24" y="55" width="16" height="7" rx="2" fill={darken(c, 0.08)} /><path d="M29 47v8M35 47v8" stroke={trim} strokeWidth="1" /></g>}
      {o.style === "vest" && <g><path d="M22 44l10 8 10-8v20H22z" fill={c} /><path d="M27 46l5 8 5-8z" fill={o.shirt} stroke="none" /><path d="M32 49l-1.6 5 1.6 6 1.6-6z" fill={trim} stroke="none" /></g>}
      {o.style === "jersey" && <g><path d="M24 43q8 6 16 0" fill="none" stroke={trim} strokeWidth="2.6" /><circle cx="32" cy="56" r="5" fill={trim} /><path d="M32 53v6" stroke={luma(trim) > 0.5 ? "#222" : "#fff"} strokeWidth="1.6" /></g>}
      {o.style === "overalls" && <g><path d="M24 64V54h16v10z" fill={c} /><path d="M24 54l-1-9M40 54l1-9" stroke={c} strokeWidth="3.4" /><circle cx="24" cy="54" r="1.2" fill={trim} stroke="none" /><circle cx="40" cy="54" r="1.2" fill={trim} stroke="none" /></g>}
      {o.style === "trench" && <g><path d="M32 46l-9 4 2 14 7-9z" fill={darken(c, 0.15)} /><path d="M32 46l9 4-2 14-7-9z" fill={darken(c, 0.15)} /><path d="M10 57h44" stroke={trim} strokeWidth="2.4" /><g fill={trim} stroke="none">{[52, 61].map((y) => <g key={y}><circle cx="29" cy={y} r="1" /><circle cx="35" cy={y} r="1" /></g>)}</g></g>}
      {o.stripe && o.style !== "jersey" && <path d="M12 52h40" stroke={trim} strokeWidth="1.4" opacity=".9" />}
    </g>
  );
}

function BuddyArt({ id, c1, c2 }: { id: string; c1: string; c2: string }) {
  const dark = INK;
  const s = { stroke: dark, strokeWidth: 1.6, strokeLinejoin: "round" as const, strokeLinecap: "round" as const };
  return id.startsWith("buddy_duck") ? (<g {...s}><ellipse cx="30" cy="40" rx="16" ry="11" fill={c1} /><path d="M18 42q4 6 12 4" fill="none" stroke={darken(c1, 0.15)} /><circle cx="42" cy="26" r="9" fill={c1} /><path d="M49 27h9l-8 5z" fill="#f4895a" /><circle cx="44" cy="24" r="1.4" fill={dark} /><path d="M14 40l-5-4 1 7z" fill={c1} /></g>)
    : id.startsWith("buddy_robot") ? (<g {...s}><rect x="18" y="22" width="28" height="24" rx="6" fill={c1} /><circle cx="26" cy="32" r="3.2" fill={c2} /><circle cx="38" cy="32" r="3.2" fill={c2} /><path d="M32 22v-7M28 15h8M26 40h12" /><rect x="14" y="30" width="4" height="8" rx="2" fill={darken(c1, 0.25)} /><rect x="46" y="30" width="4" height="8" rx="2" fill={darken(c1, 0.25)} /></g>)
    : (<g {...s}><ellipse cx="30" cy="38" rx="11" ry="15" fill={c1} /><circle cx="36" cy="20" r="8" fill={c1} /><path d="M42 20l9 3-9 4z" fill="#f2c94c" /><circle cx="37" cy="18" r="1.4" fill={dark} /><path d="M24 50l-6 8M32 52l-2 8" /><path d="M24 30c-6 4-7 10-5 16" fill="none" stroke={c2} strokeWidth="4" /></g>);
}

/** Flat illustration of a shop item: character items hang on a small bust (same shapes as the 3D character), office items
 *  are drawn alone. The tile's backdrop and glow come from the rarity. */
export function ItemPreview({ item, size = 72 }: { item: ShopItem; size?: number }) {
  const uid = useId().replace(/:/g, "");
  const c1 = item.color || "#8f78c9";
  const c2 = item.color2 || "#f4f1ea";
  const alt = item.color2 || (luma(c1) > 0.6 ? darken(c1, 0.45) : lighten(c1, 0.5));
  const dark = "#2c323b";
  const s = { stroke: dark, strokeWidth: 1.6, strokeLinejoin: "round" as const, strokeLinecap: "round" as const };
  const glow = RARITY_GLOW[item.rarity] ?? RARITY_GLOW.common;
  let art: React.ReactNode = null;
  const id = item.id;
  switch (item.category) {
    case "outfit": art = <OutfitArt item={item} c={c1} />; break;
    case "hat": art = <g><Bust /><HatArt id={id} c1={c1} c2={alt} /></g>; break;
    case "glasses": art = <g><Bust /><GlassesArt id={id} c={c1} /></g>; break;
    case "headphones": art = <g><Bust /><CansArt id={id} c={c1} /></g>; break;
    case "scarf": art = <g><Bust shirt="#8da2bd" /><ScarfArt id={id} c={c1} c2={item.color2 || ""} /></g>; break;
    case "badge": art = <g><Bust shirt="#4b5d78" /><BadgeArt id={id} c={c1} c2={item.color2 || ""} /></g>; break;
    case "buddy": art = <BuddyArt id={id} c1={c1} c2={c2} />; break;
    case "plant":
      art = (<g {...s}><path d="M22 44h20l-3 14H25z" fill="#c4aa84" />{id === "plant_cactus" ? (<><rect x="26" y="18" width="12" height="28" rx="6" fill={c1} /><rect x="16" y="26" width="6" height="14" rx="3" fill={c1} /><rect x="42" y="22" width="6" height="14" rx="3" fill={c1} /></>) : id === "plant_flowers" ? (<><path d="M32 44V28M24 44V34M40 44V30" fill="none" stroke="#4f8a62" /><circle cx="32" cy="24" r="5" fill={c1} /><circle cx="24" cy="30" r="4.5" fill={c1} /><circle cx="40" cy="26" r="4.5" fill="#f2c94c" /></>) : (<><path d="M32 44C20 40 14 28 12 18c12 2 18 10 20 26zM32 44C44 40 50 28 52 18c-12 2-18 10-20 26zM32 44C30 32 30 20 32 10c4 12 4 24 0 34z" fill={c1} /></>)}</g>);
      break;
    case "lamp":
      art = (<g {...s}>{id === "lamp_lava" ? (<><path d="M24 54h16l-2-30H26z" fill={c1} opacity=".9" /><circle cx="32" cy="38" r="5" fill={c2} /><circle cx="31" cy="28" r="3" fill={c2} /><path d="M26 24h12l-3-9H29z" fill="#8a8f98" /></>) : (<><path d="M32 22v30M24 54h16" fill="none" strokeWidth="2.2" /><path d="M20 24l5-12h14l5 12z" fill={c1} /></>)}</g>);
      break;
    case "rug":
      art = (<g {...s}><rect x="8" y="22" width="48" height="22" rx="4" fill={c1} /><rect x="13" y="27" width="38" height="12" rx="2" fill="none" stroke={c2} />{id === "rug_persian" && <path d="M32 27l8 6-8 6-8-6z" fill={c2} />}</g>);
      break;
    case "coffee":
      art = (<g {...s}><rect x="16" y="12" width="32" height="38" rx="5" fill={c1} /><rect x="22" y="20" width="20" height="8" rx="2" fill="#fffaf0" /><path d="M24 50v6h16v-6" fill="#c9d3dc" /><rect x="27" y="38" width="10" height="9" rx="2" fill="#f4efe6" /></g>);
      break;
    case "poster":
      art = (<g {...s}><rect x="14" y="8" width="36" height="46" rx="2" fill="#fffaf0" /><rect x="19" y="13" width="26" height="30" fill={c1} />{id === "poster_mountain" ? <path d="M19 43l9-14 6 8 5-7 6 13z" fill={c2} /> : id === "poster_rocket" ? <path d="M32 18c5 5 5 13 0 20-5-7-5-15 0-20z" fill={c2} /> : <circle cx="32" cy="28" r="9" fill={c2} />}<path d="M22 49h20" /></g>);
      break;
    case "shelf":
      art = (<g {...s}><rect x="12" y="8" width="40" height="50" rx="2" fill={c1} /><path d="M12 24h40M12 40h40" />{[0, 1, 2, 3].map((i) => <rect key={i} x={16 + i * 8} y={12} width="6" height="12" fill={["#d9534f", "#5a95e8", "#f2c94c", "#4cb987"][i]} />)}{[0, 1, 2].map((i) => <rect key={i} x={16 + i * 10} y={28} width="8" height="12" fill={["#8f78c9", "#f4895a", "#45b8cc"][i]} />)}</g>);
      break;
    case "aquarium":
      art = (<g {...s}><rect x="8" y="16" width="48" height="34" rx="3" fill={c1} opacity=".55" /><path d="M8 24h48" /><ellipse cx="26" cy="35" rx="7" ry="4" fill="#f4895a" /><path d="M33 35l6-4v8z" fill="#f4895a" /><ellipse cx="44" cy="42" rx="4" ry="2.5" fill="#f2c94c" /><path d="M14 50c0-6 2-9 2-12M20 50c0-4 1-6 1-8" stroke="#4f8a62" /></g>);
      break;
    case "arcade":
      art = (<g {...s}><path d="M16 56V14l6-6h20l6 6v42z" fill={c1} /><rect x="21" y="16" width="22" height="16" rx="2" fill="#1b2238" /><path d="M26 24h12M32 20v8" stroke={item.color2 || "#fff"} /><circle cx="25" cy="42" r="3" fill={item.color2 || "#fff"} /><rect x="33" y="40" width="8" height="4" rx="2" fill={item.color2 || "#fff"} /></g>);
      break;
    case "trophy":
      art = (<g {...s}><rect x="10" y="10" width="44" height="46" rx="3" fill="#e9eef5" /><path d="M10 33h44" />{[0, 1].map((r) => [0, 1, 2].map((i) => <g key={`${r}${i}`}><path d={`M${17 + i * 14} ${r ? 51 : 28}h8M${21 + i * 14} ${r ? 51 : 28}v-6`} /><path d={`M${17 + i * 14} ${r ? 42 : 19}h8v5a4 4 0 0 1-8 0z`} fill={c1} /></g>))}</g>);
      break;
    case "window": {
      const night = id === "view_night" || id === "view_aurora";
      art = (<g {...s}><rect x="8" y="12" width="48" height="40" rx="3" fill={night ? c1 : "#cfe3f3"} />{id === "view_aurora" ? <path d="M8 36c10-16 18-8 26-18 6 6 14 4 22-2v22H8z" fill={c2} opacity=".6" stroke="none" /> : id === "view_beach" ? (<><rect x="8" y="34" width="48" height="18" fill={c2} stroke="none" /><rect x="8" y="26" width="48" height="8" fill={c1} stroke="none" /></>) : id === "view_forest" ? (<><path d="M14 52l8-20 8 20zM30 52l10-26 10 26z" fill={c1} stroke="none" /></>) : (<><rect x="14" y="30" width="8" height="22" fill={night ? "#3a4560" : c1} stroke="none" /><rect x="24" y="22" width="9" height="30" fill={night ? "#3a4560" : c2 === "#f4f1ea" ? "#9fb0d8" : c2} stroke="none" /><rect x="36" y="34" width="8" height="18" fill={night ? "#3a4560" : c1} stroke="none" />{night && <><circle cx="28" cy="28" r="1" fill="#f2c94c" stroke="none" /><circle cx="40" cy="40" r="1" fill="#f2c94c" stroke="none" /></>}</>)}<path d="M32 12v40M8 32h48" /></g>);
      break;
    }
    case "desk": case "chair": case "table": case "sofa": case "cabinet": case "divider": case "whiteboard": case "appliance": case "light":
      art = <FurnitureArt item={item} />;
      break;
    default:
      art = <circle cx="32" cy="32" r="14" fill={c1} {...s} />;
  }
  const character = item.kind === "character";
  return (
    <svg width={size} height={size} viewBox="0 0 64 64" role="img" aria-hidden focusable="false" data-testid={`shop-preview-${item.id}`}>
      <defs>
        <radialGradient id={`bg${uid}`} cx="50%" cy="38%" r="75%">
          <stop offset="0%" stopColor={mix(c1, "#ffffff", 0.55)} stopOpacity={character ? 0.5 : 0.4} />
          <stop offset="100%" stopColor={c1} stopOpacity={character ? 0.16 : 0.12} />
        </radialGradient>
      </defs>
      <rect x="1.5" y="1.5" width="61" height="61" rx="14" fill={`url(#bg${uid})`} />
      {item.rarity !== "common" && <rect x="1.5" y="1.5" width="61" height="61" rx="14" fill="none" stroke={glow} strokeOpacity={item.rarity === "epic" ? 0.8 : 0.5} strokeWidth={item.rarity === "epic" ? 1.6 : 1.1} />}
      {!character && mountOf(item) === "floor" && <ellipse cx="32" cy="57" rx="17" ry="2.6" fill="#1a2233" opacity=".14" />}
      {art}
      {item.rarity === "epic" && <g fill="#fff" opacity=".9"><path d="M54 9l1.2 3 3 1.2-3 1.2L54 17.4l-1.2-3-3-1.2 3-1.2z" /><path d="M10 14l.8 2 2 .8-2 .8-.8 2-.8-2-2-.8 2-.8z" opacity=".7" /></g>}
    </svg>
  );
}
