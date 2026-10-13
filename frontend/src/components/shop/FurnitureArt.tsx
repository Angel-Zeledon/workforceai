import type { ShopItem } from "@/lib/rewards";
import { applSpec, cabSpec, chairSpec, deskSpec, divSpec, lightSpec, sofaSpec, tableSpec, tone, wbSpec } from "../office/furnitureStyle";
import { darken, lighten } from "../office/itemStyle";

/**
 * Flat front/three-quarter illustrations of the office furniture, drawn from the same specs as the 3D models
 * (office/furnitureStyle.ts). Floor pieces stand on y = 55 of the 64x64 tile; the tile styling lives in ItemPreview.
 */
const INK = "#2c323b";
const st = { stroke: INK, strokeWidth: 1.5, strokeLinejoin: "round" as const, strokeLinecap: "round" as const };
const GLASSY = "#cfe6ee";
const BOOKS = ["#d9534f", "#5a95e8", "#f2c94c", "#4cb987", "#8f78c9", "#f4895a"];

function Desk({ item }: { item: ShopItem }) {
  const sp = deskSpec(item), top = tone(item, sp.top), leg = tone(item, sp.leg), glass = sp.topFin === "glass";
  const y = sp.h > 0.8 ? 18 : 25, th = Math.max(2.5, sp.thick * 60), by = 55;
  const legs = (() => {
    switch (sp.legs) {
      case "splay": return <g fill="none" stroke={leg} strokeWidth="3"><path d={`M11 ${y + th}L8 ${by}M53 ${y + th}L56 ${by}`} /></g>;
      case "panel": return <g fill={leg}><rect x="8" y={y + th} width="4" height={by - y - th} /><rect x="52" y={y + th} width="4" height={by - y - th} /></g>;
      case "sled": return <g fill="none" stroke={leg} strokeWidth="2.4"><path d={`M10 ${y + th}V${by}h-4M54 ${y + th}V${by}h4`} /></g>;
      case "hairpin": return <g fill="none" stroke={leg} strokeWidth="1.6"><path d={`M10 ${y + th}L7 ${by}M12 ${y + th}L15 ${by}M54 ${y + th}L57 ${by}M52 ${y + th}L49 ${by}`} /></g>;
      case "pipe": return <g fill="none" stroke={leg} strokeWidth="3"><path d={`M10 ${y + th}V${by}M54 ${y + th}V${by}M10 ${by - 8}H54`} /><circle cx="10" cy={by - 8} r="2.2" fill={leg} /><circle cx="54" cy={by - 8} r="2.2" fill={leg} /></g>;
      case "x": return <g fill="none" stroke={leg} strokeWidth="2.6"><path d={`M8 ${y + th}L16 ${by}M16 ${y + th}L8 ${by}M48 ${y + th}L56 ${by}M56 ${y + th}L48 ${by}`} /></g>;
      case "slab": return <g fill={leg}><rect x="8" y={y + th} width="6" height={by - y - th} /><rect x="50" y={y + th} width="6" height={by - y - th} /></g>;
      case "lift": return <g fill={leg}><rect x="12" y={y + th} width="5" height={by - y - th} /><rect x="47" y={y + th} width="5" height={by - y - th} /><rect x="6" y={by - 2} width="16" height="2.5" /><rect x="42" y={by - 2} width="16" height="2.5" /></g>;
      case "pedestal": return <g><rect x="36" y={y + th} width="20" height={by - y - th} fill={leg} /><path d={`M37 ${y + th + 8}h18M37 ${y + th + 15}h18`} /><path d={`M43 ${y + th + 4}h6M43 ${y + th + 11.5}h6M43 ${y + th + 19}h6`} stroke="#c9a24a" /><rect x="8" y={y + th} width="3" height={by - y - th} fill={leg} /></g>;
      default: return <g fill={leg}><rect x="8" y={y + th} width="3.4" height={by - y - th} /><rect x="52.6" y={y + th} width="3.4" height={by - y - th} /></g>;
    }
  })();
  return (
    <g {...st}>
      {sp.panel && !glass && <rect x="12" y={y + th} width="40" height="14" fill={darken(tone(item, sp.panel), 0.1)} />}
      {legs}
      {sp.wing && <path d={`M44 ${y - 5}h14l-3 5H41z`} fill={darken(top, 0.1)} />}
      {sp.glow && <rect x="8" y={y + th + 1} width="48" height="2" fill={tone(item, sp.rim ?? "c2")} stroke="none" />}
      <rect x="5" y={y} width="54" height={th} rx="1.5" fill={top} opacity={glass ? 0.6 : 1} />
      {sp.veins && <path d={`M14 ${y + 1}l7 2M30 ${y + 1}l9 1.5M46 ${y + 1}l6 2`} stroke={darken(top, 0.2)} strokeWidth="0.8" />}
      {sp.rim && !sp.glow && <rect x="5" y={y + th - 1} width="54" height="1.6" fill={tone(item, sp.rim)} stroke="none" />}
      <rect x="22" y={y - 9} width="20" height="9" rx="1.5" fill="#dfe6ef" />
      <path d={`M32 ${y - 1}v1.5`} />
    </g>
  );
}

function Chair({ item }: { item: ShopItem }) {
  const sp = chairSpec(item), seat = tone(item, sp.seat), back = tone(item, sp.back), base = tone(item, sp.base), trim = tone(item, sp.trim ?? "c2");
  switch (sp.kind) {
    case "task": case "exec": case "gaming": case "throne": {
      const tall = sp.kind === "exec" || sp.kind === "gaming" || sp.kind === "throne";
      const hb = sp.kind === "throne" ? 8 : tall ? 10 : 18;
      return (
        <g {...st}>
          <path d="M32 44v7M20 55l12-4 12 4" fill="none" stroke={base} strokeWidth="2.4" />
          <circle cx="20" cy="55" r="1.8" fill={INK} /><circle cx="44" cy="55" r="1.8" fill={INK} />
          <rect x="22" y={hb} width="20" height={tall ? 28 : 22} rx="5" fill={back} />
          {sp.mesh && <rect x="25" y={hb + 3} width="14" height="14" rx="3" fill={lighten(back, 0.25)} stroke="none" />}
          {sp.head && <rect x="26" y={hb - 6} width="12" height="6" rx="3" fill={sp.kind === "gaming" ? trim : back} />}
          {sp.kind === "gaming" && <rect x="29" y={hb + 4} width="6" height="22" rx="2" fill={trim} stroke="none" />}
          {sp.kind === "throne" && <path d={`M22 ${hb}l5-6 5 4 5-4 5 6z`} fill={back} />}
          <rect x="17" y="38" width="30" height="7" rx="3.5" fill={sp.kind === "throne" ? seat : darken(seat, 0.05)} />
          {sp.arms && <path d="M15 33h8M41 33h8" strokeWidth="3" />}
        </g>
      );
    }
    case "stool": return <g {...st}><ellipse cx="32" cy="24" rx="15" ry="4" fill={seat} /><path d="M20 27L15 55M44 27l5 28M26 28l-2 27M38 28l2 27" fill="none" stroke={base} strokeWidth="2.6" /><ellipse cx="32" cy="44" rx="13" ry="2.4" fill="none" /></g>;
    case "wood": return <g {...st}><path d="M20 10v45M44 10v45" fill="none" stroke={base} strokeWidth="3" />{[14, 21, 28].map((y) => <rect key={y} x="21" y={y} width="22" height="4.5" fill={back} />)}<rect x="17" y="36" width="30" height="5" rx="1.5" fill={seat} /></g>;
    case "ball": return <g {...st}><circle cx="32" cy="38" r="16" fill={seat} /><path d="M20 30q12 6 24 0" fill="none" stroke={lighten(seat, 0.4)} /><ellipse cx="32" cy="55" rx="14" ry="2.5" fill={base} /></g>;
    case "bean": return <g {...st}><path d="M10 52c0-14 4-24 14-28 6-4 14-2 18 4 8 4 12 14 12 24z" fill={seat} /><path d="M22 36q10 6 22 0" fill="none" stroke={darken(seat, 0.2)} /><ellipse cx="46" cy="25" rx="3" ry="2" fill="none" stroke={trim} /></g>;
    case "cube": return <g {...st}><rect x="14" y="26" width="36" height="28" rx="6" fill={seat} /><rect x="16" y="22" width="32" height="8" rx="4" fill={lighten(seat, 0.25)} /><path d="M14 50h36" stroke={trim} /></g>;
    case "kneel": return <g {...st}><path d="M18 55h32M26 34l-4 21M48 46l-6 9" fill="none" stroke={base} strokeWidth="2.4" /><rect x="16" y="30" width="24" height="6" rx="3" fill={seat} transform="rotate(-14 28 33)" /><rect x="40" y="40" width="12" height="7" rx="3" fill={seat} transform="rotate(20 46 43)" /></g>;
    case "velvet": return <g {...st}><path d="M18 56l2-10M46 56l-2-10" stroke={base} strokeWidth="2.2" /><rect x="16" y="14" width="32" height="26" rx="8" fill={seat} />{[[26, 22], [38, 22], [32, 29]].map(([x, y]) => <circle key={`${x}${y}`} cx={x} cy={y} r="1.1" fill={base} />)}<rect x="10" y="30" width="9" height="16" rx="4" fill={darken(seat, 0.1)} /><rect x="45" y="30" width="9" height="16" rx="4" fill={darken(seat, 0.1)} /><rect x="15" y="38" width="34" height="9" rx="4" fill={seat} /></g>;
    default: return <g {...st}><path d="M22 52l-3 4M42 52l3 4M26 52l-1 4M38 52l1 4" stroke={base} strokeWidth="2.2" fill="none" /><path d="M14 36a18 22 0 0 1 36 0q-18 6-36 0z" fill={seat} /><path d="M12 40h40l-4 12H16z" fill={seat} /></g>;
  }
}

function Table({ item }: { item: ShopItem }) {
  const sp = tableSpec(item), top = tone(item, sp.top), leg = tone(item, sp.leg), glass = sp.topFin === "glass";
  const y = Math.round(54 - sp.h * 30), tall = sp.h > 0.9;
  const legs = (() => {
    switch (sp.legs) {
      case "splay": case "hairpin": return <g fill="none" stroke={leg} strokeWidth={sp.legs === "splay" ? 3 : 1.6}><path d={`M16 ${y + 3}L11 55M48 ${y + 3}L53 55M24 ${y + 3}L22 55M40 ${y + 3}L42 55`} /></g>;
      case "pedestal": return <g fill={leg}><rect x="29" y={y + 3} width="6" height={52 - y} /><ellipse cx="32" cy="55" rx="13" ry="2.4" /></g>;
      case "trestle": return <g fill={leg}><rect x="12" y={y + 3} width="4" height={52 - y} /><rect x="48" y={y + 3} width="4" height={52 - y} /><rect x="8" y="53" width="12" height="2.5" /><rect x="44" y="53" width="12" height="2.5" /><rect x="14" y={y + 6} width="36" height="2.5" /></g>;
      case "x": return <g fill="none" stroke={leg} strokeWidth="2.4"><path d={`M13 ${y + 3}L21 55M21 ${y + 3}L13 55M43 ${y + 3}L51 55M51 ${y + 3}L43 55`} /></g>;
      case "cross": return <g fill="none" stroke={leg} strokeWidth="2.6"><path d={`M32 ${y + 3}V54M22 55h20`} /><ellipse cx="32" cy="46" rx="9" ry="2" strokeWidth="1.4" /></g>;
      default: return <g fill={leg}><rect x="12" y={y + 3} width="3" height={52 - y} /><rect x="49" y={y + 3} width="3" height={52 - y} /></g>;
    }
  })();
  return (
    <g {...st}>
      {legs}
      {sp.extra === "shelf" && <rect x="14" y="45" width="36" height="2.5" fill={darken(top, 0.1)} />}
      {sp.shape === "rect"
        ? <path d={`M8 ${y + 3}l5-6h38l5 6z`} fill={glass ? GLASSY : top} opacity={glass ? 0.75 : 1} />
        : sp.shape === "hex" ? <path d={`M14 ${y}l5-5h26l5 5-5 5H19z`} fill={top} /> : <ellipse cx="32" cy={y} rx={tall ? 15 : 20} ry="5" fill={top} />}
      {sp.shape === "rect" && <rect x="8" y={y + 3} width="48" height="3.5" fill={darken(glass ? GLASSY : top, 0.12)} opacity={glass ? 0.8 : 1} />}
      {sp.extra === "net" && <g><path d={`M32 ${y - 7}v10`} strokeWidth="1" /><rect x="29" y={y - 7} width="6" height="1.6" fill="#fff" /><path d={`M10 ${y + 1}h44`} stroke="#fff" strokeWidth="0.8" /></g>}
      {sp.veins && <path d={`M22 ${y - 1}l6 1.5M36 ${y - 1.5}l8 1`} stroke={darken(top, 0.2)} strokeWidth="0.8" />}
    </g>
  );
}

function Sofa({ item }: { item: ShopItem }) {
  const sp = sofaSpec(item), body = tone(item, sp.seat), cush = tone(item, sp.cush), fr = tone(item, sp.frame);
  switch (sp.kind) {
    case "pouf": return <g {...st}><path d="M14 30v20q18 7 36 0V30z" fill={body} /><ellipse cx="32" cy="30" rx="18" ry="6" fill={lighten(body, 0.2)} /><path d="M14 38q18 7 36 0" fill="none" stroke={darken(body, 0.25)} /></g>;
    case "bench": return <g {...st}><path d="M14 42v13M50 42v13M18 52h28" fill="none" stroke={fr} strokeWidth="2.4" /><rect x="8" y="34" width="48" height="8" rx="2" fill={body} /></g>;
    case "egg": return <g {...st}><ellipse cx="32" cy="55" rx="12" ry="2.4" fill={fr} /><path d="M30 44v11" stroke={fr} strokeWidth="3" /><path d="M14 36c0-18 8-27 18-27s18 9 18 27c0 8-6 12-18 12S14 44 14 36z" fill={body} /><ellipse cx="32" cy="38" rx="12" ry="6" fill={cush} /></g>;
    case "corner": return <g {...st}><rect x="6" y="22" width="52" height="22" rx="5" fill={darken(body, 0.05)} /><rect x="10" y="32" width="26" height="10" rx="3" fill={lighten(body, 0.1)} /><rect x="38" y="32" width="18" height="18" rx="3" fill={lighten(body, 0.1)} /><rect x="38" y="42" width="20" height="10" rx="3" fill={body} /><rect x="6" y="24" width="8" height="24" rx="3" fill={darken(body, 0.1)} /></g>;
    case "modular": return <g {...st}>{[0, 1, 2].map((i) => <g key={i}><rect x={6 + i * 17.5} y="22" width="16" height="12" rx="3" fill={i === 1 ? cush : body} /><rect x={6 + i * 17.5} y="32" width="16" height="16" rx="3" fill={lighten(i === 1 ? cush : body, 0.1)} /></g>)}</g>;
    default: {
      const n = sp.seats;
      return (
        <g {...st}>
          {sp.legs !== "none" && <path d="M12 50v5M52 50v5" stroke={fr} strokeWidth="2.4" />}
          <rect x={sp.kind === "armchair" ? 14 : 8} y="18" width={sp.kind === "armchair" ? 36 : 48} height="22" rx="6" fill={darken(body, 0.04)} />
          {sp.tuft && [20, 28, 36, 44].map((x) => [24, 32].map((y) => <circle key={`${x}${y}`} cx={x + (y === 32 ? 4 : 0) - 2} cy={y} r="1" fill={fr} stroke="none" />))}
          {sp.arms !== "none" && <><rect x={sp.kind === "armchair" ? 8 : 5} y="28" width="9" height="22" rx="4.5" fill={darken(body, 0.1)} /><rect x={sp.kind === "armchair" ? 47 : 50} y="28" width="9" height="22" rx="4.5" fill={darken(body, 0.1)} /></>}
          {Array.from({ length: n }).map((_, i) => { const x0 = sp.kind === "armchair" ? 17 : 15, wi = (sp.kind === "armchair" ? 30 : 34) / n; return <rect key={i} x={x0 + i * wi} y="36" width={wi - 1} height="12" rx="3" fill={lighten(body, 0.1)} />; })}
          {sp.fin !== "leather" && <rect x="16" y="30" width="9" height="9" rx="2" fill={cush} transform="rotate(-12 20 34)" />}
        </g>
      );
    }
  }
}

function Cabinet({ item }: { item: ShopItem }) {
  const sp = cabSpec(item), body = tone(item, sp.body), front = tone(item, sp.front), hd = tone(item, sp.handle);
  switch (sp.kind) {
    case "filing": return <g {...st}><rect x="18" y="8" width="28" height="47" rx="2" fill={body} />{[0, 1, 2, 3].map((i) => <g key={i}><rect x="20.5" y={10.5 + i * 11} width="23" height="9.5" rx="1" fill={lighten(front, 0.08)} /><path d={`M29 ${13 + i * 11}h6`} stroke={hd} /><rect x="29" y={16 + i * 11} width="6" height="2" fill="#fff" stroke="none" /></g>)}</g>;
    case "lowbook": case "tallbook": {
      const rows = sp.kind === "lowbook" ? 2 : 5, h = sp.kind === "lowbook" ? 26 : 46, y0 = 55 - h;
      return <g {...st}><rect x={sp.kind === "lowbook" ? 8 : 16} y={y0} width={sp.kind === "lowbook" ? 48 : 32} height={h} rx="1.5" fill={body} />{Array.from({ length: rows }).map((_, r) => { const rh = (h - 3) / rows, xa = sp.kind === "lowbook" ? 11 : 19, nb = sp.kind === "lowbook" ? 8 : 5; return <g key={r}><path d={`M${xa - 3} ${y0 + 1.5 + (r + 1) * rh}h${sp.kind === "lowbook" ? 48 : 32}`} />{Array.from({ length: nb }).map((__, b) => <rect key={b} x={xa + b * (sp.kind === "lowbook" ? 5.4 : 5.4)} y={y0 + 3 + (r + 1) * rh - rh * 0.7} width="4" height={rh * 0.7 - 1.5} fill={BOOKS[(b + r * 2) % 6]} strokeWidth="1" />)}</g>; })}</g>;
    }
    case "locker": return <g {...st}><rect x="8" y="8" width="48" height="47" rx="1.5" fill={darken(body, 0.15)} />{[0, 1, 2].map((i) => <g key={i}><rect x={10 + i * 15.3} y="10" width="14" height="42" rx="1" fill={front} /><path d={`M${13 + i * 15.3} 15h8M${13 + i * 15.3} 18h8M${13 + i * 15.3} 21h8`} strokeWidth="1" /><rect x={20 + i * 15.3} y="34" width="2" height="6" fill={STEELC} /></g>)}</g>;
    case "sideboard": case "credenza": return <g {...st}><path d="M12 42l-2 13M52 42l2 13" stroke={tone(item, "c2")} strokeWidth="2.6" /><rect x="6" y="22" width="52" height="22" rx="1.5" fill={body} />{Array.from({ length: sp.n }).map((_, i) => <g key={i}><rect x={8 + i * (48 / sp.n)} y="25" width={48 / sp.n - 1.5} height="17" fill={front} />{sp.kind === "credenza" ? <path d={`M${8 + i * (48 / sp.n) + 4}  30v6`} stroke={hd} /> : <circle cx={8 + i * (48 / sp.n) + (48 / sp.n) / 2} cy="30" r="1.1" fill={hd} />}</g>)}<rect x="4" y="19" width="56" height="4" rx="1.5" fill={lighten(front, 0.1)} /></g>;
    case "wardrobe": return <g {...st}><rect x="12" y="5" width="40" height="50" rx="1.5" fill={body} />{[0, 1].map((i) => <rect key={i} x={14 + i * 18} y="8" width="17" height="44" fill={lighten(body, 0.04)} />)}<path d="M29 28v8M35 28v8" stroke={hd} strokeWidth="2" /><rect x="10" y="3" width="44" height="4" rx="1" fill={body} /></g>;
    case "cubes": return <g {...st}><rect x="10" y="10" width="44" height="45" rx="1.5" fill={body} />{[0, 1, 2].map((r) => [0, 1, 2].map((c) => { const k = (r * 3 + c) % 4; return k === 3 ? <rect key={`${r}${c}`} x={12 + c * 14} y={12 + r * 14} width="12" height="12" fill={darken(body, 0.12)} strokeWidth="0.8" /> : <rect key={`${r}${c}`} x={13 + c * 14} y={15 + r * 14} width="10" height="9" rx="1.5" fill={k === 0 ? front : ["#f2b44a", "#6fb9a4"][k - 1]} strokeWidth="1" />; }))}</g>;
    case "vitrine": return <g {...st}><rect x="14" y="5" width="36" height="50" rx="1.5" fill={body} /><rect x="17" y="9" width="30" height="42" fill={GLASSY} opacity=".85" />{[19, 29, 39].map((y) => <path key={y} d={`M17 ${y}h30`} strokeWidth="1" />)}<path d="M24 17l1-5h4l1 5zM35 27a3 3 0 1 0 6 0z" fill={tone(item, "c2")} strokeWidth="1" /><rect x="38" y="31" width="4" height="7" fill="#d9534f" strokeWidth="1" /><rect x="22" y="40" width="3" height="9" fill="#5a95e8" strokeWidth="1" /><rect x="26" y="42" width="3" height="7" fill="#4cb987" strokeWidth="1" /></g>;
    default: return <g {...st}><circle cx="18" cy="55" r="2" fill={INK} /><circle cx="46" cy="55" r="2" fill={INK} /><rect x="14" y="22" width="36" height="31" rx="2" fill={body} />{[0, 1, 2].map((i) => <g key={i}><rect x="16" y={24 + i * 9.5} width="32" height="8" rx="1" fill={lighten(front, 0.1)} /><path d={`M28 ${28 + i * 9.5}h8`} stroke={STEELC} /></g>)}<rect x="12" y="19" width="40" height="4" rx="1.5" fill={lighten(body, 0.25)} /></g>;
  }
}
const STEELC = "#b3bac4";

function Divider({ item }: { item: ShopItem }) {
  const sp = divSpec(item), body = tone(item, sp.body), fr = tone(item, sp.frame);
  switch (sp.kind) {
    case "fabric": return <g {...st}><rect x="8" y="10" width="48" height="43" rx="3" fill={body} />{[24, 32, 40].map((x) => <path key={x} d={`M${x} 14v35`} stroke={darken(body, 0.25)} strokeWidth="1" />)}<path d="M12 55h10M42 55h10" stroke={fr} strokeWidth="2.4" /></g>;
    case "folding": return <g {...st}>{[0, 1, 2].map((i) => <g key={i}><path d={i % 2 ? `M${10 + i * 15} 12l14 -2v44l-14 2z` : `M${10 + i * 15} 10l14 2v44l-14 -2z`} fill={i % 2 ? darken(body, 0.08) : body} /><path d={`M${14 + i * 15} 30h6`} stroke={fr} strokeWidth="1" /></g>)}</g>;
    case "slats": return <g {...st}><rect x="8" y="8" width="48" height="4" fill={fr} />{Array.from({ length: 9 }).map((_, i) => <rect key={i} x={10 + i * 5} y="12" width="3.2" height="40" fill={body} strokeWidth="1" />)}<rect x="8" y="52" width="48" height="4" fill={fr} /></g>;
    case "cubes": return <g {...st}><rect x="6" y="14" width="52" height="41" rx="1.5" fill={body} />{[0, 1, 2].map((r) => [0, 1, 2, 3].map((c) => { const k = (r * 4 + c) % 5; return <g key={`${r}${c}`}><rect x={8 + c * 12.5} y={16 + r * 12.7} width="11" height="11" fill={darken(body, 0.1)} strokeWidth="0.8" />{k === 0 ? <circle cx={13.5 + c * 12.5} cy={22 + r * 12.7} r="3" fill="#5f9a62" strokeWidth="1" /> : k === 2 ? <rect x={10 + c * 12.5} y={20 + r * 12.7} width="7" height="7" fill={fr} strokeWidth="1" /> : null}</g>; }))}</g>;
    case "glass": return <g {...st}><rect x="8" y="8" width="48" height="45" rx="1.5" fill={GLASSY} opacity=".8" /><path d="M14 18l12-6M14 28l24-12" stroke="#fff" strokeWidth="1.2" /><rect x="8" y="8" width="48" height="45" rx="1.5" fill="none" stroke={fr} strokeWidth="2" /><path d="M12 55h10M42 55h10" stroke={fr} strokeWidth="2.4" /></g>;
    case "felt": return <g {...st}><rect x="8" y="8" width="48" height="44" rx="5" fill={body} />{[16, 22, 28, 34, 40, 46].map((x) => <path key={x} d={`M${x} 13v34`} stroke={darken(body, 0.25)} strokeWidth="1" />)}<path d="M16 52v4M48 52v4" stroke={fr} strokeWidth="2.4" /></g>;
    case "plants": return <g {...st}><path d="M12 14v28M32 14v28M52 14v28" stroke={fr} strokeWidth="1.4" /><path d="M10 24h44M10 33h44" stroke={fr} strokeWidth="1" />{[14, 32, 50].map((x) => <g key={x}><circle cx={x} cy="22" r="7" fill="#4f8a62" /><circle cx={x - 5} cy="30" r="5" fill="#5f9a62" /><circle cx={x + 5} cy="16" r="5" fill="#3f7a52" /></g>)}<rect x="6" y="42" width="52" height="12" rx="1.5" fill={body} /></g>;
    default: return <g {...st}><rect x="6" y="6" width="52" height="49" rx="2" fill="#2c323b" />{Array.from({ length: 25 }).map((_, i) => <circle key={i} cx={13 + (i % 5) * 9.5} cy={12 + Math.floor(i / 5) * 9.4} r="4.4" fill={["#3f7a52", "#4f8a62", "#5f9a62", "#6aa86a", "#2f6a46"][i % 5]} strokeWidth="0.8" />)}<circle cx="22" cy="30" r="1.8" fill="#ee7aa8" stroke="none" /><circle cx="43" cy="41" r="1.8" fill="#f2c94c" stroke="none" /></g>;
  }
}

function Whiteboard({ item }: { item: ShopItem }) {
  const sp = wbSpec(item), c1 = tone(item, "c1"), c2 = tone(item, "c2");
  const notes = ["#f2c94c", "#ee7aa8", "#7fc8f0", "#9ad87a"];
  switch (sp.kind) {
    case "white": return <g {...st}><rect x="6" y="14" width="52" height="34" rx="2" fill={c1} stroke={c2} strokeWidth="3" /><rect x="12" y="20" width="12" height="9" fill="none" stroke="#5a95e8" /><rect x="34" y="20" width="12" height="9" fill="none" stroke="#d9534f" /><path d="M24 24h10M12 38h26" /><rect x="16" y="48" width="32" height="3" fill={c2} /></g>;
    case "cork": return <g {...st}><rect x="9" y="14" width="46" height="34" rx="2" fill={c1} stroke={c2} strokeWidth="3" />{[[13, 18, 0], [28, 19, 1], [41, 18, 2], [16, 32, 3], [32, 33, 0], [44, 31, 1]].map(([x, y, k], i) => <g key={i}><rect x={x} y={y} width="10" height="9" fill={i % 2 ? "#fffaf0" : notes[k]} strokeWidth="1" /><circle cx={x + 5} cy={y + 1.5} r="1.1" fill="#d9534f" stroke="none" /></g>)}</g>;
    case "chalk": return <g {...st}><rect x="7" y="13" width="50" height="36" rx="2" fill={c1} stroke={c2} strokeWidth="3.5" /><path d="M14 22h18M14 29h26M14 36h14" stroke="#e9eef5" strokeWidth="1.4" /><circle cx="46" cy="25" r="5" fill="none" stroke="#e9eef5" strokeWidth="1.2" /><rect x="34" y="47" width="5" height="2.4" fill="#fff" strokeWidth="1" /></g>;
    case "kanban": return <g {...st}><rect x="5" y="12" width="54" height="38" rx="2" fill={c1} stroke={darken(c1, 0.4)} strokeWidth="2.5" /><path d="M23 16v30M41 16v30M7.5 20h49" strokeWidth="1" />{[[9, 23, 0], [9, 33, 1], [27, 23, 2], [45, 23, 3], [45, 33, 0], [27, 33, 1]].map(([x, y, k], i) => <rect key={i} x={x} y={y} width="9" height="8" fill={notes[k]} strokeWidth="1" />)}<path d="M9 17h9M27 17h9M45 17h9" stroke={INK} strokeWidth="1.4" /></g>;
    case "glass": return <g {...st}><rect x="6" y="13" width="52" height="36" rx="2" fill={c1} opacity=".75" />{[[9, 16], [55, 16], [9, 46], [55, 46]].map(([x, y]) => <circle key={`${x}${y}`} cx={x} cy={y} r="2" fill={c2} />)}<rect x="14" y="20" width="14" height="9" fill="none" stroke={c2} /><path d="M32 26h16M14 38h30" stroke="#ee4fa0" /><path d="M12 18l10-3" stroke="#fff" strokeWidth="1.4" /></g>;
    default: return <g {...st}><rect x="4" y="12" width="56" height="38" rx="3" fill={c1} /><rect x="8" y="16" width="48" height="29" rx="1" fill="#0f1a2a" stroke="none" />{[0.5, 0.8, 0.6, 1, 0.7].map((v, i) => <rect key={i} x={12 + i * 8} y={42 - v * 18} width="5" height={v * 18} fill={i === 3 ? c2 : "#5a95e8"} stroke="none" />)}<path d="M40 24l6-5 6 3" stroke={c2} strokeWidth="1.6" fill="none" /></g>;
  }
}

function Appliance({ item }: { item: ShopItem }) {
  const sp = applSpec(item), c1 = tone(item, "c1"), c2 = tone(item, "c2");
  switch (sp.kind) {
    case "printer": return <g {...st}><rect x="14" y="36" width="36" height="19" rx="1.5" fill="#cfd5dd" /><path d="M32 38v15" strokeWidth="1" /><rect x="10" y="18" width="44" height="20" rx="3" fill={c1} /><rect x="36" y="21" width="12" height="5" rx="1" fill="#1d2128" /><circle cx="14" cy="23" r="1.4" fill="#4cb987" stroke="none" /><path d="M16 38l4 5h24l4-5z" fill="#fff" /></g>;
    case "shredder": return <g {...st}><rect x="18" y="26" width="28" height="27" rx="3" fill={c1} /><rect x="16" y="16" width="32" height="11" rx="3" fill={c2} /><path d="M24 21h16" stroke="#0c0e12" strokeWidth="2" /><rect x="24" y="32" width="16" height="10" rx="1" fill={GLASSY} /><circle cx="22" cy="55" r="2" fill={INK} /><circle cx="42" cy="55" r="2" fill={INK} /></g>;
    case "coatrack": return <g {...st}><path d="M32 8v47M22 55h20" strokeWidth="2.4" stroke={c1} fill="none" /><path d="M32 12l-9 4M32 12l9 4M32 20l-8 3M32 20l8 3" stroke={c2} strokeWidth="2" fill="none" /><rect x="35" y="22" width="12" height="22" rx="3" fill="#4a5568" /><path d="M27 12a5 3 0 0 0 10 0z" fill="#c9a24a" /></g>;
    case "bin": return <g {...st}>{[0, 1].map((i) => <g key={i}><path d={`M${10 + i * 25} 22h20l-2 33H${12 + i * 25}z`} fill={i ? c2 : c1} /><rect x={8 + i * 25} y="18" width="24" height="6" rx="2" fill={darken(i ? c2 : c1, 0.2)} /><path d={`M${20 + i * 25} 31l5 8h-10z`} fill="none" stroke="#fff" strokeWidth="1.4" /></g>)}</g>;
    case "cooler": return <g {...st}><path d="M22 30h20v25H22z" fill={c1} /><rect x="26" y="34" width="12" height="8" rx="1" fill="#2c323b" /><circle cx="29" cy="38" r="1.4" fill="#5a95e8" stroke="none" /><circle cx="35" cy="38" r="1.4" fill="#d9534f" stroke="none" /><path d="M26 47h12" /><path d="M24 12q-2 8 0 18h16q2-10 0-18z" fill={c2} opacity=".7" /><path d="M24 8h16v4H24z" fill={c2} opacity=".5" /></g>;
    case "purifier": return <g {...st}><rect x="22" y="8" width="20" height="47" rx="6" fill={c1} /><path d="M26 14h12M26 20h12" stroke="#14181e" strokeWidth="1.6" /><path d="M26 32h12M26 36h12M26 40h12M26 44h12" stroke={darken(c1, 0.3)} strokeWidth="1" /><circle cx="32" cy="27" r="1.8" fill={c2} stroke="none" /></g>;
    case "fridge": return <g {...st}><rect x="16" y="6" width="32" height="49" rx="3" fill={c1} /><path d="M16 22h32" /><path d="M21 12v7M21 28v14" stroke={c2} strokeWidth="2.4" /><rect x="34" y="10" width="9" height="3" fill="#7fc8f0" stroke="none" /></g>;
    case "copier": return <g {...st}><rect x="8" y="26" width="48" height="29" rx="2" fill={c1} /><rect x="8" y="20" width="48" height="8" rx="2" fill={c2} /><rect x="28" y="14" width="12" height="7" rx="1.5" fill="#1d2128" transform="rotate(-8 34 17)" /><rect x="12" y="43" width="40" height="5" rx="1" fill={darken(c1, 0.08)} /><rect x="12" y="49" width="40" height="5" rx="1" fill={darken(c1, 0.08)} /></g>;
    case "safe": return <g {...st}><rect x="12" y="16" width="40" height="39" rx="3" fill={c1} /><rect x="16" y="20" width="32" height="31" rx="2" fill={lighten(c1, 0.12)} /><circle cx="32" cy="34" r="8" fill={c2} /><circle cx="32" cy="34" r="3" fill="#23272e" /><path d="M32 26v3M32 39v3M24 34h3M37 34h3" strokeWidth="1" /><rect x="42" y="42" width="6" height="2.5" fill={c2} /></g>;
    default: return <g {...st}><rect x="12" y="4" width="40" height="51" rx="3" fill={c1} /><rect x="15" y="10" width="22" height="34" fill="#14181e" />{[0, 1, 2, 3].map((r) => [0, 1, 2].map((c) => <rect key={`${r}${c}`} x={17.5 + c * 6.5} y={13 + r * 8} width="4.5" height="5.5" rx="1" fill={BOOKS[(r * 3 + c) % 6]} strokeWidth="0.8" />))}<rect x="40" y="11" width="9" height="18" rx="1" fill="#2c323b" />{[0, 1, 2].map((i) => <rect key={i} x="42" y={13 + i * 5} width="5" height="3" fill={c2} stroke="none" />)}<rect x="15" y="46" width="26" height="5" rx="1" fill="#1d2128" /><rect x="14" y="5.5" width="36" height="3" fill={c2} stroke="none" /></g>;
  }
}

function Light({ item }: { item: ShopItem }) {
  const sp = lightSpec(item), c1 = tone(item, "c1"), c2 = tone(item, "c2");
  const g = sp.glow ? tone(item, sp.glow.tone, "#ffd9a0") : "#ffd9a0";
  const halo = (x: number, y: number, r: number) => <circle cx={x} cy={y} r={r} fill={g} opacity=".28" stroke="none" />;
  switch (sp.kind) {
    case "sconce": return <g {...st}>{halo(32, 30, 16)}<circle cx="32" cy="42" r="5" fill={c1} /><path d="M32 40v-6" /><path d="M23 34l4-14h10l4 14z" fill={c1} /><circle cx="32" cy="30" r="3" fill={g} stroke="none" /></g>;
    case "cone": return <g {...st}><path d="M32 4v18" strokeWidth="1.2" />{halo(32, 38, 18)}<path d="M26 22h12l9 14H17z" fill={c1} /><ellipse cx="32" cy="36" rx="15" ry="2.6" fill={g} stroke="none" /></g>;
    case "table": return <g {...st}>{halo(32, 20, 15)}<path d="M23 28l4-14h10l4 14z" fill="#f4efe6" /><path d="M32 28v10" /><ellipse cx="32" cy="42" rx="14" ry="3" fill={c2} /><path d="M20 42l-3 13M44 42l3 13M32 45v10" stroke={c2} strokeWidth="2" fill="none" /></g>;
    case "string": return <g {...st}><path d="M6 12Q32 36 58 12" fill="none" strokeWidth="1.2" />{[14, 22, 30, 38, 46, 54].map((x, i) => { const y = 12 + Math.sin(((x - 6) / 52) * Math.PI) * 17; return <g key={x}><circle cx={x} cy={y + 4} r="6" fill={g} opacity=".25" stroke="none" /><circle cx={x} cy={y + 3.5} r="2.6" fill={i % 2 ? g : "#fff1d6"} /></g>; })}</g>;
    case "uplight": return <g {...st}>{halo(32, 12, 16)}<path d="M32 18v36M24 55h16" fill="none" strokeWidth="2" stroke={c2} /><path d="M22 10l3 10h14l3-10z" fill={c1} /><ellipse cx="32" cy="10" rx="10" ry="2.4" fill="#fff1d6" /></g>;
    case "globe": return <g {...st}><path d="M32 4v14" strokeWidth="1.2" /><rect x="29" y="16" width="6" height="5" fill={c2} />{halo(32, 34, 20)}<circle cx="32" cy="34" r="12" fill={c1} opacity=".92" /><circle cx="32" cy="34" r="5" fill={g} stroke="none" /></g>;
    case "neon": return <g {...st}><rect x="9" y="14" width="46" height="34" rx="9" fill={c1} opacity=".12" stroke="none" /><rect x="11" y="16" width="42" height="30" rx="8" fill="none" stroke={c1} strokeWidth="3.4" /><rect x="11" y="16" width="42" height="30" rx="8" fill="none" stroke="#fff" strokeWidth="1" opacity=".8" /><path d="M36 20l-9 12h8l-6 11" fill="none" stroke={lighten(c1, 0.5)} strokeWidth="3" /></g>;
    case "arc": return <g {...st}>{halo(47, 38, 14)}<rect x="8" y="52" width="16" height="4" fill="#e9e6e0" /><path d="M16 52V16Q16 6 34 6q14 2 15 14" fill="none" stroke={c2} strokeWidth="2.2" /><path d="M41 22h16l-4 10H45z" fill={c1} /><circle cx="49" cy="34" r="2.2" fill={g} stroke="none" /></g>;
    case "chandelier": return <g {...st}><path d="M32 3v12" strokeWidth="1.2" />{halo(32, 30, 22)}<path d="M18 28h28M24 20h16" stroke={c2} strokeWidth="2.4" fill="none" /><path d="M32 15v13" stroke={c2} strokeWidth="2.4" />{[16, 24, 32, 40, 48].map((x) => <g key={x}><rect x={x - 1.5} y="21" width="3" height="7" fill="#fffaf0" strokeWidth="1" /><ellipse cx={x} cy="19" rx="1.4" ry="2.4" fill="#ffe9a8" stroke="none" /></g>)}{[18, 30, 42].map((x) => <path key={x} d={`M${x} 30l2.5 4-2.5 4-2.5-4z`} fill="#d9efff" strokeWidth="1" />)}<path d="M32 30l3 5-3 5-3-5z" fill="#d9efff" strokeWidth="1" /></g>;
    default: return <g {...st}>{halo(32, 32, 24)}<circle cx="32" cy="32" r="16" fill={c1} /><circle cx="26" cy="27" r="3.2" fill={darken(c1, 0.18)} strokeWidth="1" /><circle cx="38" cy="36" r="4" fill={darken(c1, 0.18)} strokeWidth="1" /><circle cx="33" cy="40" r="1.8" fill={darken(c1, 0.18)} strokeWidth="1" /></g>;
  }
}

/** Preview art of one office furniture item (desk, chair, table, sofa, cabinet, divider, whiteboard, appliance, light). */
export function FurnitureArt({ item }: { item: ShopItem }) {
  switch (item.category) {
    case "desk": return <Desk item={item} />;
    case "chair": return <Chair item={item} />;
    case "table": return <Table item={item} />;
    case "sofa": return <Sofa item={item} />;
    case "cabinet": return <Cabinet item={item} />;
    case "divider": return <Divider item={item} />;
    case "whiteboard": return <Whiteboard item={item} />;
    case "appliance": return <Appliance item={item} />;
    case "light": return <Light item={item} />;
    default: return null;
  }
}
