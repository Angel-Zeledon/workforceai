package projects

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Customer isolation of the artifacts a plan references (B12, docs/architecture/customer-district.md). A person can
// point a task at an artifact by writing "artifact:<id>" in the plan (goal, titles, descriptions, plan changes); the
// backend hands its text to the task (W3, application/context_flow.go). A project scoped to customer C may only
// reference artifacts tagged C: an artifact of another customer, or one without any customer (it may hold anyone's
// data), is refused HERE, when the plan is written, with a clear error. The artifacts service filters again when
// the task runs (artifacts/context_source.go), so a reference that slips past this check still never leaks.

// CodeCustomerIsolation is the error code of a refused reference.
const CodeCustomerIsolation = "customer_isolation"

// planText is everything of a plan that reaches the tasks (and so may carry artifact references): the goal and,
// for every task, its title and the description the orchestrator receives.
func planText(r Record) string {
	var b strings.Builder
	b.WriteString(r.Goal)
	for _, t := range planOf(r).Tasks {
		b.WriteString("\n")
		b.WriteString(t.Title)
		b.WriteString("\n")
		b.WriteString(t.Description)
	}
	return b.String()
}

// checkArtifactRefs refuses a plan of a customer-scoped project that references an artifact of another customer
// or of none. A reference to an artifact that does not exist is left to the task-time filter (nothing to leak).
func (s *Service) checkArtifactRefs(ctx context.Context, r Record) error {
	if r.CustomerID == "" {
		return nil
	}
	ids := application.ArtifactRefIDs(planText(r))
	if len(ids) == 0 {
		return nil
	}
	if s.cfg.ArtifactCustomer == nil {
		return fmt.Errorf("%w: %s: the plan references artifacts but they cannot be checked against the project's customer", domain.ErrInvalid, CodeCustomerIsolation)
	}
	org := r.OrgID
	if org == "" {
		org = s.org(ctx)
	}
	for _, id := range ids {
		cid, err := s.cfg.ArtifactCustomer(ctx, org, id)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: artifact %s could not be checked: %w", CodeCustomerIsolation, id, err)
		}
		switch {
		case cid == "":
			return fmt.Errorf("%w: %s: the plan references artifact %s, which is not tagged with any customer; a customer's project may only use that customer's artifacts", domain.ErrInvalid, CodeCustomerIsolation, id)
		case cid != r.CustomerID:
			return fmt.Errorf("%w: %s: the plan references artifact %s, which belongs to another customer; a customer's project may only use that customer's artifacts", domain.ErrInvalid, CodeCustomerIsolation, id)
		}
	}
	return nil
}

// checkChangedRefs checks the plan a change would leave (nodes replace the record's).
func (s *Service) checkChangedRefs(ctx context.Context, r Record, nodes []NodeDef) error {
	r.Nodes = nodes
	return s.checkArtifactRefs(ctx, r)
}
