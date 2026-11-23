// Pure decisions of the "Estudio" office style (no THREE / React / app imports: unit tested with node, see
// studioLogic.test.mjs). The renderer feeds it what the shared simulation (flat/agentSim.ts) says an agent is doing and
// gets back which recorded clip must play, which hand-made gestures are blended on top, and the look of each person.

/** The recorded clips the people are built from (the glTF packs bring Idle, Walk, SitDown and StandUp). */
export type Base = "Idle" | "Walk" | "SitDown" | "Seated" | "StandUp";
/** Gestures blended ADDITIVELY over the base clip: they never replace it, so a seated person can type and a walking one stays walking. */
export type Add = "type" | "talk" | "reach" | "nod" | "shake" | "wave" | "look" | "stretch" | "sip";
export const ADD_CLIPS: Add[] = ["type", "talk", "reach", "nod", "shake", "wave", "look", "stretch", "sip"];

/** Seconds the sit-down and the stand-up clips take on screen (the recorded ones are a bit slower; they are sped up to fit). */
export const SIT_S = 0.95;
export const STAND_S = 0.8;

export interface BaseState { base: Base; t: number }
export interface BaseWant { walking: boolean; seated: boolean }

/**
 * One step of the clip state machine. The person only walks when standing: sitting down plays SitDown once and then
 * holds the seated pose; getting up plays StandUp once. `hold` is true while StandUp plays, and the renderer freezes the
 * simulation then (it does not slide along the floor while it is still getting up).
 */
export function nextBase(cur: BaseState, want: BaseWant, dt: number): { state: BaseState; hold: boolean } {
  let { base, t } = cur;
  t += dt;
  const change = (b: Base) => { base = b; t = 0; };
  switch (base) {
    case "Seated":
      if (!want.seated) change("StandUp");
      break;
    case "SitDown":
      if (!want.seated) change("StandUp");
      else if (t >= SIT_S) change("Seated");
      break;
    case "StandUp":
      if (t >= STAND_S) {
        if (want.seated && !want.walking) change("SitDown");
        else change(want.walking ? "Walk" : "Idle");
      }
      break;
    case "Idle":
      if (want.seated && !want.walking) change("SitDown");
      else if (want.walking) change("Walk");
      break;
    case "Walk":
      if (want.seated && !want.walking) change("SitDown");
      else if (!want.walking) change("Idle");
      break;
  }
  return { state: { base, t }, hold: base === "StandUp" };
}

/** Playback speed of the Walk clip for a walking speed given as a share of the agents' (1 = gestures.WALK_SPEED): proportional above 1 so faster walkers do not skate. */
export const walkTimeScale = (speed: number) => (speed >= 1 ? 1.2 * Math.min(2, speed) : 0.45 + 0.75 * Math.max(0, speed));

/** The clip state a person starts in. */
export const startBase = (seated: boolean): BaseState => ({ base: seated ? "Seated" : "Idle", t: 0 });

/** The additive gesture that stands for each event or idle gesture of the simulation (null = none that reads at this distance). */
export function gestureClip(kind: string | null): Add | null {
  switch (kind) {
    case "wave": return "wave";
    case "nod": return "nod";
    case "headShake": return "shake";
    case "lookAround": case "lookAt": case "neckRoll": case "phone": return "look";
    case "stretch": return "stretch";
    case "sip": return "sip";
    case "point": return "reach";
    default: return null;
  }
}

const TYPING = new Set(["working", "reviewing"]);

export interface AddInput {
  /** simulation mode: "walk", "seat:working", "seat:meeting", "stand:idle"... */
  mode: string;
  /** the agent's real state (working, thinking, ...) */
  state: string;
  talking: boolean;
  gesture: string | null;
  /** 0..1 envelope of the current gesture (0 outside it) */
  env: number;
  /** the base clip that is playing: nothing is added while sitting down or getting up */
  base: Base;
}

/** Target weight of every additive gesture (the renderer eases toward it). */
export function addWeights(i: AddInput): Record<Add, number> {
  const w = Object.fromEntries(ADD_CLIPS.map((a) => [a, 0])) as Record<Add, number>;
  if (i.base === "SitDown" || i.base === "StandUp") return w;
  const walking = i.mode === "walk";
  if (i.mode.startsWith("seat:") && i.mode !== "seat:meeting" && TYPING.has(i.state)) w.type = 1;
  if (i.talking && !walking) w.talk = 1;
  const g = gestureClip(i.gesture);
  if (g && !walking) w[g] = Math.max(w[g], i.env);
  return w;
}

/** Sober clothes (suits, knits, shirts): a person's pieces are picked by a hash of the agent id, so they never change. */
const SHIRTS = ["#f1f3f6", "#dfe6ee", "#c9d4e2", "#aebccd", "#e9e4da", "#f4f4f1"];
const JACKETS = ["#26303d", "#38434f", "#4b5563", "#2d3a4f", "#3b3f46", "#31404a"];
const KNITS = ["#6c7b8e", "#7d8a99", "#3f5f8a", "#5c6f66", "#8b8176", "#4f5d75", "#a3a8ae", "#0A66C2"];
const PANTS = ["#2c3542", "#3a3f48", "#4b4f57", "#2a2d33", "#46505e"];
const BELTS = ["#1d2127", "#2a211b"];

export function hashId(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619); }
  return h >>> 0;
}
const pick = <T>(list: readonly T[], h: number, salt: number): T => list[Math.floor(((Math.imul(h ^ (salt * 2654435761), 2246822519) >>> 0) / 4294967296) * list.length)];

export type Model = "suit" | "casual" | "classy";
export interface Outfit { model: Model; shirt: string; jacket: string; knit: string; pants: string; belt: string; /** relative height, 0.95..1.05 */ height: number }
/** Which glTF person and which sober clothes an agent wears. Men: the suit; women: one of the two women. */
export function outfitFor(id: string, female: boolean): Outfit {
  const h = hashId(id);
  return {
    model: female ? (h % 2 ? "casual" : "classy") : "suit",
    shirt: pick(SHIRTS, h, 1), jacket: pick(JACKETS, h, 2), knit: pick(KNITS, h, 3), pants: pick(PANTS, h, 4), belt: pick(BELTS, h, 5),
    height: 0.95 + (((h >>> 7) % 100) / 100) * 0.1,
  };
}

/** Floor texture recipe per floor kind: size of one tile in meters and the pattern the renderer draws on it. */
export const FLOOR_TILE: Record<string, { m: number; pattern: "grid" | "planks" | "checker" | "plain" | "veins" }> = {
  concrete: { m: 2, pattern: "grid" }, wood: { m: 2, pattern: "planks" }, parquet: { m: 2, pattern: "checker" },
  carpet: { m: 2, pattern: "plain" }, tile: { m: 1, pattern: "grid" }, marble: { m: 4, pattern: "veins" },
};
