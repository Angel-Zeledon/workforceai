package skills

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Frontmatter is the parsed YAML header of a SKILL.md. The parser understands
// the subset of YAML the Agent Skills format uses (scalars, quoted strings,
// block scalars, inline and block lists, one level of nested map for
// metadata) and nothing else: no anchors, tags, merges or code.
type Frontmatter struct {
	Name          string
	Description   string
	License       string
	Compatibility string
	// AllowedTools is informational: it never grants anything.
	AllowedTools []string
	Metadata     map[string]string
	// Extra keeps other top-level scalar keys so an import/export round trip
	// does not lose them.
	Extra map[string]string
}

var nameRe = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// ValidName reports whether s is a valid skill name.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// SplitSkillMD separates the frontmatter text from the markdown body.
func SplitSkillMD(raw string) (front, body string, err error) {
	raw = strings.TrimPrefix(raw, "\xef\xbb\xbf")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != "---" {
		return "", "", invalid("frontmatter_missing", "SKILL.md must start with a --- YAML frontmatter block")
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			return strings.Join(lines[1:i], "\n"), strings.TrimLeft(strings.Join(lines[i+1:], "\n"), "\n"), nil
		}
	}
	return "", "", invalid("frontmatter_unclosed", "the frontmatter block of SKILL.md is not closed with ---")
}

// ParseSkillMD parses and validates SKILL.md: frontmatter and body.
func ParseSkillMD(raw string) (Frontmatter, string, error) {
	if !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) {
		return Frontmatter{}, "", invalid("not_text", "SKILL.md must be valid UTF-8 text")
	}
	front, body, err := SplitSkillMD(raw)
	if err != nil {
		return Frontmatter{}, "", err
	}
	fm, err := parseFront(front)
	if err != nil {
		return Frontmatter{}, "", err
	}
	if !ValidName(fm.Name) {
		return Frontmatter{}, "", invalid("name_invalid", "name must match ^[a-z0-9-]{1,64}$")
	}
	if strings.TrimSpace(fm.Description) == "" {
		return Frontmatter{}, "", invalid("description_required", "description is required")
	}
	if utf8.RuneCountInString(fm.Description) > MaxDescription {
		return Frontmatter{}, "", invalid("description_too_long", "description is longer than %d characters", MaxDescription)
	}
	if len(fm.AllowedTools) > MaxAllowedTools {
		return Frontmatter{}, "", invalid("too_many_allowed_tools", "allowed-tools lists more than %d entries", MaxAllowedTools)
	}
	if strings.TrimSpace(body) == "" {
		return Frontmatter{}, "", invalid("instructions_required", "SKILL.md has no instructions after the frontmatter")
	}
	return fm, body, nil
}

type fline struct {
	indent int
	text   string
}

func parseFront(front string) (Frontmatter, error) {
	fm := Frontmatter{Metadata: map[string]string{}, Extra: map[string]string{}}
	var lines []fline
	for _, l := range strings.Split(front, "\n") {
		if strings.Contains(l, "\t") && strings.TrimLeft(l, " ") != strings.TrimLeft(l, " \t") {
			return fm, invalid("frontmatter_invalid", "tabs are not allowed in the frontmatter indentation")
		}
		lines = append(lines, fline{indent: len(l) - len(strings.TrimLeft(l, " ")), text: strings.TrimRight(l, " \t")})
	}
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		t := strings.TrimSpace(ln.text)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if ln.indent != 0 {
			return fm, invalid("frontmatter_invalid", "unexpected indentation: %q", clip(t))
		}
		key, val, ok := strings.Cut(ln.text, ":")
		if !ok || strings.TrimSpace(key) == "" || strings.ContainsAny(key, " \t") {
			return fm, invalid("frontmatter_invalid", "expected key: value, got %q", clip(t))
		}
		val = strings.TrimSpace(val)
		// children: the following lines indented more than the key
		var kids []fline
		for i+1 < len(lines) && (strings.TrimSpace(lines[i+1].text) == "" || lines[i+1].indent > 0) {
			i++
			kids = append(kids, lines[i])
		}
		switch {
		case val == "" && len(kids) > 0:
			if err := assignBlock(&fm, key, kids); err != nil {
				return fm, err
			}
		case strings.HasPrefix(val, ">") || strings.HasPrefix(val, "|"):
			if err := assignScalar(&fm, key, blockScalar(val, kids)); err != nil {
				return fm, err
			}
		case strings.HasPrefix(val, "["):
			items, err := inlineList(val)
			if err != nil {
				return fm, err
			}
			if err := assignList(&fm, key, items); err != nil {
				return fm, err
			}
		default:
			if len(kids) > 0 { // plain multi-line scalar
				parts := []string{unquoteCut(val)}
				for _, k := range kids {
					parts = append(parts, strings.TrimSpace(k.text))
				}
				val = strings.Join(parts, " ")
				if err := assignScalar(&fm, key, strings.TrimSpace(val)); err != nil {
					return fm, err
				}
				continue
			}
			s, err := scalar(val)
			if err != nil {
				return fm, err
			}
			if err := assignScalar(&fm, key, s); err != nil {
				return fm, err
			}
		}
	}
	return fm, nil
}

func clip(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

func unquoteCut(v string) string {
	s, err := scalar(v)
	if err != nil {
		return v
	}
	return s
}

// scalar reads a plain, 'single' or "double" quoted scalar (trailing # comments of plain scalars are dropped).
func scalar(v string) (string, error) {
	v = strings.TrimSpace(v)
	switch {
	case strings.HasPrefix(v, `"`):
		if len(v) < 2 || !strings.HasSuffix(v, `"`) {
			return "", invalid("frontmatter_invalid", "unterminated quoted string")
		}
		return unescapeDouble(v[1 : len(v)-1]), nil
	case strings.HasPrefix(v, "'"):
		if len(v) < 2 || !strings.HasSuffix(v, "'") {
			return "", invalid("frontmatter_invalid", "unterminated quoted string")
		}
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'"), nil
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	if strings.HasPrefix(v, "&") || strings.HasPrefix(v, "*") || strings.HasPrefix(v, "!") || strings.HasPrefix(v, "{") {
		return "", invalid("frontmatter_invalid", "YAML anchors, tags and flow maps are not supported")
	}
	return v, nil
}

func unescapeDouble(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i == len(s)-1 {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case '"', '\\', '/':
			b.WriteByte(s[i])
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func blockScalar(head string, kids []fline) string {
	folded := strings.HasPrefix(head, ">")
	strip := strings.Contains(head, "-")
	minIndent := -1
	for _, k := range kids {
		if strings.TrimSpace(k.text) == "" {
			continue
		}
		if minIndent < 0 || k.indent < minIndent {
			minIndent = k.indent
		}
	}
	var rows []string
	for _, k := range kids {
		if strings.TrimSpace(k.text) == "" {
			rows = append(rows, "")
			continue
		}
		rows = append(rows, k.text[minIndent:])
	}
	for len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	var out string
	if folded {
		var parts []string
		para := []string{}
		for _, r := range rows {
			if r == "" {
				parts = append(parts, strings.Join(para, " "))
				para = nil
				continue
			}
			para = append(para, strings.TrimSpace(r))
		}
		parts = append(parts, strings.Join(para, " "))
		out = strings.Join(parts, "\n")
	} else {
		out = strings.Join(rows, "\n")
	}
	if !strip && out != "" {
		out += "\n"
	}
	return strings.TrimRight(out, "\n")
}

func inlineList(v string) ([]string, error) {
	if !strings.HasSuffix(v, "]") {
		return nil, invalid("frontmatter_invalid", "unterminated inline list")
	}
	inner := strings.TrimSpace(v[1 : len(v)-1])
	if inner == "" {
		return nil, nil
	}
	var out []string
	var cur strings.Builder
	var q byte
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		switch {
		case q != 0:
			cur.WriteByte(c)
			if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
			cur.WriteByte(c)
		case c == ',':
			s, err := scalar(cur.String())
			if err != nil {
				return nil, err
			}
			out = append(out, s)
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	s, err := scalar(cur.String())
	if err != nil {
		return nil, err
	}
	return append(out, s), nil
}

func assignBlock(fm *Frontmatter, key string, kids []fline) error {
	var rows []fline
	for _, k := range kids {
		if strings.TrimSpace(k.text) != "" && !strings.HasPrefix(strings.TrimSpace(k.text), "#") {
			rows = append(rows, k)
		}
	}
	if len(rows) == 0 {
		return nil
	}
	if strings.HasPrefix(strings.TrimSpace(rows[0].text), "- ") || strings.TrimSpace(rows[0].text) == "-" {
		var items []string
		for _, r := range rows {
			t := strings.TrimSpace(r.text)
			if !strings.HasPrefix(t, "-") {
				return invalid("frontmatter_invalid", "mixed list and map under %q", key)
			}
			s, err := scalar(strings.TrimSpace(strings.TrimPrefix(t, "-")))
			if err != nil {
				return err
			}
			items = append(items, s)
		}
		return assignList(fm, key, items)
	}
	if key != "metadata" {
		return nil // nested maps of unknown keys are ignored (not carried)
	}
	for _, r := range rows {
		k, v, ok := strings.Cut(strings.TrimSpace(r.text), ":")
		if !ok || strings.TrimSpace(k) == "" {
			return invalid("frontmatter_invalid", "metadata entries must be key: value")
		}
		s, err := scalar(v)
		if err != nil {
			return err
		}
		if len(fm.Metadata) >= 30 {
			return invalid("frontmatter_invalid", "too many metadata entries")
		}
		fm.Metadata[strings.TrimSpace(k)] = s
	}
	return nil
}

func assignList(fm *Frontmatter, key string, items []string) error {
	switch key {
	case "allowed-tools", "allowed_tools":
		for _, it := range items {
			fm.AllowedTools = append(fm.AllowedTools, splitTools(it)...)
		}
	}
	return nil
}

func splitTools(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' || r == '\n' }) {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func assignScalar(fm *Frontmatter, key, val string) error {
	switch key {
	case "name":
		fm.Name = val
	case "description":
		fm.Description = strings.TrimSpace(val)
	case "license":
		fm.License = val
	case "compatibility":
		fm.Compatibility = val
	case "allowed-tools", "allowed_tools":
		fm.AllowedTools = append(fm.AllowedTools, splitTools(val)...)
	case "metadata":
		// "metadata: {}" or empty
	default:
		if len(fm.Extra) < 20 && len(val) <= 300 {
			fm.Extra[key] = val
		}
	}
	return nil
}

// yamlString quotes a scalar when needed.
func yamlString(s string) string {
	plain := s != "" && !strings.ContainsAny(s, ":#\"'\n\\[]{}&*!|>%@`,") && s == strings.TrimSpace(s) &&
		!strings.HasPrefix(s, "-") && !strings.HasPrefix(s, "?")
	switch strings.ToLower(s) {
	case "true", "false", "null", "yes", "no", "on", "off", "~":
		plain = false
	}
	if plain {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

// RenderSkillMD writes the canonical SKILL.md of a frontmatter and a body.
func RenderSkillMD(fm Frontmatter, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + yamlString(fm.Name) + "\n")
	b.WriteString("description: " + yamlString(strings.Join(strings.Fields(fm.Description), " ")) + "\n")
	if fm.License != "" {
		b.WriteString("license: " + yamlString(fm.License) + "\n")
	}
	if fm.Compatibility != "" {
		b.WriteString("compatibility: " + yamlString(fm.Compatibility) + "\n")
	}
	if len(fm.AllowedTools) > 0 {
		b.WriteString("allowed-tools: " + yamlString(strings.Join(fm.AllowedTools, " ")) + "\n")
	}
	if len(fm.Metadata) > 0 {
		b.WriteString("metadata:\n")
		for _, k := range sortedKeys(fm.Metadata) {
			b.WriteString("  " + k + ": " + yamlString(fm.Metadata[k]) + "\n")
		}
	}
	for _, k := range sortedKeys(fm.Extra) {
		b.WriteString(k + ": " + yamlString(fm.Extra[k]) + "\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n")
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
