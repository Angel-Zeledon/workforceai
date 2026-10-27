package inboxes

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"aiworkforce/backend/internal/drive"
)

// ErrUnsupportedType is a content type (or file extension) the drive does not store (HTTP 415).
var ErrUnsupportedType = errors.New("inboxes: unsupported media type")

// MultipartOverhead is what a multipart body may add to the payload (boundaries and part headers).
const MultipartOverhead = 64 << 10

// Part is one file of a push, as parsed from the body. Name is sanitized ("" = the service generates one).
type Part struct {
	Name string
	Ext  string // with the dot, lower case
	Kind string
	Data []byte
}

// Parsed is a parsed body.
type Parsed struct {
	Parts         []Part
	Size          int64
	IgnoredFields int
}

// rawTypes maps the media type of a raw body to the stored extension. Anything else is refused: the drive keeps
// an allowlist of document types (no html, svg, archives or executables).
var rawTypes = map[string]string{
	"application/json": ".json", "application/x-ndjson": ".jsonl", "application/jsonl": ".jsonl",
	"text/csv": ".csv", "application/csv": ".csv", "text/tab-separated-values": ".tsv",
	"text/plain": ".txt", "text/markdown": ".md", "application/xml": ".xml", "text/xml": ".xml",
	"application/yaml": ".yaml", "application/x-yaml": ".yaml", "text/yaml": ".yaml",
	"application/pdf": ".pdf", "image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":       ".xlsx",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": ".docx",
}

// KindOfExt is the content kind of a file extension.
func KindOfExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".json", ".jsonl":
		return KindJSON
	case ".csv", ".tsv":
		return KindCSV
	}
	return KindFile
}

func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBadPayload, fmt.Sprintf(format, a...))
}

func tooLarge(max int64) error {
	return fmt.Errorf("%w: the payload exceeds the limit of this inbox (%d bytes)", ErrTooLarge, max)
}

// readCapped reads at most max bytes; one more is a too-large error.
func readCapped(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, tooLarge(max)
	}
	return data, nil
}

// Parse reads the body of a push. contentType is the request's Content-Type; fileName the optional X-Filename
// header (raw bodies only); max the payload cap of the inbox (sum of every file). The content is never
// interpreted beyond what storing it safely needs (JSON is checked to be JSON, nothing more).
func Parse(contentType, fileName string, body io.Reader, max int64) (Parsed, error) {
	mt, params, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || mt == "" {
		return Parsed{}, bad("a Content-Type header is required (application/json, text/csv, multipart/form-data or a file type)")
	}
	mt = strings.ToLower(mt)
	if mt == "multipart/form-data" {
		return parseMultipart(params["boundary"], body, max)
	}
	ext, ok := rawTypes[mt]
	if !ok && strings.HasSuffix(mt, "+json") {
		ext, ok = ".json", true
	}
	if !ok {
		return Parsed{}, fmt.Errorf("%w: content type %q is not accepted", ErrUnsupportedType, mt)
	}
	data, err := readCapped(body, max)
	if err != nil {
		return Parsed{}, err
	}
	p := Part{Ext: ext, Kind: KindOfExt(ext), Data: data}
	if strings.TrimSpace(fileName) != "" {
		n := SanitizeName(fileName)
		if n == "" {
			return Parsed{}, bad("X-Filename is not a usable file name")
		}
		nx := strings.ToLower(path.Ext(n))
		if !sameFamily(nx, ext) {
			return Parsed{}, bad("the X-Filename extension does not match the Content-Type")
		}
		p.Name, p.Ext = n, nx
	}
	if err := checkPart(p); err != nil {
		return Parsed{}, err
	}
	return Parsed{Parts: []Part{p}, Size: int64(len(data))}, nil
}

// sameFamily accepts the usual aliases of one type (.jpeg/.jpg, .yml/.yaml, .markdown/.md).
func sameFamily(a, b string) bool {
	alias := map[string]string{".jpeg": ".jpg", ".yml": ".yaml", ".markdown": ".md", ".log": ".txt"}
	n := func(e string) string {
		e = strings.ToLower(e)
		if v, ok := alias[e]; ok {
			return v
		}
		return e
	}
	return n(a) == n(b)
}

func parseMultipart(boundary string, body io.Reader, max int64) (Parsed, error) {
	if boundary == "" {
		return Parsed{}, bad("multipart/form-data without a boundary")
	}
	mr := multipart.NewReader(body, boundary)
	var out Parsed
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Parsed{}, bad("malformed multipart body")
		}
		if part.FileName() == "" {
			// a plain form field: not stored (only files are); drained within the payload budget
			n, err := io.Copy(io.Discard, io.LimitReader(part, max+1))
			if err != nil {
				return Parsed{}, bad("malformed multipart body")
			}
			if out.Size+n > max {
				return Parsed{}, tooLarge(max)
			}
			out.IgnoredFields++
			continue
		}
		if len(out.Parts) >= MaxParts {
			return Parsed{}, bad("at most %d files per push", MaxParts)
		}
		data, err := readCapped(part, max-out.Size)
		if err != nil {
			if errors.Is(err, ErrTooLarge) {
				return Parsed{}, tooLarge(max)
			}
			return Parsed{}, bad("malformed multipart body")
		}
		name := SanitizeName(part.FileName())
		if name == "" {
			return Parsed{}, bad("a file part has no usable file name")
		}
		ext := strings.ToLower(path.Ext(name))
		p := Part{Name: name, Ext: ext, Kind: KindOfExt(ext), Data: data}
		if err := checkPart(p); err != nil {
			return Parsed{}, err
		}
		out.Parts = append(out.Parts, p)
		out.Size += int64(len(data))
	}
	if len(out.Parts) == 0 {
		return Parsed{}, bad("the multipart body has no file part")
	}
	return out, nil
}

// checkPart refuses what the drive would refuse anyway, with the inbox's own codes, plus JSON validity.
func checkPart(p Part) error {
	if len(p.Data) == 0 {
		return bad("empty payload")
	}
	if _, ok := drive.TypeOf("x" + p.Ext); !ok {
		return fmt.Errorf("%w: files of type %q are not accepted", ErrUnsupportedType, p.Ext)
	}
	switch p.Ext {
	case ".json":
		if !json.Valid(p.Data) {
			return bad("the body is not valid JSON")
		}
	case ".jsonl":
		sc := bufio.NewScanner(bytes.NewReader(p.Data))
		sc.Buffer(make([]byte, 0, 64<<10), len(p.Data)+1)
		for sc.Scan() {
			if line := bytes.TrimSpace(sc.Bytes()); len(line) > 0 && !json.Valid(line) {
				return bad("a line of the JSON Lines body is not valid JSON")
			}
		}
	}
	return nil
}

// SanitizeName turns a caller-supplied file name into a safe drive name, or "" when nothing usable is left.
// Only the last path element is kept ("C:\\exports\\a.csv" and "../../a.csv" are "a.csv"); control, invisible
// and reserved characters are dropped or replaced; the length is capped keeping the extension.
func SanitizeName(raw string) string {
	raw = strings.ReplaceAll(raw, "\\", "/")
	if i := strings.LastIndex(raw, "/"); i >= 0 {
		raw = raw[i+1:]
	}
	raw = strings.ToValidUTF8(raw, "")
	var b strings.Builder
	for _, r := range raw {
		switch {
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == 0x2028, r == 0x2029:
			continue
		case strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteRune('-')
		default:
			b.WriteRune(r)
		}
	}
	n := strings.TrimSpace(b.String())
	n = strings.TrimLeft(n, ". ") // no hidden files, no "..": a name starts with a visible character
	n = strings.TrimRight(n, ". ")
	if utf8.RuneCountInString(n) > drive.MaxNameRunes {
		ext := path.Ext(n)
		if utf8.RuneCountInString(ext) > 10 {
			ext = ""
		}
		stem := []rune(strings.TrimSuffix(n, ext))
		stem = stem[:drive.MaxNameRunes-utf8.RuneCountInString(ext)]
		n = strings.TrimRight(strings.TrimSpace(string(stem)), ". ") + ext
	}
	c, err := drive.CleanName(n)
	if err != nil || path.Ext(c) == "" {
		return ""
	}
	return c
}
