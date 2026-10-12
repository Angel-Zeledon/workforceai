package demodata

import (
	"fmt"
	"math/rand"
	"strings"
)

// Group consolidated close: up to 20 fictional subsidiaries of "Grupo Aurora"
// (reporting currency USD), one trial balance per subsidiary in its local
// currency, the intercompany matrix, FX rates, the chart-of-accounts mapping
// and the materiality policy. By construction:
//
//   - every trial balance balances to the cent (retained earnings is the plug);
//   - every intercompany row exists on both sides: the creditor's receivable and
//     the debtor's payable are the same USD amount converted at the closing rate
//     (balances) or at the average rate (sales and purchases), and they are the
//     amounts booked in the trial balances (accounts 1150, 2050, 4100, 5050);
//   - the FX rates of the rates file are the ones used to convert.

const GroupSet = "group_close"

// Country is a fictional entity location in the order of the project templates
// (projects.countries): name in Spanish and English, currency and the closing
// rate in local currency per 1 USD.
type Country struct {
	Code, ES, EN, Ccy string
	Close             float64
}

// Countries has the same order as the countries of the multinational templates.
var Countries = []Country{
	{"MX", "México", "Mexico", "MXN", 17.85}, {"CO", "Colombia", "Colombia", "COP", 4120}, {"CL", "Chile", "Chile", "CLP", 945.5},
	{"PE", "Perú", "Peru", "PEN", 3.78}, {"ES", "España", "Spain", "EUR", 0.925}, {"BR", "Brasil", "Brazil", "BRL", 5.42},
	{"AR", "Argentina", "Argentina", "ARS", 1180}, {"CR", "Costa Rica", "Costa Rica", "CRC", 515.3}, {"PA", "Panamá", "Panama", "USD", 1},
	{"GT", "Guatemala", "Guatemala", "GTQ", 7.72}, {"EC", "Ecuador", "Ecuador", "USD", 1}, {"UY", "Uruguay", "Uruguay", "UYU", 40.1},
	{"US", "Estados Unidos", "United States", "USD", 1}, {"CA", "Canadá", "Canada", "CAD", 1.372}, {"DE", "Alemania", "Germany", "EUR", 0.925},
	{"FR", "Francia", "France", "EUR", 0.925}, {"GB", "Reino Unido", "United Kingdom", "GBP", 0.781}, {"IT", "Italia", "Italy", "EUR", 0.925},
	{"PT", "Portugal", "Portugal", "EUR", 0.925}, {"DO", "República Dominicana", "Dominican Republic", "DOP", 59.4},
}

// GroupPeriod is the closing date of the sample data.
const GroupPeriod = "2026-09-30"

type acct struct {
	Code   int
	ES, EN string
	Typ    string // A asset, L liability, E equity, R revenue, X expense
}

// Chart is the group chart of accounts.
var Chart = []acct{
	{1000, "Caja y bancos", "Cash and banks", "A"},
	{1100, "Clientes", "Accounts receivable", "A"},
	{1150, "Cuentas por cobrar intercompañía", "Intercompany receivable", "A"},
	{1200, "Inventarios", "Inventory", "A"},
	{1500, "Propiedad, planta y equipo (neto)", "Property, plant and equipment (net)", "A"},
	{1600, "Intangibles", "Intangibles", "A"},
	{2000, "Proveedores", "Accounts payable", "L"},
	{2050, "Cuentas por pagar intercompañía", "Intercompany payable", "L"},
	{2100, "Gastos acumulados", "Accrued liabilities", "L"},
	{2200, "Impuesto sobre la renta por pagar", "Income tax payable", "L"},
	{2500, "Deuda a largo plazo", "Long-term debt", "L"},
	{3000, "Capital social", "Share capital", "E"},
	{3100, "Utilidades acumuladas", "Retained earnings", "E"},
	{4000, "Ingresos por ventas externas", "External revenue", "R"},
	{4100, "Ingresos intercompañía", "Intercompany revenue", "R"},
	{5000, "Costo de ventas", "Cost of sales", "X"},
	{5050, "Costo de compras intercompañía", "Intercompany purchases cost", "X"},
	{6000, "Sueldos y salarios", "Salaries and wages", "X"},
	{6100, "Rentas", "Rent", "X"},
	{6200, "Depreciación y amortización", "Depreciation and amortization", "X"},
	{6300, "Otros gastos operativos", "Other operating expenses", "X"},
	{7000, "Gastos financieros", "Interest expense", "X"},
	{8000, "Impuesto sobre la renta", "Income tax expense", "X"},
}

func (a acct) name(p Params) string { return p.tr(a.ES, a.EN) }

func (a acct) debitNormal() bool { return a.Typ == "A" || a.Typ == "X" }

type gcSub struct {
	idx        int
	code       string
	c          Country
	close, avg float64
	revUSD     cents
}

type icPair struct {
	cred, debt int
	flow, bal  cents // USD cents
}

func gcBuild(p Params) ([]gcSub, []icPair) {
	n := p.count("subsidiaries", 8, 1, 20)
	r := newRNG(p, "group_close")
	subs := make([]gcSub, n)
	for i := range subs {
		c := Countries[i%len(Countries)]
		avg := c.Close
		if c.Ccy != "USD" {
			avg = round6(c.Close * (1 + (r.Float64()-0.5)*0.05))
		}
		subs[i] = gcSub{idx: i, code: fmt.Sprintf("S%02d", i+1), c: c, close: round6(c.Close), avg: avg,
			revUSD: between(r, 4_000_000_00, 38_000_000_00)}
	}
	var pairs []icPair
	seen := map[[2]int]bool{}
	add := func(a, b int) {
		if a == b || seen[[2]int{a, b}] {
			return
		}
		seen[[2]int{a, b}] = true
		flow := between(r, 60_000_00, 1_400_000_00)
		pairs = append(pairs, icPair{cred: a, debt: b, flow: flow, bal: frac(flow, 0.08+r.Float64()*0.27)})
	}
	for i := 0; i < n && n > 1; i++ {
		add(i, (i+1)%n) // a ring: every subsidiary trades with the next one
	}
	for i := 0; i < n*n/3; i++ {
		add(r.Intn(n), r.Intn(n))
	}
	return subs, pairs
}

type tbLine struct {
	a      acct
	dr, cr cents
}

// gcTB builds the trial balance of one subsidiary in local currency.
func gcTB(r *rand.Rand, s gcSub, pairs []icPair, subs []gcSub) []tbLine {
	rev := conv(s.revUSD, s.avg)
	var icRecv, icPay, icRev, icCost cents
	for _, pr := range pairs {
		if pr.cred == s.idx {
			icRecv += conv(pr.bal, s.close)
			icRev += conv(pr.flow, s.avg)
		}
		if pr.debt == s.idx {
			icPay += conv(pr.bal, s.close)
			icCost += conv(pr.flow, s.avg)
		}
	}
	cogs := frac(rev, 0.52+r.Float64()*0.14)
	salaries := frac(rev, 0.10+r.Float64()*0.05)
	rent := frac(rev, 0.02+r.Float64()*0.02)
	dep := frac(rev, 0.015+r.Float64()*0.015)
	other := frac(rev, 0.03+r.Float64()*0.03)
	interest := frac(rev, 0.004+r.Float64()*0.012)
	pretax := rev + icRev - cogs - icCost - salaries - rent - dep - other - interest
	var tax cents
	if pretax > 0 {
		tax = frac(pretax, 0.25)
	}
	// Balance sheet at the closing rate (the base is the same USD revenue).
	bs := conv(s.revUSD, s.close)
	vals := map[int]cents{
		1000: frac(bs, 0.07+r.Float64()*0.05), 1100: frac(bs, 0.13+r.Float64()*0.05), 1150: icRecv, 1200: frac(bs, 0.08+r.Float64()*0.05),
		1500: frac(bs, 0.30+r.Float64()*0.10), 1600: frac(bs, 0.03+r.Float64()*0.04),
		2000: frac(bs, 0.08+r.Float64()*0.03), 2050: icPay, 2100: frac(bs, 0.03+r.Float64()*0.02), 2200: frac(tax, 0.35), 2500: frac(bs, 0.15+r.Float64()*0.10),
		3000: frac(bs, 0.12+r.Float64()*0.05),
		4000: rev, 4100: icRev, 5000: cogs, 5050: icCost, 6000: salaries, 6100: rent, 6200: dep, 6300: other, 7000: interest, 8000: tax,
	}
	var dr, cr cents
	for _, a := range Chart {
		if a.Code == 3100 {
			continue
		}
		if a.debitNormal() {
			dr += vals[a.Code]
		} else {
			cr += vals[a.Code]
		}
	}
	var out []tbLine
	for _, a := range Chart {
		l := tbLine{a: a}
		if a.Code == 3100 { // retained earnings is the balancing figure
			if d := dr - cr; d >= 0 {
				l.cr = d
			} else {
				l.dr = -d
			}
		} else if a.debitNormal() {
			l.dr = vals[a.Code]
		} else {
			l.cr = vals[a.Code]
		}
		if l.dr != 0 || l.cr != 0 {
			out = append(out, l)
		}
	}
	_ = subs
	return out
}

func localCode(c Country, group int) string { return fmt.Sprintf("%s-%d", c.Code, group) }

func init() {
	register(GroupSet, genGroupClose)
	register("financial_close", genFinancialClose)
}

func genGroupClose(p Params) Set {
	subs, pairs := gcBuild(p)
	r := newRNG(p, "group_close_tb")
	var set Set
	var extUSD cents
	for _, s := range subs {
		var c csvb
		c.row("sub_code", "period", "currency", "account", "account_name", "debit", "credit")
		for _, l := range gcTB(r, s, pairs, subs) {
			dr, cr := "", ""
			if l.dr != 0 {
				dr = fmtC(l.dr)
			}
			if l.cr != 0 {
				cr = fmtC(l.cr)
			}
			c.row(s.code, GroupPeriod, s.c.Ccy, localCode(s.c, l.a.Code), l.a.name(p)+" ("+s.c.Code+")", dr, cr)
		}
		set.Files = append(set.Files, file("tb", fmt.Sprintf("tb_%s_%s.csv", s.code, s.c.Code), c.bytes()))
		extUSD += s.revUSD
	}

	var ic csvb
	ic.row("period", "type", "creditor_sub", "debtor_sub", "usd_amount", "rate_basis", "creditor_ccy", "creditor_local_amount", "debtor_ccy", "debtor_local_amount", "description")
	for _, pr := range pairs {
		cs, ds := subs[pr.cred], subs[pr.debt]
		ic.row(GroupPeriod, "balance", cs.code, ds.code, fmtC(pr.bal), "closing", cs.c.Ccy, fmtC(conv(pr.bal, cs.close)), ds.c.Ccy, fmtC(conv(pr.bal, ds.close)),
			p.tr("Saldo por cobrar / por pagar de servicios entre filiales", "Receivable / payable balance for services between subsidiaries"))
	}
	for _, pr := range pairs {
		cs, ds := subs[pr.cred], subs[pr.debt]
		ic.row(GroupPeriod, "sales", cs.code, ds.code, fmtC(pr.flow), "average", cs.c.Ccy, fmtC(conv(pr.flow, cs.avg)), ds.c.Ccy, fmtC(conv(pr.flow, ds.avg)),
			p.tr("Ventas de servicios del trimestre (ingreso del acreedor, costo del deudor)", "Services sold in the quarter (creditor revenue, debtor cost)"))
	}
	set.Files = append(set.Files, file("intercompany", "intercompany_matrix.csv", ic.bytes()))

	var fx csvb
	fx.row("date", "currency", "quote", "closing_rate", "average_rate")
	done := map[string]bool{}
	fx.row(GroupPeriod, "USD", "local per 1 USD", fmtRate(1), fmtRate(1))
	done["USD"] = true
	for _, s := range subs {
		if done[s.c.Ccy] {
			continue
		}
		done[s.c.Ccy] = true
		fx.row(GroupPeriod, s.c.Ccy, "local per 1 USD", fmtRate(s.close), fmtRate(s.avg))
	}
	set.Files = append(set.Files, file("fx_rates", "fx_rates.csv", fx.bytes()))

	var coa csvb
	coa.row("sub_code", "local_account", "local_name", "group_account", "group_name", "group_type")
	for _, s := range subs {
		for _, a := range Chart {
			coa.row(s.code, localCode(s.c, a.Code), a.name(p)+" ("+s.c.Code+")", fmt.Sprint(a.Code), a.name(p), a.Typ)
		}
	}
	set.Files = append(set.Files, file("coa_mapping", "coa_mapping.csv", coa.bytes()))

	set.Files = append(set.Files, md("materiality", "materiality_policy.md", string(gcMateriality(p, extUSD, len(subs)))))
	set.Files = append(set.Files, md("", "README_datos_de_ejemplo.md", string(gcReadme(p, subs))))
	return set
}

func roundTo(c cents, unit cents) cents { return (c + unit/2) / unit * unit }

func gcMateriality(p Params, extUSD cents, n int) []byte {
	mat := roundTo(frac(extUSD, 0.005), 100_000)
	perf := frac(mat, 0.75)
	trivial := roundTo(frac(mat, 0.05), 100_00)
	ic := roundTo(frac(mat, 0.02), 100_00)
	usd := func(c cents) string { return "USD " + commas(c/100) }
	if p.en() {
		return []byte(fmt.Sprintf(`# Materiality and consolidation policy - Grupo Aurora (sample data)

%s

- Reporting currency: USD. Closing date: %s. Subsidiaries in scope: %d.
- Group external revenue (sum of the subsidiaries, at the average rate): %s.
- **Group materiality: %s** (0.5%% of external revenue, rounded to USD 1,000).
- Performance materiality: %s (75%% of materiality).
- Clearly trivial threshold (do not accumulate): %s (5%% of materiality).
- Intercompany tolerance: differences up to %s between creditor and debtor are accepted; larger ones are investigated and reported.

## Translation (FX)
- Assets and liabilities: closing rate. Revenue and expenses: average rate of the period. Share capital and retained earnings: historical rate (not provided: use the closing rate and say so as an ASSUMPTION).
- Rates are quoted as local currency per 1 USD (file fx_rates.csv).

## Eliminations
- Eliminate intercompany balances (accounts 1150 against 2050) and intercompany sales against purchases (4100 against 5050).
- Report any unreconciled difference above the tolerance with the subsidiary pair and the amount.
`, fictionalEN, GroupPeriod, n, usd(extUSD), usd(mat), usd(perf), usd(trivial), usd(ic)))
	}
	return []byte(fmt.Sprintf(`# Política de materialidad y consolidación - Grupo Aurora (datos de ejemplo)

%s

- Moneda de reporte: USD. Fecha de cierre: %s. Filiales en alcance: %d.
- Ingresos externos del grupo (suma de las filiales, a tipo de cambio promedio): %s.
- **Materialidad del grupo: %s** (0.5%% de los ingresos externos, redondeada a USD 1,000).
- Materialidad de ejecución: %s (75%% de la materialidad).
- Umbral de claramente trivial (no se acumula): %s (5%% de la materialidad).
- Tolerancia intercompañía: se aceptan diferencias de hasta %s entre acreedor y deudor; las mayores se investigan y se reportan.

## Conversión (tipo de cambio)
- Activos y pasivos: tipo de cambio de cierre. Ingresos y gastos: tipo de cambio promedio del periodo. Capital social y utilidades acumuladas: tipo histórico (no se entrega: usa el de cierre y dilo como SUPUESTO).
- Los tipos están expresados en moneda local por 1 USD (archivo fx_rates.csv).

## Eliminaciones
- Elimina los saldos intercompañía (cuenta 1150 contra 2050) y las ventas contra las compras intercompañía (4100 contra 5050).
- Reporta cualquier diferencia sin conciliar por encima de la tolerancia con el par de filiales y el importe.
`, fictionalES, GroupPeriod, n, usd(extUSD), usd(mat), usd(perf), usd(trivial), usd(ic)))
}

func commas(n int64) string {
	s := fmt.Sprint(n)
	var out []string
	for len(s) > 3 {
		out = append([]string{s[len(s)-3:]}, out...)
		s = s[:len(s)-3]
	}
	out = append([]string{s}, out...)
	return strings.Join(out, ",")
}

func gcReadme(p Params, subs []gcSub) []byte {
	var b strings.Builder
	b.WriteString(p.tr("# Datos de ejemplo: cierre consolidado del grupo\n\n", "# Sample data: group consolidated close\n\n"))
	b.WriteString(p.fiction() + "\n\n")
	b.WriteString(p.tr("Archivos:\n", "Files:\n"))
	b.WriteString(p.tr("- `tb_<filial>_<pais>.csv`: balanza de comprobación por filial, en moneda local (debe = haber).\n", "- `tb_<sub>_<country>.csv`: trial balance per subsidiary, in local currency (debit = credit).\n"))
	b.WriteString(p.tr("- `intercompany_matrix.csv`: saldos y ventas entre filiales, vistos por el acreedor y por el deudor.\n", "- `intercompany_matrix.csv`: balances and sales between subsidiaries, seen by the creditor and by the debtor.\n"))
	b.WriteString(p.tr("- `fx_rates.csv`: tipos de cambio de cierre y promedio (moneda local por 1 USD).\n", "- `fx_rates.csv`: closing and average FX rates (local currency per 1 USD).\n"))
	b.WriteString(p.tr("- `coa_mapping.csv`: cuenta local a cuenta del grupo.\n", "- `coa_mapping.csv`: local account to group account.\n"))
	b.WriteString(p.tr("- `materiality_policy.md`: materialidad y reglas de consolidación.\n\nFiliales:\n", "- `materiality_policy.md`: materiality and consolidation rules.\n\nSubsidiaries:\n"))
	for _, s := range subs {
		b.WriteString(fmt.Sprintf("- %s: %s (%s)\n", s.code, p.tr(s.c.ES, s.c.EN), s.c.Ccy))
	}
	return []byte(b.String())
}

// genFinancialClose is the sample data of the single-entity quarterly close: one trial balance.
func genFinancialClose(p Params) Set {
	q := p
	q.Count = map[string]int{"subsidiaries": 1}
	subs, pairs := gcBuild(q)
	r := newRNG(p, "financial_close")
	s := subs[0]
	var c csvb
	c.row("sub_code", "period", "currency", "account", "account_name", "debit", "credit")
	for _, l := range gcTB(r, s, pairs, subs) {
		dr, cr := "", ""
		if l.dr != 0 {
			dr = fmtC(l.dr)
		}
		if l.cr != 0 {
			cr = fmtC(l.cr)
		}
		c.row(s.code, GroupPeriod, s.c.Ccy, fmt.Sprint(l.a.Code), l.a.name(p), dr, cr)
	}
	return Set{Files: []File{file("trial_balance", "trial_balance.csv", c.bytes()),
		md("", "README_datos_de_ejemplo.md", (p.tr("# Datos de ejemplo: cierre financiero\n\n"+fictionalES+"\n\nBalanza de comprobación de una entidad en "+s.c.Ccy+".\n",
			"# Sample data: financial close\n\n"+fictionalEN+"\n\nTrial balance of one entity in "+s.c.Ccy+".\n")))}}
}
