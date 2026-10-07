/**
 * Mock of the artifact endpoints and `artifact.*` events (docs/architecture/agent-workspaces.md sec. 5.4/5.5).
 * Used only by the mock engine (NEXT_PUBLIC_MOCK=true) until the Go backend ships these endpoints.
 * Visible demo texts are generated per locale (like the spec's localizable templates) and stored as plain strings.
 */
import { getLocale } from "../i18n-core";
import type {
  Artifact, ArtifactKind, ArtifactLink, ArtifactMeta, ArtifactTemplate, ArtifactVersionInfo, BuildState, ProjectWorkspace,
} from "../artifacts";
import { ARTIFACT_KINDS, ROLE_SUGGESTED } from "../artifacts";
import type { Task } from "../types";

type Emit = (type: string, payload: unknown, agentId?: string) => void;
type Wait = (ms: number) => Promise<void>;
const L = (es: string, en: string) => (getLocale() === "en" ? en : es);
const nowIso = () => new Date().toISOString();
let seq = 0;
const uid = (p: string) => `${p}_${Date.now().toString(36)}${(seq++).toString(36)}`;
const clone = <T,>(x: T): T => JSON.parse(JSON.stringify(x));

// ---- content builders -----------------------------------------------------------------------------------
interface Row { a: string; v?: number; f?: string; fmt?: string }
const MONEY = "$#,##0";
const sheetOf = (name: string, rows: Row[], k = rows.length, imports?: { alias: string; artifact_id: string }[]) => {
  const cells: Record<string, any> = { A1: { v: L("Concepto", "Item") }, B1: { v: L("Monto", "Amount") } };
  rows.slice(0, k).forEach((r, i) => {
    cells[`A${i + 2}`] = { v: r.a };
    if (r.f) cells[`B${i + 2}`] = { f: r.f, v: r.v ?? 0, fmt: r.fmt ?? MONEY };
    else if (r.v !== undefined) cells[`B${i + 2}`] = { v: r.v, fmt: r.fmt ?? MONEY };
  });
  return { schema: "aiw.sheet/1", sheets: [{ id: "s1", name, rows: 40, cols: 8, cells, colw: { A: 220, B: 130 } }], ...(imports ? { imports: imports.map((i) => ({ ...i, pinned_version: null })) } : {}) };
};
const incomeRows = (): Row[] => [
  { a: L("Ingresos", "Revenue"), v: 1200000 }, { a: L("Costo de ventas", "Cost of sales"), v: 700000 },
  { a: L("Utilidad bruta", "Gross profit"), f: "=B2-B3", v: 500000 },
  { a: L("Sueldos", "Salaries"), v: 220000 }, { a: L("Renta", "Rent"), v: 60000 }, { a: L("Otros gastos", "Other expenses"), v: 45000 },
  { a: L("Gastos operativos", "Operating expenses"), f: "=SUM(B5:B7)", v: 325000 },
  { a: L("Utilidad operativa", "Operating income"), f: "=B4-B8", v: 175000 },
  { a: L("Intereses", "Interest"), v: 15000 },
  { a: L("Impuestos", "Taxes"), f: "=ROUND((B9-B10)*0.3,0)", v: 48000 },
  { a: L("Utilidad neta", "Net income"), f: "=B9-B10-B11", v: 112000 },
];
const balanceRows = (): Row[] => [
  { a: L("Efectivo", "Cash"), v: 962000 }, { a: L("Cuentas por cobrar", "Receivables"), v: 430000 }, { a: L("Inventario", "Inventory"), v: 300000 },
  { a: L("Total activos", "Total assets"), f: "=SUM(B2:B4)", v: 1692000 },
  { a: L("Cuentas por pagar", "Payables"), v: 380000 }, { a: L("Deuda", "Debt"), v: 600000 }, { a: L("Capital", "Capital"), v: 600000 },
  { a: L("Utilidad del periodo (del estado de resultados)", "Period earnings (from income statement)"), f: '=AIW_REF("IS","Result!B12")', v: 112000 },
  { a: L("Pasivo + capital", "Liabilities + equity"), f: "=B6+B7+B8+B9", v: 1692000 },
];
const cashRows = (): Row[] => [
  { a: L("Flujo operativo", "Operating cash flow"), v: 240000 }, { a: L("Flujo de inversión", "Investing cash flow"), v: -80000 },
  { a: L("Flujo de financiamiento", "Financing cash flow"), v: -30000 }, { a: L("Cambio neto", "Net change"), f: "=SUM(B2:B4)", v: 130000 },
  { a: L("Saldo inicial", "Opening balance"), v: 832000 }, { a: L("Saldo final", "Closing balance"), f: "=B5+B6", v: 962000 },
];
const para = (bid: string, text: string, marks?: any[]) => ({ type: "paragraph", attrs: { bid }, content: [{ type: "text", text, ...(marks ? { marks } : {}) }] });
const head = (bid: string, level: number, text: string) => ({ type: "heading", attrs: { bid, level }, content: [{ type: "text", text }] });
const docOf = (nodes: any[]) => ({ schema: "aiw.doc/1", doc: { type: "doc", content: nodes } });
const linkMark = (artifact_id: string, anchor?: any) => [{ type: "artifact_link", attrs: { artifact_id, anchor: anchor ?? null, pinned_version: null } }];
const tableOf = (cols: [string, string, string?, string[]?][], rows: Record<string, string | number | boolean>[]) => ({
  schema: "aiw.table/1",
  columns: cols.map(([key, label, type, options]) => ({ key, label, type: type ?? "text", ...(options ? { options } : {}) })),
  rows: rows.map((cells, i) => ({ id: `r${i + 1}`, cells })),
});

function blank(kind: ArtifactKind): any {
  switch (kind) {
    case "sheet": return { schema: "aiw.sheet/1", sheets: [{ id: "s1", name: L("Hoja 1", "Sheet 1"), rows: 40, cols: 8, cells: {} }] };
    case "doc": return docOf([head("b1", 1, L("Documento sin título", "Untitled document")), para("b2", "")]);
    case "table": return tableOf([["name", L("Nombre", "Name")], ["note", L("Nota", "Note")]], [{ name: "", note: "" }]);
    case "board": return { schema: "aiw.board/1", view: "kanban", columns: [{ id: "todo", title: L("Por hacer", "To do") }, { id: "doing", title: L("En curso", "Doing") }, { id: "done", title: L("Hecho", "Done") }], cards: [] };
    case "chart": return { schema: "aiw.chart/1", title: L("Gráfica", "Chart"), mark: "bar", source: { inline: { columns: ["x", "y"], rows: [["A", 3], ["B", 5], ["C", 2]] } }, encoding: { x: "x", y: ["y"] } };
    case "pdf": return { schema: "aiw.pdf/1", blob_id: null, pages: 3, annotations: [] };
    case "form": return { schema: "aiw.form/1", fields: [{ key: "note", type: "textarea", label: L("Comentario", "Comment"), required: false }], answers: null, answered_by: null };
    case "inbox": return { schema: "aiw.inbox/1", items: [] };
    case "agenda": return { schema: "aiw.agenda/1", events: [] };
  }
}

interface Tpl { id: string; kind: ArtifactKind; suggested_for: string[]; title: () => { es: string; en: string }; build: () => any }
const day = (d: number, h: number, m = 0) => { const x = new Date(); x.setDate(x.getDate() + d); x.setHours(h, m, 0, 0); return x.toISOString(); };
const TEMPLATES: Tpl[] = [
  { id: "bank_reconciliation", kind: "sheet", suggested_for: ["accounting"], title: () => ({ es: "Conciliación bancaria", en: "Bank reconciliation" }),
    build: () => sheetOf(L("Conciliación", "Reconciliation"), [{ a: L("Saldo según banco", "Bank balance"), v: 50000 }, { a: L("Depósitos en tránsito", "Deposits in transit"), v: 4200 }, { a: L("Cheques pendientes", "Outstanding checks"), v: -1800 }, { a: L("Saldo conciliado", "Reconciled balance"), f: "=SUM(B2:B4)", v: 52400 }]) },
  { id: "income_statement", kind: "sheet", suggested_for: ["accounting"], title: () => ({ es: "Estado de resultados", en: "Income statement" }), build: () => sheetOf("Result", incomeRows()) },
  { id: "balance_sheet", kind: "sheet", suggested_for: ["accounting"], title: () => ({ es: "Balance general", en: "Balance sheet" }), build: () => sheetOf("Balance", balanceRows()) },
  { id: "cash_flow", kind: "sheet", suggested_for: ["accounting"], title: () => ({ es: "Flujo de efectivo", en: "Cash flow" }), build: () => sheetOf("Cash", cashRows()) },
  { id: "accounting_policies", kind: "doc", suggested_for: ["accounting"], title: () => ({ es: "Políticas contables", en: "Accounting policies" }),
    build: () => docOf([head("b1", 1, L("Políticas contables", "Accounting policies")), para("b2", L("Los estados se preparan con base en devengado.", "Statements are prepared on an accrual basis."))]) },
  { id: "service_contract", kind: "doc", suggested_for: ["legal"], title: () => ({ es: "Contrato de servicios", en: "Service contract" }),
    build: () => docOf([head("b1", 1, L("Contrato de servicios", "Service agreement")), para("b2", L("Las partes acuerdan lo siguiente.", "The parties agree to the following.")), para("b3", L("7. Penalidades: la penalidad será del 10% del monto mensual.", "7. Penalties: the penalty will be 10% of the monthly amount."))]) },
  { id: "commercial_proposal", kind: "doc", suggested_for: ["sales"], title: () => ({ es: "Propuesta comercial", en: "Commercial proposal" }),
    build: () => docOf([head("b1", 1, L("Propuesta comercial", "Commercial proposal")), para("b2", L("Alcance, inversión y calendario.", "Scope, investment and timeline."))]) },
  { id: "risk_matrix", kind: "table", suggested_for: ["legal"], title: () => ({ es: "Matriz de riesgos", en: "Risk matrix" }),
    build: () => tableOf([["risk", L("Riesgo", "Risk")], ["sev", L("Severidad", "Severity"), "select", ["Alta", "Media", "Baja"]]], [{ risk: L("Penalidad desproporcionada", "Disproportionate penalty"), sev: "Alta" }]) },
  { id: "sales_pipeline", kind: "board", suggested_for: ["sales"], title: () => ({ es: "Pipeline de ventas", en: "Sales pipeline" }), build: () => ({ ...blank("board"), cards: [{ id: "k1", col: "doing", title: "Acme", labels: [], assignee: { kind: "agent", id: "sales" } }] }) },
  { id: "ops_checklist", kind: "board", suggested_for: ["operations"], title: () => ({ es: "Checklist de operación", en: "Operations checklist" }),
    build: () => ({ schema: "aiw.board/1", view: "checklist", columns: [{ id: "todo", title: L("Pendientes", "Open") }], cards: [{ id: "k1", col: "todo", title: L("Contratar 2 personas", "Hire 2 people"), checklist: [{ t: L("Perfil", "Profile"), done: true }, { t: L("Entrevistas", "Interviews"), done: false }] }] }) },
  { id: "kpi_dashboard", kind: "chart", suggested_for: ["analyst"], title: () => ({ es: "Tablero de KPIs", en: "KPI dashboard" }), build: () => blank("chart") },
  { id: "daily_inbox", kind: "inbox", suggested_for: ["assistant"], title: () => ({ es: "Bandeja del día", en: "Daily inbox" }), build: () => blank("inbox") },
  { id: "weekly_agenda", kind: "agenda", suggested_for: ["assistant", "operations"], title: () => ({ es: "Agenda semanal", en: "Weekly agenda" }), build: () => blank("agenda") },
  { id: "hiring_pipeline", kind: "table", suggested_for: ["hr"], title: () => ({ es: "Pipeline de contratación", en: "Hiring pipeline" }),
    build: () => tableOf([["name", L("Candidato", "Candidate")], ["stage", L("Etapa", "Stage"), "select", ["Nuevo", "Entrevista", "Oferta"]]], [{ name: "Ana", stage: "Nuevo" }]) },
];
const DEFAULT_TEMPLATE: Record<string, string> = {
  accounting: "bank_reconciliation", legal: "service_contract", sales: "commercial_proposal", analyst: "kpi_dashboard",
  operations: "ops_checklist", assistant: "daily_inbox", hr: "hiring_pipeline",
};

// ---- store ---------------------------------------------------------------------------------------------
interface Item { meta: ArtifactMeta; content: any; versions: { info: ArtifactVersionInfo; content: any }[] }
const PROJECT_ID = "proj_close";

export class MockArtifacts {
  private items = new Map<string, Item>();
  private links: ArtifactLink[] = [];
  private seeded = false;
  private gen = 0;
  private building = new Map<string, string>(); // task id -> artifact id

  constructor(private emit: Emit, private wait: Wait) {}

  matches(r0?: string) { return r0 === "artifacts" || r0 === "artifact-kinds" || r0 === "artifact-templates" || r0 === "projects" || r0 === "workspaces"; }
  reset() { this.gen++; this.items.clear(); this.links = []; this.seeded = false; this.building.clear(); }

  private mk(p: Partial<ArtifactMeta> & { id: string; kind: ArtifactKind; title: string }, content: any): Item {
    const t = nowIso();
    const meta: ArtifactMeta = {
      schema_version: `aiw.${p.kind}/1`, status: "draft", head_version: 1, created_by: { kind: "system", id: "seed" }, last_author: { kind: "system", id: "seed" },
      progress: 100, build_state: "done", locale: getLocale(), depends_on_artifacts: [], pending_proposals: 0, tainted: false, locked: false,
      size_bytes: JSON.stringify(content).length, attachments: [], created_at: t, updated_at: t, ...p,
    };
    const item: Item = { meta, content, versions: [{ info: { artifact_id: meta.id, version: 1, base_version: null, author: meta.created_by, source: "create", summary: L("Versión inicial", "Initial version"), created_at: t }, content: clone(content) }] };
    this.items.set(meta.id, item);
    return item;
  }
  private agentOwned(agent: string) { return { created_by: { kind: "agent" as const, id: agent }, last_author: { kind: "agent" as const, id: agent }, attachments: [{ agent_id: agent, mode: "edit" as const }] }; }

  private ensureSeeded() {
    if (this.seeded) return;
    this.seeded = true;
    const acc = this.agentOwned("accounting");
    const pr = { project_id: PROJECT_ID };
    this.mk({ id: "art_demo_income", kind: "sheet", title: L("Estado de resultados", "Income statement"), ...acc, ...pr, deliverable_id: "dlv_income", progress: 20, build_state: "building" }, sheetOf("Result", incomeRows(), 2));
    this.mk({ id: "art_demo_balance", kind: "sheet", title: L("Balance general", "Balance sheet"), ...acc, ...pr, deliverable_id: "dlv_balance", progress: 10, build_state: "building", depends_on_artifacts: ["art_demo_income"] },
      sheetOf("Balance", balanceRows(), 3, [{ alias: "IS", artifact_id: "art_demo_income" }]));
    this.mk({ id: "art_demo_cashflow", kind: "sheet", title: L("Flujo de efectivo", "Cash flow"), ...acc, ...pr, deliverable_id: "dlv_cashflow", progress: 15, build_state: "building" }, sheetOf("Cash", cashRows(), 1));
    this.mk({ id: "art_demo_policies", kind: "doc", title: L("Políticas contables", "Accounting policies"), ...acc, ...pr, deliverable_id: "dlv_policies", depends_on_artifacts: ["art_demo_balance"] }, docOf([
      head("b1", 1, L("Políticas contables", "Accounting policies")),
      para("b2", L("Los estados financieros se preparan con base en devengado.", "Financial statements are prepared on an accrual basis.")),
      { type: "paragraph", attrs: { bid: "b3" }, content: [{ type: "text", text: L("Ver ", "See ") }, { type: "text", text: L("balance general", "balance sheet"), marks: linkMark("art_demo_balance", { sheet: "Balance", range: "B9:B10" }) }, { type: "text", text: L(" para la utilidad del periodo.", " for the period earnings.") }] },
      { type: "embed", attrs: { bid: "b4", artifact_id: "art_demo_chart", view: { height: 220 }, pinned_version: null } },
    ]));
    this.mk({ id: "art_demo_chart", kind: "chart", title: L("Estado de resultados por concepto", "Income statement by item"), created_by: { kind: "agent", id: "analyst" }, last_author: { kind: "agent", id: "analyst" }, attachments: [{ agent_id: "analyst", mode: "edit" }, { agent_id: "accounting", mode: "read" }], ...pr, deliverable_id: "dlv_policies" },
      { schema: "aiw.chart/1", title: L("Resultados", "Results"), mark: "bar", source: { artifact_id: "art_demo_income", sheet: "Result", range: "A1:B12" }, encoding: { x: "A", y: ["B"] }, format: { y: MONEY } });
    this.links = [
      { id: "lnk_1", from: "art_demo_balance", to: "art_demo_income", relation: "source_of", alias: "IS", anchor: { sheet: "Result", range: "B12" } },
      { id: "lnk_2", from: "art_demo_policies", to: "art_demo_balance", relation: "refers_to", anchor: { sheet: "Balance", range: "B9:B10" } },
      { id: "lnk_3", from: "art_demo_policies", to: "art_demo_chart", relation: "embeds" },
      { id: "lnk_4", from: "art_demo_chart", to: "art_demo_income", relation: "source_of", anchor: { sheet: "Result", range: "A1:B12" } },
    ];
    // one artifact of every other kind so each viewer is reachable from a desk
    const legal = this.agentOwned("legal");
    this.mk({ id: "art_demo_contract", kind: "doc", title: L("Contrato marco", "Master agreement"), ...legal, status: "in_review" }, TEMPLATES.find((t) => t.id === "service_contract")!.build());
    this.mk({ id: "art_demo_risks", kind: "table", title: L("Matriz de riesgos", "Risk matrix"), ...legal }, TEMPLATES.find((t) => t.id === "risk_matrix")!.build());
    this.mk({ id: "art_demo_pdf", kind: "pdf", title: L("NDA firmado (PDF)", "Signed NDA (PDF)"), ...legal, attachments: [{ agent_id: "legal", mode: "read" }] },
      { schema: "aiw.pdf/1", blob_id: null, pages: 4, annotations: [{ id: "a1", page: 2, text: L("Revisar vigencia", "Check term"), author: { kind: "agent", id: "legal" } }] });
    this.mk({ id: "art_demo_pipeline", kind: "board", title: L("Pipeline de ventas", "Sales pipeline"), ...this.agentOwned("sales") }, {
      schema: "aiw.board/1", view: "kanban", columns: [{ id: "todo", title: L("Prospectos", "Leads") }, { id: "doing", title: L("En negociación", "Negotiating"), wip: 3 }, { id: "done", title: L("Ganado", "Won") }],
      cards: [{ id: "k1", col: "todo", title: "Grupo Alfa", labels: [L("nuevo", "new")] }, { id: "k2", col: "doing", title: "Acme", assignee: { kind: "agent", id: "sales" }, due: "2026-11-01", labels: ["$50,000"], link: { artifact_id: "art_demo_contract" } }, { id: "k3", col: "done", title: "Beta SA" }],
    });
    this.mk({ id: "art_demo_inbox", kind: "inbox", title: L("Bandeja del día", "Daily inbox"), ...this.agentOwned("assistant") }, { schema: "aiw.inbox/1", items: [
      { id: "m1", from: "ana@acme.com", subject: L("Propuesta rediseño", "Redesign proposal"), snippet: L("¿Podemos ajustar el alcance?", "Can we adjust the scope?"), received_at: day(0, 8, 40), source: "email", triage: { label: L("cliente", "client"), priority: "high", suggested_action: L("responder", "reply"), draft_artifact_id: null } },
      { id: "m2", from: "billing@vendor.io", subject: L("Factura 4411", "Invoice 4411"), snippet: L("Adjuntamos su factura.", "Please find your invoice attached."), received_at: day(0, 7, 5), source: "email", triage: { label: L("finanzas", "finance"), priority: "normal", suggested_action: L("turnar a contabilidad", "forward to accounting"), draft_artifact_id: null } },
    ] });
    this.mk({ id: "art_demo_agenda", kind: "agenda", title: L("Agenda semanal", "Weekly agenda"), ...this.agentOwned("assistant") }, { schema: "aiw.agenda/1", events: [
      { id: "e1", title: L("Revisión Acme", "Acme review"), start: day(1, 10), end: day(1, 11), attendees: ["ana@acme.com"], status: "proposed", source: "agent" },
      { id: "e2", title: L("Cierre financiero", "Financial close"), start: day(2, 9), end: day(2, 10, 30), attendees: [], status: "confirmed", source: "user" },
    ] });
    this.mk({ id: "art_demo_form", kind: "form", title: L("Aprobación de descuento", "Discount approval"), ...this.agentOwned("hr"), status: "in_review" }, { schema: "aiw.form/1", fields: [
      { key: "discount", type: "money", label: L("Descuento autorizado", "Authorized discount"), required: true, max: 10 },
      { key: "reason", type: "textarea", label: L("Motivo", "Reason"), required: false },
      { key: "ok", type: "checkbox", label: L("Verifiqué la política", "I checked the policy"), required: true },
    ], answers: null, answered_by: null });
    this.mk({ id: "art_demo_candidates", kind: "table", title: L("Candidatos", "Candidates"), ...this.agentOwned("hr") }, TEMPLATES.find((t) => t.id === "hiring_pipeline")!.build());
    // live "Financial close": three sheets built in parallel
    this.runClose().catch(() => { /* cancelled by reset */ });
  }

  // ---- scripted parallel build (sec. 8.1) --------------------------------------------------------------
  private bump(id: string, content: any, author: { kind: "agent" | "user" | "system"; id: string }, summary: string, source = "agent_task") {
    const it = this.items.get(id)!;
    const base = it.meta.head_version;
    it.content = content;
    it.meta.head_version = base + 1;
    it.meta.last_author = author; it.meta.updated_at = nowIso(); it.meta.size_bytes = JSON.stringify(content).length;
    const info: ArtifactVersionInfo = { artifact_id: id, version: base + 1, base_version: base, author, source, summary, created_at: nowIso() };
    it.versions.push({ info, content: clone(content) });
    this.emit("artifact.version_created", { artifact_id: id, version: base + 1, base_version: base, author, source, summary, changed_units: [] }, author.kind === "agent" ? author.id : undefined);
    return base + 1;
  }
  private progress(id: string, progress: number, build_state: BuildState, extra: Record<string, unknown> = {}) {
    const it = this.items.get(id)!;
    it.meta.progress = progress; it.meta.build_state = build_state;
    this.emit("artifact.progress_changed", { artifact_id: id, project_id: it.meta.project_id, deliverable_id: it.meta.deliverable_id, progress, build_state, ...extra }, it.meta.created_by.id);
  }
  private async build(id: string, rows: Row[], sheetName: string, stepMs: number, step: number, imports?: any[], onDone?: () => void) {
    const g = this.gen;
    let p = this.items.get(id)!.meta.progress;
    while (p < 100) {
      await this.wait(stepMs);
      if (g !== this.gen) return;
      p = Math.min(100, p + step);
      const k = Math.max(1, Math.ceil((p / 100) * rows.length));
      this.emit("artifact.agent_focus", { artifact_id: id, agent_id: "accounting", region: `${sheetName}!B${k + 1}`, ttl_ms: stepMs + 400 }, "accounting");
      this.bump(id, sheetOf(sheetName, rows, k, imports), { kind: "agent", id: "accounting" }, L(`Agrega la fila ${k + 1}`, `Adds row ${k + 1}`));
      this.progress(id, p, p >= 100 ? "ready_for_review" : "building");
    }
    onDone?.();
  }
  private async runClose() {
    const g = this.gen;
    await this.wait(2500);
    if (g !== this.gen) return;
    this.emit("artifact.created", { artifact: this.pub("art_demo_income"), open_in_workspace: false }, "accounting");
    const jobs = [
      this.build("art_demo_income", incomeRows(), "Result", 1000, 9, undefined, () => this.incomeDone()),
      this.build("art_demo_balance", balanceRows(), "Balance", 1400, 8, [{ alias: "IS", artifact_id: "art_demo_income" }]),
      this.build("art_demo_cashflow", cashRows(), "Cash", 1200, 10),
    ];
    await Promise.all(jobs);
  }
  private async incomeDone() {
    const g = this.gen;
    this.emit("artifact.dependency_changed", { artifact_id: "art_demo_balance", source_id: "art_demo_income", source_version: this.items.get("art_demo_income")!.meta.head_version, stale: true });
    await this.wait(1800);
    if (g !== this.gen) return;
    const bal = this.items.get("art_demo_balance");
    if (!bal) return;
    const v = this.bump("art_demo_balance", clone(bal.content), { kind: "system", id: "recalc" }, L("Recalculado por cambio en el estado de resultados", "Recalculated after the income statement changed"), "recalc");
    this.emit("artifact.recalculated", { artifact_id: "art_demo_balance", version: v, source_id: "art_demo_income", changed_units: ["Balance!B9"] });
    this.emit("artifact.dependency_changed", { artifact_id: "art_demo_balance", source_id: "art_demo_income", source_version: this.items.get("art_demo_income")!.meta.head_version, stale: false });
  }

  /** Re-runs the parallel scenario (called when a request mentions the financial close). */
  startClose() {
    this.ensureSeeded();
    const reset = (id: string, content: any, progress: number) => { const it = this.items.get(id); if (it) { it.content = content; it.meta.progress = progress; it.meta.build_state = "building"; } };
    this.gen++;
    reset("art_demo_income", sheetOf("Result", incomeRows(), 2), 0);
    reset("art_demo_balance", sheetOf("Balance", balanceRows(), 2, [{ alias: "IS", artifact_id: "art_demo_income" }]), 0);
    reset("art_demo_cashflow", sheetOf("Cash", cashRows(), 1), 0);
    ["art_demo_income", "art_demo_balance", "art_demo_cashflow"].forEach((id) => this.progress(id, 0, "building"));
    this.runClose().catch(() => {});
  }

  // ---- agent-produced artifacts for regular tasks ---------------------------------------------------------
  onTask(type: string, t: Task) {
    this.ensureSeeded();
    if (type === "task.started" && !this.building.has(t.id) && DEFAULT_TEMPLATE[t.agent_id]) {
      const tpl = TEMPLATES.find((x) => x.id === DEFAULT_TEMPLATE[t.agent_id])!;
      const it = this.mk({ id: uid("art"), kind: tpl.kind, title: t.title, ...this.agentOwned(t.agent_id), task_id: t.id, request_id: t.request_id, progress: 5, build_state: "building" }, tpl.build());
      this.building.set(t.id, it.meta.id);
      this.emit("artifact.created", { artifact: this.pub(it.meta.id), open_in_workspace: true }, t.agent_id);
      const g = this.gen;
      (async () => {
        for (let p = 20; p < 90; p += 20) {
          await this.wait(1500);
          if (g !== this.gen || !this.building.has(t.id)) return;
          this.progress(it.meta.id, p, "building");
          this.emit("artifact.agent_focus", { artifact_id: it.meta.id, agent_id: t.agent_id, ttl_ms: 1800 }, t.agent_id);
        }
      })().catch(() => {});
    }
    if (type === "task.completed" || type === "task.failed") {
      const id = this.building.get(t.id);
      if (id && this.items.has(id)) { this.building.delete(t.id); this.progress(id, type === "task.completed" ? 100 : this.items.get(id)!.meta.progress, type === "task.completed" ? "ready_for_review" : "blocked"); }
    }
  }
  onRequest(text: string) { if (/cierre|financial close|close the (quarter|month)/i.test(text)) this.startClose(); }

  // ---- REST ---------------------------------------------------------------------------------------------
  private pub(id: string): ArtifactMeta {
    const m = this.items.get(id)!.meta;
    return { ...m, attachments: m.attachments.map((a) => ({ ...a })) };
  }
  private full(id: string): Artifact {
    const it = this.items.get(id);
    if (!it) throw new Error("mock: artifact not found");
    return { ...this.pub(id), content: clone(it.content), version: it.meta.head_version };
  }

  async handle(method: string, seg: string[], q: URLSearchParams, body: any): Promise<unknown> {
    this.ensureSeeded();
    const [r0, r1, r2, r3] = seg;
    if (r0 === "artifact-kinds") return { items: ARTIFACT_KINDS.map((kind) => ({ kind, schema_version: `aiw.${kind}/1`, suggested: (ROLE_SUGGESTED[q.get("agent_id") || ""] || []).includes(kind) })) };
    if (r0 === "artifact-templates") {
      const agent = q.get("agent_id");
      const items: ArtifactTemplate[] = TEMPLATES.filter((t) => !q.get("kind") || t.kind === q.get("kind")).sort((a, b) => Number(b.suggested_for.includes(agent || "")) - Number(a.suggested_for.includes(agent || "")))
        .map((t) => ({ id: t.id, kind: t.kind, title: t.title(), suggested_for: t.suggested_for }));
      return { items };
    }
    if (r0 === "workspaces") return { tabs: [], layout: {}, active: null };
    if (r0 === "projects" && r2 === "workspace") return this.project(r1);
    if (r0 !== "artifacts") throw new Error("mock: unknown artifact route");

    if (!r1) {
      if (method === "GET") return { items: [...this.items.values()].map((i) => this.pub(i.meta.id)) };
      if (method === "POST") return this.createFrom(body);
    }
    const it = this.items.get(r1);
    if (!it) throw new Error(`mock: ${method} /artifacts/${r1} -> 404`);
    if (method === "GET" && !r2) return this.full(r1);
    if (method === "GET" && r2 === "versions") return { items: it.versions.map((v) => v.info).reverse() };
    if (method === "GET" && r2 === "links") return { items: this.links.filter((l) => l.from === r1 || l.to === r1) };
    if (method === "PATCH" && !r2) {
      if (body?.title) it.meta.title = String(body.title).slice(0, 200);
      if (body?.status) {
        const from = it.meta.status; it.meta.status = body.status; it.meta.locked = body.status === "approved" || body.status === "sent";
        this.emit("artifact.status_changed", { artifact_id: r1, from, to: body.status, by: { kind: "user", id: "me" } });
      }
      it.meta.updated_at = nowIso();
      this.emit("artifact.updated", { artifact: this.pub(r1) });
      return this.pub(r1);
    }
    if (method === "POST" && r2 === "versions") return this.saveVersion(it, body);
    if (method === "POST" && r2 === "restore") {
      const v = it.versions.find((x) => x.info.version === Number(body?.version));
      if (!v) throw new Error("mock: version not found 404");
      this.bump(r1, clone(v.content), { kind: "user", id: "me" }, L(`Restaura la versión ${v.info.version}`, `Restores version ${v.info.version}`), "restore");
      return this.pub(r1);
    }
    if (method === "PUT" && r2 === "attachments" && r3) {
      // agents can only be given read/propose/edit: there is no approve mode
      const mode = ["none", "read", "propose", "edit"].includes(body?.mode) ? body.mode : "read";
      it.meta.attachments = it.meta.attachments.filter((a) => a.agent_id !== r3);
      if (mode !== "none") it.meta.attachments.push({ agent_id: r3, mode });
      this.emit("artifact.updated", { artifact: this.pub(r1) });
      return this.pub(r1);
    }
    if (method === "POST" && r2 === "ask") return { request_id: uid("req") };
    if (method === "POST" && r2 === "refresh-dependencies") return { ok: true };
    throw new Error(`mock: ${method} /${seg.join("/")} not implemented`);
  }

  private createFrom(body: any) {
    const kind: ArtifactKind = ARTIFACT_KINDS.includes(body?.kind) ? body.kind : "doc";
    const tpl = body?.template_id ? TEMPLATES.find((t) => t.id === body.template_id) : undefined;
    const agent: string | undefined = body?.agent_id;
    const content = body?.content ?? (tpl ? tpl.build() : blank(kind));
    const base = agent ? this.agentOwned(agent) : { created_by: { kind: "user" as const, id: "me" }, last_author: { kind: "user" as const, id: "me" }, attachments: [] };
    const it = this.mk({ id: uid("art"), kind: tpl?.kind ?? kind, title: String(body?.title || tpl?.title()[getLocale()] || ""), ...base, created_by: { kind: "user", id: "me" } }, content);
    if (agent) it.meta.attachments = [{ agent_id: agent, mode: "read" }]; // opened by a human in an agent desk: read by default (sec. 2.2)
    this.emit("artifact.created", { artifact: this.pub(it.meta.id), open_in_workspace: false });
    return this.full(it.meta.id);
  }

  private saveVersion(it: Item, body: any) {
    const id = it.meta.id;
    if (it.meta.locked) throw new Error("mock: locked 409");
    const baseV = Number(body?.base_version);
    const mine = body?.content;
    if (!mine) throw new Error("mock: content required 422");
    if (baseV === it.meta.head_version) {
      const v = this.bump(id, mine, { kind: "user", id: "me" }, body?.summary || L("Edición manual", "Manual edit"), "human_edit");
      return { version: v, merged: false };
    }
    // 3-way merge by cell for sheets (sec. 5.3); everything else conflicts when the head moved
    const base = it.versions.find((x) => x.info.version === baseV)?.content;
    if (it.meta.kind === "sheet" && base) {
      const merged = clone(it.content);
      for (let si = 0; si < mine.sheets.length; si++) {
        const ms = mine.sheets[si]; const bs = base.sheets[si]; const hs = merged.sheets[si];
        if (!bs || !hs) continue;
        const keys = new Set([...Object.keys(ms.cells), ...Object.keys(bs.cells)]);
        for (const k of keys) {
          const myChange = JSON.stringify(ms.cells[k]) !== JSON.stringify(bs.cells[k]);
          if (!myChange) continue;
          const theirChange = JSON.stringify(hs.cells[k]) !== JSON.stringify(bs.cells[k]);
          if (theirChange && JSON.stringify(hs.cells[k]) !== JSON.stringify(ms.cells[k])) throw new Error("mock: conflict 409");
          if (ms.cells[k]) hs.cells[k] = ms.cells[k]; else delete hs.cells[k];
        }
      }
      const v = this.bump(id, merged, { kind: "user", id: "me" }, L("Edición manual (fusionada)", "Manual edit (merged)"), "rebase");
      return { version: v, merged: true, content: merged };
    }
    throw new Error("mock: conflict 409");
  }

  private project(pid: string): ProjectWorkspace {
    const arts = [...this.items.values()].filter((i) => i.meta.project_id === pid).map((i) => this.pub(i.meta.id));
    const groups = new Map<string, ArtifactMeta[]>();
    arts.forEach((a) => { const k = a.deliverable_id || a.id; groups.set(k, [...(groups.get(k) || []), a]); });
    const rank: BuildState[] = ["blocked", "queued", "building", "ready_for_review", "done"];
    const deliverables = [...groups.entries()].map(([deliverable_id, list]) => {
      const main = list[0];
      const progress = Math.round(list.reduce((s, a) => s + a.progress, 0) / list.length);
      const build_state = list.map((a) => a.build_state).sort((a, b) => rank.indexOf(a) - rank.indexOf(b))[0];
      return { deliverable_id, title: main.title, agent_id: main.created_by.id, status: build_state === "done" ? "done" : "running", progress, build_state, artifacts: list };
    });
    const avg = deliverables.length ? deliverables.reduce((s, d) => s + d.progress, 0) / deliverables.length : 0;
    return { project_id: pid, name: L("Cierre financiero", "Financial close"), status: avg >= 100 ? "done" : "running", deliverables, links: this.links.filter((l) => arts.some((a) => a.id === l.from || a.id === l.to)) };
  }
}
