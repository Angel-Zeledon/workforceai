package customers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Folders is the drive as the customers see it (optional: without a drive customers have no folder).
type Folders interface {
	// EnsureCustomerFolder creates (idempotently) /Clients/<name> owned by the customer.
	EnsureCustomerFolder(ctx context.Context, customerID, name string) (id, path string, err error)
	// FolderPath is the current path of a folder (domain.ErrNotFound when it does not exist or is deleted).
	FolderPath(ctx context.Context, folderID string) (string, error)
}

// ProjectInfo is what the district needs of a project.
type ProjectInfo struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Light      string     `json:"light"`
	TasksDone  int        `json:"tasks_done"`
	TasksTotal int        `json:"tasks_total"`
	Awaiting   int        `json:"awaiting"`
	Failed     int        `json:"failed"`
	CustomerID string     `json:"-"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// RequestInfo is what the district needs of a request.
type RequestInfo struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// Work reads the projects and requests behind a customer (implemented in cmd/server).
type Work interface {
	// Projects lists the projects of the organization that are scoped to a customer.
	Projects(ctx context.Context, org string) ([]ProjectInfo, error)
	// ProjectCustomer is the customer a project is scoped to ("" = none).
	ProjectCustomer(ctx context.Context, org, projectID string) (string, error)
	Request(ctx context.Context, org, id string) (RequestInfo, error)
}

// Config wires the service.
type Config struct {
	Store Store
	OrgID string
	// Agents lists the agents of the organization (a clone needs an existing agent).
	Agents func(ctx context.Context, org string) ([]domain.Agent, error)
	// Audit records every write (ids and names only). nil = none.
	Audit func(ctx context.Context, org, actor, action, entityID string, details map[string]any)
	// WriteGate refuses writes (read-only mode); nil = always allowed.
	WriteGate func(ctx context.Context, org string) error
	Now       func() time.Time
	Log       *slog.Logger
}

// Service is the use-case layer of the customer district.
type Service struct {
	cfg     Config
	mu      sync.RWMutex
	folders Folders
	work    Work
	history History
}

var _ application.CustomerScope = (*Service)(nil)

// New builds the service. Folders and Work are installed later (the drive and the projects are wired after it).
func New(cfg Config) *Service {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Service{cfg: cfg}
}

// SetFolders installs the drive.
func (s *Service) SetFolders(f Folders) { s.mu.Lock(); s.folders = f; s.mu.Unlock() }

// SetWork installs the projects and requests reader.
func (s *Service) SetWork(w Work) { s.mu.Lock(); s.work = w; s.mu.Unlock() }

func (s *Service) deps() (Folders, Work) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.folders, s.work
}

func (s *Service) org(ctx context.Context) string { return application.OrgFrom(ctx, s.cfg.OrgID) }

func (s *Service) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (s *Service) audit(ctx context.Context, org, actor, action, id string, d map[string]any) {
	if s.cfg.Audit != nil {
		s.cfg.Audit(ctx, org, actor, action, id, d)
	}
}

func (s *Service) gate(ctx context.Context, org string) error {
	if s.cfg.WriteGate == nil {
		return nil
	}
	return s.cfg.WriteGate(ctx, org)
}

// ---- validation ----

const (
	maxName    = 120
	maxTaxID   = 40
	maxNotes   = 2000
	maxDisplay = 160
)

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// palette tints the buildings of customers created without a color (stable per name).
var palette = []string{"#2563eb", "#16a34a", "#d97706", "#db2777", "#7c3aed", "#0891b2", "#dc2626", "#65a30d"}

func defaultColor(name string) string {
	h := sha256.Sum256([]byte(strings.ToLower(name)))
	return palette[int(h[0])%len(palette)]
}

// clean removes control and invisible characters and collapses spaces (one line).
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) || r == 0x2028 || r == 0x2029 {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// cleanNotes keeps line breaks.
func cleanNotes(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\r':
			return -1
		case unicode.Is(unicode.Cc, r), unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, a...)...)
}

func (s *Service) apply(c *Customer, in Input) error {
	if in.Name != nil {
		n := clean(*in.Name)
		if n == "" || utf8.RuneCountInString(n) > maxName {
			return invalid("name must have between 1 and %d characters", maxName)
		}
		c.Name = n
	}
	if in.TaxID != nil {
		t := clean(*in.TaxID)
		if utf8.RuneCountInString(t) > maxTaxID {
			return invalid("tax_id must have at most %d characters", maxTaxID)
		}
		c.TaxID = t
	}
	if in.Color != nil {
		col := strings.TrimSpace(*in.Color)
		if col != "" && !colorRe.MatchString(col) {
			return invalid("color must be #rrggbb")
		}
		c.Color = strings.ToLower(col)
	}
	if in.Notes != nil {
		n := cleanNotes(*in.Notes)
		if utf8.RuneCountInString(n) > maxNotes {
			return invalid("notes must have at most %d characters", maxNotes)
		}
		c.Notes = n
	}
	if c.Color == "" {
		c.Color = defaultColor(c.Name)
	}
	return nil
}

// ---- customers ----

func (s *Service) withFolder(ctx context.Context, c Customer) Customer {
	folders, _ := s.deps()
	if folders == nil || c.FolderID == "" {
		return c
	}
	if p, err := folders.FolderPath(ctx, c.FolderID); err == nil {
		c.FolderPath = p
	}
	return c
}

// List returns the customers of the organization.
func (s *Service) List(ctx context.Context, includeArchived bool) ([]Customer, error) {
	cs, err := s.cfg.Store.ListCustomers(ctx, s.org(ctx), includeArchived)
	if err != nil {
		return nil, err
	}
	for i := range cs {
		cs[i] = s.withFolder(ctx, cs[i])
	}
	return cs, nil
}

// Get returns one customer.
func (s *Service) Get(ctx context.Context, id string) (Customer, error) {
	c, err := s.cfg.Store.GetCustomer(ctx, s.org(ctx), id)
	if err != nil {
		return Customer{}, err
	}
	return s.withFolder(ctx, c), nil
}

// Active returns the customer when it exists and is not archived (new work can be scoped to it).
func (s *Service) Active(ctx context.Context, org, id string) (Customer, error) {
	c, err := s.cfg.Store.GetCustomer(ctx, org, strings.TrimSpace(id))
	if err != nil {
		return Customer{}, err
	}
	if c.Archived {
		return Customer{}, fmt.Errorf("%w: the customer is archived", ErrConflict)
	}
	return c, nil
}

// Create adds a customer and its drive folder /Clients/<name> (best effort: a drive failure leaves it without one).
func (s *Service) Create(ctx context.Context, actor string, in Input) (Customer, error) {
	org := s.org(ctx)
	if in.Name == nil {
		return Customer{}, invalid("name is required")
	}
	if err := s.gate(ctx, org); err != nil {
		return Customer{}, err
	}
	now := s.now()
	c := Customer{ID: newID("cus_"), OrgID: org, CreatedBy: actor, CreatedAt: now, UpdatedAt: now}
	if err := s.apply(&c, in); err != nil {
		return Customer{}, err
	}
	if err := s.cfg.Store.CreateCustomer(ctx, c); err != nil {
		return Customer{}, err
	}
	if folders, _ := s.deps(); folders != nil {
		if fid, p, err := folders.EnsureCustomerFolder(ctx, c.ID, c.Name); err != nil {
			s.cfg.Log.Warn("customers: drive folder", "customer", c.ID, "err", err)
		} else {
			c.FolderID, c.FolderPath = fid, p
			if err := s.cfg.Store.UpdateCustomer(ctx, c); err != nil {
				s.cfg.Log.Warn("customers: store folder", "customer", c.ID, "err", err)
			}
		}
	}
	s.audit(ctx, org, actor, "customer.created", c.ID, map[string]any{"name": c.Name, "folder": c.FolderPath})
	return c, nil
}

// Update changes the fields given.
func (s *Service) Update(ctx context.Context, actor, id string, in Input) (Customer, error) {
	org := s.org(ctx)
	if err := s.gate(ctx, org); err != nil {
		return Customer{}, err
	}
	c, err := s.cfg.Store.GetCustomer(ctx, org, id)
	if err != nil {
		return Customer{}, err
	}
	if c.Archived {
		return Customer{}, fmt.Errorf("%w: the customer is archived", ErrConflict)
	}
	if err := s.apply(&c, in); err != nil {
		return Customer{}, err
	}
	c.UpdatedAt = s.now()
	if err := s.cfg.Store.UpdateCustomer(ctx, c); err != nil {
		return Customer{}, err
	}
	var fields []string
	for k, set := range map[string]bool{"name": in.Name != nil, "tax_id": in.TaxID != nil, "color": in.Color != nil, "notes": in.Notes != nil} {
		if set {
			fields = append(fields, k)
		}
	}
	s.audit(ctx, org, actor, "customer.updated", c.ID, map[string]any{"name": c.Name, "fields": fields})
	return s.withFolder(ctx, c), nil
}

// Archive hides a customer from new work. Nothing it owns is deleted, and its tags keep isolating its data.
func (s *Service) Archive(ctx context.Context, actor, id string) error {
	org := s.org(ctx)
	if err := s.gate(ctx, org); err != nil {
		return err
	}
	c, err := s.cfg.Store.GetCustomer(ctx, org, id)
	if err != nil {
		return err
	}
	if c.Archived {
		return nil
	}
	now := s.now()
	c.Archived, c.ArchivedAt, c.UpdatedAt = true, &now, now
	if err := s.cfg.Store.UpdateCustomer(ctx, c); err != nil {
		return err
	}
	s.audit(ctx, org, actor, "customer.archived", c.ID, map[string]any{"name": c.Name})
	return nil
}

// ---- clones ----

// DefaultDisplayName is "<agent title> · <customer name>" ("Contador · Ferretería López").
func DefaultDisplayName(a domain.Agent, customerName string) string {
	who := strings.TrimSpace(a.Title)
	if who == "" {
		who = strings.TrimSpace(a.Name)
	}
	if who == "" {
		who = a.ID
	}
	return who + " · " + customerName
}

// AddAssignment creates a clone of an agent for a customer.
func (s *Service) AddAssignment(ctx context.Context, actor, customerID string, in AssignmentInput) (Assignment, error) {
	org := s.org(ctx)
	if err := s.gate(ctx, org); err != nil {
		return Assignment{}, err
	}
	c, err := s.Active(ctx, org, customerID)
	if err != nil {
		return Assignment{}, err
	}
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return Assignment{}, invalid("agent_id is required")
	}
	var agent *domain.Agent
	if s.cfg.Agents != nil {
		agents, err := s.cfg.Agents(ctx, org)
		if err != nil {
			return Assignment{}, err
		}
		for i := range agents {
			if agents[i].ID == agentID {
				agent = &agents[i]
				break
			}
		}
		if agent == nil {
			return Assignment{}, fmt.Errorf("%w: agent %s", ErrNotFound, agentID)
		}
	} else {
		agent = &domain.Agent{ID: agentID}
	}
	name := clean(in.DisplayName)
	if name == "" {
		name = DefaultDisplayName(*agent, c.Name)
	}
	if r := []rune(name); len(r) > maxDisplay {
		name = string(r[:maxDisplay])
	}
	a := Assignment{ID: newID("cla_"), CustomerID: c.ID, AgentID: agentID, DisplayName: name, CreatedBy: actor, CreatedAt: s.now()}
	if err := s.cfg.Store.AddAssignment(ctx, org, a); err != nil {
		return Assignment{}, err
	}
	s.audit(ctx, org, actor, "customer.clone.created", a.ID, map[string]any{"customer_id": c.ID, "agent_id": agentID, "display_name": name})
	return a, nil
}

// RemoveAssignment deletes a clone (the agent and the customer's data stay).
func (s *Service) RemoveAssignment(ctx context.Context, actor, customerID, id string) error {
	org := s.org(ctx)
	if err := s.gate(ctx, org); err != nil {
		return err
	}
	if err := s.cfg.Store.DeleteAssignment(ctx, org, customerID, id); err != nil {
		return err
	}
	s.audit(ctx, org, actor, "customer.clone.deleted", id, map[string]any{"customer_id": customerID})
	return nil
}

// Assignments lists the clones of a customer ("" = all).
func (s *Service) Assignments(ctx context.Context, customerID string) ([]Assignment, error) {
	return s.cfg.Store.Assignments(ctx, s.org(ctx), customerID)
}

// ---- drive folders ----

// LinkFolder scopes an existing drive folder (and everything under it) to a customer.
func (s *Service) LinkFolder(ctx context.Context, actor, customerID, folderID string) (Link, error) {
	org := s.org(ctx)
	if err := s.gate(ctx, org); err != nil {
		return Link{}, err
	}
	c, err := s.Active(ctx, org, customerID)
	if err != nil {
		return Link{}, err
	}
	folders, _ := s.deps()
	if folders == nil {
		return Link{}, invalid("the drive is not available")
	}
	folderID = strings.TrimSpace(folderID)
	p, err := folders.FolderPath(ctx, folderID)
	if err != nil {
		return Link{}, err
	}
	if strings.Count(strings.Trim(p, "/"), "/") < 1 {
		return Link{}, invalid("a top-level folder cannot belong to one customer")
	}
	l := Link{Kind: LinkDriveFolder, RefID: folderID, CustomerID: c.ID, CreatedBy: actor, CreatedAt: s.now()}
	if err := s.cfg.Store.AddLink(ctx, org, l); err != nil {
		return Link{}, err
	}
	s.audit(ctx, org, actor, "customer.folder.linked", c.ID, map[string]any{"folder_id": folderID, "path": p})
	return l, nil
}

// UnlinkFolder removes the scope of a folder.
func (s *Service) UnlinkFolder(ctx context.Context, actor, customerID, folderID string) error {
	org := s.org(ctx)
	if err := s.gate(ctx, org); err != nil {
		return err
	}
	l, err := s.cfg.Store.LinkOf(ctx, org, LinkDriveFolder, folderID)
	if err != nil || l.CustomerID != customerID {
		return notFound("folder link")
	}
	if err := s.cfg.Store.DeleteLink(ctx, org, LinkDriveFolder, folderID); err != nil {
		return err
	}
	s.audit(ctx, org, actor, "customer.folder.unlinked", customerID, map[string]any{"folder_id": folderID})
	return nil
}

// LinkedFolders implements drive.CustomerDirectory: folder id -> customer.
func (s *Service) LinkedFolders(ctx context.Context, org string) (map[string]string, error) {
	ls, err := s.cfg.Store.Links(ctx, org, "", LinkDriveFolder, 0)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(ls))
	for _, l := range ls {
		out[l.RefID] = l.CustomerID
	}
	return out, nil
}

// ProjectCustomer implements drive.CustomerDirectory.
func (s *Service) ProjectCustomer(ctx context.Context, org, projectID string) (string, error) {
	_, work := s.deps()
	if work == nil {
		return "", errors.New("customers: projects are not wired yet")
	}
	return work.ProjectCustomer(ctx, org, projectID)
}

// ---- requests (application.CustomerScope) ----

// LinkRequest implements application.CustomerScope. It is called by the orchestrator before the request
// exists; the actor is the person who submitted it.
func (s *Service) LinkRequest(ctx context.Context, org, requestID, customerID string) error {
	c, err := s.Active(ctx, org, customerID)
	if err != nil {
		return err
	}
	actor := application.ActorFrom(ctx, "")
	if err := s.cfg.Store.AddLink(ctx, org, Link{Kind: LinkRequest, RefID: requestID, CustomerID: c.ID, CreatedBy: actor, CreatedAt: s.now()}); err != nil {
		return err
	}
	s.audit(ctx, org, actor, "customer.request.scoped", c.ID, map[string]any{"request_id": requestID})
	return nil
}

var _ application.CustomerChecker = (*Service)(nil)

// CheckCustomer implements application.CustomerChecker: new work (a chat turn) can be scoped to the customer.
func (s *Service) CheckCustomer(ctx context.Context, org, customerID string) error {
	_, err := s.Active(ctx, org, customerID)
	return err
}

// CustomerOfRequest implements application.CustomerScope.
func (s *Service) CustomerOfRequest(ctx context.Context, org, requestID string) (string, error) {
	if requestID == "" {
		return "", nil
	}
	l, err := s.cfg.Store.LinkOf(ctx, org, LinkRequest, requestID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return l.CustomerID, nil
}

// AssignedAgents implements application.CustomerScope.
func (s *Service) AssignedAgents(ctx context.Context, org, customerID string) ([]string, error) {
	as, err := s.cfg.Store.Assignments(ctx, org, customerID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.AgentID)
	}
	return out, nil
}
