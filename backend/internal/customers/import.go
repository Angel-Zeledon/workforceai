package customers

import (
	"context"
	"errors"
	"strings"

	"aiworkforce/backend/internal/textfold"
)

// Importing a list of customers (projects for many customers, docs/superpowers/specs/2026-11-03-proyectos-por-
// cliente-y-ayuda-design.md part 1.2). A row is "the same customer" when its e-mail matches; without an e-mail, its tax
// id; without one, its normalized name (case, accents and spaces ignored). The preview creates nothing: it shows which
// rows already exist, which will be created and which have errors, and the person can correct each match before the
// import (ApplyImport with choices). New customers are normal customers (with their folder).

// ImportRow is one row of the list (CSV columns: name, email, phone, company, notes, tax_id, contact). A customer may be
// a person or a company: with a company, the company is the customer's name and the row's name its contact person.
type ImportRow struct {
	Name        string `json:"name"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	Company     string `json:"company"`
	Notes       string `json:"notes"`
	TaxID       string `json:"tax_id"`
	ContactName string `json:"contact_name"`
}

// ImportResult is what happens (preview) or happened (apply) to one row.
type ImportResult struct {
	Row          int    `json:"row"`
	Action       string `json:"action"` // preview: existing|new|error; apply: existing|created|skipped|error
	CustomerID   string `json:"customer_id,omitempty"`
	CustomerName string `json:"customer_name,omitempty"` // the existing customer it matched
	MatchedBy    string `json:"matched_by,omitempty"`    // email|tax_id|name|choice
	Reason       string `json:"reason,omitempty"`        // name_required|invalid_email|duplicate_in_file|too_long|name_taken|unknown_customer
	// The customer as the row describes it (what would be created).
	Name        string `json:"name"`
	ContactName string `json:"contact_name,omitempty"`
	Email       string `json:"email,omitempty"`
	Phone       string `json:"phone,omitempty"`
}

// Choices of ApplyImport per row: create it, skip it, or a customer id to use instead of the automatic match.
const (
	ChoiceNew  = "new"
	ChoiceSkip = "skip"
	// MaxImportRows bounds one import.
	MaxImportRows = 1000
)

func nameKey(s string) string { return strings.Join(strings.Fields(textfold.Fold(clean(s))), " ") }

func (r ImportRow) customer() (name, contact string) {
	name, contact = clean(r.Company), clean(r.ContactName)
	if name == "" {
		return clean(r.Name), contact
	}
	if contact == "" {
		contact = clean(r.Name)
	}
	return name, contact
}

// PreviewImport matches every row against the customers of the organization and the rows before it.
func (s *Service) PreviewImport(ctx context.Context, rows []ImportRow) ([]ImportResult, error) {
	if len(rows) > MaxImportRows {
		return nil, invalid("at most %d rows per import", MaxImportRows)
	}
	cs, err := s.cfg.Store.ListCustomers(ctx, s.org(ctx), false)
	if err != nil {
		return nil, err
	}
	byEmail, byTax, byName := map[string]Customer{}, map[string]Customer{}, map[string]Customer{}
	for _, c := range cs {
		if c.Email != "" {
			byEmail[c.Email] = c
		}
		if t := strings.ToLower(clean(c.TaxID)); t != "" {
			byTax[t] = c
		}
		byName[nameKey(c.Name)] = c
	}
	seen := map[string]int{}
	out := make([]ImportResult, 0, len(rows))
	for i, r := range rows {
		name, contact := r.customer()
		res := ImportResult{Row: i, Name: name, ContactName: contact, Email: NormalizeEmail(r.Email), Phone: clean(r.Phone)}
		raw := clean(r.Email)
		switch {
		case name == "":
			res.Action, res.Reason = "error", "name_required"
		case raw != "" && res.Email == "":
			res.Action, res.Reason = "error", "invalid_email"
		case len([]rune(name)) > maxName || len([]rune(contact)) > maxContact || len([]rune(res.Phone)) > maxPhone || len([]rune(clean(r.TaxID))) > maxTaxID:
			res.Action, res.Reason = "error", "too_long"
		}
		if res.Action == "error" {
			out = append(out, res)
			continue
		}
		tax := strings.ToLower(clean(r.TaxID))
		key := "n:" + nameKey(name)
		var hit Customer
		var ok bool
		switch {
		case res.Email != "":
			key = "e:" + res.Email
			hit, ok = byEmail[res.Email]
			res.MatchedBy = "email"
		case tax != "":
			key = "t:" + tax
			hit, ok = byTax[tax]
			res.MatchedBy = "tax_id"
		default:
			hit, ok = byName[nameKey(name)]
			res.MatchedBy = "name"
		}
		if j, dup := seen[key]; dup {
			res.Action, res.Reason, res.MatchedBy = "error", "duplicate_in_file", ""
			res.CustomerName = out[j].Name
			out = append(out, res)
			continue
		}
		seen[key] = i
		if ok {
			res.Action, res.CustomerID, res.CustomerName = "existing", hit.ID, hit.Name
		} else {
			res.Action, res.MatchedBy = "new", ""
		}
		out = append(out, res)
	}
	return out, nil
}

// ApplyImport creates the new customers of the list (with their folder) and returns, per row, the customer it stands
// for. choices override the automatic match of a row (ChoiceNew, ChoiceSkip or an existing customer id); rows with an
// error are never created.
func (s *Service) ApplyImport(ctx context.Context, actor string, rows []ImportRow, choices map[int]string) ([]ImportResult, error) {
	pre, err := s.PreviewImport(ctx, rows)
	if err != nil {
		return nil, err
	}
	for i := range pre {
		res := &pre[i]
		choice := choices[i]
		switch {
		case res.Action == "error" && choice != ChoiceSkip:
			continue
		case choice == ChoiceSkip:
			res.Action, res.CustomerID = "skipped", ""
			continue
		case choice != "" && choice != ChoiceNew:
			c, err := s.Active(ctx, s.org(ctx), choice)
			if err != nil {
				res.Action, res.Reason = "error", "unknown_customer"
				continue
			}
			res.Action, res.CustomerID, res.CustomerName, res.MatchedBy = "existing", c.ID, c.Name, "choice"
			continue
		case res.Action == "existing" && choice != ChoiceNew:
			continue
		}
		r := rows[i]
		name, contact := r.customer()
		notes, tax := r.Notes, r.TaxID
		in := Input{Name: &name, ContactName: &contact, Email: &res.Email, Phone: &res.Phone, Notes: &notes, TaxID: &tax}
		c, err := s.Create(ctx, actor, in)
		if err != nil {
			res.Action = "error"
			res.Reason = "invalid"
			if errors.Is(err, ErrConflict) {
				res.Reason = "name_taken"
			}
			continue
		}
		res.Action, res.CustomerID, res.CustomerName = "created", c.ID, c.Name
	}
	s.audit(ctx, s.org(ctx), actor, "customer.imported", "", map[string]any{"rows": len(rows)})
	return pre, nil
}
