package main

import (
	"context"
	"errors"
	"log/slog"
	"sort"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/customers"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/memory"
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

// bindCustomerHistory installs the reader/writer of earlier work (projects, requests and memories without a
// customer) that an administrator may assign to a customer explicitly (customers/history.go).
func bindCustomerHistory(svc *customers.Service, ws workspaceWiring, store application.Store, mem *memory.Service) {
	if svc == nil || mem == nil {
		return
	}
	svc.SetHistory(customerHistory{pstore: ws.pstore, psvc: ws.projects, store: store, mem: mem})
}

type customerHistory struct {
	pstore projects.Store
	psvc   *projects.Service
	store  application.Store
	mem    *memory.Service
}

func movableProject(status string) bool {
	switch status {
	case projects.StatusDraft, projects.StatusDone, projects.StatusFailed, projects.StatusCancelled:
		return true
	}
	return false
}

func (h customerHistory) Project(ctx context.Context, org, id string) (customers.HistoryProjectInfo, error) {
	if h.pstore == nil {
		return customers.HistoryProjectInfo{}, domain.ErrNotFound
	}
	r, err := h.pstore.Get(application.WithOrg(ctx, org), org, id)
	if err != nil {
		return customers.HistoryProjectInfo{}, err
	}
	return customers.HistoryProjectInfo{CustomerID: r.CustomerID, RequestID: r.RequestID, Movable: movableProject(r.Status)}, nil
}

func (h customerHistory) SetProjectCustomer(ctx context.Context, org, id, customerID string) error {
	if h.psvc == nil {
		return domain.ErrNotFound
	}
	_, err := h.psvc.SetCustomer(application.WithOrg(ctx, org), id, customerID)
	return err
}

func (h customerHistory) RequestFinished(ctx context.Context, org, id string) (bool, error) {
	r, err := h.store.GetRequest(application.WithOrg(ctx, org), org, id)
	if err != nil {
		return false, err
	}
	return r.Status == domain.RequestDone || r.Status == domain.RequestFailed, nil
}

func (h customerHistory) Memory(ctx context.Context, org, id string) (customers.HistoryMemoryInfo, error) {
	d, err := h.mem.Doc(application.WithOrg(ctx, org), org, id)
	if err != nil {
		return customers.HistoryMemoryInfo{}, err
	}
	return customers.HistoryMemoryInfo{CustomerID: d.CustomerID, RequestID: d.RequestID}, nil
}

func (h customerHistory) RetagMemories(ctx context.Context, org, requestID, docID, from, to string) (int, error) {
	return h.mem.RetagCustomer(application.WithOrg(ctx, org), org, memory.RetagSelector{RequestID: requestID, DocID: docID}, from, to)
}

func (h customerHistory) Candidates(ctx context.Context, org string, limit int) ([]customers.ProjectInfo, []customers.RequestInfo, error) {
	ctx = application.WithOrg(ctx, org)
	var ps []customers.ProjectInfo
	if h.psvc != nil {
		sums, err := h.psvc.List(ctx)
		if err != nil {
			return nil, nil, err
		}
		for _, p := range sums {
			if p.CustomerID == "" && movableProject(p.Status) && len(ps) < limit {
				ps = append(ps, customers.ProjectInfo{ID: p.ID, Name: p.Name, Status: p.Status, Light: p.Light, TasksDone: p.TasksDone, TasksTotal: p.TasksTotal,
					Awaiting: p.Awaiting, Failed: p.Failed, FinishedAt: p.FinishedAt})
			}
		}
	}
	reqs, err := h.store.ListRequests(ctx, org)
	if err != nil {
		return nil, nil, err
	}
	sort.SliceStable(reqs, func(i, j int) bool { return reqs[i].CreatedAt.After(reqs[j].CreatedAt) })
	var rs []customers.RequestInfo
	for _, r := range reqs {
		if len(rs) >= limit {
			break
		}
		if r.Status != domain.RequestDone && r.Status != domain.RequestFailed {
			continue
		}
		if h.pstore != nil { // a project's request moves with its project
			if _, err := h.pstore.ByRequest(ctx, org, r.ID); err == nil {
				continue
			}
		}
		rs = append(rs, customers.RequestInfo{ID: r.ID, Text: r.Text, Status: string(r.Status), CreatedAt: r.CreatedAt})
	}
	return ps, rs, nil
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
