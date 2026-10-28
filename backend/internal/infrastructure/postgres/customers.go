package postgres

import (
	"context"
	"fmt"
	"time"

	"aiworkforce/backend/internal/customers"
	"aiworkforce/backend/internal/domain"
)

// CustomerStore implements customers.Store on Postgres (migration 797). Every call runs through WithOrgTx, so
// Row-Level Security isolates the organizations; the org_id filters are a second barrier.
type CustomerStore struct{ S *Store }

var _ customers.Store = (*CustomerStore)(nil)

const customerCols = `id, org_id, name, tax_id, color, notes, folder_id, created_by, created_at, updated_at, archived_at`

func scanCustomer(r scanner) (customers.Customer, error) {
	var c customers.Customer
	err := r.Scan(&c.ID, &c.OrgID, &c.Name, &c.TaxID, &c.Color, &c.Notes, &c.FolderID, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt, &c.ArchivedAt)
	c.Archived = c.ArchivedAt != nil
	return c, err
}

func conflictOr(err error, msg string) error {
	if isUnique(err) {
		return fmt.Errorf("%w: %s", domain.ErrConflict, msg)
	}
	return err
}

func (s *CustomerStore) CreateCustomer(ctx context.Context, c customers.Customer) error {
	err := s.S.exec(ctx, c.OrgID, `INSERT INTO customers (org_id, id, name, tax_id, color, notes, folder_id, created_by, created_at, updated_at, archived_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		c.OrgID, c.ID, c.Name, c.TaxID, c.Color, c.Notes, c.FolderID, c.CreatedBy, c.CreatedAt, c.UpdatedAt, c.ArchivedAt)
	return conflictOr(err, "a customer with that name already exists")
}

func (s *CustomerStore) UpdateCustomer(ctx context.Context, c customers.Customer) error {
	var archived *time.Time
	if c.Archived {
		archived = c.ArchivedAt
		if archived == nil {
			now := time.Now().UTC()
			archived = &now
		}
	}
	tag, err := s.S.execTag(ctx, c.OrgID, `UPDATE customers SET name=$3, tax_id=$4, color=$5, notes=$6, folder_id=$7, updated_at=$8, archived_at=$9
		WHERE org_id=$1 AND id=$2`, c.OrgID, c.ID, c.Name, c.TaxID, c.Color, c.Notes, c.FolderID, c.UpdatedAt, archived)
	if err != nil {
		return conflictOr(err, "a customer with that name already exists")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: customer", domain.ErrNotFound)
	}
	return nil
}

func (s *CustomerStore) GetCustomer(ctx context.Context, org, id string) (customers.Customer, error) {
	c, err := one(ctx, s.S, org, scanCustomer, `SELECT `+customerCols+` FROM customers WHERE org_id=$1 AND id=$2`, org, id)
	return c, mapErr(err)
}

func (s *CustomerStore) ListCustomers(ctx context.Context, org string, includeArchived bool) ([]customers.Customer, error) {
	out, err := many(ctx, s.S, org, scanCustomer, `SELECT `+customerCols+` FROM customers
		WHERE org_id=$1 AND ($2 OR archived_at IS NULL) ORDER BY lower(name), id`, org, includeArchived)
	if out == nil {
		out = []customers.Customer{}
	}
	return out, err
}

const customerCloneCols = `id, customer_id, agent_id, display_name, created_by, created_at`

func scanCustomerClone(r scanner) (customers.Assignment, error) {
	var a customers.Assignment
	err := r.Scan(&a.ID, &a.CustomerID, &a.AgentID, &a.DisplayName, &a.CreatedBy, &a.CreatedAt)
	return a, err
}

func (s *CustomerStore) AddAssignment(ctx context.Context, org string, a customers.Assignment) error {
	err := s.S.exec(ctx, org, `INSERT INTO customer_assignments (org_id, id, customer_id, agent_id, display_name, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, org, a.ID, a.CustomerID, a.AgentID, a.DisplayName, a.CreatedBy, a.CreatedAt)
	return conflictOr(err, "the agent already works for this customer")
}

func (s *CustomerStore) DeleteAssignment(ctx context.Context, org, customerID, id string) error {
	tag, err := s.S.execTag(ctx, org, `DELETE FROM customer_assignments WHERE org_id=$1 AND customer_id=$2 AND id=$3`, org, customerID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: assignment", domain.ErrNotFound)
	}
	return nil
}

func (s *CustomerStore) Assignments(ctx context.Context, org, customerID string) ([]customers.Assignment, error) {
	out, err := many(ctx, s.S, org, scanCustomerClone, `SELECT `+customerCloneCols+` FROM customer_assignments
		WHERE org_id=$1 AND ($2='' OR customer_id=$2) ORDER BY created_at, id`, org, customerID)
	if out == nil {
		out = []customers.Assignment{}
	}
	return out, err
}

const customerLinkCols = `kind, ref_id, customer_id, created_by, created_at`

func scanCustomerLink(r scanner) (customers.Link, error) {
	var l customers.Link
	err := r.Scan(&l.Kind, &l.RefID, &l.CustomerID, &l.CreatedBy, &l.CreatedAt)
	return l, err
}

func (s *CustomerStore) AddLink(ctx context.Context, org string, l customers.Link) error {
	err := s.S.exec(ctx, org, `INSERT INTO customer_links (org_id, kind, ref_id, customer_id, created_by, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		org, l.Kind, l.RefID, l.CustomerID, l.CreatedBy, l.CreatedAt)
	return conflictOr(err, "already scoped to a customer")
}

func (s *CustomerStore) DeleteLink(ctx context.Context, org, kind, refID string) error {
	tag, err := s.S.execTag(ctx, org, `DELETE FROM customer_links WHERE org_id=$1 AND kind=$2 AND ref_id=$3`, org, kind, refID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: link", domain.ErrNotFound)
	}
	return nil
}

func (s *CustomerStore) LinkOf(ctx context.Context, org, kind, refID string) (customers.Link, error) {
	l, err := one(ctx, s.S, org, scanCustomerLink, `SELECT `+customerLinkCols+` FROM customer_links WHERE org_id=$1 AND kind=$2 AND ref_id=$3`, org, kind, refID)
	return l, mapErr(err)
}

func (s *CustomerStore) Links(ctx context.Context, org, customerID, kind string, limit int) ([]customers.Link, error) {
	if limit <= 0 || limit > 10000 {
		limit = 10000
	}
	out, err := many(ctx, s.S, org, scanCustomerLink, `SELECT `+customerLinkCols+` FROM customer_links
		WHERE org_id=$1 AND kind=$2 AND ($3='' OR customer_id=$3) ORDER BY created_at DESC, ref_id LIMIT $4`, org, kind, customerID, limit)
	if out == nil {
		out = []customers.Link{}
	}
	return out, err
}
