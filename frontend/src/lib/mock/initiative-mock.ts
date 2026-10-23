import type { Initiative, InitiativeSettings, SignalSettings } from "../initiativeApi";
import type { MetricSignal, SIGNAL_FIELDS } from "../initiativeLogic";

/** The ranges of initiativeLogic.ts, passed in (type-only import keeps this file runnable under node --test). */
export type SignalSpec = { signals: MetricSignal[]; fields: typeof SIGNAL_FIELDS };

/**
 * Proactive suggestions in mock mode (NEXT_PUBLIC_MOCK=true): one pending suggestion per metric signal, built the
 * way backend/internal/application/initiative_signals.go builds them (signal + params; Spanish fallback text).
 * Accepting submits the stored prompt as a normal mock request; nothing happens on its own.
 */

const defaults = (): SignalSettings => ({
  metric_change: { enabled: true, change_pct: 10, margin_points: 3 },
  spend_pace: { enabled: true, pace_pct: 100, min_days: 3 },
  approval_aging: { enabled: true, min_age_hours: 24, min_count: 1 },
  quality_drop: { enabled: true, drop_points: 10 },
});

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();

function seed(): Initiative[] {
  const month = new Date().toISOString().slice(0, 7);
  const mk = (id: string, agent_id: string, signal: string, signal_key: string, min: number, title: string, body: string, prompt: string,
    params: Record<string, string>): Initiative => ({ id, agent_id, signal, signal_key, title, body, prompt, params, status: "pending", created_at: ago(min) });
  return [
    mk("ini-metric", "accounting", "metric_change", "art-margin:s1!d4", 12, "«Margen» bajó 7.0 puntos en «Margen Acme - conciliación»",
      "Septiembre: pasó de 31.0% a 24.0% frente al periodo anterior. ¿Lo reviso?",
      "Revisa por qué «Margen» pasó de 31.0% a 24.0% (Septiembre) en «Margen Acme - conciliación». Busca las causas en los datos disponibles y propón qué hacer. No modifiques el archivo ni apruebes nada: entrega un análisis.",
      { artifact: "Margen Acme - conciliación", column: "Margen", period: "Septiembre", previous: "31.0%", current: "24.0%", change: "7.0", direction: "down", unit: "points", metric: "margin" }),
    mk("ini-spend", "accounting", "spend_pace", `org:${month}`, 35, "El gasto de IA va camino de superar lo disponible este mes",
      "Llevamos US$ 18.40 este mes; a este ritmo serían US$ 41.20 y el mes tenía US$ 35.00 disponibles. ¿Reviso en qué se va?",
      "Analiza el gasto de IA de este mes (US$ 18.40; proyección US$ 41.20 frente a US$ 35.00 disponibles): qué empleados y tareas concentran el costo y qué se puede ajustar. No cambies topes ni configuraciones: entrega una propuesta.",
      { spent: "18.40", projected: "41.20", available: "35.00", pace: "118", month }),
    mk("ini-approvals", "assistant", "approval_aging", "ap-demo-old", 50, "Hay aprobaciones esperando: 2 con más de 24 h",
      "La más antigua es «Enviar propuesta a Acme» (31 h). ¿Preparo un resumen para que decidas?",
      "Prepara un resumen breve de estas aprobaciones pendientes para que una persona decida: qué pide cada una, el riesgo y qué pasa si sigue esperando. No apruebes ni rechaces nada.\n- «Enviar propuesta a Acme» (high, 31 h)\n- «Contratar proveedor de logística» (medium, 26 h)",
      { count: "2", hours: "24", oldest: "Enviar propuesta a Acme", age: "31" }),
    mk("ini-quality", "sales", "quality_drop", "sales", 90, "El puntaje de calidad de Valeria Ríos bajó 15 puntos",
      "Pasó de 86 a 71 frente a la ventana anterior. ¿Reviso qué cambió?",
      "Revisa por qué bajó el puntaje de calidad de Valeria Ríos (de 86 a 71): aprobaciones rechazadas, tareas fallidas y revisiones recientes. Propón cómo mejorar; no cambies configuraciones.",
      { agent: "Valeria Ríos", previous: "86", current: "71", drop: "15" }),
  ];
}

export class MockInitiatives {
  private items: Initiative[] = seed();
  private settings: InitiativeSettings = { enabled: true, daily_limit: 3, monthly_limit: 30, signals: defaults() };

  private submit: (text: string) => Promise<string>;
  private spec: SignalSpec;

  constructor(submit: (text: string) => Promise<string>, spec: SignalSpec) {
    this.submit = submit;
    this.spec = spec;
  }

  reset() { this.items = seed(); this.settings = { enabled: true, daily_limit: 3, monthly_limit: 30, signals: defaults() }; }

  matches(seg: string[]) { return seg[0] === "initiatives" || (seg[0] === "settings" && seg[1] === "initiative"); }

  async handle(method: string, seg: string[], q: URLSearchParams, body?: any): Promise<unknown> {
    const [r0, r1, r2] = seg;
    if (r0 === "settings") {
      if (method === "GET") return structuredClone(this.settings);
      if (method === "PUT") return this.put(body || {});
    }
    if (r0 === "initiatives" && method === "GET" && !r1) {
      const st = q.get("status") || "";
      return this.items.filter((i) => !st || i.status === st).map((i) => structuredClone(i));
    }
    if (r0 === "initiatives" && method === "POST" && r1 === "scan") return { results: [], raised: 0 }; // the demo already shows one of each
    if (r0 === "initiatives" && method === "POST" && r1 && (r2 === "accept" || r2 === "dismiss")) {
      const it = this.items.find((i) => i.id === r1);
      if (!it) throw new Error("not found");
      if (it.status !== "pending") throw new Error("conflict");
      it.status = r2 === "accept" ? "accepted" : "dismissed";
      it.resolved_at = new Date().toISOString();
      it.resolved_by = "demo";
      if (r2 === "accept") {
        try { it.request_id = await this.submit(it.prompt || it.title); } catch (e) { it.status = "pending"; delete it.resolved_at; throw e; }
      }
      return structuredClone(it);
    }
    throw new Error(`mock: ${method} /${seg.join("/")} not implemented`);
  }

  private put(body: Partial<InitiativeSettings>): InitiativeSettings {
    const lim = (n: unknown) => typeof n === "number" && Number.isInteger(n) && n >= 0 && n <= 1000;
    if (!lim(body.daily_limit) || !lim(body.monthly_limit)) throw new Error("invalid limits");
    const signals = structuredClone(this.settings.signals ?? defaults()) as SignalSettings;
    for (const s of this.spec.signals) {
      const patch = (body.signals as Partial<SignalSettings> | undefined)?.[s];
      if (patch) Object.assign(signals[s], patch);
      for (const f of this.spec.fields[s]) {
        const v = (signals[s] as unknown as Record<string, number>)[f.field];
        if (typeof v !== "number" || v < f.min || v > f.max) throw new Error(`invalid ${s}.${f.field}`);
      }
    }
    this.settings = { enabled: !!body.enabled, daily_limit: body.daily_limit as number, monthly_limit: body.monthly_limit as number,
      agent_daily: body.agent_daily, signals };
    return structuredClone(this.settings);
  }
}
