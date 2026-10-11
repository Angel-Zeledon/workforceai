package projects

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
)

// Templates as data (G2). A template is a tree objectives -> workflows -> nodes
// with parameters. A workflow can repeat over one or two list parameters
// ("Repeat"): the "per subsidiary" / "per country" pattern of the multinational
// templates, generalized, so a user can build any "block per item" structure
// without code. Expansion is pure and bounded: it happens before the plan is
// instantiated, and the bounds (items per list, nodes, edges) are checked
// before anything is built.

const (
	// Bounds of a list parameter and of the definition of a template.
	DefaultListItems = 20
	MaxListItems     = 50
	MaxItemLen       = 80
	maxTplObjectives = 30
	maxTplWorkflows  = 400
	maxTplNodes      = 1000
	maxTplNodesPerWF = 200
	maxTplParams     = 12
	maxTplDeps       = 200
	maxTplEdges      = 250000
	maxTplTitle      = 300
	maxTplDesc       = 4000
	maxTplName       = 120

	LimitListItems     = "max_list_items"
	LimitTemplateEdges = "max_template_edges"

	ParamText   = "text"
	ParamNumber = "number"
	ParamList   = "list"
)

var (
	tplKeyRe      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,31}$`)
	paramKeyRe    = regexp.MustCompile(`^[a-z][a-zA-Z0-9]{0,23}$`)
	placeholderRe = regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9_]*)\}`)
	actionRe      = regexp.MustCompile(`^[a-z][a-z0-9_]{1,40}$`)
)

func (p TemplateParam) kind() string {
	switch p.Type {
	case ParamNumber, ParamList:
		return p.Type
	}
	return ParamText
}

func (p TemplateParam) maxItems() int {
	if p.Max > 0 {
		return min(p.Max, MaxListItems)
	}
	return DefaultListItems
}

// hasRepeat reports whether any workflow repeats over a list parameter.
func (t Template) hasRepeat() bool {
	for _, o := range t.Objectives {
		for _, w := range o.Workflows {
			if len(w.Repeat) > 0 {
				return true
			}
		}
	}
	return false
}

// isOrg reports whether the template is an organization template (saved by a user).
func (t Template) isOrg() bool { return !t.Builtin && (t.CreatedAt != nil || t.CreatedBy != "") }

// needsData tells whether instantiate must run the data expansion first.
func (t Template) needsData() bool {
	if t.hasRepeat() || t.isOrg() {
		return true
	}
	for _, p := range t.Params {
		if p.kind() == ParamList {
			return true
		}
	}
	return false
}

// itemNames assigns the placeholder of the current item to each list parameter.
func itemNames(params []TemplateParam) map[string]string {
	out := map[string]string{}
	n := 0
	for _, p := range params {
		if p.kind() != ParamList {
			continue
		}
		n++
		name := p.ItemName
		if name == "" {
			name = "item"
			if n > 1 {
				name = "item" + strconv.Itoa(n)
			}
		}
		out[p.Key] = name
	}
	return out
}

func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\r' })
}

func cleanItem(s string) string {
	return truncRunes(catalog.SanitizeParam(s), MaxItemLen)
}

// resolveParams applies defaults and bounds to the user values. Text values are
// sanitized; numbers are clamped; lists are split, cleaned and bounded (an
// oversized list is an error, never silently cut).
func resolveParams(t Template, raw map[string]string, locale string) (map[string]string, map[string][]string, error) {
	vals := map[string]string{}
	lists := map[string][]string{}
	for _, p := range t.Params {
		v := strings.TrimSpace(raw[p.Key])
		def := p.Default
		if def == "" && p.DefaultKey != "" {
			def = tr(locale, p.DefaultKey, nil)
		}
		switch p.kind() {
		case ParamNumber:
			lo, hi := p.Min, p.Max
			if hi <= 0 {
				hi = 1000
			}
			d, _ := strconv.Atoi(strings.TrimSpace(def))
			vals[p.Key] = strconv.Itoa(clampParam(v, min(max(d, lo), hi), lo, hi))
		case ParamList:
			if v == "" {
				v = def
			}
			var items []string
			for _, it := range splitList(v) {
				if c := cleanItem(it); c != "" {
					items = append(items, c)
				}
			}
			if len(items) == 0 {
				return nil, nil, fmt.Errorf("%w: parameter %q needs at least one item", domain.ErrInvalid, p.Key)
			}
			if len(items) > p.maxItems() {
				return nil, nil, limitError(domain.ErrInvalid, LimitListItems, p.maxItems(), fmt.Sprintf("parameter %q has %d items", p.Key, len(items)))
			}
			lists[p.Key] = items
			vals[p.Key] = strings.Join(items, ", ")
		default:
			c := catalog.SanitizeParam(v)
			if c == "" {
				c = def
			}
			vals[p.Key] = c
		}
	}
	return vals, lists, nil
}

func substText(s string, vars map[string]string) string {
	if !strings.Contains(s, "{") {
		return s
	}
	return placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := vars[m[1:len(m)-1]]; ok {
			return v
		}
		return m
	})
}

func mergeVars(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// repeatDims returns the positions (in the order of the list parameters) a
// workflow repeats over.
func repeatDims(w TemplateWorkflow, listOrder []string) ([]int, error) {
	var dims []int
	for _, k := range w.Repeat {
		i := slices.Index(listOrder, k)
		if i < 0 {
			return nil, fmt.Errorf("%w: workflow %q repeats over %q, which is not a list parameter", domain.ErrInvalid, w.Key, k)
		}
		if !slices.Contains(dims, i) {
			dims = append(dims, i)
		}
	}
	if len(dims) > 2 {
		return nil, fmt.Errorf("%w: workflow %q repeats over more than two lists", domain.ErrInvalid, w.Key)
	}
	slices.Sort(dims)
	return dims, nil
}

// combos enumerates the index tuples of the dims in lexicographic order.
func combos(dims []int, lens []int) [][]int {
	out := [][]int{{}}
	for _, d := range dims {
		var next [][]int
		for _, c := range out {
			for i := 0; i < lens[d]; i++ {
				next = append(next, append(slices.Clone(c), i))
			}
		}
		out = next
	}
	return out
}

func suffixOf(idx []int) string {
	var b strings.Builder
	for _, i := range idx {
		b.WriteString("_" + strconv.Itoa(i+1))
	}
	return b.String()
}

// expandData turns a data template into a flat one: parameters substituted,
// repeated workflows unrolled. It returns the normalized parameter values.
func expandData(t Template, goal string, raw map[string]string, locale string, lim Limits) (Template, map[string]string, error) {
	lim = lim.withDefaults()
	vals, lists, err := resolveParams(t, raw, locale)
	if err != nil {
		return t, nil, err
	}
	var listOrder []string
	for _, p := range t.Params {
		if p.kind() == ParamList {
			listOrder = append(listOrder, p.Key)
		}
	}
	names := itemNames(t.Params)
	lens := make([]int, len(listOrder))
	for i, k := range listOrder {
		lens[i] = len(lists[k])
	}
	type wfRef struct {
		dims []int
	}
	wfOf := map[string]wfRef{} // node key -> dims of its workflow
	total := 0
	for _, o := range t.Objectives {
		for _, w := range o.Workflows {
			dims, err := repeatDims(w, listOrder)
			if err != nil {
				return t, nil, err
			}
			mult := 1
			for _, d := range dims {
				mult *= lens[d]
			}
			per := 1 + len(w.Nodes)
			for _, n := range w.Nodes {
				if _, dup := wfOf[n.Key]; dup {
					return t, nil, fmt.Errorf("%w: duplicate node key %q", domain.ErrInvalid, n.Key)
				}
				wfOf[n.Key] = wfRef{dims}
				if n.Delegate != nil {
					per++
				}
			}
			total += mult * per
			if total > lim.MaxNodesPerProject {
				return t, nil, limitError(domain.ErrInvalid, LimitMaxNodes, lim.MaxNodesPerProject, fmt.Sprintf("the template expands to more than %d nodes", total-1))
			}
		}
	}
	base := mergeVars(vals, map[string]string{"goal": truncRunes(strings.TrimSpace(goal), 60)})
	out := t
	out.Name = substText(t.Name, base)
	out.Description = substText(t.Description, base)
	out.Objectives = make([]TemplateObjective, 0, len(t.Objectives))
	edges := 0
	for _, o := range t.Objectives {
		no := o
		no.Title = substText(o.Title, base)
		no.Params = o.Params
		no.Workflows = nil
		for _, w := range o.Workflows {
			dims, _ := repeatDims(w, listOrder)
			for _, idx := range combos(dims, lens) {
				sfx := suffixOf(idx)
				vars := base
				itemVars := map[string]string{}
				for j, d := range dims {
					itemVars[names[listOrder[d]]] = lists[listOrder[d]][idx[j]]
					itemVars[map[bool]string{true: "index", false: "index2"}[j == 0]] = strconv.Itoa(idx[j] + 1)
				}
				if len(dims) > 0 {
					vars = mergeVars(base, itemVars)
				}
				nw := TemplateWorkflow{Key: w.Key + sfx, TitleKey: w.TitleKey, Title: substText(w.Title, vars), Params: w.Params}
				if len(dims) > 0 && w.TitleKey != "" {
					nw.Params = mergeVars(w.Params, itemVars)
				}
				// position of each dim of this instance, by list position
				pos := map[int]int{}
				for j, d := range dims {
					pos[d] = idx[j]
				}
				for _, n := range w.Nodes {
					nn := n
					nn.Key = n.Key + sfx
					nn.Title = substText(n.Title, vars)
					nn.Description = substText(n.Description, vars)
					if len(n.Acceptance) > 0 {
						nn.Acceptance = make([]string, len(n.Acceptance))
						for i, a := range n.Acceptance {
							nn.Acceptance[i] = substText(a, vars)
						}
					}
					if len(dims) > 0 && n.TitleKey != "" {
						nn.Params = mergeVars(n.Params, itemVars)
					}
					if n.Delegate != nil {
						d := *n.Delegate
						d.Title = substText(d.Title, vars)
						nn.Delegate = &d
					}
					nn.Deps = nil
					for _, dep := range n.Deps {
						dk, prev, _ := strings.Cut(dep, "^")
						ref, ok := wfOf[dk]
						if !ok {
							return t, nil, fmt.Errorf("%w: node %q depends on unknown node %q", domain.ErrInvalid, n.Key, dk)
						}
						fixed := pos
						if prev != "" {
							// "key^list": the same instance but the previous item of that list (none for the first item).
							pi := slices.Index(listOrder, prev)
							at, here := pos[pi]
							if pi < 0 || !here || !slices.Contains(ref.dims, pi) {
								return t, nil, fmt.Errorf("%w: node %q: %q is not a repeat dimension of both blocks", domain.ErrInvalid, n.Key, prev)
							}
							if at == 0 {
								continue
							}
							fixed = map[int]int{}
							for k, v := range pos {
								fixed[k] = v
							}
							fixed[pi] = at - 1
						}
						// Dims shared with the dependent use its item; the others fan in.
						for _, tgt := range combosFixed(ref.dims, lens, fixed) {
							nn.Deps = append(nn.Deps, dk+suffixOf(tgt))
							if edges++; edges > maxTplEdges {
								return t, nil, limitError(domain.ErrInvalid, LimitTemplateEdges, maxTplEdges, "the template has too many dependencies after expansion")
							}
						}
					}
					nw.Nodes = append(nw.Nodes, nn)
				}
				no.Workflows = append(no.Workflows, nw)
			}
		}
		out.Objectives = append(out.Objectives, no)
	}
	return out, vals, nil
}

// combosFixed enumerates the index tuples of dims where the dims present in
// fixed take that value and the others range over every item.
func combosFixed(dims []int, lens []int, fixed map[int]int) [][]int {
	out := [][]int{{}}
	for _, d := range dims {
		var next [][]int
		for _, c := range out {
			if v, ok := fixed[d]; ok {
				next = append(next, append(slices.Clone(c), v))
				continue
			}
			for i := 0; i < lens[d]; i++ {
				next = append(next, append(slices.Clone(c), i))
			}
		}
		out = next
	}
	return out
}

// ---- validation ----

// Template validation issue codes (Issue.Code); NodeID carries the template key.
const (
	IssueBadKey      = "invalid_key"
	IssueDupKey      = "duplicate_key"
	IssueUnknownDep  = "unknown_dependency"
	IssueSelfDep     = "self_dependency"
	IssueTooLarge    = "too_large"
	IssueBadParam    = "invalid_param"
	IssueBadRepeat   = "invalid_repeat"
	IssueBadApproval = "invalid_approval"
	IssueBadName     = "invalid_name"
)

// ValidateTemplate checks the definition of a template: structure, keys,
// dependencies (unknown, self, cycles), agents, parameters, repeats and sizes,
// and that it expands within the limits both at its defaults and at its maximum
// parameters. agentIDs empty skips the agent existence check.
func ValidateTemplate(t Template, agentIDs []string, lim Limits) []Issue {
	lim = lim.withDefaults()
	var issues []Issue
	add := func(sev, key, code string) { issues = append(issues, Issue{Severity: sev, NodeID: key, Code: code}) }
	if n := len([]rune(strings.TrimSpace(t.Name))); (n == 0 && t.NameKey == "") || n > maxTplName {
		add("error", "", IssueBadName)
	}
	if len([]rune(t.Description)) > maxTplDesc {
		add("error", "", IssueTooLarge)
	}
	if len(t.Params) > maxTplParams {
		add("error", "", IssueTooLarge)
	}
	pkeys := map[string]bool{}
	items := map[string]bool{}
	var listOrder []string
	for _, p := range t.Params {
		if !paramKeyRe.MatchString(p.Key) || pkeys[p.Key] || p.Key == "goal" || strings.HasPrefix(p.Key, "item") || strings.HasPrefix(p.Key, "index") {
			add("error", p.Key, IssueBadParam)
		}
		pkeys[p.Key] = true
		if p.Type != "" && p.Type != ParamText && p.Type != ParamNumber && p.Type != ParamList {
			add("error", p.Key, IssueBadParam)
		}
		if p.kind() == ParamNumber && p.Max > 0 && p.Min > p.Max {
			add("error", p.Key, IssueBadParam)
		}
		if p.kind() == ParamList {
			listOrder = append(listOrder, p.Key)
			if p.ItemName != "" {
				if !paramKeyRe.MatchString(p.ItemName) || items[p.ItemName] || pkeys[p.ItemName] {
					add("error", p.Key, IssueBadParam)
				}
				items[p.ItemName] = true
			}
			if p.Max > MaxListItems {
				add("error", p.Key, IssueBadParam)
			}
		}
		if len([]rune(p.Label)) > maxTplTitle || len([]rune(p.Default)) > 2000 {
			add("error", p.Key, IssueTooLarge)
		}
	}
	if len(t.Objectives) == 0 {
		add("error", "", "no_nodes")
	}
	if len(t.Objectives) > maxTplObjectives {
		add("error", "", IssueTooLarge)
	}
	keys := map[string]bool{} // objectives, workflows and nodes share one namespace
	nodeKeys := map[string]bool{}
	repeatOf := map[string][]string{} // node key -> repeat dims of its workflow
	nodes := 0
	workflows := 0
	for _, o := range t.Objectives {
		if !tplKeyRe.MatchString(o.Key) || keys[o.Key] {
			add("error", o.Key, IssueBadKey)
		}
		keys[o.Key] = true
		if strings.TrimSpace(o.Title) == "" && o.TitleKey == "" {
			add("error", o.Key, "empty_title")
		}
		for _, w := range o.Workflows {
			workflows++
			if !tplKeyRe.MatchString(w.Key) || keys[w.Key] {
				add("error", w.Key, IssueBadKey)
			}
			keys[w.Key] = true
			if strings.TrimSpace(w.Title) == "" && w.TitleKey == "" {
				add("error", w.Key, "empty_title")
			}
			if len(w.Nodes) > maxTplNodesPerWF {
				add("error", w.Key, IssueTooLarge)
			}
			if _, err := repeatDims(w, listOrder); err != nil {
				add("error", w.Key, IssueBadRepeat)
			}
			for _, n := range w.Nodes {
				nodes++
				if !tplKeyRe.MatchString(n.Key) {
					add("error", n.Key, IssueBadKey)
				}
				if keys[n.Key] {
					add("error", n.Key, IssueDupKey)
				}
				keys[n.Key], nodeKeys[n.Key] = true, true
				repeatOf[n.Key] = w.Repeat
			}
		}
	}
	if workflows > maxTplWorkflows || nodes > maxTplNodes {
		add("error", "", IssueTooLarge)
	}
	if nodes == 0 {
		add("error", "", "no_nodes")
	}
	// per node checks and the key graph
	graph := map[string][]string{}
	for _, o := range t.Objectives {
		for _, w := range o.Workflows {
			for _, n := range w.Nodes {
				human := n.Kind == KindGate || n.Kind == KindMilestone || n.Kind == KindWait
				switch n.Kind {
				case "", KindTask, KindGate, KindMilestone, KindWait:
				default:
					add("error", n.Key, "invalid_kind")
				}
				if strings.TrimSpace(n.Title) == "" && n.TitleKey == "" {
					add("error", n.Key, "empty_title")
				}
				if len([]rune(n.Title)) > maxTplTitle || len([]rune(n.Description)) > maxTplDesc || len(n.Acceptance) > MaxAcceptance {
					add("error", n.Key, IssueTooLarge)
				}
				if n.Complexity != "" {
					if _, ok := priors[n.Complexity]; !ok {
						add("error", n.Key, "invalid_complexity")
					}
				}
				if !human {
					switch {
					case n.Agent == "":
						add("error", n.Key, "no_agent")
					case len(agentIDs) > 0 && !slices.Contains(agentIDs, n.Agent):
						add("error", n.Key, "unknown_agent")
					}
				}
				if n.Delegate != nil {
					if n.Delegate.Agent == "" || (len(agentIDs) > 0 && !slices.Contains(agentIDs, n.Delegate.Agent)) {
						add("error", n.Key, "unknown_agent")
					}
					if n.Delegate.Title == "" && n.Delegate.TitleKey == "" {
						add("error", n.Key, "empty_title")
					}
				}
				if n.Kind == KindGate && n.Approval == nil {
					add("warn", n.Key, IssueBadApproval) // gets the default action on instantiate
				}
				if a := n.Approval; a != nil {
					if !actionRe.MatchString(a.Action) || (a.Risk != "" && a.Risk != "low" && a.Risk != "medium" && a.Risk != "high") {
						add("error", n.Key, IssueBadApproval)
					}
				}
				if len(n.Deps) > maxTplDeps {
					add("error", n.Key, IssueTooLarge)
				}
				for _, dep := range n.Deps {
					d, prev, hasPrev := strings.Cut(dep, "^")
					switch {
					case !nodeKeys[d]:
						add("error", n.Key, IssueUnknownDep)
					case d == n.Key && !hasPrev:
						add("error", n.Key, IssueSelfDep)
					case hasPrev && !(slices.Contains(w.Repeat, prev) && slices.Contains(repeatOf[d], prev)):
						add("error", n.Key, IssueBadRepeat)
					case hasPrev:
						// the previous item of a list is not a cycle: it is a chain
					default:
						graph[n.Key] = append(graph[n.Key], d)
					}
				}
			}
		}
	}
	if keyGraphCyclic(graph) {
		add("error", "", "cycle")
	}
	if hasErrors(issues) {
		return issues
	}
	// It must expand within the limits at the defaults and at the maximum parameters.
	if _, _, err := expandData(t, "", nil, "es", lim); err != nil {
		add("error", "", expandCode(err))
	}
	if _, _, err := expandData(t, "", maxParams(t), "es", lim); err != nil {
		add("error", "", expandCode(err))
	}
	return issues
}

func expandCode(err error) string {
	m := regexp.MustCompile(`limit_exceeded: (\w+)`).FindStringSubmatch(err.Error())
	if m != nil {
		return m[1]
	}
	return IssueTooLarge
}

func keyGraphCyclic(g map[string][]string) bool {
	state := map[string]int{}
	var visit func(string) bool
	visit = func(k string) bool {
		switch state[k] {
		case 1:
			return true
		case 2:
			return false
		}
		state[k] = 1
		for _, d := range g[k] {
			if visit(d) {
				return true
			}
		}
		state[k] = 2
		return false
	}
	for k := range g {
		if visit(k) {
			return true
		}
	}
	return false
}

// maxParams returns parameter values at the largest size the template allows.
func maxParams(t Template) map[string]string {
	out := map[string]string{}
	for _, p := range t.Params {
		switch p.kind() {
		case ParamNumber:
			if p.Max > 0 {
				out[p.Key] = strconv.Itoa(p.Max)
			}
		case ParamList:
			items := make([]string, p.maxItems())
			for i := range items {
				items[i] = "Item " + strconv.Itoa(i+1)
			}
			out[p.Key] = strings.Join(items, ", ")
		}
	}
	return out
}

// expandedFor produces the flat plan template for the given parameters,
// running whichever expansion the template needs.
func expandedFor(t Template, goal string, params map[string]string, locale string, lim Limits) (Template, map[string]string, error) {
	if t.Key == keyCashClose && t.Builtin {
		t = expandCashClose(params, locale)
	}
	if t.Builtin {
		if x, ok := expandMultinational(t, params, locale); ok {
			t = x
		}
	}
	if t.needsData() {
		x, vals, err := expandData(t, goal, params, locale, lim)
		if err != nil {
			return t, params, err
		}
		return x, vals, nil
	}
	return t, params, nil
}

// leafCounts counts work items and human gates of an expanded template.
func leafCounts(t Template) (tasks, gates int) {
	for _, o := range t.Objectives {
		for _, w := range o.Workflows {
			for _, n := range w.Nodes {
				tasks++
				if n.Kind == KindGate {
					gates++
				}
				if n.Delegate != nil {
					tasks++
				}
			}
		}
	}
	return
}

// statsOf computes the size of a template at its default and maximum parameters.
func statsOf(t Template, locale string, lim Limits) *TemplateStat {
	st := &TemplateStat{Objectives: len(t.Objectives)}
	if x, _, err := expandedFor(t, "", nil, locale, lim); err == nil {
		st.Tasks, st.Gates = leafCounts(x)
		st.Objectives = len(x.Objectives)
		st.TasksMax = st.Tasks
	}
	if x, _, err := expandedFor(t, "", maxParams(t), locale, lim); err == nil {
		st.TasksMax, _ = leafCounts(x)
	}
	return st
}

// normalizeKeys rewrites the keys that do not fit the key format (a project
// saved as a template carries keys such as "s1_close" or "p3_t7") and the
// dependencies that point to them. Valid keys are left untouched.
func normalizeKeys(t *Template) {
	used := map[string]bool{}
	for _, o := range t.Objectives {
		used[o.Key] = true
		for _, w := range o.Workflows {
			used[w.Key] = true
			for _, n := range w.Nodes {
				used[n.Key] = true
			}
		}
	}
	remap := map[string]string{}
	fix := func(k string) string {
		if tplKeyRe.MatchString(k) {
			return k
		}
		if v, ok := remap[k]; ok {
			return v
		}
		var b strings.Builder
		for _, r := range k {
			if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
		base := b.String()
		if base == "" || !(base[0] >= 'A' && base[0] <= 'Z' || base[0] >= 'a' && base[0] <= 'z') {
			base = "k" + base
		}
		base = truncRunes(base, 28)
		cand := base
		for i := 2; used[cand]; i++ {
			cand = base + "x" + strconv.Itoa(i)
		}
		used[cand] = true
		remap[k] = cand
		return cand
	}
	for oi := range t.Objectives {
		o := &t.Objectives[oi]
		o.Key = fix(o.Key)
		for wi := range o.Workflows {
			w := &o.Workflows[wi]
			w.Key = fix(w.Key)
			for ni := range w.Nodes {
				w.Nodes[ni].Key = fix(w.Nodes[ni].Key)
			}
		}
	}
	if len(remap) == 0 {
		return
	}
	for oi := range t.Objectives {
		for wi := range t.Objectives[oi].Workflows {
			for ni := range t.Objectives[oi].Workflows[wi].Nodes {
				n := &t.Objectives[oi].Workflows[wi].Nodes[ni]
				for di, d := range n.Deps {
					if v, ok := remap[d]; ok {
						n.Deps[di] = v
					}
				}
			}
		}
	}
}
