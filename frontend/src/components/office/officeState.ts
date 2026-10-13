// Pure decisions of the "office reflects the real state" layer: where an agent should be, which speech bubble
// it shows, the progress/ETA of its task, its budget "energy" and the folder hand-offs between desks.
// No THREE / React / store imports (so it runs under `node --test`, see officeState.test.mjs): the callers pass plain data.
// Nothing here invents content: every input comes from store events (tasks, approvals, budget, plans, projects).

export type Zone = "desk" | "approvals" | "cafeteria" | "meeting";

/** Max agents standing / sitting per zone; beyond it the rest stay at their desks (the room is small). */
export const ZONE_CAP = { approvals: 5, cafeteria: 6, meeting: 6 } as const;

// ---------------------------------------------------------------------------------------------
// Where to walk
// ---------------------------------------------------------------------------------------------

export interface ZoneInput {
  state: string;
  /** the agent's work is paused by a budget cap (request / agent budget exceeded, request paused) */
  budgetPaused: boolean;
  /** the agent takes part in a project / request being planned (planner running or plan review gate) */
  planning: boolean;
  /** the agent has a pending tool/action approval of its own */
  approvalPending: boolean;
}

/**
 * Priority: budget pause > planning meeting > approvals desk > own desk. A budget pause also wins over `blocked`
 * (the backend marks a budget-paused agent as blocked, but it is waiting for money, not failing). Agents that
 * just finished, failed or are in a conversation keep their normal behavior.
 */
export function zoneFor(i: ZoneInput): Zone {
  if (i.state === "completed" || i.state === "error" || i.state === "talking") return "desk";
  if (i.budgetPaused) return "cafeteria";
  if (i.planning && i.state !== "blocked" && i.state !== "awaiting_approval") return "meeting";
  if (i.approvalPending && i.state === "awaiting_approval") return "approvals";
  return "desk";
}

/** Stable slot per id inside a zone (-1 when the zone is full). `ids` must already be in a stable order. */
export function slotIndex(ids: readonly string[], id: string, cap: number): number {
  const k = ids.indexOf(id);
  return k >= cap ? -1 : k;
}

export const CAFE_CENTER: [number, number] = [9.2, 1.2];
/** Counter along the east edge (local x = +1.75); 4 spots face it, 2 more flank the small high table. */
const CAFE_SPOTS: { dx: number; dz: number; yaw: number }[] = [
  { dx: 0.95, dz: -1.2, yaw: Math.PI / 2 }, { dx: 0.95, dz: -0.4, yaw: Math.PI / 2 }, { dx: 0.95, dz: 0.4, yaw: Math.PI / 2 }, { dx: 0.95, dz: 1.2, yaw: Math.PI / 2 },
  { dx: -0.6, dz: -0.95, yaw: 0 }, { dx: -0.6, dz: 0.95, yaw: Math.PI },
];
export function cafeSpot(k: number, center: readonly [number, number] = CAFE_CENTER): { pos: [number, number]; yaw: number } {
  const o = CAFE_SPOTS[Math.abs(k) % CAFE_SPOTS.length];
  return { pos: [center[0] + o.dx, center[1] + o.dz], yaw: o.yaw };
}

const APPR_OFFSETS = [0, 0.9, -0.9, 1.8, -1.8];
/** Standing spot in front of the approvals desk (the CEO desk) for the k-th waiting agent. user = USER_POS. */
export function approvalsSpot(user: readonly [number, number, number], k: number): [number, number] {
  return [user[0] + APPR_OFFSETS[Math.abs(k) % APPR_OFFSETS.length], user[2] - 0.95];
}
/** Seat around the meeting table (same angles as the chairs in OfficeEnv.MeetingRoom) and the yaw toward the table. */
export function meetingSeat(center: readonly [number, number], k: number, radius = 2.25): { pos: [number, number]; yaw: number } {
  const a = (k / 6) * Math.PI * 2 + 0.3;
  const x = center[0] + Math.sin(a) * radius, z = center[1] + Math.cos(a) * radius;
  return { pos: [x, z], yaw: Math.atan2(center[0] - x, center[1] - z) };
}

// ---------------------------------------------------------------------------------------------
// Speech bubbles
// ---------------------------------------------------------------------------------------------

/** Single line, collapsed whitespace, cut at a word boundary with an ellipsis. */
export function truncateText(s: string, max = 64): string {
  const line = (s || "").split("\n").map((x) => x.trim()).find((x) => x.length > 0) ?? "";
  const one = line.replace(/\s+/g, " ");
  if (one.length <= max) return one;
  const cut = one.slice(0, max - 1);
  const sp = cut.lastIndexOf(" ");
  return (sp > max * 0.55 ? cut.slice(0, sp) : cut).replace(/[\s,.;:]+$/, "") + "…";
}

export type BubbleKind = "phase" | "consult" | "chat" | "task" | "step" | "file";
/** `urgent`: a change of situation (e.g. the agent just got paused) replaces a visible bubble immediately. */
export interface Bubble { key: string; kind: BubbleKind; text: string; urgent?: boolean }

export interface BubbleInput {
  state: string;
  /** current step as reported by the agent (agent.activity) */
  activity: string;
  /** title of the agent's current task, if any */
  taskTitle: string;
  /** a phase title of the plan being drafted (agent sits in the meeting room) */
  phaseTitle: string | null;
  /** latest message this agent sent (chat or consult question) and its age in ms */
  line: { text: string; kind: string; ageMs: number } | null;
  /** last drive file the agent touched (file.activity) and its age in ms */
  file?: { action: string; path: string; ageMs: number } | null;
}

export const LINE_MAX_AGE = 20000;
export const FILE_MAX_AGE = 15000;
const FILE_ICON: Record<string, string> = { write: "✍️", read: "📖", list: "📂", search: "🔎", move: "📦", delete: "🗑️" };

/** "✍️ propuesta.md": what the agent is doing with which file (name only, never content). */
export function fileBubbleText(action: string, path: string): string {
  const name = path.split("/").filter(Boolean).pop() || path || "/";
  return `${FILE_ICON[action] ?? "📄"} ${truncateText(name, 48)}`;
}

/**
 * Real content only, in priority order: phase being planned > consult question / chat line (recent) >
 * "task title - step" while working > the step alone. Returns null when there is nothing real to say.
 */
export function pickBubble(i: BubbleInput): Bubble | null {
  if (i.phaseTitle) return { key: `phase:${i.phaseTitle}`, kind: "phase", text: truncateText(i.phaseTitle, 60) };
  if (i.line && i.line.ageMs <= LINE_MAX_AGE && i.line.text.trim()) {
    const kind: BubbleKind = i.line.kind === "consult" ? "consult" : "chat";
    return { key: `${kind}:${i.line.text}`, kind, text: truncateText(i.line.text, 70) };
  }
  if (i.file && i.file.ageMs <= FILE_MAX_AGE && i.file.path) {
    return { key: `file:${i.file.action}:${i.file.path}`, kind: "file", text: fileBubbleText(i.file.action, i.file.path) };
  }
  const busy = i.state === "working" || i.state === "thinking" || i.state === "reviewing";
  if (busy && i.taskTitle) {
    return { key: `task:${i.taskTitle}|${i.activity}`, kind: "task", text: truncateText(i.activity ? `${i.taskTitle} · ${i.activity}` : i.taskTitle, 70) };
  }
  if (busy && i.activity) return { key: `step:${i.activity}`, kind: "step", text: truncateText(i.activity, 60) };
  return null;
}

export const BUBBLE_SHOW_MS = 6000;
export const BUBBLE_GAP_MS = 8000;
export const BUBBLE_MAX_VISIBLE = 3;

export interface BubbleSlot { key: string; text: string; kind: BubbleKind; shownAt: number; until: number; nextAt: number }
export type BubbleState = Map<string, BubbleSlot>;

/**
 * Rate limiter: an agent shows one bubble at a time for BUBBLE_SHOW_MS, then stays quiet for BUBBLE_GAP_MS; a bubble is
 * never repeated while its content is unchanged and at most BUBBLE_MAX_VISIBLE agents talk at once.
 * `cands` is in priority order (first wins when the cap is reached). Returns the ids currently visible.
 */
export function stepBubbles(state: BubbleState, cands: readonly { id: string; bubble: Bubble | null }[], now: number): string[] {
  let visible = 0;
  for (const s of state.values()) if (now < s.until) visible++;
  const out: string[] = [];
  const seen = new Set<string>();
  for (const c of cands) {
    seen.add(c.id);
    const cur = state.get(c.id);
    if (cur && now < cur.until) {
      if (!c.bubble) { state.delete(c.id); visible--; continue; } // nothing real to say anymore: the stale bubble goes away
      if (c.bubble.urgent && c.bubble.key !== cur.key) { cur.key = c.bubble.key; cur.text = c.bubble.text; cur.kind = c.bubble.kind; cur.shownAt = now; cur.until = now + BUBBLE_SHOW_MS; cur.nextAt = cur.until + BUBBLE_GAP_MS; }
      out.push(c.id); continue;
    }
    if (!c.bubble) { if (cur && now >= cur.nextAt) state.delete(c.id); continue; }
    if (cur && now < cur.nextAt) continue;
    if (cur && cur.key === c.bubble.key) continue; // same content already said
    if (visible >= BUBBLE_MAX_VISIBLE) continue;
    state.set(c.id, { key: c.bubble.key, text: c.bubble.text, kind: c.bubble.kind, shownAt: now, until: now + BUBBLE_SHOW_MS, nextAt: now + BUBBLE_SHOW_MS + BUBBLE_GAP_MS });
    visible++; out.push(c.id);
  }
  // forget agents that disappeared
  for (const id of Array.from(state.keys())) if (!seen.has(id) && now >= (state.get(id)?.nextAt ?? 0)) state.delete(id);
  return out;
}

/** Which phase title the k-th planning agent says; rotates every `cycle` so each phase is read in turn. */
export function phaseFor(phases: readonly string[], k: number, cycle: number): string | null {
  if (!phases.length) return null;
  return phases[(((k + cycle) % phases.length) + phases.length) % phases.length];
}

// ---------------------------------------------------------------------------------------------
// Progress, ETA and budget energy
// ---------------------------------------------------------------------------------------------

export interface EtaInput {
  /** agent.progress (0-100, reported by the agent) */
  progress: number;
  /** task.started_at in ms (null when not started) */
  startedMs: number | null;
  now: number;
  /** estimated duration of the task (project node est_seconds) when known */
  estSeconds?: number | null;
  /** observed/estimated factor of the project (health.schedule.calibration_factor) */
  calibration?: number | null;
}
export interface EtaInfo { pct: number; elapsedS: number; etaS: number | null }

/**
 * Progress and remaining time of one running task, from real data only: the agent's reported progress when it
 * has any (ETA extrapolated from the elapsed time) or the project's estimate versus the elapsed time.
 * Returns null when nothing can be said (not started, or neither progress nor estimate).
 */
export function taskEta(i: EtaInput): EtaInfo | null {
  if (i.startedMs === null || !(i.startedMs > 0)) return null;
  const elapsedS = Math.max(0, (i.now - i.startedMs) / 1000);
  const est = i.estSeconds && i.estSeconds > 0 ? i.estSeconds * (i.calibration && i.calibration > 0 ? i.calibration : 1) : 0;
  const p = Math.max(0, Math.min(100, i.progress || 0));
  if (p <= 0 && !est) return null;
  let pct = p;
  let etaS: number | null = null;
  if (p >= 8 && p < 100) etaS = (elapsedS * (100 - p)) / p;
  else if (est) { etaS = Math.max(0, est - elapsedS); if (p <= 0) pct = Math.min(95, (elapsedS / est) * 100); }
  return { pct, elapsedS, etaS };
}

/** "45s", "3m", "1h20m": language-neutral compact duration. */
export function etaShort(sec: number): string {
  const s = Math.max(0, Math.round(sec));
  if (s < 60) return `${Math.max(1, s)}s`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60), r = m % 60;
  return r ? `${h}h${r}m` : `${h}h`;
}

export interface Energy { remaining: number; fraction: number; level: "ok" | "low" | "empty" }
/** Remaining budget of an agent as an "energy" level; null when the agent has no cap (nothing to show). */
export function energyOf(capUsd: number, spentUsd: number, reservedUsd = 0): Energy | null {
  if (!(capUsd > 0)) return null;
  const remaining = Math.max(0, capUsd - spentUsd - reservedUsd);
  const fraction = Math.max(0, Math.min(1, remaining / capUsd));
  return { remaining, fraction, level: fraction <= 0.001 ? "empty" : fraction < 0.25 ? "low" : "ok" };
}

// ---------------------------------------------------------------------------------------------
// Folder hand-offs
// ---------------------------------------------------------------------------------------------

export interface HandoffTask { id: string; status: string; agent_id: string; depends_on: string[] }
export interface Handoff { from: string; to: string }
export const HANDOFF_BATCH_MAX = 12;

/**
 * Hand-offs produced by a store update: a task that just became "done" delivers its output to every dependent
 * task (pending or running) of ANOTHER agent. `prev` is the status map of the previous update (null on the first
 * one: loading history must not replay old hand-offs). Updates `prev` in place.
 */
export function detectHandoffs(prev: Map<string, string> | null, tasks: Record<string, HandoffTask>): { next: Map<string, string>; handoffs: Handoff[] } {
  const next = new Map<string, string>();
  const handoffs: Handoff[] = [];
  const all = Object.values(tasks);
  for (const t of all) next.set(t.id, t.status);
  if (!prev) return { next, handoffs };
  const seen = new Set<string>();
  for (const t of all) {
    if (t.status !== "done" || prev.get(t.id) === "done" || !prev.has(t.id)) continue;
    for (const d of all) {
      if (handoffs.length >= HANDOFF_BATCH_MAX) return { next, handoffs };
      if (d.agent_id === t.agent_id || !d.depends_on.includes(t.id)) continue;
      if (d.status !== "pending" && d.status !== "running") continue;
      const key = `${t.agent_id}>${d.agent_id}`;
      if (seen.has(key)) continue;
      seen.add(key);
      handoffs.push({ from: t.agent_id, to: d.agent_id });
    }
  }
  return { next, handoffs };
}

export interface FolderSlot { busy: boolean; from: string; to: string; t0: number; dur: number }

/** Fixed pool of folder animations: at most `max` fly at once, a bounded queue holds the rest, duplicates are merged. */
export class FolderPool {
  readonly slots: FolderSlot[];
  readonly max: number;
  private queueMax: number;
  private dedupeMs: number;
  private queue: Handoff[] = [];
  private recent = new Map<string, number>();
  constructor(max = 4, queueMax = 10, dedupeMs = 2500) {
    this.max = max; this.queueMax = queueMax; this.dedupeMs = dedupeMs;
    this.slots = Array.from({ length: max }, () => ({ busy: false, from: "", to: "", t0: 0, dur: 1 }));
  }
  /** false when dropped as a duplicate of a recent identical hand-off */
  enqueue(h: Handoff, now: number): boolean {
    if (h.from === h.to) return false;
    const key = `${h.from}>${h.to}`;
    const last = this.recent.get(key);
    if (last !== undefined && now - last < this.dedupeMs) return false;
    this.recent.set(key, now);
    if (this.recent.size > 64) for (const [k, ts] of this.recent) if (now - ts > this.dedupeMs) this.recent.delete(k);
    if (this.queue.length >= this.queueMax) this.queue.shift();
    this.queue.push(h);
    return true;
  }
  /** Frees finished slots and starts queued hand-offs; `durOf` gives the flight time. Returns how many slots are busy. */
  pump(now: number, durOf: (h: Handoff) => number): number {
    let busy = 0;
    for (const s of this.slots) {
      if (s.busy && now - s.t0 >= s.dur) s.busy = false;
      if (!s.busy && this.queue.length) {
        const h = this.queue.shift()!;
        s.busy = true; s.from = h.from; s.to = h.to; s.t0 = now; s.dur = durOf(h);
      }
      if (s.busy) busy++;
    }
    return busy;
  }
  get queued() { return this.queue.length; }
  clear() { this.queue.length = 0; for (const s of this.slots) s.busy = false; }
}
