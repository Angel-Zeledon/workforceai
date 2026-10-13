package officedoc

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

// Precise edits (docs/architecture/agent-edit.md). An edit is a short list of
// operations applied to the CURRENT bytes of a file, all or none: the caller
// stores the result as a new version only when every operation succeeded.
//
//	text (md, txt, csv, json, code): replace, insert_after, insert_before, delete, append
//	docx: replace, set_heading, append_paragraph   (edited in place in word/document.xml)
//	xlsx: set_cell, add_row, rename_sheet          (workbook opened and written back)

// Limits of one edit call.
const (
	MaxEditOps       = 50
	MaxEditArgRunes  = 200_000 // all strings of all operations together
	MaxEditOneRunes  = 20_000  // one string of one operation
	MaxEditTextBytes = 2 << 20 // a text file bigger than this is not edited this way
	MaxEditPattern   = 500     // regex source length
	maxEditMatches   = 10_000
)

// Stable codes of an edit refusal (the agent corrects the call from them).
const (
	CodeInvalidArgs   = "invalid_args"
	CodeNotFound      = "edit_not_found"      // the old/anchor text does not exist
	CodeAmbiguous     = "edit_ambiguous"      // it exists more times than expected
	CodeCountMismatch = "edit_count_mismatch" // it exists fewer times than expected
	CodeInvalidResult = "edit_invalid_result" // the edit would break the file (json, xml)
	CodeUnsupported   = "unsupported_type"
	CodeTooLarge      = "file_too_large"
)

// EditError is a refusal with a stable code. Op is the 1-based index of the
// operation that failed (0: the call as a whole) and Matches how many times the
// searched text was found (so the agent can retry with more context).
type EditError struct {
	Code    string
	Msg     string
	Op      int
	Matches int
}

func (e *EditError) Error() string { return e.Code + ": " + e.Msg }

func editBad(op int, format string, a ...any) *EditError {
	return &EditError{Code: CodeInvalidArgs, Op: op, Msg: fmt.Sprintf(format, a...)}
}

// Op is one decoded operation: a flat map of its arguments.
type Op map[string]any

// ParseOps decodes the ops argument (a JSON array, or a string holding one) and
// enforces the size caps. It never inspects file content.
func ParseOps(raw any) ([]Op, *EditError) {
	var list []any
	switch v := raw.(type) {
	case nil:
		return nil, editBad(0, "ops is required: a list of operations")
	case string:
		if err := json.Unmarshal([]byte(v), &list); err != nil {
			return nil, editBad(0, "ops must be a list of operations")
		}
	case []any:
		list = v
	case []map[string]any:
		for _, m := range v {
			list = append(list, m)
		}
	default:
		return nil, editBad(0, "ops must be a list of operations")
	}
	if len(list) == 0 {
		return nil, editBad(0, "ops is empty")
	}
	if len(list) > MaxEditOps {
		return nil, editBad(0, "at most %d operations per edit (got %d): split the work into several edits", MaxEditOps, len(list))
	}
	total := 0
	ops := make([]Op, 0, len(list))
	for i, x := range list {
		m, ok := x.(map[string]any)
		if !ok {
			return nil, editBad(i+1, "each operation must be an object with an op field")
		}
		for k, v := range m {
			if s, ok := v.(string); ok {
				n := utf8.RuneCountInString(s)
				if n > MaxEditOneRunes {
					return nil, editBad(i+1, "%s is longer than %d characters", k, MaxEditOneRunes)
				}
				total += n
			}
			if arr, ok := v.([]any); ok { // add_row values
				for _, e := range arr {
					if s, ok := e.(string); ok {
						total += utf8.RuneCountInString(s)
					}
				}
			}
		}
		if total > MaxEditArgRunes {
			return nil, editBad(i+1, "the operations together exceed %d characters", MaxEditArgRunes)
		}
		ops = append(ops, Op(m))
	}
	return ops, nil
}

// EditKind says how a file name is edited: "text", "docx", "xlsx" or "" (not editable).
func EditKind(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".docx":
		return FormatDOCX
	case ".xlsx":
		return FormatXLSX
	case ".pdf", ".pptx", ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return ""
	}
	return "text"
}

// Edit applies ops to the bytes of the file called name. typeOK tells whether
// the extension is an allowed text type (the drive decides; pass true for
// text-like names). It returns the new bytes; the input is never modified.
func Edit(name string, data []byte, ops []Op) ([]byte, *EditError) {
	switch EditKind(name) {
	case FormatDOCX:
		return editDOCX(data, ops)
	case FormatXLSX:
		return editXLSX(data, ops)
	case "text":
		if len(data) > MaxEditTextBytes {
			return nil, &EditError{Code: CodeTooLarge, Msg: fmt.Sprintf("the file is larger than %d MB: too large to edit with operations", MaxEditTextBytes>>20)}
		}
		if !utf8.Valid(data) {
			return nil, &EditError{Code: CodeUnsupported, Msg: "the file is not valid UTF-8 text"}
		}
		out, err := editText(string(data), ops)
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(path.Ext(name), ".json") && json.Valid(data) && !json.Valid([]byte(out)) {
			return nil, &EditError{Code: CodeInvalidResult, Msg: "the edit would leave the JSON file invalid; adjust the operations"}
		}
		return []byte(out), nil
	}
	return nil, &EditError{Code: CodeUnsupported, Msg: "this file type cannot be edited with operations (text, docx and xlsx can)"}
}

// ---- argument helpers ----

func (o Op) str(k string) (string, bool) {
	s, ok := o[k].(string)
	return s, ok
}

// expected reads the "count" argument: a positive integer (default 1) or "all" (-1).
func (o Op) expected(i int) (int, *EditError) {
	v, ok := o["count"]
	if !ok || v == nil {
		return 1, nil
	}
	switch c := v.(type) {
	case string:
		if strings.EqualFold(c, "all") {
			return -1, nil
		}
	case float64:
		if c >= 1 && c == float64(int(c)) && c <= maxEditMatches {
			return int(c), nil
		}
	case int:
		if c >= 1 && c <= maxEditMatches {
			return c, nil
		}
	}
	return 0, editBad(i, "count must be a positive integer or \"all\"")
}

// checkCount compares the matches found with the expected number.
func checkCount(i, found, want int, what string) *EditError {
	switch {
	case found == 0:
		return &EditError{Code: CodeNotFound, Op: i, Matches: 0,
			Msg: fmt.Sprintf("op %d: the %s text was not found (0 matches); read the file again and quote the exact text", i, what)}
	case want > 0 && found > want:
		return &EditError{Code: CodeAmbiguous, Op: i, Matches: found,
			Msg: fmt.Sprintf("op %d: the %s text was found %d times, expected %d; quote more surrounding text to make it unique or set count", i, what, found, want)}
	case want > 0 && found < want:
		return &EditError{Code: CodeCountMismatch, Op: i, Matches: found,
			Msg: fmt.Sprintf("op %d: the %s text was found %d times, expected %d", i, what, found, want)}
	}
	return nil
}
