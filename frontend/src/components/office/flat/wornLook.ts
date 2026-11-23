// What an agent wears in the "Plano" office: the same shop items and the same shape specs as the 3D character
// (Worn.tsx reads itemStyle.ts too), reduced to numbers the vector figure can draw. Pure: no Pixi, no React.
import type { ShopItem } from "@/lib/rewards";
import {
  badgeSpec, cansSpec, darken, glassesSpec, hatShape, lighten, luma, mix, outfitSpec, scarfSpec,
  type BadgeFrame, type Cans, type Crown, type Deco, type Frame, type OutfitStyle, type ScarfPattern,
} from "../itemStyle";
import { hexToNum } from "./isoMath";

/** One head radius of the 3D character is 0.19 m and the flat head is 8.5 px: meters of a hat spec -> figure pixels. */
const M = 8.5 / 0.19;

export interface WornLook {
  outfit?: { body: number; pants: number; shirt: number; trim: number; style: OutfitStyle; stripe: boolean };
  hat?: { crown: Crown; h: number; r: number; brim: number; front: number; c1: number; c2: number; band: boolean; deco: Deco[]; tilt: number };
  glasses?: { frame: Frame; color: number; tint: number };
  cans?: { kind: Cans; color: number };
  neck?: { c1: number; c2: number; pattern: ScarfPattern; bow: boolean; fringe: boolean };
  badge?: { color: number; color2: number; frame: BadgeFrame };
  buddy?: { kind: "duck" | "robot" | "parrot"; c1: number; c2: number };
}

/** The worn items of one agent from its equipped slots; undefined when it wears nothing (the figure keeps its own colors). */
export function wornLookOf(slots: Record<string, string> | undefined, catalog: readonly ShopItem[], fallbackColor: string): WornLook | undefined {
  if (!slots) return undefined;
  const item = (slot: string) => (slots[slot] ? catalog.find((i) => i.id === slots[slot]) : undefined);
  const out: WornLook = {};
  const outfit = item("outfit");
  if (outfit) {
    const c = outfit.color || fallbackColor;
    const s = outfitSpec(outfit.id, c, outfit.color2, outfit.rarity);
    out.outfit = { body: hexToNum(c), pants: hexToNum(s.pants), shirt: hexToNum(s.shirt), trim: hexToNum(s.trim), style: s.style, stripe: s.stripe };
  }
  const hat = item("hat");
  if (hat) {
    const s = hatShape(hat.id);
    const base = hat.color || "#d9534f";
    const alt = hat.color2 || (luma(base) > 0.6 ? darken(base, 0.45) : lighten(base, 0.5));
    out.hat = {
      crown: s.crown, h: s.h * M, r: (s.r ?? 0.19) * M, brim: (s.brim ?? 0) * M, front: (s.front ?? 0) * M,
      c1: hexToNum(s.swap ? alt : base), c2: hexToNum(s.swap ? base : alt), band: !!s.band, deco: s.deco ?? [], tilt: hat.id === "hat_beret" || hat.id === "hat_blueberet" ? 0.2 : 0,
    };
  }
  const face = item("face");
  if (face) { const g = glassesSpec(face.id); out.glasses = { frame: g.frame, color: hexToNum(face.color || "#1f2a37"), tint: g.tint }; }
  const ears = item("ears");
  if (ears) out.cans = { kind: cansSpec(ears.id).kind, color: hexToNum(ears.color || "#374151") };
  const neck = item("neck");
  if (neck) {
    const s = scarfSpec(neck.id, !!neck.color2);
    const c1 = neck.color || "#c0392b";
    out.neck = { c1: hexToNum(c1), c2: hexToNum(neck.color2 || mix(c1, "#ffffff", 0.45)), pattern: s.pattern, bow: !!s.bow, fringe: s.fringe };
  }
  const badge = item("badge");
  if (badge) out.badge = { color: hexToNum(badge.color || "#d4a017"), color2: hexToNum(badge.color2 || "#fff4c2"), frame: badgeSpec(badge.id).frame };
  const buddy = item("buddy");
  if (buddy) out.buddy = { kind: buddy.id.startsWith("buddy_duck") ? "duck" : buddy.id.startsWith("buddy_robot") ? "robot" : "parrot", c1: hexToNum(buddy.color || "#f7d046"), c2: hexToNum(buddy.color2 || "#f4895a") };
  return Object.keys(out).length ? out : undefined;
}

/** A role accessory that a worn item replaces (two pairs of glasses never overlap): same rule as Worn.suppressesAccessory. */
export function accessoryAfterWorn(accessory: string, worn: WornLook | undefined): string {
  if (!worn) return accessory;
  if (worn.glasses && (accessory === "glasses" || accessory === "roundglasses")) return "";
  if (worn.cans && (accessory === "headphones" || accessory === "headset")) return "";
  if (worn.hat && (accessory === "hardhat" || accessory === "beanie")) return "";
  return accessory;
}
