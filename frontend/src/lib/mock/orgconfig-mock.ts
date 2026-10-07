/**
 * Mock of the organization-config endpoints (NEXT_PUBLIC_MOCK=true): workflow templates, onboarding,
 * org settings / regional tone and schedules. Same shapes as docs/architecture/08-api.md section 11.
 * Template texts are Spanish only here (the real backend resolves es/en from the template data).
 */
import type { OnboardInput, OrgSettings, Schedule, ScheduleInput, TaxTemplate, TemplateStep, ToneCode, WorkflowTemplate } from "../orgConfigApi";

type StepSeed = [key: string, title: string, agent: string, deps: string[]];

function tpl(key: string, category: string, name: string, description: string, params: [string, string, boolean][], steps: StepSeed[]): WorkflowTemplate {
  const stage: Record<string, number> = {};
  const levels = (k: string): number => stage[k] ?? (stage[k] = 1 + Math.max(0, ...(steps.find((s) => s[0] === k)![3].map(levels))));
  const out: TemplateStep[] = steps.map(([k, title, agent, deps]) => ({ key: k, title_key: `workflow.${key}.step.${k}.title`, title, description_key: `workflow.${key}.step.${k}.desc`, description: title, agent_id: agent, depends_on: deps, stage: levels(k) }));
  const width: Record<number, number> = {};
  out.forEach((s) => { width[s.stage] = (width[s.stage] ?? 0) + 1; });
  return {
    key, version: 1, category, name_key: `workflow.${key}.tpl.name`, name, description_key: `workflow.${key}.tpl.description`, description,
    params: params.map(([k, label, required]) => ({ key: k, label_key: `workflow.${key}.param.${k}`, label, required, default: required ? "" : "por definir" })),
    steps: out, parallel_steps: out.filter((s) => width[s.stage] > 1).length,
  };
}

const TEMPLATES: WorkflowTemplate[] = [
  tpl("new_client", "sales", "Nuevo cliente", "Perfila al cliente, revisa contrato, facturación y capacidad en paralelo.", [["client", "Nombre del cliente", true]],
    [["profile", "Perfilar al cliente", "sales", []], ["contract", "Revisar contrato", "legal", ["profile"]], ["billing", "Configurar facturación", "accounting", ["profile"]], ["capacity", "Validar capacidad", "operations", ["profile"]], ["welcome", "Preparar bienvenida", "assistant", ["contract", "billing", "capacity"]]]),
  tpl("collections", "finance", "Cobranza", "Clasifica cuentas por cobrar, prioriza riesgo y redacta recordatorios.", [["scope", "Alcance", false]],
    [["aging", "Clasificar por antigüedad", "accounting", []], ["risk", "Priorizar por riesgo", "analyst", ["aging"]], ["reminders", "Redactar recordatorios", "sales", ["aging"]], ["plan", "Armar el plan de cobranza", "assistant", ["risk", "reminders"]]]),
  tpl("proposal", "sales", "Propuesta comercial", "Define alcance y valida precio, términos y capacidad en paralelo.", [["client", "Cliente", true], ["amount", "Monto aproximado", false]],
    [["scope", "Definir alcance", "sales", []], ["pricing", "Validar precio y margen", "accounting", ["scope"]], ["terms", "Revisar términos legales", "legal", ["scope"]], ["capacity", "Confirmar capacidad", "operations", ["scope"]], ["draft", "Redactar la propuesta", "sales", ["pricing", "terms", "capacity"]], ["review", "Revisión final", "assistant", ["draft"]]]),
  tpl("month_close", "finance", "Cierre de mes", "Concilia y elabora balance general y estado de resultados en paralelo.", [["month", "Mes a cerrar", true]],
    [["reconcile", "Conciliar movimientos", "accounting", []], ["balance_sheet", "Balance general", "accounting", ["reconcile"]], ["income_statement", "Estado de resultados", "analyst", ["reconcile"]], ["variance", "Variaciones", "analyst", ["income_statement"]], ["close_report", "Informe de cierre", "assistant", ["balance_sheet", "variance"]]]),
];

const PACKS = ["professional_services", "agency", "commerce", "general"].map((key, i) => ({
  key, version: 1, name_key: `pack.${key}.pack.name`, name: ["Despacho o consultoría", "Agencia creativa o de marketing", "Comercio o distribución", "Otro tipo de negocio"][i],
  description_key: `pack.${key}.pack.description`, description: "Agentes, memoria inicial y reglas de aprobación sembradas.",
  agents: ["assistant", "sales", "legal", "accounting", "analyst", "operations", "hr"].map((id) => ({ id, autonomy: "approve_each" })),
  memory: [{ agent_id: "assistant", scope: "org", key: "business_type", value_key: "x", value: "Negocio de ejemplo" }],
  rules: { approval_amount_usd: [5000, 3000, 2000, 1000][i], always_approve: ["send_proposal", "send_contract"] },
  templates: ["new_client", "proposal", "daily_briefing"], briefing: { hour: 8, minute: 0 },
}));

const TAX: TaxTemplate[] = [{
  key: "mx_invoice_example", country: "MX", example: true, name: "Facturación en México (estructura de ejemplo)",
  disclaimer: "Ejemplo, no asesoría fiscal. Valida tasas, documentos y plazos con tu contador.",
  fields: [{ key: "tax_id", label: "Identificador fiscal del cliente", kind: "id", example: "RFC" }],
  checklist: [{ key: "advisor", text: "Pedir a tu contador que revise la factura antes de emitirla." }],
}];

export class MockOrgConfig {
  private settings: OrgSettings = {
    configured: false, locale: "es", tone: "neutral", business_type: "", pack_version: 0, onboarding_completed: false, onboarding_completed_at: null,
    rules: { approval_amount_usd: 0, always_approve: [] }, agent_tones: {}, recommended_templates: [],
    available_tones: ["neutral", "mx", "co", "ar", "cl", "es"], available_locales: ["es", "en"],
  };
  private schedules: Schedule[] = [];
  private n = 0;

  matches(seg: string[]): boolean {
    const [r0, , r2] = seg;
    return ["workflow-templates", "onboarding", "schedules", "tax-templates"].includes(r0) || r0 === "org" || (r0 === "agents" && r2 === "tone");
  }

  private sched(i: ScheduleInput): Schedule {
    return { id: `sch-${++this.n}`, template_key: "daily_briefing", params: {}, hour: i.hour, minute: i.minute, timezone: i.timezone, weekdays: i.weekdays ?? [],
      enabled: i.enabled ?? true, next_run_at: new Date(Date.now() + 86_400_000).toISOString(), last_run_at: null, created_at: new Date().toISOString() };
  }

  async handle(method: string, seg: string[], body: any, submit: (text: string) => Promise<unknown>): Promise<unknown> {
    const [r0, r1, r2] = seg;
    if (r0 === "workflow-templates") {
      if (method === "GET") return TEMPLATES;
      const t = TEMPLATES.find((x) => x.key === r1);
      if (!t || r2 !== "instantiate") throw new Error("not found");
      const p: Record<string, string> = body?.params ?? {};
      if (t.params.some((x) => x.required && !p[x.key]?.trim())) throw new Error("POST -> 400");
      return submit(`${t.name}: ${Object.values(p).join(", ") || "mock"}`);
    }
    if (r0 === "onboarding") {
      if (method === "GET" && r1 === "packs") return PACKS;
      const b = body as OnboardInput;
      if (this.settings.onboarding_completed && !b.force) throw new Error("POST /onboarding -> 409");
      const pack = PACKS.find((x) => x.key === b.pack_key);
      if (!pack) throw new Error("POST -> 404");
      this.settings = { ...this.settings, configured: true, locale: b.locale ?? "es", tone: b.tone ?? "neutral", business_type: pack.key, pack_version: 1,
        onboarding_completed: true, onboarding_completed_at: new Date().toISOString(), rules: pack.rules, recommended_templates: pack.templates };
      this.schedules = [];
      if (b.briefing?.enabled) this.schedules.push(this.sched(b.briefing));
      return { settings: this.settings, agents_configured: pack.agents.map((a) => a.id), memory_seeded: pack.memory.length, schedule: this.schedules[0] ?? null };
    }
    if (r0 === "org") {
      if (method === "PUT") this.settings = { ...this.settings, configured: true, ...(body?.locale ? { locale: body.locale } : {}), ...(body?.tone ? { tone: body.tone as ToneCode } : {}) };
      return this.settings;
    }
    if (r0 === "agents") {
      const tones = { ...this.settings.agent_tones };
      if (body?.tone) tones[r1] = body.tone; else delete tones[r1];
      this.settings = { ...this.settings, agent_tones: tones };
      return this.settings;
    }
    if (r0 === "tax-templates") return TAX;
    // schedules
    if (method === "GET") return this.schedules;
    if (method === "POST") { const s = this.sched(body as ScheduleInput); this.schedules.push(s); return s; }
    if (method === "DELETE") { this.schedules = this.schedules.filter((s) => s.id !== r1); return {}; }
    this.schedules = this.schedules.map((s) => (s.id === r1 ? { ...s, ...(body as ScheduleInput), next_run_at: body?.enabled === false ? null : s.next_run_at } : s));
    return this.schedules.find((s) => s.id === r1);
  }
}
