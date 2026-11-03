// Package invoicepnl turns invoice rows (extracted by an agent from PDFs) into an income statement, computed in code,
// never by the model (docs/superpowers/specs/2026-11-03-agentes-ubican-archivos-y-accesos-design.md, part 2).
//
// Money is kept in integer cents; nothing is added in floating point. Every invoice ends either in the result or in the
// "to review" list with its reason, never silently dropped:
//
//	unreadable / scanned          to review, not added ("escaneada: no se pudo leer el texto")
//	duplicate (number + issuer)   the first one counts; the others to review, not added
//	subtotal + taxes != total     to review, added with its subtotal
//	another currency, no rate     to review, not added (currencies are never added without an exchange rate)
//	classification in doubt       to review; added as the agent classified it when it said, otherwise not added
//
// Income = invoices the company issued, expense = invoices it received (by tax id, else by name); the income
// statement uses subtotals (taxes are shown apart, they are not income nor expense).
package invoicepnl

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// MaxInvoices bounds one statement.
const MaxInvoices = 2000

// Invoice is one row extracted from a file.
type Invoice struct {
	File          string `json:"file"`
	Page          int    `json:"page,omitempty"`
	Date          string `json:"date"`
	Number        string `json:"number"`
	Issuer        string `json:"issuer"`
	IssuerTaxID   string `json:"issuer_tax_id,omitempty"`
	Receiver      string `json:"receiver"`
	ReceiverTaxID string `json:"receiver_tax_id,omitempty"`
	Concept       string `json:"concept"`
	// Category is the account or category the agent suggests ("Ventas", "Renta", "Costo de ventas"...).
	Category string `json:"category,omitempty"`
	Currency string `json:"currency,omitempty"`
	Subtotal Amount `json:"subtotal"`
	Taxes    Amount `json:"taxes"`
	Total    Amount `json:"total"`
	// Type is the agent's reading: income | expense ("" = let the company decide).
	Type string `json:"type,omitempty"`
	// Status: ok (default) | unreadable | scanned. Reason says why (shown in "to review").
	Status string `json:"status,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Doubt is a classification doubt the agent wants a person to look at.
	Doubt string `json:"doubt,omitempty"`
}

// Amount is a money value as the model sends it: a JSON number or a string ("1.234,56", "$1,234.56", "(100)").
type Amount struct {
	Cents int64
	Set   bool
	// Ambiguous: a string like "1.234" that could be 1234 or 1.234.
	Ambiguous bool
	Bad       bool
}

// UnmarshalJSON accepts numbers, strings and null.
func (a *Amount) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` {
		*a = Amount{}
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		uq, err := strconv.Unquote(s)
		if err != nil {
			*a = Amount{Bad: true}
			return nil
		}
		*a = ParseAmount(uq)
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > 1e13 {
		*a = Amount{Bad: true}
		return nil
	}
	*a = Amount{Cents: int64(math.Round(f * 100)), Set: true}
	return nil
}

// ParseAmount reads a money string with either decimal separator.
func ParseAmount(s string) Amount {
	s = strings.TrimSpace(s)
	if s == "" {
		return Amount{}
	}
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, s[1:len(s)-1]
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r == '.', r == ',':
			b.WriteRune(r)
		case r == '-':
			neg = !neg
		case unicode.IsSpace(r), r == '\'', r == '’', unicode.IsLetter(r), unicode.Is(unicode.Sc, r):
			// thousands spaces, currency symbols and codes
		default:
			return Amount{Bad: true}
		}
	}
	t := b.String()
	if t == "" {
		return Amount{Bad: true}
	}
	lastDot, lastComma := strings.LastIndex(t, "."), strings.LastIndex(t, ",")
	ambiguous := false
	dec := byte(0)
	switch {
	case lastDot >= 0 && lastComma >= 0:
		dec = t[max(lastDot, lastComma)]
	case lastDot >= 0 || lastComma >= 0:
		sep := byte('.')
		i := lastDot
		if lastComma >= 0 {
			sep, i = ',', lastComma
		}
		after := len(t) - i - 1
		count := strings.Count(t, string(sep))
		switch {
		case count > 1:
			dec = 0 // 1.234.567 or 1,234,567: thousands
		case after == 3:
			ambiguous = true // 1.234 / 1,234
			dec = 0
		default:
			dec = sep
		}
	}
	intPart, frac := t, ""
	if dec != 0 {
		i := strings.LastIndexByte(t, dec)
		intPart, frac = t[:i], t[i+1:]
	}
	intPart = strings.NewReplacer(".", "", ",", "").Replace(intPart)
	if strings.ContainsAny(frac, ".,") || len(frac) > 4 || len(intPart) > 13 {
		return Amount{Bad: true}
	}
	if intPart == "" {
		intPart = "0"
	}
	n, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return Amount{Bad: true}
	}
	cents := n * 100
	if frac != "" {
		f := (frac + "0000")[:4]
		x, _ := strconv.ParseInt(f, 10, 64)
		cents += (x + 50) / 100 // round to cents
	}
	if neg {
		cents = -cents
	}
	return Amount{Cents: cents, Set: true, Ambiguous: ambiguous}
}

// Company is who the statement is for.
type Company struct {
	Name  string `json:"name"`
	TaxID string `json:"tax_id,omitempty"`
}

// Input of Compute.
type Input struct {
	Company  Company `json:"company"`
	Currency string  `json:"currency,omitempty"` // base currency; "" = the most common one of the invoices
	// Rates converts other currencies into the base currency: 1 unit of the key = rate units of the base.
	Rates    map[string]float64 `json:"rates,omitempty"`
	Invoices []Invoice          `json:"invoices"`
	Locale   string             `json:"locale,omitempty"`
}

// Kinds of a row.
const (
	KindIncome  = "income"
	KindExpense = "expense"
)

// Groups of the statement.
const (
	GroupRevenue = "revenue"
	GroupCost    = "cost"
	GroupExpense = "expense"
)

// Row is one invoice with what the system decided.
type Row struct {
	Invoice
	Kind     string `json:"kind"`  // income | expense | "" (not classified)
	Group    string `json:"group"` // revenue | cost | expense
	Category string `json:"category_used"`
	Subtotal int64  `json:"subtotal_cents"`
	Taxes    int64  `json:"taxes_cents"`
	Total    int64  `json:"total_cents"`
	// Base is the subtotal in the base currency (what the statement adds).
	Base     int64    `json:"base_cents"`
	Included bool     `json:"included"`
	Reasons  []string `json:"reasons,omitempty"`
}

// Review is one line of the "to review" sheet.
type Review struct {
	Row      int    `json:"row"` // 1-based index in the input
	File     string `json:"file"`
	Page     int    `json:"page,omitempty"`
	Number   string `json:"number"`
	Issuer   string `json:"issuer"`
	Reason   string `json:"reason"`
	Included bool   `json:"included"`
}

// CatAmount is a category of the statement.
type CatAmount struct {
	Name   string `json:"name"`
	Amount int64  `json:"amount_cents"`
	Count  int    `json:"count"`
}

// Statement is the income statement in the base currency (cents).
type Statement struct {
	Revenue       int64       `json:"revenue_cents"`
	RevenueByCat  []CatAmount `json:"revenue_by_category"`
	Cost          int64       `json:"cost_cents"`
	CostByCat     []CatAmount `json:"cost_by_category"`
	Gross         int64       `json:"gross_profit_cents"`
	Expenses      int64       `json:"expenses_cents"`
	ExpensesByCat []CatAmount `json:"expenses_by_category"`
	Operating     int64       `json:"operating_profit_cents"`
	TaxesIssued   int64       `json:"taxes_issued_cents"`   // taxes charged on income invoices (memo)
	TaxesReceived int64       `json:"taxes_received_cents"` // taxes paid on expense invoices (memo)
}

// Result of Compute.
type Result struct {
	Currency  string    `json:"currency"`
	Rows      []Row     `json:"rows"`
	Statement Statement `json:"statement"`
	Review    []Review  `json:"review"`
	Read      int       `json:"read"`     // invoices received
	Included  int       `json:"included"` // added to the statement
	Period    string    `json:"period,omitempty"`
}

// Compute classifies, checks and adds up the invoices. It is pure and deterministic.
func Compute(in Input) (Result, error) {
	if len(in.Invoices) == 0 {
		return Result{}, fmt.Errorf("no invoices")
	}
	if len(in.Invoices) > MaxInvoices {
		return Result{}, fmt.Errorf("too many invoices (max %d)", MaxInvoices)
	}
	if strings.TrimSpace(in.Company.Name) == "" && strings.TrimSpace(in.Company.TaxID) == "" {
		return Result{}, fmt.Errorf("company name or tax_id is required (to tell income from expenses)")
	}
	lab := labelsFor(in.Locale)
	base := normCurrency(in.Currency)
	if base == "" {
		base = commonCurrency(in.Invoices)
	}
	rates := map[string]float64{}
	for k, v := range in.Rates {
		if v > 0 && !math.IsInf(v, 0) && !math.IsNaN(v) {
			rates[normCurrency(k)] = v
		}
	}
	res := Result{Currency: base, Read: len(in.Invoices)}
	seen := map[string]int{}
	var minDate, maxDate string
	for i, inv := range in.Invoices {
		inv = clip(inv)
		r := Row{Invoice: inv}
		reason := func(s string) { r.Reasons = append(r.Reasons, s) }
		include := true
		st := strings.ToLower(strings.TrimSpace(inv.Status))
		if st == "unreadable" || st == "scanned" || st == "illegible" {
			msg := strings.TrimSpace(inv.Reason)
			if msg == "" {
				msg = lab.unreadable
				if st == "scanned" {
					msg = lab.scanned
				}
			}
			reason(msg)
			include = false
		}
		// amounts
		if include {
			for _, a := range []Amount{inv.Subtotal, inv.Taxes, inv.Total} {
				if a.Bad {
					reason(lab.badAmount)
					include = false
					break
				}
				if a.Ambiguous {
					reason(lab.ambiguous)
					include = false
					break
				}
			}
		}
		if include {
			sub, tax, tot := inv.Subtotal, inv.Taxes, inv.Total
			switch {
			case sub.Set && tax.Set && tot.Set:
				r.Subtotal, r.Taxes, r.Total = sub.Cents, tax.Cents, tot.Cents
				if d := abs(sub.Cents + tax.Cents - tot.Cents); d > max(2, abs(tot.Cents)/2000) {
					reason(fmt.Sprintf(lab.mismatch, money(sub.Cents+tax.Cents), money(tot.Cents)))
				}
			case sub.Set && tax.Set:
				r.Subtotal, r.Taxes, r.Total = sub.Cents, tax.Cents, sub.Cents+tax.Cents
			case sub.Set && tot.Set:
				r.Subtotal, r.Taxes, r.Total = sub.Cents, tot.Cents-sub.Cents, tot.Cents
			case tot.Set && tax.Set:
				r.Subtotal, r.Taxes, r.Total = tot.Cents-tax.Cents, tax.Cents, tot.Cents
			case sub.Set:
				r.Subtotal, r.Total = sub.Cents, sub.Cents
			case tot.Set:
				r.Subtotal, r.Total = tot.Cents, tot.Cents
				reason(lab.noBreakdown)
			default:
				reason(lab.noAmount)
				include = false
			}
		}
		// classification
		kind, doubt := classify(in.Company, inv)
		if kind == "" {
			if t := strings.ToLower(strings.TrimSpace(inv.Type)); t == KindIncome || t == KindExpense {
				kind = t
				reason(fmt.Sprintf(lab.classifiedByAgent, lab.kind(t)) + doubtSuffix(doubt))
			} else {
				reason(lab.notClassified + doubtSuffix(doubt))
				include = false
			}
		} else if t := strings.ToLower(strings.TrimSpace(inv.Type)); (t == KindIncome || t == KindExpense) && t != kind {
			reason(fmt.Sprintf(lab.typeConflict, lab.kind(kind), lab.kind(t)))
		}
		if d := strings.TrimSpace(inv.Doubt); d != "" {
			reason(lab.doubt + d)
		}
		r.Kind = kind
		r.Group, r.Category = group(kind, inv.Category, lab)
		// duplicates: same number and issuer
		if key := dupKey(inv); key != "" {
			if first, ok := seen[key]; ok {
				reason(fmt.Sprintf(lab.duplicate, first))
				include = false
			} else {
				seen[key] = i + 1
			}
		}
		// currency
		cur := normCurrency(inv.Currency)
		if cur == "" {
			cur = base
		}
		r.Currency = cur
		r.Base = r.Subtotal
		if include && cur != base {
			rate, ok := rates[cur]
			if !ok {
				reason(fmt.Sprintf(lab.noRate, cur, base))
				include = false
			} else {
				r.Base = int64(math.Round(float64(r.Subtotal) * rate))
			}
		}
		r.Included = include
		if include {
			res.Included++
			if d := normDate(inv.Date); d != "" {
				if minDate == "" || d < minDate {
					minDate = d
				}
				if d > maxDate {
					maxDate = d
				}
			}
		}
		if len(r.Reasons) > 0 {
			res.Review = append(res.Review, Review{Row: i + 1, File: inv.File, Page: inv.Page, Number: inv.Number, Issuer: inv.Issuer,
				Reason: strings.Join(r.Reasons, "; "), Included: include})
		}
		res.Rows = append(res.Rows, r)
	}
	if minDate != "" {
		res.Period = minDate
		if maxDate != minDate {
			res.Period += " - " + maxDate
		}
	}
	res.Statement = statement(res.Rows, rates, base)
	return res, nil
}

func doubtSuffix(d string) string {
	if d == "" {
		return ""
	}
	return " (" + d + ")"
}

func statement(rows []Row, rates map[string]float64, base string) Statement {
	var st Statement
	cats := map[string]map[string]*CatAmount{GroupRevenue: {}, GroupCost: {}, GroupExpense: {}}
	order := map[string][]string{}
	for _, r := range rows {
		if !r.Included {
			continue
		}
		g := cats[r.Group]
		key := strings.ToLower(r.Category)
		c := g[key]
		if c == nil {
			c = &CatAmount{Name: r.Category}
			g[key] = c
			order[r.Group] = append(order[r.Group], key)
		}
		c.Amount += r.Base
		c.Count++
		tax := r.Taxes
		if r.Currency != base {
			tax = int64(math.Round(float64(r.Taxes) * rates[r.Currency]))
		}
		switch r.Group {
		case GroupRevenue:
			st.Revenue += r.Base
			st.TaxesIssued += tax
		case GroupCost:
			st.Cost += r.Base
			st.TaxesReceived += tax
		default:
			st.Expenses += r.Base
			st.TaxesReceived += tax
		}
	}
	list := func(g string) []CatAmount {
		out := []CatAmount{}
		for _, k := range order[g] {
			out = append(out, *cats[g][k])
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Amount > out[j].Amount })
		return out
	}
	st.RevenueByCat, st.CostByCat, st.ExpensesByCat = list(GroupRevenue), list(GroupCost), list(GroupExpense)
	st.Gross = st.Revenue - st.Cost
	st.Operating = st.Gross - st.Expenses
	return st
}

// ---- classification ----

var legalSuffixes = []string{"s a de c v", "sa de cv", "s de rl de cv", "s a", "sa", "s l", "sl", "sas", "s a s", "srl", "s r l",
	"ltda", "ltd", "llc", "inc", "corp", "co", "gmbh", "bv", "plc", "spa", "eireli", "me", "epp"}

func fold(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		out = s
	}
	var b strings.Builder
	for _, r := range strings.ToLower(out) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func normName(s string) string {
	n := " " + fold(s) + " "
	for changed := true; changed; {
		changed = false
		for _, suf := range legalSuffixes {
			if strings.HasSuffix(n, " "+suf+" ") && len(n) > len(suf)+3 {
				n = n[:len(n)-len(suf)-1]
				changed = true
			}
		}
	}
	return strings.TrimSpace(n)
}

func normTax(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// matches: by tax id when both have one, otherwise by normalized name.
func matches(c Company, name, tax string) bool {
	if ct, t := normTax(c.TaxID), normTax(tax); ct != "" && t != "" {
		return ct == t
	}
	cn, n := normName(c.Name), normName(name)
	return cn != "" && n != "" && cn == n
}

// classify: the company issued it = income, received it = expense; "" when it cannot tell (with the doubt).
func classify(c Company, inv Invoice) (string, string) {
	iss := matches(c, inv.Issuer, inv.IssuerTaxID)
	rec := matches(c, inv.Receiver, inv.ReceiverTaxID)
	switch {
	case iss && !rec:
		return KindIncome, ""
	case rec && !iss:
		return KindExpense, ""
	case iss && rec:
		return "", "issuer and receiver are both the company"
	}
	return "", "neither the issuer nor the receiver is the company"
}

var costWords = []string{"costo", "coste", "cost of", "cogs", "mercancia", "mercaderia", "inventario", "materia prima", "materias primas",
	"compra de", "compras", "merchandise", "inventory", "raw material", "purchases", "custo", "mercadoria"}

func group(kind, category string, lab labels) (string, string) {
	c := strings.TrimSpace(category)
	switch kind {
	case KindIncome:
		if c == "" {
			c = lab.defaultRevenue
		}
		return GroupRevenue, c
	case KindExpense:
		f := fold(c)
		for _, w := range costWords {
			if strings.Contains(f, w) {
				if c == "" {
					c = lab.defaultCost
				}
				return GroupCost, c
			}
		}
		if c == "" {
			c = lab.defaultExpense
		}
		return GroupExpense, c
	}
	if c == "" {
		c = "-"
	}
	return "", c
}

func dupKey(inv Invoice) string {
	num := stripZeros(normTax(inv.Number))
	if num == "" {
		return ""
	}
	who := normTax(inv.IssuerTaxID)
	if who == "" {
		who = normName(inv.Issuer)
	}
	return who + "|" + num
}

// stripZeros drops the leading zeros of every run of digits ("F-0012" and "F12" are the same number).
func stripZeros(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] < '0' || s[i] > '9' {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		run := strings.TrimLeft(s[i:j], "0")
		if run == "" {
			run = "0"
		}
		b.WriteString(run)
		i = j
	}
	return b.String()
}

// ---- small helpers ----

func normCurrency(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	switch s {
	case "US$", "USD$":
		return "USD"
	case "€":
		return "EUR"
	case "R$":
		return "BRL"
	}
	if len(s) > 8 {
		s = s[:8]
	}
	return s
}

func commonCurrency(invs []Invoice) string {
	n := map[string]int{}
	best, bestN := "", 0
	for _, inv := range invs {
		c := normCurrency(inv.Currency)
		if c == "" {
			continue
		}
		n[c]++
		if n[c] > bestN || n[c] == bestN && c < best {
			best, bestN = c, n[c]
		}
	}
	return best
}

// normDate keeps ISO dates (YYYY-MM-DD) for the period; anything else is ignored for it.
func normDate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 10 && s[4] == '-' && s[7] == '-' {
		return s[:10]
	}
	return ""
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// money formats cents as 1234.56 (the sheet formats the cells; this is for the reasons text).
func money(c int64) string {
	sign := ""
	if c < 0 {
		sign, c = "-", -c
	}
	return fmt.Sprintf("%s%d.%02d", sign, c/100, c%100)
}

// Money is money() for other packages (summaries).
func Money(c int64) string { return money(c) }

func clip(inv Invoice) Invoice {
	c := func(s string, n int) string {
		s = strings.TrimSpace(s)
		if r := []rune(s); len(r) > n {
			return string(r[:n])
		}
		return s
	}
	inv.File, inv.Date, inv.Number = c(inv.File, 400), c(inv.Date, 40), c(inv.Number, 80)
	inv.Issuer, inv.IssuerTaxID, inv.Receiver, inv.ReceiverTaxID = c(inv.Issuer, 200), c(inv.IssuerTaxID, 40), c(inv.Receiver, 200), c(inv.ReceiverTaxID, 40)
	inv.Concept, inv.Category, inv.Currency = c(inv.Concept, 300), c(inv.Category, 80), c(inv.Currency, 12)
	inv.Reason, inv.Doubt, inv.Status, inv.Type = c(inv.Reason, 300), c(inv.Doubt, 300), c(inv.Status, 20), c(inv.Type, 20)
	if inv.Page < 0 || inv.Page > 100000 {
		inv.Page = 0
	}
	return inv
}
