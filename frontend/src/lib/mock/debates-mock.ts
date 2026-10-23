/**
 * Team debates in the offline demo (B5): GET/POST /debates and the debate before a decision of a request. Mirrors
 * backend/internal/application/debate.go (caps, advisory, untrusted) and the deterministic simulated debate of
 * agent-runtime/app/debate.py, with the same debate.started / debate.completed frames, so the office plays it.
 */
import type { Agent, Debate, DebateTurn } from "../types";
import { getLocale } from "../i18n-core";

export interface DebatesHost {
  emit: (type: string, payload: unknown, agentId?: string) => void;
  wait: (ms: number) => Promise<void>;
  agents: () => Agent[];
}

const MAX_TURNS = 8;
const MARKER = /\[\[debate(?::([^\]]*))?\]\]/i;
const DECISION = /\b(decid\w*|decisi[oó]n\w*|elegir|escoger|choose|decide|decision|which option|qu[eé] opci[oó]n|versus)\b/i;

const TXT = {
  es: {
    options: ["Seguir adelante como está propuesto", "Hacerlo por fases, con un piloto primero", "Posponer y reunir más datos"],
    position: (area: string, opt: string, why: string) => `Desde ${area}, apoyo «${opt}»: ${why}`,
    objection: (name: string, opt: string, risk: string) => `${name}, sobre «${opt}»: ${risk}`,
    synthesis: (opt: string, n: number, total: number) => `El equipo se inclina por «${opt}» (${n} de ${total}). Es una recomendación: la decisión y sus acciones siguen pasando por aprobación.`,
    rationale: "Es la opción con más apoyo y la que mejor responde a las objeciones de riesgo y costo.",
    dissent: (name: string, opt: string) => `${name} prefería «${opt}»`,
    why: { sales: "el cliente valora la rapidez y no quiero perder la ventana comercial.", legal: "es la que deja menos exposición contractual si algo sale mal.", accounting: "protege el margen y el flujo de caja de este trimestre.", operations: "es la que el equipo puede entregar con la capacidad actual.", hr: "no sobrecarga al equipo ni obliga a contratar con prisa.", analyst: "los datos que tenemos la respaldan mejor que las alternativas.", other: "es la que mejor equilibra impacto y riesgo para mi área." } as Record<string, string>,
    risk: { sales: "puede retrasar la respuesta al cliente y enfriar la oportunidad.", legal: "falta revisar las cláusulas de responsabilidad antes de comprometernos.", accounting: "el costo real puede ser mayor que el estimado y bajar el margen.", operations: "la capacidad está al límite; habría que mover otras entregas.", hr: "el equipo ya está cargado; el riesgo de rotación sube.", analyst: "la muestra de datos es pequeña; la conclusión puede no sostenerse.", other: "no está claro quién responde si el resultado no es el esperado." } as Record<string, string>,
    chat: (opt: string) => `Debate del equipo (consultivo, nadie aprobó nada): el equipo se inclina por «${opt}».`,
  },
  en: {
    options: ["Go ahead as proposed", "Do it in phases, with a pilot first", "Postpone and gather more data"],
    position: (area: string, opt: string, why: string) => `From ${area}, I support “${opt}”: ${why}`,
    objection: (name: string, opt: string, risk: string) => `${name}, about “${opt}”: ${risk}`,
    synthesis: (opt: string, n: number, total: number) => `The team leans to “${opt}” (${n} of ${total}). This is a recommendation: the decision and its actions still go through approval.`,
    rationale: "It has the most support and answers the risk and cost objections best.",
    dissent: (name: string, opt: string) => `${name} preferred “${opt}”`,
    why: { sales: "the client values speed and I do not want to lose the commercial window.", legal: "it leaves the least contractual exposure if something goes wrong.", accounting: "it protects this quarter's margin and cash flow.", operations: "it is what the team can deliver with the current capacity.", hr: "it does not overload the team or force rushed hiring.", analyst: "the data we have supports it better than the alternatives.", other: "it balances impact and risk best for my area." } as Record<string, string>,
    risk: { sales: "it may delay the answer to the client and cool the opportunity.", legal: "the liability clauses need a review before we commit.", accounting: "the real cost may exceed the estimate and cut the margin.", operations: "capacity is at its limit; other deliveries would have to move.", hr: "the team is already loaded; attrition risk goes up.", analyst: "the data sample is small; the conclusion may not hold.", other: "it is not clear who answers if the outcome is not the expected one." } as Record<string, string>,
    chat: (opt: string) => `Team debate (advisory, nobody approved anything): the team leans to “${opt}”.`,
  },
};

const hash = (s: string) => { let h = 2166136261 >>> 0; for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619) >>> 0; } return h >>> 0; };
const roleKey = (role: string) => ["sales", "legal", "accounting", "operations", "hr", "analyst"].find((k) => role.includes(k)) ?? "other";

export interface DebateInput { topic: string; options?: string[]; agentIds?: string[]; trigger: Debate["trigger"]; requestId?: string; taskId?: string }

export class MockDebates {
  private list: Debate[] = [];
  private seq = 0;
  constructor(private host: DebatesHost) {}

  reset() { this.list = []; }

  matches(seg: string[]) { return seg[0] === "debates"; }

  async handle(method: string, seg: string[], q: URLSearchParams, body: any): Promise<unknown> {
    if (method === "GET" && !seg[1]) { const r = q.get("request_id"); return this.list.filter((d) => !r || d.request_id === r).slice().reverse().slice(0, 30); }
    if (method === "GET" && seg[1]) { const d = this.list.find((x) => x.id === seg[1]); if (!d) throw new Error(`GET /debates/${seg[1]} -> 404`); return d; }
    if (method === "POST" && !seg[1]) {
      const topic = String(body?.topic ?? "").trim();
      const ids: string[] = Array.isArray(body?.agent_ids) ? body.agent_ids.map(String) : [];
      const options: string[] = Array.isArray(body?.options) ? body.options.map(String) : [];
      if (!topic || ids.length === 1 || ids.length > 4 || options.length > 4) throw new Error("POST /debates -> 400");
      return this.run({ topic, options, agentIds: ids, trigger: "user_request" });
    }
    throw new Error(`mock: ${method} /${seg.join("/")} not implemented`);
  }

  /** The request asks for a debate: the [[debate]] marker (with its named agents) or a decision between alternatives. */
  static wants(text: string): { marked: boolean; ids: string[]; decision: boolean } {
    const m = MARKER.exec(text);
    return { marked: !!m, ids: m?.[1] ? m[1].split(/[,\s]+/).filter(Boolean) : [], decision: DECISION.test(text.replace(MARKER, " ")) };
  }
  static strip(text: string) { return text.replace(MARKER, " ").replace(/\s+/g, " ").trim(); }

  /** Holds one debate: debate.started, the simulated runtime call, debate.completed. */
  async run(in_: DebateInput): Promise<Debate> {
    const agents = this.host.agents();
    const en = getLocale() === "en";
    const t = en ? TXT.en : TXT.es;
    let ids = (in_.agentIds ?? []).filter((id) => id !== "assistant" && agents.some((a) => a.id === id));
    if (ids.length < 2) for (const a of agents) if (a.id !== "assistant" && !ids.includes(a.id) && ids.length < 3) ids.push(a.id);
    ids = ids.slice(0, 4);
    const id = `deb-${Date.now().toString(36)}${(this.seq++).toString(36)}`;
    this.host.emit("debate.started", { debate_id: id, topic: in_.topic, participants: ids, trigger: in_.trigger, request_id: in_.requestId ?? "", task_id: in_.taskId ?? "", max_turns: MAX_TURNS }, "assistant");
    await this.host.wait(1500); // the simulated runtime call
    const options = (in_.options ?? []).length ? in_.options!.slice(0, 4) : t.options;
    const seed = hash(`debate|${in_.topic}|${ids.join(",")}`);
    const favored = new Map(ids.map((p, i) => [p, options[(seed + (i % 2)) % options.length]]));
    const who = (p: string) => agents.find((a) => a.id === p);
    const name = (p: string) => (who(p)?.name ?? p).split(" ")[0];
    const turns: DebateTurn[] = [];
    for (const p of ids) if (turns.length < MAX_TURNS - 1) turns.push({ seq: turns.length + 1, agent_id: p, kind: "position", text: t.position(who(p)?.title || p, favored.get(p)!, t.why[roleKey(who(p)?.role ?? "")]) });
    ids.forEach((p, i) => {
      const o = ids[(i + 1) % ids.length];
      if (turns.length < MAX_TURNS - 1) turns.push({ seq: turns.length + 1, agent_id: p, kind: "objection", text: t.objection(name(o), favored.get(o)!, t.risk[roleKey(who(p)?.role ?? "")]) });
    });
    const votes = (o: string) => ids.filter((p) => favored.get(p) === o).length;
    const choice = options.reduce((best, o) => (votes(o) > votes(best) ? o : best), options[0]);
    turns.push({ seq: turns.length + 1, agent_id: "assistant", kind: "synthesis", text: t.synthesis(choice, votes(choice), ids.length) });
    const d: Debate = {
      id, topic: in_.topic, trigger: in_.trigger, request_id: in_.requestId, task_id: in_.taskId, options, participants: ids, turns,
      synthesis: { choice, rationale: t.rationale, dissent: ids.filter((p) => favored.get(p) !== choice).map((p) => t.dissent(name(p), favored.get(p)!)) },
      status: "done", max_turns: MAX_TURNS, locale: en ? "en" : "es", model: "simulation-claude-sonnet", provider: "simulation", cost_usd: 0.006,
      created_by: in_.trigger === "planner_decision" ? "assistant" : "user", created_at: new Date().toISOString(), advisory: true, untrusted: true,
    };
    this.list.push(d);
    this.host.emit("debate.completed", { debate: d }, "assistant");
    return d;
  }

  /** The chat line the assistant posts after a request's debate (same wording as the backend). */
  static chatLine(d: Debate) { return (getLocale() === "en" ? TXT.en : TXT.es).chat(d.synthesis.choice); }
}
