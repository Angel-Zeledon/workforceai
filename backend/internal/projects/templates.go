package projects

import (
	"fmt"
	"strings"

	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
)

// Built-in project templates (the same three the frontend ships in
// lib/projects/templates.ts, so ids and keys match) plus the workflow templates
// of the catalog exposed as one-objective projects. Every user-visible string is
// an i18n key resolved here for the project locale; the key is kept on the node
// (title_key) so the UI can re-localize it until the user edits the title.

const (
	idFinancialClose = "tpl-financial-close"
	idBranchOpening  = "tpl-branch-opening"
	idGeneric        = "tpl-generic"
	catalogPrefix    = "wf:"
)

func tnode(key, titleKey, agent, cx string, deps ...string) TemplateNode {
	return TemplateNode{Key: key, TitleKey: titleKey, Agent: agent, Complexity: cx, Deps: deps}
}

func builtinTemplates() []Template {
	fc := Template{ID: idFinancialClose, Key: "financial_close", Version: 1, Builtin: true, NameKey: "proj.fc.name", DescriptionKey: "proj.fc.desc",
		Params: []TemplateParam{}, Objectives: []TemplateObjective{
			{Key: "o1", TitleKey: "proj.fc.o1", Workflows: []TemplateWorkflow{
				{Key: "w1", TitleKey: "proj.fc.w1", Nodes: []TemplateNode{
					tnode("n1", "proj.fc.n1", "accounting", "M"),
					tnode("n2", "proj.fc.n2", "accounting", "M", "n1"),
				}},
			}},
			{Key: "o2", TitleKey: "proj.fc.o2", Workflows: []TemplateWorkflow{
				// The balance sheet and the income statement run in parallel; the equity step needs the net
				// income (cross-workflow dependency a2 -> b3).
				{Key: "w2", TitleKey: "proj.fc.w2", Nodes: []TemplateNode{
					tnode("a1", "proj.fc.a1", "accounting", "M", "n1"),
					tnode("a2", "proj.fc.a2", "accounting", "M", "a1", "b3"),
					{Key: "a3", TitleKey: "proj.fc.a3", Agent: "accounting", Complexity: "L", Deps: []string{"a2", "n2"},
						Delegate: &TemplateDelegate{Agent: "operations", TitleKey: "proj.fc.a3s", Complexity: "S"},
						Approval: &TemplateApproval{Action: "publish_statement", Risk: "medium"}},
				}},
				{Key: "w3", TitleKey: "proj.fc.w3", Nodes: []TemplateNode{
					tnode("b1", "proj.fc.b1", "accounting", "M", "n1"),
					tnode("b2", "proj.fc.b2", "accounting", "M", "n1"),
					tnode("b3", "proj.fc.b3", "accounting", "M", "b1", "b2"),
					{Key: "b4", TitleKey: "proj.fc.b4", Agent: "accounting", Complexity: "L", Deps: []string{"b3"},
						Approval: &TemplateApproval{Action: "publish_statement", Risk: "medium"}},
				}},
			}},
			{Key: "o3", TitleKey: "proj.fc.o3", Workflows: []TemplateWorkflow{
				{Key: "w4", TitleKey: "proj.fc.w4", Nodes: []TemplateNode{
					tnode("c1", "proj.fc.c1", "analyst", "M", "a3", "b4"),
					tnode("c2", "proj.fc.c2", "legal", "M", "a3", "b4"),
					{Key: "g1", Kind: KindGate, TitleKey: "proj.fc.g1", Deps: []string{"c1", "c2"}, Secs: 8,
						Approval: &TemplateApproval{Action: "approve_close", Risk: "high"}},
					tnode("c3", "proj.fc.c3", "assistant", "M", "g1"),
				}},
			}},
		}}
	bo := Template{ID: idBranchOpening, Key: "branch_opening", Version: 1, Builtin: true, NameKey: "proj.bo.name", DescriptionKey: "proj.bo.desc",
		Params: []TemplateParam{{Key: "city", LabelKey: "proj.bo.param.city", Default: "Norte"}},
		Objectives: []TemplateObjective{
			{Key: "o1", TitleKey: "proj.bo.o1", Workflows: []TemplateWorkflow{
				{Key: "w1", TitleKey: "proj.bo.w1", Nodes: []TemplateNode{
					tnode("n1", "proj.bo.n1", "legal", "M"),
					{Key: "n2", TitleKey: "proj.bo.n2", Agent: "legal", Complexity: "L", Deps: []string{"n1"},
						Approval: &TemplateApproval{Action: "send_contract", Risk: "high"}},
					{Key: "n3", Kind: KindWait, TitleKey: "proj.bo.n3", Deps: []string{"n2"}, Secs: 6},
				}},
				{Key: "w2", TitleKey: "proj.bo.w2", Nodes: []TemplateNode{
					tnode("p1", "proj.bo.p1", "operations", "M"),
					tnode("p2", "proj.bo.p2", "accounting", "S", "p1"),
				}},
			}},
			{Key: "o2", TitleKey: "proj.bo.o2", Workflows: []TemplateWorkflow{
				{Key: "w3", TitleKey: "proj.bo.w3", Nodes: []TemplateNode{
					tnode("h1", "proj.bo.h1", "hr", "M", "n1"),
					tnode("h2", "proj.bo.h2", "hr", "M", "h1"),
				}},
			}},
			{Key: "o3", TitleKey: "proj.bo.o3", Workflows: []TemplateWorkflow{
				{Key: "w4", TitleKey: "proj.bo.w4", Nodes: []TemplateNode{
					tnode("m1", "proj.bo.m1", "sales", "L", "n3", "p2"),
					{Key: "m2", Kind: KindMilestone, TitleKey: "proj.bo.m2", Deps: []string{"m1", "h2"}},
					tnode("m3", "proj.bo.m3", "assistant", "S", "m2"),
				}},
			}},
		}}
	gen := Template{ID: idGeneric, Key: "generic", Version: 1, Builtin: true, NameKey: "proj.gen.name", DescriptionKey: "proj.gen.desc",
		Params: []TemplateParam{},
		Objectives: []TemplateObjective{
			{Key: "o1", TitleKey: "proj.gen.o1", Workflows: []TemplateWorkflow{
				{Key: "w1", TitleKey: "proj.gen.w1", Nodes: []TemplateNode{
					tnode("r1", "proj.gen.r1", "analyst", "M"),
					tnode("r2", "proj.gen.r2", "operations", "M"),
				}},
			}},
			{Key: "o2", TitleKey: "proj.gen.o2", Workflows: []TemplateWorkflow{
				{Key: "w2", TitleKey: "proj.gen.w2", Nodes: []TemplateNode{
					tnode("e1", "proj.gen.e1", "sales", "L", "r1", "r2"),
					tnode("e2", "proj.gen.e2", "accounting", "M", "r1"),
				}},
			}},
			{Key: "o3", TitleKey: "proj.gen.o3", Workflows: []TemplateWorkflow{
				{Key: "w3", TitleKey: "proj.gen.w3", Nodes: []TemplateNode{
					{Key: "v1", Kind: KindGate, TitleKey: "proj.gen.v1", Deps: []string{"e1", "e2"}, Secs: 8,
						Approval: &TemplateApproval{Action: "approve_plan", Risk: "medium"}},
					tnode("v2", "proj.gen.v2", "assistant", "M", "v1"),
				}},
			}},
		}}
	return []Template{fc, bo, gen}
}

// i18n of the built-in templates: the keys of the frontend catalogs (proj.*, pv.action.*).
var i18n = map[string]map[string]string{
	"es": {
		"proj.fc.name": "Cierre financiero del trimestre", "proj.fc.desc": "El contador arma el balance general y, en paralelo, el estado de resultados; luego se revisa y se entrega.",
		"proj.fc.o1": "Preparar la información contable", "proj.fc.w1": "Datos base", "proj.fc.n1": "Recopilar la balanza de comprobación", "proj.fc.n2": "Conciliar las cuentas bancarias",
		"proj.fc.o2": "Estados financieros", "proj.fc.w2": "Balance general", "proj.fc.a1": "Clasificar activos y pasivos", "proj.fc.a2": "Calcular el patrimonio (usa la utilidad neta)",
		"proj.fc.a3": "Armar el balance general", "proj.fc.a3s": "Confirmar el inventario físico", "proj.fc.w3": "Estado de resultados", "proj.fc.b1": "Consolidar los ingresos",
		"proj.fc.b2": "Consolidar costos y gastos", "proj.fc.b3": "Calcular la utilidad neta", "proj.fc.b4": "Armar el estado de resultados", "proj.fc.o3": "Revisión y entrega",
		"proj.fc.w4": "Cierre", "proj.fc.c1": "Conciliar el balance contra los resultados", "proj.fc.c2": "Revisar el cumplimiento fiscal", "proj.fc.g1": "Aprobación del dueño del cierre",
		"proj.fc.c3":   "Redactar el informe ejecutivo",
		"proj.bo.name": "Apertura de sucursal {city}", "proj.bo.desc": "Permisos, personal y lanzamiento de una nueva sucursal.", "proj.bo.param.city": "Ciudad",
		"proj.bo.o1": "Local y permisos en {city}", "proj.bo.w1": "Trámites legales", "proj.bo.n1": "Listar los permisos requeridos en {city}", "proj.bo.n2": "Enviar el contrato de arrendamiento",
		"proj.bo.n3": "Esperar la resolución de permisos", "proj.bo.w2": "Local y presupuesto", "proj.bo.p1": "Evaluar el local y la logística", "proj.bo.p2": "Presupuestar la adecuación",
		"proj.bo.o2": "Equipo humano", "proj.bo.w3": "Contratación", "proj.bo.h1": "Definir perfiles y vacantes", "proj.bo.h2": "Reclutar y seleccionar", "proj.bo.o3": "Lanzamiento",
		"proj.bo.w4": "Campaña de apertura", "proj.bo.m1": "Diseñar la campaña de apertura", "proj.bo.m2": "Sucursal lista para abrir", "proj.bo.m3": "Consolidar el informe de apertura",
		"proj.gen.name": "Proyecto libre", "proj.gen.desc": "Plan genérico: investigar, ejecutar y revisar.", "proj.gen.o1": "Entender y preparar: {goal}", "proj.gen.w1": "Investigación",
		"proj.gen.r1": "Investigar el contexto y los datos", "proj.gen.r2": "Evaluar la viabilidad operativa", "proj.gen.o2": "Ejecutar", "proj.gen.w2": "Ejecución",
		"proj.gen.e1": "Elaborar la propuesta principal", "proj.gen.e2": "Calcular el impacto financiero", "proj.gen.o3": "Revisar y entregar", "proj.gen.w3": "Entrega",
		"proj.gen.v1": "Aprobar el plan", "proj.gen.v2": "Consolidar el informe final",
		"pv.action.publish_statement": "Publicar: {node}", "pv.action.approve_close": "Aprobar: {node}", "pv.action.approve_plan": "Aprobar: {node}",
		"pv.action.send_contract": "Enviar: {node}", "pv.action.extend_budget": "Ampliar el presupuesto del proyecto (+25 %)", "pv.action.generic": "Aprobar: {node}",
		"pv.approval.details":       "Un humano debe autorizar este paso antes de que continúe el proyecto: {node}",
		"pv.approval.budgetDetails": "El proyecto alcanzó su tope de presupuesto y está en pausa. Aprueba una ampliación del 25 % para continuar o recházala para cancelar el proyecto.",
		"project.delegated":         "Delegado desde: {node}",
	},
	"en": {
		"proj.fc.name": "Quarterly financial close", "proj.fc.desc": "The accountant builds the balance sheet and, in parallel, the income statement; then it is reviewed and delivered.",
		"proj.fc.o1": "Prepare the accounting data", "proj.fc.w1": "Base data", "proj.fc.n1": "Collect the trial balance", "proj.fc.n2": "Reconcile the bank accounts",
		"proj.fc.o2": "Financial statements", "proj.fc.w2": "Balance sheet", "proj.fc.a1": "Classify assets and liabilities", "proj.fc.a2": "Calculate equity (uses net income)",
		"proj.fc.a3": "Build the balance sheet", "proj.fc.a3s": "Confirm the physical inventory", "proj.fc.w3": "Income statement", "proj.fc.b1": "Consolidate revenue",
		"proj.fc.b2": "Consolidate costs and expenses", "proj.fc.b3": "Calculate net income", "proj.fc.b4": "Build the income statement", "proj.fc.o3": "Review and delivery",
		"proj.fc.w4": "Close", "proj.fc.c1": "Reconcile the balance sheet against the results", "proj.fc.c2": "Review tax compliance", "proj.fc.g1": "Close owner approval",
		"proj.fc.c3":   "Write the executive report",
		"proj.bo.name": "{city} branch opening", "proj.bo.desc": "Permits, staffing and launch of a new branch.", "proj.bo.param.city": "City",
		"proj.bo.o1": "Premises and permits in {city}", "proj.bo.w1": "Legal procedures", "proj.bo.n1": "List the required permits in {city}", "proj.bo.n2": "Send the lease contract",
		"proj.bo.n3": "Wait for the permit decision", "proj.bo.w2": "Premises and budget", "proj.bo.p1": "Assess the premises and logistics", "proj.bo.p2": "Budget the fit-out",
		"proj.bo.o2": "People", "proj.bo.w3": "Hiring", "proj.bo.h1": "Define profiles and openings", "proj.bo.h2": "Recruit and select", "proj.bo.o3": "Launch",
		"proj.bo.w4": "Opening campaign", "proj.bo.m1": "Design the opening campaign", "proj.bo.m2": "Branch ready to open", "proj.bo.m3": "Consolidate the opening report",
		"proj.gen.name": "Free project", "proj.gen.desc": "Generic plan: research, execute and review.", "proj.gen.o1": "Understand and prepare: {goal}", "proj.gen.w1": "Research",
		"proj.gen.r1": "Research the context and data", "proj.gen.r2": "Assess operational feasibility", "proj.gen.o2": "Execute", "proj.gen.w2": "Execution",
		"proj.gen.e1": "Draft the main proposal", "proj.gen.e2": "Calculate the financial impact", "proj.gen.o3": "Review and deliver", "proj.gen.w3": "Delivery",
		"proj.gen.v1": "Approve the plan", "proj.gen.v2": "Consolidate the final report",
		"pv.action.publish_statement": "Publish: {node}", "pv.action.approve_close": "Approve: {node}", "pv.action.approve_plan": "Approve: {node}",
		"pv.action.send_contract": "Send: {node}", "pv.action.extend_budget": "Extend the project budget (+25%)", "pv.action.generic": "Approve: {node}",
		"pv.approval.details":       "A person must authorize this step before the project continues: {node}",
		"pv.approval.budgetDetails": "The project reached its budget cap and is paused. Approve a 25% extension to continue, or reject it to cancel the project.",
		"project.delegated":         "Delegated from: {node}",
	},
}

// tr resolves a key for a locale with {param} substitution; unknown keys come back as the key.
func tr(locale, key string, params map[string]string) string {
	loc := catalog.NormalizeLocale(locale)
	s, ok := i18n[loc][key]
	if !ok {
		return key
	}
	for k, v := range params {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

func hasKey(key string) bool { _, ok := i18n["es"][key]; return ok }

func actionTitle(locale, action, node string) string {
	key := "pv.action." + action
	if !hasKey(key) {
		key = "pv.action.generic"
	}
	return tr(locale, key, map[string]string{"node": node})
}

// resolved returns the template with name, description and param labels in the locale.
func (t Template) resolved(locale string) Template {
	out := t
	if t.NameKey != "" {
		out.Name = tr(locale, t.NameKey, nil)
	}
	if t.DescriptionKey != "" {
		out.Description = tr(locale, t.DescriptionKey, nil)
	}
	out.Params = make([]TemplateParam, len(t.Params))
	for i, p := range t.Params {
		p.Label = tr(locale, p.LabelKey, nil)
		out.Params[i] = p
	}
	return out
}

// builtinByID finds a built-in template.
func builtinByID(id string) (Template, bool) {
	for _, t := range builtinTemplates() {
		if t.ID == id || t.Key == id {
			return t, true
		}
	}
	return Template{}, false
}

// catalogTemplate turns a workflow of the catalog (already resolved for a
// locale) into a project template: one objective, one workflow, the steps as nodes.
func catalogTemplate(v catalog.WorkflowView) Template {
	steps := make([]TemplateNode, 0, len(v.Steps))
	for _, s := range v.Steps {
		steps = append(steps, TemplateNode{Key: s.Key, Title: s.Title, Description: s.Description, Agent: s.AgentID, Complexity: "M", Deps: s.DependsOn})
	}
	params := make([]TemplateParam, 0, len(v.Params))
	for _, p := range v.Params {
		params = append(params, TemplateParam{Key: p.Key, LabelKey: p.LabelKey, Label: p.Label, Default: p.Default})
	}
	return Template{ID: catalogPrefix + v.Key, Key: v.Key, Version: v.Version, Name: v.Name, Description: v.Description, Params: params, Builtin: true,
		Objectives: []TemplateObjective{{Key: "o1", Title: v.Name, Workflows: []TemplateWorkflow{{Key: "w1", Title: v.Name, Nodes: steps}}}}}
}

// instance is a template turned into concrete objectives and nodes.
type instance struct {
	Name       string
	NameKey    string
	Goal       string
	Objectives []Objective
	Nodes      []NodeDef
}

func pad(n int) string { return fmt.Sprintf("%04d", n) }

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// instantiate builds the nodes of a draft. params are the user values (missing
// ones take the template default); the goal is available as {goal}.
func (t Template) instantiate(pid, goal string, params map[string]string, locale string) (instance, error) {
	all := map[string]string{"goal": truncRunes(strings.TrimSpace(goal), 60)}
	for _, p := range t.Params {
		v := catalog.SanitizeParam(params[p.Key])
		if v == "" {
			v = p.Default
		}
		all[p.Key] = v
	}
	text := func(key, literal string) (string, string) {
		if literal != "" {
			return literal, ""
		}
		if hasKey(key) {
			return tr(locale, key, all), key
		}
		return key, ""
	}
	inst := instance{Goal: goal}
	name, nameKey := text(t.NameKey, t.Name)
	if t.Key == "generic" && strings.TrimSpace(goal) != "" {
		name, nameKey = truncRunes(strings.TrimSpace(goal), 60), ""
	}
	inst.Name, inst.NameKey = name, nameKey
	if strings.TrimSpace(inst.Goal) == "" {
		inst.Goal, _ = text(t.DescriptionKey, t.Description)
	}
	keyToID := map[string]string{}
	for _, o := range t.Objectives {
		for _, w := range o.Workflows {
			for _, n := range w.Nodes {
				if _, dup := keyToID[n.Key]; dup {
					return inst, fmt.Errorf("%w: duplicate node key %q", domain.ErrInvalid, n.Key)
				}
				keyToID[n.Key] = pid + ":" + n.Key
			}
		}
	}
	for oi, o := range t.Objectives {
		oid := pid + ":" + o.Key
		title, key := text(o.TitleKey, o.Title)
		inst.Objectives = append(inst.Objectives, Objective{ID: oid, Title: title, TitleKey: key, TitleParams: all, Position: oi})
		for wi, w := range o.Workflows {
			gid := pid + ":" + w.Key
			gt, gk := text(w.TitleKey, w.Title)
			inst.Nodes = append(inst.Nodes, NodeDef{ID: gid, Key: w.Key, ObjectiveID: oid, Kind: KindGroup, Title: gt, TitleKey: gk, TitleParams: all,
				DependsOn: []string{}, DelegationDepth: 1, DelegationChain: []string{}, WBSPath: pad(oi+1) + "." + pad(wi+1), Complexity: "S"})
			for ni, n := range w.Nodes {
				id := keyToID[n.Key]
				kind := n.Kind
				if kind == "" {
					kind = KindTask
				}
				title, tkey := text(n.TitleKey, n.Title)
				def := NodeDef{ID: id, Key: n.Key, ObjectiveID: oid, ParentID: &gid, Kind: kind, Title: title, TitleKey: tkey, TitleParams: all,
					Description: n.Description, DelegationDepth: 1, DelegationChain: []string{}, Complexity: normComplexity(n.Complexity),
					WBSPath: pad(oi+1) + "." + pad(wi+1) + "." + pad(ni+1)}
				def.DependsOn = []string{}
				for _, d := range n.Deps {
					did, ok := keyToID[d]
					if !ok {
						return inst, fmt.Errorf("%w: node %q depends on unknown node %q", domain.ErrInvalid, n.Key, d)
					}
					def.DependsOn = append(def.DependsOn, did)
				}
				if n.Agent != "" {
					a := n.Agent
					def.AgentID = &a
					def.DelegationChain = []string{a}
				}
				switch {
				case def.human():
					def.EstSeconds = n.Secs
					if def.EstSeconds <= 0 {
						def.EstSeconds = 6
					}
				default:
					def.EstSeconds = n.Secs
					if def.EstSeconds <= 0 {
						def.EstSeconds = priorSeconds(def.Complexity)
					}
					def.EstCostUSD = priorCost(def.Complexity, len(def.DependsOn))
				}
				if n.Approval != nil {
					def.ApprovalAction, def.ApprovalRisk = n.Approval.Action, n.Approval.Risk
				}
				inst.Nodes = append(inst.Nodes, def)
				if n.Delegate != nil {
					// Fork/join: the agent delegates a subtask (depth + 1) to another agent and waits for it.
					st, sk := text(n.Delegate.TitleKey, n.Delegate.Title)
					sa := n.Delegate.Agent
					cx := normComplexity(n.Delegate.Complexity)
					sub := NodeDef{ID: id + "~s", Key: n.Key + "~s", ObjectiveID: oid, ParentID: &id, Kind: KindSubtask, Title: st, TitleKey: sk, TitleParams: all,
						AgentID: &sa, DependsOn: append([]string{}, def.DependsOn...), DelegationDepth: def.DelegationDepth + 1,
						DelegationChain: append(append([]string{}, def.DelegationChain...), sa), WBSPath: def.WBSPath + "." + pad(1), Complexity: cx,
						EstSeconds: priorSeconds(cx), EstCostUSD: priorCost(cx, len(def.DependsOn)) * 0.5}
					last := &inst.Nodes[len(inst.Nodes)-1]
					last.DependsOn = append(last.DependsOn, sub.ID)
					inst.Nodes = append(inst.Nodes, sub)
				}
			}
		}
	}
	if !computeLevels(inst.Nodes) {
		return inst, fmt.Errorf("%w: the template has a dependency cycle", domain.ErrInvalid)
	}
	// est_cost of nodes with a delegated subtask counts the subtask dependency too.
	for i := range inst.Nodes {
		if n := inst.Nodes[i]; !n.isGroup() && !n.human() && n.Kind == KindTask {
			inst.Nodes[i].EstCostUSD = priorCost(n.Complexity, len(n.DependsOn))
		}
	}
	return inst, nil
}

// templateFromRecord builds a reusable template out of a project (save as template).
func templateFromRecord(r Record, id, key string) Template {
	idKey := func(nid string) string { return strings.TrimPrefix(nid, r.ID+":") }
	subOf := map[string]NodeDef{}
	for _, n := range r.Nodes {
		if n.Kind == KindSubtask && n.ParentID != nil {
			subOf[*n.ParentID] = n
		}
	}
	t := Template{ID: id, Key: key, Version: 1, Name: r.Name, Description: r.Goal, Params: []TemplateParam{}, Builtin: false}
	for _, o := range r.Objectives {
		to := TemplateObjective{Key: idKey(o.ID), Title: displayTitle(o.Title, o.TitleKey, o.TitleParams, r.Locale), Workflows: []TemplateWorkflow{}}
		for _, g := range r.Nodes {
			if !g.isGroup() || g.ObjectiveID != o.ID {
				continue
			}
			tw := TemplateWorkflow{Key: idKey(g.ID), Title: displayTitle(g.Title, g.TitleKey, g.TitleParams, r.Locale), Nodes: []TemplateNode{}}
			for _, n := range r.Nodes {
				if n.ParentID == nil || *n.ParentID != g.ID || n.Kind == KindSubtask {
					continue
				}
				tn := TemplateNode{Key: idKey(n.ID), Title: displayTitle(n.Title, n.TitleKey, n.TitleParams, r.Locale), Description: n.Description,
					Complexity: n.Complexity, Secs: n.EstSeconds}
				if n.Kind != KindTask {
					tn.Kind = n.Kind
				}
				if n.AgentID != nil {
					tn.Agent = *n.AgentID
				}
				sub, hasSub := subOf[n.ID]
				for _, d := range n.DependsOn {
					if hasSub && d == sub.ID {
						continue
					}
					tn.Deps = append(tn.Deps, idKey(d))
				}
				if n.ApprovalAction != "" {
					tn.Approval = &TemplateApproval{Action: n.ApprovalAction, Risk: orDefault(n.ApprovalRisk, "medium")}
				}
				if hasSub && sub.AgentID != nil {
					tn.Delegate = &TemplateDelegate{Agent: *sub.AgentID, Title: displayTitle(sub.Title, sub.TitleKey, sub.TitleParams, r.Locale), Complexity: sub.Complexity}
				}
				tw.Nodes = append(tw.Nodes, tn)
			}
			to.Workflows = append(to.Workflows, tw)
		}
		t.Objectives = append(t.Objectives, to)
	}
	if r.Estimate != nil {
		t.BudgetHint = &BudgetHint{P50USD: r.Estimate.Total.P50USD, P90USD: r.Estimate.Total.P90USD, Basis: "priors"}
	}
	return t
}

func displayTitle(title, key string, params map[string]string, locale string) string {
	if key != "" && hasKey(key) {
		return tr(locale, key, params)
	}
	return title
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
