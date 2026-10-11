package projects

// The giant built-in templates (G2). Data, not code: blocks repeat over list
// parameters, so the user decides the units, countries, workstreams... Built
// with the roles of the demo office only (accounting, analyst, legal, sales,
// hr, operations, assistant), each ends in at least one human gate.
//
//   - annual_budget            units x countries      up to 6 x 8   -> 279 tasks
//   - erp_migration            streams x waves        up to 8 x 5   -> 249 tasks (+ a go-live gate per wave)
//   - compliance_program       regulations x countries up to 6 x 8  -> 278 tasks
//   - product_launch           markets x channels     up to 12 x 4  -> 211 tasks
//   - post_merger_integration  functions x entities   up to 8 x 6   -> 261 tasks
//   - year_end_audit           entities x areas       up to 8 x 8   -> 286 tasks

func annualBudgetSrc() *giantB {
	g := newGiant("proj.ab", "tpl-annual-budget", "annual_budget",
		"Presupuesto anual de una multinacional",
		"Annual budget of a multinational",
		"Presupuesto anual de abajo hacia arriba: cada unidad de negocio en cada país proyecta ventas, plantilla, gastos e inversiones y arma su estado de resultados; luego se consolida por unidad y por país, se modelan escenarios y el CFO y el consejo aprueban. Ejemplo, no asesoría contable.",
		"Bottom-up annual budget: every business unit in every country forecasts sales, headcount, expenses and capex and builds its P&L; then it is consolidated by unit and by country, scenarios are modeled and the CFO and the board approve. Example, not accounting advice.")
	g.list("units", "unit", 6, "Unidades de negocio", "Business units", "Retail, Servicios, Industrial", "Retail, Services, Industrial")
	g.list("countries", "country", 8, "Países", "Countries", "México, Colombia, Chile, España", "Mexico, Colombia, Chile, Spain")
	g.action("approve_annual_budget", "Aprobar: {node}", "Approve: {node}")
	o1 := g.obj("o1", "Lineamientos del presupuesto", "Budget guidelines")
	o1.wf("wgl", "Supuestos y metas", "Assumptions and targets").
		task("macro", "analyst", "M", "Definir supuestos macroeconómicos y de tipo de cambio", "Define macroeconomic and FX assumptions").
		task("guide", "accounting", "M", "Emitir lineamientos y calendario presupuestal", "Issue the budget guidelines and calendar", "macro").
		task("targets", "sales", "L", "Fijar metas de crecimiento por unidad de negocio", "Set growth targets per business unit", "macro")
	o2 := g.obj("o2", "Presupuesto de abajo hacia arriba", "Bottom-up budget")
	o2.wf("wb", "{unit} · {country}", "{unit} · {country}", "units", "countries").
		task("sales", "sales", "M", "Proyectar las ventas de {unit} en {country}", "Forecast sales of {unit} in {country}", "targets", "guide").
		task("hc", "hr", "M", "Plan de plantilla de {unit} en {country}", "Headcount plan of {unit} in {country}", "guide").
		task("opex", "operations", "M", "Presupuesto de gastos operativos de {unit} en {country}", "Operating expense budget of {unit} in {country}", "guide").
		task("capex", "operations", "M", "Solicitud de inversiones (capex) de {unit} en {country}", "Capex request of {unit} in {country}", "guide").
		task("pl", "accounting", "L", "Estado de resultados presupuestado de {unit} en {country}", "Budgeted P&L of {unit} in {country}", "sales", "hc", "opex", "capex")
	o3 := g.obj("o3", "Consolidación y escenarios", "Consolidation and scenarios")
	o3.wf("wu", "Unidad {unit}", "Unit {unit}", "units").
		task("ucons", "accounting", "L", "Consolidar el presupuesto de la unidad {unit}", "Consolidate the budget of unit {unit}", "pl").
		task("urev", "analyst", "M", "Revisar márgenes y supuestos de la unidad {unit}", "Review margins and assumptions of unit {unit}", "ucons")
	o3.wf("wc", "País {country}", "Country {country}", "countries").
		task("tax", "accounting", "M", "Presupuesto de impuestos y tipo de cambio de {country}", "Tax and FX budget of {country}", "pl").
		task("law", "legal", "S", "Revisar obligaciones legales y fiscales de {country}", "Review legal and tax obligations of {country}", "tax")
	o3.wf("wg", "Grupo", "Group").
		task("cons", "accounting", "XL", "Consolidar el presupuesto del grupo", "Consolidate the group budget", "ucons", "tax").
		task("scen", "analyst", "L", "Modelar escenarios base, optimista y adverso", "Model base, upside and downside scenarios", "cons").
		task("cash", "accounting", "L", "Proyectar el flujo de caja anual", "Project the annual cash flow", "cons").
		task("kpi", "analyst", "M", "Definir indicadores y metas de desempeño", "Define performance indicators and targets", "urev", "scen").
		task("deck", "assistant", "L", "Preparar la presentación para el consejo", "Prepare the board presentation", "scen", "cash", "kpi", "law").
		gate("gate", "approve_annual_budget", "high", "Aprobación del CFO y del consejo del presupuesto anual", "CFO and board approval of the annual budget", "deck").
		task("comm", "assistant", "M", "Comunicar los presupuestos aprobados a cada unidad y país", "Communicate the approved budgets to every unit and country", "gate").
		task("lock", "accounting", "S", "Cargar y bloquear el presupuesto aprobado en el sistema", "Load and lock the approved budget in the system", "gate")
	return g
}

func erpMigrationSrc() *giantB {
	g := newGiant("proj.erp", "tpl-erp-migration", "erp_migration",
		"Migración de ERP y transformación digital",
		"ERP migration and digital transformation",
		"Programa de transformación en olas: fundamentos, diseño por frente de trabajo (finanzas, compras, ventas...) y, para cada ola, migración de datos, configuración, pruebas, capacitación y aceptación, con una autorización humana de salida en vivo por ola. Ejemplo, no asesoría tecnológica.",
		"Transformation program in waves: foundations, design per workstream (finance, procurement, sales...) and, for every wave, data migration, configuration, testing, training and acceptance, with a human go-live approval per wave. Example, not technology advice.")
	g.list("streams", "stream", 8, "Frentes de trabajo", "Workstreams", "Finanzas, Compras, Ventas, Inventario, Recursos humanos", "Finance, Procurement, Sales, Inventory, HR")
	g.list("waves", "wave", 5, "Olas de salida", "Waves", "Piloto, Ola 1, Ola 2", "Pilot, Wave 1, Wave 2")
	g.action("approve_erp_program", "Aprobar: {node}", "Approve: {node}")
	g.action("approve_go_live", "Autorizar: {node}", "Authorize: {node}")
	o1 := g.obj("o1", "Fundamentos del programa", "Program foundations")
	o1.wf("w0", "Diagnóstico y arquitectura", "Assessment and architecture").
		task("asis", "analyst", "L", "Diagnosticar los sistemas y procesos actuales", "Assess the current systems and processes").
		task("arch", "operations", "L", "Definir la arquitectura objetivo y el modelo de datos", "Define the target architecture and data model", "asis").
		task("vendor", "legal", "M", "Negociar los contratos con el integrador y el proveedor del ERP", "Negotiate the contracts with the integrator and the ERP vendor", "arch").
		task("biz", "accounting", "M", "Armar el caso de negocio y el presupuesto del programa", "Build the business case and the program budget", "asis").
		task("change", "hr", "M", "Diseñar la gestión del cambio y la capacitación", "Design change management and training", "asis").
		task("migs", "operations", "M", "Definir la estrategia de migración y limpieza de datos", "Define the data migration and cleansing strategy", "arch").
		gate("gate0", "approve_erp_program", "high", "Aprobación del comité directivo para iniciar el programa", "Steering committee approval to start the program", "biz", "vendor", "change", "migs")
	o2 := g.obj("o2", "Diseño por frente de trabajo", "Design per workstream")
	o2.wf("wds", "{stream}", "{stream}", "streams").
		task("map", "analyst", "M", "Mapear los procesos de {stream}", "Map the processes of {stream}", "gate0").
		task("fit", "analyst", "M", "Análisis de brechas (fit-gap) de {stream}", "Fit-gap analysis of {stream}", "map").
		task("spec", "operations", "L", "Especificar la configuración y las integraciones de {stream}", "Specify the configuration and integrations of {stream}", "fit")
	o3 := g.obj("o3", "Implementación por olas", "Implementation in waves")
	o3.wf("wim", "{wave}: {stream}", "{wave}: {stream}", "waves", "streams").
		task("mig", "operations", "L", "Migrar y validar los datos de {stream} en {wave}", "Migrate and validate the data of {stream} in {wave}", "spec", "golive^waves").
		task("build", "operations", "L", "Configurar y desarrollar {stream} para {wave}", "Configure and build {stream} for {wave}", "spec", "golive^waves").
		task("test", "analyst", "M", "Pruebas integrales de {stream} en {wave}", "End-to-end tests of {stream} in {wave}", "mig", "build").
		task("train", "hr", "M", "Capacitar a los usuarios de {stream} en {wave}", "Train the users of {stream} in {wave}", "spec", "golive^waves").
		task("uat", "assistant", "S", "Aceptación del usuario de {stream} en {wave}", "User acceptance of {stream} in {wave}", "test", "train")
	o3.wf("wgl", "Salida en vivo: {wave}", "Go-live: {wave}", "waves").
		task("cut", "operations", "L", "Plan de corte y puesta en producción de {wave}", "Cutover and production plan of {wave}", "uat").
		gate("golive", "approve_go_live", "high", "Autorización de salida en vivo de {wave}", "Go-live authorization of {wave}", "cut").
		task("hyper", "operations", "M", "Soporte intensivo posterior a la salida de {wave}", "Hypercare after the go-live of {wave}", "golive")
	o4 := g.obj("o4", "Cierre del programa", "Program closure")
	o4.wf("wcl", "Cierre", "Closure").
		task("lessons", "analyst", "M", "Documentar lecciones aprendidas y beneficios logrados", "Document lessons learned and benefits achieved", "hyper").
		task("hand", "operations", "M", "Transferir el soporte a operaciones", "Hand the support over to operations", "hyper").
		task("final", "assistant", "L", "Informe final del programa al comité directivo", "Final program report to the steering committee", "lessons", "hand")
	return g
}

func complianceSrc() *giantB {
	g := newGiant("proj.cp", "tpl-compliance-program", "compliance_program",
		"Programa de cumplimiento regulatorio",
		"Regulatory compliance program",
		"Programa de cumplimiento por norma y por país: inventario de procesos y riesgos, análisis de brechas y políticas por norma, y para cada norma en cada país los requisitos locales, controles, evidencia, capacitación y pruebas; termina con la declaración por país y la aprobación del comité de cumplimiento. Ejemplo, no asesoría legal.",
		"Compliance program per regulation and per country: inventory of processes and risks, gap analysis and policies per regulation, and for each regulation in each country the local requirements, controls, evidence, training and tests; it ends with a statement per country and the compliance committee approval. Example, not legal advice.")
	g.list("regulations", "regulation", 6, "Normas y regulaciones", "Regulations", "GDPR, SOX, ISO 27001, Antilavado", "GDPR, SOX, ISO 27001, Anti-money laundering")
	g.list("countries", "country", 8, "Países", "Countries", "México, España, Chile", "Mexico, Spain, Chile")
	g.action("approve_compliance", "Aprobar: {node}", "Approve: {node}")
	o1 := g.obj("o1", "Alcance e inventario", "Scope and inventory")
	o1.wf("wsc", "Inventario de riesgos", "Risk inventory").
		task("inv", "analyst", "L", "Inventariar procesos, sistemas y datos sujetos a regulación", "Inventory the processes, systems and data subject to regulation").
		task("risk", "analyst", "M", "Evaluar el riesgo inherente por proceso", "Assess the inherent risk per process", "inv").
		task("own", "assistant", "S", "Asignar responsables y calendario del programa", "Assign owners and the program calendar", "inv")
	o2 := g.obj("o2", "Análisis por norma", "Analysis per regulation")
	o2.wf("wreg", "{regulation}", "{regulation}", "regulations").
		task("gap", "analyst", "L", "Análisis de brechas contra {regulation}", "Gap analysis against {regulation}", "risk").
		task("pol", "legal", "L", "Actualizar políticas y procedimientos para {regulation}", "Update policies and procedures for {regulation}", "gap").
		task("ctl", "operations", "M", "Diseñar los controles requeridos por {regulation}", "Design the controls required by {regulation}", "pol")
	o3 := g.obj("o3", "Implementación por norma y país", "Implementation per regulation and country")
	o3.wf("wloc", "{regulation} · {country}", "{regulation} · {country}", "regulations", "countries").
		task("req", "legal", "M", "Requisitos locales de {regulation} en {country}", "Local requirements of {regulation} in {country}", "pol").
		task("impl", "operations", "L", "Implementar los controles de {regulation} en {country}", "Implement the controls of {regulation} in {country}", "req", "ctl").
		task("evid", "accounting", "M", "Recopilar evidencia de {regulation} en {country}", "Collect evidence of {regulation} in {country}", "impl").
		task("train", "hr", "M", "Capacitar al personal de {country} en {regulation}", "Train the staff of {country} on {regulation}", "req").
		task("test", "analyst", "M", "Probar la eficacia de los controles de {regulation} en {country}", "Test the effectiveness of the {regulation} controls in {country}", "evid", "train")
	o4 := g.obj("o4", "Certificación y reporte", "Certification and reporting")
	o4.wf("wat", "Declaración de {country}", "Statement of {country}", "countries").
		task("attest", "assistant", "M", "Declaración de cumplimiento de {country}", "Compliance statement of {country}", "test")
	o4.wf("wres", "Riesgo residual de {regulation}", "Residual risk of {regulation}", "regulations").
		task("resid", "analyst", "M", "Riesgo residual y plan de remediación de {regulation}", "Residual risk and remediation plan for {regulation}", "test")
	o4.wf("wg", "Comité de cumplimiento", "Compliance committee").
		gate("gate", "approve_compliance", "high", "Aprobación del comité de cumplimiento", "Compliance committee approval", "attest", "resid").
		task("audit", "legal", "L", "Preparar la documentación para auditores y reguladores", "Prepare the documentation for auditors and regulators", "gate").
		task("report", "assistant", "L", "Informe anual de cumplimiento al directorio", "Annual compliance report to the board", "gate")
	return g
}

func productLaunchSrc() *giantB {
	g := newGiant("proj.pl", "tpl-product-launch", "product_launch",
		"Lanzamiento de producto en varios mercados",
		"Product launch in several markets",
		"Lanzamiento coordinado entre marketing, legal, operaciones y ventas: estrategia global, preparación de cada mercado (regulación, precios locales, contenidos, campaña, logística, equipo), plan de cada canal y una decisión humana de salida (go/no-go) antes de ejecutar y medir en cada mercado. Ejemplo, no asesoría.",
		"Coordinated launch across marketing, legal, operations and sales: global strategy, preparation of every market (regulation, local pricing, content, campaign, logistics, team), a plan for each channel and a human go/no-go decision before executing and measuring in every market. Example, not advice.")
	g.list("markets", "market", 12, "Mercados", "Markets", "México, Colombia, Chile, España", "Mexico, Colombia, Chile, Spain")
	g.list("channels", "channel", 4, "Canales de venta", "Sales channels", "Tienda en línea, Distribuidores, Retail", "Online store, Distributors, Retail")
	g.action("approve_launch", "Decidir: {node}", "Decide: {node}")
	o1 := g.obj("o1", "Estrategia global", "Global strategy")
	o1.wf("wgl", "Posicionamiento y plan maestro", "Positioning and master plan").
		task("pos", "sales", "L", "Definir posicionamiento y mensajes del producto", "Define the product positioning and messages").
		task("price", "accounting", "M", "Definir la estructura de precios y márgenes", "Define the price structure and margins", "pos").
		task("ip", "legal", "M", "Proteger marca y propiedad intelectual", "Protect the brand and intellectual property").
		task("plan", "operations", "M", "Armar el plan maestro de lanzamiento", "Build the launch master plan", "pos").
		task("supply", "operations", "L", "Asegurar la cadena de suministro y el inventario inicial", "Secure the supply chain and the initial inventory", "plan")
	o2 := g.obj("o2", "Preparación por mercado", "Preparation per market")
	o2.wf("wmk", "{market}", "{market}", "markets").
		task("reg", "legal", "L", "Autorizaciones y cumplimiento regulatorio en {market}", "Authorizations and regulatory compliance in {market}", "ip").
		task("lprice", "accounting", "M", "Precios locales, impuestos y moneda en {market}", "Local prices, taxes and currency in {market}", "price").
		task("loc", "assistant", "M", "Localizar contenidos y materiales para {market}", "Localize content and materials for {market}", "pos").
		task("camp", "sales", "L", "Campaña de lanzamiento en {market}", "Launch campaign in {market}", "loc").
		task("log", "operations", "M", "Logística y distribución en {market}", "Logistics and distribution in {market}", "supply").
		task("team", "hr", "M", "Contratar y capacitar al equipo comercial de {market}", "Hire and train the sales team of {market}", "plan").
		task("brief", "analyst", "M", "Ficha de preparación de {market}", "Readiness brief of {market}", "reg", "lprice", "camp", "log", "team")
	o3 := g.obj("o3", "Canales por mercado", "Channels per market")
	o3.wf("wch", "{market} · {channel}", "{market} · {channel}", "markets", "channels").
		task("cplan", "sales", "M", "Plan comercial del canal {channel} en {market}", "Commercial plan of the {channel} channel in {market}", "camp", "lprice").
		task("cen", "operations", "M", "Habilitar el canal {channel} en {market}", "Enable the {channel} channel in {market}", "cplan", "log")
	o4 := g.obj("o4", "Decisión y lanzamiento", "Decision and launch")
	o4.wf("wgo", "Decisión de salida", "Go/no-go decision").
		gate("go", "approve_launch", "high", "Decisión de salida al mercado (go/no-go) de la dirección", "Executive go/no-go decision to enter the market", "brief", "cen")
	o4.wf("wlv", "Lanzamiento en {market}", "Launch in {market}", "markets").
		task("live", "operations", "M", "Ejecutar el lanzamiento en {market}", "Execute the launch in {market}", "go").
		task("rev", "analyst", "M", "Medir los resultados de las primeras semanas en {market}", "Measure the results of the first weeks in {market}", "live")
	o4.wf("wfin", "Informe", "Report").
		task("report", "assistant", "L", "Informe consolidado del lanzamiento", "Consolidated launch report", "rev")
	return g
}

func postMergerSrc() *giantB {
	g := newGiant("proj.pm", "tpl-post-merger-integration", "post_merger_integration",
		"Integración posterior a una fusión (M&A)",
		"Post-merger integration (M&A)",
		"Integración por función y por entidad: preparación del Día 1, evaluación y plan de cada función en cada entidad, aprobación humana del comité de integración, ejecución (procesos, personas, contratos), captura de sinergias y el informe del Día 100. Ejemplo, no asesoría.",
		"Integration per function and per entity: Day-1 readiness, assessment and plan of every function in every entity, human approval by the integration committee, execution (processes, people, contracts), synergy capture and the Day-100 report. Example, not advice.")
	g.list("functions", "function", 8, "Funciones", "Functions", "Finanzas, Tecnología, Recursos humanos, Legal, Operaciones, Ventas", "Finance, Technology, HR, Legal, Operations, Sales")
	g.list("entities", "entity", 6, "Entidades adquiridas", "Acquired entities", "Entidad A, Entidad B", "Entity A, Entity B")
	g.action("approve_integration_plan", "Aprobar: {node}", "Approve: {node}")
	o1 := g.obj("o1", "Preparación del Día 1", "Day-1 readiness")
	o1.wf("wd1", "Día 1", "Day 1").
		task("gov", "assistant", "M", "Instalar la oficina de integración y la gobernanza", "Set up the integration office and governance").
		task("comms", "assistant", "M", "Plan de comunicación a empleados, clientes y proveedores", "Communication plan for employees, customers and suppliers").
		task("ret", "hr", "M", "Programa de retención del talento clave", "Key talent retention program").
		task("d1", "operations", "L", "Lista de verificación del Día 1: accesos, pagos y nóminas", "Day-1 checklist: access, payments and payroll", "gov").
		task("base", "analyst", "L", "Línea base de sinergias y costos de integración", "Baseline of synergies and integration costs")
	o2 := g.obj("o2", "Evaluación y planes", "Assessment and plans")
	o2.wf("wpl", "{function} · {entity}", "{function} · {entity}", "functions", "entities").
		task("assess", "analyst", "M", "Evaluar {function} en {entity}", "Assess {function} in {entity}", "base").
		task("plan", "operations", "M", "Plan de integración de {function} en {entity}", "Integration plan of {function} in {entity}", "assess")
	o3 := g.obj("o3", "Aprobación", "Approval")
	o3.wf("wap", "Comité de integración", "Integration committee").
		gate("gate", "approve_integration_plan", "high", "Aprobación de los planes por el comité de integración", "Integration committee approval of the plans", "plan", "d1")
	o4 := g.obj("o4", "Ejecución", "Execution")
	o4.wf("wex", "{function} · {entity}", "{function} · {entity}", "functions", "entities").
		task("align", "operations", "M", "Alinear procesos y sistemas de {function} en {entity}", "Align the processes and systems of {function} in {entity}", "gate", "plan").
		task("people", "hr", "M", "Reorganizar equipos y roles de {function} en {entity}", "Reorganize the teams and roles of {function} in {entity}", "gate", "plan").
		task("contr", "legal", "M", "Renegociar contratos y cumplimiento de {function} en {entity}", "Renegotiate contracts and compliance of {function} in {entity}", "gate", "plan")
	o5 := g.obj("o5", "Sinergias y cierre", "Synergies and closure")
	o5.wf("wsy", "Sinergias de {function}", "Synergies of {function}", "functions").
		task("syn", "analyst", "M", "Capturar y medir las sinergias de {function}", "Capture and measure the synergies of {function}", "align")
	o5.wf("wen", "Día 100 de {entity}", "Day 100 of {entity}", "entities").
		task("d100", "assistant", "M", "Informe del Día 100 de {entity}", "Day-100 report of {entity}", "people", "contr")
	o5.wf("wfi", "Cierre", "Closure").
		task("final", "assistant", "L", "Informe final de integración y traspaso a la operación normal", "Final integration report and handover to normal operations", "syn", "d100")
	return g
}

func yearEndAuditSrc() *giantB {
	g := newGiant("proj.ya", "tpl-year-end-audit", "year_end_audit",
		"Preparación de la auditoría de cierre de año",
		"Year-end audit preparation",
		"Prepara la auditoría anual de un grupo: planeación con los auditores, y por cada entidad y área la documentación, conciliaciones, análisis de variaciones y soportes; revisión final y cartas por entidad, aprobación del CFO y atención de hallazgos. Ejemplo, no asesoría contable.",
		"Prepares the annual audit of a group: planning with the auditors, and for every entity and area the documentation, reconciliations, variance analysis and support; final review and letters per entity, CFO approval and follow-up of findings. Example, not accounting advice.")
	g.list("entities", "entity", 8, "Entidades", "Entities", "Matriz, Filial 1, Filial 2", "Parent, Subsidiary 1, Subsidiary 2")
	g.list("areas", "area", 8, "Áreas de auditoría", "Audit areas", "Ingresos, Inventarios, Activos fijos, Nómina, Impuestos, Tesorería", "Revenue, Inventory, Fixed assets, Payroll, Taxes, Treasury")
	g.action("approve_audit_package", "Aprobar: {node}", "Approve: {node}")
	o1 := g.obj("o1", "Planeación", "Planning")
	o1.wf("wpl", "Alcance con los auditores", "Scope with the auditors").
		task("mat", "accounting", "M", "Definir la materialidad y el alcance con los auditores", "Define materiality and scope with the auditors").
		task("pbc", "assistant", "M", "Emitir la lista de documentos solicitados (PBC) y el calendario", "Issue the list of requested documents (PBC) and the calendar", "mat").
		task("risk", "analyst", "M", "Identificar áreas de riesgo y cambios del año", "Identify risk areas and changes of the year", "mat")
	o2 := g.obj("o2", "Preparación por entidad y área", "Preparation per entity and area")
	o2.wf("wea", "{entity} · {area}", "{entity} · {area}", "entities", "areas").
		task("docs", "assistant", "S", "Reunir la documentación de {area} de {entity}", "Gather the documentation of {area} of {entity}", "pbc").
		task("recon", "accounting", "M", "Conciliar los saldos de {area} de {entity}", "Reconcile the balances of {area} of {entity}", "docs").
		task("anal", "analyst", "M", "Análisis de variaciones de {area} de {entity}", "Variance analysis of {area} of {entity}", "recon", "risk").
		task("sup", "accounting", "S", "Preparar los soportes para auditoría de {area} de {entity}", "Prepare the audit support of {area} of {entity}", "anal")
	o3 := g.obj("o3", "Cierre por entidad", "Closure per entity")
	o3.wf("wen", "{entity}", "{entity}", "entities").
		task("rev", "accounting", "L", "Revisión final de los estados financieros de {entity}", "Final review of the financial statements of {entity}", "sup").
		task("law", "legal", "M", "Cartas de abogados y contingencias de {entity}", "Lawyer letters and contingencies of {entity}", "rev").
		task("rep", "assistant", "M", "Carta de representación de {entity}", "Representation letter of {entity}", "rev", "law")
	o4 := g.obj("o4", "Auditoría", "Audit")
	o4.wf("wau", "Entrega y seguimiento", "Delivery and follow-up").
		gate("gate", "approve_audit_package", "high", "Aprobación del CFO antes de entregar a los auditores", "CFO approval before delivering to the auditors", "rep").
		task("pack", "assistant", "L", "Entregar el paquete completo a los auditores", "Deliver the complete package to the auditors", "gate").
		task("fu", "accounting", "M", "Atender los hallazgos y preguntas de los auditores", "Answer the findings and questions of the auditors", "pack").
		task("ml", "analyst", "M", "Plan de acción para la carta de gerencia", "Action plan for the management letter", "fu").
		task("fin", "assistant", "M", "Informe de cierre de la auditoría para el consejo", "Audit closing report for the board", "ml")
	return g
}
