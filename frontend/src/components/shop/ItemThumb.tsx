"use client";
import { useRef } from "react";
import type { ShopItem } from "@/lib/rewards";
import { useOfficeStyle } from "@/lib/officeLayout";
import { ItemPreview } from "./ItemPreview";
import { useThumb } from "./ThumbStudio";
import { pixelDisplay } from "../office/editor/thumbPost";

/**
 * An item shown as the office draws it in its current style (see ThumbStudio), with the flat drawing underneath until the
 * picture is ready so the card never jumps. When the style changes the previous picture stays until the new one is in
 * (no blink to the flat drawing). Pixel pictures are shown at a whole multiple of their size, never smoothed.
 * Needs a <ThumbStudio/> mounted somewhere on the screen.
 */
export function ItemThumb({ item, size = 112 }: { item: ShopItem; size?: number }) {
  const url = useThumb(item);
  const style = useOfficeStyle();
  const last = useRef<{ url: string; pixel: boolean } | null>(null);
  if (url) last.current = { url, pixel: style === "pixel" };
  else if (url === "") last.current = null; // this style has no picture of it: back to the flat drawing
  const shown = last.current; // keeps the previous style's picture while the new one is drawn
  const side = shown?.pixel ? pixelDisplay(size) : size;
  return (
    <div className="relative" style={{ width: size, height: size }}>
      <div className={`absolute inset-0 transition-opacity duration-300 ${shown ? "opacity-0" : "opacity-100"}`}><ItemPreview item={item} size={size} /></div>
      {shown && (
        // eslint-disable-next-line @next/next/no-img-element
        <img key={shown.url.length + shown.url.slice(-24)} src={shown.url} alt="" width={side} height={side} draggable={false} data-testid={`shop-thumb-${item.id}`}
          style={{ left: (size - side) / 2, top: (size - side) / 2, width: side, height: side, imageRendering: shown.pixel ? "pixelated" : "auto" }}
          className={`absolute object-contain [animation:thumb-in_.35s_ease-out] ${shown.pixel ? "" : "drop-shadow-[0_6px_8px_rgba(20,30,50,0.22)]"}`} />
      )}
    </div>
  );
}
