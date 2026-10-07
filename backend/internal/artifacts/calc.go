package artifacts

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// A small formula evaluator for the cached values (`v`) of sheet cells. It
// understands what the editor produces: numbers, + - * / ^, parentheses,
// comparisons, cell references and ranges (A1, B2:C9, Sheet!A1), the functions
// SUM AVERAGE MIN MAX COUNT ROUND ABS IF and AIW_REF(alias,"Sheet!A1"), which
// reads a cell of another artifact. Anything else leaves the cached value as it
// is (the cell is reported as unresolved). The browser editor remains the
// reference for rich calculations; this keeps values coherent when a source
// artifact changes while nobody has the dependent open.

var errUnsupported = errors.New("unsupported formula")

type cellVal struct {
	num   float64
	str   string
	isStr bool
	list  []cellVal // a range
}

// sheetRef resolves AIW_REF: the cell value of an imported artifact.
type sheetRef func(alias, sheet, cell string) (cellVal, error)

type calcSheet struct {
	name  string
	cells map[string]any
}

type evaluator struct {
	sheets   map[string]*calcSheet
	cur      string
	ref      sheetRef
	memo     map[string]cellVal
	visiting map[string]bool
}

func newEvaluator(sheets []any, ref sheetRef) *evaluator {
	e := &evaluator{sheets: map[string]*calcSheet{}, ref: ref, memo: map[string]cellVal{}, visiting: map[string]bool{}}
	for _, s := range sheets {
		sm := asMap(s)
		name := asStr(sm["name"])
		e.sheets[name] = &calcSheet{name: name, cells: asMap(sm["cells"])}
	}
	return e
}

func toNum(v any) (float64, bool) {
	switch t := v.(type) {
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case float64:
		return t, true
	case int:
		return float64(t), true
	}
	return 0, false
}

func (e *evaluator) cell(sheet, addr string) (cellVal, error) {
	key := sheet + "!" + addr
	if v, ok := e.memo[key]; ok {
		return v, nil
	}
	sh := e.sheets[sheet]
	if sh == nil {
		return cellVal{}, fmt.Errorf("unknown sheet %q", sheet)
	}
	c := asMap(sh.cells[addr])
	if c == nil {
		return cellVal{}, nil // empty cell
	}
	if f, ok := c["f"].(string); ok && f != "" {
		if e.visiting[key] {
			return cellVal{}, errors.New("circular reference")
		}
		e.visiting[key] = true
		prev := e.cur
		e.cur = sheet
		v, err := e.evalFormula(f)
		e.cur = prev
		delete(e.visiting, key)
		if err != nil {
			return cellVal{}, err
		}
		e.memo[key] = v
		return v, nil
	}
	var v cellVal
	if n, ok := toNum(c["v"]); ok {
		v.num = n
	} else if s, ok := c["v"].(string); ok {
		v.str, v.isStr = s, true
	}
	e.memo[key] = v
	return v, nil
}

func (e *evaluator) evalFormula(f string) (cellVal, error) {
	p := &fparser{src: strings.TrimPrefix(strings.TrimSpace(f), "="), e: e}
	v, err := p.expr()
	if err != nil {
		return cellVal{}, err
	}
	p.skip()
	if p.i < len(p.src) {
		return cellVal{}, errUnsupported
	}
	return v, nil
}

type fparser struct {
	src string
	i   int
	e   *evaluator
}

func (p *fparser) skip() {
	for p.i < len(p.src) && (p.src[p.i] == ' ' || p.src[p.i] == '\t') {
		p.i++
	}
}

func (p *fparser) peek() byte {
	p.skip()
	if p.i < len(p.src) {
		return p.src[p.i]
	}
	return 0
}

func (p *fparser) eat(s string) bool {
	p.skip()
	if strings.HasPrefix(p.src[p.i:], s) {
		p.i += len(s)
		return true
	}
	return false
}

func (p *fparser) expr() (cellVal, error) {
	l, err := p.additive()
	if err != nil {
		return l, err
	}
	for _, op := range []string{"<=", ">=", "<>", "=", "<", ">"} {
		if p.eat(op) {
			r, err := p.additive()
			if err != nil {
				return r, err
			}
			ok := false
			switch op {
			case "<=":
				ok = l.num <= r.num
			case ">=":
				ok = l.num >= r.num
			case "<>":
				ok = l.num != r.num || l.str != r.str
			case "=":
				ok = l.num == r.num && l.str == r.str
			case "<":
				ok = l.num < r.num
			case ">":
				ok = l.num > r.num
			}
			if ok {
				return cellVal{num: 1}, nil
			}
			return cellVal{}, nil
		}
	}
	return l, nil
}

func (p *fparser) additive() (cellVal, error) {
	l, err := p.term()
	for err == nil {
		switch p.peek() {
		case '+':
			p.i++
			var r cellVal
			if r, err = p.term(); err == nil {
				l = cellVal{num: l.num + r.num}
			}
		case '-':
			p.i++
			var r cellVal
			if r, err = p.term(); err == nil {
				l = cellVal{num: l.num - r.num}
			}
		default:
			return l, nil
		}
	}
	return l, err
}

func (p *fparser) term() (cellVal, error) {
	l, err := p.power()
	for err == nil {
		switch p.peek() {
		case '*':
			p.i++
			var r cellVal
			if r, err = p.power(); err == nil {
				l = cellVal{num: l.num * r.num}
			}
		case '/':
			p.i++
			var r cellVal
			if r, err = p.power(); err == nil {
				if r.num == 0 {
					return cellVal{}, errors.New("division by zero")
				}
				l = cellVal{num: l.num / r.num}
			}
		default:
			return l, nil
		}
	}
	return l, err
}

func (p *fparser) power() (cellVal, error) {
	l, err := p.unary()
	if err != nil {
		return l, err
	}
	if p.eat("^") {
		r, err := p.power()
		if err != nil {
			return r, err
		}
		return cellVal{num: math.Pow(l.num, r.num)}, nil
	}
	return l, nil
}

func (p *fparser) unary() (cellVal, error) {
	switch p.peek() {
	case '-':
		p.i++
		v, err := p.unary()
		v.num = -v.num
		return v, err
	case '+':
		p.i++
		return p.unary()
	}
	return p.primary()
}

var (
	numTokRe   = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?`)
	identTokRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*`)
	addrRe     = regexp.MustCompile(`^([A-Z]{1,3})([0-9]{1,7})$`)
)

func (p *fparser) primary() (cellVal, error) {
	p.skip()
	rest := p.src[p.i:]
	switch {
	case rest == "":
		return cellVal{}, errUnsupported
	case rest[0] == '(':
		p.i++
		v, err := p.expr()
		if err != nil {
			return v, err
		}
		if !p.eat(")") {
			return v, errUnsupported
		}
		return v, nil
	case rest[0] == '"':
		end := strings.Index(rest[1:], `"`)
		if end < 0 {
			return cellVal{}, errUnsupported
		}
		p.i += end + 2
		return cellVal{str: rest[1 : end+1], isStr: true}, nil
	}
	if m := numTokRe.FindString(rest); m != "" {
		p.i += len(m)
		f, _ := strconv.ParseFloat(m, 64)
		return cellVal{num: f}, nil
	}
	id := identTokRe.FindString(rest)
	if id == "" {
		return cellVal{}, errUnsupported
	}
	p.i += len(id)
	if p.peek() == '(' {
		p.i++
		return p.call(strings.ToUpper(id))
	}
	sheet := p.e.cur
	if p.peek() == '!' { // Sheet!A1
		p.i++
		sheet = id
		id = identTokRe.FindString(p.src[p.i:])
		p.i += len(id)
	}
	if !addrRe.MatchString(id) {
		return cellVal{}, errUnsupported
	}
	if p.peek() == ':' { // range
		p.i++
		end := identTokRe.FindString(p.src[p.i:])
		if !addrRe.MatchString(end) {
			return cellVal{}, errUnsupported
		}
		p.i += len(end)
		return p.rangeVals(sheet, id, end)
	}
	return p.e.cell(sheet, id)
}

func splitAddr(a string) (col, row int) {
	m := addrRe.FindStringSubmatch(a)
	for _, ch := range m[1] {
		col = col*26 + int(ch-'A') + 1
	}
	row, _ = strconv.Atoi(m[2])
	return
}

func colName(c int) string {
	s := ""
	for c > 0 {
		c--
		s = string(rune('A'+c%26)) + s
		c /= 26
	}
	return s
}

func (p *fparser) rangeVals(sheet, a, b string) (cellVal, error) {
	c1, r1 := splitAddr(a)
	c2, r2 := splitAddr(b)
	if c1 > c2 {
		c1, c2 = c2, c1
	}
	if r1 > r2 {
		r1, r2 = r2, r1
	}
	if (c2-c1+1)*(r2-r1+1) > 20000 {
		return cellVal{}, errUnsupported
	}
	var out cellVal
	for c := c1; c <= c2; c++ {
		for r := r1; r <= r2; r++ {
			v, err := p.e.cell(sheet, colName(c)+strconv.Itoa(r))
			if err != nil {
				return cellVal{}, err
			}
			out.list = append(out.list, v)
		}
	}
	return out, nil
}

func (p *fparser) args() ([]cellVal, error) {
	var out []cellVal
	if p.eat(")") {
		return out, nil
	}
	for {
		v, err := p.expr()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		if p.eat(")") {
			return out, nil
		}
		if !p.eat(",") {
			return nil, errUnsupported
		}
	}
}

func flatten(args []cellVal) []float64 {
	var out []float64
	for _, a := range args {
		if a.list != nil {
			for _, x := range a.list {
				if !x.isStr {
					out = append(out, x.num)
				}
			}
			continue
		}
		out = append(out, a.num)
	}
	return out
}

func (p *fparser) call(name string) (cellVal, error) {
	args, err := p.args()
	if err != nil {
		return cellVal{}, err
	}
	nums := flatten(args)
	switch name {
	case "SUM":
		s := 0.0
		for _, n := range nums {
			s += n
		}
		return cellVal{num: s}, nil
	case "AVERAGE":
		if len(nums) == 0 {
			return cellVal{}, errors.New("division by zero")
		}
		s := 0.0
		for _, n := range nums {
			s += n
		}
		return cellVal{num: s / float64(len(nums))}, nil
	case "MIN", "MAX":
		if len(nums) == 0 {
			return cellVal{}, nil
		}
		best := nums[0]
		for _, n := range nums[1:] {
			if (name == "MIN" && n < best) || (name == "MAX" && n > best) {
				best = n
			}
		}
		return cellVal{num: best}, nil
	case "COUNT":
		return cellVal{num: float64(len(nums))}, nil
	case "ABS":
		if len(args) != 1 {
			return cellVal{}, errUnsupported
		}
		return cellVal{num: math.Abs(args[0].num)}, nil
	case "ROUND":
		if len(args) < 1 || len(args) > 2 {
			return cellVal{}, errUnsupported
		}
		digits := 0.0
		if len(args) == 2 {
			digits = args[1].num
		}
		pow := math.Pow(10, digits)
		return cellVal{num: math.Round(args[0].num*pow) / pow}, nil
	case "IF":
		if len(args) < 2 || len(args) > 3 {
			return cellVal{}, errUnsupported
		}
		if args[0].num != 0 || (args[0].isStr && args[0].str != "") {
			return args[1], nil
		}
		if len(args) == 3 {
			return args[2], nil
		}
		return cellVal{}, nil
	case "AIW_REF":
		if len(args) != 2 || !args[0].isStr || !args[1].isStr || p.e.ref == nil {
			return cellVal{}, errUnsupported
		}
		sheet, cell, ok := strings.Cut(args[1].str, "!")
		if !ok || !addrRe.MatchString(cell) {
			return cellVal{}, errUnsupported
		}
		return p.e.ref(args[0].str, sheet, cell)
	}
	return cellVal{}, errUnsupported
}

// recalcSheets recomputes the cached value of every formula cell of the sheets
// (decoded content, modified in place). It returns the changed cells
// ("Sheet!A1") and the formulas it could not evaluate.
func recalcSheets(root map[string]any, ref sheetRef) (changed, unresolved []string) {
	sheets := asList(root["sheets"])
	ev := newEvaluator(sheets, ref)
	for _, s := range sheets {
		sm := asMap(s)
		name := asStr(sm["name"])
		cells := asMap(sm["cells"])
		for addr, c := range cells {
			cm := asMap(c)
			f, _ := cm["f"].(string)
			if f == "" {
				continue
			}
			v, err := ev.cell(name, addr)
			if err != nil {
				unresolved = append(unresolved, name+"!"+addr)
				continue
			}
			var nv any = v.num
			if v.isStr {
				nv = v.str
			}
			if !equal(cm["v"], nv) {
				cm["v"] = nv
				changed = append(changed, name+"!"+addr)
			}
		}
	}
	return changed, unresolved
}
