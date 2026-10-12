package skills

import (
	"context"
	"embed"
	"io/fs"
	"sort"
	"strings"
)

// The starter skills ship as data (builtin/<lang>/<name>/SKILL.md plus their
// resource files), in Spanish and English, in the same format as an imported
// skill. They are created, active, the first time an organization uses the
// library; role templates (default_skills) decide which agents get which.
//
//go:embed all:builtin
var builtinFS embed.FS

// Starters returns the starter packages of a language ("es" default, "en").
func Starters(lang string) ([]Package, error) {
	if !strings.HasPrefix(lang, "en") {
		lang = "es"
	} else {
		lang = "en"
	}
	root := "builtin/" + lang
	entries, err := fs.ReadDir(builtinFS, root)
	if err != nil {
		return nil, err
	}
	var out []Package
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := root + "/" + e.Name()
		raw, err := fs.ReadFile(builtinFS, dir+"/SKILL.md")
		if err != nil {
			return nil, err
		}
		var files []File
		err = fs.WalkDir(builtinFS, dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || p == dir+"/SKILL.md" {
				return err
			}
			b, err := fs.ReadFile(builtinFS, p)
			if err != nil {
				return err
			}
			files = append(files, File{Path: strings.TrimPrefix(p, dir+"/"), Content: string(b)})
			return nil
		})
		if err != nil {
			return nil, err
		}
		pkg, err := Build(string(raw), files)
		if err != nil {
			return nil, err
		}
		out = append(out, pkg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Front.Name < out[j].Front.Name })
	return out, nil
}

// StarterNames are the names of the starter skills.
func StarterNames() []string {
	ps, _ := Starters("es")
	var out []string
	for _, p := range ps {
		out = append(out, p.Front.Name)
	}
	return out
}

// Ensure creates the starter skills of an organization the first time it uses
// the library (an organization that already has any skill, deleted ones
// included, is left alone). Failures are logged and retried on the next call.
func (s *Service) Ensure(ctx context.Context, org string) {
	if _, loaded := s.seeded.LoadOrStore(org, struct{}{}); loaded {
		return
	}
	ctx = withOrg(ctx, org)
	n, err := s.cfg.Store.CountSkills(ctx, org)
	if err != nil {
		s.seeded.Delete(org)
		s.cfg.Log.Warn("skills: count", "org", org, "err", err)
		return
	}
	if n > 0 {
		return
	}
	lang := "es"
	if s.cfg.Locale != nil {
		lang = s.cfg.Locale(ctx)
	}
	pkgs, err := Starters(lang)
	if err != nil {
		s.cfg.Log.Error("skills: starters", "err", err)
		return
	}
	sys := Actor{ID: "system", System: true, Admin: true}
	for _, p := range pkgs {
		if _, err := s.createFrom(ctx, sys, p, SourceCreated, StatusActive, "Starter skill", true); err != nil && CodeOf(err) != "name_taken" {
			s.cfg.Log.Warn("skills: starter not created", "name", p.Front.Name, "err", err)
			s.seeded.Delete(org) // e.g. read-only mode: try again later
		}
	}
}
