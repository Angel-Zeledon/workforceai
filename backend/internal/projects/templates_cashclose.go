package projects

import (
	"fmt"
	"strconv"
	"strings"

	"aiworkforce/backend/internal/catalog"
)

// Q3: "Cierre de caja trimestral / Quarterly cash close" (professions catalog
// 4.5), the large example project of the finance_treasury role.
//
// Per client: for every bank account a chain prepare -> reconcile -> review of
// open items (the chains are independent, so they run in parallel), plus the
// receivables aging and the 13-week forecast; then consolidation and
// variances (accounting), memo and alerts (assistant) and the PROPOSED payment
// list followed by a human gate. Nothing here moves money: the payment list
// is a draft artifact a person acts on outside the system, and the gate is the
// human review of that draft.
//
// Parameters (strings, like every template param): accounts (1-12, default 6)
// and clients (1-10, default 1). With clients > 1 the whole structure repeats
// per client (an accounting firm closing several clients) and a firm-level
// summary depends on every client's memo. 3 clients x 6 accounts = 88 tasks.
// The agent ids are the roles finance_treasury, accounting and assistant: the
// treasury role must be hired from its template first (launch validation
// reports unknown_agent otherwise; PatchPlan can re-assign).

const (
	idCashClose       = "tpl-quarterly-cash-close"
	keyCashClose      = "quarterly_cash_close"
	cashCloseMaxAccts = 12
	cashCloseMaxCli   = 10
	treasuryAgent     = "finance_treasury"
	approvePayment    = "approve_payment_proposal"
)

var cashCloseI18n = map[string]map[string]string{
	"es": {
		"proj.cc.name":                       "Cierre de caja trimestral",
		"proj.cc.desc":                       "Concilia varias cuentas bancarias en paralelo, la antigüedad de cuentas por cobrar y el flujo de caja a 13 semanas; luego consolida, redacta el memo y propone una lista de pagos que una persona decide y ejecuta fuera del sistema. Ejemplo, no asesoría financiera.",
		"proj.cc.param.accounts":             "Cuentas bancarias",
		"proj.cc.param.clients":              "Clientes (despacho)",
		"proj.cc.client":                     "Cliente {n}",
		"proj.cc.o1":                         "Conciliar y proyectar la caja",
		"proj.cc.o1c":                        "{client}: conciliar y proyectar la caja",
		"proj.cc.o2":                         "Consolidar, informar y proponer pagos",
		"proj.cc.o2c":                        "{client}: consolidar, informar y proponer pagos",
		"proj.cc.o3":                         "Resumen del despacho",
		"proj.cc.w1":                         "Conciliaciones bancarias",
		"proj.cc.w2":                         "Cartera por cobrar",
		"proj.cc.w3":                         "Flujo de caja a 13 semanas",
		"proj.cc.w4":                         "Consolidación y variaciones",
		"proj.cc.w5":                         "Memo y alertas",
		"proj.cc.w6":                         "Pagos propuestos",
		"proj.cc.w7":                         "Consolidado",
		"proj.cc.prep":                       "Preparar el extracto de la cuenta {account}",
		"proj.cc.rec":                        "Conciliar la cuenta {account}",
		"proj.cc.var":                        "Revisar las partidas abiertas de la cuenta {account}",
		"proj.cc.ar1":                        "Listar las facturas vencidas",
		"proj.cc.ar2":                        "Calcular la antigüedad de cuentas por cobrar",
		"proj.cc.f1":                         "Estimar los cobros de las próximas 13 semanas",
		"proj.cc.f2":                         "Estimar los pagos programados de las próximas 13 semanas",
		"proj.cc.f3":                         "Armar la proyección de flujo a 13 semanas",
		"proj.cc.cons":                       "Consolidar las conciliaciones y la proyección",
		"proj.cc.vars":                       "Analizar las variaciones contra el periodo anterior",
		"proj.cc.memo":                       "Redactar el memo de tesorería",
		"proj.cc.alerts":                     "Redactar las alertas de liquidez y vencimientos",
		"proj.cc.pay":                        "Preparar la lista de pagos propuesta (nunca se ejecuta)",
		"proj.cc.gate":                       "Revisión humana de la lista de pagos propuesta",
		"proj.cc.firm":                       "Resumir el cierre de todos los clientes",
		"pv.action.approve_payment_proposal": "Revisar: {node}",
	},
	"en": {
		"proj.cc.name":                       "Quarterly cash close",
		"proj.cc.desc":                       "Reconciles several bank accounts in parallel, receivables aging and the 13-week cash flow; then consolidates, writes the memo and proposes a payment list that a person decides on and executes outside the system. Example, not financial advice.",
		"proj.cc.param.accounts":             "Bank accounts",
		"proj.cc.param.clients":              "Clients (firm)",
		"proj.cc.client":                     "Client {n}",
		"proj.cc.o1":                         "Reconcile and forecast cash",
		"proj.cc.o1c":                        "{client}: reconcile and forecast cash",
		"proj.cc.o2":                         "Consolidate, report and propose payments",
		"proj.cc.o2c":                        "{client}: consolidate, report and propose payments",
		"proj.cc.o3":                         "Firm summary",
		"proj.cc.w1":                         "Bank reconciliations",
		"proj.cc.w2":                         "Receivables",
		"proj.cc.w3":                         "13-week cash flow",
		"proj.cc.w4":                         "Consolidation and variances",
		"proj.cc.w5":                         "Memo and alerts",
		"proj.cc.w6":                         "Proposed payments",
		"proj.cc.w7":                         "Roll-up",
		"proj.cc.prep":                       "Prepare the statement of account {account}",
		"proj.cc.rec":                        "Reconcile account {account}",
		"proj.cc.var":                        "Review the open items of account {account}",
		"proj.cc.ar1":                        "List the overdue invoices",
		"proj.cc.ar2":                        "Compute the receivables aging",
		"proj.cc.f1":                         "Estimate receipts for the next 13 weeks",
		"proj.cc.f2":                         "Estimate scheduled payments for the next 13 weeks",
		"proj.cc.f3":                         "Build the 13-week cash-flow forecast",
		"proj.cc.cons":                       "Consolidate the reconciliations and the forecast",
		"proj.cc.vars":                       "Analyze variances against the previous period",
		"proj.cc.memo":                       "Write the treasury memo",
		"proj.cc.alerts":                     "Write the liquidity and due-date alerts",
		"proj.cc.pay":                        "Prepare the proposed payment list (never executed)",
		"proj.cc.gate":                       "Human review of the proposed payment list",
		"proj.cc.firm":                       "Summarize the close of every client",
		"pv.action.approve_payment_proposal": "Review: {node}",
	},
}

func init() {
	for loc, m := range cashCloseI18n {
		for k, v := range m {
			i18n[loc][k] = v
		}
	}
}

func cashCloseParams() []TemplateParam {
	return []TemplateParam{
		{Key: "accounts", LabelKey: "proj.cc.param.accounts", Default: "6"},
		{Key: "clients", LabelKey: "proj.cc.param.clients", Default: "1"},
	}
}

// cashCloseTemplate is the static default (6 accounts, 1 client) listed in the template catalog.
func cashCloseTemplate() Template {
	return buildCashClose(6, 1, "es")
}

func clampParam(v string, def, lo, hi int) int {
	n, err := strconv.Atoi(strings.TrimSpace(catalog.SanitizeParam(v)))
	if err != nil {
		return def
	}
	return min(max(n, lo), hi)
}

func accountLabel(i int) string { // A..Z, then A2.. never needed below 13 accounts
	return string(rune('A' + i))
}

// buildCashClose expands the template for a number of accounts and clients.
func buildCashClose(accounts, clients int, locale string) Template {
	t := Template{ID: idCashClose, Key: keyCashClose, Version: 1, Builtin: true, NameKey: "proj.cc.name", DescriptionKey: "proj.cc.desc", Params: cashCloseParams()}
	multi := clients > 1
	var firmDeps []string
	for c := 1; c <= clients; c++ {
		pre, params, o1, o2 := "", map[string]string(nil), "proj.cc.o1", "proj.cc.o2"
		if multi {
			pre = fmt.Sprintf("c%d_", c)
			params = map[string]string{"client": tr(locale, "proj.cc.client", map[string]string{"n": strconv.Itoa(c)})}
			o1, o2 = "proj.cc.o1c", "proj.cc.o2c"
		}
		k := func(s string) string { return pre + s }
		var rec []TemplateNode
		var vars []string
		for a := 0; a < accounts; a++ {
			ap := map[string]string{"account": accountLabel(a)}
			id := strconv.Itoa(a + 1)
			rec = append(rec,
				TemplateNode{Key: k("prep" + id), TitleKey: "proj.cc.prep", Params: ap, Agent: treasuryAgent, Complexity: "S"},
				TemplateNode{Key: k("rec" + id), TitleKey: "proj.cc.rec", Params: ap, Agent: treasuryAgent, Complexity: "M", Deps: []string{k("prep" + id)}},
				TemplateNode{Key: k("var" + id), TitleKey: "proj.cc.var", Params: ap, Agent: "accounting", Complexity: "M", Deps: []string{k("rec" + id)}})
			vars = append(vars, k("var"+id))
		}
		consDeps := append(append([]string{}, vars...), k("ar2"), k("f3"))
		t.Objectives = append(t.Objectives,
			TemplateObjective{Key: k("o1"), TitleKey: o1, Params: params, Workflows: []TemplateWorkflow{
				{Key: k("w1"), TitleKey: "proj.cc.w1", Nodes: rec},
				{Key: k("w2"), TitleKey: "proj.cc.w2", Nodes: []TemplateNode{
					{Key: k("ar1"), TitleKey: "proj.cc.ar1", Agent: treasuryAgent, Complexity: "S"},
					{Key: k("ar2"), TitleKey: "proj.cc.ar2", Agent: treasuryAgent, Complexity: "M", Deps: []string{k("ar1")}},
				}},
				{Key: k("w3"), TitleKey: "proj.cc.w3", Nodes: []TemplateNode{
					{Key: k("f1"), TitleKey: "proj.cc.f1", Agent: treasuryAgent, Complexity: "M", Deps: []string{k("ar2")}},
					{Key: k("f2"), TitleKey: "proj.cc.f2", Agent: treasuryAgent, Complexity: "M"},
					{Key: k("f3"), TitleKey: "proj.cc.f3", Agent: treasuryAgent, Complexity: "L", Deps: []string{k("f1"), k("f2")}},
				}},
			}},
			TemplateObjective{Key: k("o2"), TitleKey: o2, Params: params, Workflows: []TemplateWorkflow{
				{Key: k("w4"), TitleKey: "proj.cc.w4", Nodes: []TemplateNode{
					{Key: k("cons"), TitleKey: "proj.cc.cons", Agent: "accounting", Complexity: "L", Deps: consDeps},
					{Key: k("vars"), TitleKey: "proj.cc.vars", Agent: "accounting", Complexity: "M", Deps: []string{k("cons")}},
				}},
				{Key: k("w5"), TitleKey: "proj.cc.w5", Nodes: []TemplateNode{
					{Key: k("memo"), TitleKey: "proj.cc.memo", Agent: "assistant", Complexity: "L", Deps: []string{k("vars")}},
					{Key: k("alerts"), TitleKey: "proj.cc.alerts", Agent: "assistant", Complexity: "S", Deps: []string{k("vars")}},
				}},
				{Key: k("w6"), TitleKey: "proj.cc.w6", Nodes: []TemplateNode{
					{Key: k("pay"), TitleKey: "proj.cc.pay", Agent: treasuryAgent, Complexity: "M", Deps: []string{k("memo"), k("alerts")}},
					// The human reviews the DRAFT list; paying is done by a person outside the system.
					{Key: k("gate"), Kind: KindGate, TitleKey: "proj.cc.gate", Deps: []string{k("pay")}, Secs: 8,
						Approval: &TemplateApproval{Action: approvePayment, Risk: "high"}},
				}},
			}})
		firmDeps = append(firmDeps, k("memo"))
	}
	if multi {
		t.Objectives = append(t.Objectives, TemplateObjective{Key: "o3", TitleKey: "proj.cc.o3", Workflows: []TemplateWorkflow{
			{Key: "w7", TitleKey: "proj.cc.w7", Nodes: []TemplateNode{
				{Key: "firm", TitleKey: "proj.cc.firm", Agent: "assistant", Complexity: "L", Deps: firmDeps}}}}})
	}
	return t
}

// expandCashClose applies the user parameters (accounts, clients) to the template.
func expandCashClose(params map[string]string, locale string) Template {
	return buildCashClose(clampParam(params["accounts"], 6, 1, cashCloseMaxAccts), clampParam(params["clients"], 1, 1, cashCloseMaxCli), locale)
}
