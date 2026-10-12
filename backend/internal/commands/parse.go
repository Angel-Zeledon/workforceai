package commands

import (
	"regexp"
	"strings"
)

type token struct {
	text       string
	start, end int
}

func tokenize(s string) []token {
	var out []token
	start := -1
	for i, r := range s {
		space := r == ' ' || r == '\t' || r == '\n'
		if space && start >= 0 {
			out = append(out, token{s[start:i], start, i})
			start = -1
		} else if !space && start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, token{s[start:], start, len(s)})
	}
	return out
}

// Split separates "/resumir foo bar" into the command word and the rest. ok is
// false when raw is not a slash command.
func Split(raw string) (word, rest string, ok bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "/") || len(raw) < 2 {
		return "", "", false
	}
	raw = raw[1:]
	i := strings.IndexAny(raw, " \t\n")
	if i < 0 {
		word = raw
	} else {
		word, rest = raw[:i], strings.TrimSpace(raw[i:])
	}
	if !IsWord(word) {
		return "", "", false
	}
	return word, rest, true
}

// ParseOpts customizes ParseArgs.
type ParseOpts struct {
	// IsAgent says whether a bare word names an agent (used by /meeting without a colon).
	IsAgent func(string) bool
}

var enumSynonyms = map[string]string{
	"ver": "view", "mostrar": "view", "list": "view", "show": "view",
	"borrar": "delete", "eliminar": "delete", "clear": "delete", "remove": "delete",
	"olvidar":  "forget",
	"markdown": "md",
}

// NormalizeEnum maps a typed word (either language) to the canonical enum value of arg.
func NormalizeEnum(arg Arg, v string) (string, bool) {
	f := Fold(v)
	if s, ok := enumSynonyms[f]; ok && contains(arg.Enum, s) {
		return s, true
	}
	for _, e := range arg.Enum {
		if Fold(e) == f {
			return e, true
		}
	}
	return "", false
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// ParseArgs assigns the text after the command to its arguments, in order. The
// last variadic argument takes the rest of the line. "@name" is taken as the
// agent argument when it is the first or the last word. Values are not
// validated here (the executor resolves agents, files, ...).
func ParseArgs(c Command, rest string, o ParseOpts) map[string]any {
	out := map[string]any{}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return out
	}
	if c.ID == "meeting" {
		return parseMeeting(rest, o)
	}
	toks := tokenize(rest)
	hasAgent := false
	for _, a := range c.Args {
		if a.Kind == KindAgent && a.Name == "agent" {
			hasAgent = true
		}
	}
	if hasAgent && len(toks) > 0 {
		switch {
		case strings.HasPrefix(toks[0].text, "@") && len(toks[0].text) > 1:
			out["agent"] = strings.TrimPrefix(toks[0].text, "@")
			toks = toks[1:]
		case len(toks) > 1 && strings.HasPrefix(toks[len(toks)-1].text, "@") && len(toks[len(toks)-1].text) > 1:
			out["agent"] = strings.TrimPrefix(toks[len(toks)-1].text, "@")
			toks = toks[:len(toks)-1]
		}
	}
	// the remaining text is cut from the original string so variadic text keeps its spacing
	remaining := func(from int) string {
		if from >= len(toks) {
			return ""
		}
		return strings.TrimSpace(rest[toks[from].start:toks[len(toks)-1].end])
	}
	i := 0
	for _, a := range c.Args {
		if _, done := out[a.Name]; done {
			continue
		}
		if i >= len(toks) {
			break
		}
		switch {
		case a.Variadic && a.Kind == KindAgent:
			var names []any
			for _, t := range toks[i:] {
				names = append(names, strings.TrimPrefix(strings.Trim(t.text, ","), "@"))
			}
			out[a.Name] = names
			i = len(toks)
		case a.Variadic:
			out[a.Name] = remaining(i)
			i = len(toks)
		case a.Kind == KindEnum:
			if v, ok := NormalizeEnum(a, toks[i].text); ok {
				out[a.Name] = v
				i++
			} else if a.Required {
				out[a.Name] = toks[i].text // reported as invalid by the executor
				i++
			}
			// an optional enum that does not match is left for the next argument
		default:
			out[a.Name] = strings.TrimPrefix(toks[i].text, "@")
			i++
		}
	}
	return out
}

func parseMeeting(rest string, o ParseOpts) map[string]any {
	out := map[string]any{}
	clean := func(s string) []any {
		var names []any
		for _, w := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
			w = strings.TrimPrefix(w, "@")
			if w != "" && !strings.EqualFold(w, "y") && !strings.EqualFold(w, "and") {
				names = append(names, w)
			}
		}
		return names
	}
	if i := strings.Index(rest, ":"); i >= 0 {
		if names := clean(rest[:i]); len(names) > 0 {
			out["agents"] = names
		}
		if topic := strings.TrimSpace(rest[i+1:]); topic != "" {
			out["topic"] = topic
		}
		return out
	}
	toks := tokenize(rest)
	n := 0
	var names []any
	for n < len(toks) && o.IsAgent != nil && o.IsAgent(strings.TrimPrefix(strings.Trim(toks[n].text, ","), "@")) {
		names = append(names, strings.TrimPrefix(strings.Trim(toks[n].text, ","), "@"))
		n++
	}
	if len(names) > 0 {
		out["agents"] = names
	}
	if n < len(toks) {
		out["topic"] = strings.TrimSpace(rest[toks[n].start:])
	}
	return out
}

var (
	esHint = regexp.MustCompile(`[áéíóúñ¿¡]|\b(el|la|los|las|de|del|que|para|con|por|una|un|y|en|es|mi|tu|hoy|resumen|cliente|precio|reunion|ventas|contrato)\b`)
	enHint = regexp.MustCompile(`\b(the|and|of|to|for|with|about|is|are|my|your|today|summary|client|price|meeting|sales|contract|please|this|that)\b`)
)

// DetectLang guesses "es" or "en" from free text; "" when it cannot tell.
func DetectLang(text string) string {
	t := strings.ToLower(text)
	es, en := len(esHint.FindAllString(t, -1)), len(enHint.FindAllString(t, -1))
	switch {
	case es > en:
		return "es"
	case en > es:
		return "en"
	}
	return ""
}
