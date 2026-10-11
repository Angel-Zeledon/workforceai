package projects

// A tiny builder for the giant data templates (G2). The structure and the
// texts of a template are written once, side by side (es / en); the builder
// registers the i18n keys in the backend maps and the same texts are exported
// to the frontend catalogs (giant_i18n.json, see templates_giant_test.go and
// frontend/scripts/merge-giant-i18n.mjs), so the two never drift.

type giantB struct {
	prefix string
	t      Template
	es, en map[string]string
}

func newGiant(prefix, id, key, nameEs, nameEn, descEs, descEn string) *giantB {
	g := &giantB{prefix: prefix, es: map[string]string{}, en: map[string]string{}}
	g.t = Template{ID: id, Key: key, Version: 1, Builtin: true, NameKey: g.k("name"), DescriptionKey: g.k("desc"), Params: []TemplateParam{}}
	g.text("name", nameEs, nameEn)
	g.text("desc", descEs, descEn)
	return g
}

func (g *giantB) k(name string) string { return g.prefix + "." + name }

func (g *giantB) text(name, es, en string) string {
	g.es[g.k(name)], g.en[g.k(name)] = es, en
	return g.k(name)
}

// list declares a list parameter (its items repeat the blocks that name it).
func (g *giantB) list(key, item string, max int, labelEs, labelEn, defEs, defEn string) *giantB {
	g.text("param."+key, labelEs, labelEn)
	g.text("default."+key, defEs, defEn)
	g.t.Params = append(g.t.Params, TemplateParam{Key: key, LabelKey: g.k("param." + key), Type: ParamList, ItemName: item, Max: max, DefaultKey: g.k("default." + key)})
	return g
}

// action registers the label of a human approval ("{node}" is the gate title).
func (g *giantB) action(name, es, en string) {
	g.es["pv.action."+name], g.en["pv.action."+name] = es, en
}

type giantObj struct {
	g *giantB
	i int
}

type giantWf struct {
	g    *giantB
	o, w int
}

func (g *giantB) obj(key, es, en string) giantObj {
	g.text(key, es, en)
	g.t.Objectives = append(g.t.Objectives, TemplateObjective{Key: key, TitleKey: g.k(key)})
	return giantObj{g, len(g.t.Objectives) - 1}
}

func (o giantObj) wf(key, es, en string, repeat ...string) giantWf {
	o.g.text(key, es, en)
	ob := &o.g.t.Objectives[o.i]
	ob.Workflows = append(ob.Workflows, TemplateWorkflow{Key: key, TitleKey: o.g.k(key), Repeat: repeat})
	return giantWf{o.g, o.i, len(ob.Workflows) - 1}
}

func (w giantWf) add(n TemplateNode) giantWf {
	wf := &w.g.t.Objectives[w.o].Workflows[w.w]
	wf.Nodes = append(wf.Nodes, n)
	return w
}

// task adds an agent task. deps may use "key^list" (the same block, previous item of that list).
func (w giantWf) task(key, agent, cx, es, en string, deps ...string) giantWf {
	return w.add(TemplateNode{Key: key, TitleKey: w.g.text(key, es, en), Agent: agent, Complexity: cx, Deps: deps})
}

// gate adds a human approval gate.
func (w giantWf) gate(key, action, risk, es, en string, deps ...string) giantWf {
	return w.add(TemplateNode{Key: key, Kind: KindGate, TitleKey: w.g.text(key, es, en), Deps: deps, Secs: 8,
		Approval: &TemplateApproval{Action: action, Risk: risk}})
}

func (g *giantB) build() Template { return g.t }

// giantSources builds every giant template with its texts.
func giantSources() []*giantB {
	return []*giantB{annualBudgetSrc(), erpMigrationSrc(), complianceSrc(), productLaunchSrc(), postMergerSrc(), yearEndAuditSrc()}
}

func giantTemplates() []Template {
	var out []Template
	for _, g := range giantSources() {
		out = append(out, g.build())
	}
	return out
}

// giantI18N returns the texts of all the giant templates by locale.
func giantI18N() map[string]map[string]string {
	es, en := map[string]string{}, map[string]string{}
	for _, g := range giantSources() {
		for k, v := range g.es {
			es[k] = v
		}
		for k, v := range g.en {
			en[k] = v
		}
	}
	return map[string]map[string]string{"es": es, "en": en}
}

func init() {
	for loc, m := range giantI18N() {
		for k, v := range m {
			i18n[loc][k] = v
		}
	}
	for loc, m := range map[string]map[string]string{"es": {"tpl.copySuffix": "(copia)"}, "en": {"tpl.copySuffix": "(copy)"}} {
		for k, v := range m {
			i18n[loc][k] = v
		}
	}
}
