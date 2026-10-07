/**
 * Mock backend (NEXT_PUBLIC_MOCK=true). Simulates the Go backend locally: same REST shapes and the same
 * WebSocket frames/payloads as docs/SPEC.md, including the scripted $50,000 proposal scenario.
 * The frontend never special-cases it: frames go through the same store.apply() as the real WS.
 */
import type {
  ActivityItem, Agent, AgentState, Approval, Conversation, MemoryItem, Message, MessageKind,
  Metrics, PlanTask, Report, Request, StructuredOutput, Task, WsFrame,
} from "../types";
import { MockArtifacts } from "./artifacts-mock";
import { MockOrgConfig } from "./orgconfig-mock";
import { MockConnections } from "./connections-mock";
import { MockChat } from "./chat-mock";
import { tr } from "../i18n-core";

const ORG = "00000000-0000-0000-0000-000000000001";
let seq = 0;
const uid = (p: string) => `${p}-${Date.now().toString(36)}${(seq++).toString(36)}`;
const nowIso = () => new Date().toISOString();

class Cancelled extends Error {}

interface TaskSpec {
  key: string; agent: string; title: string; desc: string; secs: number; deps: string[];
  activities: string[];
  consult?: { to: string; q: string; a: string };
  approval?: { action: string; title: string; details: string; risk: "low" | "medium" | "high" };
  reviewing?: boolean;
  fail?: string;
  out: Partial<StructuredOutput>;
}

const SEED_AGENTS: Omit<Agent, "state" | "activity" | "current_task_id" | "progress">[] = [
  { id: "sales", name: "Valeria Ríos", role: "sales", title: "Gerente de Ventas", description: "Gestiona pipeline, cuentas clave y propuestas comerciales.", tools: ["crm.read", "proposal.draft", "email.send"], permissions: ["read:crm", "draft:proposal", "send:proposal (approval)"], autonomy: "approve_each", metrics: { tasks_completed: 0, tasks_pending: 0, avg_seconds: 0, cost_usd: 0 } },
  { id: "hr", name: "Marcos Peña", role: "hr", title: "Recursos Humanos", description: "Perfiles, contratación, clima y políticas internas.", tools: ["ats.read", "payroll.read"], permissions: ["read:hr", "draft:job_post"], autonomy: "suggest", metrics: { tasks_completed: 0, tasks_pending: 0, avg_seconds: 0, cost_usd: 0 } },
  { id: "legal", name: "Elena Castro", role: "legal", title: "Abogada", description: "Revisión de contratos, cumplimiento y riesgo legal.", tools: ["contracts.read", "clauses.search"], permissions: ["read:contracts", "draft:review"], autonomy: "approve_each", metrics: { tasks_completed: 0, tasks_pending: 0, avg_seconds: 0, cost_usd: 0 } },
  { id: "accounting", name: "Tomás Vidal", role: "accounting", title: "Contador", description: "Márgenes, costos, flujo de caja y presupuesto.", tools: ["ledger.read", "margin.calc"], permissions: ["read:finance"], autonomy: "rules", metrics: { tasks_completed: 0, tasks_pending: 0, avg_seconds: 0, cost_usd: 0 } },
  { id: "analyst", name: "Nadia Ortega", role: "analyst", title: "Analista", description: "Analítica de negocio, rentabilidad y escenarios.", tools: ["bi.query", "forecast.run"], permissions: ["read:analytics"], autonomy: "autonomous", metrics: { tasks_completed: 0, tasks_pending: 0, avg_seconds: 0, cost_usd: 0 } },
  { id: "operations", name: "Iván Duarte", role: "operations", title: "Operaciones", description: "Capacidad, entrega, proveedores y logística.", tools: ["capacity.read", "schedule.read"], permissions: ["read:ops"], autonomy: "rules", metrics: { tasks_completed: 0, tasks_pending: 0, avg_seconds: 0, cost_usd: 0 } },
  { id: "assistant", name: "Sofía Lara", role: "assistant", title: "Secretaria / Asistente Ejecutivo", description: "Recibe solicitudes, coordina al equipo y consolida informes.", tools: ["planner", "report.build"], permissions: ["delegate", "read:all"], autonomy: "autonomous", metrics: { tasks_completed: 0, tasks_pending: 0, avg_seconds: 0, cost_usd: 0 } },
];

const SEED_MEMORY: Record<string, MemoryItem[]> = {
  sales: [{ key: "cliente_clave", value: "Grupo Alfa — cuenta estratégica, contacto: dirección comercial", scope: "org" }, { key: "descuento_max", value: "12% sin aprobación del director", scope: "org" }],
  hr: [{ key: "tiempo_contratacion", value: "Promedio 3-4 semanas para perfiles senior", scope: "org" }, { key: "banda_salarial", value: "Senior ops: $3,600-$4,200 mensuales", scope: "agent" }],
  legal: [{ key: "plantilla_contrato", value: "MSA v3.2 con cláusula de penalización estándar 2% semanal", scope: "org" }],
  accounting: [{ key: "margen_objetivo", value: "Margen mínimo aceptable: 22%", scope: "org" }, { key: "costo_hora", value: "Costo cargado por hora de consultoría: $48", scope: "agent" }],
  analyst: [{ key: "modelo_rentabilidad", value: "Modelo v2: margen = (ingreso - costo directo - capacidad) / ingreso", scope: "agent" }],
  operations: [{ key: "capacidad_actual", value: "86% de utilización del equipo de entrega", scope: "org" }],
  assistant: [{ key: "preferencia_informe", value: "Informes ejecutivos con resumen, riesgos y siguientes pasos", scope: "org" }],
};

const out = (o: Partial<StructuredOutput>): Partial<StructuredOutput> => o;

export function buildPlan(text: string): TaskSpec[] {
  const t = text.toLowerCase();
  if (/50[, ]?000|propuesta|cliente|cotiz|proposal|\bquote/.test(t)) {
    return [
      { key: "t1", agent: "sales", title: "Analizar cliente y requerimientos", desc: "Perfilar al cliente y alcance de la propuesta de $50,000.", secs: 5, deps: [], activities: ["Revisando historial del cliente", "Mapeando requerimientos", "Estimando alcance y precio"], out: out({ summary: "Cliente Grupo Alfa: alcance de 6 meses, ticket de $50,000, decisor identificado.", findings: ["Cliente recurrente con buen historial de pago", "Pide entrega en 8 semanas", "Margen objetivo comercial: 31%"], metrics: { ticket: "$50,000", margen_estimado: "31%" }, recommendations: ["Preparar propuesta por fases"], confidence: 0.86 }) },
      { key: "t2", agent: "legal", title: "Revisar términos del contrato", desc: "Revisar cláusulas de penalización, SLA y responsabilidad.", secs: 6, deps: ["t1"], reviewing: true, activities: ["Leyendo borrador de contrato", "Comparando contra plantilla MSA", "Marcando cláusulas de riesgo"],
        consult: { to: "sales", q: "¿El cliente exige penalización por retraso en la entrega?", a: "Sí, pidió 2% semanal con tope del 10% sobre el valor total." },
        out: out({ summary: "Contrato aceptable con dos ajustes: tope de penalización y cláusula de cambio de alcance.", findings: ["Penalización 2%/semana con tope 10% (aceptable)", "Falta cláusula de cambio de alcance", "Propiedad intelectual sin conflictos"], recommendations: ["Agregar cláusula de cambio de alcance", "Limitar responsabilidad al monto del contrato"], confidence: 0.82 }) },
      { key: "t3", agent: "accounting", title: "Calcular margen real de la propuesta", desc: "Recalcular el margen con costos de capacidad y descuentos.", secs: 6, deps: ["t1"], activities: ["Cargando costos directos", "Aplicando descuento por volumen", "Recalculando margen"],
        consult: { to: "operations", q: "¿Cuál es el costo real de capacidad adicional para esta entrega?", a: "Dos contrataciones senior, cerca de $7,800 al mes cargado." },
        out: out({ summary: "El margen real es 24%, no 31%: el costo de capacidad adicional lo reduce 7 puntos.", findings: ["Margen presentado: 31%", "Margen real tras capacidad: 24%", "Sigue sobre el mínimo de 22%"], metrics: { margen_presentado: "31%", margen_real: "24%", costo_capacidad_mensual: "$7,800" }, evidence: ["Libro mayor Q3", "Costo cargado por hora $48"], recommendations: ["Aprobar solo si el margen se mantiene ≥ 22%"], confidence: 0.9 }) },
      { key: "t4", agent: "analyst", title: "Analizar rentabilidad y escenarios", desc: "Modelar escenarios optimista, base y adverso.", secs: 7, deps: ["t1"], activities: ["Construyendo modelo de rentabilidad", "Corriendo escenarios", "Calculando sensibilidad"],
        consult: { to: "accounting", q: "¿Confirmas el margen del 24% para el escenario base?", a: "Confirmado: 24% tras costos de capacidad y descuento por volumen." },
        out: out({ summary: "Escenario base rentable (24%); el adverso cae a 17% si el retraso supera 3 semanas.", findings: ["Base: 24% margen, retorno en 5 meses", "Adverso: 17% si hay retraso > 3 semanas", "Optimista: 28%"], hypotheses: ["El retraso depende de la velocidad de contratación"], metrics: { base: "24%", adverso: "17%", optimista: "28%" }, recommendations: ["Incluir hitos con pago parcial"], confidence: 0.78 }) },
      { key: "t5", agent: "operations", title: "Evaluar capacidad de entrega", desc: "Verificar si el equipo puede cumplir el plazo de 8 semanas.", secs: 6, deps: ["t1"], activities: ["Revisando carga actual (86%)", "Simulando asignación de equipo", "Calculando brecha de capacidad"],
        consult: { to: "hr", q: "¿En cuánto tiempo podemos contratar 2 personas senior?", a: "Entre 3 y 4 semanas con el pipeline actual de candidatos." },
        out: out({ summary: "No hay capacidad suficiente: se necesita contratar 2 personas para cumplir 8 semanas.", findings: ["Utilización actual 86%", "Brecha: 2 perfiles senior", "Contratación toma 3-4 semanas"], metrics: { brecha_personas: 2, utilizacion: "86%" }, recommendations: ["Iniciar reclutamiento en paralelo a la firma"], suggested_tasks: ["Abrir 2 vacantes senior"], confidence: 0.84 }) },
      { key: "t6", agent: "sales", title: "Preparar y enviar propuesta", desc: "Redactar la propuesta final con los hallazgos y enviarla al cliente.", secs: 5, deps: ["t2", "t3", "t4", "t5"], reviewing: true, activities: ["Integrando hallazgos del equipo", "Redactando propuesta", "Revisión final de la propuesta"],
        approval: { action: "send_proposal", title: "Enviar propuesta de $50,000 a Grupo Alfa", details: "Propuesta final: ticket $50,000, margen real 24%, penalización 2%/semana tope 10%, entrega en 8 semanas sujeta a 2 contrataciones.", risk: "high" },
        out: out({ summary: "Propuesta final lista y enviada a Grupo Alfa tras la aprobación.", findings: ["Margen real 24% incluido en el análisis interno", "Cláusula de cambio de alcance agregada"], recommendations: ["Dar seguimiento en 3 días"], confidence: 0.88 }) },
      { key: "t7", agent: "assistant", title: "Consolidar informe ejecutivo", desc: "Unir los resultados del equipo en un informe.", secs: 4, deps: ["t6"], activities: ["Reuniendo resultados del equipo", "Redactando resumen ejecutivo", "Verificando cifras"], out: out({ summary: "Informe ejecutivo consolidado.", confidence: 0.9 }) },
    ];
  }
  if (/ventas|bajaron|bajó|cayeron|caída|sales (dropped|fell)|sales are down/.test(t)) {
    return [
      { key: "b1", agent: "analyst", title: "Analizar caída de ventas por segmento", desc: "Descomponer la caída por cliente, producto y región.", secs: 6, deps: [], activities: ["Consultando datos de ventas", "Segmentando por cliente y región", "Buscando anomalías"],
        consult: { to: "sales", q: "¿Hubo cambios en el pipeline o en el equipo este mes?", a: "Perdimos 2 cuentas medianas por precio y el pipeline de nuevos leads bajó 18%." },
        out: out({ summary: "La caída (-14%) se concentra en 2 cuentas medianas y en menor flujo de leads nuevos.", findings: ["-14% vs. mes anterior", "62% de la caída viene de 2 cuentas", "Leads nuevos -18%"], hypotheses: ["Sensibilidad a precio frente a un competidor"], metrics: { variacion: "-14%", leads: "-18%" }, confidence: 0.76 }) },
      { key: "b2", agent: "sales", title: "Revisar pipeline y cuentas perdidas", desc: "Entender causas comerciales de las pérdidas.", secs: 5, deps: [], activities: ["Revisando oportunidades perdidas", "Contactando cuentas en riesgo", "Priorizando recuperación"], out: out({ summary: "Dos cuentas perdidas por precio; hay 5 oportunidades recuperables.", findings: ["5 oportunidades recuperables", "Competidor ofrece 9% menos"], recommendations: ["Plan de retención con descuento controlado"], confidence: 0.8 }) },
      { key: "b3", agent: "accounting", title: "Medir impacto financiero", desc: "Cuantificar efecto en ingresos y margen.", secs: 5, deps: ["b1"], activities: ["Calculando ingresos perdidos", "Proyectando flujo de caja"], out: out({ summary: "Impacto estimado de $38,000 en el trimestre; margen estable.", metrics: { impacto_trimestre: "$38,000" }, confidence: 0.82 }) },
      { key: "b4", agent: "assistant", title: "Consolidar informe", desc: "Unir resultados.", secs: 4, deps: ["b2", "b3"], activities: ["Reuniendo resultados", "Redactando resumen"], out: out({ summary: "Informe consolidado.", confidence: 0.88 }) },
    ];
  }
  if (/contrat|vacante|personal|reclut|\bhire\b|hiring|vacanc/.test(t)) {
    return [
      { key: "c1", agent: "hr", title: "Definir perfiles y banda salarial", desc: "Perfiles, requisitos y rango salarial de mercado.", secs: 5, deps: [], activities: ["Definiendo perfiles", "Comparando banda salarial"], out: out({ summary: "Dos perfiles senior definidos; banda $3,600-$4,200.", metrics: { banda: "$3,600-$4,200" }, confidence: 0.84 }) },
      { key: "c2", agent: "accounting", title: "Validar presupuesto de contratación", desc: "Impacto en nómina y flujo de caja.", secs: 5, deps: ["c1"], activities: ["Calculando costo cargado", "Proyectando nómina"], consult: { to: "hr", q: "¿Incluyes prestaciones en la banda salarial?", a: "Sí, el rango es bruto; el costo cargado agrega ~28%." }, out: out({ summary: "Costo cargado de $7,800/mes por las 2 personas; viable dentro del presupuesto.", metrics: { costo_mensual: "$7,800" }, confidence: 0.87 }) },
      { key: "c3", agent: "legal", title: "Revisar contratos laborales", desc: "Modelos de contrato y cumplimiento.", secs: 5, deps: ["c1"], reviewing: true, activities: ["Revisando plantilla laboral", "Verificando cumplimiento"], out: out({ summary: "Plantilla vigente cumple; agregar cláusula de confidencialidad.", confidence: 0.85 }) },
      { key: "c4", agent: "assistant", title: "Consolidar informe", desc: "Unir resultados.", secs: 4, deps: ["c2", "c3"], activities: ["Reuniendo resultados", "Redactando resumen"], out: out({ summary: "Informe consolidado.", confidence: 0.88 }) },
    ];
  }
  const failing = /error|falla|fall[óo]/.test(t);
  return [
    { key: "d1", agent: "analyst", title: "Investigar y estructurar la solicitud", desc: "Entender el pedido y definir qué datos hacen falta.", secs: 5, deps: [], activities: ["Estructurando el problema", "Reuniendo datos relevantes"], out: out({ summary: "Solicitud estructurada en 3 líneas de análisis.", findings: ["Se requiere validación operativa"], confidence: 0.75 }) },
    { key: "d2", agent: "operations", title: "Evaluar viabilidad operativa", desc: "Impacto operativo y recursos.", secs: 5, deps: ["d1"], fail: failing ? "El sistema de capacidad no respondió (timeout 30s)" : undefined, activities: ["Revisando carga actual", "Evaluando recursos"], out: out({ summary: "Operativamente viable con ajustes menores.", confidence: 0.8 }) },
    { key: "d3", agent: "assistant", title: "Consolidar informe", desc: "Unir resultados.", secs: 4, deps: ["d2"], activities: ["Reuniendo resultados", "Redactando resumen"], out: out({ summary: "Informe consolidado.", confidence: 0.85 }) },
  ];
}

const fullOutput = (o: Partial<StructuredOutput>): StructuredOutput => ({
  summary: "", findings: [], metrics: {}, hypotheses: [], evidence: [], recommendations: [], confidence: 0.8, suggested_tasks: [], ...o,
});

export class MockBackend {
  private listeners = new Set<(f: WsFrame) => void>();
  private gen = 0;
  private agents: Agent[] = [];
  private tasks: Task[] = [];
  private requests: Request[] = [];
  private conversations: Conversation[] = [];
  private messages: Record<string, Message[]> = {};
  private approvals: Approval[] = [];
  private reports: Report[] = [];
  private activity: ActivityItem[] = [];
  private errorsCount = 0;
  private budget = 50;
  private decisions = new Map<string, (d: "approve" | "reject") => void>();
  private specs = new Map<string, TaskSpec>();
  private running = new Map<string, number>(); // agent -> number of tasks running
  private orgCfg = new MockOrgConfig(); // workflow templates, onboarding, org settings and schedules
  private art = new MockArtifacts((t, p, a) => this.emit(t, p, a), (ms) => this.wait(ms)); // workspaces: artifact.* endpoints/events
  private chat = new MockChat({
    emit: (t, p, a) => this.emit(t, p, a), wait: (ms) => this.wait(ms), agent: (id) => this.agents.find((a) => a.id === id), agents: () => this.agents,
    setState: (id, st, act, tid, pr) => this.setState(id, st, act, tid, pr), log: (a, k, t) => this.log(a, k, t),
    planPreview: (text) => buildPlan(text).map((s) => ({ key: s.key, agent: s.agent, title: s.title })),
    startRequest: (text) => this.startRequest(text), startDirectTask: (id, text, reason) => this.startDirectTask(id, text, reason), uid,
  }); // chat routing: POST /messages, chat.* and route.decided
  private conns = new MockConnections((t, p, a) => this.emit(t, p, a)); // connections + controls: connection.*, control.* endpoints/events

  constructor() { this.seed(); }

  private seed() {
    this.agents = SEED_AGENTS.map((a) => ({ ...a, metrics: { ...a.metrics }, state: "idle" as AgentState, activity: "Disponible", current_task_id: null, progress: 0 }));
    this.tasks = []; this.requests = []; this.conversations = []; this.messages = {}; this.approvals = [];
    this.reports = []; this.activity = []; this.errorsCount = 0; this.chat?.reset(); this.decisions.clear(); this.specs.clear(); this.running.clear();
  }

  // ---- realtime -----------------------------------------------------------------------------
  connect(cb: (f: WsFrame) => void) {
    this.listeners.add(cb);
    cb(this.frame("hello", { agents: this.agents }));
    return () => { this.listeners.delete(cb); };
  }
  private frame(type: string, payload: unknown, agent_id?: string): WsFrame {
    return { id: uid("evt"), type, ts: nowIso(), org_id: ORG, ...(agent_id ? { agent_id } : {}), payload };
  }
  private emit(type: string, payload: unknown, agent_id?: string) {
    const f = this.frame(type, payload, agent_id);
    this.listeners.forEach((l) => l(f));
  }
  private wait(ms: number) {
    const g = this.gen;
    return new Promise<void>((res, rej) => setTimeout(() => (g === this.gen ? res() : rej(new Cancelled())), ms));
  }
  private agent(id: string) { return this.agents.find((a) => a.id === id)!; }

  private setState(id: string, state: AgentState, activity: string, task_id: string | null, progress: number) {
    const a = this.agent(id);
    if (!a) return;
    Object.assign(a, { state, activity, current_task_id: task_id, progress: Math.round(progress) });
    this.emit("agent.state_changed", { agent_id: id, state, activity, task_id, progress: Math.round(progress) }, id);
  }
  private log(agent_id: string | null, kind: string, text: string) {
    const item: ActivityItem = { id: uid("act"), ts: nowIso(), agent_id, kind, text };
    this.activity.unshift(item);
    this.emit("activity.logged", { item }, agent_id ?? undefined);
  }
  private metrics(): Metrics {
    return {
      tasks_active: this.tasks.filter((t) => t.status === "running" || t.status === "awaiting_approval").length,
      tasks_done: this.tasks.filter((t) => t.status === "done").length,
      approvals_pending: this.approvals.filter((a) => a.status === "pending").length,
      cost_usd: +this.agents.reduce((s, a) => s + a.metrics.cost_usd, 0).toFixed(4),
      budget_usd: this.budget, errors: this.errorsCount, requests_total: this.requests.length,
    };
  }
  private pushMetrics() {
    for (const a of this.agents) a.metrics.tasks_pending = this.tasks.filter((t) => t.agent_id === a.id && (t.status === "pending" || t.status === "running")).length;
    this.emit("metrics.updated", { metrics: this.metrics() });
  }
  private sendMessage(conv: Conversation, from: string, to: string, kind: MessageKind, text: string, task_id: string | null = null) {
    const m: Message = { id: uid("msg"), conversation_id: conv.id, from, to, kind, text, task_id, ts: nowIso() };
    (this.messages[conv.id] ||= []).push(m);
    conv.last_message_at = m.ts;
    this.emit("message.sent", { message: m }, from !== "user" && from !== "system" ? from : undefined);
    return m;
  }
  private emitTask(type: string, t: Task) { this.emit(type, { task: { ...t }, ...(type === "task.created" && t.assigned_reason ? { assigned_reason: t.assigned_reason } : {}) }, t.agent_id); this.art.onTask(type, t); }

  // ---- orchestrator -------------------------------------------------------------------------
  private async startRequest(text: string): Promise<string> {
    const req: Request = { id: uid("req"), text, status: "planning", created_at: nowIso(), report_id: null, cost_usd: 0 };
    this.requests.unshift(req);
    this.emit("request.received", { request_id: req.id, text });
    this.art.onRequest(text);
    this.log("assistant", "request.received", `Nueva solicitud: ${text}`);
    const conv: Conversation = { id: uid("conv"), title: `Solicitud: ${text.slice(0, 48)}`, participants: ["assistant"], request_id: req.id, last_message_at: nowIso() };
    this.conversations.unshift(conv);
    this.messages[conv.id] = [];
    this.sendMessage(conv, "user", "assistant", "chat", text);
    this.pushMetrics();
    this.orchestrate(req, conv).catch(() => { /* cancelled by reset */ });
    return req.id;
  }

  /** Task asked in an agent's own 1:1 chat: only that agent works on it. Resolves with the summary. */
  private async startDirectTask(agentId: string, text: string, reason?: string): Promise<string> {
    const title = tr("mock.direct.title", { text: text.slice(0, 48) });
    const req: Request = { id: uid("req"), text, status: "running", created_at: nowIso(), report_id: null, cost_usd: 0 };
    this.requests.unshift(req);
    this.emit("request.received", { request_id: req.id, text });
    const spec: TaskSpec = {
      key: "direct", agent: agentId, title, desc: tr("mock.direct.desc"), secs: 4, deps: [],
      activities: [tr("mock.direct.act1"), tr("mock.direct.act2"), tr("mock.direct.act3")],
      out: out({ summary: tr("mock.direct.summary", { title }), confidence: 0.82 }),
    };
    const task: Task = {
      id: uid("task"), request_id: req.id, workflow_id: null, title, description: spec.desc, agent_id: agentId, status: "pending",
      depends_on: [], parent_task_id: null, created_at: nowIso(), started_at: null, finished_at: null, output: null,
      assigned_reason: reason || tr("mock.reason.direct"),
    };
    this.specs.set(task.id, spec); this.tasks.push(task);
    this.emit("plan.created", { request_id: req.id, tasks: [{ id: task.id, title, agent_id: agentId, depends_on: [] }] }, agentId);
    this.emitTask("task.created", task);
    this.pushMetrics();
    await this.runTask(task, req);
    req.status = task.status === "done" ? "done" : "failed";
    if (task.status === "done") this.emit("request.completed", { request_id: req.id, report_id: null }, agentId);
    this.pushMetrics();
    return task.output?.summary || "";
  }

  private async orchestrate(req: Request, conv: Conversation) {
    this.setState("assistant", "thinking", "Analizando la solicitud", null, 15);
    await this.wait(900);
    this.setState("assistant", "thinking", "Planificando tareas y dependencias", null, 60);
    await this.wait(1500);
    const specs = buildPlan(req.text);
    const keyToId = new Map<string, string>();
    const tasks: Task[] = specs.map((s) => {
      const t: Task = {
        id: uid("task"), request_id: req.id, workflow_id: null, title: s.title, description: s.desc, agent_id: s.agent, status: "pending",
        depends_on: [], parent_task_id: null, created_at: nowIso(), started_at: null, finished_at: null, output: null,
        assigned_reason: tr(`mock.reason.${s.key}`),
      };
      keyToId.set(s.key, t.id); this.specs.set(t.id, s);
      return t;
    });
    tasks.forEach((t, i) => { t.depends_on = specs[i].deps.map((k) => keyToId.get(k)!); });
    this.tasks.push(...tasks);
    const plan: PlanTask[] = tasks.map((t) => ({ id: t.id, title: t.title, agent_id: t.agent_id, depends_on: t.depends_on }));
    this.emit("plan.created", { request_id: req.id, tasks: plan }, "assistant");
    this.log("assistant", "plan.created", `Plan creado con ${tasks.length} tareas`);
    for (const t of tasks) this.emitTask("task.created", t);
    conv.participants = Array.from(new Set(["assistant", ...tasks.map((t) => t.agent_id)]));
    this.setState("assistant", "talking", "Delegando tareas al equipo", null, 80);
    // delegation messages (assistant -> each agent), draws lines in the office
    const delegated = Array.from(new Set(tasks.filter((t) => t.depends_on.length === 0 || true).map((t) => t.agent_id))).filter((a) => a !== "assistant");
    for (const a of delegated) {
      const first = tasks.find((t) => t.agent_id === a)!;
      this.sendMessage(conv, "assistant", a, "delegation", `Te asigno: ${first.title}.`, first.id);
      await this.wait(450);
    }
    this.setState("assistant", "waiting", "Coordinando al equipo", null, 0);
    this.pushMetrics();

    // scheduler: run tasks whose dependencies are done, in parallel
    const started = new Set<string>();
    const pending = new Set(tasks.map((t) => t.id));
    while (pending.size) {
      let progressed = false;
      for (const id of Array.from(pending)) {
        const t = this.tasks.find((x) => x.id === id)!;
        const deps = t.depends_on.map((d) => this.tasks.find((x) => x.id === d)!);
        if (deps.some((d) => d.status === "failed" || d.status === "blocked")) { pending.delete(id); continue; }
        if (deps.every((d) => d.status === "done") && !started.has(id)) {
          started.add(id); pending.delete(id); progressed = true;
          this.runTask(t, req).catch(() => { /* cancelled */ });
        }
      }
      if (!pending.size) break;
      await this.wait(progressed ? 200 : 400);
    }
    // wait for all tasks in this request to reach a terminal state
    while (tasks.some((t) => !["done", "failed", "blocked"].includes(this.tasks.find((x) => x.id === t.id)!.status))) await this.wait(400);
    const all = tasks.map((t) => this.tasks.find((x) => x.id === t.id)!);
    if (all.every((t) => t.status === "done")) await this.finishRequest(req, all);
    else {
      this.log("assistant", "request.failed", "La solicitud no pudo completarse: hay tareas bloqueadas o fallidas");
      this.setState("assistant", "idle", "Disponible", null, 0);
    }
    this.pushMetrics();
  }

  private async finishRequest(req: Request, tasks: Task[]) {
    this.setState("assistant", "working", "Sintetizando informe final", null, 50);
    await this.wait(1800);
    const body = (t: Task) => [t.output?.summary, ...(t.output?.findings || []).map((f) => `• ${f}`)].filter(Boolean).join("\n");
    const contributors = Array.from(new Set(tasks.map((t) => t.agent_id)));
    const cost = +this.agents.reduce((s, a) => s + a.metrics.cost_usd, 0).toFixed(2);
    const is50k = /50[, ]?000|propuesta/i.test(req.text);
    const report: Report = {
      id: uid("rep"), request_id: req.id, title: `Informe: ${req.text.slice(0, 70)}`,
      summary: is50k
        ? "La propuesta de $50,000 es viable pero el margen real baja de 31% a 24% por el costo de capacidad adicional, y requiere contratar 2 personas (3-4 semanas). Contrato aceptable con ajustes. La propuesta fue enviada tras tu aprobación."
        : tasks.map((t) => t.output?.summary).filter(Boolean).join(" "),
      sections: tasks.filter((t) => t.agent_id !== "assistant").map((t) => ({ heading: `${this.agent(t.agent_id).title}: ${t.title}`, body: body(t) })),
      contributors, cost_usd: cost, created_at: nowIso(),
    };
    this.reports.unshift(report);
    this.emit("report.created", { report }, "assistant");
    req.report_id = report.id; req.status = "done"; req.cost_usd = cost;
    this.emit("request.completed", { request_id: req.id, report_id: report.id }, "assistant");
    this.log("assistant", "request.completed", `Solicitud completada. Informe: ${report.title}`);
    this.chat.note("assistant", "user", "chat", tr("mock.task.done", { title: report.title }));
    this.setState("assistant", "completed", "Informe entregado", null, 100);
    await this.wait(2500);
    if (this.agent("assistant").state === "completed") this.setState("assistant", "idle", "Disponible", null, 0);
  }

  private bump(id: string, d: number) { this.running.set(id, (this.running.get(id) || 0) + d); }
  private settle(id: string, delay: number) {
    this.wait(delay).then(() => {
      const a = this.agent(id);
      if ((this.running.get(id) || 0) === 0 && a.state === "completed") this.setState(id, "idle", "Disponible", null, 0);
    }).catch(() => {});
  }

  private async runTask(task: Task, req: Request) {
    const spec = this.specs.get(task.id)!;
    const id = task.agent_id;
    const t0 = Date.now();
    task.status = "running"; task.started_at = nowIso();
    this.bump(id, 1);
    this.emitTask("task.started", task);
    this.log(id, "task.started", tr("mock.act.started", { name: this.agent(id).name.split(" ")[0], task: task.title, reason: task.assigned_reason || "" }));
    this.setState(id, "thinking", "Entendiendo la tarea", task.id, 5);
    this.pushMetrics();
    await this.wait(800);

    const steps = Math.max(6, Math.round((spec.secs * 1000) / 500));
    let consulted = !spec.consult;
    try {
      for (let i = 1; i <= steps; i++) {
        const pct = Math.min(94, (i / steps) * 94);
        const phase = Math.min(spec.activities.length - 1, Math.floor((i / steps) * spec.activities.length));
        const reviewing = spec.reviewing && i > steps * 0.55;
        this.setState(id, reviewing ? "reviewing" : "working", reviewing ? `Revisando: ${spec.activities[phase]}` : spec.activities[phase], task.id, pct);
        if (!consulted && i >= steps * 0.4) {
          consulted = true;
          await this.doConsult(task, spec, id);
        }
        await this.wait(500);
      }
    } catch (e) { if (e instanceof Cancelled) return; throw e; }

    if (spec.fail) {
      task.status = "failed"; task.finished_at = nowIso(); this.bump(id, -1); this.errorsCount++;
      this.emit("error", { agent_id: id, message: spec.fail }, id);
      this.emitTask("task.failed", task);
      this.setState(id, "error", spec.fail, task.id, 60);
      this.log(id, "task.failed", `${task.title} falló: ${spec.fail}`);
      this.pushMetrics();
      await this.wait(6000);
      if (this.agent(id).state === "error") this.setState(id, "idle", "Disponible", null, 0);
      return;
    }

    if (spec.approval) {
      const ap: Approval = {
        id: uid("appr"), task_id: task.id, agent_id: id, action: spec.approval.action, title: spec.approval.title,
        details: spec.approval.details, risk: spec.approval.risk, status: "pending", created_at: nowIso(), resolved_at: null,
      };
      this.approvals.unshift(ap);
      task.status = "awaiting_approval";
      this.setState(id, "awaiting_approval", `Esperando aprobación: ${spec.approval.title}`, task.id, 100);
      this.emitTask("task.started", task); // status carries awaiting_approval; the spec has no dedicated event
      this.emit("approval.requested", { approval: { ...ap } }, id);
      this.log(id, "approval.requested", `Aprobación requerida: ${ap.title}`);
      this.chat.note("assistant", "user", "chat", tr("mock.task.approval", { title: ap.title }));
      this.pushMetrics();
      const decision = await new Promise<"approve" | "reject">((res) => this.decisions.set(ap.id, res));
      ap.status = decision === "approve" ? "approved" : "rejected"; ap.resolved_at = nowIso();
      this.emit("approval.resolved", { approval: { ...ap } }, id);
      this.log(id, "approval.resolved", `${ap.title}: ${decision === "approve" ? "aprobada" : "rechazada"}`);
      if (decision === "reject") {
        task.status = "blocked";
        this.bump(id, -1);
        this.emitTask("task.blocked", task);
        this.setState(id, "blocked", "Aprobación rechazada: tarea bloqueada", task.id, 100);
        this.pushMetrics();
        await this.wait(6000);
        if (this.agent(id).state === "blocked") this.setState(id, "idle", "Disponible", null, 0);
        return;
      }
      task.status = "running";
      this.setState(id, "working", "Enviando propuesta al cliente", task.id, 96);
      await this.wait(1400);
    }

    // finish
    const cost = +(0.03 + Math.random() * 0.09).toFixed(4);
    task.output = fullOutput(spec.out);
    task.status = "done"; task.finished_at = nowIso();
    this.bump(id, -1);
    const a = this.agent(id);
    const dur = (Date.now() - t0) / 1000;
    a.metrics.tasks_completed++;
    a.metrics.avg_seconds = Math.round(((a.metrics.avg_seconds * (a.metrics.tasks_completed - 1)) + dur) / a.metrics.tasks_completed);
    a.metrics.cost_usd = +(a.metrics.cost_usd + cost).toFixed(4);
    req.cost_usd = +(req.cost_usd + cost).toFixed(4);
    this.emitTask("task.completed", task);
    this.setState(id, "completed", "Tarea completada", task.id, 100);
    this.log(id, "task.completed", tr("mock.act.completed", { name: a.name.split(" ")[0], task: task.title }));
    if (id !== "assistant" && spec.key !== "direct") this.chat.note(id, "user", "chat", tr("mock.task.agentDone", { task: task.title }));
    this.pushMetrics();
    this.settle(id, 3200);
  }

  private async doConsult(task: Task, spec: TaskSpec, from: string) {
    const c = spec.consult!;
    const to = c.to;
    const prevTo = { ...this.agent(to) };
    const toBusy = prevTo.state === "talking";
    const conv: Conversation = {
      id: uid("conv"), title: `Consulta: ${this.agent(from).name.split(" ")[0]} → ${this.agent(to).name.split(" ")[0]}`,
      participants: [from, to], request_id: task.request_id, last_message_at: nowIso(),
    };
    this.conversations.unshift(conv);
    this.messages[conv.id] = [];
    this.setState(from, "talking", `Consultando a ${this.agent(to).name}`, task.id, this.agent(from).progress);
    this.sendMessage(conv, from, to, "consult", c.q, task.id);
    this.log(from, "message.sent", tr("mock.act.consult", { from: this.agent(from).name.split(" ")[0], to: this.agent(to).name.split(" ")[0], q: c.q }));
    this.chat.note(from, to, "consult", c.q);
    if (!toBusy) this.setState(to, "talking", `Atendiendo consulta de ${this.agent(from).name}`, prevTo.current_task_id, prevTo.progress);
    await this.wait(5200); // time for the asker to walk to the other desk
    this.sendMessage(conv, to, from, "answer", c.a, task.id);
    this.log(to, "message.sent", tr("mock.act.answered", { from: this.agent(from).name.split(" ")[0], to: this.agent(to).name.split(" ")[0] }));
    this.chat.note(to, from, "answer", c.a);
    await this.wait(3500);
    if (!toBusy) this.setState(to, prevTo.state === "talking" ? "idle" : prevTo.state, prevTo.activity, prevTo.current_task_id, prevTo.progress);
    this.setState(from, "working", spec.activities[spec.activities.length - 1], task.id, this.agent(from).progress);
  }

  // ---- REST ---------------------------------------------------------------------------------
  async handle(method: string, rawPath: string, body?: any): Promise<unknown> {
    await new Promise((r) => setTimeout(r, 40)); // latency
    const [path, qs] = rawPath.split("?");
    const q = new URLSearchParams(qs || "");
    const seg = path.split("/").filter(Boolean);
    const [r0, r1, r2] = seg;
    if (this.art.matches(r0)) return this.art.handle(method, seg, q, body);
    if (this.orgCfg.matches(seg)) return this.orgCfg.handle(method, seg, body, (text) => this.handle("POST", "/requests", { text }));
    if (this.conns.matches(r0, r2)) return this.conns.handle(method, seg, q, body);
    if (method === "GET") {
      if (r0 === "agents" && !r1) return this.agents;
      if (r0 === "agents" && r1 && r2 === "detail") {
        const agent = this.agent(r1);
        return {
          agent,
          tasks: this.tasks.filter((t) => t.agent_id === r1).slice().reverse().slice(0, 20),
          conversations: this.conversations.filter((c) => c.participants.includes(r1)),
          memory: SEED_MEMORY[r1] || [],
          recent_activity: this.activity.filter((a) => a.agent_id === r1).slice(0, 20),
        };
      }
      if (r0 === "agents" && r1) return this.agent(r1);
      if (r0 === "tasks" && !r1) {
        const aid = q.get("agent_id"), st = q.get("status");
        return this.tasks.filter((t) => (!aid || t.agent_id === aid) && (!st || t.status === st));
      }
      if (r0 === "tasks" && r1) return this.tasks.find((t) => t.id === r1);
      if (r0 === "requests" && !r1) return this.requests;
      if (r0 === "requests" && r1) {
        return { request: this.requests.find((r) => r.id === r1), tasks: this.tasks.filter((t) => t.request_id === r1) };
      }
      if (r0 === "conversations" && !r1) return this.conversations;
      if (r0 === "conversations" && r1 && r2 === "messages") {
        if (this.chat.matches(r1)) return this.chat.list(r1, q.get("before"), Number(q.get("limit") || 60));
        return this.messages[r1] || [];
      }
      if (r0 === "approvals") { const st = q.get("status"); return this.approvals.filter((a) => !st || a.status === st); }
      if (r0 === "reports" && !r1) return this.reports;
      if (r0 === "reports" && r1) return this.reports.find((r) => r.id === r1);
      if (r0 === "activity") return this.activity.slice(0, Number(q.get("limit") || 100));
      if (r0 === "metrics") return this.metrics();
      if (r0 === "healthz") return { status: "ok", mode: "simulation" };
    }
    if (method === "POST") {
      if (r0 === "messages" && !r1) return this.chat.post(String(body?.conversation || "office"), String(body?.text || "").trim());
      if (r0 === "requests") { const id = await this.startRequest(String(body?.text || "").trim()); return { request_id: id }; }
      if (r0 === "conversations" && r1 && r2 === "messages") {
        const conv = this.conversations.find((c) => c.id === r1);
        if (!conv) throw new Error("conversation not found");
        const target = conv.participants.find((p) => p !== "user") || "assistant";
        const text = String(body?.text || "");
        const m = this.sendMessage(conv, "user", target, "chat", text);
        this.log(null, "message.sent", `Intervención del usuario: ${text}`);
        this.replyToUser(conv, target).catch(() => {});
        return m;
      }
      if (r0 === "approvals" && r1 && r2 === "decision") {
        const res = this.decisions.get(r1);
        if (res) { this.decisions.delete(r1); res(body?.decision === "approve" ? "approve" : "reject"); }
        return { ok: true };
      }
      if (r0 === "demo" && r1 === "reset") {
        this.gen++;
        this.art.reset();
        this.conns.reset();
        this.decisions.clear();
        this.seed();
        this.emit("hello", { agents: this.agents });
        this.pushMetrics();
        return { ok: true };
      }
    }
    throw new Error(`mock: ${method} ${rawPath} not implemented`);
  }

  private async replyToUser(conv: Conversation, agentId: string) {
    await this.wait(1100);
    const a = this.agent(agentId);
    const prev = { state: a.state, activity: a.activity, task: a.current_task_id, progress: a.progress };
    const wasTalking = prev.state === "talking";
    if (!wasTalking) this.setState(agentId, "talking", "Respondiendo al usuario", prev.task, prev.progress);
    this.sendMessage(conv, agentId, "user", "answer", "Entendido. Incorporo tu indicación al trabajo en curso y te aviso si cambia el alcance.");
    await this.wait(2600);
    if (!wasTalking && this.agent(agentId).state === "talking") this.setState(agentId, prev.state, prev.activity, prev.task, prev.progress);
  }
}

export const mockBackend = new MockBackend();
