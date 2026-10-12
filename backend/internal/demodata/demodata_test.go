package demodata

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
)

// The tests re-read the generated files: nothing is checked against the
// generator's internal numbers.

func parseCSV(t *testing.T, data []byte) [][]string {
	t.Helper()
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatalf("invalid CSV: %v", err)
	}
	return rows
}

func toC(t *testing.T, s string) cents {
	t.Helper()
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("not a number %q", s)
	}
	return cents(math.Round(f * 100))
}

func col(t *testing.T, header []string, name string) int {
	t.Helper()
	for i, h := range header {
		if h == name {
			return i
		}
	}
	t.Fatalf("column %q not in %v", name, header)
	return -1
}

func filesOf(s Set, input string) []File {
	var out []File
	for _, f := range s.Files {
		if f.Input == input {
			out = append(out, f)
		}
	}
	return out
}

func gen(t *testing.T, name string, p Params) Set {
	t.Helper()
	s, err := Generate(name, p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEverySetIsDeterministicAndNotEmpty(t *testing.T) {
	for _, name := range Names() {
		for _, loc := range []string{"es", "en"} {
			a := gen(t, name, Params{Locale: loc})
			b := gen(t, name, Params{Locale: loc})
			if len(a.Files) == 0 {
				t.Fatalf("%s: no files", name)
			}
			if len(a.Files) != len(b.Files) {
				t.Fatalf("%s: not deterministic", name)
			}
			seen := map[string]bool{}
			for i := range a.Files {
				if !bytes.Equal(a.Files[i].Data, b.Files[i].Data) || a.Files[i].Name != b.Files[i].Name {
					t.Fatalf("%s/%s: not deterministic", name, a.Files[i].Name)
				}
				if seen[a.Files[i].Name] {
					t.Fatalf("%s: duplicate file name %s", name, a.Files[i].Name)
				}
				seen[a.Files[i].Name] = true
				if strings.HasSuffix(a.Files[i].Name, ".csv") {
					rows := parseCSV(t, a.Files[i].Data)
					if len(rows) < 2 {
						t.Fatalf("%s/%s: no data rows", name, a.Files[i].Name)
					}
					for _, r := range rows {
						if len(r) != len(rows[0]) {
							t.Fatalf("%s/%s: ragged row", name, a.Files[i].Name)
						}
					}
				}
			}
		}
	}
	// a different seed gives different numbers
	if bytes.Equal(gen(t, GroupSet, Params{}).Files[0].Data, gen(t, GroupSet, Params{Seed: 7}).Files[0].Data) {
		t.Fatal("the seed has no effect")
	}
	if _, err := Generate("nope", Params{}); err == nil {
		t.Fatal("unknown set must fail")
	}
}

func TestGroupCloseTrialBalancesBalance(t *testing.T) {
	for _, n := range []int{1, 3, 8, 12, 20} {
		s := gen(t, GroupSet, Params{Count: map[string]int{"subsidiaries": n}})
		tbs := filesOf(s, "tb")
		if len(tbs) != n {
			t.Fatalf("n=%d: %d trial balances", n, len(tbs))
		}
		for _, f := range tbs {
			rows := parseCSV(t, f.Data)
			di, ci := col(t, rows[0], "debit"), col(t, rows[0], "credit")
			var dr, cr cents
			for _, r := range rows[1:] {
				if r[di] != "" && r[ci] != "" {
					t.Fatalf("%s: a line with debit and credit", f.Name)
				}
				dr += toC(t, r[di])
				cr += toC(t, r[ci])
			}
			if dr != cr || dr == 0 {
				t.Fatalf("%s: debits %d != credits %d", f.Name, dr, cr)
			}
		}
	}
}

type tbTotals map[string]cents // account -> debit - credit, by sub

func readTBs(t *testing.T, s Set) (map[string]map[int]cents, map[string]string) {
	t.Helper()
	out := map[string]map[int]cents{}
	ccy := map[string]string{}
	for _, f := range filesOf(s, "tb") {
		rows := parseCSV(t, f.Data)
		h := rows[0]
		si, ci, ai, di, cri := col(t, h, "sub_code"), col(t, h, "currency"), col(t, h, "account"), col(t, h, "debit"), col(t, h, "credit")
		for _, r := range rows[1:] {
			if out[r[si]] == nil {
				out[r[si]] = map[int]cents{}
			}
			ccy[r[si]] = r[ci]
			code, err := strconv.Atoi(r[ai][strings.Index(r[ai], "-")+1:])
			if err != nil {
				t.Fatalf("account %q", r[ai])
			}
			out[r[si]][code] += toC(t, r[di]) - toC(t, r[cri])
		}
	}
	return out, ccy
}

func readFX(t *testing.T, s Set) (closing, average map[string]float64) {
	t.Helper()
	rows := parseCSV(t, filesOf(s, "fx_rates")[0].Data)
	ci, ai, ki := col(t, rows[0], "currency"), col(t, rows[0], "average_rate"), col(t, rows[0], "closing_rate")
	closing, average = map[string]float64{}, map[string]float64{}
	for _, r := range rows[1:] {
		closing[r[ci]], _ = strconv.ParseFloat(r[ki], 64)
		average[r[ci]], _ = strconv.ParseFloat(r[ai], 64)
	}
	return
}

func TestGroupCloseIntercompanyMirrorsAndTiesToTheLedgers(t *testing.T) {
	for _, n := range []int{2, 3, 8, 12} {
		s := gen(t, GroupSet, Params{Count: map[string]int{"subsidiaries": n}})
		tbs, ccy := readTBs(t, s)
		closing, average := readFX(t, s)
		rows := parseCSV(t, filesOf(s, "intercompany")[0].Data)
		h := rows[0]
		ty, cs, ds, usd, cc, cl, dc, dl := col(t, h, "type"), col(t, h, "creditor_sub"), col(t, h, "debtor_sub"), col(t, h, "usd_amount"),
			col(t, h, "creditor_ccy"), col(t, h, "creditor_local_amount"), col(t, h, "debtor_ccy"), col(t, h, "debtor_local_amount")
		recv, pay, rev, cost := map[string]cents{}, map[string]cents{}, map[string]cents{}, map[string]cents{}
		balRows := 0
		for _, r := range rows[1:] {
			rates := closing
			if r[ty] == "sales" {
				rates = average
			} else {
				balRows++
			}
			if r[cs] == r[ds] {
				t.Fatalf("an intercompany row with the same subsidiary on both sides")
			}
			// the same USD amount seen by the creditor and by the debtor (within a rounding cent)
			u := float64(toC(t, r[usd])) / 100
			ic := float64(toC(t, r[cl])) / 100 / rates[r[cc]]
			id := float64(toC(t, r[dl])) / 100 / rates[r[dc]]
			if math.Abs(ic-u) > 0.01 || math.Abs(id-u) > 0.01 || math.Abs(ic-id) > 0.02 {
				t.Fatalf("row does not mirror: usd %.4f creditor %.4f debtor %.4f", u, ic, id)
			}
			if ccy[r[cs]] != r[cc] || ccy[r[ds]] != r[dc] {
				t.Fatalf("currency of the row does not match the trial balance")
			}
			if r[ty] == "balance" {
				recv[r[cs]] += toC(t, r[cl])
				pay[r[ds]] += toC(t, r[dl])
			} else {
				rev[r[cs]] += toC(t, r[cl])
				cost[r[ds]] += toC(t, r[dl])
			}
		}
		if balRows == 0 {
			t.Fatalf("no intercompany balances")
		}
		// what the matrix says is what the ledgers booked
		for sub, tb := range tbs {
			if tb[1150] != recv[sub] || -tb[2050] != pay[sub] || -tb[4100] != rev[sub] || tb[5050] != cost[sub] {
				t.Fatalf("%s: ledger and matrix differ: 1150 %d/%d 2050 %d/%d 4100 %d/%d 5050 %d/%d", sub, tb[1150], recv[sub], -tb[2050], pay[sub], -tb[4100], rev[sub], tb[5050], cost[sub])
			}
		}
		// group totals: receivables and payables eliminate against each other in USD
		var rUSD, pUSD float64
		for sub := range tbs {
			rUSD += float64(recv[sub]) / 100 / closing[ccy[sub]]
			pUSD += float64(pay[sub]) / 100 / closing[ccy[sub]]
		}
		if math.Abs(rUSD-pUSD) > 0.01*float64(len(rows)) {
			t.Fatalf("intercompany does not eliminate: receivable %.2f payable %.2f USD", rUSD, pUSD)
		}
	}
}

func TestGroupCloseFXMappingAndMateriality(t *testing.T) {
	s := gen(t, GroupSet, Params{Count: map[string]int{"subsidiaries": 12}})
	tbs, ccy := readTBs(t, s)
	closing, average := readFX(t, s)
	if len(tbs) != 12 {
		t.Fatalf("%d subsidiaries", len(tbs))
	}
	if closing["USD"] != 1 || average["USD"] != 1 {
		t.Fatal("USD must be 1")
	}
	var extUSD float64
	for sub, c := range ccy {
		if closing[c] <= 0 || average[c] <= 0 {
			t.Fatalf("no rate for %s", c)
		}
		if c != "USD" && math.Abs(average[c]/closing[c]-1) > 0.03 {
			t.Fatalf("%s average rate too far from closing", c)
		}
		extUSD += float64(-tbs[sub][4000]) / 100 / average[c]
	}
	// every account of every trial balance maps to a group account
	mp := parseCSV(t, filesOf(s, "coa_mapping")[0].Data)
	mapped := map[string]bool{}
	for _, r := range mp[1:] {
		mapped[r[0]+"|"+r[1]] = true
	}
	for _, f := range filesOf(s, "tb") {
		rows := parseCSV(t, f.Data)
		for _, r := range rows[1:] {
			if !mapped[r[0]+"|"+r[3]] {
				t.Fatalf("account %s of %s is not in the mapping", r[3], r[0])
			}
		}
	}
	// the materiality policy quotes the real external revenue of the group
	pol := string(filesOf(s, "materiality")[0].Data)
	r0 := int64(math.Round(extUSD))
	if !strings.Contains(pol, "USD "+commas(r0)) && !strings.Contains(pol, "USD "+commas(r0+1)) && !strings.Contains(pol, "USD "+commas(r0-1)) {
		t.Fatalf("the policy does not quote the group revenue (%.0f)", extUSD)
	}
	if !strings.Contains(pol, "Materialidad del grupo") {
		t.Fatal("es policy expected by default")
	}
}

func TestEnglishGroupClose(t *testing.T) {
	s := gen(t, GroupSet, Params{Locale: "en", Count: map[string]int{"subsidiaries": 3}})
	if !strings.Contains(string(filesOf(s, "materiality")[0].Data), "Group materiality") {
		t.Fatal("en policy expected")
	}
	if !strings.Contains(string(filesOf(s, "tb")[0].Data), "External revenue") {
		t.Fatal("en account names expected")
	}
}

func TestSubsidiariesUseTheCountriesOfTheTemplates(t *testing.T) {
	s := gen(t, GroupSet, Params{Count: map[string]int{"subsidiaries": 5}})
	var names []string
	for _, f := range filesOf(s, "tb") {
		names = append(names, f.Name)
	}
	want := []string{"tb_S01_MX.csv", "tb_S02_CO.csv", "tb_S03_CL.csv", "tb_S04_PE.csv", "tb_S05_ES.csv"}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("%v", names)
	}
}

func TestDueDiligenceFinancialsAreInternallyConsistent(t *testing.T) {
	s := gen(t, DueDiligenceSet, Params{Count: map[string]int{"entities": 4}})
	fin := filesOf(s, "financials")
	if len(fin) != 4 {
		t.Fatalf("%d financials", len(fin))
	}
	for _, f := range fin {
		rows := parseCSV(t, f.Data)
		m := map[string][3]cents{}
		for _, r := range rows[1:] {
			m[r[1]] = [3]cents{toC(t, r[2]), toC(t, r[3]), toC(t, r[4])}
		}
		for y := 0; y < 3; y++ {
			g := func(k string) cents { return m[k][y] }
			checks := map[string][2]cents{
				"gross_profit":   {g("gross_profit"), g("revenue") - g("cost_of_sales")},
				"ebitda":         {g("ebitda"), g("gross_profit") - g("operating_expenses")},
				"ebit":           {g("ebit"), g("ebitda") - g("depreciation_amortization")},
				"pre_tax_income": {g("pre_tax_income"), g("ebit") - g("interest_expense")},
				"net_income":     {g("net_income"), g("pre_tax_income") - g("income_tax")},
				"equity":         {g("total_equity"), g("total_assets") - g("total_liabilities")},
			}
			for k, v := range checks {
				if v[0] != v[1] {
					t.Fatalf("%s %s year %d: %d != %d", f.Name, k, y, v[0], v[1])
				}
			}
		}
	}
	// litigation provision never exceeds the claim
	rows := parseCSV(t, filesOf(s, "litigation")[0].Data)
	ci, pi := col(t, rows[0], "claim_usd"), col(t, rows[0], "provision_usd")
	for _, r := range rows[1:] {
		if toC(t, r[pi]) > toC(t, r[ci]) {
			t.Fatal("provision above the claim")
		}
	}
}

func TestProposalAddsUpToFiftyThousand(t *testing.T) {
	s := gen(t, ProposalSet, Params{})
	rows := parseCSV(t, filesOf(s, "price_list")[0].Data)
	hi, ri, ai := col(t, rows[0], "hours"), col(t, rows[0], "rate_usd"), col(t, rows[0], "amount_usd")
	var total cents
	for _, r := range rows[1:] {
		h, _ := strconv.Atoi(r[hi])
		if r[hi] != "1" || !strings.Contains(r[0], "Ajuste") {
			if toC(t, r[ri])*cents(h) != toC(t, r[ai]) {
				t.Fatalf("line %v: hours * rate != amount", r)
			}
		}
		total += toC(t, r[ai])
	}
	if total != 50_000_00 {
		t.Fatalf("total %d", total)
	}
	if len(filesOf(s, "client_brief")) != 1 || len(filesOf(s, "cost_rates")) != 1 {
		t.Fatal("missing companion files")
	}
}

func TestBudgetTotalsAreTheSumOfTheRows(t *testing.T) {
	s := gen(t, BudgetSet, Params{Lists: map[string][]string{"units": {"A", "B"}, "countries": {"X", "Y", "Z"}}})
	rows := parseCSV(t, filesOf(s, "prior_actuals")[0].Data)
	h := rows[0]
	if len(rows) != 1+6+1 {
		t.Fatalf("%d rows", len(rows))
	}
	for _, name := range []string{"revenue_usd", "cogs_usd", "gross_profit_usd", "opex_usd", "ebitda_usd", "headcount"} {
		i := col(t, h, name)
		var sum cents
		for _, r := range rows[1 : len(rows)-1] {
			if name == "headcount" {
				n, _ := strconv.Atoi(r[i])
				sum += cents(n)
			} else {
				sum += toC(t, r[i])
			}
		}
		tot := rows[len(rows)-1][i]
		got := toC(t, tot)
		if name == "headcount" {
			n, _ := strconv.Atoi(tot)
			got = cents(n)
		}
		if sum != got {
			t.Fatalf("%s: total %d != sum %d", name, got, sum)
		}
	}
	for _, r := range rows[1 : len(rows)-1] {
		if toC(t, r[col(t, h, "revenue_usd")])-toC(t, r[col(t, h, "cogs_usd")]) != toC(t, r[col(t, h, "gross_profit_usd")]) {
			t.Fatal("gross profit")
		}
	}
	// headcount by function adds up to the headcount per unit and country
	hr := parseCSV(t, filesOf(s, "headcount")[0].Data)
	var hs int
	for _, r := range hr[1:] {
		n, _ := strconv.Atoi(r[3])
		hs += n
	}
	tot, _ := strconv.Atoi(rows[len(rows)-1][col(t, h, "headcount")])
	if hs != tot {
		t.Fatalf("headcount %d != %d", hs, tot)
	}
}

func TestBankStatementsRunFromOpeningToClosing(t *testing.T) {
	s := gen(t, CashCloseSet, Params{Count: map[string]int{"accounts": 4, "clients": 2}})
	st := filesOf(s, "bank_statements")
	if len(st) != 8 {
		t.Fatalf("%d statements", len(st))
	}
	for _, f := range st {
		rows := parseCSV(t, f.Data)
		bal := toC(t, rows[1][4])
		for _, r := range rows[2:] {
			bal = bal - toC(t, r[2]) + toC(t, r[3])
			if bal != toC(t, r[4]) {
				t.Fatalf("%s: running balance broken at %v", f.Name, r)
			}
		}
	}
}

func TestAuditTrialBalancesBalanceAndValuesAreSet(t *testing.T) {
	s := gen(t, AuditSet, Params{Lists: map[string][]string{"entities": {"Matriz", "Filial 1", "Filial 2"}}})
	tbs := filesOf(s, "trial_balances")
	if len(tbs) != 3 {
		t.Fatalf("%d", len(tbs))
	}
	for _, f := range tbs {
		rows := parseCSV(t, f.Data)
		di, ci := col(t, rows[0], "debit"), col(t, rows[0], "credit")
		var dr, cr cents
		for _, r := range rows[1:] {
			dr += toC(t, r[di])
			cr += toC(t, r[ci])
		}
		if dr != cr {
			t.Fatalf("%s: %d != %d", f.Name, dr, cr)
		}
	}
	if s.Values["materiality_pct"] == "" {
		t.Fatal("no value for the number input")
	}
	fc := gen(t, "financial_close", Params{})
	if len(filesOf(fc, "trial_balance")) != 1 {
		t.Fatal("financial close needs one trial balance")
	}
}

func TestEverySetHasTheFilesForItsInputs(t *testing.T) {
	want := map[string][]string{
		GroupSet:          {"tb", "intercompany", "fx_rates", "coa_mapping", "materiality"},
		DueDiligenceSet:   {"financials", "contracts", "people", "litigation", "deal_brief"},
		ProposalSet:       {"client_brief", "price_list", "cost_rates", "past_proposals"},
		BudgetSet:         {"prior_actuals", "assumptions", "headcount"},
		ERPSet:            {"systems", "data_volumes"},
		ComplianceSet:     {"process_inventory", "controls_catalog", "regulation_texts"},
		AuditSet:          {"trial_balances", "prior_workpapers"},
		LaunchSet:         {"market_sizing", "channel_costs", "product_brief"},
		PostMergerSet:     {"org_charts", "systems_overlap"},
		MarketSet:         {"market_data", "competitors", "regulatory_notes"},
		CashCloseSet:      {"bank_statements", "ar_ledger", "payables_due"},
		"financial_close": {"trial_balance"},
	}
	if len(want) != len(Names()) {
		t.Fatalf("sets %v vs expectations %d", Names(), len(want))
	}
	for name, keys := range want {
		s := gen(t, name, Params{})
		for _, k := range keys {
			if len(filesOf(s, k)) == 0 {
				t.Errorf("%s: no file for input %q", name, k)
			}
		}
	}
}
