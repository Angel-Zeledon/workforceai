import type { ShopItem } from "@/lib/rewards";

/** Flat illustration of a shop item (no 3D canvas per card): the item's own colors on a soft tile. */
export function ItemPreview({ item, size = 72 }: { item: ShopItem; size?: number }) {
  const c1 = item.color || "#8f78c9";
  const c2 = item.color2 || "#f4f1ea";
  const dark = "#2c323b";
  const s = { stroke: dark, strokeWidth: 1.6, strokeLinejoin: "round" as const, strokeLinecap: "round" as const };
  let art: React.ReactNode = null;
  const id = item.id;
  switch (item.category) {
    case "outfit":
      art = (<g {...s}><path d="M20 22l12-5 12 5 8 6-6 8-5-3v22H23V33l-5 3-6-8z" fill={c1} /><path d="M32 17v11M27 20l5 8 5-8" fill="#fffaf0" /><circle cx="32" cy="38" r="1.4" fill={dark} /><circle cx="32" cy="45" r="1.4" fill={dark} /></g>);
      break;
    case "hat":
      if (id === "hat_cap") art = (<g {...s}><path d="M16 38a16 14 0 0 1 32 0z" fill={c1} /><path d="M40 38h14a4 3 0 0 1-4 4H36z" fill={c1} /></g>);
      else if (id === "hat_beanie") art = (<g {...s}><path d="M16 40a16 18 0 0 1 32 0z" fill={c1} /><rect x="14" y="38" width="36" height="7" rx="3" fill={c1} /><circle cx="32" cy="15" r="4" fill="#f4f1ea" /></g>);
      else if (id === "hat_party") art = (<g {...s}><path d="M32 12l14 32H18z" fill={c1} /><path d="M24 30h16M20 38h24" stroke={c2} /><circle cx="32" cy="12" r="3" fill={c2} /></g>);
      else if (id === "hat_chef") art = (<g {...s}><path d="M20 44V34a8 8 0 0 1 2-15 9 9 0 0 1 20 0 8 8 0 0 1 2 15v10z" fill={c1} /></g>);
      else if (id === "hat_graduate") art = (<g {...s}><path d="M32 20l24 10-24 10-24-10z" fill={c1} /><path d="M20 36v8c8 5 16 5 24 0v-8" fill={c1} /><path d="M54 31v10" stroke={c2} /></g>);
      else if (id === "hat_top") art = (<g {...s}><rect x="22" y="14" width="20" height="26" rx="2" fill={c1} /><rect x="22" y="32" width="20" height="5" fill={c2} /><rect x="14" y="40" width="36" height="5" rx="2.5" fill={c1} /></g>);
      else art = (<g {...s}><path d="M14 44V24l9 10 9-16 9 16 9-10v20z" fill={c1} /><circle cx="32" cy="24" r="2.6" fill={c2} /><circle cx="16" cy="22" r="2" fill={c2} /><circle cx="48" cy="22" r="2" fill={c2} /></g>);
      break;
    case "glasses":
      if (id === "glasses_star") art = (<g {...s}><path d="M20 24l3 6 7 1-5 5 1 7-6-3-6 3 1-7-5-5 7-1z" fill={c1} transform="translate(0 2)" /><path d="M44 24l3 6 7 1-5 5 1 7-6-3-6 3 1-7-5-5 7-1z" fill={c1} transform="translate(-6 2)" /></g>);
      else if (id === "glasses_visor") art = (<g {...s}><rect x="10" y="26" width="44" height="14" rx="7" fill={c1} opacity=".85" /><path d="M14 31h36" stroke="#fff" /></g>);
      else if (id === "glasses_heart") art = (<g {...s}><path d="M22 42c-9-6-12-10-12-14a6 6 0 0 1 12-1 6 6 0 0 1 12 1c0 4-3 8-12 14z" fill={c1} transform="translate(-1 -4) scale(.9)" /><path d="M22 42c-9-6-12-10-12-14a6 6 0 0 1 12-1 6 6 0 0 1 12 1c0 4-3 8-12 14z" fill={c1} transform="translate(30 -4) scale(.9)" /></g>);
      else art = (<g {...s}><rect x="8" y="26" width="22" height="14" rx="5" fill={c1} /><rect x="34" y="26" width="22" height="14" rx="5" fill={c1} /><path d="M30 31h4" /></g>);
      break;
    case "headphones":
      art = (<g {...s}><path d="M14 38V32a18 18 0 0 1 36 0v6" fill="none" strokeWidth="3" /><rect x="10" y="36" width="9" height="15" rx="4" fill={c1} /><rect x="45" y="36" width="9" height="15" rx="4" fill={c1} /></g>);
      break;
    case "scarf":
      art = (<g {...s}>{id === "bowtie_gold" ? (<><path d="M32 32L14 22v20zM32 32l18-10v20z" fill={c1} /><circle cx="32" cy="32" r="4" fill={c1} /></>) : (<><path d="M14 24c10 8 26 8 36 0v8c-10 8-26 8-36 0z" fill={c1} /><path d="M38 34l8 20h-9l-6-17z" fill={c1} />{item.color2 && <path d="M24 29v8M32 31v8M40 29v8" stroke={c2} strokeWidth="2.5" />}</>)}</g>);
      break;
    case "badge":
      art = (<g {...s}><circle cx="32" cy="28" r="14" fill={c1} />{id === "badge_heart" ? <path d="M32 36c-8-5-9-8-9-10a4 4 0 0 1 9-1 4 4 0 0 1 9 1c0 2-1 5-9 10z" fill="#fff" stroke="none" /> : id === "badge_rocket" ? <path d="M32 18c5 5 5 13 0 20-5-7-5-15 0-20z" fill="#fff" /> : <path d="M32 20l2.6 5.4 5.8.8-4.2 4 1 5.8-5.2-2.8-5.2 2.8 1-5.8-4.2-4 5.8-.8z" fill="#fff" stroke="none" />}{id === "badge_medal" && <path d="M24 42l-4 14 12-6 12 6-4-14" fill={item.color2 || c1} />}</g>);
      break;
    case "buddy":
      art = id === "buddy_duck" ? (<g {...s}><ellipse cx="30" cy="40" rx="16" ry="11" fill={c1} /><circle cx="42" cy="26" r="9" fill={c1} /><path d="M49 27h9l-8 5z" fill="#f4895a" /><circle cx="44" cy="24" r="1.4" fill={dark} /></g>)
        : id === "buddy_robot" ? (<g {...s}><rect x="18" y="22" width="28" height="24" rx="6" fill={c1} /><circle cx="26" cy="32" r="3.2" fill={c2} /><circle cx="38" cy="32" r="3.2" fill={c2} /><path d="M32 22v-7M28 15h8M26 40h12" /></g>)
        : (<g {...s}><ellipse cx="30" cy="38" rx="11" ry="15" fill={c1} /><circle cx="36" cy="20" r="8" fill={c1} /><path d="M42 20l9 3-9 4z" fill="#f2c94c" /><path d="M24 50l-6 8M32 52l-2 8" /><path d="M24 30c-6 4-7 10-5 16" fill="none" stroke={c2} strokeWidth="4" /></g>);
      break;
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
    default:
      art = <circle cx="32" cy="32" r="14" fill={c1} {...s} />;
  }
  return (
    <svg width={size} height={size} viewBox="0 0 64 64" role="img" aria-hidden focusable="false" data-testid={`shop-preview-${item.id}`}>
      <rect x="2" y="2" width="60" height="60" rx="14" fill={c1} opacity=".13" />
      {art}
    </svg>
  );
}
