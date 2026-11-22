import type { TeamCard, TeamInstallResult, TeamPreview, TeamPreviewAgent } from "../teamsCatalogLogic";

/** Demo mode of the ready-to-hire teams (the real data lives in backend/internal/catalog/data/teams). Two languages,
 *  five teams; "hiring" only remembers the roles so the preview flips to "ya tienes". */

type Loc = "es" | "en";
interface MockTeam {
  key: string; packs: string[]; agents: string[]; week: [string, number][];
  t: Record<Loc, { name: string; for: string; solves: string; department: string }>;
}

const ROLE: Record<string, Record<Loc, [string, string]>> = {
  accounting: { es: ["Contabilidad", "Concilia bancos, registra lo pendiente y prepara el cierre."], en: ["Accounting", "Reconciles banks, records what is pending and prepares the close."] },
  finance_treasury: { es: ["Tesorería", "Vigila el flujo de caja y los pagos por hacer."], en: ["Treasury", "Watches cash flow and payments due."] },
  internal_auditor: { es: ["Auditoría interna", "Revisa que los gastos tengan respaldo."], en: ["Internal audit", "Checks that expenses have support."] },
  analyst: { es: ["Análisis", "Compara el mes con el anterior y explica las diferencias."], en: ["Analysis", "Compares the month with the last one and explains the differences."] },
  sales: { es: ["Ventas", "Prepara propuestas y da seguimiento al cliente."], en: ["Sales", "Prepares proposals and follows up with the customer."] },
  assistant: { es: ["Asistente", "Reúne cada mañana lo que vence hoy."], en: ["Assistant", "Gathers every morning what is due today."] },
  legal: { es: ["Legal", "Revisa condiciones y contratos antes de enviarlos."], en: ["Legal", "Reviews terms and contracts before they go out."] },
  operations: { es: ["Operaciones", "Confirma que lo prometido se puede entregar."], en: ["Operations", "Confirms that what is promised can be delivered."] },
  project_manager: { es: ["Gestión de proyectos", "Convierte cada brief en un plan con fechas."], en: ["Project manager", "Turns each brief into a plan with dates."] },
};
const FLOW: Record<string, Record<Loc, string>> = {
  month_close: { es: "Cierre de mes", en: "Month close" }, expense_audit: { es: "Auditoría de gastos", en: "Expense audit" },
  budget_planning: { es: "Presupuesto", en: "Budget planning" }, collections: { es: "Cobranza", en: "Collections" },
  customer_complaint: { es: "Reclamo de cliente", en: "Customer complaint" }, daily_briefing: { es: "Resumen diario", en: "Daily briefing" },
  new_client: { es: "Cliente nuevo", en: "New client" }, proposal: { es: "Propuesta", en: "Proposal" },
  contract_review: { es: "Revisión de contrato", en: "Contract review" }, marketing_campaign: { es: "Campaña", en: "Campaign" },
  supplier_selection: { es: "Elegir proveedor", en: "Supplier selection" }, quarterly_review: { es: "Revisión trimestral", en: "Quarterly review" },
};

const TEAMS: MockTeam[] = [
  { key: "agency", packs: ["agency"], agents: ["project_manager", "sales", "accounting", "assistant"], week: [["new_client", 1], ["proposal", 2], ["marketing_campaign", 3], ["collections", 5]],
    t: { es: { name: "Agencia de servicios", for: "Agencias y estudios que venden proyectos y retainers.", solves: "Llevar cada cliente del brief a la entrega y a la factura sin perder fechas.", department: "Proyectos" },
      en: { name: "Service agency", for: "Agencies and studios that sell projects and retainers.", solves: "Take each client from brief to delivery to invoice without missing dates.", department: "Projects" } } },
  { key: "collections", packs: ["commerce", "general"], agents: ["accounting", "sales", "assistant"], week: [["collections", 1], ["customer_complaint", 3], ["daily_briefing", 5]],
    t: { es: { name: "Cobranza", for: "Negocios con clientes que pagan tarde.", solves: "Saber quién te debe y recordárselo a tiempo cuidando la relación.", department: "Cobranza" },
      en: { name: "Collections", for: "Businesses whose customers pay late.", solves: "Know who owes you and remind them in time while keeping the relationship.", department: "Collections" } } },
  { key: "month_close", packs: ["professional_services", "general"], agents: ["accounting", "finance_treasury", "internal_auditor", "analyst"], week: [["month_close", 1], ["expense_audit", 3], ["budget_planning", 5]],
    t: { es: { name: "Cierre contable del mes", for: "Negocios que cierran su mes con hojas sueltas.", solves: "Cerrar el mes con cuentas conciliadas y un resumen claro.", department: "Contabilidad" },
      en: { name: "Month-end close", for: "Businesses that close the month with loose spreadsheets.", solves: "Close the month with reconciled accounts and a clear summary.", department: "Accounting" } } },
  { key: "online_store", packs: ["commerce"], agents: ["operations", "sales", "accounting", "analyst"], week: [["daily_briefing", 1], ["supplier_selection", 2], ["customer_complaint", 3], ["quarterly_review", 5]],
    t: { es: { name: "Tienda online", for: "Tiendas que venden por internet.", solves: "Mantener el inventario al día y atender los reclamos.", department: "Tienda online" },
      en: { name: "Online store", for: "Stores that sell online.", solves: "Keep inventory up to date and handle complaints.", department: "Online store" } } },
  { key: "proposal", packs: ["professional_services"], agents: ["sales", "legal", "operations"], week: [["new_client", 1], ["proposal", 2], ["contract_review", 4]],
    t: { es: { name: "Propuesta comercial", for: "Equipos que cotizan a mano.", solves: "Pasar de la primera conversación a una propuesta revisada.", department: "Ventas" },
      en: { name: "Sales proposal", for: "Teams that quote by hand.", solves: "Go from the first conversation to a reviewed proposal.", department: "Sales" } } },
];

/** roles the demo "hired" during this session, on top of the demo's own agents */
const hired = new Set<string>();
const DEMO_HAS = ["assistant", "sales", "accounting", "operations", "legal", "analyst"];

const have = (role: string) => hired.has(role) || DEMO_HAS.includes(role);

function preview(t: MockTeam, loc: Loc): TeamPreview {
  const agents: TeamPreviewAgent[] = t.agents.map((role) => {
    const [title, does] = ROLE[role][loc];
    const there = have(role);
    return { role, title, does, status: there ? "existing" : "new", name: there ? "" : title, autonomy: "approve_each", skills: [],
      ...(there ? { existing: { id: role, name: title, title } } : {}) };
  });
  const newCount = agents.filter((a) => a.status === "new").length;
  return {
    team: { key: t.key, version: 1, packs: t.packs, ...t.t[loc] },
    destination: { office_id: "general", office_name: "General", suggested_office_id: "" },
    agents, new_count: newCount, existing_count: agents.length - newCount, skills: [],
    week: t.week.map(([template, day]) => ({ template, day, why: "", name: FLOW[template][loc], description: "" })),
    can_install: newCount > 0, blocked_reason: newCount > 0 ? "" : "nothing_new",
  };
}

export const teamsCatalogMock = {
  list(loc: Loc, pack?: string): TeamCard[] {
    return TEAMS.filter((t) => !pack || t.packs.includes(pack)).map((t) => {
      const p = preview(t, loc);
      return { key: t.key, version: 1, name: p.team.name, for: p.team.for, solves: p.team.solves, department: p.team.department, packs: t.packs,
        agents: p.agents.map((a) => ({ role: a.role, title: a.title })), templates: t.week.length, new_count: p.new_count, existing_count: p.existing_count };
    });
  },
  get(key: string, loc: Loc): TeamPreview {
    const t = TEAMS.find((x) => x.key === key);
    if (!t) throw new Error(`GET /teams/${key} -> 404`);
    return preview(t, loc);
  },
  install(key: string, loc: Loc): TeamInstallResult {
    const before = this.get(key, loc);
    const created = before.agents.filter((a) => a.status === "new").map((a) => ({ id: a.role, name: a.title, role: a.role }));
    created.forEach((c) => hired.add(c.role));
    return { created, preview: this.get(key, loc) };
  },
};
