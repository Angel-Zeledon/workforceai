package domain

import (
	"errors"
	"fmt"
)

// PlanLimitError is a count limit of the organization's plan (policy.Catalog) that refused one more item. It is a
// conflict (HTTP 409); the API adds code "plan_limit" with the kind, the limit and the plan so the UI can offer an upgrade.
type PlanLimitError struct {
	Kind  string `json:"limit_kind"` // policy.LimitAgents, LimitProjects, LimitSched, LimitSeats, LimitStorage
	Limit int    `json:"limit"`
	Plan  string `json:"plan"`
}

func (e *PlanLimitError) Error() string {
	return fmt.Sprintf("plan_limit: %s (%d) on the %s plan", e.Kind, e.Limit, e.Plan)
}

func (e *PlanLimitError) Unwrap() error { return ErrConflict }

// AsPlanLimit returns the plan limit behind err, if any.
func AsPlanLimit(err error) (*PlanLimitError, bool) {
	var pe *PlanLimitError
	ok := errors.As(err, &pe)
	return pe, ok
}
