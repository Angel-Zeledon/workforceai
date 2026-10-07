/**
 * Mock of the chat routing contract (docs/architecture/chat-routing.md): POST /messages decides who answers
 * (greeting -> assistant + 1-2 teammates, finance -> accounting only, 1:1 -> only that agent, task -> plan with reasons)
 * and answers over the WebSocket with chat.typing / route.decided / chat.message. All texts are localized (es/en).
 */
import { tr } from "../i18n-core";
import type { Agent, AgentState, ChatMessage, MessageKind, RouteResponder, TurnIntent } from "../types";

export interface PlanPreview { key: string; agent: string; title: string }

export interface ChatHost {
  emit(type: string, payload: unknown, agentId?: string): void;
  wait(ms: number): Promise<void>;
  agent(id: string): Agent | undefined;
  agents(): Agent[];
  setState(id: string, state: AgentState, activity: string, taskId: string | null, progress: number): void;
  log(agentId: string | null, kind: string, text: string): void;
  planPreview(text: string): PlanPreview[];
  /** opens a request (assistant plans and delegates) */
  startRequest(text: string): Promise<string>;
  /** a task for one agent only, asked from their 1:1 chat; resolves with the summary when done */
  startDirectTask(agentId: string, text: string): Promise<string>;
  uid(prefix: string): string;
}

type Topic = "finance" | "legal" | "hr" | "sales" | "operations" | "analytics" | "general";
const TOPIC_OWNER: Record<Topic, string> = { finance: "accounting", legal: "legal", hr: "hr", sales: "sales", operations: "operations", analytics: "analyst", general: "assistant" };
const TOPIC_RX: [Topic, RegExp][] = [
  ["finance", /finanz|margen|costo|coste|presupuesto|flujo de caja|\bcaja\b|gasto|ingreso|utilidad|impuesto|contab|finance|margin|\bcosts?\b|budget|cash|expense|revenue|profit|\btax/i],
  ["legal", /legal|contrato|cl[aá]usula|demanda|cumplimiento|\bley(es)?\b|contract|clause|lawsuit|compliance/i],
  ["hr", /rrhh|recursos humanos|vacante|reclut|candidat|n[oó]mina|empleado|clima laboral|\bhr\b|hiring|recruit|payroll|employee/i],
  ["operations", /operaci|capacidad|entrega|proveedor|log[ií]stica|inventario|operations|capacity|delivery|supplier/i],
  ["sales", /ventas|vender|cliente|pipeline|comercial|\bsales\b|customer|client|\blead/i],
  ["analytics", /anal[ií]tica|datos|escenario|rentabilidad|pron[oó]stico|an[aá]lisis|analytics|\bdata\b|forecast|scenario|profitab/i],
];
const LEGAL_TOUCH = /impuesto|fiscal|contrat|descuento|precio|cumpl|\btax|contract|discount|pricing|compliance/i;
const PLAN_SPECIFIC = /50[, ]?000|propuesta|cotiz|proposal|\bquote\b|(ventas|sales).*(bajaron|baj[oó]|cayeron|dropped|fell|down)/i;
const IMPERATIVE = /^\s*[¡¿]?(prepara|prep[aá]rame|haz|hazme|elabora|redacta|analiza|investiga|env[ií]a|arma|genera|crea|revisa|calcula|necesito|quiero|por favor|ay[uú]dame|organiza|programa|agenda|prepare|make|draft|write|analy[sz]e|investigate|send|build|create|review|calculate|i need|i want|please|schedule|organi[sz]e)\b/i;
const GREETING = /^\s*[¡¿]?(hola|buen[oa]s|hey|hi\b|hello|saludos|qu[eé] tal|good (morning|afternoon|evening)|ey\b)/i;
const THANKS = /gracias|thanks|thank you|genial|perfecto|great|nice one/i;

export function detectTopic(text: string): Topic | null {
  for (const [t, rx] of TOPIC_RX) if (rx.test(text)) return t;
  return null;
}
export function classify(text: string): { intent: TurnIntent; topic: Topic | "smalltalk" | "task"; thanks?: boolean } {
  const topic = detectTopic(text);
  const words = text.trim().split(/\s+/).length;
  const task = PLAN_SPECIFIC.test(text) || IMPERATIVE.test(text) || (!topic && !text.includes("?") && words >= 6 && !GREETING.test(text) && !THANKS.test(text));
  if (task) return { intent: "task", topic: topic ?? "task" };
  if (topic) return { intent: "question", topic };
  if (THANKS.test(text) && words <= 6) return { intent: "smalltalk", topic: "smalltalk", thanks: true };
  if (GREETING.test(text) || words <= 3) return { intent: "smalltalk", topic: "smalltalk" };
  return { intent: "question", topic: "general" };
}

const first = (a: Agent) => a.name.split(" ")[0];
const shuffle = <T,>(xs: T[]) => xs.map((x) => [Math.random(), x] as const).sort((a, b) => a[0] - b[0]).map((p) => p[1]);

export class MockChat {
  private history: Record<string, ChatMessage[]> = {};
  constructor(private host: ChatHost) {}

  reset() { this.history = {}; }

  list(conv: string, before?: string | null, limit = 60): ChatMessage[] {
    let rows = this.history[conv] || [];
    if (before) rows = rows.filter((m) => +new Date(m.ts) < +new Date(before));
    return rows.slice(-limit);
  }
  matches(conv: string) { return conv === "office" || conv.startsWith("agent:"); }

  /** Say something in a conversation (stores history and emits chat.message). */
  say(conv: string, from: string, to: string, kind: MessageKind, text: string, turn: string | null, replyTo: string | null = null) {
    const m: ChatMessage = { id: this.host.uid("cm"), conversation: conv, turn_id: turn, from, to, kind, text, reply_to: replyTo, ts: new Date().toISOString() };
    (this.history[conv] ||= []).push(m);
    this.host.emit("chat.message", { ...m, message_id: m.id }, from !== "user" && from !== "system" ? from : undefined);
    return m;
  }
  /** Team notes mirrored into the office thread while a task runs (who does what, who asks whom). */
  note(from: string, to: string, kind: MessageKind, text: string) { this.say("office", from, to, kind, text, null); }

  private typing(conv: string, id: string, on: boolean) { this.host.emit("chat.typing", { conversation: conv, agent_id: id, on }, id); }

  /** Shows "typing…", waits a human-feeling time, speaks, and keeps the agent in `talking` only if it was free. */
  private async speak(conv: string, id: string, to: string, text: string, turn: string, replyTo: string | null, kind: MessageKind = "chat") {
    const a = this.host.agent(id); if (!a) return;
    this.typing(conv, id, true);
    await this.host.wait(Math.min(2600, 700 + text.length * 11));
    const prev = { state: a.state, activity: a.activity, task: a.current_task_id, progress: a.progress };
    const free = prev.state === "idle" || prev.state === "completed";
    if (free) this.host.setState(id, "talking", tr("mock.chat.replying"), prev.task, 0);
    this.say(conv, id, to, kind, text, turn, replyTo);
    this.host.log(id, "message.sent", tr("mock.act.chat", { name: first(a) }));
    if (free) {
      this.host.wait(2600).then(() => { if (this.host.agent(id)?.state === "talking") this.host.setState(id, prev.state, prev.activity, prev.task, prev.progress); }).catch(() => {});
    }
  }

  private topicAnswer(id: string, topic: string): string {
    const a = this.host.agent(id)!;
    const answer = tr(`mock.chat.topic.${id}`);
    const cur = a.current_task_id && ["working", "thinking", "reviewing"].includes(a.state) ? a.activity : "";
    void topic;
    return cur ? tr("mock.chat.busy", { task: cur, answer }) : answer;
  }

  /** POST /messages */
  post(conversation: string, text: string): { message_id: string; turn_id: string } {
    const turn = this.host.uid("turn");
    const um = this.say(conversation, "user", conversation === "office" ? "all" : conversation.slice(6), "chat", text, turn);
    this.route(conversation, text, turn, um.id).catch(() => { /* cancelled by reset */ });
    return { message_id: um.id, turn_id: turn };
  }

  private async route(conv: string, text: string, turn: string, replyTo: string) {
    const h = this.host;
    const convAgent = conv.startsWith("agent:") ? conv.slice(6) : null;
    const cls = classify(text);
    await h.wait(350);
    const others = h.agents().filter((a) => a.id !== "assistant");

    // ---------- 1:1 chat: only that agent answers ----------
    if (convAgent && !(cls.intent === "task" && convAgent === "assistant")) {
      const me = h.agent(convAgent); if (!me) return;
      const direct: RouteResponder = { agent_id: convAgent, role: "primary", reason: tr("mock.reason.direct1on1") };
      h.emit("route.decided", { turn_id: turn, intent: cls.intent, topic: cls.topic, responders: [direct] });
      if (cls.intent === "task") {
        await this.speak(conv, convAgent, "user", tr("mock.chat.direct.ok"), turn, replyTo);
        h.log(convAgent, "task.started", tr("mock.act.direct", { name: first(me), task: text.slice(0, 60) }));
        const summary = await h.startDirectTask(convAgent, text);
        await this.speak(conv, convAgent, "user", tr("mock.chat.direct.done", { summary }), turn, replyTo, "answer");
        return;
      }
      let reply: string;
      if (cls.intent === "smalltalk") reply = tr(cls.thanks ? "mock.chat.thanks1" : `mock.chat.hello.${convAgent}`);
      else if (cls.topic !== "general" && cls.topic !== "smalltalk" && TOPIC_OWNER[cls.topic as Topic] !== convAgent) {
        const owner = h.agent(TOPIC_OWNER[cls.topic as Topic]);
        reply = tr("mock.chat.redirect", { name: owner ? first(owner) : "" });
      } else reply = this.topicAnswer(convAgent, cls.topic);
      await this.speak(conv, convAgent, "user", reply, turn, replyTo, "answer");
      return;
    }

    // ---------- office: the assistant routes ----------
    const assistant = h.agent("assistant")!;
    if (cls.intent === "smalltalk") {
      const crew = cls.thanks ? [] : shuffle(others.filter((a) => a.state === "idle" || a.state === "completed")).slice(0, Math.random() < 0.5 ? 1 : 2);
      const responders: RouteResponder[] = [
        { agent_id: "assistant", role: "primary", reason: tr("mock.reason.greet.primary") },
        ...crew.map((a) => ({ agent_id: a.id, role: "contributor" as const, reason: tr("mock.reason.greet.contributor") })),
      ];
      h.emit("route.decided", { turn_id: turn, intent: "smalltalk", topic: "smalltalk", responders });
      h.log("assistant", "route", tr("mock.act.routed", { name: first(assistant), intent: tr("mock.intent.smalltalk"), who: responders.map((r) => first(h.agent(r.agent_id)!)).join(", ") }));
      await this.speak(conv, "assistant", "user", tr(cls.thanks ? "mock.chat.thanks" : "mock.chat.officeHello"), turn, replyTo);
      for (const a of crew) {
        await h.wait(450 + Math.random() * 500);
        await this.speak(conv, a.id, "user", tr(`mock.chat.wave.${a.id}`), turn, replyTo);
      }
      return;
    }

    if (cls.intent === "question") {
      const topic = cls.topic as Topic;
      const owner = TOPIC_OWNER[topic] ?? "assistant";
      const responders: RouteResponder[] = [{ agent_id: owner, role: "primary", reason: tr(`mock.reason.q.${topic}`) }];
      const legalToo = topic === "finance" && LEGAL_TOUCH.test(text);
      if (legalToo) responders.push({ agent_id: "legal", role: "contributor", reason: tr("mock.reason.contrib.legal") });
      h.emit("route.decided", { turn_id: turn, intent: "question", topic, responders });
      h.log("assistant", "route", tr("mock.act.routed", { name: first(assistant), intent: tr("mock.intent.question"), who: responders.map((r) => first(h.agent(r.agent_id)!)).join(", ") }));
      await this.speak(conv, owner, "user", this.topicAnswer(owner, topic), turn, replyTo, "answer");
      if (legalToo) {
        await h.wait(700);
        await this.speak(conv, "legal", "user", tr("mock.chat.finance.legal"), turn, replyTo, "answer");
      }
      return;
    }

    // ---------- office / assistant: a task ----------
    const plan = h.planPreview(text);
    const seen = new Set<string>();
    const responders: RouteResponder[] = [{ agent_id: "assistant", role: "primary", reason: tr("mock.reason.coordinator") }];
    for (const p of plan) if (p.agent !== "assistant" && !seen.has(p.agent)) { seen.add(p.agent); responders.push({ agent_id: p.agent, role: "contributor", reason: tr(`mock.reason.${p.key}`) }); }
    h.emit("route.decided", { turn_id: turn, intent: "task", topic: cls.topic === "task" ? "task" : cls.topic, responders });
    h.log("assistant", "route", tr("mock.act.routed", { name: first(assistant), intent: tr("mock.intent.task"), who: responders.slice(1).map((r) => first(h.agent(r.agent_id)!)).join(", ") }));
    const lines = plan.filter((p) => p.agent !== "assistant").map((p) => tr("mock.task.line", { name: first(h.agent(p.agent)!), task: p.title, reason: tr(`mock.reason.${p.key}`) }));
    await this.speak(conv, "assistant", "user", `${tr("mock.task.intro")}\n${lines.join("\n")}`, turn, replyTo, "delegation");
    h.startRequest(text).catch(() => {});
  }
}
