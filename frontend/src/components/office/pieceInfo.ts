import type { ShopItem } from "@/lib/rewards";
import { footprint, lightOf, mountOf, type Mount } from "./furnitureStyle";

/** What the editor needs to know about an item to place it: its floor footprint, how it is mounted and whether it emits light. */
export interface PieceInfo {
  w: number;
  d: number;
  mount: Mount;
  /** Lies on the floor (rugs): people walk over it, so it collides with nothing and nothing collides with it. */
  flat: boolean;
  light: { color: string; intensity: number; distance: number } | null;
  /** Default height above the floor for wall / ceiling pieces. */
  y: number;
}

/** The categories that existed before the editor: their models live in OfficeItems.tsx, their sizes here. */
const LEGACY = new Set(["plant", "lamp", "rug", "coffee", "poster", "shelf", "aquarium", "arcade", "trophy", "window"]);
export const isLegacyCategory = (c: string) => LEGACY.has(c);

function legacy(item: ShopItem): Pick<PieceInfo, "w" | "d" | "flat"> {
  switch (item.category) {
    case "plant": return { w: item.id === "plant_palm" ? 0.9 : 0.6, d: item.id === "plant_palm" ? 0.9 : 0.6, flat: false };
    case "lamp": return { w: 0.6, d: 0.6, flat: false };
    case "rug": return { w: 6.4, d: 2.6, flat: true };
    case "coffee": return { w: 0.7, d: 0.7, flat: false };
    case "poster": return { w: 1.1, d: 0.45, flat: false };
    case "shelf": return { w: 2.0, d: 0.5, flat: false };
    case "aquarium": return item.id === "aquarium_large" ? { w: 2.3, d: 0.95, flat: false } : { w: 1.5, d: 0.7, flat: false };
    case "arcade": return item.id === "arcade_pinball" ? { w: 0.95, d: 1.6, flat: false } : { w: 0.95, d: 0.95, flat: false };
    case "trophy": return { w: 1.6, d: 0.55, flat: false };
    default: return { w: 1, d: 1, flat: false };
  }
}

export function pieceInfo(item: ShopItem): PieceInfo {
  if (isLegacyCategory(item.category)) return { ...legacy(item), mount: "floor", light: null, y: 0 };
  const f = footprint(item);
  const mount = mountOf(item);
  return { w: f.w, d: f.d, mount, flat: false, light: lightOf(item), y: mount === "wall" ? 1.5 : 0 };
}
