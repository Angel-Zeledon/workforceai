"use client";
import type { ShopItem } from "@/lib/rewards";
import { ItemPreview } from "./ItemPreview";
import { useThumb } from "./ThumbStudio";

/**
 * An item shown as a lit 3D render (see ThumbStudio) with the flat drawing underneath until the render is ready, so the
 * card never jumps. Needs a <ThumbStudio/> mounted somewhere on the screen.
 */
export function ItemThumb({ item, size = 112 }: { item: ShopItem; size?: number }) {
  const url = useThumb(item);
  return (
    <div className="relative" style={{ width: size, height: size }}>
      <div className={`absolute inset-0 transition-opacity duration-300 ${url ? "opacity-0" : "opacity-100"}`}><ItemPreview item={item} size={size} /></div>
      {url && (
        // eslint-disable-next-line @next/next/no-img-element
        <img src={url} alt="" width={size} height={size} draggable={false} data-testid={`shop-thumb-${item.id}`}
          className="absolute inset-0 h-full w-full object-contain drop-shadow-[0_6px_8px_rgba(20,30,50,0.22)] [animation:thumb-in_.35s_ease-out]" />
      )}
    </div>
  );
}
