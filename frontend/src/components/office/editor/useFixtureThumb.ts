"use client";
// Pictures of the office's own rooms and original decor (they are not catalog items). They are drawn by the Plano
// drawing code whatever the style is (the only drawing that exists for them), and turned into pixels for Pixel. Cached per
// style; the previous picture stays while a new one is made, so changing the style never blinks.
import { useEffect, useRef, useState } from "react";
import { useOfficeStyle } from "@/lib/officeLayout";
import type { FixtureId, ZoneName } from "@/lib/officeLayoutModel";

type Key = { kind: "room"; id: ZoneName } | { kind: "fixture"; id: FixtureId };
const cache = new Map<string, string>();

export function useFixtureThumb(k: Key): { url: string | null; pixel: boolean } {
  const style = useOfficeStyle();
  const key = `${style}|${k.kind}|${k.id}`;
  const [, bump] = useState(0);
  const last = useRef<{ url: string; pixel: boolean } | null>(null);
  const hit = cache.get(key);
  useEffect(() => {
    if (cache.has(key)) return;
    let live = true;
    void (async () => {
      try {
        const f = await import("./flatThumb");
        const raw = k.kind === "room" ? await f.roomThumb(k.id) : await f.fixtureThumb(k.id);
        if (!raw) return;
        cache.set(key, style === "pixel" ? await f.toPixelUrl(raw) : raw);
        if (live) bump((n) => n + 1);
      } catch { /* no picture: the card keeps its plain look */ }
    })();
    return () => { live = false; };
  }, [key, style, k.kind, k.id]);
  if (hit) last.current = { url: hit, pixel: style === "pixel" };
  return { url: last.current?.url ?? null, pixel: last.current?.pixel ?? false };
}
