package projects

import (
	"aiworkforce/backend/internal/demodata"
)

// The inputs of the built-in templates (H1). Large templates are useless
// without data: seen in a live run of the group consolidated close launched
// with an empty project folder, where nearly every agent answered "there is no
// data" and produced plans instead of numbers. Each template says which files
// (or values) it needs; the sample datasets of package demodata stand in for
// them. Texts are i18n keys resolved for the project locale.

type inDef struct {
	key, kind string
	required  bool
	accept    []string
	example   string
	perParam  string
	options   []string
	// label and description, es then en
	label, desc [2]string
}

var tplDemoSets = map[string]string{
	"financial_close":         "financial_close",
	keyGroupClose:             demodata.GroupSet,
	keyDueDilig:               demodata.DueDiligenceSet,
	keyMarketEnt:              demodata.MarketSet,
	keyCashClose:              demodata.CashCloseSet,
	"annual_budget":           demodata.BudgetSet,
	"erp_migration":           demodata.ERPSet,
	"compliance_program":      demodata.ComplianceSet,
	"product_launch":          demodata.LaunchSet,
	"post_merger_integration": demodata.PostMergerSet,
	"year_end_audit":          demodata.AuditSet,
}

var tabular = []string{"csv", "xlsx"}
var docs = []string{"md", "pdf", "txt", "docx"}

var inputDefs = map[string][]inDef{
	"financial_close": {
		{key: "trial_balance", kind: InputFile, required: false, accept: tabular, example: "trial_balance.csv",
			label: [2]string{"Balanza de comprobación", "Trial balance"},
			desc:  [2]string{"Balanza del periodo con cuenta, nombre, debe y haber.", "Trial balance of the period with account, name, debit and credit."}},
	},
	keyGroupClose: {
		{key: "tb", kind: InputFolder, required: true, accept: tabular, perParam: "subsidiaries", example: "tb_S01_MX.csv",
			label: [2]string{"Balanza de comprobación por filial", "Trial balance per subsidiary"},
			desc:  [2]string{"Un archivo por filial, en su moneda local, con cuenta local, nombre, debe y haber.", "One file per subsidiary, in its local currency, with local account, name, debit and credit."}},
		{key: "intercompany", kind: InputFile, required: true, accept: tabular, example: "intercompany_matrix.csv",
			label: [2]string{"Matriz intercompañía", "Intercompany matrix"},
			desc:  [2]string{"Saldos y ventas entre filiales, vistos por el acreedor y por el deudor.", "Balances and sales between subsidiaries, seen by the creditor and by the debtor."}},
		{key: "fx_rates", kind: InputFile, required: true, accept: tabular, example: "fx_rates.csv",
			label: [2]string{"Tipos de cambio (cierre y promedio)", "FX rates (closing and average)"},
			desc:  [2]string{"Moneda local por 1 USD: tipo de cierre para el balance y promedio para resultados.", "Local currency per 1 USD: closing rate for the balance sheet and average for the P&L."}},
		{key: "coa_mapping", kind: InputFile, required: true, accept: tabular, example: "coa_mapping.csv",
			label: [2]string{"Mapeo del catálogo de cuentas", "Chart of accounts mapping"},
			desc:  [2]string{"Cuenta local de cada filial a cuenta del grupo.", "Local account of each subsidiary to group account."}},
		{key: "materiality", kind: InputFile, required: false, accept: docs, example: "materiality_policy.md",
			label: [2]string{"Política de materialidad", "Materiality policy"},
			desc:  [2]string{"Umbrales de materialidad y tolerancias de conciliación. Si falta, el equipo propone unos como SUPUESTO.", "Materiality thresholds and reconciliation tolerances. If missing, the team proposes some as an ASSUMPTION."}},
	},
	keyDueDilig: {
		{key: "financials", kind: InputFolder, required: true, accept: tabular, perParam: "entities", example: "financials_T01_empresa.csv",
			label: [2]string{"Estados financieros por empresa", "Financial statements per company"},
			desc:  [2]string{"Un archivo por empresa objetivo con resultados y balance de 3 años.", "One file per target company with 3 years of income statement and balance sheet."}},
		{key: "contracts", kind: InputFile, required: true, accept: tabular, example: "contracts_register.csv",
			label: [2]string{"Registro de contratos", "Contract register"},
			desc:  [2]string{"Contratos relevantes con valor, vigencia y cláusulas de cambio de control.", "Relevant contracts with value, term and change-of-control clauses."}},
		{key: "people", kind: InputFile, required: false, accept: tabular, example: "people_summary.csv",
			label: [2]string{"Resumen de personal", "People summary"},
			desc:  [2]string{"Plantilla y costo por función, personas clave.", "Headcount and cost per function, key people."}},
		{key: "litigation", kind: InputFile, required: false, accept: tabular, example: "litigation_register.csv",
			label: [2]string{"Registro de litigios", "Litigation register"},
			desc:  [2]string{"Casos abiertos con monto reclamado, probabilidad y provisión.", "Open cases with claim, probability and provision."}},
		{key: "deal_brief", kind: InputFile, required: false, accept: docs, example: "deal_brief.md",
			label: [2]string{"Resumen de la operación", "Deal summary"},
			desc:  [2]string{"Tesis, precio máximo y plazo de la adquisición.", "Thesis, maximum price and timeline of the acquisition."}},
	},
	keyMarketEnt: {
		{key: "market_data", kind: InputFile, required: true, accept: tabular, example: "market_data.csv",
			label: [2]string{"Datos de mercado por país", "Market data per country"},
			desc:  [2]string{"Población, PIB per cápita, impuestos y tamaño de mercado por país.", "Population, GDP per capita, taxes and market size per country."}},
		{key: "competitors", kind: InputFile, required: false, accept: tabular, example: "competitors.csv",
			label: [2]string{"Competidores", "Competitors"}, desc: [2]string{"Competidores principales con participación e índice de precio.", "Main competitors with share and price index."}},
		{key: "regulatory_notes", kind: InputFile, required: false, accept: docs, example: "regulatory_notes.md",
			label: [2]string{"Notas regulatorias", "Regulatory notes"}, desc: [2]string{"Requisitos legales y laborales conocidos.", "Known legal and labor requirements."}},
	},
	keyCashClose: {
		{key: "bank_statements", kind: InputFolder, required: true, accept: []string{"csv", "xlsx", "pdf"}, perParam: "accounts", example: "bank_C01_A.csv",
			label: [2]string{"Estados de cuenta bancarios", "Bank statements"},
			desc:  [2]string{"Un extracto por cuenta bancaria con movimientos y saldo.", "One statement per bank account with transactions and balance."}},
		{key: "ar_ledger", kind: InputFile, required: true, accept: tabular, example: "ar_ledger.csv",
			label: [2]string{"Cartera por cobrar", "Receivables ledger"}, desc: [2]string{"Facturas, vencimientos y cobros.", "Invoices, due dates and receipts."}},
		{key: "payables_due", kind: InputFile, required: false, accept: tabular, example: "payables_due.csv",
			label: [2]string{"Pagos próximos", "Upcoming payments"}, desc: [2]string{"Cuentas por pagar con vencimiento.", "Payables with due dates."}},
	},
	"annual_budget": {
		{key: "prior_actuals", kind: InputFile, required: true, accept: tabular, example: "prior_year_actuals.csv",
			label: [2]string{"Resultados reales del año anterior", "Prior-year actuals"},
			desc:  [2]string{"Ingresos, costos, gastos y plantilla por unidad de negocio y país.", "Revenue, costs, expenses and headcount per business unit and country."}},
		{key: "assumptions", kind: InputFile, required: true, accept: tabular, example: "macro_assumptions.csv",
			label: [2]string{"Supuestos macroeconómicos", "Macroeconomic assumptions"},
			desc:  [2]string{"Inflación, crecimiento salarial, PIB y tipo de cambio por país.", "Inflation, wage growth, GDP and FX per country."}},
		{key: "headcount", kind: InputFile, required: false, accept: tabular, example: "headcount_current.csv",
			label: [2]string{"Plantilla actual", "Current headcount"}, desc: [2]string{"Personas por unidad, país y función.", "People per unit, country and function."}},
	},
	"erp_migration": {
		{key: "systems", kind: InputFile, required: true, accept: tabular, example: "systems_inventory.csv",
			label: [2]string{"Inventario de sistemas", "Systems inventory"}, desc: [2]string{"Sistemas actuales por frente: proveedor, usuarios, antigüedad e integraciones.", "Current systems per workstream: vendor, users, age and integrations."}},
		{key: "data_volumes", kind: InputFile, required: true, accept: tabular, example: "data_volumes.csv",
			label: [2]string{"Volúmenes y calidad de datos", "Data volumes and quality"}, desc: [2]string{"Registros por objeto de datos y porcentaje limpio.", "Records per data object and clean percentage."}},
		{key: "program_budget", kind: InputNumber, required: false, example: "2400000",
			label: [2]string{"Presupuesto del programa (USD)", "Program budget (USD)"}, desc: [2]string{"Tope de inversión del programa.", "Investment ceiling of the program."}},
	},
	"compliance_program": {
		{key: "process_inventory", kind: InputFile, required: true, accept: tabular, example: "process_inventory.csv",
			label: [2]string{"Inventario de procesos", "Process inventory"}, desc: [2]string{"Procesos, responsables, datos personales y riesgo inherente.", "Processes, owners, personal data and inherent risk."}},
		{key: "controls_catalog", kind: InputFile, required: false, accept: tabular, example: "controls_catalog.csv",
			label: [2]string{"Catálogo de controles", "Control catalog"}, desc: [2]string{"Controles existentes con frecuencia y estado.", "Existing controls with frequency and status."}},
		{key: "regulation_texts", kind: InputFolder, required: false, accept: docs, example: "reg_gdpr.md",
			label: [2]string{"Textos de las normas", "Regulation texts"}, desc: [2]string{"Resumen o texto de cada norma aplicable.", "Summary or text of each applicable regulation."}},
	},
	"product_launch": {
		{key: "market_sizing", kind: InputFile, required: true, accept: tabular, example: "market_sizing.csv",
			label: [2]string{"Tamaño de mercado", "Market sizing"}, desc: [2]string{"Población, mercado alcanzable, precio y penetración por mercado.", "Population, addressable market, price and penetration per market."}},
		{key: "channel_costs", kind: InputFile, required: true, accept: tabular, example: "channel_costs.csv",
			label: [2]string{"Costos por canal", "Channel costs"}, desc: [2]string{"Costo de adquisición, margen y tiempos por mercado y canal.", "Acquisition cost, margin and lead time per market and channel."}},
		{key: "product_brief", kind: InputFile, required: false, accept: docs, example: "product_brief.md",
			label: [2]string{"Ficha del producto", "Product brief"}, desc: [2]string{"Qué es el producto, precio sugerido y fecha objetivo.", "What the product is, suggested price and target date."}},
	},
	"post_merger_integration": {
		{key: "org_charts", kind: InputFolder, required: true, accept: tabular, perParam: "entities", example: "org_entidad_a.csv",
			label: [2]string{"Organigrama por entidad", "Org chart per entity"}, desc: [2]string{"Un archivo por entidad adquirida con plantilla y costo por función.", "One file per acquired entity with headcount and cost per function."}},
		{key: "systems_overlap", kind: InputFile, required: true, accept: tabular, example: "systems_overlap.csv",
			label: [2]string{"Traslape de sistemas", "Systems overlap"}, desc: [2]string{"Sistemas por función y si duplican a los del adquirente.", "Systems per function and whether they duplicate the acquirer's."}},
		{key: "synergy_target", kind: InputNumber, required: false, example: "1800000",
			label: [2]string{"Meta de sinergias anuales (USD)", "Annual synergy target (USD)"}, desc: [2]string{"Ahorro anual que se espera de la integración.", "Annual saving expected from the integration."}},
	},
	"year_end_audit": {
		{key: "trial_balances", kind: InputFolder, required: true, accept: tabular, perParam: "entities", example: "tb_matriz.csv",
			label: [2]string{"Balanzas por entidad", "Trial balances per entity"}, desc: [2]string{"Una balanza de comprobación por entidad auditada.", "One trial balance per audited entity."}},
		{key: "prior_workpapers", kind: InputFile, required: false, accept: docs, example: "prior_year_workpapers.md",
			label: [2]string{"Papeles de trabajo del año anterior", "Prior-year workpapers"}, desc: [2]string{"Hallazgos abiertos y materialidad del año anterior.", "Open findings and materiality of last year."}},
		{key: "materiality_pct", kind: InputNumber, required: false, example: "0.5",
			label: [2]string{"Materialidad (% de ingresos)", "Materiality (% of revenue)"}, desc: [2]string{"Porcentaje de los ingresos que se usa como materialidad.", "Percentage of revenue used as materiality."}},
	},
}

func inputI18nKey(tpl, key, part string) string { return "proj.in." + tpl + "." + key + "." + part }

func init() {
	for tpl, defs := range inputDefs {
		for _, d := range defs {
			i18n["es"][inputI18nKey(tpl, d.key, "label")], i18n["en"][inputI18nKey(tpl, d.key, "label")] = d.label[0], d.label[1]
			i18n["es"][inputI18nKey(tpl, d.key, "desc")], i18n["en"][inputI18nKey(tpl, d.key, "desc")] = d.desc[0], d.desc[1]
		}
	}
}

// builtinInputs returns the inputs and the sample dataset of a built-in template.
func builtinInputs(tplKey string) ([]InputSpec, string) {
	defs := inputDefs[tplKey]
	if len(defs) == 0 {
		return nil, ""
	}
	out := make([]InputSpec, 0, len(defs))
	for _, d := range defs {
		out = append(out, InputSpec{Key: d.key, Kind: d.kind, Required: d.required, Accept: d.accept, Example: d.example, PerParam: d.perParam, Options: d.options,
			LabelKey: inputI18nKey(tplKey, d.key, "label"), DescriptionKey: inputI18nKey(tplKey, d.key, "desc"),
			Label: d.label[0], Description: d.desc[0]})
	}
	return out, tplDemoSets[tplKey]
}

// DemoParams are the parameters the sample dataset of a draft is generated with:
// the sizes and lists the user chose (or the template defaults).
func (t Template) DemoParams(params map[string]string, locale string) demodata.Params {
	p := demodata.Params{Locale: locale, Count: map[string]int{}, Lists: map[string][]string{}}
	for _, tp := range t.Params {
		switch tp.kind() {
		case ParamList:
			raw := params[tp.Key]
			if raw == "" {
				raw = tp.Default
				if tp.DefaultKey != "" && hasKey(tp.DefaultKey) {
					raw = tr(locale, tp.DefaultKey, nil)
				}
			}
			var items []string
			for _, it := range splitList(raw) {
				if c := cleanItem(it); c != "" {
					items = append(items, c)
				}
			}
			if len(items) > tp.maxItems() {
				items = items[:tp.maxItems()]
			}
			p.Lists[tp.Key], p.Count[tp.Key] = items, len(items)
		case ParamNumber:
			p.Count[tp.Key] = t.paramCount(tp.Key, params, locale)
		}
	}
	return p
}
