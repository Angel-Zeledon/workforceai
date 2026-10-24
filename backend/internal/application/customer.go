package application

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"aiworkforce/backend/internal/domain"
)

// Customer scope (B12, docs/architecture/customer-district.md). An accounting firm works for many
// clients; a request (and the project behind it) can be scoped to ONE customer of the organization.
// The scope is stored when the request is created, before anything runs, and every place that hands
// stored knowledge to an agent (memory recall, memory.search, fs.*) resolves it from the request id and
// never returns data of ANOTHER customer. A request without a customer keeps the old behaviour.

type customerCtxKey struct{}

// WithCustomer marks ctx as working for one customer ("" leaves ctx unchanged).
func WithCustomer(ctx context.Context, customerID string) context.Context {
	customerID = strings.TrimSpace(customerID)
	if customerID == "" {
		return ctx
	}
	return context.WithValue(ctx, customerCtxKey{}, customerID)
}

// CustomerFrom is the customer ctx works for ("" = not scoped to a customer).
func CustomerFrom(ctx context.Context) string {
	v, _ := ctx.Value(customerCtxKey{}).(string)
	return v
}

// CustomerScope is implemented by internal/customers (installed by the server).
type CustomerScope interface {
	// LinkRequest scopes a new request to a customer. It fails when the customer does not exist, is
	// archived or the request is already scoped to another customer.
	LinkRequest(ctx context.Context, org, requestID, customerID string) error
	// CustomerOfRequest is the customer a request is scoped to ("" = none). An error means "unknown":
	// callers must fail closed (no memories, no files).
	CustomerOfRequest(ctx context.Context, org, requestID string) (string, error)
	// AssignedAgents lists the agents with a clone for the customer (empty = no restriction).
	AssignedAgents(ctx context.Context, org, customerID string) ([]string, error)
}

// CustomerChecker is optionally implemented by the CustomerScope: it accepts a customer new work can be scoped
// to (it exists and is not archived). The chat uses it to refuse a bad customer at once, before the turn runs
// asynchronously (LinkRequest checks it again when the turn creates a request).
type CustomerChecker interface {
	CheckCustomer(ctx context.Context, org, customerID string) error
}

// SetCustomerScope installs the customer scope (nil removes it).
func (o *Orchestrator) SetCustomerScope(c CustomerScope) { o.customers = c }

// checkCustomer validates the customer in ctx ("" = none, always fine) for work that will be scoped later.
func (o *Orchestrator) checkCustomer(ctx context.Context) error {
	cid := CustomerFrom(ctx)
	if cid == "" {
		return nil
	}
	if o.customers == nil {
		return fmt.Errorf("%w: customers are not available", domain.ErrInvalid)
	}
	if ch, ok := o.customers.(CustomerChecker); ok {
		return ch.CheckCustomer(ctx, o.org(ctx), cid)
	}
	return nil
}

// linkCustomer stores the customer of a new request before it is created (so no task can ever run
// unscoped). Without a customer in ctx it does nothing; with one but without a scope service it refuses.
func (o *Orchestrator) linkCustomer(ctx context.Context, requestID string) error {
	cid := CustomerFrom(ctx)
	if cid == "" {
		return nil
	}
	if o.customers == nil {
		return fmt.Errorf("%w: customers are not available", domain.ErrInvalid)
	}
	return o.customers.LinkRequest(ctx, o.org(ctx), requestID, cid)
}

// plannerAgents narrows the agents the planner may use for a customer-scoped request to the ones with
// a clone for that customer (when the customer has any). The assistant is never removed.
func (o *Orchestrator) plannerAgents(ctx context.Context, rs *run, all []domain.Agent) []domain.Agent {
	if o.customers == nil || rs.customer == "" {
		return all
	}
	ids, err := o.customers.AssignedAgents(ctx, o.org(ctx), rs.customer)
	if err != nil || len(ids) == 0 {
		return all
	}
	out := make([]domain.Agent, 0, len(ids))
	clones := 0
	for _, a := range all {
		switch {
		case a.ID == assistantID:
			out = append(out, a)
		case slices.Contains(ids, a.ID):
			out = append(out, a)
			clones++
		}
	}
	if clones == 0 { // every clone points to an agent that no longer exists: do not leave the planner empty
		return all
	}
	return out
}

// ScopeCustomer resolves the customer of a request and puts it in ctx, for the tools that hand stored
// data to an agent. ok=false means the scope could not be determined: the caller must refuse.
func ScopeCustomer(ctx context.Context, resolve func(ctx context.Context, org, requestID string) (string, error), org, requestID string) (context.Context, bool) {
	if resolve == nil || requestID == "" {
		return ctx, true
	}
	cid, err := resolve(ctx, org, requestID)
	if err != nil {
		return ctx, false
	}
	return WithCustomer(ctx, cid), true
}
