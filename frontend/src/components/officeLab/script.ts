// EXPERIMENTAL (spike /office-lab, desechable): no lo usa la oficina de producción.
// El guion único que reproducen los cuatro paneles. Puro: sin THREE / React / Pixi, cada panel lo dibuja a su manera.
// Plano en metros: x a la derecha, z hacia la cámara. yaw: el personaje mira hacia (sin yaw, cos yaw).

export type V2 = [number, number];
export type PhaseName =
  | "enter" | "sit" | "type" | "stand" | "walkMeet" | "talk" | "walkBoss" | "give" | "approve" | "celebrate" | "walkBack" | "sitBack";

export const LOOP = 27;

/** Muebles y sitios del plano (los mismos para todos los paneles). */
export const PLAN = {
  door: [-4.8, 0.4] as V2,
  desk: { pos: [-2.6, -1.6] as V2 }, // escritorio del agente (mira a +z)
  seat: [-2.6, -0.85] as V2, // silla del agente (se sienta mirando a -z)
  meeting: { pos: [1.2, 1.6] as V2, r: 0.62 }, // mesa redonda de reunión
  partner: [2.05, 1.6] as V2, // el compañero espera de pie aquí
  meetSpot: [0.35, 1.6] as V2,
  bossDesk: { pos: [3.2, -1.9] as V2 },
  boss: [3.2, -2.65] as V2, // la jefa, sentada, mira a +z
  bossSpot: [3.2, -1.0] as V2,
  room: { x0: -5.2, x1: 5.2, z0: -3.6, z1: 3.4 },
};

interface Phase { name: PhaseName; t0: number; t1: number; path?: V2[]; yaw?: number }

const P = PLAN;
export const PHASES: Phase[] = [
  { name: "enter", t0: 0, t1: 3.8, path: [P.door, [-1.7, 0.4], [-1.7, -0.85], P.seat] },
  { name: "sit", t0: 3.8, t1: 5.0, yaw: Math.PI },
  { name: "type", t0: 5.0, t1: 8.4, yaw: Math.PI },
  { name: "stand", t0: 8.4, t1: 9.2, yaw: Math.PI },
  { name: "walkMeet", t0: 9.2, t1: 12.0, path: [P.seat, [-1.7, -0.85], [-1.0, 1.6], P.meetSpot] },
  { name: "talk", t0: 12.0, t1: 15.2, yaw: Math.PI / 2 },
  { name: "walkBoss", t0: 15.2, t1: 17.6, path: [P.meetSpot, [1.0, 0.55], [3.2, -0.2], P.bossSpot] },
  { name: "give", t0: 17.6, t1: 18.8, yaw: Math.PI },
  { name: "approve", t0: 18.8, t1: 19.8, yaw: Math.PI },
  { name: "celebrate", t0: 19.8, t1: 21.6, yaw: Math.PI * 0.75 },
  { name: "walkBack", t0: 21.6, t1: 25.6, path: [P.bossSpot, [2.4, 0.1], [-1.7, 0.1], [-1.7, -0.85], P.seat] },
  { name: "sitBack", t0: 25.6, t1: LOOP, yaw: Math.PI },
];

export const STEPS = [
  { n: 1, label: "Llega y se sienta", from: 0 },
  { n: 2, label: "Trabaja en el ordenador", from: 5.0 },
  { n: 3, label: "Reunión con un compañero", from: 9.2 },
  { n: 4, label: "Entrega a la jefa", from: 15.2 },
  { n: 5, label: "Aprobado: celebra y vuelve", from: 18.8 },
];

export type Who = "agent" | "partner" | "boss";
export interface BubbleLine { who: Who; text: string; t0: number; t1: number }
export const LINES: BubbleLine[] = [
  { who: "agent", text: "El informe de ventas está listo.", t0: 12.1, t1: 13.7 },
  { who: "partner", text: "Bien. Llévaselo a la jefa.", t0: 13.7, t1: 15.2 },
  { who: "agent", text: "Le traigo el informe para aprobar.", t0: 17.7, t1: 18.9 },
  { who: "boss", text: "Aprobado. Buen trabajo.", t0: 18.9, t1: 20.6 },
];

export interface Frame {
  t: number;
  phase: PhaseName;
  /** 0..1 dentro de la fase */
  u: number;
  /** segundos dentro de la fase */
  local: number;
  step: number;
  pos: V2;
  yaw: number;
  walking: boolean;
  /** 0..1 fracción de la velocidad de crucero (arranca y frena) */
  speed: number;
  /** metros recorridos en la fase (para la fase del paso) */
  stride: number;
  /** 0 de pie .. 1 sentado */
  seated: number;
  /** intensidad del tecleo 0..1 (la pantalla brilla y cambia) */
  typing: number;
  /** pantalla encendida 0..1 */
  screen: number;
  folder: "table" | "agent" | "boss" | "none";
  approved: boolean;
  /** 0..1 dentro de la celebración (0 fuera) */
  celebrate: number;
  /** 0..1 el brazo extendido entregando la carpeta */
  reach: number;
  bubble: BubbleLine | null;
  partner: { mode: "idle" | "listen" | "talk" | "hand"; yaw: number };
  boss: { mode: "work" | "look" | "receive" | "approve" | "clap"; u: number };
}

const TAU = Math.PI * 2;
export const clamp01 = (x: number) => (x < 0 ? 0 : x > 1 ? 1 : x);
export const smooth = (x: number) => { const c = clamp01(x); return c * c * (3 - 2 * c); };
export const wrap = (a: number) => Math.atan2(Math.sin(a), Math.cos(a));
export const lerpAngle = (a: number, b: number, k: number) => a + wrap(b - a) * k;

function pathLen(path: V2[]) {
  let L = 0;
  for (let i = 1; i < path.length; i++) L += Math.hypot(path[i][0] - path[i - 1][0], path[i][1] - path[i - 1][1]);
  return L;
}
function along(path: V2[], s: number): V2 {
  let rest = Math.max(0, s);
  for (let i = 1; i < path.length; i++) {
    const a = path[i - 1], b = path[i];
    const l = Math.hypot(b[0] - a[0], b[1] - a[1]);
    if (rest <= l || i === path.length - 1) {
      const k = l > 0 ? Math.min(1, rest / l) : 1;
      return [a[0] + (b[0] - a[0]) * k, a[1] + (b[1] - a[1]) * k];
    }
    rest -= l;
  }
  return path[path.length - 1];
}
/** Perfil trapezoidal: acelera el 15% inicial, frena el 15% final. Devuelve [fracción recorrida, fracción de velocidad]. */
function profile(u: number): [number, number] {
  const r = 0.15, vmax = 1 / (1 - r);
  if (u < r) return [(vmax * u * u) / (2 * r), u / r];
  if (u > 1 - r) { const w = 1 - u; return [1 - (vmax * w * w) / (2 * r), w / r]; }
  return [vmax * (u - r / 2), 1];
}

function phaseAt(t: number): number {
  for (let i = 0; i < PHASES.length; i++) if (t < PHASES[i].t1) return i;
  return PHASES.length - 1;
}

/** El yaw con el que termina la fase i (para enlazar giros). */
function endYaw(i: number): number {
  const ph = PHASES[i];
  if (ph.path) {
    const p = ph.path, a = p[p.length - 2], b = p[p.length - 1];
    return Math.atan2(b[0] - a[0], b[1] - a[1]);
  }
  return ph.yaw ?? 0;
}

export function sample(tRaw: number): Frame {
  const t = ((tRaw % LOOP) + LOOP) % LOOP;
  const i = phaseAt(t);
  const ph = PHASES[i];
  const local = t - ph.t0, dur = ph.t1 - ph.t0, u = clamp01(local / dur);
  let pos: V2, yaw: number, walking = false, speed = 0, stride = 0;
  if (ph.path) {
    const L = pathLen(ph.path);
    const [f, v] = profile(u);
    const s = f * L;
    pos = along(ph.path, s);
    const ahead = along(ph.path, Math.min(L, s + 0.45));
    const back = along(ph.path, Math.max(0, s - 0.05));
    yaw = Math.hypot(ahead[0] - back[0], ahead[1] - back[1]) > 1e-3 ? Math.atan2(ahead[0] - back[0], ahead[1] - back[1]) : endYaw(i);
    // el primer cuarto de segundo gira desde donde miraba
    if (i > 0 && local < 0.35) yaw = lerpAngle(endYaw(i - 1), yaw, smooth(local / 0.35));
    walking = u < 1; speed = v; stride = s;
  } else {
    const prev = PHASES[i - 1];
    pos = prev?.path ? prev.path[prev.path.length - 1] : (i === 0 ? P.door : PLAN.seat);
    if (ph.name === "talk") pos = P.meetSpot;
    if (ph.name === "give" || ph.name === "approve" || ph.name === "celebrate") pos = P.bossSpot;
    if (ph.name === "sit" || ph.name === "type" || ph.name === "stand" || ph.name === "sitBack") pos = P.seat;
    const from = i > 0 ? endYaw(i - 1) : 0;
    yaw = lerpAngle(from, ph.yaw ?? from, smooth(local / 0.4));
  }
  // sentado
  let seated = 0;
  if (ph.name === "sit" || ph.name === "sitBack") seated = smooth((local - 0.3) / 0.8);
  else if (ph.name === "type") seated = 1;
  else if (ph.name === "stand") seated = 1 - smooth(local / 0.7);
  const typing = ph.name === "type" ? smooth(local / 0.4) * smooth((dur - local) / 0.3) : 0;
  const screen = ph.name === "type" ? 1 : ph.name === "sit" ? smooth((local - 0.6) / 0.5) : ph.name === "stand" ? 1 - smooth(local / 0.8) : ph.name === "sitBack" ? smooth((local - 0.8) / 0.5) : 0;
  let folder: Frame["folder"] = "table";
  if (t >= 15.0 && t < 18.5) folder = "agent";
  else if (t >= 18.5) folder = "boss";
  const reach = ph.name === "give" ? Math.sin(Math.PI * clamp01(local / 1.1)) : 0;
  const approved = t >= 19.1;
  const celebrate = ph.name === "celebrate" ? u : 0;
  const bubble = LINES.find((l) => t >= l.t0 && t < l.t1) ?? null;
  // el compañero mira hacia donde viene el agente
  const partnerYaw = -Math.PI / 2;
  let pMode: Frame["partner"]["mode"] = "idle";
  if (t >= 12.1 && t < 13.7) pMode = "listen";
  else if (t >= 13.7 && t < 14.7) pMode = "talk";
  else if (t >= 14.7 && t < 15.25) pMode = "hand";
  let bMode: Frame["boss"]["mode"] = "work", bu = 0;
  if (t >= 16.4 && t < 17.6) { bMode = "look"; bu = (t - 16.4) / 1.2; }
  else if (t >= 17.6 && t < 18.8) { bMode = "receive"; bu = (t - 17.6) / 1.2; }
  else if (t >= 18.8 && t < 19.8) { bMode = "approve"; bu = (t - 18.8) / 1.0; }
  else if (t >= 19.8 && t < 21.4) { bMode = "clap"; bu = (t - 19.8) / 1.6; }
  let step = 1;
  for (const s of STEPS) if (t >= s.from) step = s.n;
  return {
    t, phase: ph.name, u, local, step, pos, yaw: wrap(yaw), walking, speed, stride, seated, typing, screen, folder, approved, celebrate, reach, bubble,
    partner: { mode: pMode, yaw: partnerYaw }, boss: { mode: bMode, u: bu },
  };
}

export { TAU };
