package positions

import (
	"slices"
	"sort"
	"strings"
)

// The rules of a library are pure and mirror the drive (internal/drive/access.go): an entry applies to its folder and
// everything below it, the deepest entry covering a path decides, paths compare without regard to case. A library
// with no entries is "no library": the defaults of the department (Bounds.Defaults).

// Bounds are where the folders of a department's library may be: its own folder, /Shared and what other departments
// share with it (read only).
type Bounds struct {
	Office   string   `json:"office_folder"` // "" when the department has no drive folder yet
	Shared   string   `json:"shared"`
	SharedIn []string `json:"shared_in"`
}

// Kinds of a path inside the bounds.
const (
	kindOffice   = "office"
	kindShared   = "shared"
	kindSharedIn = "shared_in"
)

// Defaults is what an agent of the department reads without a library: its department (Edit), /Shared (Read) and
// what is shared with the department (Read).
func (b Bounds) Defaults() []Folder {
	var out []Folder
	if b.Office != "" {
		out = append(out, Folder{Path: b.Office, Level: LevelEdit})
	}
	if b.Shared != "" {
		out = append(out, Folder{Path: b.Shared, Level: LevelRead})
	}
	for _, s := range b.SharedIn {
		out = append(out, Folder{Path: s, Level: LevelRead})
	}
	return out
}

// Within tells where a path lies: inside the department's folder, /Shared, something shared with the department, or
// outside ("").
func (b Bounds) Within(p string) string {
	switch {
	case b.Office != "" && under(p, b.Office):
		return kindOffice
	case b.Shared != "" && under(p, b.Shared):
		return kindShared
	}
	for _, s := range b.SharedIn {
		if under(p, s) {
			return kindSharedIn
		}
	}
	return ""
}

// under reports whether p is root or lies below it, without regard to case.
func under(p, root string) bool {
	p, root = strings.ToLower(strings.TrimRight(p, "/")), strings.ToLower(strings.TrimRight(root, "/"))
	if root == "" {
		return true
	}
	return p == root || strings.HasPrefix(p, root+"/")
}

func depth(p string) int {
	t := strings.Trim(p, "/")
	if t == "" {
		return 0
	}
	return strings.Count(t, "/") + 1
}

func rank(level string) int {
	switch level {
	case LevelEdit:
		return 2
	case LevelRead:
		return 1
	}
	return 0
}

// Effective is the library that applies: the defaults when it has no entries.
func Effective(lib []Folder, b Bounds) []Folder {
	if len(lib) == 0 {
		return b.Defaults()
	}
	return lib
}

// LevelAt is the level a library gives on a path (the deepest entry covering it; none when no entry does).
func LevelAt(lib []Folder, p string) string {
	best, level := -1, LevelNone
	for _, f := range lib {
		if d := depth(f.Path); under(p, f.Path) && d > best {
			best, level = d, f.Level
		}
	}
	return level
}

// checkPoints are the paths where two libraries can differ: the paths of their entries (between entries both level
// functions are constant).
func checkPoints(a, b []Folder) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range append(append([]Folder{}, a...), b...) {
		if k := strings.ToLower(strings.TrimRight(f.Path, "/")); !seen[k] {
			seen[k] = true
			out = append(out, f.Path)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if di, dj := depth(out[i]), depth(out[j]); di != dj {
			return di < dj
		}
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

// Exceeds lists what sub has beyond sup: the skills sup does not have and the folders where sub's level is higher.
func Exceeds(sup, sub Package, b Bounds) Excess {
	ex := Excess{Skills: []string{}, Folders: []Folder{}}
	for _, s := range sub.Skills {
		if !slices.Contains(sup.Skills, s) && !slices.Contains(ex.Skills, s) {
			ex.Skills = append(ex.Skills, s)
		}
	}
	u, s := Effective(sup.Library, b), Effective(sub.Library, b)
	for _, p := range checkPoints(u, s) {
		if ls := LevelAt(s, p); rank(ls) > rank(LevelAt(u, p)) {
			ex.Folders = append(ex.Folders, Folder{Path: p, Level: ls})
		}
	}
	return ex
}

// Clamp cuts sub down to sup: it keeps only the skills sup has, and on every folder the lower of both levels. It
// returns the new package, the skills removed and the folders lost or lowered. A library cut to nothing stays
// explicit (an exclusion of /Shared), never "no library" (which would be the defaults: more, not less).
func Clamp(sup, sub Package, b Bounds) (Package, []string, []FolderLoss) {
	out := Package{Skills: []string{}}
	removed := []string{}
	for _, s := range sub.Skills {
		if slices.Contains(sup.Skills, s) {
			out.Skills = append(out.Skills, s)
		} else {
			removed = append(removed, s)
		}
	}
	u, s := Effective(sup.Library, b), Effective(sub.Library, b)
	if len(sub.Library) > 0 && len(Exceeds(Package{Library: sup.Library}, Package{Library: sub.Library}, b).Folders) == 0 {
		out.Library = append([]Folder{}, sub.Library...) // nothing to cut: the library stays as it was written
		return out, removed, []FolderLoss{}
	}
	var raw []Folder
	losses := []FolderLoss{}
	for _, p := range checkPoints(u, s) {
		ls, lu := LevelAt(s, p), LevelAt(u, p)
		m := ls
		if rank(lu) < rank(ls) {
			m = lu
			losses = append(losses, FolderLoss{Path: p, From: ls, To: lu})
		}
		raw = append(raw, Folder{Path: p, Level: m})
	}
	out.Library = simplify(raw)
	if len(out.Library) == 0 {
		root := b.Shared
		if root == "" {
			root = "/Shared"
		}
		out.Library = []Folder{{Path: root, Level: LevelNone}}
	}
	return out, removed, losses
}

// simplify drops the entries that repeat what their nearest ancestor (or nothing, for a top exclusion) already says.
// The input is sorted by depth.
func simplify(in []Folder) []Folder {
	var out []Folder
	for _, f := range in {
		if LevelAt(out, f.Path) == f.Level {
			continue
		}
		out = append(out, f)
	}
	return out
}
