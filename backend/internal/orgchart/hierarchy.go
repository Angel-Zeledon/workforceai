package orgchart

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Limits of the department tree.
const (
	MaxDepartments   = 200
	MaxDepth         = 8
	MaxNameRunes     = 80
	MaxCostCenterLen = 32
)

var costCenterRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

// DetectCycle looks for a cycle in a "child -> parent" map (an empty parent is a root). It returns
// the nodes of the first cycle found, in order and ending where it started (a -> b -> a is
// [a b a]), or nil. Iteration is sorted so the answer is deterministic.
func DetectCycle(parent map[string]string) []string {
	keys := make([]string, 0, len(parent))
	for k := range parent {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	const (
		unseen = iota
		onPath
		done
	)
	state := map[string]int{}
	for _, start := range keys {
		if state[start] != unseen {
			continue
		}
		var path []string
		cur := start
		for cur != "" {
			switch state[cur] {
			case done:
				cur = ""
				continue
			case onPath:
				i := 0
				for path[i] != cur {
					i++
				}
				return append(append([]string{}, path[i:]...), cur)
			}
			state[cur] = onPath
			path = append(path, cur)
			cur = parent[cur]
		}
		for _, n := range path {
			state[n] = done
		}
	}
	return nil
}

// ValidateDepartments checks a whole department set: ids and names unique (names case-insensitive),
// parents exist, no cycle, bounded depth, sane cost centers and heads.
func ValidateDepartments(deps []Department) error {
	if len(deps) > MaxDepartments {
		return fmt.Errorf("too many departments (max %d)", MaxDepartments)
	}
	ids, names := map[string]Department{}, map[string]string{}
	for _, d := range deps {
		if err := validateDepartmentFields(d); err != nil {
			return err
		}
		if _, dup := ids[d.ID]; dup {
			return fmt.Errorf("duplicate department id %q", d.ID)
		}
		n := strings.ToLower(strings.TrimSpace(d.Name))
		if other, dup := names[n]; dup {
			return fmt.Errorf("department name %q is already used by %q", d.Name, other)
		}
		ids[d.ID], names[n] = d, d.ID
	}
	parent := map[string]string{}
	for _, d := range deps {
		if d.ParentID != "" {
			if _, ok := ids[d.ParentID]; !ok {
				return fmt.Errorf("department %q: parent %q does not exist", d.ID, d.ParentID)
			}
		}
		parent[d.ID] = d.ParentID
	}
	if c := DetectCycle(parent); c != nil {
		return fmt.Errorf("departments form a cycle: %s", strings.Join(c, " -> "))
	}
	for id := range parent {
		depth := 1
		for p := parent[id]; p != ""; p = parent[p] {
			depth++
		}
		if depth > MaxDepth {
			return fmt.Errorf("department %q is nested too deep (max %d levels)", id, MaxDepth)
		}
	}
	return nil
}

func validateDepartmentFields(d Department) error {
	if strings.TrimSpace(d.ID) == "" || len(d.ID) > 64 {
		return fmt.Errorf("department id is required (max 64 characters)")
	}
	name := strings.TrimSpace(d.Name)
	if name == "" || utf8.RuneCountInString(name) > MaxNameRunes {
		return fmt.Errorf("department %q: name is required (max %d characters)", d.ID, MaxNameRunes)
	}
	if d.ParentID == d.ID {
		return fmt.Errorf("department %q cannot be its own parent", d.ID)
	}
	if d.CostCenter != "" && !costCenterRe.MatchString(d.CostCenter) {
		return fmt.Errorf("department %q: cost center must be 1-%d letters, digits, dot, dash or underscore", d.ID, MaxCostCenterLen)
	}
	return validateRef(d.Head, "head")
}

func validateRef(r Ref, what string) error {
	if r.IsZero() {
		return nil
	}
	if (r.Kind != KindPerson && r.Kind != KindAgent) || strings.TrimSpace(r.ID) == "" || len(r.ID) > 64 {
		return fmt.Errorf("%s must be a person or an agent with an id", what)
	}
	return nil
}

// WouldCycle reports whether giving department id the parent newParent would create a cycle.
func WouldCycle(deps []Department, id, newParent string) bool {
	parent := map[string]string{}
	for _, d := range deps {
		parent[d.ID] = d.ParentID
	}
	parent[id] = newParent
	return DetectCycle(parent) != nil
}

// Descendants returns the ids of a department and all its sub-departments.
func Descendants(deps []Department, id string) map[string]bool {
	kids := map[string][]string{}
	for _, d := range deps {
		kids[d.ParentID] = append(kids[d.ParentID], d.ID)
	}
	out := map[string]bool{id: true}
	stack := []string{id}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, k := range kids[n] {
			if !out[k] {
				out[k] = true
				stack = append(stack, k)
			}
		}
	}
	return out
}

// ReportingCycle checks the reporting lines (who reports to whom, people and agents together) of an
// org: edges is "kind:id" -> "kind:id". It returns the cycle or nil.
func ReportingCycle(edges map[string]string) []string { return DetectCycle(edges) }
