package drive

import (
	"archive/zip"
	"bytes"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Limits of names and paths.
const (
	MaxNameRunes = 120
	MaxPathRunes = 900
	MaxDepth     = 12
	MaxTags      = 20
	MaxTagRunes  = 40
	MaxMessage   = 300
)

const forbiddenNameChars = `/\:*?"<>|` + "\x00"

// CleanName validates and normalizes (NFC) one path segment.
func CleanName(s string) (string, error) {
	s = norm.NFC.String(s)
	if s == "" {
		return "", invalid("name_invalid", "the name is empty")
	}
	if utf8.RuneCountInString(s) > MaxNameRunes {
		return "", invalid("name_invalid", "the name is longer than %d characters", MaxNameRunes)
	}
	if !utf8.ValidString(s) {
		return "", invalid("name_invalid", "the name is not valid UTF-8")
	}
	if s != strings.TrimSpace(s) || strings.HasSuffix(s, ".") {
		return "", invalid("name_invalid", "the name cannot start or end with spaces or end with a dot")
	}
	if strings.Trim(s, ".") == "" {
		return "", invalid("name_invalid", "the name cannot be only dots")
	}
	for _, r := range s {
		switch {
		case strings.ContainsRune(forbiddenNameChars, r):
			return "", invalid("name_invalid", "the name contains a forbidden character")
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == 0x2028, r == 0x2029:
			// control, zero-width and bidi override characters hide what a name really says
			return "", invalid("name_invalid", "the name contains a control or invisible character")
		}
	}
	return s, nil
}

// Segments validates an absolute path ("/", "/Projects/x") and returns its
// normalized segments. Traversal ("..", "."), empty segments, relative paths,
// backslashes and over-long or over-deep paths are refused. Paths are never
// URL-decoded here: "%2e%2e" is just a (valid) literal name.
func Segments(p string) ([]string, error) {
	p = norm.NFC.String(p)
	if !strings.HasPrefix(p, "/") {
		return nil, invalid("path_invalid", "the path must be absolute (start with /)")
	}
	if utf8.RuneCountInString(p) > MaxPathRunes {
		return nil, invalid("path_invalid", "the path is longer than %d characters", MaxPathRunes)
	}
	if p == "/" {
		return nil, nil
	}
	p = strings.TrimSuffix(p, "/")
	parts := strings.Split(p[1:], "/")
	if len(parts) > MaxDepth {
		return nil, invalid("path_invalid", "the path is deeper than %d levels", MaxDepth)
	}
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		if s == "." || s == ".." {
			return nil, invalid("path_invalid", "path traversal is not allowed")
		}
		n, err := CleanName(s)
		if err != nil {
			return nil, invalid("path_invalid", "%s", err.(*Error).Msg)
		}
		out = append(out, n)
	}
	return out, nil
}

// JoinPath builds "/a/b" from segments ("/" for none).
func JoinPath(segs ...string) string {
	if len(segs) == 0 {
		return "/"
	}
	return "/" + strings.Join(segs, "/")
}

// CleanPath normalizes a path.
func CleanPath(p string) (string, error) {
	s, err := Segments(p)
	if err != nil {
		return "", err
	}
	return JoinPath(s...), nil
}

// SplitFile splits a file path into its folder path and file name.
func SplitFile(p string) (folder, name string, err error) {
	s, err := Segments(p)
	if err != nil {
		return "", "", err
	}
	if len(s) < 2 { // a file must live inside a folder (never in the virtual root)
		return "", "", invalid("path_invalid", "a file must be inside a folder such as /Shared")
	}
	return JoinPath(s[:len(s)-1]...), s[len(s)-1], nil
}

// rootKind maps a top-level name to its kind ("" = not a well-known root).
func rootKind(name string) string {
	switch name {
	case RootProjects:
		return "projects"
	case RootClients:
		return "clients"
	case RootAgents:
		return "agents"
	case RootShared:
		return "shared"
	}
	return ""
}

// IsUnder reports whether path is prefix or below it (both normalized).
func IsUnder(p, prefix string) bool {
	return p == prefix || prefix == "/" || strings.HasPrefix(p, strings.TrimSuffix(prefix, "/")+"/")
}

// ParentPath returns the parent of a normalized path ("/" for top level).
func ParentPath(p string) string {
	d := path.Dir(p)
	if d == "." {
		return "/"
	}
	return d
}

// ---- file types ----

// Content kinds of an allowed file type.
const (
	kindText   = "text"
	kindBinary = "binary"
)

type fileType struct {
	mime    string
	kind    string
	inline  bool // safe to show inline (served with a sandbox CSP and nosniff)
	extract string
}

// allowed is the extension allowlist. There is deliberately no html, svg, zip,
// executable or macro-enabled office type: the drive stores documents, not
// things a browser or an OS would run. Types are decided by extension; the
// content must then match (CheckContent), the client's Content-Type is ignored.
var allowed = map[string]fileType{
	".txt": {"text/plain", kindText, false, "text"}, ".md": {"text/markdown", kindText, false, "text"},
	".markdown": {"text/markdown", kindText, false, "text"}, ".csv": {"text/csv", kindText, false, "text"},
	".tsv": {"text/tab-separated-values", kindText, false, "text"}, ".json": {"application/json", kindText, false, "text"},
	".jsonl": {"application/x-ndjson", kindText, false, "text"}, ".yaml": {"application/yaml", kindText, false, "text"},
	".yml": {"application/yaml", kindText, false, "text"}, ".xml": {"application/xml", kindText, false, "text"},
	".log": {"text/plain", kindText, false, "text"}, ".ini": {"text/plain", kindText, false, "text"},
	".toml": {"text/plain", kindText, false, "text"}, ".tex": {"text/plain", kindText, false, "text"},
	".py": {"text/plain", kindText, false, "text"}, ".go": {"text/plain", kindText, false, "text"},
	".js": {"text/plain", kindText, false, "text"}, ".ts": {"text/plain", kindText, false, "text"},
	".tsx": {"text/plain", kindText, false, "text"}, ".jsx": {"text/plain", kindText, false, "text"},
	".sql": {"text/plain", kindText, false, "text"}, ".sh": {"text/plain", kindText, false, "text"},
	".css": {"text/plain", kindText, false, "text"}, ".java": {"text/plain", kindText, false, "text"},
	".rb": {"text/plain", kindText, false, "text"}, ".rs": {"text/plain", kindText, false, "text"},
	".c": {"text/plain", kindText, false, "text"}, ".h": {"text/plain", kindText, false, "text"},
	".cpp": {"text/plain", kindText, false, "text"}, ".cs": {"text/plain", kindText, false, "text"},
	".php": {"text/plain", kindText, false, "text"},

	".xlsx": {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", kindBinary, false, "table"},
	".docx": {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", kindBinary, false, "docx"},
	".pptx": {"application/vnd.openxmlformats-officedocument.presentationml.presentation", kindBinary, false, "none"},
	".pdf":  {"application/pdf", kindBinary, true, "none"},
	".png":  {"image/png", kindBinary, true, "none"}, ".jpg": {"image/jpeg", kindBinary, true, "none"},
	".jpeg": {"image/jpeg", kindBinary, true, "none"}, ".gif": {"image/gif", kindBinary, true, "none"},
	".webp": {"image/webp", kindBinary, true, "none"},
}

// TypeOf returns the type of a file name by extension.
func TypeOf(name string) (fileType, bool) {
	t, ok := allowed[strings.ToLower(path.Ext(name))]
	return t, ok
}

// IsTextName reports whether the name is an allowed plain-text type (the only
// kind agents can write).
func IsTextName(name string) bool {
	t, ok := TypeOf(name)
	return ok && t.kind == kindText
}

// MimeOf returns the stored mime of an allowed name.
func MimeOf(name string) string { t, _ := TypeOf(name); return t.mime }

// CheckContent verifies that the bytes are what the extension claims.
func CheckContent(name string, data []byte) error {
	t, ok := TypeOf(name)
	if !ok {
		return errf(ErrUnsupportedType, "unsupported_type", "files of this type are not allowed (extension %q)", strings.ToLower(path.Ext(name)))
	}
	bad := func() error {
		return invalid("content_mismatch", "the content does not match the file type %s", path.Ext(name))
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".pdf":
		if !bytes.HasPrefix(data, []byte("%PDF-")) {
			return bad()
		}
	case ".png":
		if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
			return bad()
		}
	case ".jpg", ".jpeg":
		if !bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}) {
			return bad()
		}
	case ".gif":
		if !bytes.HasPrefix(data, []byte("GIF8")) {
			return bad()
		}
	case ".webp":
		if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
			return bad()
		}
	case ".docx", ".xlsx", ".pptx":
		return checkOOXML(strings.ToLower(path.Ext(name)), data)
	default:
		if t.kind == kindText {
			if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
				return invalid("content_mismatch", "a text file must be valid UTF-8 without NUL bytes")
			}
		}
	}
	return nil
}

func checkOOXML(ext string, data []byte) error {
	bad := func() error { return invalid("content_mismatch", "the content is not a valid %s file", ext[1:]) }
	if !bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		return bad()
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(zr.File) > 5000 {
		return bad()
	}
	need := map[string]string{".docx": "word/document.xml", ".xlsx": "xl/workbook.xml", ".pptx": "ppt/presentation.xml"}[ext]
	have := map[string]bool{}
	for _, f := range zr.File {
		have[f.Name] = true
	}
	if !have["[Content_Types].xml"] || !have[need] {
		return bad()
	}
	return nil
}

// CleanTags validates and normalizes tags.
func CleanTags(tags []string) ([]string, error) {
	if len(tags) > MaxTags {
		return nil, invalid("tags_invalid", "at most %d tags", MaxTags)
	}
	seen := map[string]bool{}
	out := []string{}
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(norm.NFC.String(t)))
		if t == "" || utf8.RuneCountInString(t) > MaxTagRunes {
			return nil, invalid("tags_invalid", "a tag is empty or longer than %d characters", MaxTagRunes)
		}
		for _, r := range t {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				return nil, invalid("tags_invalid", "a tag contains a control character")
			}
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out, nil
}
