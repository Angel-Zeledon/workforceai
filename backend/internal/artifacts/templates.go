package artifacts

import (
	"encoding/json"
	"fmt"
)

// Artifact templates (the same ids the frontend offers in the "+" menu) and the
// blank content of every kind. Texts exist in es and en; the content stored is
// plain data in the canonical aiw.<kind>/1 format.

func L(locale, es, en string) string {
	if locale == "en" {
		return en
	}
	return es
}

type tpl struct {
	id           string
	kind         Kind
	suggestedFor []string
	es, en       string
	build        func(loc string) map[string]any
}

type row struct {
	a string
	v float64
	f string
}

const money = "$#,##0"

func sheetOf(name string, rows []row, imports ...map[string]any) map[string]any {
	return sheetLoc("es", name, rows, imports...)
}

func sheetLoc(loc, name string, rows []row, imports ...map[string]any) map[string]any {
	cells := map[string]any{"A1": map[string]any{"v": L(loc, "Concepto", "Item")}, "B1": map[string]any{"v": L(loc, "Monto", "Amount")}}
	for i, r := range rows {
		cells[fmt.Sprintf("A%d", i+2)] = map[string]any{"v": r.a}
		c := map[string]any{"v": r.v, "fmt": money}
		if r.f != "" {
			c["f"] = r.f
		}
		cells[fmt.Sprintf("B%d", i+2)] = c
	}
	out := map[string]any{"schema": "aiw.sheet/1", "sheets": []any{map[string]any{"id": "s1", "name": name, "rows": 40, "cols": 8, "cells": cells,
		"colw": map[string]any{"A": 220, "B": 130}}}}
	if len(imports) > 0 {
		list := make([]any, 0, len(imports))
		for _, im := range imports {
			list = append(list, im)
		}
		out["imports"] = list
	}
	return out
}

func para(bid, text string) map[string]any {
	return map[string]any{"type": "paragraph", "attrs": map[string]any{"bid": bid}, "content": []any{map[string]any{"type": "text", "text": text}}}
}

func heading(bid string, level int, text string) map[string]any {
	return map[string]any{"type": "heading", "attrs": map[string]any{"bid": bid, "level": level}, "content": []any{map[string]any{"type": "text", "text": text}}}
}

func docOf(nodes ...any) map[string]any {
	return map[string]any{"schema": "aiw.doc/1", "doc": map[string]any{"type": "doc", "content": nodes}}
}

func tableOf(cols [][]any, rows []map[string]any) map[string]any {
	cs := make([]any, 0, len(cols))
	for _, c := range cols {
		col := map[string]any{"key": c[0], "label": c[1], "type": "text"}
		if len(c) > 2 {
			col["type"] = c[2]
		}
		if len(c) > 3 {
			col["options"] = c[3]
		}
		cs = append(cs, col)
	}
	rs := make([]any, 0, len(rows))
	for i, r := range rows {
		rs = append(rs, map[string]any{"id": fmt.Sprintf("r%d", i+1), "cells": r})
	}
	return map[string]any{"schema": "aiw.table/1", "columns": cs, "rows": rs}
}

func incomeRows(loc string) []row {
	return []row{
		{L(loc, "Ingresos", "Revenue"), 1200000, ""}, {L(loc, "Costo de ventas", "Cost of sales"), 700000, ""},
		{L(loc, "Utilidad bruta", "Gross profit"), 500000, "=B2-B3"},
		{L(loc, "Sueldos", "Salaries"), 220000, ""}, {L(loc, "Renta", "Rent"), 60000, ""}, {L(loc, "Otros gastos", "Other expenses"), 45000, ""},
		{L(loc, "Gastos operativos", "Operating expenses"), 325000, "=SUM(B5:B7)"},
		{L(loc, "Utilidad operativa", "Operating income"), 175000, "=B4-B8"},
		{L(loc, "Intereses", "Interest"), 15000, ""}, {L(loc, "Impuestos", "Taxes"), 48000, "=ROUND((B9-B10)*0.3,0)"},
		{L(loc, "Utilidad neta", "Net income"), 112000, "=B9-B10-B11"},
	}
}

func balanceRows(loc string) []row {
	return []row{
		{L(loc, "Efectivo", "Cash"), 962000, ""}, {L(loc, "Cuentas por cobrar", "Receivables"), 430000, ""}, {L(loc, "Inventario", "Inventory"), 300000, ""},
		{L(loc, "Total activos", "Total assets"), 1692000, "=SUM(B2:B4)"},
		{L(loc, "Cuentas por pagar", "Payables"), 380000, ""}, {L(loc, "Deuda", "Debt"), 600000, ""}, {L(loc, "Capital", "Capital"), 600000, ""},
		{L(loc, "Pasivo + capital", "Liabilities + equity"), 1580000, "=B6+B7+B8"},
	}
}

func blank(kind Kind, loc string) map[string]any {
	switch kind {
	case KindSheet:
		return map[string]any{"schema": "aiw.sheet/1", "sheets": []any{map[string]any{"id": "s1", "name": L(loc, "Hoja 1", "Sheet 1"), "rows": 40, "cols": 8, "cells": map[string]any{}}}}
	case KindDoc:
		return docOf(heading("b1", 1, L(loc, "Documento sin título", "Untitled document")), para("b2", ""))
	case KindTable:
		return tableOf([][]any{{"name", L(loc, "Nombre", "Name")}, {"note", L(loc, "Nota", "Note")}}, []map[string]any{{"name": "", "note": ""}})
	case KindBoard:
		return map[string]any{"schema": "aiw.board/1", "view": "kanban", "columns": []any{
			map[string]any{"id": "todo", "title": L(loc, "Por hacer", "To do")}, map[string]any{"id": "doing", "title": L(loc, "En curso", "Doing")},
			map[string]any{"id": "done", "title": L(loc, "Hecho", "Done")}}, "cards": []any{}}
	case KindChart:
		return map[string]any{"schema": "aiw.chart/1", "title": L(loc, "Gráfica", "Chart"), "mark": "bar",
			"source":   map[string]any{"inline": map[string]any{"columns": []any{"x", "y"}, "rows": []any{[]any{"A", 3}, []any{"B", 5}, []any{"C", 2}}}},
			"encoding": map[string]any{"x": "x", "y": []any{"y"}}}
	case KindPDF:
		return map[string]any{"schema": "aiw.pdf/1", "blob_id": nil, "pages": 0, "annotations": []any{}}
	case KindForm:
		return map[string]any{"schema": "aiw.form/1", "fields": []any{map[string]any{"key": "note", "type": "textarea", "label": L(loc, "Comentario", "Comment"), "required": false}},
			"answers": nil, "answered_by": nil}
	case KindInbox:
		return map[string]any{"schema": "aiw.inbox/1", "items": []any{}}
	default:
		return map[string]any{"schema": "aiw.agenda/1", "events": []any{}}
	}
}

var templates = []tpl{
	{"bank_reconciliation", KindSheet, []string{"accounting"}, "Conciliación bancaria", "Bank reconciliation", func(l string) map[string]any {
		return sheetLoc(l, L(l, "Conciliación", "Reconciliation"), []row{{L(l, "Saldo según banco", "Bank balance"), 50000, ""},
			{L(l, "Depósitos en tránsito", "Deposits in transit"), 4200, ""}, {L(l, "Cheques pendientes", "Outstanding checks"), -1800, ""},
			{L(l, "Saldo conciliado", "Reconciled balance"), 52400, "=SUM(B2:B4)"}})
	}},
	{"income_statement", KindSheet, []string{"accounting"}, "Estado de resultados", "Income statement", func(l string) map[string]any { return sheetLoc(l, "Result", incomeRows(l)) }},
	{"balance_sheet", KindSheet, []string{"accounting"}, "Balance general", "Balance sheet", func(l string) map[string]any { return sheetLoc(l, "Balance", balanceRows(l)) }},
	{"cash_flow", KindSheet, []string{"accounting"}, "Flujo de efectivo", "Cash flow", func(l string) map[string]any {
		return sheetLoc(l, "Cash", []row{{L(l, "Flujo operativo", "Operating cash flow"), 240000, ""}, {L(l, "Flujo de inversión", "Investing cash flow"), -80000, ""},
			{L(l, "Flujo de financiamiento", "Financing cash flow"), -30000, ""}, {L(l, "Cambio neto", "Net change"), 130000, "=SUM(B2:B4)"}})
	}},
	{"accounting_policies", KindDoc, []string{"accounting"}, "Políticas contables", "Accounting policies", func(l string) map[string]any {
		return docOf(heading("b1", 1, L(l, "Políticas contables", "Accounting policies")),
			para("b2", L(l, "Los estados se preparan con base en devengado.", "Statements are prepared on an accrual basis.")))
	}},
	{"service_contract", KindDoc, []string{"legal"}, "Contrato de servicios", "Service contract", func(l string) map[string]any {
		return docOf(heading("b1", 1, L(l, "Contrato de servicios", "Service agreement")), para("b2", L(l, "Las partes acuerdan lo siguiente.", "The parties agree to the following.")),
			para("b3", L(l, "7. Penalidades: la penalidad será del 10% del monto mensual.", "7. Penalties: the penalty will be 10% of the monthly amount.")))
	}},
	{"commercial_proposal", KindDoc, []string{"sales"}, "Propuesta comercial", "Commercial proposal", func(l string) map[string]any {
		return docOf(heading("b1", 1, L(l, "Propuesta comercial", "Commercial proposal")), para("b2", L(l, "Alcance, inversión y calendario.", "Scope, investment and timeline.")))
	}},
	{"risk_matrix", KindTable, []string{"legal"}, "Matriz de riesgos", "Risk matrix", func(l string) map[string]any {
		return tableOf([][]any{{"risk", L(l, "Riesgo", "Risk")}, {"sev", L(l, "Severidad", "Severity"), "select", []any{"Alta", "Media", "Baja"}}},
			[]map[string]any{{"risk": L(l, "Penalidad desproporcionada", "Disproportionate penalty"), "sev": "Alta"}})
	}},
	{"sales_pipeline", KindBoard, []string{"sales"}, "Pipeline de ventas", "Sales pipeline", func(l string) map[string]any {
		b := blank(KindBoard, l)
		b["cards"] = []any{map[string]any{"id": "k1", "col": "doing", "title": "Acme", "labels": []any{}, "assignee": map[string]any{"kind": "agent", "id": "sales"}}}
		return b
	}},
	{"ops_checklist", KindBoard, []string{"operations"}, "Checklist de operación", "Operations checklist", func(l string) map[string]any {
		return map[string]any{"schema": "aiw.board/1", "view": "checklist", "columns": []any{map[string]any{"id": "todo", "title": L(l, "Pendientes", "Open")}},
			"cards": []any{map[string]any{"id": "k1", "col": "todo", "title": L(l, "Contratar 2 personas", "Hire 2 people"),
				"checklist": []any{map[string]any{"t": L(l, "Perfil", "Profile"), "done": true}, map[string]any{"t": L(l, "Entrevistas", "Interviews"), "done": false}}}}}
	}},
	{"kpi_dashboard", KindChart, []string{"analyst"}, "Tablero de KPIs", "KPI dashboard", func(l string) map[string]any { return blank(KindChart, l) }},
	{"daily_inbox", KindInbox, []string{"assistant"}, "Bandeja del día", "Daily inbox", func(l string) map[string]any { return blank(KindInbox, l) }},
	{"weekly_agenda", KindAgenda, []string{"assistant", "operations"}, "Agenda semanal", "Weekly agenda", func(l string) map[string]any { return blank(KindAgenda, l) }},
	{"hiring_pipeline", KindTable, []string{"hr"}, "Pipeline de contratación", "Hiring pipeline", func(l string) map[string]any {
		return tableOf([][]any{{"name", L(l, "Candidato", "Candidate")}, {"stage", L(l, "Etapa", "Stage"), "select", []any{"Nuevo", "Entrevista", "Oferta"}}},
			[]map[string]any{{"name": "Ana", "stage": "Nuevo"}})
	}},
}

// defaultTemplate is the artifact an agent starts with for its role.
var defaultTemplate = map[string]string{
	"accounting": "bank_reconciliation", "legal": "service_contract", "sales": "commercial_proposal", "analyst": "kpi_dashboard",
	"operations": "ops_checklist", "assistant": "daily_inbox", "hr": "hiring_pipeline",
}

func findTemplate(id string) (tpl, bool) {
	for _, t := range templates {
		if t.id == id {
			return t, true
		}
	}
	return tpl{}, false
}

func rawOf(v map[string]any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
