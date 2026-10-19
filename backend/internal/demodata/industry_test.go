package demodata

import (
	"bytes"
	"math"
	"strconv"
	"strings"
	"testing"
)

func indGen(t *testing.T, name string, p Params) Set {
	t.Helper()
	s, err := GenerateIndustry(name, p)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return s
}

func indRows(t *testing.T, s Set, name string) (map[string]int, [][]string) {
	t.Helper()
	for _, f := range s.Files {
		if f.Name == name {
			rows := parseCSV(t, f.Data)
			h := map[string]int{}
			for i, c := range rows[0] {
				h[c] = i
			}
			return h, rows[1:]
		}
	}
	t.Fatalf("file %s not generated", name)
	return nil, nil
}

func num(t *testing.T, s string) float64 {
	t.Helper()
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("not a number %q", s)
	}
	return f
}

func cc(t *testing.T, s string) int64 { return int64(math.Round(num(t, s) * 100)) }

func TestIndustrySetsAreSeparateFromTheMainRegistry(t *testing.T) {
	want := []string{"accounting_firm", "ecommerce_store", "law_firm", "marketing_agency", "multinational_group"}
	got := IndustryNames()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("industry sets %v", got)
	}
	for _, n := range Names() {
		if HasIndustry(n) {
			t.Errorf("%s leaked into the main registry", n)
		}
	}
	if len(Names()) != 12 {
		t.Fatalf("main registry changed: %v", Names())
	}
	if _, err := GenerateIndustry("nope", Params{}); err == nil {
		t.Fatal("unknown set must fail")
	}
}

func TestIndustrySetsAreLocalizedAndDeterministic(t *testing.T) {
	for _, name := range IndustryNames() {
		readmes := map[string]string{}
		for _, loc := range []string{"es", "en", "pt"} {
			a, b := indGen(t, name, Params{Locale: loc}), indGen(t, name, Params{Locale: loc})
			if len(a.Files) < 3 {
				t.Fatalf("%s/%s: only %d files", name, loc, len(a.Files))
			}
			for i, f := range a.Files {
				if len(f.Data) == 0 || !bytes.Equal(f.Data, b.Files[i].Data) || f.Name != b.Files[i].Name {
					t.Fatalf("%s/%s: %s is empty or not deterministic", name, loc, f.Name)
				}
			}
			readmes[loc] = string(a.Files[0].Data)
			if !strings.HasPrefix(a.Files[0].Name, "README_") {
				t.Fatalf("%s: first file should be the README", name)
			}
		}
		if readmes["es"] == readmes["en"] || readmes["en"] == readmes["pt"] || readmes["es"] == readmes["pt"] {
			t.Errorf("%s: READMEs are not different per locale", name)
		}
		if !strings.Contains(readmes["pt"], "FICTÍCIOS") || !strings.Contains(readmes["en"], "FICTIONAL") || !strings.Contains(readmes["es"], "FICTICIOS") {
			t.Errorf("%s: fiction notice missing", name)
		}
		x, y := indGen(t, name, Params{Seed: 1}), indGen(t, name, Params{Seed: 2})
		same := true
		for i := 1; i < len(x.Files); i++ {
			if !bytes.Equal(x.Files[i].Data, y.Files[i].Data) {
				same = false
			}
		}
		if same {
			t.Errorf("%s: the seed does not change the numbers", name)
		}
	}
}

func TestAccountingFirmIdentities(t *testing.T) {
	for _, loc := range []string{"es", "en", "pt"} {
		s := indGen(t, AccountingFirmSet, Params{Locale: loc})
		ih, invs := indRows(t, s, "invoices.csv")
		lh, lines := indRows(t, s, "invoice_lines.csv")
		sums := map[string]int64{}
		for _, l := range lines {
			q, up, tot := cc(t, l[lh["quantity"]])/100, cc(t, l[lh["unit_price"]]), cc(t, l[lh["line_total"]])
			if q*up != tot {
				t.Fatalf("line %v: %d*%d != %d", l, q, up, tot)
			}
			sums[l[lh["invoice_id"]]] += tot
		}
		for _, v := range invs {
			sub, tax, tot := cc(t, v[ih["subtotal"]]), cc(t, v[ih["tax"]]), cc(t, v[ih["total"]])
			if sums[v[ih["invoice_id"]]] != sub || sub+tax != tot {
				t.Fatalf("invoice %v does not add up", v)
			}
			bp := int64(math.Round(num(t, v[ih["tax_rate_pct"]]) * 100))
			if (sub*bp+5000)/10000 != tax {
				t.Fatalf("invoice %v: tax is not rate*subtotal", v)
			}
		}
		bh, mv := indRows(t, s, "bank_movements.csv")
		bal := cc(t, mv[0][bh["balance"]])
		if fmtC(bal) != s.Values["opening_balance"] {
			t.Fatalf("opening %s vs %s", fmtC(bal), s.Values["opening_balance"])
		}
		for _, m := range mv[1:] {
			bal += cc(t, m[bh["amount"]])
			if bal != cc(t, m[bh["balance"]]) {
				t.Fatalf("running balance breaks at %v", m)
			}
		}
		if fmtC(bal) != s.Values["closing_balance"] {
			t.Fatalf("closing %s vs %s", fmtC(bal), s.Values["closing_balance"])
		}
	}
}

func TestLawFirmIdentities(t *testing.T) {
	s := indGen(t, LawFirmSet, Params{Locale: "pt"})
	mh, matters := indRows(t, s, "matters.csv")
	th, entries := indRows(t, s, "time_entries.csv")
	hours, amt := map[string]float64{}, map[string]int64{}
	for _, e := range entries {
		h, rate, a := num(t, e[th["hours"]]), cc(t, e[th["hourly_rate"]]), cc(t, e[th["amount"]])
		if int64(math.Round(h*float64(rate))) != a {
			t.Fatalf("entry %v: hours*rate != amount", e)
		}
		hours[e[th["matter_id"]]] += h
		amt[e[th["matter_id"]]] += a
	}
	for _, m := range matters {
		id := m[mh["matter_id"]]
		if math.Abs(hours[id]-num(t, m[mh["total_hours"]])) > 1e-9 || amt[id] != cc(t, m[mh["total_amount"]]) {
			t.Fatalf("matter %v totals differ from its entries", m)
		}
	}
	_, dl := indRows(t, s, "deadlines.csv")
	if len(dl) == 0 {
		t.Fatal("no deadlines")
	}
}

func TestEcommerceStoreIdentities(t *testing.T) {
	s := indGen(t, EcommerceStoreSet, Params{Locale: "en"})
	lh, lines := indRows(t, s, "order_lines.csv")
	oh, orders := indRows(t, s, "orders.csv")
	sub, sold := map[string]int64{}, map[string]int64{}
	qty := map[string]int64{} // order|sku -> quantity
	for _, l := range lines {
		q, up, tot := int64(num(t, l[lh["quantity"]])), cc(t, l[lh["unit_price"]]), cc(t, l[lh["line_total"]])
		if q*up != tot {
			t.Fatalf("line %v", l)
		}
		sub[l[lh["order_id"]]] += tot
		sold[l[lh["sku"]]] += q
		qty[l[lh["order_id"]]+"|"+l[lh["sku"]]] += q
	}
	for _, o := range orders {
		if sub[o[oh["order_id"]]] != cc(t, o[oh["subtotal"]]) || cc(t, o[oh["subtotal"]])+cc(t, o[oh["shipping"]]) != cc(t, o[oh["total"]]) {
			t.Fatalf("order %v does not add up", o)
		}
	}
	ih, inv := indRows(t, s, "inventory.csv")
	for _, r := range inv {
		rc, sd, oh2 := int64(num(t, r[ih["received"]])), int64(num(t, r[ih["sold"]])), int64(num(t, r[ih["on_hand"]]))
		if sd != sold[r[ih["sku"]]] || oh2 != rc-sd || oh2 < 0 {
			t.Fatalf("inventory %v", r)
		}
	}
	rh, rets := indRows(t, s, "returns.csv")
	for _, r := range rets {
		if qty[r[rh["order_id"]]+"|"+r[rh["sku"]]] < int64(num(t, r[rh["quantity"]])) {
			t.Fatalf("return %v exceeds what was bought", r)
		}
	}
}

func TestMarketingAgencyIdentities(t *testing.T) {
	s := indGen(t, MarketingAgencySet, Params{Locale: "es"})
	sh, spend := indRows(t, s, "channel_spend.csv")
	byCamp := map[string]int64{}
	byClientMonth := map[string]int64{}
	for _, r := range spend {
		clk, sp, cpc := cc(t, r[sh["clicks"]])/100, cc(t, r[sh["spend"]]), cc(t, r[sh["cpc"]])
		if (sp+clk/2)/clk != cpc {
			t.Fatalf("cpc of %v", r)
		}
		byCamp[r[sh["campaign_id"]]] += sp
	}
	ch, camps := indRows(t, s, "campaigns.csv")
	for _, c := range camps {
		if byCamp[c[ch["campaign_id"]]] != cc(t, c[ch["total_spend"]]) {
			t.Fatalf("campaign %v total differs from its channels", c)
		}
	}
	_ = byClientMonth
	rh, reps := indRows(t, s, "client_reports.csv")
	var repTotal, allTotal int64
	for _, r := range reps {
		repTotal += cc(t, r[rh["spend"]])
	}
	for _, v := range byCamp {
		allTotal += v
	}
	if repTotal != allTotal {
		t.Fatalf("client reports %d vs channel spend %d", repTotal, allTotal)
	}
}

func TestMultinationalGroupBalancesAndReconciles(t *testing.T) {
	s := indGen(t, MultinationalGroupS, Params{Locale: "pt"})
	sh, subs := indRows(t, s, "subsidiaries.csv")
	if len(subs) != 3 {
		t.Fatalf("%d subsidiaries", len(subs))
	}
	rate := map[string]float64{}
	for _, r := range subs {
		rate[r[sh["subsidiary_id"]]] = num(t, r[sh["rate_per_usd"]])
		th, tb := indRows(t, s, "trial_balance_"+strings.ToLower(r[sh["subsidiary_id"]])+".csv")
		var d, c int64
		for _, x := range tb {
			d += cc(t, x[th["debit"]])
			c += cc(t, x[th["credit"]])
		}
		if d != c || d == 0 {
			t.Fatalf("%s: debits %d != credits %d", r[0], d, c)
		}
	}
	ih, ic := indRows(t, s, "intercompany.csv")
	if len(ic) != 3 {
		t.Fatalf("%d intercompany rows", len(ic))
	}
	for _, r := range ic {
		recv := num(t, r[ih["receivable_local"]]) / rate[r[ih["seller_id"]]]
		pay := num(t, r[ih["payable_local"]]) / rate[r[ih["buyer_id"]]]
		if math.Abs(recv-pay) > 0.02 || math.Abs(recv-num(t, r[ih["usd_amount"]])) > 0.02 {
			t.Fatalf("intercompany %v does not reconcile in USD (%.4f vs %.4f)", r, recv, pay)
		}
	}
}
