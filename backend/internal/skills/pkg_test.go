package skills

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

const okSkill = "---\nname: demo\ndescription: A demo skill\n---\n\nSteps.\n"

type zent struct {
	name string
	body string
	mode uint32 // 0 = regular
}

func mkzip(t *testing.T, ents ...zent) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range ents {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(fsMode(e.mode))
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(e.body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestReadZipHappyPathAndWrapperFolder(t *testing.T) {
	for _, prefix := range []string{"", "demo/"} {
		p, err := ReadZip(mkzip(t, zent{name: prefix + "SKILL.md", body: okSkill}, zent{name: prefix + "references/guide.md", body: "# guide\n"},
			zent{name: prefix + "scripts/run.py", body: "print(1)\n"}, zent{name: "__MACOSX/._x", body: "junk"}))
		if prefix == "" {
			// __MACOSX alongside root files is ignored, the layout has no wrapper
			if err != nil {
				t.Fatal(err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		if p.Front.Name != "demo" || len(p.Files) != 2 || p.Files[0].Path != "references/guide.md" || p.Files[1].Path != "scripts/run.py" {
			t.Fatalf("%q: %+v", prefix, p.Files)
		}
	}
}

func TestReadZipGuards(t *testing.T) {
	big := strings.Repeat("A", 400<<10)
	manyFiles := []zent{{name: "SKILL.md", body: okSkill}}
	for i := 0; i < MaxFiles; i++ {
		manyFiles = append(manyFiles, zent{name: "f" + strings.Repeat("a", i%5) + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".md", body: "x"})
	}
	tooManyEntries := []zent{{name: "SKILL.md", body: okSkill}}
	for i := 0; i < MaxZipEntries+1; i++ {
		tooManyEntries = append(tooManyEntries, zent{name: "d/" + strings.Repeat("a", 3) + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i/676)) + ".md", body: "x"})
	}
	cases := map[string]struct {
		data []byte
		code string
	}{
		"not a zip":        {[]byte("PK\x03\x04garbage"), "zip_invalid"},
		"traversal":        {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: "../evil.md", body: "x"}), "path_traversal"},
		"nested traversal": {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: "a/../../evil.md", body: "x"}), "path_traversal"},
		"absolute":         {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: "/etc/passwd.md", body: "x"}), "path_traversal"},
		"backslash":        {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: "a\\b.md", body: "x"}), "path_traversal"},
		"symlink":          {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: "link.md", body: "/etc/passwd", mode: 0o120777}), "zip_symlink"},
		"device":           {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: "dev.md", body: "", mode: 0o60666}), "zip_special_file"},
		"binary ext":       {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: "tool.exe", body: "MZ"}), "extension_not_allowed"},
		"image":            {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: "logo.png", body: "\x89PNG"}), "extension_not_allowed"},
		"html":             {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: "x.html", body: "<b>"}), "extension_not_allowed"},
		"no extension":     {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: "Makefile", body: "all:"}), "extension_not_allowed"},
		"hidden file":      {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: ".env", body: "A=1"}), "path_invalid"},
		"NUL in text":      {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: "a.md", body: "a\x00b"}), "not_text"},
		"invalid utf8":     {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: "a.md", body: "\xff\xfe"}), "not_text"},
		"no SKILL.md":      {mkzip(t, zent{name: "a.md", body: "x"}), "skill_md_missing"},
		"bad frontmatter":  {mkzip(t, zent{name: "SKILL.md", body: "# nope"}), "frontmatter_missing"},
		"one file too big": {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: "a.md", body: strings.Repeat("x", MaxFileBytes+1)}), "file_too_large"},
		"bomb (total)":     {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: "a.md", body: big}, zent{name: "b.md", body: big}, zent{name: "c.md", body: big}, zent{name: "d.md", body: big}, zent{name: "e.md", body: big}, zent{name: "f.md", body: big}), "zip_bomb"},
		"bomb (ratio)":     {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: "a.md", body: strings.Repeat("\x00", 0) + strings.Repeat("A", 300<<10)}), "zip_bomb"},
		"too many files":   {mkzip(t, manyFiles...), "too_many_files"},
		"too many entries": {mkzip(t, tooManyEntries...), "zip_too_many_entries"},
		"empty":            {mkzip(t), "zip_empty"},
		"duplicate":        {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: "a.md", body: "x"}, zent{name: "A.md", body: "y"}), "duplicate_path"},
		"deep":             {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: "a/b/c/d/e.md", body: "x"}), "path_invalid"},
		"space in name":    {mkzip(t, zent{name: "SKILL.md", body: okSkill}, zent{name: "my file.md", body: "x"}), "path_invalid"},
	}
	for n, c := range cases {
		_, err := ReadZip(c.data)
		if err == nil || CodeOf(err) != c.code {
			t.Errorf("%s: want %s, got %v", n, c.code, err)
		}
	}
}

func TestReadSkillMDSingle(t *testing.T) {
	p, err := ReadSkillMD([]byte(okSkill))
	if err != nil || p.Front.Name != "demo" || len(p.Files) != 0 {
		t.Fatalf("%v %+v", err, p)
	}
	if _, err := ReadSkillMD([]byte("---\nname: x\n---\n")); err == nil {
		t.Fatal("must fail")
	}
}

func TestZipRoundTrip(t *testing.T) {
	p, err := Build("---\nname: rt\ndescription: round trip\nlicense: MIT\n---\n\nBody\n", []File{{"templates/a.csv", "a,b\n1,2\n"}, {"scripts/x.sh", "echo hi\n"}})
	if err != nil {
		t.Fatal(err)
	}
	z, err := p.Zip()
	if err != nil {
		t.Fatal(err)
	}
	q, err := ReadZip(z)
	if err != nil {
		t.Fatal(err)
	}
	if q.SkillMD != p.SkillMD || len(q.Files) != 2 || q.Files[0] != p.Files[0] || q.Files[1] != p.Files[1] {
		t.Fatalf("round trip differs:\n%+v\n%+v", p, q)
	}
	zr, _ := zip.NewReader(bytes.NewReader(z), int64(len(z)))
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "rt/") || f.Mode()&0o111 != 0 {
			t.Fatalf("layout/mode: %s %v", f.Name, f.Mode())
		}
	}
}
