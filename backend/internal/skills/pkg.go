package skills

import (
	"archive/zip"
	"bytes"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Package is a validated skill package: SKILL.md plus text resource files.
type Package struct {
	Front   Frontmatter
	Body    string
	SkillMD string // the SKILL.md text as stored
	// Files are the resource files (everything except SKILL.md), sorted by path.
	Files []File
	// Notes are things the importer changed or ignored (shown to the reviewer).
	Notes []string
}

// textExts are the only extensions a package may carry: text, never binaries.
var textExts = map[string]bool{
	".md": true, ".txt": true, ".csv": true, ".json": true, ".yaml": true, ".yml": true,
	".py": true, ".js": true, ".sh": true,
}

var scriptExts = map[string]bool{".py": true, ".js": true, ".sh": true}

// IsScript reports whether a file path is a script (never executed here).
func IsScript(p string) bool { return scriptExts[strings.ToLower(path.Ext(p))] }

var segRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// CleanPath validates a relative path inside a package and returns it
// normalized. Traversal, absolute paths, backslashes, hidden files and odd
// characters are refused; the extension must be on the allow-list.
func CleanPath(p string) (string, error) {
	p = norm.NFC.String(p)
	if p == "" || len(p) > MaxPathLen {
		return "", invalid("path_invalid", "path is empty or longer than %d characters", MaxPathLen)
	}
	if strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") || (len(p) > 1 && p[1] == ':') {
		return "", invalid("path_invalid", "path %q is not a relative package path", clip(p))
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return "", invalid("path_invalid", "path has control characters")
		}
	}
	segs := strings.Split(p, "/")
	if len(segs) > MaxPathDepth {
		return "", invalid("path_invalid", "path %q is deeper than %d levels", clip(p), MaxPathDepth)
	}
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return "", invalid("path_traversal", "path %q uses an empty, . or .. segment", clip(p))
		}
		if !segRe.MatchString(s) {
			return "", invalid("path_invalid", "path segment %q: use letters, digits, . _ - (no spaces, no leading dot)", clip(s))
		}
	}
	if !textExts[strings.ToLower(path.Ext(p))] {
		return "", invalid("extension_not_allowed", "files of type %q are not allowed in a skill (md, txt, csv, json, yaml, py, js, sh)", path.Ext(p))
	}
	return p, nil
}

func checkText(p string, data []byte) error {
	if len(data) > MaxFileBytes {
		return tooLarge("file_too_large", "%s is larger than %d KB", clip(p), MaxFileBytes>>10)
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return invalid("not_text", "%s is not valid UTF-8 text (binary files are not allowed)", clip(p))
	}
	return nil
}

// Build validates SKILL.md plus resource files and returns the package.
// skillMD is the raw SKILL.md; files must not contain it.
func Build(skillMD string, files []File) (Package, error) {
	fm, body, err := ParseSkillMD(skillMD)
	if err != nil {
		return Package{}, err
	}
	if len(skillMD) > MaxFileBytes {
		return Package{}, tooLarge("file_too_large", "SKILL.md is larger than %d KB", MaxFileBytes>>10)
	}
	if len(files)+1 > MaxFiles {
		return Package{}, tooLarge("too_many_files", "a skill may have at most %d files", MaxFiles)
	}
	total := int64(len(skillMD))
	seen := map[string]bool{strings.ToLower(SkillFile): true}
	out := make([]File, 0, len(files))
	for _, f := range files {
		p, err := CleanPath(f.Path)
		if err != nil {
			return Package{}, err
		}
		if seen[strings.ToLower(p)] {
			return Package{}, invalid("duplicate_path", "file %q appears twice", clip(p))
		}
		seen[strings.ToLower(p)] = true
		if err := checkText(p, []byte(f.Content)); err != nil {
			return Package{}, err
		}
		if f.Content == "" {
			return Package{}, invalid("empty_file", "file %q is empty", clip(p))
		}
		total += int64(len(f.Content))
		out = append(out, File{Path: p, Content: f.Content})
	}
	if total > MaxPackageBytes {
		return Package{}, tooLarge("package_too_large", "a skill may be at most %d MB in total", MaxPackageBytes>>20)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return Package{Front: fm, Body: body, SkillMD: skillMD, Files: out}, nil
}

// ReadSkillMD builds a package from a single SKILL.md upload.
func ReadSkillMD(data []byte) (Package, error) {
	if len(data) > MaxFileBytes {
		return Package{}, tooLarge("file_too_large", "SKILL.md is larger than %d KB", MaxFileBytes>>10)
	}
	if err := checkText(SkillFile, data); err != nil {
		return Package{}, err
	}
	return Build(string(data), nil)
}

var junkRe = regexp.MustCompile(`(^|/)(__MACOSX/|\.DS_Store$|Thumbs\.db$)`)

// ReadZip reads a skill package from a zip. Everything is checked from the
// headers BEFORE anything is inflated (entry count, declared sizes, ratios),
// and the inflation itself is bounded again. Symlinks, traversal, absolute
// paths, binaries and encrypted entries are refused.
func ReadZip(data []byte) (Package, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Package{}, invalid("zip_invalid", "the file is not a valid zip archive")
	}
	if len(zr.File) > MaxZipEntries {
		return Package{}, tooLarge("zip_too_many_entries", "the archive has more than %d entries", MaxZipEntries)
	}
	type entry struct {
		name string
		f    *zip.File
	}
	var entries []entry
	var declared uint64
	for _, f := range zr.File {
		name := f.Name
		if strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") {
			return Package{}, invalid("path_traversal", "entry %q has an unsafe path", clip(name))
		}
		for _, seg := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
			if seg == ".." || seg == "." || seg == "" {
				return Package{}, invalid("path_traversal", "entry %q uses an empty, . or .. segment", clip(name))
			}
		}
		if junkRe.MatchString(name) {
			continue
		}
		mode := f.Mode()
		if mode&fs.ModeSymlink != 0 {
			return Package{}, invalid("zip_symlink", "entry %q is a symbolic link", clip(name))
		}
		if strings.HasSuffix(name, "/") || mode.IsDir() {
			continue
		}
		if !mode.IsRegular() {
			return Package{}, invalid("zip_special_file", "entry %q is not a regular file", clip(name))
		}
		if f.Flags&0x1 != 0 {
			return Package{}, invalid("zip_encrypted", "encrypted archives are not supported")
		}
		if f.UncompressedSize64 > MaxFileBytes {
			return Package{}, tooLarge("file_too_large", "%s is larger than %d KB", clip(name), MaxFileBytes>>10)
		}
		declared += f.UncompressedSize64
		if declared > MaxPackageBytes {
			return Package{}, tooLarge("zip_bomb", "the archive expands to more than %d MB", MaxPackageBytes>>20)
		}
		if f.UncompressedSize64 >= 64<<10 && (f.CompressedSize64 == 0 || f.UncompressedSize64/f.CompressedSize64 > MaxZipRatio) {
			return Package{}, tooLarge("zip_bomb", "entry %q has a suspicious compression ratio", clip(name))
		}
		entries = append(entries, entry{name, f})
	}
	if len(entries) == 0 {
		return Package{}, invalid("zip_empty", "the archive has no files")
	}
	// A single top-level folder wrapping everything (standard layout) is stripped.
	prefix := ""
	if first, _, ok := strings.Cut(entries[0].name, "/"); ok {
		all := true
		for _, e := range entries {
			if !strings.HasPrefix(e.name, first+"/") {
				all = false
				break
			}
		}
		if all {
			prefix = first + "/"
		}
	}
	var notes []string
	var skillMD string
	haveSkill := false
	var files []File
	var total int64
	for _, e := range entries {
		rel := strings.TrimPrefix(e.name, prefix)
		if rel == "" {
			continue
		}
		rc, err := e.f.Open()
		if err != nil {
			return Package{}, invalid("zip_invalid", "cannot read entry %q", clip(e.name))
		}
		b, err := io.ReadAll(io.LimitReader(rc, MaxFileBytes+1))
		rc.Close()
		if err != nil {
			return Package{}, invalid("zip_invalid", "cannot read entry %q", clip(e.name))
		}
		if len(b) > MaxFileBytes {
			return Package{}, tooLarge("file_too_large", "%s is larger than %d KB", clip(e.name), MaxFileBytes>>10)
		}
		total += int64(len(b))
		if total > MaxPackageBytes {
			return Package{}, tooLarge("zip_bomb", "the archive expands to more than %d MB", MaxPackageBytes>>20)
		}
		if rel == SkillFile {
			if err := checkText(rel, b); err != nil {
				return Package{}, err
			}
			skillMD, haveSkill = string(b), true
			continue
		}
		if rel == "LICENSE" || rel == "NOTICE" { // common extension-less files
			notes = append(notes, rel+" was stored as "+rel+".txt")
			rel += ".txt"
		}
		p, err := CleanPath(rel)
		if err != nil {
			return Package{}, err
		}
		if err := checkText(p, b); err != nil {
			return Package{}, err
		}
		if len(b) == 0 {
			notes = append(notes, "empty file ignored: "+p)
			continue
		}
		files = append(files, File{Path: p, Content: string(b)})
	}
	if !haveSkill {
		return Package{}, invalid("skill_md_missing", "the archive has no SKILL.md at its root")
	}
	p, err := Build(skillMD, files)
	if err != nil {
		return Package{}, err
	}
	p.Notes = notes
	return p, nil
}

// Zip writes the package in the standard layout: <name>/SKILL.md and its
// resources. The output is deterministic (fixed times, sorted entries) and
// carries no executable bits.
func (p Package) Zip() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	t := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	add := func(rel, content string) error {
		h := &zip.FileHeader{Name: p.Front.Name + "/" + rel, Method: zip.Deflate, Modified: t}
		h.SetMode(0o644)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = io.WriteString(w, content)
		return err
	}
	if err := add(SkillFile, p.SkillMD); err != nil {
		return nil, err
	}
	for _, f := range p.Files {
		if err := add(f.Path, f.Content); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
