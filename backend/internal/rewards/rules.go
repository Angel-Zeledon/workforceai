package rewards

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Rules are the earning rules, as data. Defaults are in DefaultRules; an
// operator may override any field with REWARDS_RULES (JSON, same field names)
// and switch the whole system off with REWARDS_ENABLED=false.
//
//	task paid        = Base[complexity]                              (no review, or review passed after rework: see below)
//	first-try pass   = Base + Base*FirstTryBonusPct/100              (a quality review judged the first output "pass")
//	needed rework    = Base*ReworkPct/100                            (the review asked for rework, or rework was exhausted)
//	project finished = min(ProjectBase + ProjectPerTask*tasksDone, ProjectMax)
//
// Zero coins, always: tasks that failed, were skipped, cancelled or blocked
// (no task.completed ever fires for them), milestones and human gates (no
// work was done), tasks with a rejected approval, and projects that did not
// finish "done".
type Rules struct {
	Enabled           bool             `json:"enabled"`
	Base              map[string]int64 `json:"base"` // by complexity S|M|L|XL
	DefaultComplexity string           `json:"default_complexity"`
	FirstTryBonusPct  int64            `json:"first_try_bonus_pct"`
	ReworkPct         int64            `json:"rework_pct"`
	ProjectBase       int64            `json:"project_base"`
	ProjectPerTask    int64            `json:"project_per_task"`
	ProjectMax        int64            `json:"project_max"`
	MinProjectTasks   int              `json:"min_project_tasks"`
}

// DefaultRules is what runs when nothing is configured.
func DefaultRules() Rules {
	return Rules{
		Enabled:           true,
		Base:              map[string]int64{"S": 5, "M": 10, "L": 20, "XL": 40},
		DefaultComplexity: "M",
		FirstTryBonusPct:  50,
		ReworkPct:         50,
		ProjectBase:       20,
		ProjectPerTask:    2,
		ProjectMax:        100,
		MinProjectTasks:   2,
	}
}

// ParseRules applies a JSON override on top of the defaults. Empty input
// returns the defaults. Invalid values are rejected so a typo never silently
// changes the economy.
func ParseRules(raw string) (Rules, error) {
	r := DefaultRules()
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return r, nil
	}
	var o struct {
		Enabled           *bool            `json:"enabled"`
		Base              map[string]int64 `json:"base"`
		DefaultComplexity *string          `json:"default_complexity"`
		FirstTryBonusPct  *int64           `json:"first_try_bonus_pct"`
		ReworkPct         *int64           `json:"rework_pct"`
		ProjectBase       *int64           `json:"project_base"`
		ProjectPerTask    *int64           `json:"project_per_task"`
		ProjectMax        *int64           `json:"project_max"`
		MinProjectTasks   *int             `json:"min_project_tasks"`
	}
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		return r, fmt.Errorf("REWARDS_RULES: %w", err)
	}
	if o.Enabled != nil {
		r.Enabled = *o.Enabled
	}
	for k, v := range o.Base {
		k = strings.ToUpper(k)
		if v < 0 || (k != "S" && k != "M" && k != "L" && k != "XL") {
			return r, fmt.Errorf("REWARDS_RULES: invalid base %q=%d", k, v)
		}
		r.Base[k] = v
	}
	if o.DefaultComplexity != nil {
		c := strings.ToUpper(*o.DefaultComplexity)
		if _, ok := r.Base[c]; !ok {
			return r, fmt.Errorf("REWARDS_RULES: invalid default_complexity %q", c)
		}
		r.DefaultComplexity = c
	}
	pct := func(p *int64, dst *int64, name string) error {
		if p == nil {
			return nil
		}
		if *p < 0 || *p > 1000 {
			return fmt.Errorf("REWARDS_RULES: %s out of range", name)
		}
		*dst = *p
		return nil
	}
	for _, e := range []error{
		pct(o.FirstTryBonusPct, &r.FirstTryBonusPct, "first_try_bonus_pct"),
		pct(o.ReworkPct, &r.ReworkPct, "rework_pct"),
		pct(o.ProjectBase, &r.ProjectBase, "project_base"),
		pct(o.ProjectPerTask, &r.ProjectPerTask, "project_per_task"),
		pct(o.ProjectMax, &r.ProjectMax, "project_max"),
	} {
		if e != nil {
			return r, e
		}
	}
	if o.MinProjectTasks != nil && *o.MinProjectTasks >= 0 {
		r.MinProjectTasks = *o.MinProjectTasks
	}
	return r, nil
}

// TaskFacts is what the earning rule needs to know about a finished task.
type TaskFacts struct {
	Complexity string // S|M|L|XL, "" = default
	Work       bool   // false for milestones and human gates
	Rejected   bool   // a human rejected an action of this task
	Reviewed   bool   // a quality review judged the output
	Verdict    string // pass|rework|fail ("" when not reviewed)
	Reworks    int    // extra runs the review caused
	Exhausted  bool   // the bound of reworks was reached with a "rework" verdict
}

// Payout is the result of the earning rule for one task.
type Payout struct {
	Amount     int64
	Reason     string
	Complexity string
	Base       int64
	Bonus      int64
}

// ForTask applies the rules; Amount 0 means "nothing is paid".
func (r Rules) ForTask(f TaskFacts) Payout {
	if !r.Enabled || !f.Work || f.Rejected || f.Verdict == "fail" {
		return Payout{}
	}
	cx := strings.ToUpper(strings.TrimSpace(f.Complexity))
	base, ok := r.Base[cx]
	if !ok {
		cx = r.DefaultComplexity
		base = r.Base[cx]
	}
	p := Payout{Complexity: cx, Base: base, Amount: base, Reason: ReasonTaskDone}
	switch {
	case f.Reviewed && (f.Reworks > 0 || f.Exhausted || f.Verdict == "rework"):
		p.Amount = base * r.ReworkPct / 100
		p.Reason = ReasonTaskRework
	case f.Reviewed && f.Verdict == "pass":
		p.Bonus = base * r.FirstTryBonusPct / 100
		p.Amount = base + p.Bonus
		p.Reason = ReasonTaskFirstTry
	}
	return p
}

// ForProject is the completion bonus of a project that finished "done" with
// tasksDone finished tasks.
func (r Rules) ForProject(tasksDone int) int64 {
	if !r.Enabled || tasksDone < r.MinProjectTasks || tasksDone <= 0 {
		return 0
	}
	return min(r.ProjectBase+r.ProjectPerTask*int64(tasksDone), r.ProjectMax)
}
