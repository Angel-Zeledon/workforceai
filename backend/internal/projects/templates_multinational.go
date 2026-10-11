package projects

import (
	"strconv"
)

// Large projects a multinational runs, built only with the roles of the demo
// office (accounting, analyst, legal, sales, hr, operations, assistant) so they
// launch without hiring anyone. Each repeats a parallel block per subsidiary,
// country or target entity and then joins everything in group-level work and
// one human gate; titles carry the country/entity as a node parameter.
//
//   - Group consolidated close: 4 tasks per subsidiary (local close, intercompany,
//     FX translation, tax provision) + 6 group tasks + CFO gate.
//     Subsidiaries 1-20 (default 8): 8 -> 38 tasks, 20 -> 86.
//   - New market expansion: 7 tasks per country + 3 group tasks + board gate.
//     Countries 1-12 (default 5): 5 -> 38 tasks, 12 -> 87.
//   - Acquisition due diligence: 9 tasks per entity + 4 group tasks + IC gate.
//     Entities 1-10 (default 3): 3 -> 31 tasks, 10 -> 94.

const (
	idGroupClose  = "tpl-group-consolidated-close"
	keyGroupClose = "group_consolidated_close"
	idMarketEntry = "tpl-market-expansion"
	keyMarketEnt  = "market_expansion"
	idDueDilig    = "tpl-acquisition-due-diligence"
	keyDueDilig   = "acquisition_due_diligence"
)

var countries = [][2]string{
	{"México", "Mexico"}, {"Colombia", "Colombia"}, {"Chile", "Chile"}, {"Perú", "Peru"}, {"España", "Spain"},
	{"Brasil", "Brazil"}, {"Argentina", "Argentina"}, {"Costa Rica", "Costa Rica"}, {"Panamá", "Panama"},
	{"Guatemala", "Guatemala"}, {"Ecuador", "Ecuador"}, {"Uruguay", "Uruguay"}, {"Estados Unidos", "United States"},
	{"Canadá", "Canada"}, {"Alemania", "Germany"}, {"Francia", "France"}, {"Reino Unido", "United Kingdom"},
	{"Italia", "Italy"}, {"Portugal", "Portugal"}, {"República Dominicana", "Dominican Republic"},
}

func countryName(i int, locale string) string {
	c := countries[i%len(countries)]
	if locale == "en" {
		return c[1]
	}
	return c[0]
}

func init() {
	for loc, m := range multinationalI18n {
		for k, v := range m {
			i18n[loc][k] = v
		}
	}
}

// multinationalTemplates are the static defaults listed in the catalog.
func multinationalTemplates() []Template {
	return []Template{buildGroupClose(8, "es"), buildMarketEntry(5, "es"), buildDueDiligence(3, "es")}
}

// expandMultinational applies the user parameters; ok=false for other templates.
func expandMultinational(t Template, params map[string]string, locale string) (Template, bool) {
	switch t.Key {
	case keyGroupClose:
		return buildGroupClose(clampParam(params["subsidiaries"], 8, 1, 20), locale), true
	case keyMarketEnt:
		return buildMarketEntry(clampParam(params["countries"], 5, 1, 12), locale), true
	case keyDueDilig:
		return buildDueDiligence(clampParam(params["entities"], 3, 1, 10), locale), true
	}
	return t, false
}

func buildGroupClose(subs int, locale string) Template {
	t := Template{ID: idGroupClose, Key: keyGroupClose, Version: 1, Builtin: true, NameKey: "proj.gc.name", DescriptionKey: "proj.gc.desc",
		Params: []TemplateParam{{Key: "subsidiaries", LabelKey: "proj.gc.param.subsidiaries", Default: "8", Type: "number", Min: 1, Max: 20}}}
	var wfs []TemplateWorkflow
	var ics, fxs, taxes []string
	for i := 0; i < subs; i++ {
		p := map[string]string{"country": countryName(i, locale)}
		s := "s" + strconv.Itoa(i+1) + "_"
		wfs = append(wfs, TemplateWorkflow{Key: s + "w", TitleKey: "proj.gc.w_sub", Params: p, Nodes: []TemplateNode{
			{Key: s + "close", TitleKey: "proj.gc.close", Params: p, Agent: "accounting", Complexity: "L"},
			{Key: s + "ic", TitleKey: "proj.gc.ic", Params: p, Agent: "accounting", Complexity: "M", Deps: []string{s + "close"}},
			{Key: s + "fx", TitleKey: "proj.gc.fx", Params: p, Agent: "analyst", Complexity: "S", Deps: []string{s + "close"}},
			{Key: s + "tax", TitleKey: "proj.gc.tax", Params: p, Agent: "accounting", Complexity: "M", Deps: []string{s + "close"}},
		}})
		ics, fxs, taxes = append(ics, s+"ic"), append(fxs, s+"fx"), append(taxes, s+"tax")
	}
	consDeps := append(append([]string{"elim"}, fxs...), taxes...)
	t.Objectives = []TemplateObjective{
		{Key: "o1", TitleKey: "proj.gc.o1", Workflows: wfs},
		{Key: "o2", TitleKey: "proj.gc.o2", Workflows: []TemplateWorkflow{
			{Key: "wg", TitleKey: "proj.gc.w_group", Nodes: []TemplateNode{
				{Key: "elim", TitleKey: "proj.gc.elim", Agent: "accounting", Complexity: "L", Deps: ics},
				{Key: "cons", TitleKey: "proj.gc.cons", Agent: "accounting", Complexity: "XL", Deps: consDeps},
				{Key: "var", TitleKey: "proj.gc.var", Agent: "analyst", Complexity: "L", Deps: []string{"cons"}},
				{Key: "disc", TitleKey: "proj.gc.disc", Agent: "legal", Complexity: "M", Deps: []string{"cons"}},
			}},
			{Key: "wr", TitleKey: "proj.gc.w_report", Nodes: []TemplateNode{
				{Key: "pack", TitleKey: "proj.gc.pack", Agent: "assistant", Complexity: "L", Deps: []string{"var", "disc"}},
				{Key: "gate", Kind: KindGate, TitleKey: "proj.gc.gate", Deps: []string{"pack"}, Secs: 8,
					Approval: &TemplateApproval{Action: "approve_group_close", Risk: "high"}},
				{Key: "board", TitleKey: "proj.gc.board", Agent: "assistant", Complexity: "M", Deps: []string{"gate"}},
			}},
		}},
	}
	return t
}

func buildMarketEntry(n int, locale string) Template {
	t := Template{ID: idMarketEntry, Key: keyMarketEnt, Version: 1, Builtin: true, NameKey: "proj.me.name", DescriptionKey: "proj.me.desc",
		Params: []TemplateParam{{Key: "countries", LabelKey: "proj.me.param.countries", Default: "5", Type: "number", Min: 1, Max: 12}}}
	var wfs []TemplateWorkflow
	var briefs, sizes, comps []string
	for i := 0; i < n; i++ {
		p := map[string]string{"country": countryName(i, locale)}
		c := "c" + strconv.Itoa(i+1) + "_"
		wfs = append(wfs, TemplateWorkflow{Key: c + "w", TitleKey: "proj.me.w_country", Params: p, Nodes: []TemplateNode{
			{Key: c + "size", TitleKey: "proj.me.size", Params: p, Agent: "analyst", Complexity: "M"},
			{Key: c + "comp", TitleKey: "proj.me.comp", Params: p, Agent: "sales", Complexity: "M"},
			{Key: c + "reg", TitleKey: "proj.me.reg", Params: p, Agent: "legal", Complexity: "L"},
			{Key: c + "tax", TitleKey: "proj.me.tax", Params: p, Agent: "accounting", Complexity: "M"},
			{Key: c + "hire", TitleKey: "proj.me.hire", Params: p, Agent: "hr", Complexity: "M"},
			{Key: c + "ops", TitleKey: "proj.me.ops", Params: p, Agent: "operations", Complexity: "M"},
			{Key: c + "brief", TitleKey: "proj.me.brief", Params: p, Agent: "assistant", Complexity: "M",
				Deps: []string{c + "size", c + "comp", c + "reg", c + "tax", c + "hire", c + "ops"}},
		}})
		briefs, sizes, comps = append(briefs, c+"brief"), append(sizes, c+"size"), append(comps, c+"comp")
	}
	t.Objectives = []TemplateObjective{
		{Key: "o1", TitleKey: "proj.me.o1", Workflows: wfs},
		{Key: "o2", TitleKey: "proj.me.o2", Workflows: []TemplateWorkflow{
			{Key: "wd", TitleKey: "proj.me.w_decide", Nodes: []TemplateNode{
				{Key: "price", TitleKey: "proj.me.price", Agent: "sales", Complexity: "L", Deps: append(append([]string{}, sizes...), comps...)},
				{Key: "case", TitleKey: "proj.me.case", Agent: "accounting", Complexity: "L", Deps: append(append([]string{}, briefs...), "price")},
				{Key: "gate", Kind: KindGate, TitleKey: "proj.me.gate", Deps: []string{"case"}, Secs: 8,
					Approval: &TemplateApproval{Action: "approve_market_entry", Risk: "high"}},
			}},
			{Key: "wl", TitleKey: "proj.me.w_launch", Nodes: []TemplateNode{
				{Key: "launch", TitleKey: "proj.me.launch", Agent: "operations", Complexity: "L", Deps: []string{"gate"}},
			}},
		}},
	}
	return t
}

func buildDueDiligence(n int, locale string) Template {
	t := Template{ID: idDueDilig, Key: keyDueDilig, Version: 1, Builtin: true, NameKey: "proj.dd.name", DescriptionKey: "proj.dd.desc",
		Params: []TemplateParam{{Key: "entities", LabelKey: "proj.dd.param.entities", Default: "3", Type: "number", Min: 1, Max: 10}}}
	var wfs []TemplateWorkflow
	var flags []string
	for i := 0; i < n; i++ {
		p := map[string]string{"entity": tr(locale, "proj.dd.entity", map[string]string{"n": strconv.Itoa(i + 1)}) + " (" + countryName(i, locale) + ")"}
		e := "e" + strconv.Itoa(i+1) + "_"
		work := []string{e + "qoe", e + "debt", e + "contracts", e + "lit", e + "people", e + "cust", e + "ops", e + "data"}
		wfs = append(wfs, TemplateWorkflow{Key: e + "w", TitleKey: "proj.dd.w_entity", Params: p, Nodes: []TemplateNode{
			{Key: e + "qoe", TitleKey: "proj.dd.qoe", Params: p, Agent: "accounting", Complexity: "L"},
			{Key: e + "debt", TitleKey: "proj.dd.debt", Params: p, Agent: "accounting", Complexity: "M"},
			{Key: e + "contracts", TitleKey: "proj.dd.contracts", Params: p, Agent: "legal", Complexity: "L"},
			{Key: e + "lit", TitleKey: "proj.dd.lit", Params: p, Agent: "legal", Complexity: "M"},
			{Key: e + "people", TitleKey: "proj.dd.people", Params: p, Agent: "hr", Complexity: "M"},
			{Key: e + "cust", TitleKey: "proj.dd.cust", Params: p, Agent: "sales", Complexity: "M"},
			{Key: e + "ops", TitleKey: "proj.dd.ops", Params: p, Agent: "operations", Complexity: "M"},
			{Key: e + "data", TitleKey: "proj.dd.data", Params: p, Agent: "analyst", Complexity: "M"},
			{Key: e + "flags", TitleKey: "proj.dd.flags", Params: p, Agent: "analyst", Complexity: "M", Deps: work},
		}})
		flags = append(flags, e+"flags")
	}
	t.Objectives = []TemplateObjective{
		{Key: "o1", TitleKey: "proj.dd.o1", Workflows: wfs},
		{Key: "o2", TitleKey: "proj.dd.o2", Workflows: []TemplateWorkflow{
			{Key: "wi", TitleKey: "proj.dd.w_ic", Nodes: []TemplateNode{
				{Key: "val", TitleKey: "proj.dd.val", Agent: "accounting", Complexity: "XL", Deps: flags},
				{Key: "report", TitleKey: "proj.dd.report", Agent: "legal", Complexity: "L", Deps: flags},
				{Key: "integ", TitleKey: "proj.dd.integ", Agent: "operations", Complexity: "L", Deps: flags},
				{Key: "gate", Kind: KindGate, TitleKey: "proj.dd.gate", Deps: []string{"val", "report", "integ"}, Secs: 8,
					Approval: &TemplateApproval{Action: "approve_investment", Risk: "high"}},
				{Key: "memo", TitleKey: "proj.dd.memo", Agent: "assistant", Complexity: "M", Deps: []string{"gate"}},
			}},
		}},
	}
	return t
}
