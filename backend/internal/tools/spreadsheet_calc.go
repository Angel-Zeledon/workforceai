package tools

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// ---------------------------------------------------------------- spreadsheet

// Sheet is a table with a header.
type Sheet struct {
	Name    string     `json:"name"`
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

// Spreadsheet is a fake in-memory spreadsheet.
type Spreadsheet struct {
	mu     sync.Mutex
	Sheets map[string]*Sheet
}

// NewSpreadsheet seeds a sales sheet.
func NewSpreadsheet() *Spreadsheet {
	return &Spreadsheet{Sheets: map[string]*Sheet{
		"ventas": {Name: "ventas", Columns: []string{"mes", "ingresos", "costos"}, Rows: [][]string{
			{"2026-04", "120000", "80000"}, {"2026-05", "110000", "79000"}, {"2026-06", "95000", "76000"}, {"2026-07", "90000", "75000"},
		}},
	}}
}

func (s *Spreadsheet) Name() string { return "spreadsheet" }

func (s *Spreadsheet) Actions() []ActionSpec {
	return []ActionSpec{
		{"read", true, "Read a sheet (sheet)"},
		{"compute", true, "Aggregate a column (sheet, column, op=sum|avg|min|max|count)"},
		{"write", false, "Set a cell (sheet, row, column, value)"},
	}
}

func (s *Spreadsheet) sheet(a map[string]any) (*Sheet, error) {
	sh, ok := s.Sheets[strings.ToLower(argStr(a, "sheet"))]
	if !ok {
		return nil, invalid("sheet %q: %v", argStr(a, "sheet"), ErrNotFound)
	}
	return sh, nil
}

func (sh *Sheet) col(name string) int {
	for i, c := range sh.Columns {
		if strings.EqualFold(c, name) {
			return i
		}
	}
	return -1
}

func (s *Spreadsheet) Execute(_ context.Context, c Call) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, err := s.sheet(c.Args)
	if err != nil {
		return Result{}, err
	}
	switch c.Action {
	case "read":
		return Result{OK: true, Summary: fmt.Sprintf("%s: %d rows", sh.Name, len(sh.Rows)), Data: map[string]any{"sheet": sh}}, nil
	case "compute":
		ci := sh.col(argStr(c.Args, "column"))
		if ci < 0 {
			return Result{}, invalid("unknown column %q", argStr(c.Args, "column"))
		}
		var vals []float64
		for _, r := range sh.Rows {
			if f, err := strconv.ParseFloat(r[ci], 64); err == nil {
				vals = append(vals, f)
			}
		}
		if len(vals) == 0 {
			return Result{}, invalid("column has no numeric values")
		}
		op := strings.ToLower(argStr(c.Args, "op"))
		res := vals[0]
		sum := 0.0
		for i, v := range vals {
			sum += v
			if i > 0 {
				switch op {
				case "min":
					res = math.Min(res, v)
				case "max":
					res = math.Max(res, v)
				}
			}
		}
		switch op {
		case "sum":
			res = sum
		case "avg":
			res = sum / float64(len(vals))
		case "count":
			res = float64(len(vals))
		case "min", "max":
		default:
			return Result{}, invalid("unknown op %q", op)
		}
		return Result{OK: true, Summary: fmt.Sprintf("%s(%s) = %g", op, argStr(c.Args, "column"), res), Data: map[string]any{"value": res}}, nil
	case "write":
		ci := sh.col(argStr(c.Args, "column"))
		row := argInt(c.Args, "row", -1)
		if ci < 0 || row < 0 || row >= len(sh.Rows) {
			return Result{}, invalid("row/column out of range")
		}
		sh.Rows[row][ci] = argStr(c.Args, "value")
		return Result{OK: true, Summary: fmt.Sprintf("Set %s[%d].%s", sh.Name, row, argStr(c.Args, "column"))}, nil
	}
	return Result{}, unknownAction("spreadsheet", c.Action)
}

// ---------------------------------------------------------------- calculator

// Calculator is a pure calculator (no state).
type Calculator struct{}

// NewCalculator creates a Calculator.
func NewCalculator() *Calculator { return &Calculator{} }

func (*Calculator) Name() string { return "calculator" }

func (*Calculator) Actions() []ActionSpec {
	return []ActionSpec{
		{"calculate", true, "Evaluate an arithmetic expression (+ - * / % ^ parentheses)"},
		{"margin", true, "Gross margin % (revenue, cost)"},
		{"percent_change", true, "Percent change (from, to)"},
	}
}

func (*Calculator) Execute(_ context.Context, c Call) (Result, error) {
	switch c.Action {
	case "calculate":
		expr := argStr(c.Args, "expression")
		if expr == "" {
			return Result{}, invalid("expression is required")
		}
		v, err := EvalExpr(expr)
		if err != nil {
			return Result{}, invalid("%v", err)
		}
		return Result{OK: true, Summary: fmt.Sprintf("%s = %g", expr, v), Data: map[string]any{"value": v}}, nil
	case "margin":
		rev, ok1 := argNum(c.Args, "revenue")
		cost, ok2 := argNum(c.Args, "cost")
		if !ok1 || !ok2 || rev == 0 {
			return Result{}, invalid("revenue (non-zero) and cost are required")
		}
		m := (rev - cost) / rev * 100
		return Result{OK: true, Summary: fmt.Sprintf("margin %.2f%%", m), Data: map[string]any{"margin_pct": m}}, nil
	case "percent_change":
		from, ok1 := argNum(c.Args, "from")
		to, ok2 := argNum(c.Args, "to")
		if !ok1 || !ok2 || from == 0 {
			return Result{}, invalid("from (non-zero) and to are required")
		}
		p := (to - from) / from * 100
		return Result{OK: true, Summary: fmt.Sprintf("change %.2f%%", p), Data: map[string]any{"change_pct": p}}, nil
	}
	return Result{}, unknownAction("calculator", c.Action)
}

// EvalExpr evaluates + - * / % ^ and parentheses with unary minus. It never
// executes code; the input length and nesting are bounded.
func EvalExpr(s string) (float64, error) {
	if len(s) > 500 {
		return 0, fmt.Errorf("expression too long")
	}
	p := &exprParser{in: []rune(s)}
	v, err := p.expr()
	if err != nil {
		return 0, err
	}
	p.ws()
	if p.i != len(p.in) {
		return 0, fmt.Errorf("unexpected %q at %d", string(p.in[p.i]), p.i)
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("result is not finite")
	}
	return v, nil
}

type exprParser struct {
	in    []rune
	i     int
	depth int
}

func (p *exprParser) ws() {
	for p.i < len(p.in) && unicode.IsSpace(p.in[p.i]) {
		p.i++
	}
}

func (p *exprParser) peek() rune {
	p.ws()
	if p.i < len(p.in) {
		return p.in[p.i]
	}
	return 0
}

func (p *exprParser) expr() (float64, error) {
	v, err := p.term()
	if err != nil {
		return 0, err
	}
	for {
		switch p.peek() {
		case '+':
			p.i++
			r, err := p.term()
			if err != nil {
				return 0, err
			}
			v += r
		case '-':
			p.i++
			r, err := p.term()
			if err != nil {
				return 0, err
			}
			v -= r
		default:
			return v, nil
		}
	}
}

func (p *exprParser) term() (float64, error) {
	v, err := p.power()
	if err != nil {
		return 0, err
	}
	for {
		op := p.peek()
		if op != '*' && op != '/' && op != '%' {
			return v, nil
		}
		p.i++
		r, err := p.power()
		if err != nil {
			return 0, err
		}
		switch op {
		case '*':
			v *= r
		case '/':
			if r == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			v /= r
		case '%':
			if r == 0 {
				return 0, fmt.Errorf("modulo by zero")
			}
			v = math.Mod(v, r)
		}
	}
}

func (p *exprParser) power() (float64, error) {
	b, err := p.unary()
	if err != nil {
		return 0, err
	}
	if p.peek() == '^' {
		p.i++
		e, err := p.power()
		if err != nil {
			return 0, err
		}
		return math.Pow(b, e), nil
	}
	return b, nil
}

func (p *exprParser) unary() (float64, error) {
	switch p.peek() {
	case '-':
		p.i++
		v, err := p.unary()
		return -v, err
	case '+':
		p.i++
		return p.unary()
	}
	return p.atom()
}

func (p *exprParser) atom() (float64, error) {
	c := p.peek()
	if c == '(' {
		p.depth++
		if p.depth > 50 {
			return 0, fmt.Errorf("nesting too deep")
		}
		p.i++
		v, err := p.expr()
		if err != nil {
			return 0, err
		}
		if p.peek() != ')' {
			return 0, fmt.Errorf("missing )")
		}
		p.i++
		p.depth--
		return v, nil
	}
	start := p.i
	for p.i < len(p.in) && (unicode.IsDigit(p.in[p.i]) || p.in[p.i] == '.') {
		p.i++
	}
	if start == p.i {
		if p.i < len(p.in) {
			return 0, fmt.Errorf("unexpected %q at %d", string(p.in[p.i]), p.i)
		}
		return 0, fmt.Errorf("unexpected end of expression")
	}
	f, err := strconv.ParseFloat(string(p.in[start:p.i]), 64)
	if err != nil {
		return 0, fmt.Errorf("bad number %q", string(p.in[start:p.i]))
	}
	return f, nil
}
