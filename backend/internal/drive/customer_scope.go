package drive

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Customer isolation of the drive (B12, docs/architecture/customer-district.md).
//
// When the task an agent works on is scoped to a customer (application.CustomerFrom(ctx) != ""),
// the zone policy is narrowed so that nothing of ANOTHER customer can be read or written:
//
//	owned by the task's customer   /Clients/<its folder>, its projects' folders, folders a human linked
//	                               to it, and the agent's per-customer desk /Agents/<me>/Clients/<id>:
//	                               readable; the per-customer desk and assigned projects stay free to write
//	owned by another customer      not readable, writes refused (customer_isolation)
//	/Shared                        readable (firm-wide material curated by people); writes refused
//	anything else                  (the general desk, unscoped projects, unlinked client folders):
//	                               not readable, writes refused, because it may hold anyone's data
//
// A task without a customer keeps the policy of agent.go unchanged.

// CustomerDirectory tells which customer owns what (implemented in cmd/server on top of the customers
// and projects stores). Errors fail closed: the scoped agent then reads nothing.
type CustomerDirectory interface {
	// ProjectCustomer is the customer a project is scoped to ("" = none).
	ProjectCustomer(ctx context.Context, org, projectID string) (string, error)
	// LinkedFolders maps the folder ids a person linked to a customer to that customer.
	LinkedFolders(ctx context.Context, org string) (map[string]string, error)
}

// Zones that exist only inside a customer-scoped task.
const (
	ZoneCustomer      = "customer"       // owned by the task's customer (read; writes by approval unless desk/project)
	ZoneOtherCustomer = "other_customer" // owned by another customer: never readable, never writable
	ZoneUnscoped      = "unscoped"       // not owned by any customer and not /Shared: closed in a scoped task
)

// unknownOwner owns client folders that are linked to no customer: in a scoped task they are foreign.
const unknownOwner = "\x00unlinked"

// ErrCustomerIsolation is the deny code of a write that would cross the customer boundary.
const denyCustomerIsolation = "customer_isolation"

type ownedPath struct {
	path     string
	customer string
}

// CustomerDesk is the per-customer desk of an agent: what its clone for that customer writes.
func CustomerDesk(agentID, customerID string) string {
	return JoinPath(RootAgents, agentID, RootClients, customerID)
}

// loadCustomerScope fills the ownership map of a customer-scoped task.
func (a *AgentFS) loadCustomerScope(ctx context.Context, org string, sc *scope) error {
	if a.Customers == nil {
		return fmt.Errorf("%w: customer scope without a customer directory", domain.ErrInvalid)
	}
	add := func(p, c string) { sc.owned = append(sc.owned, ownedPath{path: p, customer: c}) }
	kids := func(root string) ([]Folder, error) {
		top, err := a.Svc.cfg.Store.FolderByPath(ctx, org, "/"+root)
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return a.Svc.cfg.Store.ChildFolders(ctx, org, top.ID)
	}
	clients, err := kids(RootClients)
	if err != nil {
		return err
	}
	for _, k := range clients {
		owner := k.RefID
		if owner == "" {
			owner = unknownOwner
		}
		add(k.Path, owner)
	}
	projs, err := kids(RootProjects)
	if err != nil {
		return err
	}
	for _, k := range projs {
		if k.RefID == "" {
			continue
		}
		cid, err := a.Customers.ProjectCustomer(ctx, org, k.RefID)
		if err != nil {
			return err
		}
		if cid != "" {
			add(k.Path, cid)
		}
	}
	links, err := a.Customers.LinkedFolders(ctx, org)
	if err != nil {
		return err
	}
	for fid, cid := range links {
		f, err := a.Svc.cfg.Store.FolderByID(ctx, org, fid)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if f.DeletedAt == nil {
			add(f.Path, cid)
		}
	}
	// The deepest owner wins (a folder linked to a customer inside another one's area).
	sort.SliceStable(sc.owned, func(i, j int) bool { return len(sc.owned[i].path) > len(sc.owned[j].path) })
	return nil
}

// ownerOf is the customer that owns a path ("" and false when nobody does).
func (sc scope) ownerOf(p string) (string, bool) {
	if segs, _ := Segments(p); len(segs) >= 4 && segs[0] == RootAgents && segs[2] == RootClients {
		// /Agents/<agent>/Clients/<customer>/...: a per-customer desk (any agent's).
		for _, o := range sc.owned {
			if IsUnder(p, o.path) && len(o.path) > len(JoinPath(segs[:4]...)) {
				return o.customer, true
			}
		}
		return segs[3], true
	}
	for _, o := range sc.owned {
		if IsUnder(p, o.path) {
			return o.customer, true
		}
	}
	return "", false
}

// customerZone narrows a zone inside a customer-scoped task (sc.customer != "").
func (sc scope) customerZone(base, p string) string {
	owner, owned := sc.ownerOf(p)
	switch {
	case owned && owner != sc.customer:
		return ZoneOtherCustomer
	case owned:
		if base == ZoneDesk || base == ZoneProject {
			return base
		}
		return ZoneCustomer
	case base == ZoneShared:
		return ZoneShared
	}
	return ZoneUnscoped
}

// writeRefused reports whether a write into the zone is refused outright (not even a human can
// approve it): in a customer-scoped task, everything that is not the customer's own.
func (sc scope) writeRefused(z string) bool {
	return sc.customer != "" && z != ZoneDesk && z != ZoneProject && z != ZoneCustomer
}

// customerRoots are the readable roots of a customer-scoped task.
func (a *AgentFS) customerRoots(sc scope) []string {
	roots := []string{CustomerDesk(sc.agent, sc.customer), "/" + RootShared}
	for _, o := range sc.owned {
		if o.customer == sc.customer {
			roots = append(roots, o.path)
		}
	}
	return roots
}

// customerFromCtx is the customer the call works for ("" = none).
func customerFromCtx(ctx context.Context) string { return application.CustomerFrom(ctx) }

// ReadableFilter returns a predicate telling whether the agent may read a path in the scope of ctx
// (defense in depth for search hits under a readable root that contain a foreign customer's folder).
func (a *AgentFS) ReadableFilter(ctx context.Context, agentID string) (func(path string) bool, error) {
	sc, err := a.scopeOf(ctx, agentID)
	if err != nil {
		return nil, err
	}
	org := a.Svc.org(ctx)
	return func(p string) bool { return canRead(a.zoneAll(ctx, org, sc, p)) }, nil
}
