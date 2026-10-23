import type { Agent, Approval, Task } from "../types";
import type { Proposal, Ritual, RitualSettings } from "../ritualsApi";
import { buildStandupEntries, type RitualEntry, type RitualKind } from "../ritualsLogic";

/**
 * Offline demo of the rituals (B4): the same contract as backend/internal/api/rituals.go. The stand-up is built from
 * the demo's real tasks and approvals (shared builder with the UI tests); the retro counts the week's failures and
 * rejections honestly, so with little history it proposes nothing. Nothing calls a model.
 */
export interface RitualsHost {
  agents(): Agent[];
  tasks(): Task[];
  approvals(): Approval[];
  emit(type: string, payload: unknown, agentId?: string): void;
  /** posts a system line in the office chat */
  note(text: string): void;
}

const DAY = 24 * 3600_000;
const uid = (p: string) => `${p}${Math.random().toString(36).slice(2, 10)}`;

function defaults(): RitualSettings {
  return { timezone: "UTC", standup: { enabled: false, time: "09:30", weekdays: [1, 2, 3, 4, 5] }, retro: { enabled: false, time: "16:00", weekdays: [5] }, use_model: false };
}

const q = (s: string) => `«${s}»`;
function lines(e: RitualEntry): string[] {
  const list = (items: RitualEntry["done"], fmt: (t: string, st: string) => string) =>
    items.map((it) => (it.status === "more" ? `${it.title} más` : fmt(it.title, it.status))).join("; ");
  const out = [
    e.done.length ? `Hice: ${list(e.done, (t, st) => q(t) + (st === "failed" ? " (falló)" : ""))}.` : "Hice: sin tareas terminadas desde el último stand-up.",
    e.next.length ? `Haré: ${list(e.next, (t, st) => q(t) + (st === "running" ? " (en curso)" : ""))}.` : "Haré: nada asignado; disponible para nuevo trabajo.",
  ];
  if (e.blockers.length) out.push(`Bloqueos: ${list(e.blockers, (t, st) => (st === "blocked" ? `${q(t)} está bloqueada` : `espero aprobación de ${q(t)}`))}.`);
  return out;
}

export class MockRituals {
  private rituals: Ritual[] = [];
  private proposals: Proposal[] = [];
  private cfg: RitualSettings = defaults();
  constructor(private host: RitualsHost) {}

  reset() { this.rituals = []; this.proposals = []; this.cfg = defaults(); }

  matches(seg: string[]) { return seg[0] === "rituals" || (seg[0] === "settings" && seg[1] === "rituals"); }

  handle(method: string, seg: string[], body: any, q: URLSearchParams): unknown {
    if (seg[0] === "settings") {
      if (method === "PUT") this.cfg = { ...defaults(), ...body };
      return this.cfg;
    }
    const [, a, b, c] = seg;
    if (method === "GET" && !a) {
      const kind = q.get("kind");
      return this.rituals.filter((r) => !kind || r.kind === kind).slice(0, Number(q.get("limit") || 20));
    }
    if (method === "GET" && a === "proposals") {
      const st = q.get("status");
      return this.proposals.filter((p) => !st || p.status === st);
    }
    if (method === "GET" && a) {
      const r = this.rituals.find((x) => x.id === a);
      if (!r) throw new Error("not found");
      return { ...r, proposals: this.proposals.filter((p) => p.ritual_id === r.id) };
    }
    if (method === "POST" && b === "run" && (a === "standup" || a === "retro")) return this.run(a);
    if (method === "POST" && a === "proposals" && b && (c === "accept" || c === "dismiss")) {
      const p = this.proposals.find((x) => x.id === b);
      if (!p) throw new Error("not found");
      if (p.status !== "pending") throw new Error("conflict");
      Object.assign(p, { status: c === "accept" ? "accepted" : "dismissed", resolved_at: new Date().toISOString(), resolved_by: "demo", applied: false });
      return c === "accept" ? { proposal: p, applied: false } : p;
    }
    throw new Error(`mock rituals: ${method} /${seg.join("/")}`);
  }

  private run(kind: RitualKind): Ritual {
    const now = Date.now();
    const prev = this.rituals.find((r) => r.kind === "standup" && r.status === "held");
    const from = kind === "retro" ? now - 7 * DAY : prev && now - Date.parse(prev.held_at) < 7 * DAY ? Date.parse(prev.held_at) : now - DAY;
    const agents = this.host.agents();
    let entries: RitualEntry[];
    let proposals: Proposal[] = [];
    const id = uid("rit_");
    if (kind === "standup") {
      entries = buildStandupEntries(agents, this.host.tasks(), this.host.approvals(), from, now).map((e) => ({ ...e, lines: lines(e) }));
    } else {
      entries = agents.map((ag) => {
        const inWeek = (ts: string | null) => !!ts && Date.parse(ts) >= from;
        const tasks = this.host.tasks().filter((t) => t.agent_id === ag.id && inWeek(t.finished_at));
        const done = tasks.filter((t) => t.status === "done").length, failed = tasks.filter((t) => t.status === "failed").length;
        const rejected = this.host.approvals().filter((p) => p.agent_id === ag.id && p.status === "rejected" && inWeek(p.resolved_at)).length;
        const mine: Proposal[] = [];
        const mk = (signal: Proposal["signal"], title: string, text: string): Proposal => ({ id: uid("rp_"), ritual_id: id, agent_id: ag.id, kind: "instruction_note", signal, title, text, evidence: [], status: "pending", applied: false, created_at: new Date(now).toISOString() });
        if (rejected >= 2) mine.push(mk("rejected_approvals", "Explicar mejor mis solicitudes de aprobación", `Antes de pedir una aprobación, explica en una línea por qué la acción es necesaria. (Retro: ${rejected} aprobaciones rechazadas en la semana.)`));
        if (failed >= 2) mine.push(mk("failed_tasks", "Pedir lo que me falta al empezar", `Si te falta un dato, un archivo o un permiso, dilo al empezar. (Retro: ${failed} tareas fallidas en la semana.)`));
        proposals = proposals.concat(mine.slice(0, 2));
        return {
          agent_id: ag.id, name: ag.name, role: ag.role, done: [], next: [], blockers: [], quiet: !done && !failed && !rejected,
          retro: { tasks_done: done, tasks_failed: failed, rejected, reworks: 0, score: null },
          lines: [`Semana: ${done} tareas hechas, ${failed} fallidas, ${rejected} aprobaciones rechazadas, 0 retrabajos.`,
            mine.length ? `Propongo ${mine.length} mejora(s) a mi trabajo (pendiente de que una persona decida).` : "Sin propuestas: ninguna señal se repitió en la semana."],
        };
      });
    }
    const r: Ritual = {
      id, kind, origin: "manual", status: "held", locale: "es", window_from: new Date(from).toISOString(), window_to: new Date(now).toISOString(),
      entries, absent: [], summary: "", summary_source: "deterministic", created_by: "demo", held_at: new Date(now).toISOString(),
    };
    const busy = entries.filter((e) => !e.quiet);
    r.summary = kind === "standup"
      ? `Stand-up con ${entries.length} agentes: ${busy.length} con novedades, ${entries.length - busy.length} sin novedades.`
      : `Retrospectiva semanal con ${entries.length} agentes; ${proposals.length} propuestas de mejora pendientes de decisión.`;
    this.rituals.unshift(r);
    this.proposals = proposals.concat(this.proposals);
    this.host.emit("ritual.held", { ritual_id: id, kind, origin: "manual", agent_ids: entries.map((e) => e.agent_id), proposals: proposals.length });
    const quiet = entries.filter((e) => e.quiet).map((e) => e.name);
    this.host.note([kind === "standup" ? "Stand-up diario" : "Retrospectiva semanal", r.summary,
      ...busy.map((e) => `\n${e.name}:\n${e.lines.map((l) => `- ${l}`).join("\n")}`),
      quiet.length ? `\nSin novedades: ${quiet.join(", ")}` : ""].filter(Boolean).join("\n"));
    return { ...r, proposals };
  }
}
