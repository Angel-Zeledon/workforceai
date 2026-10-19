package demodata

import (
	"fmt"
	"sort"
	"strings"
)

// Sample datasets of the industry templates (docs/architecture/organization.md). They live in their
// own registry: the main one backs the inputs of project templates and its size is asserted by tests.
// Everything is FICTIONAL, deterministic per seed and arithmetically consistent (the tests re-read the
// CSVs): invoice lines add up to their totals, a bank balance runs from opening to closing, time
// entries multiply to their amounts, stock is received minus sold, order totals are lines plus
// shipping, campaign spend is the sum of its channels, trial balances balance, and the mirrored
// intercompany balances agree once converted to USD.

const (
	AccountingFirmSet   = "accounting_firm"
	LawFirmSet          = "law_firm"
	EcommerceStoreSet   = "ecommerce_store"
	MarketingAgencySet  = "marketing_agency"
	MultinationalGroupS = "multinational_group"
)

var industrySets = map[string]generator{
	AccountingFirmSet:   genAccountingFirm,
	LawFirmSet:          genLawFirm,
	EcommerceStoreSet:   genEcommerceStore,
	MarketingAgencySet:  genMarketingAgency,
	MultinationalGroupS: genMultinationalGroup,
}

// HasIndustry reports whether an industry dataset exists.
func HasIndustry(name string) bool { _, ok := industrySets[name]; return ok }

// IndustryNames lists the industry datasets (sorted).
func IndustryNames() []string {
	out := make([]string, 0, len(industrySets))
	for k := range industrySets {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// GenerateIndustry builds an industry dataset.
func GenerateIndustry(name string, p Params) (Set, error) {
	g, ok := industrySets[name]
	if !ok {
		return Set{}, fmt.Errorf("unknown industry demo dataset %q", name)
	}
	s := g(p)
	for i := range s.Files {
		if len(s.Files[i].Data) == 0 {
			return Set{}, fmt.Errorf("industry demo dataset %q produced an empty file %q", name, s.Files[i].Name)
		}
	}
	return s, nil
}

// lang is "es" (default), "en" or "pt".
func (p Params) lang() string {
	l := strings.ToLower(p.Locale)
	switch {
	case strings.HasPrefix(l, "pt"):
		return "pt"
	case strings.HasPrefix(l, "en"):
		return "en"
	}
	return "es"
}

func (p Params) tr3(es, en, pt string) string {
	switch p.lang() {
	case "pt":
		return pt
	case "en":
		return en
	}
	return es
}

func industryReadme(p Params, title string, lines ...string) File {
	notice := p.tr3(fictionalES, fictionalEN, "Dados FICTÍCIOS gerados para praticar: não correspondem a nenhuma empresa real.")
	return md("", "README_"+p.lang()+".md", "# "+title+"\n\n"+notice+"\n\n"+strings.Join(lines, "\n")+"\n")
}

var companyNames = []string{"Aurora Textiles", "Brújula Studio", "Cobre y Sol", "Dínamo Labs", "Estrella Norte", "Faro Digital", "Garza Foods",
	"Horizonte Bio", "Isla Verde", "Jade Logistics", "Kiwi Café", "Lumbre Crafts", "Mirador Hotels", "Nube Cloud", "Olivo Gourmet",
	"Pampa Wear", "Quasar Energy", "Rio Claro", "Sendero Tours", "Tinta Print", "Umbral Health", "Valle Seco", "Xilo Design", "Yunque Steel", "Zafiro Jewels"}

func companyName(i int) string {
	if i < len(companyNames) {
		return companyNames[i]
	}
	return fmt.Sprintf("Empresa Ficticia %d", i+1)
}

func day(i int) string { return fmt.Sprintf("2026-%02d-%02d", 1+(i/28)%12, 1+i%28) }

// ---- accounting firm ----

func genAccountingFirm(p Params) Set {
	n := p.count("clients", 8, 3, 25)
	r := newRNG(p, AccountingFirmSet)
	var clients, inv, lines, bank csvb
	clients.row("client_id", "name", "tax_id", "monthly_fee")
	inv.row("invoice_id", "client_id", "date", "subtotal", "tax_rate_pct", "tax", "total")
	lines.row("invoice_id", "line_no", "description", "quantity", "unit_price", "line_total")
	descs := []string{p.tr3("Contabilidad mensual", "Monthly bookkeeping", "Contabilidade mensal"),
		p.tr3("Declaración de impuestos", "Tax filing", "Declaração de impostos"),
		p.tr3("Nómina", "Payroll", "Folha de pagamento"),
		p.tr3("Asesoría", "Advisory", "Consultoria")}
	rates := []int64{1600, 1900, 2100} // basis points
	var received cents
	for i := 0; i < n; i++ {
		cid := fmt.Sprintf("C%03d", i+1)
		clients.row(cid, companyName(i), fmt.Sprintf("FIC-%06d", 100000+r.Intn(899999)), fmtC(between(r, 30000, 250000)))
		iid := fmt.Sprintf("INV-%04d", i+1)
		var sub cents
		for l := 0; l < 1+r.Intn(3); l++ {
			q := cents(1 + r.Intn(4))
			up := between(r, 5000, 90000)
			lines.row(iid, fmt.Sprint(l+1), descs[(i+l)%len(descs)], fmt.Sprint(q), fmtC(up), fmtC(q*up))
			sub += q * up
		}
		bp := rates[r.Intn(len(rates))]
		tax := (sub*bp + 5000) / 10000
		inv.row(iid, cid, day(i*3), fmtC(sub), fmt.Sprintf("%.2f", float64(bp)/100), fmtC(tax), fmtC(sub+tax))
		if r.Intn(4) != 0 { // most invoices are paid
			received += sub + tax
		}
	}
	opening := between(r, 500000, 2000000)
	bal := opening
	bank.row("date", "description", "amount", "balance")
	bank.row("2026-01-01", p.tr3("Saldo inicial", "Opening balance", "Saldo inicial"), "0.00", fmtC(bal))
	pay := received
	for i := 0; pay > 0; i++ {
		a := min(pay, between(r, 50000, 600000))
		pay -= a
		bal += a
		bank.row(day(i*2+1), p.tr3("Cobro de cliente", "Customer receipt", "Recebimento de cliente"), fmtC(a), fmtC(bal))
	}
	for i := 0; i < 6; i++ {
		a := between(r, 20000, 150000)
		bal -= a
		bank.row(day(i*5+2), p.tr3("Gasto del despacho", "Firm expense", "Despesa do escritório"), fmtC(-a), fmtC(bal))
	}
	return Set{Files: []File{
		industryReadme(p, p.tr3("Despacho contable", "Accounting firm", "Escritório de contabilidade"),
			"- clients.csv, invoices.csv, invoice_lines.csv, bank_movements.csv"),
		file("", "clients.csv", clients.bytes()), file("", "invoices.csv", inv.bytes()),
		file("", "invoice_lines.csv", lines.bytes()), file("", "bank_movements.csv", bank.bytes()),
	}, Values: map[string]string{"opening_balance": fmtC(opening), "closing_balance": fmtC(bal)}}
}

// ---- law firm ----

func genLawFirm(p Params) Set {
	n := p.count("matters", 8, 3, 25)
	r := newRNG(p, LawFirmSet)
	var matters, te, dl csvb
	matters.row("matter_id", "client", "type", "hourly_rate", "status", "total_hours", "total_amount")
	te.row("entry_id", "matter_id", "date", "lawyer", "hours", "hourly_rate", "amount")
	dl.row("deadline_id", "matter_id", "description", "due_date", "status")
	types := []string{p.tr3("Contrato mercantil", "Commercial contract", "Contrato comercial"), p.tr3("Laboral", "Employment", "Trabalhista"),
		p.tr3("Propiedad intelectual", "Intellectual property", "Propriedade intelectual"), p.tr3("Litigio", "Litigation", "Contencioso")}
	statuses := []string{p.tr3("abierto", "open", "aberto"), p.tr3("en espera", "on hold", "em espera"), p.tr3("cerrado", "closed", "encerrado")}
	lawyers := []string{"A. Ríos", "B. Costa", "C. Navarro", "D. Silva"}
	eid, did := 0, 0
	for i := 0; i < n; i++ {
		mid := fmt.Sprintf("M-%03d", i+1)
		rate := cents(10000 * (6 + r.Intn(10))) // whole dollars x 100: quarter hours stay exact
		var q, amt cents
		for e := 0; e < 2+r.Intn(5); e++ {
			eid++
			qs := cents(1 + r.Intn(16)) // quarter hours
			a := qs * rate / 4
			te.row(fmt.Sprintf("T%04d", eid), mid, day(i*4+e), lawyers[r.Intn(len(lawyers))], fmt.Sprintf("%.2f", float64(qs)/4), fmtC(rate), fmtC(a))
			q += qs
			amt += a
		}
		matters.row(mid, companyName(i), types[i%len(types)], fmtC(rate), statuses[r.Intn(len(statuses))], fmt.Sprintf("%.2f", float64(q)/4), fmtC(amt))
		for d := 0; d < 1+r.Intn(2); d++ {
			did++
			dl.row(fmt.Sprintf("D%03d", did), mid, p.tr3("Presentar escrito", "File brief", "Protocolar petição"), day(60+did*3), p.tr3("pendiente", "pending", "pendente"))
		}
	}
	return Set{Files: []File{
		industryReadme(p, p.tr3("Despacho de abogados", "Law firm", "Escritório de advocacia"), "- matters.csv, time_entries.csv, deadlines.csv"),
		file("", "matters.csv", matters.bytes()), file("", "time_entries.csv", te.bytes()), file("", "deadlines.csv", dl.bytes()),
	}}
}

// ---- e-commerce store ----

func genEcommerceStore(p Params) Set {
	np := p.count("products", 10, 4, 40)
	no := p.count("orders", 25, 5, 100)
	r := newRNG(p, EcommerceStoreSet)
	var prod, inv, orders, lines, rets csvb
	prod.row("sku", "name", "category", "price", "cost")
	inv.row("sku", "received", "sold", "on_hand")
	orders.row("order_id", "date", "customer", "subtotal", "shipping", "total")
	lines.row("order_id", "line_no", "sku", "quantity", "unit_price", "line_total")
	rets.row("order_id", "sku", "quantity", "refund", "reason")
	cats := []string{p.tr3("Hogar", "Home", "Casa"), p.tr3("Moda", "Fashion", "Moda"), p.tr3("Deportes", "Sports", "Esportes")}
	reasons := []string{p.tr3("talla incorrecta", "wrong size", "tamanho errado"), p.tr3("defectuoso", "defective", "defeituoso")}
	price := make([]cents, np)
	sold := make([]cents, np)
	for i := 0; i < np; i++ {
		price[i] = between(r, 1500, 30000)
		prod.row(fmt.Sprintf("SKU-%03d", i+1), fmt.Sprintf("%s %d", p.tr3("Producto", "Product", "Produto"), i+1), cats[i%len(cats)], fmtC(price[i]), fmtC(frac(price[i], 0.55)))
	}
	for o := 0; o < no; o++ {
		oid := fmt.Sprintf("O-%04d", o+1)
		var sub cents
		for l := 0; l < 1+r.Intn(3); l++ {
			k := r.Intn(np)
			q := cents(1 + r.Intn(3))
			lines.row(oid, fmt.Sprint(l+1), fmt.Sprintf("SKU-%03d", k+1), fmt.Sprint(q), fmtC(price[k]), fmtC(q*price[k]))
			sub += q * price[k]
			sold[k] += q
			if r.Intn(15) == 0 {
				rets.row(oid, fmt.Sprintf("SKU-%03d", k+1), "1", fmtC(price[k]), reasons[r.Intn(len(reasons))])
			}
		}
		ship := between(r, 300, 1500)
		orders.row(oid, day(o), fmt.Sprintf("CUST-%03d", 1+r.Intn(40)), fmtC(sub), fmtC(ship), fmtC(sub+ship))
	}
	for i := 0; i < np; i++ {
		recv := sold[i] + cents(r.Intn(60))
		inv.row(fmt.Sprintf("SKU-%03d", i+1), fmt.Sprint(recv), fmt.Sprint(sold[i]), fmt.Sprint(recv-sold[i]))
	}
	return Set{Files: []File{
		industryReadme(p, p.tr3("Tienda en línea", "Online store", "Loja online"), "- products.csv, inventory.csv, orders.csv, order_lines.csv, returns.csv"),
		file("", "products.csv", prod.bytes()), file("", "inventory.csv", inv.bytes()), file("", "orders.csv", orders.bytes()),
		file("", "order_lines.csv", lines.bytes()), file("", "returns.csv", rets.bytes()),
	}}
}

// ---- marketing agency ----

func genMarketingAgency(p Params) Set {
	n := p.count("campaigns", 6, 3, 20)
	r := newRNG(p, MarketingAgencySet)
	var camp, spend, cal, rep csvb
	camp.row("campaign_id", "client", "name", "total_spend")
	spend.row("campaign_id", "channel", "month", "impressions", "clicks", "spend", "cpc")
	cal.row("date", "campaign_id", "channel", "piece", "owner", "status")
	rep.row("client", "month", "spend", "clicks")
	channels := []string{"search", "social", "email", "display"}
	months := []string{"2026-01", "2026-02", "2026-03"}
	type key struct{ client, month string }
	repSpend, repClicks := map[key]cents{}, map[key]cents{}
	var order []key
	for i := 0; i < n; i++ {
		cid, client := fmt.Sprintf("CP-%03d", i+1), companyName(i%8)
		var total cents
		for _, m := range months {
			for _, ch := range channels[:2+r.Intn(3)] {
				imp := between(r, 20000, 400000)
				clk := between(r, 300, 9000)
				sp := between(r, 50000, 900000)
				spend.row(cid, ch, m, fmt.Sprint(imp), fmt.Sprint(clk), fmtC(sp), fmtC((sp+clk/2)/clk))
				total += sp
				k := key{client, m}
				if _, ok := repSpend[k]; !ok {
					order = append(order, k)
				}
				repSpend[k] += sp
				repClicks[k] += clk
			}
		}
		camp.row(cid, client, fmt.Sprintf("%s %d", p.tr3("Campaña", "Campaign", "Campanha"), i+1), fmtC(total))
		for c := 0; c < 3; c++ {
			cal.row(day(i*3+c), cid, channels[c%len(channels)], fmt.Sprintf("%s %d.%d", p.tr3("Pieza", "Piece", "Peça"), i+1, c+1), []string{"Luz", "Marco", "Inés"}[r.Intn(3)],
				[]string{p.tr3("borrador", "draft", "rascunho"), p.tr3("aprobado", "approved", "aprovado")}[r.Intn(2)])
		}
	}
	for _, k := range order {
		rep.row(k.client, k.month, fmtC(repSpend[k]), fmt.Sprint(repClicks[k]))
	}
	return Set{Files: []File{
		industryReadme(p, p.tr3("Agencia de marketing", "Marketing agency", "Agência de marketing"), "- campaigns.csv, channel_spend.csv, content_calendar.csv, client_reports.csv"),
		file("", "campaigns.csv", camp.bytes()), file("", "channel_spend.csv", spend.bytes()),
		file("", "content_calendar.csv", cal.bytes()), file("", "client_reports.csv", rep.bytes()),
	}}
}

// ---- multinational group (3 subsidiaries) ----

type subsidiary struct {
	id, name, country, currency string
	rate                        float64 // local units per USD (fictional)
}

var groupSubs = []subsidiary{
	{"MX01", "Grupo Ficticio México", "MX", "MXN", 17.25},
	{"BR01", "Grupo Ficticio Brasil", "BR", "BRL", 5.10},
	{"ES01", "Grupo Ficticio España", "ES", "EUR", 0.92},
}

func genMultinationalGroup(p Params) Set {
	r := newRNG(p, MultinationalGroupS)
	var subs, ic csvb
	subs.row("subsidiary_id", "name", "country", "currency", "rate_per_usd")
	files := []File{industryReadme(p, p.tr3("Grupo multinacional", "Multinational group", "Grupo multinacional"),
		"- subsidiaries.csv, trial_balance_<id>.csv, intercompany.csv")}
	for _, s := range groupSubs {
		subs.row(s.id, s.name, s.country, s.currency, fmtRate(s.rate))
		var tb csvb
		tb.row("account", "name", "debit", "credit")
		cash, ar, fixed := between(r, 2000000, 9000000), between(r, 1000000, 5000000), between(r, 3000000, 8000000)
		ap, debt := between(r, 800000, 3000000), between(r, 1000000, 4000000)
		rev, cost := between(r, 6000000, 12000000), between(r, 3000000, 6000000)
		opex := between(r, 1000000, 2500000)
		debits := cash + ar + fixed + cost + opex
		credits := ap + debt + rev
		equity := debits - credits // plug: balances by construction, kept positive below
		if equity < 0 {
			cash += -equity // more debits so it still balances with zero equity
			equity = 0
		}
		rows := []struct {
			a, n string
			d, c cents
		}{
			{"1000", p.tr3("Caja y bancos", "Cash and banks", "Caixa e bancos"), cash, 0},
			{"1100", p.tr3("Clientes", "Receivables", "Clientes"), ar, 0},
			{"1500", p.tr3("Activo fijo", "Fixed assets", "Ativo imobilizado"), fixed, 0},
			{"2000", p.tr3("Proveedores", "Payables", "Fornecedores"), 0, ap},
			{"2500", p.tr3("Deuda", "Debt", "Dívida"), 0, debt},
			{"3000", p.tr3("Capital", "Equity", "Patrimônio"), 0, equity},
			{"4000", p.tr3("Ingresos", "Revenue", "Receita"), 0, rev},
			{"5000", p.tr3("Costo de ventas", "Cost of sales", "Custo das vendas"), cost, 0},
			{"6000", p.tr3("Gastos operativos", "Operating expenses", "Despesas operacionais"), opex, 0},
		}
		for _, x := range rows {
			tb.row(x.a, x.n, fmtOrBlank(x.d), fmtOrBlank(x.c))
		}
		files = append(files, file("", "trial_balance_"+strings.ToLower(s.id)+".csv", tb.bytes()))
	}
	ic.row("seller_id", "buyer_id", "seller_currency", "receivable_local", "buyer_currency", "payable_local", "usd_amount")
	pairs := [][2]int{{0, 1}, {1, 2}, {2, 0}}
	for _, pr := range pairs {
		s, b := groupSubs[pr[0]], groupSubs[pr[1]]
		usd := between(r, 200000, 900000)
		ic.row(s.id, b.id, s.currency, fmtC(conv(usd, s.rate)), b.currency, fmtC(conv(usd, b.rate)), fmtC(usd))
	}
	files = append(files, file("", "subsidiaries.csv", subs.bytes()), file("", "intercompany.csv", ic.bytes()))
	return Set{Files: files}
}

func fmtOrBlank(c cents) string {
	if c == 0 {
		return ""
	}
	return fmtC(c)
}
