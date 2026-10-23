package main

import (
	"context"
	"errors"
	"log/slog"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/customers"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/projects"
)

// newCustomers builds the customer district (B12, docs/architecture/customer-district.md) before the drive and the
// projects, which consult it (folder ownership, project scope); bindCustomers installs them once they exist. The
// orchestrator stores the customer of every scoped request through it before the request is created.
func newCustomers(cfg config.Config, log *slog.Logger, pg *postgres.Store, store application.Store, rec *application.Recorder,
	orch *application.Orchestrator, cw connWiring) *customers.Service {
	var cs customers.Store = customers.NewMemStore()
	if pg != nil {
		cs = &postgres.CustomerStore{S: pg}
	}
	svc := customers.New(customers.Config{Store: cs, OrgID: cfg.App.OrgID, Log: log, WriteGate: driveReadOnlyGate(cw.ctl),
		Agents: func(ctx context.Context, org string) ([]domain.Agent, error) {
			return store.ListAgents(application.WithOrg(ctx, org), org)
		},
		Audit: func(ctx context.Context, org, actor, action, id string, d map[string]any) {
			rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: actor, Action: action, Entity: "customer", EntityID: id, Details: d})
		}})
	orch.SetCustomerScope(svc)
	return svc
}

// bindCustomers installs the drive and the projects into the customer service.
func bindCustomers(svc *customers.Service, ws workspaceWiring, store application.Store) {
	if ws.drive != nil {
		svc.SetFolders(customerFolders{drive: ws.drive})
	}
	svc.SetWork(customerWork{pstore: ws.pstore, psvc: ws.projects, store: store})
}

// customerFolders is the drive as the customers see it: /Clients/<name> owned by the customer.
type customerFolders struct{ drive *drive.Service }

func (f customerFolders) EnsureCustomerFolder(ctx context.Context, customerID, name string) (string, string, error) {
	fl, err := f.drive.EnsureOwnedFolder(ctx, drive.RootClients, customerID, name)
	return fl.ID, fl.Path, err
}

func (f customerFolders) FolderPath(ctx context.Context, folderID string) (string, error) {
	fl, err := f.drive.FolderByID(ctx, folderID)
	return fl.Path, err
}

// customerWork reads the projects and requests behind each customer.
type customerWork struct {
	pstore projects.Store
	psvc   *projects.Service
	store  application.Store
}

func (w customerWork) Projects(ctx context.Context, org string) ([]customers.ProjectInfo, error) {
	if w.psvc == nil {
		return nil, nil
	}
	sums, err := w.psvc.List(application.WithOrg(ctx, org))
	if err != nil {
		return nil, err
	}
	var out []customers.ProjectInfo
	for _, p := range sums {
		if p.CustomerID == "" {
			continue
		}
		out = append(out, customers.ProjectInfo{ID: p.ID, Name: p.Name, Status: p.Status, Light: p.Light, TasksDone: p.TasksDone, TasksTotal: p.TasksTotal,
			Awaiting: p.Awaiting, Failed: p.Failed, CustomerID: p.CustomerID, FinishedAt: p.FinishedAt})
	}
	return out, nil
}

func (w customerWork) ProjectCustomer(ctx context.Context, org, projectID string) (string, error) {
	if w.pstore == nil {
		return "", nil
	}
	r, err := w.pstore.Get(application.WithOrg(ctx, org), org, projectID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", nil
	}
	return r.CustomerID, err
}

func (w customerWork) Request(ctx context.Context, org, id string) (customers.RequestInfo, error) {
	r, err := w.store.GetRequest(application.WithOrg(ctx, org), org, id)
	if err != nil {
		return customers.RequestInfo{}, err
	}
	return customers.RequestInfo{ID: r.ID, Text: r.Text, Status: string(r.Status), CreatedAt: r.CreatedAt}, nil
}

// customerActive is the projects' check of a customer id.
func customerActive(svc *customers.Service) func(ctx context.Context, org, id string) error {
	if svc == nil {
		return nil
	}
	return func(ctx context.Context, org, id string) error {
		_, err := svc.Active(ctx, org, id)
		return err
	}
}

// customerOf is the resolver the drive and memory tools use (nil without customers).
func customerOf(svc *customers.Service) func(ctx context.Context, org, requestID string) (string, error) {
	if svc == nil {
		return nil
	}
	return svc.CustomerOfRequest
}

// customerDirectory is the drive's view of folder ownership (nil interface without customers).
func customerDirectory(svc *customers.Service) drive.CustomerDirectory {
	if svc == nil {
		return nil
	}
	return svc
}
