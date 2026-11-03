package invoicepnl

import (
	"encoding/json"
	"strings"
	"testing"

	"aiworkforce/backend/internal/officedoc"
)

func amt(c int64) Amount { return Amount{Cents: c, Set: true} }

var acme = Company{Name: "Acme, S.A. de C.V.", TaxID: "ACM010101AB1"}

func inv(num, issuer, issTax, receiver, recTax string, sub, tax, tot int64) Invoice {
	return Invoice{File: "/Shared/Facturas/" + num + ".pdf", Page: 1, Date: "2026-10-05", Number: num, Issuer: issuer, IssuerTaxID: issTax,
		Receiver: receiver, ReceiverTaxID: recTax, Concept: "x", Currency: "MXN", Subtotal: amt(sub), Taxes: amt(tax), Total: amt(tot)}
}

func TestParseAmount(t *testing.T) {
	for in, want := range map[string]int64{"1234.56": 123456, "1.234,56": 123456, "1,234.56": 123456, "$ 1 234,5": 123450, "(100)": -10000,
		"-7,5": -750, "1.234.567": 123456700, "MXN 99.999": 0, "0,1": 10, "12": 1200} {
		a := ParseAmount(in)
		if in == "MXN 99.999" {
			if !a.Ambiguous {
				t.Fatalf("%q must be ambiguous", in)
			}
			continue
		}
		if !a.Set || a.Bad || a.Cents != want {
			t.Fatalf("%q = %+v, want %d", in, a, want)
		}
	}
	if !ParseAmount("12a#").Bad || ParseAmount("").Set {
		t.Fatal("bad / empty")
	}
	var x struct{ A, B, C Amount }
	if err := json.Unmarshal([]byte(`{"A": 10.005, "B": "2.500,10", "C": null}`), &x); err != nil || x.A.Cents != 1001 || x.B.Cents != 250010 || x.C.Set {
		t.Fatalf("json: %+v %v", x, err)
	}
}

func TestComputeIncomeStatement(t *testing.T) {
	in := Input{Company: acme, Currency: "MXN", Invoices: []Invoice{
		inv("A-1", "ACME SA de CV", "ACM010101AB1", "Cliente Uno", "CUN", 100000, 16000, 116000), // income
		inv("A-2", "Acme", "", "Cliente Dos", "", 50050, 8008, 58058),                             // income by name
		func() Invoice {
			i := inv("P-9", "Proveedor Mercancía", "PRV", "Acme S.A. de C.V.", "ACM010101AB1", 40000, 6400, 46400)
			i.Category = "Costo de mercancía"
			return i
		}(),
		func() Invoice {
			i := inv("R-3", "Inmobiliaria", "INM", "acme", "acm010101ab1", 20000, 3200, 23200)
			i.Category = "Renta"
			return i
		}(),
	}}
	res, err := Compute(in)
	if err != nil {
		t.Fatal(err)
	}
	st := res.Statement
	if st.Revenue != 150050 || st.Cost != 40000 || st.Gross != 110050 || st.Expenses != 20000 || st.Operating != 90050 {
		t.Fatalf("statement: %+v", st)
	}
	if st.TaxesIssued != 24008 || st.TaxesReceived != 9600 || res.Included != 4 || len(res.Review) != 0 || res.Period != "2026-10-05" {
		t.Fatalf("memo / counts: %+v %+v", st, res.Review)
	}
	if res.Rows[0].Kind != KindIncome || res.Rows[2].Group != GroupCost || res.Rows[3].Group != GroupExpense || res.Rows[3].Category != "Renta" {
		t.Fatalf("rows: %+v", res.Rows)
	}
	// deterministic: same input, same result
	again, _ := Compute(in)
	a, _ := json.Marshal(res)
	b, _ := json.Marshal(again)
	if string(a) != string(b) {
		t.Fatal("not deterministic")
	}
}

func TestComputeReviewNeverDropsAnInvoice(t *testing.T) {
	scanned := inv("S-1", "Acme", "", "Cliente", "", 0, 0, 0)
	scanned.Status, scanned.Subtotal, scanned.Taxes, scanned.Total = "scanned", Amount{}, Amount{}, Amount{}
	usd := inv("U-1", "Acme", "", "Client US", "", 10000, 0, 10000)
	usd.Currency = "USD"
	mismatch := inv("M-1", "Acme", "", "Cliente", "", 10000, 1600, 12000)
	nobody := inv("N-1", "Otro", "", "Tercero", "", 5000, 800, 5800)
	byAgent := inv("G-1", "Otro", "", "Tercero", "", 7000, 0, 7000)
	byAgent.Type = "expense"
	in := Input{Company: acme, Currency: "MXN", Invoices: []Invoice{
		inv("D-1", "Acme", "", "Cliente", "", 10000, 1600, 11600),
		inv("d-01", "ACME", "", "Cliente", "", 10000, 1600, 11600), // duplicate: same number (normalized) and issuer
		scanned, usd, mismatch, nobody, byAgent,
	}}
	res, err := Compute(in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Read != 7 || len(res.Rows) != 7 {
		t.Fatalf("every invoice is a row: %+v", res)
	}
	reasons := map[int]Review{}
	for _, r := range res.Review {
		reasons[r.Row] = r
	}
	want := map[int]struct {
		frag     string
		included bool
	}{
		2: {"duplicada", false}, 3: {"escaneada: no se pudo leer el texto", false}, 4: {"moneda USD sin tipo de cambio", false},
		5: {"no cuadra", true}, 6: {"no se pudo clasificar", false}, 7: {"según el agente", true},
	}
	for row, w := range want {
		r, ok := reasons[row]
		if !ok || !strings.Contains(r.Reason, w.frag) || r.Included != w.included {
			t.Fatalf("row %d: %+v (want %q included=%v)", row, r, w.frag, w.included)
		}
	}
	if _, ok := reasons[1]; ok {
		t.Fatal("the first of the duplicates counts and is clean")
	}
	// revenue: D-1 (10000) + mismatch (10000, its subtotal); expense: byAgent (7000)
	if res.Statement.Revenue != 20000 || res.Statement.Expenses != 7000 || res.Included != 3 {
		t.Fatalf("statement: %+v included=%d", res.Statement, res.Included)
	}
	// with an exchange rate the USD invoice is converted and added
	in.Rates = map[string]float64{"usd": 18.5}
	res, _ = Compute(in)
	if res.Statement.Revenue != 20000+185000 {
		t.Fatalf("converted: %+v", res.Statement)
	}
}

func TestComputeRefusesWithoutCompanyOrRows(t *testing.T) {
	if _, err := Compute(Input{Invoices: []Invoice{inv("1", "a", "", "b", "", 1, 0, 1)}}); err == nil {
		t.Fatal("company required")
	}
	if _, err := Compute(Input{Company: acme}); err == nil {
		t.Fatal("rows required")
	}
}

func TestWorkbookHasTheThreeSheets(t *testing.T) {
	bad := inv("X", "Acme", "", "C", "", 0, 0, 0)
	bad.Status = "unreadable"
	res, _ := Compute(Input{Company: acme, Invoices: []Invoice{inv("A-1", "Acme", "", "C", "", 100000, 16000, 116000), bad}, Locale: "es"})
	doc, fileCol := Workbook(res, "es")
	if len(doc.Sheets) != 3 || doc.Sheets[0].Name != "Detalle" || doc.Sheets[1].Name != "Estado de resultados" || doc.Sheets[2].Name != "Revisar" {
		t.Fatalf("sheets: %+v", doc.Sheets)
	}
	if doc.Sheets[0].Columns[fileCol-1].Header != "Archivo" || len(doc.Sheets[0].Rows) != 2 || len(doc.Sheets[2].Rows) != 1 {
		t.Fatalf("detail / review: %+v", doc.Sheets)
	}
	data, err := officedoc.Build("xlsx", doc)
	if err != nil || len(data) < 1000 {
		t.Fatalf("build: %v", err)
	}
	en, _ := Workbook(res, "en")
	if en.Sheets[1].Name != "Income statement" {
		t.Fatal("english")
	}
}
