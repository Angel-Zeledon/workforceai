package projects

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
)

// Batches ("lotes", docs/superpowers/specs/2026-11-03-proyectos-por-cliente-y-ayuda-design.md part 1): one template
// launched for many customers. Every customer gets its own normal project (its customer_id, folder and isolation), so
// one stuck or failed customer never holds the others back; the batch groups them and launches at most Parallel at a
// time (the rest wait "queued"). A batch counts as ONE active project of the plan, whatever its size; the plan bounds
// its customers (MaxBatchCustomers) and its pace (BatchParallel). A batch never creates agents.

// Batch statuses and item states.
const (
	BatchRunning   = "running"
	BatchDone      = "done"
	BatchCancelled = "cancelled"

	ItemQueued    = "queued"
	ItemLaunched  = "launched"
	ItemCancelled = "cancelled"
	ItemError     = "error"

	defaultBatchCustomers = 10
	defaultBatchParallel  = 2
)

// CustomerVars are the customer data a batch template can use: {{cliente.nombre}} (contact, else name),
// {{cliente.empresa}} (name), {{cliente.correo}}, {{cliente.telefono}}, {{cliente.notas}}.
type CustomerVars struct {
	Name, Contact, Email, Phone, Notes string
}

func (v CustomerVars) values() map[string]string {
	nombre := v.Contact
	if nombre == "" {
		nombre = v.Name
	}
	return map[string]string{"nombre": nombre, "empresa": v.Name, "correo": v.Email, "telefono": v.Phone, "notas": v.Notes}
}

var customerVarRe = regexp.MustCompile(`\{\{\s*cliente\.([a-z]+)\s*\}\}`)

func customerVarsUsed(texts ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range texts {
		for _, m := range customerVarRe.FindAllStringSubmatch(t, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	return out
}

func substCustomer(s string, vals map[string]string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	return customerVarRe.ReplaceAllStringFunc(s, func(m string) string {
		k := customerVarRe.FindStringSubmatch(m)[1]
		if v, ok := vals[k]; ok {
			return v
		}
		return m
	})
}

// BatchItem is one customer of a batch.
type BatchItem struct {
	CustomerID   string `json:"customer_id"`
	CustomerName string `json:"customer_name"`
	ProjectID    string `json:"project_id"`
	State        string `json:"state"`
	Error        string `json:"error,omitempty"`
}

// Batch is a template launched for many customers.
type Batch struct {
	ID                  string            `json:"id"`
	OrgID               string            `json:"org_id"`
	Name                string            `json:"name"`
	TemplateID          string            `json:"template_id"`
	Goal                string            `json:"goal,omitempty"`
	Params              map[string]string `json:"params,omitempty"`
	Items               []BatchItem       `json:"items"`
	Parallel            int               `json:"parallel"`
	BudgetPerProjectUSD float64           `json:"budget_per_project_usd"`
	Control             string            `json:"control"`
	Status              string            `json:"status"`
	CreatedBy           string            `json:"created_by,omitempty"`
	CreatedAt           time.Time         `json:"created_at"`
	FinishedAt          *time.Time        `json:"finished_at"`
}

// BatchStore persists the batches.
type BatchStore interface {
	PutBatch(ctx context.Context, b Batch) error
	GetBatch(ctx context.Context, org, id string) (Batch, error)
	ListBatches(ctx context.Context, org string) ([]Batch, error)
}

// BatchInput is what a person chooses to launch a batch.
type BatchInput struct {
	Name                string            `json:"name,omitempty"`
	TemplateID          string            `json:"template_id"`
	Goal                string            `json:"goal,omitempty"`
	Params              map[string]string `json:"params,omitempty"`
	CustomerIDs         []string          `json:"customer_ids"`
	BudgetPerProjectUSD float64           `json:"budget_per_project_usd,omitempty"`
	Parallel            int               `json:"parallel,omitempty"`
	// Confirm accepts a total above the confirmation threshold; AllowMissing launches although some customers lack
	// data the template uses (the text then has it empty).
	Confirm      bool `json:"confirm,omitempty"`
	AllowMissing bool `json:"allow_missing,omitempty"`
}

// BatchPreview is shown before launching: per customer what the template needs and is missing, and the cost.
type BatchPreview struct {
	Customers           []BatchPreviewRow `json:"customers"`
	VarsUsed            []string          `json:"vars_used"`
	PerProjectP50USD    float64           `json:"per_project_p50_usd"`
	PerProjectP90USD    float64           `json:"per_project_p90_usd"`
	TotalP50USD         float64           `json:"total_p50_usd"`
	TotalP90USD         float64           `json:"total_p90_usd"`
	BudgetPerProjectUSD float64           `json:"budget_per_project_usd"`
	TotalBudgetUSD      float64           `json:"total_budget_usd"`
	MaxCustomers        int               `json:"max_customers"`
	Parallel            int               `json:"parallel"`
	ConfirmThresholdUSD float64           `json:"confirm_threshold_usd"`
	NeedsConfirm        bool              `json:"needs_confirm"`
	Missing             int               `json:"missing"`
}

type BatchPreviewRow struct {
	CustomerID string   `json:"customer_id"`
	Name       string   `json:"name"`
	Missing    []string `json:"missing"`
	Error      string   `json:"error,omitempty"`
}

func (s *Service) batches() BatchStore {
	if s.cfg.Batches != nil {
		return s.cfg.Batches
	}
	if bs, ok := s.cfg.Store.(BatchStore); ok {
		return bs
	}
	return nil
}

func (s *Service) batchLimits(ctx context.Context, org string) (maxCustomers, parallel int) {
	maxCustomers, parallel = defaultBatchCustomers, defaultBatchParallel
	if s.cfg.PlanLimits != nil {
		t := s.cfg.PlanLimits(ctx, org)
		if t.MaxBatchCustomers > 0 {
			maxCustomers = t.MaxBatchCustomers
		}
		if t.BatchParallel > 0 {
			parallel = t.BatchParallel
		}
	}
	return maxCustomers, parallel
}

func (s *Service) confirmThreshold() float64 {
	if s.cfg.BatchConfirmUSD > 0 {
		return s.cfg.BatchConfirmUSD
	}
	return 1
}

// PreviewBatch creates nothing: it checks the customers, the data the template uses and estimates the cost.
func (s *Service) PreviewBatch(ctx context.Context, in BatchInput) (BatchPreview, error) {
	org := s.org(ctx)
	if s.batches() == nil || s.cfg.CustomerInfo == nil {
		return BatchPreview{}, fmt.Errorf("%w: batches are not available", domain.ErrInvalid)
	}
	ids := uniqueIDs(in.CustomerIDs)
	maxC, par := s.batchLimits(ctx, org)
	p := BatchPreview{Customers: []BatchPreviewRow{}, MaxCustomers: maxC, ConfirmThresholdUSD: s.confirmThreshold()}
	p.Parallel = par
	if in.Parallel > 0 && in.Parallel < par {
		p.Parallel = in.Parallel
	}
	if len(ids) == 0 {
		return p, fmt.Errorf("%w: choose at least one customer", domain.ErrInvalid)
	}
	if len(ids) > maxC {
		return p, &domain.PlanLimitError{Kind: "batch_customers", Limit: maxC, Plan: planID(s, ctx, org)}
	}
	// One draft-like instantiation to know the texts and the estimate per customer (nothing is stored).
	tpl, params, goal, err := s.pickTemplate(ctx, NewProject{TemplateID: in.TemplateID, Params: in.Params, Goal: in.Goal}, strings.TrimSpace(in.Goal), s.locale(ctx, ""))
	if err != nil {
		return p, err
	}
	inst, err := tpl.instantiateWith(s.cfg.Limits, "preview", goal, params, s.locale(ctx, ""))
	if err != nil {
		return p, err
	}
	texts := []string{inst.Goal, inst.Name}
	for _, v := range in.Params {
		texts = append(texts, v)
	}
	for _, n := range inst.Nodes {
		texts = append(texts, n.Title, n.Description)
	}
	p.VarsUsed = customerVarsUsed(texts...)
	for _, id := range ids {
		row := BatchPreviewRow{CustomerID: id, Missing: []string{}}
		if err := s.cfg.CustomerActive(ctx, org, id); err != nil {
			row.Error = "unknown_customer"
		} else if v, err := s.cfg.CustomerInfo(ctx, org, id); err != nil {
			row.Error = "unknown_customer"
		} else {
			row.Name = v.Name
			vals := v.values()
			for _, k := range p.VarsUsed {
				if val, ok := vals[k]; !ok || strings.TrimSpace(val) == "" {
					row.Missing = append(row.Missing, k)
				}
			}
		}
		if len(row.Missing) > 0 || row.Error != "" {
			p.Missing++
		}
		p.Customers = append(p.Customers, row)
	}
	est := estimatePlanWith(inst.Nodes, inst.Objectives, s.estimator(ctx, nil))
	n := float64(len(ids))
	p.PerProjectP50USD, p.PerProjectP90USD = est.Total.P50USD, est.Total.P90USD
	p.TotalP50USD, p.TotalP90USD = round4(est.Total.P50USD*n), round4(est.Total.P90USD*n)
	p.BudgetPerProjectUSD = in.BudgetPerProjectUSD
	if p.BudgetPerProjectUSD <= 0 {
		p.BudgetPerProjectUSD = defaultBudget(est)
	}
	p.TotalBudgetUSD = round4(p.BudgetPerProjectUSD * n)
	p.NeedsConfirm = p.TotalBudgetUSD > p.ConfirmThresholdUSD
	return p, nil
}

func round4(v float64) float64 { return math.Round(v*10000) / 10000 }

func planID(s *Service, ctx context.Context, org string) string {
	if s.cfg.PlanLimits != nil {
		return s.cfg.PlanLimits(ctx, org).ID
	}
	return ""
}

func uniqueIDs(ids []string) []string {
	out := []string{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// CreateBatch creates one draft project per customer and starts launching them at the batch's pace.
func (s *Service) CreateBatch(ctx context.Context, in BatchInput) (Batch, error) {
	org := s.org(ctx)
	p, err := s.PreviewBatch(ctx, in)
	if err != nil {
		return Batch{}, err
	}
	for _, c := range p.Customers {
		if c.Error != "" {
			return Batch{}, fmt.Errorf("%w: unknown customer %s", domain.ErrInvalid, c.CustomerID)
		}
	}
	if p.Missing > 0 && !in.AllowMissing {
		return Batch{}, fmt.Errorf("%w: missing_customer_data: %d customers lack data the template uses", domain.ErrConflict, p.Missing)
	}
	if p.NeedsConfirm && !in.Confirm {
		return Batch{}, fmt.Errorf("%w: needs_confirm: the batch may cost up to $%.2f", domain.ErrConflict, p.TotalBudgetUSD)
	}
	// The batch is one more active project: refused when the organization has no room for it.
	s.launchMu.Lock()
	err = s.checkActiveLimit(ctx, org, "")
	s.launchMu.Unlock()
	if err != nil {
		return Batch{}, err
	}
	b := Batch{ID: "bat-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12], OrgID: org, TemplateID: in.TemplateID, Goal: in.Goal, Params: in.Params,
		Parallel: p.Parallel, BudgetPerProjectUSD: p.BudgetPerProjectUSD, Control: ControlActive, Status: BatchRunning,
		CreatedBy: application.ActorFrom(ctx, ""), CreatedAt: s.now(), Items: []BatchItem{}}
	for _, c := range p.Customers {
		v, _ := s.cfg.CustomerInfo(ctx, org, c.CustomerID)
		vals := v.values()
		params := map[string]string{}
		for k, val := range in.Params {
			params[k] = substCustomer(val, vals)
		}
		rec, err := s.CreateDraft(ctx, NewProject{TemplateID: in.TemplateID, Params: params, Goal: substCustomer(in.Goal, vals),
			BudgetUSD: p.BudgetPerProjectUSD, CustomerID: c.CustomerID})
		item := BatchItem{CustomerID: c.CustomerID, CustomerName: v.Name, State: ItemQueued}
		if err != nil {
			item.State, item.Error = ItemError, err.Error()
		} else {
			if b.Name == "" { // the batch is named after the work, not after its first customer
				b.Name = strings.Join(strings.Fields(customerVarRe.ReplaceAllString(orStr(strings.TrimSpace(in.Goal), rec.Name), "…")), " ")
			}
			if _, err := s.update(ctx, rec.ID, func(r *Record) error {
				r.BatchID = b.ID
				applyCustomerVars(r, vals)
				return nil
			}); err != nil {
				item.State, item.Error = ItemError, err.Error()
			}
			item.ProjectID = rec.ID
		}
		b.Items = append(b.Items, item)
	}
	if in.Name != "" {
		b.Name = in.Name
	}
	if err := s.batches().PutBatch(ctx, b); err != nil {
		return Batch{}, err
	}
	s.audit(ctx, "project.batch_created", b.ID, map[string]any{"customers": len(b.Items), "template_id": b.TemplateID, "parallel": b.Parallel,
		"total_budget_usd": p.TotalBudgetUSD})
	s.startPump(org, b.ID)
	return b, nil
}

// applyCustomerVars puts the customer's data into the texts of its project.
func applyCustomerVars(r *Record, vals map[string]string) {
	r.Goal, r.Name = substCustomer(r.Goal, vals), substCustomer(r.Name, vals)
	for i := range r.Objectives {
		r.Objectives[i].Title = substCustomer(r.Objectives[i].Title, vals)
	}
	for i := range r.Nodes {
		r.Nodes[i].Title = substCustomer(r.Nodes[i].Title, vals)
		r.Nodes[i].Description = substCustomer(r.Nodes[i].Description, vals)
	}
}

// ---- pace ----

type batchPumps struct {
	mu   sync.Mutex
	runs map[string]bool
}

func (s *Service) startPump(org, id string) {
	s.pumps.mu.Lock()
	if s.pumps.runs == nil {
		s.pumps.runs = map[string]bool{}
	}
	key := org + "|" + id
	if s.pumps.runs[key] {
		s.pumps.mu.Unlock()
		return
	}
	s.pumps.runs[key] = true
	s.pumps.mu.Unlock()
	go func() {
		defer func() {
			s.pumps.mu.Lock()
			delete(s.pumps.runs, key)
			s.pumps.mu.Unlock()
		}()
		ctx := application.WithOrg(s.root, org)
		every := max(s.cfg.Poll, 20*time.Millisecond)
		for {
			if done := s.pumpOnce(ctx, id); done {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(every):
			}
		}
	}()
}

// pumpOnce launches queued customers while fewer than Parallel run, and closes the batch when every customer ended.
func (s *Service) pumpOnce(ctx context.Context, id string) bool {
	bs := s.batches()
	b, err := bs.GetBatch(ctx, s.org(ctx), id)
	if err != nil {
		return errors.Is(err, domain.ErrNotFound)
	}
	if b.Status != BatchRunning {
		return true
	}
	running, open := 0, 0
	for _, it := range b.Items {
		switch it.State {
		case ItemQueued:
			open++
		case ItemLaunched:
			if rec, err := s.cfg.Store.Get(ctx, b.OrgID, it.ProjectID); err == nil && !terminalStatus(rec.Status) {
				if d, err := s.detailOf(ctx, rec); err != nil || !terminalStatus(d.Project.Status) {
					running++
					open++
				}
			}
		}
	}
	changed := false
	actx := application.WithActor(ctx, orStr(b.CreatedBy, "system"))
	for i := range b.Items {
		if b.Control != ControlActive || running >= b.Parallel {
			break
		}
		it := &b.Items[i]
		if it.State != ItemQueued {
			continue
		}
		_, err := s.Launch(actx, it.ProjectID, LaunchBody{ApprovedBudgetUSD: b.BudgetPerProjectUSD, AcknowledgeUnderbudget: true, ProceedWithoutInputs: true})
		if err != nil {
			var pl *domain.PlanLimitError
			if errors.As(err, &pl) || strings.Contains(err.Error(), LimitMaxActive) {
				break // no room right now: try again on the next tick
			}
			it.State, it.Error = ItemError, err.Error()
			open--
			changed = true
			continue
		}
		it.State = ItemLaunched
		running++
		changed = true
	}
	if open == 0 {
		now := s.now()
		b.Status, b.FinishedAt = BatchDone, &now
		if b.Control == ControlCancelled {
			b.Status = BatchCancelled
		}
		changed = true
	}
	if changed {
		_ = bs.PutBatch(ctx, b)
	}
	return b.Status != BatchRunning
}

func orStr(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// RecoverBatches restarts the pace of the running batches after a restart.
func (s *Service) RecoverBatches(ctx context.Context, orgs []string) {
	bs := s.batches()
	if bs == nil {
		return
	}
	for _, org := range application.WithDefaultOrg(orgs, s.cfg.OrgID) {
		list, err := bs.ListBatches(application.WithOrg(ctx, org), org)
		if err != nil {
			continue
		}
		for _, b := range list {
			if b.Status == BatchRunning {
				s.startPump(org, b.ID)
			}
		}
	}
}

// ---- control ----

// BatchControl pauses (no new customer starts and the running ones pause), resumes or cancels a batch.
func (s *Service) BatchControl(ctx context.Context, id, action string) (Batch, error) {
	bs := s.batches()
	if bs == nil {
		return Batch{}, fmt.Errorf("%w: batches are not available", domain.ErrInvalid)
	}
	org := s.org(ctx)
	b, err := bs.GetBatch(ctx, org, id)
	if err != nil {
		return b, err
	}
	if b.Status != BatchRunning {
		return b, fmt.Errorf("%w: the batch already ended (%s)", domain.ErrConflict, b.Status)
	}
	switch action {
	case "pause":
		b.Control = ControlPaused
	case "resume":
		b.Control = ControlActive
	case "cancel":
		b.Control = ControlCancelled
	default:
		return b, fmt.Errorf("%w: action must be pause, resume or cancel", domain.ErrInvalid)
	}
	for i := range b.Items {
		it := &b.Items[i]
		switch {
		case it.State == ItemQueued && action == "cancel":
			it.State = ItemCancelled
		case it.State == ItemLaunched:
			_, _ = s.Control(ctx, it.ProjectID, action) // a finished project answers 409: nothing to do
		}
	}
	if err := bs.PutBatch(ctx, b); err != nil {
		return b, err
	}
	s.audit(ctx, "project.batch_"+action, id, map[string]any{"customers": len(b.Items)})
	s.startPump(org, id)
	return b, nil
}

// CancelBatchCustomer cancels one customer of a batch (its project, or its place in the queue).
func (s *Service) CancelBatchCustomer(ctx context.Context, id, customerID string) (Batch, error) {
	bs := s.batches()
	if bs == nil {
		return Batch{}, fmt.Errorf("%w: batches are not available", domain.ErrInvalid)
	}
	b, err := bs.GetBatch(ctx, s.org(ctx), id)
	if err != nil {
		return b, err
	}
	for i := range b.Items {
		it := &b.Items[i]
		if it.CustomerID != customerID {
			continue
		}
		switch it.State {
		case ItemQueued:
			it.State = ItemCancelled
		case ItemLaunched:
			if _, err := s.Control(ctx, it.ProjectID, "cancel"); err != nil {
				return b, err
			}
		default:
			return b, fmt.Errorf("%w: this customer is already %s", domain.ErrConflict, it.State)
		}
		if err := bs.PutBatch(ctx, b); err != nil {
			return b, err
		}
		s.audit(ctx, "project.batch_customer_cancelled", id, map[string]any{"customer_id": customerID, "project_id": it.ProjectID})
		return b, nil
	}
	return b, fmt.Errorf("%w: customer not in the batch", domain.ErrNotFound)
}

// RetryBatchFailed retries the failed steps of every failed customer project of the batch.
func (s *Service) RetryBatchFailed(ctx context.Context, id string) (int, error) {
	v, err := s.BatchView(ctx, id)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range v.Rows {
		if r.Status != "failed" || r.ProjectID == "" {
			continue
		}
		d, err := s.Get(ctx, r.ProjectID)
		if err != nil {
			continue
		}
		for _, nd := range d.Nodes {
			if nd.State == StateFailed {
				if _, err := s.RetryNode(ctx, r.ProjectID, nd.ID); err == nil {
					n++
				}
			}
		}
	}
	if n > 0 {
		s.reopenBatch(ctx, id)
	}
	return n, nil
}

func (s *Service) reopenBatch(ctx context.Context, id string) {
	bs := s.batches()
	if b, err := bs.GetBatch(ctx, s.org(ctx), id); err == nil && b.Status == BatchDone {
		b.Status, b.FinishedAt = BatchRunning, nil
		_ = bs.PutBatch(ctx, b)
		s.startPump(s.org(ctx), id)
	}
}

// ---- view ----

// BatchRow is one customer in the batch table.
type BatchRow struct {
	CustomerID     string  `json:"customer_id"`
	CustomerName   string  `json:"customer_name"`
	ProjectID      string  `json:"project_id,omitempty"`
	Status         string  `json:"status"` // queued|running|paused|needs_help|done|partial|failed|cancelled
	Progress       int     `json:"progress"`
	CurrentStep    string  `json:"current_step,omitempty"`
	CurrentAgentID string  `json:"current_agent_id,omitempty"`
	CostUSD        float64 `json:"cost_usd"`
	NeedsInput     int     `json:"needs_input"`
	WaitingSeconds int     `json:"waiting_seconds,omitempty"` // how long its oldest question waits
	Error          string  `json:"error,omitempty"`
}

// BatchView is the batch table: a row per customer and the counts by status.
type BatchView struct {
	Batch   Batch          `json:"batch"`
	Rows    []BatchRow     `json:"rows"`
	Summary map[string]int `json:"summary"`
	CostUSD float64        `json:"cost_usd"`
}

func rowStatus(st string, needs int) string {
	switch {
	case needs > 0:
		return "needs_help"
	case st == StatusDone, st == StatusPartial, st == StatusFailed, st == StatusCancelled, st == StatusPaused:
		return st
	}
	return "running"
}

// BatchView builds the table of a batch.
func (s *Service) BatchView(ctx context.Context, id string) (BatchView, error) {
	bs := s.batches()
	if bs == nil {
		return BatchView{}, fmt.Errorf("%w: batches are not available", domain.ErrInvalid)
	}
	org := s.org(ctx)
	b, err := bs.GetBatch(ctx, org, id)
	if err != nil {
		return BatchView{}, err
	}
	oldest := s.oldestQuestions(ctx)
	v := BatchView{Batch: b, Rows: []BatchRow{}, Summary: map[string]int{}}
	for _, it := range b.Items {
		r := BatchRow{CustomerID: it.CustomerID, CustomerName: it.CustomerName, ProjectID: it.ProjectID, Error: it.Error}
		switch it.State {
		case ItemQueued:
			r.Status = "queued"
		case ItemCancelled:
			r.Status = "cancelled"
		case ItemError:
			r.Status = "failed"
		default:
			rec, err := s.cfg.Store.Get(ctx, org, it.ProjectID)
			if err != nil {
				r.Status = "failed"
				break
			}
			d, err := s.detailOf(ctx, rec)
			if err != nil {
				r.Status = "failed"
				break
			}
			p := d.Project
			r.Status, r.NeedsInput, r.CostUSD = rowStatus(p.Status, p.NeedsInput), p.NeedsInput, p.SpentUSD
			if p.TasksTotal > 0 {
				r.Progress = p.TasksDone * 100 / p.TasksTotal
			}
			r.CurrentStep, r.CurrentAgentID = currentStep(d.Nodes)
			if t, ok := oldest[it.ProjectID]; ok {
				r.WaitingSeconds = int(s.now().Sub(t).Seconds())
			}
		}
		v.Summary[r.Status]++
		v.CostUSD += r.CostUSD
		v.Rows = append(v.Rows, r)
	}
	v.CostUSD = round4(v.CostUSD)
	return v, nil
}

// currentStep is the step a person would want to know about: one that needs help, else one running.
func currentStep(nodes []Node) (string, string) {
	rank := map[string]int{StateNeedsInput: 0, StateAwaitingApproval: 1, StateRunning: 2, StateReady: 3}
	best, bestRank := Node{}, 99
	for _, n := range nodes {
		if n.Kind == KindGroup {
			continue
		}
		if r, ok := rank[n.State]; ok && r < bestRank {
			best, bestRank = n, r
		}
	}
	if bestRank == 99 {
		return "", ""
	}
	agent := ""
	if best.AgentID != nil {
		agent = *best.AgentID
	}
	return best.Title, agent
}

// oldestQuestions is, per project, when its oldest open question was asked.
func (s *Service) oldestQuestions(ctx context.Context) map[string]time.Time {
	out := map[string]time.Time{}
	if s.cfg.Orch == nil || s.cfg.Orch.Questions() == nil {
		return out
	}
	qs, err := s.cfg.Orch.Questions().Store.ListQuestions(ctx, s.org(ctx), application.QuestionOpen)
	if err != nil {
		return out
	}
	for _, q := range qs {
		if t, ok := out[q.ProjectID]; q.ProjectID != "" && (!ok || q.CreatedAt.Before(t)) {
			out[q.ProjectID] = q.CreatedAt
		}
	}
	return out
}

// BatchSummary is a batch in the list (the card in Projects).
type BatchSummary struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Status    string         `json:"status"`
	Control   string         `json:"control"`
	Customers int            `json:"customers"`
	Summary   map[string]int `json:"summary"`
	CreatedAt time.Time      `json:"created_at"`
}

// ListBatches returns the batches of the organization, newest first, with their counts.
func (s *Service) ListBatches(ctx context.Context) ([]BatchSummary, error) {
	bs := s.batches()
	if bs == nil {
		return []BatchSummary{}, nil
	}
	list, err := bs.ListBatches(ctx, s.org(ctx))
	if err != nil {
		return nil, err
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	out := make([]BatchSummary, 0, len(list))
	for _, b := range list {
		sum := BatchSummary{ID: b.ID, Name: b.Name, Status: b.Status, Control: b.Control, Customers: len(b.Items), CreatedAt: b.CreatedAt, Summary: map[string]int{}}
		if v, err := s.BatchView(ctx, b.ID); err == nil {
			sum.Summary = v.Summary
		}
		out = append(out, sum)
	}
	return out, nil
}

var _ = policy.LimitProjects

// BatchName is the name of a batch ("" when unknown): the label of its group in Pendientes.
func (s *Service) BatchName(ctx context.Context, id string) string {
	if bs := s.batches(); bs != nil {
		if b, err := bs.GetBatch(ctx, s.org(ctx), id); err == nil {
			return b.Name
		}
	}
	return ""
}
