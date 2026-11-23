// The "Plano" picture of an item: the SAME vector drawing the Plano office uses (office/flat/draw.ts), drawn once into a
// small offscreen PixiJS surface and exported as an image. One surface for the whole session; pictures come one at a time.
// The rooms and the original decor of the office (no catalog item behind them) are drawn here too, from the same code.
import type * as PIXI from "pixi.js";
import type { ShopItem } from "@/lib/rewards";
import { DEFAULT_LAYOUT, FIXTURES, ROOM_PARTS, fixtureWorld, restOn, zoneCenterOf, type FixtureId, type ZoneName } from "@/lib/officeLayoutModel";
import { drawChair, drawDesk, drawFixture, drawMat, drawMonitorBody, deskHeightOf, drawPiece } from "../flat/draw";

const SIDE = 256;
type Pix = typeof import("pixi.js");
let boot: Promise<{ P: Pix; app: PIXI.Application }> | null = null;

function surface() {
  if (!boot) {
    boot = (async () => {
      const P = await import("pixi.js");
      const app = new P.Application();
      await app.init({ width: SIDE, height: SIDE, backgroundAlpha: 0, antialias: true, resolution: 1, autoStart: false });
      return { P, app };
    })();
    boot.catch(() => { boot = null; });
  }
  return boot;
}

/** One picture at a time on the shared surface. */
let lock: Promise<unknown> = Promise.resolve();
const serial = <T,>(fn: () => Promise<T>): Promise<T> => { const r = lock.then(fn, fn); lock = r.catch(() => {}); return r; };

/** Whether the Plano drawing has something to show for this item (windows are part of the wall there: no picture). */
export const flatDraws = (item: ShopItem) => item.kind === "office" && item.category !== "window";

async function shoot(draw: (g: PIXI.Graphics, P: Pix) => void, fill = 0.8): Promise<string | null> {
  const { P, app } = await surface();
  const stage = app.stage;
  const g = new P.Graphics();
  draw(g, P);
  const holder = new P.Container();
  holder.addChild(g);
  stage.addChild(holder);
  try {
    const b = holder.getLocalBounds();
    if (b.width < 1 || b.height < 1) return null;
    const k = Math.min((SIDE * fill) / b.width, (SIDE * fill) / b.height, 3);
    holder.scale.set(k);
    holder.position.set(SIDE / 2 - (b.x + b.width / 2) * k, SIDE / 2 - (b.y + b.height / 2) * k);
    app.render();
    return await app.renderer.extract.base64({ target: stage, frame: new P.Rectangle(0, 0, SIDE, SIDE) });
  } finally {
    stage.removeChild(holder);
    holder.destroy({ children: true });
  }
}

/** The picture (PNG data URL) of a catalog item or null when it has no drawing in this style. */
export function flatThumb(item: ShopItem): Promise<string | null> {
  if (!flatDraws(item)) return Promise.resolve(null);
  return serial(() => shoot((g) => {
    if (item.category === "desk") { drawDesk(g, 0, 0, item, 0x0a66c2); drawMonitorBody(g, 0, 0, deskHeightOf(item)); }
    else if (item.category === "chair") drawChair(g, g, 0, 0, item);
    else drawPiece(g, item, { x: 0, z: 0, rot: 0, s: 1 });
  }));
}

const L = DEFAULT_LAYOUT;
function drawParts(g: PIXI.Graphics, ids: FixtureId[]) {
  const poses = ids.map((id) => ({ id, w: fixtureWorld(L, id), rest: FIXTURES[id].level === "top" ? restOn(L, id) : { on: null, y: 0 } })).filter((p) => p.w);
  poses.sort((a, b) => (a.rest.on ? 1 : 0) - (b.rest.on ? 1 : 0) || a.w!.x + a.w!.z - (b.w!.x + b.w!.z));
  for (const p of poses) drawFixture(g, p.id, p.w!.x, p.w!.z, p.w!.rot, p.rest.y);
}
/** A room of the office (its mat and everything built into it) as it is drawn in Plano. */
export const roomThumb = (zone: ZoneName) => serial(() => shoot((g) => { const [cx, cz] = zoneCenterOf(L, zone); drawMat(g, zone, cx, cz); drawParts(g, ROOM_PARTS[zone]); }, 0.86));
/** One built-in object (a seat, the coffee machine, a plant, the server rack). */
export const fixtureThumb = (id: FixtureId) => serial(() => shoot((g) => drawParts(g, [id]), 0.7));

/** Lets the surface go (the editor or the shop closed): frees its WebGL context. */
export function disposeFlatThumbs() {
  const b = boot; boot = null;
  void b?.then(({ app }) => app.destroy(true, { children: true })).catch(() => {});
}
