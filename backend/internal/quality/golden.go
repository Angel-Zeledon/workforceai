package quality

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"aiworkforce/backend/internal/domain"
)

// Golden cases are a small, organization-defined set of "input -> expected checks" for one agent. The platform
// stores them and records pass/fail runs; there is no LLM judge. A run is reported by a person or an external
// harness, either as a verdict or as the agent's output text, which is checked here with plain string checks and
// never stored.

// Check kinds.
const (
	CheckContains    = "contains"     // the output contains Value (case-insensitive)
	CheckNotContains = "not_contains" // the output does not contain Value (case-insensitive)
)

// Limits.
const (
	MaxCasesPerAgent = 50
	MaxChecks        = 10
	MaxNameRunes     = 120
	MaxInputRunes    = 4000
	MaxValueRunes    = 500
	MaxNoteRunes     = 500
	MaxOutputBytes   = 64 << 10
)

// Check is one expectation on the agent's output.
type Check struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Case is one golden case.
type Case struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	Name      string    `json:"name"`
	Input     string    `json:"input"`
	Checks    []Check   `json:"checks"`
	Active    bool      `json:"active"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Run is the recorded result of running a case against its agent. Runs outlive their case (deleting a
// case never rewrites the agent's history).
type Run struct {
	ID           string    `json:"id"`
	CaseID       string    `json:"case_id"`
	AgentID      string    `json:"agent_id"`
	Passed       bool      `json:"passed"`
	ChecksPassed int       `json:"checks_passed"`
	ChecksTotal  int       `json:"checks_total"`
	TaskID       string    `json:"task_id,omitempty"`
	Note         string    `json:"note,omitempty"`
	RecordedBy   string    `json:"recorded_by"`
	CreatedAt    time.Time `json:"created_at"`
}

// GoldenCount is the run tally of one agent inside a range.
type GoldenCount struct{ Runs, Passed int }

// Store persists golden cases and runs. Every method is scoped by org.
type Store interface {
	CreateCase(ctx context.Context, org string, c Case) error
	GetCase(ctx context.Context, org, id string) (Case, error)
	UpdateCase(ctx context.Context, org string, c Case) error
	DeleteCase(ctx context.Context, org, id string) error
	// ListCases returns the cases of one agent (agentID "" = all), oldest first.
	ListCases(ctx context.Context, org, agentID string) ([]Case, error)
	AddRun(ctx context.Context, org string, r Run) error
	// ListRuns returns the newest runs of a case first.
	ListRuns(ctx context.Context, org, caseID string, limit int) ([]Run, error)
	// GoldenCounts tallies runs per agent whose created_at is in [from, to).
	GoldenCounts(ctx context.Context, org string, from, to time.Time) (map[string]GoldenCount, error)
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrInvalid, fmt.Sprintf(format, a...))
}

// CaseInput is what a client sends to create or replace a case.
type CaseInput struct {
	Name   string  `json:"name"`
	Input  string  `json:"input"`
	Checks []Check `json:"checks"`
	Active *bool   `json:"active"`
}

// normalize trims and validates the user supplied fields.
func (in CaseInput) normalize() (CaseInput, error) {
	in.Name, in.Input = strings.TrimSpace(in.Name), strings.TrimSpace(in.Input)
	switch {
	case in.Name == "":
		return in, invalid("name is required")
	case utf8.RuneCountInString(in.Name) > MaxNameRunes:
		return in, invalid("name is longer than %d characters", MaxNameRunes)
	case in.Input == "":
		return in, invalid("input is required")
	case utf8.RuneCountInString(in.Input) > MaxInputRunes:
		return in, invalid("input is longer than %d characters", MaxInputRunes)
	case len(in.Checks) == 0:
		return in, invalid("at least one check is required")
	case len(in.Checks) > MaxChecks:
		return in, invalid("at most %d checks", MaxChecks)
	}
	checks := make([]Check, 0, len(in.Checks))
	for _, c := range in.Checks {
		c.Kind, c.Value = strings.TrimSpace(c.Kind), strings.TrimSpace(c.Value)
		if c.Kind != CheckContains && c.Kind != CheckNotContains {
			return in, invalid("unknown check kind %q", c.Kind)
		}
		if c.Value == "" || utf8.RuneCountInString(c.Value) > MaxValueRunes {
			return in, invalid("a check value must have 1 to %d characters", MaxValueRunes)
		}
		checks = append(checks, c)
	}
	in.Checks = checks
	return in, nil
}

// Evaluate runs the checks of a case against an output and returns how many passed.
func Evaluate(checks []Check, output string) (passed int) {
	low := strings.ToLower(output)
	for _, c := range checks {
		has := strings.Contains(low, strings.ToLower(c.Value))
		if (c.Kind == CheckContains) == has {
			passed++
		}
	}
	return passed
}
