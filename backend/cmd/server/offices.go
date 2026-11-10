package main

import (
	"context"
	"slices"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/memory"
	"aiworkforce/backend/internal/notify"
	"aiworkforce/backend/internal/offices"
	"aiworkforce/backend/internal/orgchart"
)

// wireOffices builds E2 "Oficinas" (docs/architecture/equipos-oficinas.md §3 and §4): offices over the departments of
// the org chart, agent owners and visibility, office and personal memory. Postgres (migration 815) when there is a
// database, memory otherwise. Without authentication there are no people, so nothing is ever filtered.
func wireOffices(pg *postgres.Store, store application.Store, authStore auth.Store, chart *orgchart.Service, rec *application.Recorder,
	cleanNote func(string) string) *offices.Service {
	var st offices.Store = offices.NewMemStore()
	if pg != nil {
		st = &postgres.OfficesStore{S: pg}
	}
	cfg := offices.Config{Store: st, CleanNote: cleanNote,
		Agents: func(ctx context.Context, org string) ([]offices.AgentRef, error) {
			as, err := store.ListAgents(application.WithOrg(ctx, org), org)
			out := make([]offices.AgentRef, 0, len(as))
			for _, a := range as {
				out = append(out, offices.AgentRef{ID: a.ID, Name: a.Name, Role: a.Role, Title: a.Title})
			}
			return out, err
		},
		Audit: func(ctx context.Context, org, actor, action, id string, d map[string]any) {
			rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: actor, Action: action, Entity: "office", EntityID: id, Details: d})
		}}
	if chart != nil {
		cs := chart.Store
		cfg.Departments = func(ctx context.Context, org string) ([]offices.Dept, error) {
			ds, err := cs.ListDepartments(ctx, org)
			out := make([]offices.Dept, 0, len(ds))
			for _, d := range ds {
				x := offices.Dept{ID: d.ID, Name: d.Name}
				if d.Head.Kind == orgchart.KindPerson {
					x.HeadUser = d.Head.ID
				}
				out = append(out, x)
			}
			return out, err
		}
		cfg.Placements = func(ctx context.Context, org string) (map[string]string, error) {
			ps, err := cs.ListPlacements(ctx, org)
			out := make(map[string]string, len(ps))
			for _, p := range ps {
				out[p.UserID] = p.DepartmentID
			}
			return out, err
		}
		cfg.AgentDepts = func(ctx context.Context, org string) (map[string]string, error) {
			js, err := cs.ListJobs(ctx, org)
			out := make(map[string]string, len(js))
			for _, j := range js {
				out[j.AgentID] = j.DepartmentID
			}
			return out, err
		}
	}
	if authStore != nil {
		cfg.People = func(ctx context.Context, org string) ([]offices.Person, error) {
			ms, err := authStore.ListMembers(ctx, org)
			out := make([]offices.Person, 0, len(ms))
			for _, m := range ms {
				out = append(out, offices.Person{UserID: m.UserID, Name: m.Name, Admin: m.Role == auth.RoleOwner || m.Role == auth.RoleAdmin})
			}
			return out, err
		}
	}
	return offices.New(cfg)
}

// noteCleaner is the cleaner of the notes people write for an office or for themself: the same one as /recordar (a
// text with a secret or an injection pattern is refused).
func noteCleaner(m *memory.Service) func(string) string {
	if m == nil {
		return nil
	}
	return func(t string) string {
		if n := m.CleanNotes([]string{t}); len(n) > 0 {
			return n[0]
		}
		return ""
	}
}

// officeRecall feeds the long-term memory with the notes of the agent's office, the company's knowledge and the
// memory of the person the task runs for (the actor of the run; none for scheduled work).
func officeRecall(m *memory.Service, svc *offices.Service) {
	if m == nil || svc == nil {
		return
	}
	m.SetExtraRecall(func(ctx context.Context, org, agentID, _, text string) []application.RecalledMemory {
		var out []application.RecalledMemory
		for _, n := range svc.RecallNotes(ctx, org, agentID, application.PersonFrom(ctx), text, 3) {
			out = append(out, application.RecalledMemory{Ref: "team:" + n.ID, Kind: memory.KindNote, Title: offices.NoteTitle(n), Snippet: n.Text, At: n.CreatedAt})
		}
		return out
	})
}

// wireApprovers installs the D3 approval rule (equipos-oficinas.md §4.2): an approval of an office's agent goes to the
// responsibles of that office (the admins when it has none), and whoever does not see the agent's work does not see
// its approvals. The e-mail notice also goes to those responsibles. Without authentication there are no people, so
// nothing changes.
func wireApprovers(orch *application.Orchestrator, notifySvc *notify.Service, svc *offices.Service, authStore auth.Store, authOn bool) {
	if !authOn || svc == nil || authStore == nil {
		return
	}
	pol := offices.ApprovalPolicy{Svc: svc, Roles: func(ctx context.Context, org string) (map[string]string, error) {
		ms, err := authStore.ListMembers(ctx, org)
		out := make(map[string]string, len(ms))
		for _, m := range ms {
			out[m.UserID] = string(m.Role)
		}
		return out, err
	}}
	orch.SetApproverPolicy(pol)
	if notifySvc != nil {
		notifySvc.Approvers = func(ctx context.Context, org string, ap domain.Approval) (notify.ApprovalAudience, error) {
			o, ok, ids, err := pol.Audience(ctx, org, ap)
			if err != nil || !ok {
				return notify.ApprovalAudience{}, err
			}
			aud := notify.ApprovalAudience{Office: true, Private: o.Visibility == offices.Private}
			if len(ids) == 0 {
				return aud, nil
			}
			ms, err := authStore.ListMembers(ctx, org)
			if err != nil {
				return aud, err
			}
			for _, m := range ms {
				if m.Email != "" && slices.Contains(ids, m.UserID) {
					aud.Emails = append(aud.Emails, m.Email)
				}
			}
			return aud, nil
		}
	}
}
