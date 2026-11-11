package main

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/memory"
	"aiworkforce/backend/internal/notify"
	"aiworkforce/backend/internal/officeaudience"
	"aiworkforce/backend/internal/offices"
	"aiworkforce/backend/internal/orgchart"
	"aiworkforce/backend/internal/skills"
	"aiworkforce/backend/internal/slackapp"
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

// departmentWiring is what wireDepartments seals (nil parts are skipped).
type departmentWiring struct {
	AuthOn    bool
	PG        *postgres.Store
	Store     application.Store
	AuthStore auth.Store
	Chart     *orgchart.Service
	Rec       *application.Recorder
	Memory    *memory.Service
	Orch      *application.Orchestrator
	Notify    *notify.Service
	Slack     *slackapp.Service
	Conns     *connections.Service
	Drive     *drive.Service
	Skills    *skills.Service
	Log       *slog.Logger
}

// wireDepartments builds the offices (E2) and installs everything D3 seals a private department with: the office
// scope of the memory, who decides and sees its approvals (web, e-mail, Slack), its connections, its drive folders,
// its skills and who receives its live events. It runs BEFORE orch.Recover (TestDepartmentsAreWiredBeforeRecover):
// a request resumed by durable execution emits events, asks for approvals and writes memory right away.
func wireDepartments(w departmentWiring) *offices.Service {
	svc := wireOffices(w.PG, w.Store, w.AuthStore, w.Chart, w.Rec, noteCleaner(w.Memory))
	wireApprovers(w.Orch, w.Notify, w.Slack, svc, w.AuthStore, w.AuthOn) // D3: an office's approvals go to its responsibles
	officeRecall(w.Memory, svc)
	if svc == nil {
		return nil
	}
	if w.Conns != nil {
		w.Conns.AgentOffice = agentOffices{svc: svc}.AgentOffice // D3: a department's connections serve its agents only
	}
	if w.AuthOn && w.Drive != nil {
		w.Drive.SetPeople(officePeople{svc: svc}) // office folders: member edits, open reads, private none
		// agents work in their office folder and never beyond the person they work for
		w.Drive.SetAgentOffices(agentOffices{svc: svc})
	}
	if w.Skills != nil {
		w.Skills.SetOffices(svc.Model) // D3: skills of a department follow its visibility
	}
	if w.AuthOn && w.Rec != nil { // D3 (internal/officeaudience): a private office's live events reach its members only
		orch := w.Orch
		w.Rec.SetAudienceFor(officeaudience.New(svc, w.Store, func(c context.Context, org, id string) string {
			return orch.RequestedBy(application.WithOrg(c, org), id)
		}, w.Log))
	}
	return svc
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
	// D3: what the agents of a department learn stays in the department; General's agents (and unknown ones) keep
	// writing organization memory. An unreadable model is an error (the memory writes nothing), never "General".
	m.SetOfficeScope(func(ctx context.Context, org, agentID string) (string, error) {
		return agentOffices{svc: svc}.AgentOffice(ctx, org, agentID)
	}, func(ctx context.Context, org, office string) (bool, error) {
		model, err := svc.Model(ctx, org)
		if err != nil || model == nil {
			return true, errors.New("offices model unavailable")
		}
		o, ok := model.Office(office)
		return !ok || o.Visibility == offices.Private, nil
	})
	// I6: an agent of a private office working for a person who is not a member recalls nothing of the office
	m.SetOfficeMembers(func(ctx context.Context, org, office, userID string) (bool, error) {
		model, err := svc.Model(ctx, org)
		if err != nil || model == nil {
			return false, errors.New("offices model unavailable")
		}
		return model.IsMember(userID, office), nil
	})
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
func wireApprovers(orch *application.Orchestrator, notifySvc *notify.Service, slackSvc *slackapp.Service, svc *offices.Service, authStore auth.Store, authOn bool) {
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
	if slackSvc != nil {
		// The two-way Slack app: a private office's approvals never reach the company channel (an office that cannot
		// be told counts as private), /oficina pendientes lists what the person sees, the responsible decides.
		slackSvc.SetOffices(slackapp.OfficeRule{Policy: pol, Private: func(ctx context.Context, org string, ap domain.Approval) (bool, error) {
			o, ok, _, err := pol.Audience(ctx, org, ap)
			if err != nil {
				return true, err
			}
			return ok && o.Visibility == offices.Private, nil
		}})
	}
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
