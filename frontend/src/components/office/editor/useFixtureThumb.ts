"use client";
// Pictures of the office's own rooms and original decor (they are not catalog items). They go through the same queue as
// the furniture (shop/ThumbStudio) and are drawn from the same pieces the office uses in the chosen style. The previous
// picture stays while a new one is made, so changing the style never blinks.
import { useRef } from "react";
import { useOfficeStyle } from "@/lib/officeLayout";
import { useFxThumb } from "../../shop/ThumbStudio";
import type { Fx } from "./RoomSubject";

export function useFixtureThumb(fx: Fx): { url: string | null; pixel: boolean } {
  const style = useOfficeStyle();
  const url = useFxThumb(fx);
  const last = useRef<{ url: string; pixel: boolean } | null>(null);
  if (url) last.current = { url, pixel: style === "pixel" };
  else if (url === "") last.current = null;
  return { url: last.current?.url ?? null, pixel: last.current?.pixel ?? false };
}
