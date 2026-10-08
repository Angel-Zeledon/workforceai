package artifacts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"aiworkforce/backend/internal/domain"
)

// Content validation (docs/architecture/agent-workspaces.md sec. 7.2 and 7.3):
// the content of an artifact is data in a closed format. Anything outside the
// whitelist rejects the whole write; nothing is "cleaned" silently.

const (
	maxDepth      = 64
	maxBytesDoc   = 1 << 20
	maxBytesOther = 5 << 20
	maxSheets     = 50
	maxCells      = 200000
	maxBlocks     = 5000
	maxEmbeds     = 20
	maxRows       = 50000
	maxColumns    = 100
	maxCards      = 2000
	maxBoardCols  = 50
	maxTitle      = 200
)

func itoa(i int) string { return strconv.Itoa(i) }

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrInvalid, fmt.Sprintf(format, a...))
}

func contentHash(raw []byte) string {
	h := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(h[:])
}

// parseContent validates raw as the canonical content of kind and returns it
// compacted, together with its decoded form.
func parseContent(kind Kind, raw json.RawMessage) (json.RawMessage, map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil, invalid("content is required")
	}
	limit := maxBytesOther
	if kind == KindDoc {
		limit = maxBytesDoc
	}
	if len(raw) > limit {
		return nil, nil, invalid("content is too large (%d bytes, limit %d)", len(raw), limit)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil || root == nil {
		return nil, nil, invalid("content must be a JSON object")
	}
	if dec.More() {
		return nil, nil, invalid("content has trailing data")
	}
	if s, _ := root["schema"].(string); s != kind.Schema() {
		return nil, nil, invalid("content.schema must be %q", kind.Schema())
	}
	if depth(root, 0) > maxDepth {
		return nil, nil, invalid("content is nested too deeply")
	}
	var err error
	switch kind {
	case KindSheet:
		err = checkSheet(root)
	case KindDoc:
		err = checkDoc(root)
	case KindTable:
		err = checkTable(root)
	case KindBoard:
		err = checkBoard(root)
	case KindPDF:
		err = checkPDF(root)
	}
	if err != nil {
		return nil, nil, err
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, nil, invalid("content is not valid JSON")
	}
	return buf.Bytes(), root, nil
}

func depth(v any, d int) int {
	if d > maxDepth {
		return d
	}
	best := d
	switch t := v.(type) {
	case map[string]any:
		for _, x := range t {
			best = max(best, depth(x, d+1))
		}
	case []any:
		for _, x := range t {
			best = max(best, depth(x, d+1))
		}
	}
	return best
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func asList(v any) []any         { l, _ := v.([]any); return l }
func asStr(v any) string         { s, _ := v.(string); return s }

// ---- sheet ----

var (
	stringLitRe = regexp.MustCompile(`"(?:[^"]|"")*"`)
	funcCallRe  = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.]*)\s*\(`)
	cellKeyRe   = regexp.MustCompile(`^[A-Z]{1,3}[1-9][0-9]{0,6}$`)
)

// allowedFuncs is the whitelist of the formula guard (math, text, logic, lookup,
// date, financial, statistics). INDIRECT, OFFSET, HYPERLINK, WEBSERVICE, IMPORT*,
// FILTERXML, CALL, EXEC, RTD, DDE... are not in it and are rejected.
var allowedFuncs = func() map[string]bool {
	m := map[string]bool{}
	for _, f := range strings.Fields(`
ABS ACOS ASIN ATAN ATAN2 AVERAGE AVERAGEIF AVERAGEIFS CEILING COS COUNT COUNTA COUNTBLANK COUNTIF COUNTIFS DEGREES EVEN EXP FACT FLOOR INT LN LOG LOG10
MAX MAXIFS MEDIAN MIN MINIFS MOD MODE ODD PI POWER PRODUCT QUOTIENT RADIANS RAND RANDBETWEEN ROUND ROUNDDOWN ROUNDUP SIGN SIN SQRT STDEV STDEVP SUM SUMIF SUMIFS SUMPRODUCT
SUMSQ TAN TRUNC VAR VARP LARGE SMALL RANK PERCENTILE QUARTILE CORREL SLOPE INTERCEPT FORECAST TREND GROWTH
AND OR NOT IF IFS IFERROR IFNA XOR TRUE FALSE SWITCH ISBLANK ISERROR ISNUMBER ISTEXT ISLOGICAL ISNA
CHAR CLEAN CODE CONCAT CONCATENATE EXACT FIND LEFT LEN LOWER MID PROPER REPLACE REPT RIGHT SEARCH SUBSTITUTE T TEXT TEXTJOIN TRIM UPPER VALUE
VLOOKUP HLOOKUP LOOKUP XLOOKUP MATCH XMATCH INDEX CHOOSE ROW ROWS COLUMN COLUMNS TRANSPOSE UNIQUE SORT FILTER
DATE DATEDIF DAY DAYS EDATE EOMONTH HOUR MINUTE MONTH NETWORKDAYS NOW SECOND TIME TODAY WEEKDAY WEEKNUM WORKDAY YEAR YEARFRAC
FV IPMT IRR MIRR NPER NPV PMT PPMT PV RATE XIRR XNPV SLN SYD DB DDB CUMIPMT CUMPRINC EFFECT NOMINAL
AIW_REF`) {
		m[f] = true
	}
	return m
}()

var refArgRe = regexp.MustCompile(`(?i)AIW_REF\s*\(\s*"([^"]+)"\s*,\s*"([^"]+)"`)

// guardFormula applies the whitelist to one formula.
func guardFormula(f string) error {
	if !strings.HasPrefix(f, "=") {
		return invalid("formula %q must start with =", truncate(f, 40))
	}
	if len(f) > 8192 {
		return invalid("formula is too long")
	}
	body := stringLitRe.ReplaceAllString(f, `""`) // literals are data
	if strings.ContainsAny(body, "[]\\") || strings.Contains(strings.ToLower(body), "http:") || strings.Contains(strings.ToLower(body), "https:") {
		return invalid("formula %q contains an external reference", truncate(f, 40))
	}
	for _, m := range funcCallRe.FindAllStringSubmatch(body, -1) {
		if name := strings.ToUpper(m[1]); !allowedFuncs[name] {
			return invalid("function %s is not allowed in formulas", name)
		}
	}
	return nil
}

func checkSheet(root map[string]any) error {
	sheets := asList(root["sheets"])
	if len(sheets) == 0 || len(sheets) > maxSheets {
		return invalid("a sheet artifact needs between 1 and %d sheets", maxSheets)
	}
	cells := 0
	aliases := map[string]bool{}
	for _, im := range asList(root["imports"]) {
		m := asMap(im)
		if asStr(m["alias"]) == "" || asStr(m["artifact_id"]) == "" {
			return invalid("every import needs alias and artifact_id")
		}
		aliases[asStr(m["alias"])] = true
	}
	for _, s := range sheets {
		sh := asMap(s)
		if sh == nil || asStr(sh["name"]) == "" || asStr(sh["id"]) == "" {
			return invalid("every sheet needs id and name")
		}
		for key, c := range asMap(sh["cells"]) {
			if !cellKeyRe.MatchString(key) {
				return invalid("invalid cell address %q", key)
			}
			if cells++; cells > maxCells {
				return invalid("the sheet has too many cells (limit %d)", maxCells)
			}
			cell := asMap(c)
			if cell == nil {
				return invalid("cell %s must be an object", key)
			}
			if f, ok := cell["f"].(string); ok {
				if err := guardFormula(f); err != nil {
					return err
				}
				// AIW_REF may only use a declared import alias.
				for _, m := range refArgRe.FindAllStringSubmatch(f, -1) {
					if !aliases[m[1]] {
						return invalid("AIW_REF uses the undeclared alias %q", m[1])
					}
				}
			}
		}
	}
	return nil
}

// ---- doc ----

var docNodes = map[string]bool{"doc": true, "paragraph": true, "heading": true, "bulletList": true, "orderedList": true, "listItem": true,
	"blockquote": true, "codeBlock": true, "table": true, "tableRow": true, "tableCell": true, "tableHeader": true, "hardBreak": true,
	"clause": true, "embed": true, "image": true, "text": true}

// artifact_link is the mark the editor uses for a reference to another artifact.
var docMarks = map[string]bool{"bold": true, "italic": true, "underline": true, "strike": true, "code": true, "link": true,
	"suggestion": true, "comment": true, "artifact_link": true}

func checkDoc(root map[string]any) error {
	doc := asMap(root["doc"])
	if doc == nil || asStr(doc["type"]) != "doc" {
		return invalid("content.doc must be a ProseMirror document")
	}
	blocks, embeds := 0, 0
	var walk func(n map[string]any) error
	walk = func(n map[string]any) error {
		t := asStr(n["type"])
		if !docNodes[t] {
			return invalid("document node %q is not allowed", t)
		}
		if blocks++; blocks > maxBlocks {
			return invalid("the document has too many blocks (limit %d)", maxBlocks)
		}
		attrs := asMap(n["attrs"])
		switch t {
		case "embed":
			if embeds++; embeds > maxEmbeds {
				return invalid("the document has too many embeds (limit %d)", maxEmbeds)
			}
			if asStr(attrs["artifact_id"]) == "" {
				return invalid("an embed needs artifact_id")
			}
		case "image":
			if src := asStr(attrs["src"]); !strings.HasPrefix(src, "blob:") {
				return invalid("images may only reference an uploaded blob (blob:<id>)")
			}
		}
		for _, m := range asList(n["marks"]) {
			mk := asMap(m)
			mt := asStr(mk["type"])
			if !docMarks[mt] {
				return invalid("document mark %q is not allowed", mt)
			}
			if mt == "link" {
				if err := safeHref(asStr(asMap(mk["attrs"])["href"])); err != nil {
					return err
				}
			}
		}
		for _, c := range asList(n["content"]) {
			cm := asMap(c)
			if cm == nil {
				return invalid("document content must be made of nodes")
			}
			if err := walk(cm); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(doc)
}

// safeHref accepts only http(s) and mailto links (never javascript: or data:).
func safeHref(h string) error {
	u, err := url.Parse(strings.TrimSpace(h))
	if err != nil {
		return invalid("invalid link")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto":
		return nil
	}
	return invalid("links may only be http, https or mailto")
}

// ---- table / board ----

func checkTable(root map[string]any) error {
	cols, rows := asList(root["columns"]), asList(root["rows"])
	if len(cols) > maxColumns || len(rows) > maxRows {
		return invalid("the table is too large (limit %d columns, %d rows)", maxColumns, maxRows)
	}
	keys := map[string]bool{}
	for _, c := range cols {
		k := asStr(asMap(c)["key"])
		if k == "" || keys[k] {
			return invalid("table columns need a unique key")
		}
		keys[k] = true
	}
	ids := map[string]bool{}
	for _, r := range rows {
		id := asStr(asMap(r)["id"])
		if id == "" || ids[id] {
			return invalid("table rows need a unique id")
		}
		ids[id] = true
	}
	return nil
}

func checkBoard(root map[string]any) error {
	cols, cards := asList(root["columns"]), asList(root["cards"])
	if len(cols) > maxBoardCols || len(cards) > maxCards {
		return invalid("the board is too large (limit %d columns, %d cards)", maxBoardCols, maxCards)
	}
	ids := map[string]bool{}
	for _, c := range cards {
		id := asStr(asMap(c)["id"])
		if id == "" || ids[id] {
			return invalid("board cards need a unique id")
		}
		ids[id] = true
	}
	return nil
}

// ---- references between artifacts ----

// ref is one outgoing reference found in a content.
type ref struct {
	To       string
	Relation string
	Alias    string
	Anchor   *Anchor
}

// extractRefs lists what an artifact points at: sheet imports (AIW_REF), doc
// embeds and artifact_link marks, chart sources and board card links.
func extractRefs(kind Kind, root map[string]any) []ref {
	var out []ref
	switch kind {
	case KindSheet:
		anchors := map[string]*Anchor{}
		for _, s := range asList(root["sheets"]) {
			for _, c := range asMap(asMap(s)["cells"]) {
				for _, m := range refArgRe.FindAllStringSubmatch(asStr(asMap(c)["f"]), -1) {
					if _, seen := anchors[m[1]]; !seen {
						anchors[m[1]] = parseAnchor(m[2])
					}
				}
			}
		}
		for _, im := range asList(root["imports"]) {
			m := asMap(im)
			a := asStr(m["alias"])
			out = append(out, ref{To: asStr(m["artifact_id"]), Relation: RelSourceOf, Alias: a, Anchor: anchors[a]})
		}
	case KindDoc:
		var walk func(n map[string]any)
		walk = func(n map[string]any) {
			attrs := asMap(n["attrs"])
			if asStr(n["type"]) == "embed" {
				out = append(out, ref{To: asStr(attrs["artifact_id"]), Relation: RelEmbeds})
			}
			for _, m := range asList(n["marks"]) {
				mk := asMap(m)
				if asStr(mk["type"]) == "artifact_link" {
					ma := asMap(mk["attrs"])
					r := ref{To: asStr(ma["artifact_id"]), Relation: RelRefersTo}
					if an := asMap(ma["anchor"]); an != nil {
						r.Anchor = &Anchor{Sheet: asStr(an["sheet"]), Range: asStr(an["range"]), BID: asStr(an["bid"]), Card: asStr(an["card"])}
					}
					out = append(out, r)
				}
			}
			for _, c := range asList(n["content"]) {
				if cm := asMap(c); cm != nil {
					walk(cm)
				}
			}
		}
		if d := asMap(root["doc"]); d != nil {
			walk(d)
		}
	case KindChart:
		if src := asMap(root["source"]); asStr(src["artifact_id"]) != "" {
			out = append(out, ref{To: asStr(src["artifact_id"]), Relation: RelSourceOf, Anchor: &Anchor{Sheet: asStr(src["sheet"]), Range: asStr(src["range"])}})
		}
	case KindBoard:
		for _, c := range asList(root["cards"]) {
			if l := asMap(asMap(c)["link"]); asStr(l["artifact_id"]) != "" {
				out = append(out, ref{To: asStr(l["artifact_id"]), Relation: RelRefersTo})
			}
		}
	}
	return out
}

func parseAnchor(a string) *Anchor {
	sheet, rng, ok := strings.Cut(a, "!")
	if !ok {
		return &Anchor{Range: a}
	}
	return &Anchor{Sheet: sheet, Range: rng}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
