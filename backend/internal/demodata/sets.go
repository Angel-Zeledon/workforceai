package demodata

import (
	"fmt"
	"strings"
)

// The other sample datasets. Each is small, fictional and arithmetically
// consistent (the identities are noted next to each generator and checked by
// the tests): P&L lines add up, assets equal liabilities plus equity, bank
// statements run from the opening to the closing balance, totals are sums.

const (
	DueDiligenceSet = "due_diligence"
	ProposalSet     = "proposal_50k"
	BudgetSet       = "annual_budget"
	ERPSet          = "erp_migration"
	ComplianceSet   = "compliance_program"
	AuditSet        = "year_end_audit"
	LaunchSet       = "product_launch"
	PostMergerSet   = "post_merger_integration"
	MarketSet       = "market_expansion"
	CashCloseSet    = "cash_close"
)

func init() {
	register(DueDiligenceSet, genDueDiligence)
	register(ProposalSet, genProposal)
	register(BudgetSet, genBudget)
	register(ERPSet, genERP)
	register(ComplianceSet, genCompliance)
	register(AuditSet, genAudit)
	register(LaunchSet, genLaunch)
	register(PostMergerSet, genPostMerger)
	register(MarketSet, genMarket)
	register(CashCloseSet, genCashClose)
}

func readme(p Params, title string, lines ...string) File {
	return md("", "README_datos_de_ejemplo.md", "# "+title+"\n\n"+p.fiction()+"\n\n"+strings.Join(lines, "\n")+"\n")
}

var targetNames = []string{"Nortec Soluciones", "Delta Logística", "Cima Alimentos", "Vértice Salud", "Áurea Energía", "Brisa Retail",
	"Tramo Industrial", "Luma Software", "Pampa Agro", "Faro Seguros"}

func pickName(list []string, i int, prefix string) string {
	if i < len(list) {
		return list[i]
	}
	return fmt.Sprintf("%s %d", prefix, i+1)
}

// ---- M&A due diligence ----
// Identities per entity and year: gross_profit = revenue - cost_of_sales; ebitda = gross_profit - operating_expenses;
// ebit = ebitda - depreciation_amortization; pre_tax_income = ebit - interest_expense; net_income = pre_tax_income - income_tax;
// total_assets = total_liabilities + total_equity.

var ddYears = []string{"fy2023", "fy2024", "fy2025"}

func genDueDiligence(p Params) Set {
	n := p.count("entities", 3, 1, 10)
	r := newRNG(p, DueDiligenceSet)
	var set Set
	var contracts, people, lit csvb
	contracts.row("entity", "contract_id", "counterparty", "type", "annual_value_usd", "start", "end", "change_of_control_clause", "exclusivity")
	people.row("entity", "function", "headcount", "avg_annual_cost_usd", "key_person_risk")
	lit.row("entity", "case_id", "type", "claim_usd", "probability", "provision_usd")
	kinds := []string{p.tr("Suministro", "Supply"), p.tr("Licencia", "License"), p.tr("Arrendamiento", "Lease"), p.tr("Distribución", "Distribution"), p.tr("Servicios", "Services"), p.tr("Financiamiento", "Financing")}
	funcs := []string{p.tr("Ventas", "Sales"), p.tr("Operaciones", "Operations"), p.tr("Finanzas", "Finance"), p.tr("Tecnología", "Technology"), p.tr("Recursos humanos", "HR")}
	cases := []string{p.tr("Laboral", "Labor"), p.tr("Fiscal", "Tax"), p.tr("Comercial", "Commercial"), p.tr("Propiedad intelectual", "Intellectual property")}
	probs := []struct {
		name string
		f    float64
	}{{p.tr("baja", "low"), 0.05}, {p.tr("media", "medium"), 0.35}, {p.tr("alta", "high"), 0.75}}
	yn := func(b bool) string {
		if b {
			return p.tr("sí", "yes")
		}
		return "no"
	}
	for i := 0; i < n; i++ {
		name := pickName(targetNames, i, p.tr("Objetivo", "Target"))
		code := fmt.Sprintf("T%02d", i+1)
		var f csvb
		f.row("entity", "metric", ddYears[0], ddYears[1], ddYears[2])
		rev := between(r, 8_000_000_00, 90_000_000_00)
		type yr struct{ rev, cogs, opex, da, intr, tax, ta, tl cents }
		ys := make([]yr, 3)
		for y := range ys {
			rev = frac(rev, 1.04+r.Float64()*0.14)
			cogs := frac(rev, 0.55+r.Float64()*0.12)
			opex := frac(rev, 0.18+r.Float64()*0.08)
			gp := rev - cogs
			ebitda := gp - opex
			da := frac(rev, 0.02+r.Float64()*0.02)
			intr := frac(rev, 0.005+r.Float64()*0.015)
			pre := ebitda - da - intr
			tax := cents(0)
			if pre > 0 {
				tax = frac(pre, 0.27)
			}
			ta := frac(rev, 0.8+r.Float64()*0.5)
			ys[y] = yr{rev, cogs, opex, da, intr, tax, ta, frac(ta, 0.45+r.Float64()*0.2)}
		}
		row := func(metric string, fn func(y yr) cents) {
			f.row(name, metric, fmtC(fn(ys[0])), fmtC(fn(ys[1])), fmtC(fn(ys[2])))
		}
		row("revenue", func(y yr) cents { return y.rev })
		row("cost_of_sales", func(y yr) cents { return y.cogs })
		row("gross_profit", func(y yr) cents { return y.rev - y.cogs })
		row("operating_expenses", func(y yr) cents { return y.opex })
		row("ebitda", func(y yr) cents { return y.rev - y.cogs - y.opex })
		row("depreciation_amortization", func(y yr) cents { return y.da })
		row("ebit", func(y yr) cents { return y.rev - y.cogs - y.opex - y.da })
		row("interest_expense", func(y yr) cents { return y.intr })
		row("pre_tax_income", func(y yr) cents { return y.rev - y.cogs - y.opex - y.da - y.intr })
		row("income_tax", func(y yr) cents { return y.tax })
		row("net_income", func(y yr) cents { return y.rev - y.cogs - y.opex - y.da - y.intr - y.tax })
		row("total_assets", func(y yr) cents { return y.ta })
		row("total_liabilities", func(y yr) cents { return y.tl })
		row("total_equity", func(y yr) cents { return y.ta - y.tl })
		set.Files = append(set.Files, file("financials", fmt.Sprintf("financials_%s_%s.csv", code, slug(name)), f.bytes()))
		for k := 0; k < 6; k++ {
			v := between(r, 40_000_00, 3_500_000_00)
			contracts.row(name, fmt.Sprintf("%s-C%02d", code, k+1), fmt.Sprintf("%s %s %d", p.tr("Contraparte", "Counterparty"), string(rune('A'+k)), i+1),
				kinds[k%len(kinds)], fmtC(v), fmt.Sprintf("202%d-0%d-01", 1+r.Intn(4), 1+r.Intn(9)), fmt.Sprintf("202%d-1%d-31", 6+r.Intn(4), r.Intn(3)),
				yn(r.Intn(4) == 0), yn(r.Intn(5) == 0))
		}
		for _, fn := range funcs {
			people.row(name, fn, fmt.Sprint(5+r.Intn(120)), fmtC(between(r, 18_000_00, 85_000_00)), yn(r.Intn(6) == 0))
		}
		for k := 0; k < 2; k++ {
			claim := between(r, 30_000_00, 1_200_000_00)
			pr := probs[r.Intn(len(probs))]
			lit.row(name, fmt.Sprintf("%s-L%02d", code, k+1), cases[r.Intn(len(cases))], fmtC(claim), pr.name, fmtC(frac(claim, pr.f)))
		}
	}
	set.Files = append(set.Files,
		file("contracts", "contracts_register.csv", contracts.bytes()),
		file("people", "people_summary.csv", people.bytes()),
		file("litigation", "litigation_register.csv", lit.bytes()),
		md("deal_brief", "deal_brief.md", p.tr("# Resumen de la operación (datos de ejemplo)\n\n"+fictionalES+"\n\nGrupo Aurora evalúa adquirir "+fmt.Sprint(n)+" empresa(s). Tesis: crecer en servicios regionales; precio máximo sujeto a un EBITDA ajustado sostenible y a que no existan cláusulas de cambio de control críticas. Plazo: 6 semanas.\n",
			"# Deal summary (sample data)\n\n"+fictionalEN+"\n\nGrupo Aurora is evaluating the acquisition of "+fmt.Sprint(n)+" company(ies). Thesis: grow regional services; the maximum price depends on a sustainable adjusted EBITDA and no critical change-of-control clauses. Timeline: 6 weeks.\n")),
		readme(p, p.tr("Datos de ejemplo: due diligence", "Sample data: due diligence"),
			p.tr("- `financials_*.csv`: estados de resultados y balance de 3 años por empresa (USD).", "- `financials_*.csv`: 3-year income statement and balance sheet per company (USD)."),
			p.tr("- `contracts_register.csv`, `people_summary.csv`, `litigation_register.csv`: registros del cuarto de datos.", "- `contracts_register.csv`, `people_summary.csv`, `litigation_register.csv`: data room registers.")))
	return set
}

// ---- the $50,000 proposal ----
// price_list.csv: amount = hours * rate and the amounts add up to exactly 50,000.00.

func genProposal(p Params) Set {
	roles := []struct {
		es, en string
		rate   cents
		cost   cents
	}{{"Consultor senior", "Senior consultant", 150_00, 98_00}, {"Analista", "Analyst", 95_00, 58_00}, {"Gerente de proyecto", "Project manager", 175_00, 118_00}, {"Especialista técnico", "Technical specialist", 130_00, 84_00}}
	items := []struct {
		es, en string
		role   int
		hours  int
	}{{"Diagnóstico y levantamiento", "Discovery and requirements", 0, 60}, {"Diseño de la solución", "Solution design", 3, 70}, {"Implementación", "Implementation", 1, 140},
		{"Integraciones", "Integrations", 3, 50}, {"Capacitación", "Training", 0, 24}, {"Gestión del proyecto", "Project management", 2, 40}}
	var price, cost csvb
	price.row("item", "role", "hours", "rate_usd", "amount_usd")
	var total cents
	for _, it := range items {
		amt := cents(it.hours) * roles[it.role].rate
		total += amt
		price.row(p.tr(it.es, it.en), p.tr(roles[it.role].es, roles[it.role].en), fmt.Sprint(it.hours), fmtC(roles[it.role].rate), fmtC(amt))
	}
	const target = cents(50_000_00)
	// the last line is a fixed-fee adjustment so the proposal totals exactly USD 50,000.00
	price.row(p.tr("Ajuste de alcance (tarifa fija)", "Scope adjustment (fixed fee)"), "-", "1", fmtC(target-total), fmtC(target-total))
	cost.row("role", "hourly_cost_usd")
	for _, r := range roles {
		cost.row(p.tr(r.es, r.en), fmtC(r.cost))
	}
	var past csvb
	past.row("proposal_id", "client", "value_usd", "margin_pct", "outcome")
	r := newRNG(p, ProposalSet)
	clients := []string{"Grupo Sierra", "Cadena Mirador", "Transportes Andes", "Clínica Horizonte", "Textiles del Valle", "Agroexport Sur"}
	for i, c := range clients {
		past.row(fmt.Sprintf("P-%03d", 101+i), c, fmtC(between(r, 18_000_00, 90_000_00)), fmt.Sprintf("%.1f", 24+r.Float64()*14), []string{p.tr("ganada", "won"), p.tr("perdida", "lost")}[r.Intn(2)])
	}
	return Set{Files: []File{
		md("client_brief", "client_brief.md", p.tr("# Cliente nuevo: Distribuidora Pacífico (datos de ejemplo)\n\n"+fictionalES+"\n\n- Sector: distribución mayorista; 220 empleados; 3 centros logísticos.\n- Necesidad: integrar pedidos, inventario y facturación en una sola plataforma en 4 meses.\n- Presupuesto indicado: hasta USD 50,000. Pago propuesto: 40% al inicio, 30% al hito 1, 30% a la entrega.\n- Riesgos: dependencia de un solo ERP antiguo; el cliente pide penalidades por retraso.\n",
			"# New client: Distribuidora Pacifico (sample data)\n\n"+fictionalEN+"\n\n- Sector: wholesale distribution; 220 employees; 3 logistics centers.\n- Need: integrate orders, inventory and invoicing in one platform in 4 months.\n- Stated budget: up to USD 50,000. Proposed payment: 40% at start, 30% at milestone 1, 30% on delivery.\n- Risks: dependence on one legacy ERP; the client asks for late-delivery penalties.\n")),
		file("price_list", "price_list.csv", price.bytes()),
		file("cost_rates", "cost_rates.csv", cost.bytes()),
		file("past_proposals", "past_proposals.csv", past.bytes()),
		readme(p, p.tr("Datos de ejemplo: propuesta de USD 50,000", "Sample data: USD 50,000 proposal"),
			p.tr("- `price_list.csv` suma exactamente USD 50,000.00; `cost_rates.csv` trae el costo por hora de cada rol; el margen presupuestado es 31%.", "- `price_list.csv` adds up to exactly USD 50,000.00; `cost_rates.csv` has the hourly cost per role; the budgeted margin is 31%.")),
	}}
}

// ---- annual budget ----
// prior_year_actuals.csv: gross_profit = revenue - cogs; ebitda = gross_profit - opex; the TOTAL row is the sum of the rows.

func genBudget(p Params) Set {
	units := p.list("units", p.tr("Retail", "Retail"), p.tr("Servicios", "Services"), "Industrial")
	countries := p.list("countries", p.tr("México", "Mexico"), "Colombia", "Chile", p.tr("España", "Spain"))
	r := newRNG(p, BudgetSet)
	var act, hc, as csvb
	act.row("unit", "country", "revenue_usd", "cogs_usd", "gross_profit_usd", "opex_usd", "ebitda_usd", "headcount")
	hc.row("unit", "country", "function", "headcount")
	as.row("country", "inflation_pct", "wage_growth_pct", "gdp_growth_pct", "fx_local_per_usd")
	var tRev, tCogs, tGP, tOpex, tE cents
	var tHC int
	for _, u := range units {
		for _, c := range countries {
			rev := between(r, 1_500_000_00, 24_000_000_00)
			cogs := frac(rev, 0.5+r.Float64()*0.15)
			opex := frac(rev, 0.15+r.Float64()*0.1)
			h := 12 + r.Intn(400)
			act.row(u, c, fmtC(rev), fmtC(cogs), fmtC(rev-cogs), fmtC(opex), fmtC(rev-cogs-opex), fmt.Sprint(h))
			tRev, tCogs, tGP, tOpex, tE, tHC = tRev+rev, tCogs+cogs, tGP+rev-cogs, tOpex+opex, tE+rev-cogs-opex, tHC+h
			rest := h
			for k, fn := range []string{p.tr("Ventas", "Sales"), p.tr("Operaciones", "Operations"), p.tr("Administración", "Administration")} {
				v := rest / (3 - k)
				if k == 2 {
					v = rest
				}
				rest -= v
				hc.row(u, c, fn, fmt.Sprint(v))
			}
		}
	}
	act.row("TOTAL", "", fmtC(tRev), fmtC(tCogs), fmtC(tGP), fmtC(tOpex), fmtC(tE), fmt.Sprint(tHC))
	for _, c := range countries {
		as.row(c, fmt.Sprintf("%.1f", 2+r.Float64()*6), fmt.Sprintf("%.1f", 3+r.Float64()*6), fmt.Sprintf("%.1f", 0.5+r.Float64()*3.5), fmt.Sprintf("%.2f", 1+r.Float64()*20))
	}
	return Set{Files: []File{file("prior_actuals", "prior_year_actuals.csv", act.bytes()), file("assumptions", "macro_assumptions.csv", as.bytes()),
		file("headcount", "headcount_current.csv", hc.bytes()),
		readme(p, p.tr("Datos de ejemplo: presupuesto anual", "Sample data: annual budget"),
			p.tr("- `prior_year_actuals.csv`: resultados reales del año anterior por unidad y país, con fila TOTAL.", "- `prior_year_actuals.csv`: last year's actuals per unit and country, with a TOTAL row."))}}
}

// ---- ERP migration ----

func genERP(p Params) Set {
	streams := p.list("streams", p.tr("Finanzas", "Finance"), p.tr("Compras", "Procurement"), p.tr("Ventas", "Sales"), p.tr("Inventario", "Inventory"), p.tr("Recursos humanos", "HR"))
	r := newRNG(p, ERPSet)
	var sys, vol csvb
	sys.row("stream", "system", "vendor", "users", "age_years", "integrations", "criticality")
	vol.row("stream", "data_object", "records", "clean_pct", "owner")
	crit := []string{p.tr("alta", "high"), p.tr("media", "medium"), p.tr("baja", "low")}
	for i, s := range streams {
		for k := 0; k < 2; k++ {
			sys.row(s, fmt.Sprintf("%s-%d", strings.ToUpper(slug(s))[:min(3, len(slug(s)))], k+1), []string{"Legacy Systems SA", "Contabilium", "InfoPyme", "Planix"}[(i+k)%4],
				fmt.Sprint(10+r.Intn(300)), fmt.Sprint(2+r.Intn(14)), fmt.Sprint(r.Intn(9)), crit[r.Intn(3)])
		}
		for _, o := range []string{p.tr("Maestros", "Master data"), p.tr("Transacciones abiertas", "Open transactions"), p.tr("Histórico", "History")} {
			vol.row(s, o, fmt.Sprint(1000+r.Intn(900_000)), fmt.Sprintf("%.1f", 62+r.Float64()*36), p.tr("Responsable de ", "Owner of ")+s)
		}
	}
	return Set{Files: []File{file("systems", "systems_inventory.csv", sys.bytes()), file("data_volumes", "data_volumes.csv", vol.bytes()),
		readme(p, p.tr("Datos de ejemplo: migración de ERP", "Sample data: ERP migration"), p.tr("- Inventario de sistemas y volúmenes de datos por frente de trabajo.", "- Systems inventory and data volumes per workstream."))},
		Values: map[string]string{"program_budget": "2400000"}}
}

// ---- compliance ----

func genCompliance(p Params) Set {
	regs := p.list("regulations", "GDPR", "SOX", "ISO 27001", p.tr("Antilavado", "Anti-money laundering"))
	countries := p.list("countries", p.tr("México", "Mexico"), p.tr("España", "Spain"), "Chile")
	r := newRNG(p, ComplianceSet)
	var proc, ctl csvb
	proc.row("process_id", "process", "owner_function", "personal_data", "countries_in_scope", "inherent_risk_1_5")
	ctl.row("control_id", "regulation", "control", "frequency", "owner", "status")
	procs := []string{p.tr("Alta de clientes", "Customer onboarding"), p.tr("Nómina", "Payroll"), p.tr("Pagos a proveedores", "Supplier payments"), p.tr("Cierre contable", "Accounting close"), p.tr("Acceso a sistemas", "System access"), p.tr("Gestión de contratos", "Contract management")}
	funcs := []string{p.tr("Ventas", "Sales"), "RRHH", p.tr("Finanzas", "Finance"), p.tr("Finanzas", "Finance"), "TI", "Legal"}
	for i, pr := range procs {
		proc.row(fmt.Sprintf("PR-%02d", i+1), pr, funcs[i], []string{"no", p.tr("sí", "yes")}[r.Intn(2)], strings.Join(countries, "; "), fmt.Sprint(1+r.Intn(5)))
	}
	status := []string{p.tr("implementado", "implemented"), p.tr("parcial", "partial"), p.tr("faltante", "missing")}
	set := Set{}
	for _, rg := range regs {
		for k := 0; k < 3; k++ {
			ctl.row(fmt.Sprintf("%s-%02d", strings.ToUpper(slug(rg))[:min(4, len(slug(rg)))], k+1), rg, p.tr("Control de ejemplo ", "Sample control ")+fmt.Sprint(k+1),
				[]string{p.tr("mensual", "monthly"), p.tr("trimestral", "quarterly"), p.tr("anual", "annual")}[k], funcs[r.Intn(len(funcs))], status[r.Intn(3)])
		}
		set.Files = append(set.Files, md("regulation_texts", "reg_"+slug(rg)+".md", p.tr("# "+rg+" (resumen ficticio)\n\n"+fictionalES+"\n\n1. Mantener un inventario actualizado de procesos y datos alcanzados.\n2. Designar un responsable y documentar políticas.\n3. Probar los controles al menos una vez al año y conservar evidencia.\n",
			"# "+rg+" (fictional summary)\n\n"+fictionalEN+"\n\n1. Keep an up-to-date inventory of the processes and data in scope.\n2. Appoint an owner and document policies.\n3. Test the controls at least once a year and keep evidence.\n")))
	}
	set.Files = append(set.Files, file("process_inventory", "process_inventory.csv", proc.bytes()), file("controls_catalog", "controls_catalog.csv", ctl.bytes()),
		readme(p, p.tr("Datos de ejemplo: cumplimiento", "Sample data: compliance"), p.tr("- Inventario de procesos, catálogo de controles y resúmenes de normas.", "- Process inventory, control catalog and regulation summaries.")))
	return set
}

// ---- year-end audit: a trial balance per entity (same generator as the group close) ----

func genAudit(p Params) Set {
	entities := p.list("entities", p.tr("Matriz", "Parent"), p.tr("Filial 1", "Subsidiary 1"), p.tr("Filial 2", "Subsidiary 2"))
	q := p
	q.Count = map[string]int{"subsidiaries": len(entities)}
	subs, pairs := gcBuild(q)
	r := newRNG(p, "audit_tb")
	var set Set
	for i, s := range subs {
		var c csvb
		c.row("entity", "period", "currency", "account", "account_name", "debit", "credit")
		for _, l := range gcTB(r, s, pairs, subs) {
			dr, cr := "", ""
			if l.dr != 0 {
				dr = fmtC(l.dr)
			}
			if l.cr != 0 {
				cr = fmtC(l.cr)
			}
			c.row(entities[i], GroupPeriod, s.c.Ccy, localCode(s.c, l.a.Code), l.a.name(p), dr, cr)
		}
		set.Files = append(set.Files, file("trial_balances", fmt.Sprintf("tb_%s.csv", slug(entities[i])), c.bytes()))
	}
	set.Files = append(set.Files,
		md("prior_workpapers", "prior_year_workpapers.md", p.tr("# Papeles de trabajo del año anterior (ejemplo)\n\n"+fictionalES+"\n\n- Opinión: sin salvedades.\n- Hallazgos abiertos: conciliación de inventarios con diferencia recurrente; cortes de ventas en diciembre.\n- Materialidad usada: 0.5% de los ingresos.\n",
			"# Prior-year workpapers (sample)\n\n"+fictionalEN+"\n\n- Opinion: unqualified.\n- Open findings: inventory reconciliation with a recurring difference; December sales cut-off.\n- Materiality used: 0.5% of revenue.\n")),
		readme(p, p.tr("Datos de ejemplo: auditoría de cierre de año", "Sample data: year-end audit"), p.tr("- Una balanza de comprobación por entidad (debe = haber).", "- One trial balance per entity (debit = credit).")))
	set.Values = map[string]string{"materiality_pct": "0.5"}
	return set
}

// ---- product launch ----

func genLaunch(p Params) Set {
	markets := p.list("markets", p.tr("México", "Mexico"), "Colombia", "Chile", p.tr("España", "Spain"))
	channels := p.list("channels", p.tr("Tienda en línea", "Online store"), p.tr("Distribuidores", "Distributors"), "Retail")
	r := newRNG(p, LaunchSet)
	var ms, cc csvb
	ms.row("market", "population_m", "addressable_m", "avg_price_usd", "year1_penetration_pct", "year1_revenue_potential_usd")
	cc.row("market", "channel", "cac_usd", "gross_margin_pct", "lead_time_days")
	for _, m := range markets {
		pop := 5 + r.Float64()*120
		addr := pop * (0.05 + r.Float64()*0.2)
		price := 20 + r.Float64()*80
		pen := 0.5 + r.Float64()*3
		// potential = addressable (millions) * 1e6 * penetration% * price, rounded to cents
		pot := cents(addr*1e6*pen/100*price*100 + 0.5)
		ms.row(m, fmt.Sprintf("%.2f", pop), fmt.Sprintf("%.2f", addr), fmt.Sprintf("%.2f", price), fmt.Sprintf("%.2f", pen), fmtC(pot))
		for _, ch := range channels {
			cc.row(m, ch, fmt.Sprintf("%.2f", 6+r.Float64()*40), fmt.Sprintf("%.1f", 25+r.Float64()*35), fmt.Sprint(5+r.Intn(40)))
		}
	}
	return Set{Files: []File{file("market_sizing", "market_sizing.csv", ms.bytes()), file("channel_costs", "channel_costs.csv", cc.bytes()),
		md("product_brief", "product_brief.md", p.tr("# Producto: Aurora Hogar Inteligente (ejemplo)\n\n"+fictionalES+"\n\nHub de domótica con suscripción. Precio sugerido USD 79 + USD 4/mes. Fecha objetivo de lanzamiento: en 5 meses.\n",
			"# Product: Aurora Smart Home (sample)\n\n"+fictionalEN+"\n\nHome-automation hub with a subscription. Suggested price USD 79 + USD 4/month. Target launch date: in 5 months.\n")),
		readme(p, p.tr("Datos de ejemplo: lanzamiento de producto", "Sample data: product launch"), p.tr("- Tamaño de mercado y costos por canal.", "- Market sizing and channel costs."))}}
}

// ---- post-merger integration ----

func genPostMerger(p Params) Set {
	funcs := p.list("functions", p.tr("Finanzas", "Finance"), p.tr("Tecnología", "Technology"), p.tr("Recursos humanos", "HR"), "Legal")
	ents := p.list("entities", p.tr("Entidad A", "Entity A"), p.tr("Entidad B", "Entity B"))
	r := newRNG(p, PostMergerSet)
	var set Set
	var ov csvb
	ov.row("entity", "function", "system", "overlaps_with_acquirer", "annual_license_usd")
	for _, e := range ents {
		var c csvb
		c.row("entity", "function", "headcount", "avg_salary_usd", "manager")
		for _, f := range funcs {
			c.row(e, f, fmt.Sprint(3+r.Intn(60)), fmtC(between(r, 22_000_00, 90_000_00)), p.tr("Responsable de ", "Head of ")+f)
			ov.row(e, f, fmt.Sprintf("SIS-%s-%d", strings.ToUpper(slug(f))[:min(3, len(slug(f)))], 1+r.Intn(3)), []string{p.tr("sí", "yes"), "no"}[r.Intn(2)], fmtC(between(r, 5_000_00, 140_000_00)))
		}
		set.Files = append(set.Files, file("org_charts", "org_"+slug(e)+".csv", c.bytes()))
	}
	set.Files = append(set.Files, file("systems_overlap", "systems_overlap.csv", ov.bytes()),
		readme(p, p.tr("Datos de ejemplo: integración posterior a la fusión", "Sample data: post-merger integration"), p.tr("- Organigrama resumido por entidad y traslape de sistemas.", "- Summary org chart per entity and systems overlap.")))
	set.Values = map[string]string{"synergy_target": "1800000"}
	return set
}

// ---- market expansion ----

func genMarket(p Params) Set {
	n := p.count("countries", 5, 1, 12)
	r := newRNG(p, MarketSet)
	var md1, comp csvb
	md1.row("country", "population_m", "gdp_per_capita_usd", "internet_pct", "corporate_tax_pct", "monthly_min_wage_usd", "market_size_usd_m")
	comp.row("country", "competitor", "share_pct", "price_index")
	for i := 0; i < n; i++ {
		c := Countries[i]
		name := p.tr(c.ES, c.EN)
		pop := 4 + r.Float64()*130
		md1.row(name, fmt.Sprintf("%.1f", pop), fmt.Sprint(4000+r.Intn(36000)), fmt.Sprintf("%.0f", 55+r.Float64()*40), fmt.Sprintf("%.0f", 20+r.Float64()*15), fmt.Sprint(150+r.Intn(1100)), fmt.Sprintf("%.0f", 80+r.Float64()*2200))
		left := 100.0
		for k, nm := range []string{"Alfa", "Beta", "Gama"} {
			sh := 8 + r.Float64()*22
			left -= sh
			comp.row(name, nm+" "+c.Code, fmt.Sprintf("%.1f", sh), fmt.Sprintf("%.2f", 0.8+0.1*float64(k)+r.Float64()*0.3))
		}
		_ = left
	}
	return Set{Files: []File{file("market_data", "market_data.csv", md1.bytes()), file("competitors", "competitors.csv", comp.bytes()),
		md("regulatory_notes", "regulatory_notes.md", p.tr("# Notas regulatorias (ejemplo)\n\n"+fictionalES+"\n\n- Se requiere una entidad local para facturar.\n- Los contratos laborales siguen reglas locales; revisar periodos de prueba.\n",
			"# Regulatory notes (sample)\n\n"+fictionalEN+"\n\n- A local entity is required to invoice.\n- Labor contracts follow local rules; review probation periods.\n")),
		readme(p, p.tr("Datos de ejemplo: expansión a nuevos mercados", "Sample data: new market expansion"), p.tr("- Datos de mercado y competidores por país.", "- Market data and competitors per country."))}}
}

// ---- quarterly cash close ----
// A bank statement starts with an opening row and every balance is the previous one minus debits plus credits.

func genCashClose(p Params) Set {
	accounts := p.count("accounts", 6, 1, 12)
	clients := p.count("clients", 1, 1, 10)
	r := newRNG(p, CashCloseSet)
	var set Set
	var ar, ap csvb
	ar.row("client", "invoice", "customer", "issue_date", "due_date", "amount_usd", "paid_usd")
	ap.row("client", "bill", "supplier", "due_date", "amount_usd")
	for c := 0; c < clients; c++ {
		cl := fmt.Sprintf("C%02d", c+1)
		for a := 0; a < accounts; a++ {
			var st csvb
			st.row("date", "description", "debit", "credit", "balance")
			bal := between(r, 80_000_00, 900_000_00)
			st.row("2026-07-01", p.tr("Saldo inicial", "Opening balance"), "", "", fmtC(bal))
			for k := 0; k < 14; k++ {
				amt := between(r, 1_200_00, 60_000_00)
				d := fmt.Sprintf("2026-%02d-%02d", 7+k/5, 1+(k%5)*5+r.Intn(4))
				if r.Intn(2) == 0 {
					bal -= amt
					st.row(d, p.tr("Pago a proveedor", "Supplier payment"), fmtC(amt), "", fmtC(bal))
				} else {
					bal += amt
					st.row(d, p.tr("Cobro de cliente", "Customer receipt"), "", fmtC(amt), fmtC(bal))
				}
			}
			set.Files = append(set.Files, file("bank_statements", fmt.Sprintf("bank_%s_%s.csv", cl, string(rune('A'+a))), st.bytes()))
		}
		for k := 0; k < 10; k++ {
			amt := between(r, 2_000_00, 80_000_00)
			paid := cents(0)
			if r.Intn(3) == 0 {
				paid = frac(amt, 0.5)
			}
			ar.row(cl, fmt.Sprintf("%s-F%03d", cl, k+1), fmt.Sprintf("%s %d", p.tr("Cliente", "Customer"), k+1), fmt.Sprintf("2026-0%d-1%d", 6+r.Intn(4), r.Intn(9)),
				fmt.Sprintf("2026-%02d-%02d", 8+r.Intn(4), 1+r.Intn(27)), fmtC(amt), fmtC(paid))
			if k < 6 {
				ap.row(cl, fmt.Sprintf("%s-B%03d", cl, k+1), fmt.Sprintf("%s %d", p.tr("Proveedor", "Supplier"), k+1), fmt.Sprintf("2026-%02d-%02d", 10+r.Intn(3), 1+r.Intn(27)), fmtC(between(r, 1_500_00, 55_000_00)))
			}
		}
	}
	set.Files = append(set.Files, file("ar_ledger", "ar_ledger.csv", ar.bytes()), file("payables_due", "payables_due.csv", ap.bytes()),
		readme(p, p.tr("Datos de ejemplo: cierre de caja", "Sample data: cash close"), p.tr("- Estados de cuenta bancarios (saldo corrido), cartera por cobrar y pagos próximos.", "- Bank statements (running balance), receivables ledger and upcoming payments.")))
	return set
}
