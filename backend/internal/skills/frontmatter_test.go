package skills

import (
	"strings"
	"testing"
)

func TestParseSkillMDValid(t *testing.T) {
	raw := "---\nname: pdf-processing\ndescription: >\n  Extract text from PDFs.\n  Use when the user mentions PDFs.\nlicense: MIT\nallowed-tools: Bash(git:*) Read\nmetadata:\n  author: acme\n  version: \"1.0\"\nx-custom: hello # comment\n---\n\n# Title\n\nDo the thing.\n"
	fm, body, err := ParseSkillMD(raw)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Name != "pdf-processing" || fm.License != "MIT" {
		t.Fatalf("%+v", fm)
	}
	if fm.Description != "Extract text from PDFs. Use when the user mentions PDFs." {
		t.Fatalf("folded description: %q", fm.Description)
	}
	if len(fm.AllowedTools) != 2 || fm.AllowedTools[0] != "Bash(git:*)" {
		t.Fatalf("allowed tools: %v", fm.AllowedTools)
	}
	if fm.Metadata["author"] != "acme" || fm.Metadata["version"] != "1.0" || fm.Extra["x-custom"] != "hello" {
		t.Fatalf("metadata/extra: %+v", fm)
	}
	if !strings.HasPrefix(body, "# Title") {
		t.Fatalf("body: %q", body)
	}
}

func TestParseSkillMDListsAndQuotes(t *testing.T) {
	raw := "---\nname: a-b\ndescription: 'It''s: quoted'\nallowed-tools:\n  - Read\n  - \"Grep\"\n---\nbody\n"
	fm, _, err := ParseSkillMD(raw)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Description != "It's: quoted" || len(fm.AllowedTools) != 2 || fm.AllowedTools[1] != "Grep" {
		t.Fatalf("%+v", fm)
	}
	fm, _, err = ParseSkillMD("---\r\nname: crlf\r\ndescription: ok\r\nallowed-tools: [Read, Write]\r\n---\r\nbody\r\n")
	if err != nil || len(fm.AllowedTools) != 2 {
		t.Fatalf("crlf: %v %+v", err, fm)
	}
}

func TestParseSkillMDInvalid(t *testing.T) {
	long := strings.Repeat("x", MaxDescription+1)
	cases := map[string]struct{ raw, code string }{
		"no frontmatter":    {"# just markdown\n", "frontmatter_missing"},
		"unclosed":          {"---\nname: a\ndescription: b\n", "frontmatter_unclosed"},
		"missing name":      {"---\ndescription: b\n---\nbody\n", "name_invalid"},
		"uppercase name":    {"---\nname: PDF\ndescription: b\n---\nbody\n", "name_invalid"},
		"underscore name":   {"---\nname: a_b\ndescription: b\n---\nbody\n", "name_invalid"},
		"name too long":     {"---\nname: " + strings.Repeat("a", 65) + "\ndescription: b\n---\nbody\n", "name_invalid"},
		"missing desc":      {"---\nname: a\n---\nbody\n", "description_required"},
		"empty desc":        {"---\nname: a\ndescription: \"  \"\n---\nbody\n", "description_required"},
		"desc too long":     {"---\nname: a\ndescription: " + long + "\n---\nbody\n", "description_too_long"},
		"no body":           {"---\nname: a\ndescription: b\n---\n\n", "instructions_required"},
		"bad line":          {"---\nname a\ndescription: b\n---\nbody\n", "frontmatter_invalid"},
		"anchor":            {"---\nname: a\ndescription: &x b\n---\nbody\n", "frontmatter_invalid"},
		"unterminated":      {"---\nname: a\ndescription: \"b\n---\nbody\n", "frontmatter_invalid"},
		"nul byte":          {"---\nname: a\ndescription: b\x00\n---\nbody\n", "not_text"},
		"stray indentation": {"---\n  name: a\ndescription: b\n---\nbody\n", "frontmatter_invalid"},
	}
	for n, c := range cases {
		_, _, err := ParseSkillMD(c.raw)
		if err == nil || CodeOf(err) != c.code {
			t.Errorf("%s: want %s, got %v", n, c.code, err)
		}
	}
}

func TestRenderRoundTrip(t *testing.T) {
	fm := Frontmatter{Name: "my-skill", Description: "Use: when \"quoted\" & tricky # things", License: "MIT", AllowedTools: []string{"Read", "Bash(ls:*)"},
		Metadata: map[string]string{"author": "me", "k": "true"}, Extra: map[string]string{"zeta": "1"}}
	raw := RenderSkillMD(fm, "# Hi\n\nBody text.")
	got, body, err := ParseSkillMD(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != fm.Description || got.License != "MIT" || strings.Join(got.AllowedTools, " ") != "Read Bash(ls:*)" || got.Metadata["k"] != "true" || got.Extra["zeta"] != "1" {
		t.Fatalf("%+v", got)
	}
	if RenderSkillMD(got, body) != raw {
		t.Fatalf("render is not stable:\n%s\n---\n%s", RenderSkillMD(got, body), raw)
	}
}
