package invoicepnl

import (
	"fmt"
	"strings"

	"aiworkforce/backend/internal/officedoc"
)

// labels are the visible texts of the workbook and of the reasons (es, en, pt).
type labels struct {
	unreadable, scanned, badAmount, ambiguous, mismatch, noBreakdown, noAmount string
	classifiedByAgent, notClassified, typeConflict, doubt, duplicate, noRate  string
	defaultRevenue, defaultCost, defaultExpense                              string
	income, expense, yes, no, included, toReview                             string
	detail, statementSheet, review                                           string
	cols, stCols, rvCols                                                     []string
	revenue, totalRevenue, cost, totalCost, gross, expenses, totalExpenses    string
	operating, memo, taxesIssued, taxesReceived, counted, currencyNote        string
	period, title                                                            string
}

func (l labels) kind(k string) string {
	if k == KindIncome {
		return l.income
	}
	return l.expense
}

var labelSet = map[string]labels{
	"es": {
		unreadable: "ilegible: no se pudo leer", scanned: "escaneada: no se pudo leer el texto",
		badAmount: "importe que no se pudo leer", ambiguous: "importe ambiguo (¿miles o decimales?): revísalo",
		mismatch: "no cuadra: subtotal + impuestos = %s, total = %s", noBreakdown: "sin desglose de impuestos: se tomó el total",
		noAmount: "sin importe", classifiedByAgent: "la empresa no aparece como emisor ni receptor: se clasificó como %s según el agente",
		notClassified: "no se pudo clasificar como ingreso o gasto", typeConflict: "el agente la leyó como %[2]s pero la empresa la %[1]s",
		doubt: "duda: ", duplicate: "duplicada (mismo número y emisor que la fila %d)", noRate: "moneda %s sin tipo de cambio a %s: no se sumó",
		defaultRevenue: "Ventas", defaultCost: "Costo de ventas", defaultExpense: "Gastos generales",
		income: "ingreso", expense: "gasto", yes: "Sí", no: "No", included: "incluida", toReview: "revisar",
		detail: "Detalle", statementSheet: "Estado de resultados", review: "Revisar",
		cols:   []string{"Fecha", "Número", "Emisor", "Receptor", "Concepto", "Categoría", "Tipo", "Moneda", "Subtotal", "Impuestos", "Total", "Subtotal en moneda base", "Archivo", "Página", "Estado"},
		stCols: []string{"Concepto", "Importe"},
		rvCols: []string{"Fila", "Archivo", "Página", "Número", "Emisor", "Motivo", "¿En el resultado?"},
		revenue: "Ingresos", totalRevenue: "Total ingresos", cost: "Costo de ventas", totalCost: "Total costo de ventas",
		gross: "Utilidad bruta", expenses: "Gastos de operación", totalExpenses: "Total gastos de operación",
		operating: "Utilidad antes de impuestos", memo: "Para información (no forman parte del resultado)",
		taxesIssued: "Impuestos cobrados en facturas emitidas", taxesReceived: "Impuestos pagados en facturas recibidas",
		counted: "Facturas en el resultado: %d de %d (%d por revisar)", currencyNote: "Importes en %s, sin impuestos",
		period: "Periodo: %s", title: "Estado de resultados",
	},
	"en": {
		unreadable: "unreadable: could not be read", scanned: "scanned: the text could not be read",
		badAmount: "amount could not be read", ambiguous: "ambiguous amount (thousands or decimals?): check it",
		mismatch: "does not add up: subtotal + taxes = %s, total = %s", noBreakdown: "no tax breakdown: the total was used",
		noAmount: "no amount", classifiedByAgent: "the company is neither issuer nor receiver: classified as %s by the agent",
		notClassified: "could not be classified as income or expense", typeConflict: "the agent read it as %[2]s but for the company it is %[1]s",
		doubt: "doubt: ", duplicate: "duplicate (same number and issuer as row %d)", noRate: "currency %s with no exchange rate to %s: not added",
		defaultRevenue: "Sales", defaultCost: "Cost of sales", defaultExpense: "General expenses",
		income: "income", expense: "expense", yes: "Yes", no: "No", included: "included", toReview: "to review",
		detail: "Detail", statementSheet: "Income statement", review: "To review",
		cols:   []string{"Date", "Number", "Issuer", "Receiver", "Concept", "Category", "Type", "Currency", "Subtotal", "Taxes", "Total", "Subtotal in base currency", "File", "Page", "Status"},
		stCols: []string{"Item", "Amount"},
		rvCols: []string{"Row", "File", "Page", "Number", "Issuer", "Reason", "In the result?"},
		revenue: "Revenue", totalRevenue: "Total revenue", cost: "Cost of sales", totalCost: "Total cost of sales",
		gross: "Gross profit", expenses: "Operating expenses", totalExpenses: "Total operating expenses",
		operating: "Profit before taxes", memo: "For information (not part of the result)",
		taxesIssued: "Taxes charged on issued invoices", taxesReceived: "Taxes paid on received invoices",
		counted: "Invoices in the result: %d of %d (%d to review)", currencyNote: "Amounts in %s, before taxes",
		period: "Period: %s", title: "Income statement",
	},
	"pt": {
		unreadable: "ilegível: não foi possível ler", scanned: "digitalizada: não foi possível ler o texto",
		badAmount: "valor que não foi possível ler", ambiguous: "valor ambíguo (milhares ou decimais?): revise-o",
		mismatch: "não fecha: subtotal + impostos = %s, total = %s", noBreakdown: "sem detalhamento de impostos: usou-se o total",
		noAmount: "sem valor", classifiedByAgent: "a empresa não aparece como emissor nem destinatário: classificada como %s pelo agente",
		notClassified: "não foi possível classificar como receita ou despesa", typeConflict: "o agente a leu como %[2]s, mas para a empresa é %[1]s",
		doubt: "dúvida: ", duplicate: "duplicada (mesmo número e emissor da linha %d)", noRate: "moeda %s sem taxa de câmbio para %s: não foi somada",
		defaultRevenue: "Vendas", defaultCost: "Custo das vendas", defaultExpense: "Despesas gerais",
		income: "receita", expense: "despesa", yes: "Sim", no: "Não", included: "incluída", toReview: "revisar",
		detail: "Detalhe", statementSheet: "Demonstração do resultado", review: "Revisar",
		cols:   []string{"Data", "Número", "Emissor", "Destinatário", "Descrição", "Categoria", "Tipo", "Moeda", "Subtotal", "Impostos", "Total", "Subtotal na moeda base", "Arquivo", "Página", "Situação"},
		stCols: []string{"Item", "Valor"},
		rvCols: []string{"Linha", "Arquivo", "Página", "Número", "Emissor", "Motivo", "No resultado?"},
		revenue: "Receitas", totalRevenue: "Total de receitas", cost: "Custo das vendas", totalCost: "Total do custo das vendas",
		gross: "Lucro bruto", expenses: "Despesas operacionais", totalExpenses: "Total de despesas operacionais",
		operating: "Lucro antes dos impostos", memo: "Para informação (não fazem parte do resultado)",
		taxesIssued: "Impostos cobrados em notas emitidas", taxesReceived: "Impostos pagos em notas recebidas",
		counted: "Notas no resultado: %d de %d (%d para revisar)", currencyNote: "Valores em %s, sem impostos",
		period: "Período: %s", title: "Demonstração do resultado",
	},
}

func labelsFor(locale string) labels {
	l := strings.ToLower(strings.TrimSpace(locale))
	if len(l) > 2 {
		l = l[:2]
	}
	if s, ok := labelSet[l]; ok {
		return s
	}
	return labelSet["es"]
}

// SheetNames are the names of the three sheets for a locale (detail, statement, review).
func SheetNames(locale string) (string, string, string) {
	l := labelsFor(locale)
	return l.detail, l.statementSheet, l.review
}

func cents(c int64) float64 { return float64(c) / 100 }

// Workbook builds the Excel model: Detail (one row per invoice, with its file), Income statement and To review.
// FileColumn is the 1-based column of the file path in the detail sheet (for hyperlinks).
func Workbook(res Result, locale string) (officedoc.Doc, int) {
	l := labelsFor(locale)
	cur := res.Currency
	if cur == "" {
		cur = "$"
	}
	text := func(h string) officedoc.Column { return officedoc.Column{Header: h, Type: "text"} }
	money := func(h, c string) officedoc.Column { return officedoc.Column{Header: h, Type: "currency", Currency: c + " "} }
	detailCols := []officedoc.Column{text(l.cols[0]), text(l.cols[1]), text(l.cols[2]), text(l.cols[3]), text(l.cols[4]), text(l.cols[5]), text(l.cols[6]), text(l.cols[7]),
		{Header: l.cols[8], Type: "number", Format: "#,##0.00"}, {Header: l.cols[9], Type: "number", Format: "#,##0.00"}, {Header: l.cols[10], Type: "number", Format: "#,##0.00"},
		money(l.cols[11], cur), text(l.cols[12]), {Header: l.cols[13], Type: "number"}, text(l.cols[14])}
	var rows [][]any
	for _, r := range res.Rows {
		kind := ""
		if r.Kind != "" {
			kind = l.kind(r.Kind)
		}
		status := l.included
		if !r.Included || len(r.Reasons) > 0 {
			status = l.toReview
		}
		var page any = ""
		if r.Page > 0 {
			page = float64(r.Page)
		}
		var base any = cents(r.Base)
		if !r.Included {
			base = ""
		}
		rows = append(rows, []any{r.Date, r.Number, r.Issuer, r.Receiver, r.Concept, r.Category, kind, r.Currency,
			cents(r.Subtotal), cents(r.Taxes), cents(r.Total), base, r.File, page, status})
	}
	st := res.Statement
	var lines [][]any
	add := func(label string, c int64) { lines = append(lines, []any{label, cents(c)}) }
	head := func(label string) { lines = append(lines, []any{label, ""}) }
	head(l.revenue)
	for _, c := range st.RevenueByCat {
		add("   "+c.Name, c.Amount)
	}
	add(l.totalRevenue, st.Revenue)
	head(l.cost)
	for _, c := range st.CostByCat {
		add("   "+c.Name, c.Amount)
	}
	add(l.totalCost, st.Cost)
	add(l.gross, st.Gross)
	head(l.expenses)
	for _, c := range st.ExpensesByCat {
		add("   "+c.Name, c.Amount)
	}
	add(l.totalExpenses, st.Expenses)
	add(l.operating, st.Operating)
	lines = append(lines, []any{"", ""})
	head(l.memo)
	add("   "+l.taxesIssued, st.TaxesIssued)
	add("   "+l.taxesReceived, st.TaxesReceived)
	lines = append(lines, []any{"", ""}, []any{fmt.Sprintf(l.counted, res.Included, res.Read, len(res.Review)), ""}, []any{fmt.Sprintf(l.currencyNote, cur), ""})
	if res.Period != "" {
		lines = append(lines, []any{fmt.Sprintf(l.period, res.Period), ""})
	}
	var rv [][]any
	for _, r := range res.Review {
		inc := l.no
		if r.Included {
			inc = l.yes
		}
		var page any = ""
		if r.Page > 0 {
			page = float64(r.Page)
		}
		rv = append(rv, []any{float64(r.Row), r.File, page, r.Number, r.Issuer, r.Reason, inc})
	}
	if rv == nil {
		rv = [][]any{}
	}
	if rows == nil {
		rows = [][]any{}
	}
	freeze := true
	return officedoc.Doc{Title: l.title, Sheets: []officedoc.Sheet{
		{Name: l.detail, Columns: detailCols, Rows: rows, FreezeHeader: &freeze},
		{Name: l.statementSheet, Columns: []officedoc.Column{text(l.stCols[0]), money(l.stCols[1], cur)}, Rows: lines, FreezeHeader: &freeze},
		{Name: l.review, Columns: []officedoc.Column{{Header: l.rvCols[0], Type: "number"}, text(l.rvCols[1]), {Header: l.rvCols[2], Type: "number"}, text(l.rvCols[3]), text(l.rvCols[4]), text(l.rvCols[5]), text(l.rvCols[6])}, Rows: rv, FreezeHeader: &freeze},
	}}, 13
}
