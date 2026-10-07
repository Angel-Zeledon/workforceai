package policy

import (
	"encoding/json"
	"math"
	"net/mail"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// facts are values derived from the call args. They are only ever used to ADD
// restrictions: no fact can lower a decision.
type facts struct {
	amounts        []float64
	amountInvalid  bool // an amount-like field was present but unparseable/negative
	recipients     []string
	hasRecipientKy bool
	categories     map[string]bool
}

var amountKeys = map[string]bool{
	"amount": true, "amount_usd": true, "total": true, "value": true, "price": true,
	"deal_value": true, "budget": true, "payment_amount": true, "salary": true, "cost": true,
	"monto": true, "importe": true, "precio": true,
}

var recipientKeys = map[string]bool{
	"to": true, "cc": true, "bcc": true, "recipient": true, "recipients": true,
	"attendees": true, "email": true, "destinatario": true, "destinatarios": true,
}

var categoryKeys = map[string]bool{
	"category": true, "categories": true, "document_type": true, "type": true, "kind": true, "doc_type": true,
}

// keys whose text is scanned for keywords.
var textKeys = map[string]bool{
	"subject": true, "title": true, "filename": true, "name": true, "template": true,
	"attachments": true, "attachment": true, "body": true, "text": true, "description": true,
	"category": true, "categories": true, "document_type": true, "type": true, "kind": true, "doc_type": true,
	"document": true, "doc_id": true, "path": true,
}

var legalWords = []string{
	"contract", "contrato", "nda", "agreement", "acuerdo", "clausula", "cláusula", "clause",
	"terms and conditions", "terminos y condiciones", "términos y condiciones", "binding",
	"vinculante", "power of attorney", "poder notarial", "legal", "lawsuit", "demanda", "settlement",
	"confidencialidad",
}

var financialWords = []string{"invoice", "factura", "payment", "pago", "wire", "transferencia", "payroll", "nomina", "nómina"}

func analyze(args map[string]any) facts {
	f := facts{categories: map[string]bool{}}
	walk(args, 0, &f, "")
	sort.Strings(f.recipients)
	return f
}

func walk(v any, depth int, f *facts, key string) {
	if depth > 6 {
		return
	}
	k := strings.ToLower(strings.TrimSpace(key))
	switch t := v.(type) {
	case map[string]any:
		for kk, vv := range t {
			walk(vv, depth+1, f, kk)
		}
	case []any:
		for _, vv := range t {
			walk(vv, depth+1, f, key)
		}
	case []string:
		for _, s := range t {
			walk(s, depth+1, f, key)
		}
	case []map[string]any:
		for _, m := range t {
			walk(m, depth+1, f, key)
		}
	case nil:
		if amountKeys[k] || recipientKeys[k] {
			// explicit null: nothing to parse
			if recipientKeys[k] {
				f.hasRecipientKy = true
			}
		}
	default:
		if amountKeys[k] {
			if a, ok := ParseAmount(t); ok {
				f.amounts = append(f.amounts, a)
			} else if s, isStr := t.(string); !isStr || strings.TrimSpace(s) != "" {
				f.amountInvalid = true
			}
		}
		if recipientKeys[k] {
			f.hasRecipientKy = true
			if s, ok := t.(string); ok {
				f.recipients = append(f.recipients, parseAddresses(s)...)
			}
		}
		if s, ok := t.(string); ok {
			if textKeys[k] {
				low := strings.ToLower(s)
				for _, w := range legalWords {
					if strings.Contains(low, w) {
						f.categories["legal"] = true
						break
					}
				}
				for _, w := range financialWords {
					if strings.Contains(low, w) {
						f.categories["financial"] = true
						break
					}
				}
			}
			if categoryKeys[k] {
				for _, c := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == ',' || r == ';' || r == '|' }) {
					f.categories[strings.TrimSpace(c)] = true
				}
			}
		}
	}
}

var (
	plainAmountRe = regexp.MustCompile(`^\d+(\.\d+)?$`)
	groupedRe     = regexp.MustCompile(`^\d{1,3}(,\d{3})+(\.\d+)?$`)
	suffixRe      = regexp.MustCompile(`^(\d+(\.\d+)?)\s*([km])$`)
)

// ParseAmount converts numbers and strings like "$12,000.50", "10k", "USD 5000"
// into a float. Negative, NaN, Inf and ambiguous formats return ok=false.
func ParseAmount(v any) (float64, bool) {
	var f float64
	switch t := v.(type) {
	case float64:
		f = t
	case float32:
		f = float64(t)
	case int:
		f = float64(t)
	case int32:
		f = float64(t)
	case int64:
		f = float64(t)
	case uint:
		f = float64(t)
	case uint64:
		f = float64(t)
	case json.Number:
		x, err := t.Float64()
		if err != nil {
			return 0, false
		}
		f = x
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		for _, p := range []string{"usd", "us$", "$", "eur", "€", "mxn", "cop", "pen", "clp", "ars"} {
			s = strings.TrimSpace(strings.TrimPrefix(s, p))
			s = strings.TrimSpace(strings.TrimSuffix(s, p))
		}
		switch {
		case plainAmountRe.MatchString(s):
			x, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return 0, false
			}
			f = x
		case groupedRe.MatchString(s):
			x, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
			if err != nil {
				return 0, false
			}
			f = x
		case suffixRe.MatchString(s):
			m := suffixRe.FindStringSubmatch(s)
			x, err := strconv.ParseFloat(m[1], 64)
			if err != nil {
				return 0, false
			}
			if m[3] == "k" {
				x *= 1e3
			} else {
				x *= 1e6
			}
			f = x
		default:
			return 0, false
		}
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0, false
	}
	return f, true
}

func parseAddresses(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if list, err := mail.ParseAddressList(s); err == nil {
		out := make([]string, 0, len(list))
		for _, a := range list {
			out = append(out, strings.ToLower(a.Address))
		}
		return out
	}
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if a, err := mail.ParseAddress(part); err == nil {
			out = append(out, strings.ToLower(a.Address))
		} else {
			out = append(out, strings.ToLower(part)) // unparseable: kept raw, will not be "known"
		}
	}
	return out
}

func (f facts) maxAmount() (float64, bool) {
	if len(f.amounts) == 0 {
		return 0, false
	}
	m := f.amounts[0]
	for _, a := range f.amounts {
		if a > m {
			m = a
		}
	}
	return m, true
}

func (f facts) hasCategory(cs []string) bool {
	for _, c := range cs {
		if f.categories[strings.ToLower(strings.TrimSpace(c))] {
			return true
		}
	}
	return false
}

// Recipients returns the normalized recipient addresses found in args.
func Recipients(args map[string]any) []string { return analyze(args).recipients }

// Categories returns the detected categories (declared or keyword-detected).
func Categories(args map[string]any) []string {
	f := analyze(args)
	out := make([]string, 0, len(f.categories))
	for c := range f.categories {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// MaxAmount returns the highest valid amount in args.
func MaxAmount(args map[string]any) (float64, bool) { return analyze(args).maxAmount() }
