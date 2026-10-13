package officedoc

import (
	"regexp"
	"strings"
)

// editText applies the operations in order to s. Line endings: a CRLF file is
// matched with LF and written back with CRLF, so the agent quotes what it read.
func editText(s string, ops []Op) (string, *EditError) {
	crlf := strings.Contains(s, "\r\n")
	if crlf {
		s = strings.ReplaceAll(s, "\r\n", "\n")
	}
	for n, op := range ops {
		i := n + 1
		kind, _ := op.str("op")
		var err *EditError
		switch kind {
		case "replace":
			s, err = textReplace(s, op, i)
		case "insert_after", "insert_before":
			s, err = textInsert(s, op, i, kind == "insert_after")
		case "delete":
			s, err = textDelete(s, op, i)
		case "append":
			add, ok := op.str("text")
			if !ok || add == "" {
				return "", editBad(i, "append needs text")
			}
			add = strings.ReplaceAll(add, "\r\n", "\n")
			if s != "" && !strings.HasSuffix(s, "\n") && !strings.HasPrefix(add, "\n") {
				s += "\n"
			}
			s += add
		default:
			return "", editBad(i, "unknown op %q for a text file (replace, insert_after, insert_before, delete, append)", kind)
		}
		if err != nil {
			return "", err
		}
	}
	if crlf {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	return s, nil
}

func lf(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func textReplace(s string, op Op, i int) (string, *EditError) {
	old, ok := op.str("old")
	if !ok || old == "" {
		return "", editBad(i, "replace needs a non-empty old text")
	}
	nw, ok := op.str("new")
	if !ok {
		return "", editBad(i, "replace needs new (use \"\" to remove the text)")
	}
	old, nw = lf(old), lf(nw)
	want, err := op.expected(i)
	if err != nil {
		return "", err
	}
	useRe, _ := op["regex"].(bool)
	if !useRe {
		found := strings.Count(s, old)
		if e := checkCount(i, found, want, "old"); e != nil {
			return "", e
		}
		return strings.ReplaceAll(s, old, nw), nil
	}
	if len(old) > MaxEditPattern {
		return "", editBad(i, "the regex is longer than %d characters", MaxEditPattern)
	}
	re, e := regexp.Compile(old) // RE2: linear time, no backtracking bombs
	if e != nil {
		return "", editBad(i, "invalid regex: %s", shorten(e.Error(), 120))
	}
	if re.MatchString("") {
		return "", editBad(i, "the regex matches the empty string: make it more specific")
	}
	found := len(re.FindAllStringIndex(s, maxEditMatches+1))
	if found > maxEditMatches {
		return "", editBad(i, "the regex matches more than %d times", maxEditMatches)
	}
	if e := checkCount(i, found, want, "old"); e != nil {
		return "", e
	}
	return re.ReplaceAllString(s, nw), nil
}

func textInsert(s string, op Op, i int, after bool) (string, *EditError) {
	anchor, ok := op.str("anchor")
	if !ok || anchor == "" {
		return "", editBad(i, "insert needs a non-empty anchor text")
	}
	add, ok := op.str("text")
	if !ok || add == "" {
		return "", editBad(i, "insert needs text")
	}
	anchor, add = lf(anchor), lf(add)
	want, err := op.expected(i)
	if err != nil {
		return "", err
	}
	found := strings.Count(s, anchor)
	if e := checkCount(i, found, want, "anchor"); e != nil {
		return "", e
	}
	if after {
		return strings.ReplaceAll(s, anchor, anchor+add), nil
	}
	return strings.ReplaceAll(s, anchor, add+anchor), nil
}

func textDelete(s string, op Op, i int) (string, *EditError) {
	t, ok := op.str("text")
	if !ok || t == "" {
		return "", editBad(i, "delete needs a non-empty text")
	}
	t = lf(t)
	want, err := op.expected(i)
	if err != nil {
		return "", err
	}
	found := strings.Count(s, t)
	if e := checkCount(i, found, want, "text"); e != nil {
		return "", e
	}
	return strings.ReplaceAll(s, t, ""), nil
}
