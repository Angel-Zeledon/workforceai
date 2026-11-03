package governance

import "testing"

// E0: by=person exists, and a person who may not see everybody's costs keeps only their own row.
func TestUsageByPersonOnlyMine(t *testing.T) {
	if !ValidDimension(ByPerson) {
		t.Fatal("person must be a dimension")
	}
	rep := UsageReport{By: ByPerson, Rows: []UsageRow{
		{Key: "ana", Label: "Ana", Calls: 3, CostUSD: 0.30},
		{Key: "luis", Label: "Luis", Calls: 1, CostUSD: 0.10},
		{Key: "", Label: "", Calls: 5, CostUSD: 1},
	}}
	rep.Finish()
	rep.OnlyPerson("luis")
	if len(rep.Rows) != 1 || rep.Rows[0].Key != "luis" || rep.Total.CostUSD != 0.10 || rep.Total.Calls != 1 {
		t.Fatalf("only luis = %+v total %+v", rep.Rows, rep.Total)
	}
	rep.OnlyPerson("") // no identity: nothing, never the unattributed row
	if len(rep.Rows) != 0 || rep.Total.CostUSD != 0 {
		t.Fatalf("nobody = %+v", rep.Rows)
	}
}
