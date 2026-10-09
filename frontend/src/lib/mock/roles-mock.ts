import type { Agent } from "../types";
import type { RoleTemplate } from "../api";

/** Mock of GET /role-templates and POST /agents/from-template (the wave A professions). Strings are locale-keyed; ids and shapes mirror the backend. */
type L = "es" | "en";
const T: { id: string; category: string; color: string; look: RoleTemplate["display"]["appearance"]; es: [string, string]; en: [string, string]; names: [string, string]; tools: string[] }[] = [
  { id: "project_manager", category: "coordination", color: "#6b7fd7", look: { skin: "#e3b38f", hair: "#3a2a1e", hair_style: "bob", accessory: "glasses", tie: false, female: true },
    es: ["Project manager", "Planifica proyectos, dependencias, ruta crítica y riesgos; da seguimiento y reporta avance."], en: ["Project manager", "Plans projects, dependencies, critical path and risks; tracks and reports progress."], names: ["Lucía Herrera", "Lucy Harper"], tools: ["artifacts", "calendar", "docs"] },
  { id: "education", category: "education", color: "#3fae9a", look: { skin: "#c98f68", hair: "#1c1410", hair_style: "short", accessory: "none", tie: false, female: false },
    es: ["Educación", "Prepara planes de clase, materiales y evaluaciones con apoyo pedagógico."], en: ["Education", "Prepares lesson plans, materials and assessments with pedagogical support."], names: ["Mateo Salas", "Matthew Sayer"], tools: ["docs"] },
  { id: "data_analyst", category: "analytics", color: "#d4823a", look: { skin: "#f0c8a4", hair: "#2b2b2b", hair_style: "ponytail", accessory: "roundglasses", tie: false, female: true },
    es: ["Analista de datos", "Explora datos, calcula métricas y propone visualizaciones."], en: ["Data analyst", "Explores data, computes metrics and proposes visualizations."], names: ["Camila Nuñez", "Camille Nash"], tools: ["sheets", "docs"] },
  { id: "software_engineer", category: "engineering", color: "#7a5bd1", look: { skin: "#edc29b", hair: "#16110d", hair_style: "short", accessory: "headphones", tie: false, female: false },
    es: ["Ingeniería de software", "Revisa código, redacta especificaciones y apoya el diseño técnico."], en: ["Software engineer", "Reviews code, drafts specs and supports technical design."], names: ["Diego Fuentes", "Dean Foster"], tools: ["github", "docs"] },
  { id: "finance_treasury", category: "finance", color: "#2b8a8f", look: { skin: "#e0b08c", hair: "#2a211b", hair_style: "bun", accessory: "roundglasses", tie: false, female: true },
    es: ["Finanzas y tesorería (solo análisis)", "Concilia extractos importados, revisa antigüedad de cuentas por cobrar y proyecta el flujo a 13 semanas. Nunca mueve dinero."], en: ["Finance and treasury (analysis only)", "Reconciles imported statements, reviews receivables aging and projects a 13-week cash flow. It never moves money."], names: ["Valeria Montoya", "Valerie Monroe"], tools: ["sheets", "docs"] },
  { id: "internal_auditor", category: "business", color: "#5b7fa6", look: { skin: "#d9a98a", hair: "#3a3a3d", hair_style: "short", accessory: "glasses", tie: true, female: false },
    es: ["Auditor interno", "Revisa entregables y cruza cifras entre tareas, citando la evidencia. Solo lectura."], en: ["Internal auditor", "Reviews deliverables and cross-checks figures between tasks, citing the evidence. Read-only."], names: ["Álvaro Beltrán", "Alan Brooks"], tools: [] },
];

export class MockRoles {
  private hired: Record<string, number> = {};
  reset() { this.hired = {}; }
  matches(method: string, seg: string[]) {
    return (method === "GET" && seg[0] === "role-templates" && !seg[1]) || (method === "POST" && seg[0] === "agents" && seg[1] === "from-template");
  }
  list(q: URLSearchParams): { items: RoleTemplate[] } {
    const l: L = q.get("locale") === "en" ? "en" : "es";
    return {
      items: T.map((x) => ({
        id: x.id, version: 1, category: x.category, risk_tier: "green", seed: false, title: x[l][0], description: x[l][1],
        responsibilities: [], disclaimers: [], out_of_scope: [], display: { color: x.color, appearance: x.look },
        tools: x.tools, autonomy: { default: "approve_each", ceiling: "autonomous" }, hired: this.hired[x.id] ?? 0,
      })),
    };
  }
  hire(body: { template_id?: string; name?: string; locale?: string }, agents: Agent[]): { agent: Agent; template_id: string } {
    const x = T.find((t) => t.id === body?.template_id);
    if (!x) throw new Error("POST /agents/from-template -> 404");
    const l: L = body.locale === "en" ? "en" : "es";
    const n = (this.hired[x.id] = (this.hired[x.id] ?? 0) + 1);
    const agent: Agent = {
      id: n === 1 ? x.id : `${x.id}-${n}`, name: body.name?.trim() || x.names[l === "en" ? 1 : 0], role: x.id, title: x[l][0], description: x[l][1],
      state: "idle", activity: l === "en" ? "Available" : "Disponible", current_task_id: null, progress: 0, tools: x.tools, permissions: ["read:all"],
      autonomy: "approve_each", metrics: { tasks_completed: 0, tasks_pending: 0, avg_seconds: 0, cost_usd: 0 },
    };
    agents.push(agent);
    return { agent, template_id: x.id };
  }
}
