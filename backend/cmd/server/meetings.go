package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/calendar"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/connections/googleauth"
	"aiworkforce/backend/internal/connections/meetingcal"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/meetings"
)

// Meetings (docs/architecture/meetings.md): the service plus the adapters that connect it to the rest of
// the platform. The calendars go through the connections vault, the summary through the existing
// runtime summarize call (budget reservation, usage ledger, simulation like every other call), the
// tasks through the orchestrator, the minutes file through the drive.
//
// Env (all optional): MEETINGS_JITSI_BASE_URL (default https://meet.jit.si; point it at your own Jitsi),
// MICROSOFT_OAUTH_CLIENT_ID / MICROSOFT_OAUTH_CLIENT_SECRET / MICROSOFT_OAUTH_TENANT (a deployment-wide
// Microsoft app; each connection can also bring its own app, which is the normal self-hosted setup).

// addMeetingCalendarProviders registers the Google Calendar and Microsoft 365 connection providers used by
// the Meetings screen. They share the deployment-wide Google OAuth app when there is one.
func addMeetingCalendarProviders(log *slog.Logger, providers map[string]connections.Provider, apps map[string]connections.OAuthApp, redirectURL string) error {
	g, err := meetingcal.NewGoogle(googleauth.Endpoints{})
	if err != nil {
		return err
	}
	ms, err := meetingcal.NewMicrosoft(meetingcal.MicrosoftConfig{Tenant: strings.TrimSpace(os.Getenv("MICROSOFT_OAUTH_TENANT"))})
	if err != nil {
		return err
	}
	providers[meetingcal.GoogleID], providers[meetingcal.MicrosoftID] = g, ms
	if app, ok := apps["google_calendar"]; ok {
		apps[meetingcal.GoogleID] = app
	}
	id, secret := strings.TrimSpace(os.Getenv("MICROSOFT_OAUTH_CLIENT_ID")), strings.TrimSpace(os.Getenv("MICROSOFT_OAUTH_CLIENT_SECRET"))
	if id != "" && secret != "" {
		apps[meetingcal.MicrosoftID] = connections.OAuthApp{ClientID: id, ClientSecret: secret, RedirectURL: redirectURL}
	} else {
		log.Info("MICROSOFT_OAUTH_CLIENT_ID/SECRET not set: Microsoft 365 calendar needs the connection's own OAuth app (bring your own); simulated connections work")
	}
	return nil
}

// wireMeetings builds the meetings service. The service always exists; every feature behind a missing
// port (calendars, drive, task planner) answers "not available" instead of failing at start-up.
func wireMeetings(log *slog.Logger, pg *postgres.Store, store application.Store, rec *application.Recorder, orch *application.Orchestrator, cw connWiring, dsvc *drive.Service) *meetings.Service {
	var st meetings.Store = meetings.NewMemStore()
	if pg != nil {
		st = &postgres.MeetingsStore{S: pg}
	}
	cfg := meetings.Config{
		Store:      st,
		Summarizer: meetingSummarizer{orch: orch},
		Tasks:      meetingTasks{orch: orch},
		Agents:     meetingAgents{store: store},
		JitsiBase:  strings.TrimSpace(os.Getenv("MEETINGS_JITSI_BASE_URL")),
		Audit: func(ctx context.Context, org, actor, action, entity, id string, d map[string]any) {
			rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: actor, Action: action, Entity: entity, EntityID: id, Details: d})
		},
		Emit: func(ctx context.Context, org, typ string, payload map[string]any) {
			rec.Emit(application.WithOrg(ctx, org), application.Action{Type: typ, Entity: "meeting", Payload: payload, SkipAudit: true})
		},
	}
	if dsvc != nil {
		cfg.Files = meetingFiles{drive: dsvc}
	}
	if cw.conns != nil {
		cfg.Calendars = &meetingCalendars{conns: cw.conns, ctl: cw.ctl, fakes: map[string]*calendar.Fake{}}
	}
	if cfg.JitsiBase != "" {
		if _, err := meetings.NormalizeJitsiBase(cfg.JitsiBase); err != nil {
			log.Warn("MEETINGS_JITSI_BASE_URL ignored: it must be an https URL", "err", err)
			cfg.JitsiBase = ""
		}
	}
	return meetings.New(cfg)
}

// ---- summary: the existing runtime summarize call (mode "meeting") ----

type meetingSummarizer struct{ orch *application.Orchestrator }

func (m meetingSummarizer) Summarize(ctx context.Context, org string, in meetings.SummaryIn) (meetings.SummaryOut, error) {
	items := make([]application.ChatHistoryItem, 0, len(in.Messages))
	for _, msg := range in.Messages {
		items = append(items, application.ChatHistoryItem{From: msg[0], Text: msg[1]})
	}
	res, err := m.orch.Summarize(application.WithOrg(ctx, org), "", application.SummarizeRequest{Mode: "meeting", Messages: items, Topic: in.Title,
		Participants: in.Participants, Locale: in.Locale})
	var ex *application.Exceeded
	if errors.As(err, &ex) {
		return meetings.SummaryOut{}, fmt.Errorf("%w: %v", meetings.ErrBudget, err)
	}
	if err != nil {
		return meetings.SummaryOut{}, err
	}
	return meetings.SummaryOut{Text: res.Text, Source: res.Source}, nil
}

// ---- tasks: one request with a task per item, the same path as /assign and /reunion ----

type meetingTasks struct{ orch *application.Orchestrator }

func (m meetingTasks) CreateTasks(ctx context.Context, org string, b meetings.TaskBatch) (string, error) {
	plan := application.PlanResponse{Objectives: b.Objectives}
	for _, t := range b.Tasks {
		plan.Tasks = append(plan.Tasks, application.PlannedTask{Key: t.Key, Title: t.Title, Description: t.Description, AgentID: t.AgentID,
			Reason: "Meeting follow-up"})
	}
	return m.orch.SubmitPlan(application.WithOrg(ctx, org), b.Text, plan)
}

// ---- minutes file in the drive ----

type meetingFiles struct{ drive *drive.Service }

func (m meetingFiles) Save(ctx context.Context, org, actor, path string, data []byte, message string) (string, string, error) {
	res, err := m.drive.Write(application.WithOrg(ctx, org), drive.Human(actor), drive.WriteInput{Path: path, Data: data, Message: message, Source: "meetings"})
	if err != nil {
		return "", "", err
	}
	return res.File.ID, res.File.Path, nil
}

// ---- agents ----

type meetingAgents struct{ store application.Store }

func (m meetingAgents) Agents(ctx context.Context, org string) ([]meetings.AgentInfo, error) {
	as, err := m.store.ListAgents(application.WithOrg(ctx, org), org)
	if err != nil {
		return nil, err
	}
	out := make([]meetings.AgentInfo, 0, len(as))
	for _, a := range as {
		out = append(out, meetings.AgentInfo{ID: a.ID, Name: a.Name, Role: string(a.Role)})
	}
	return out, nil
}

// ---- calendars through the connections vault ----

type meetingCalendars struct {
	conns *connections.Service
	ctl   *controls.Service

	mu    sync.Mutex
	fakes map[string]*calendar.Fake // simulated connections
}

var meetingProviderIDs = []string{meetingcal.GoogleID, meetingcal.MicrosoftID}

func (c *meetingCalendars) List(ctx context.Context, org string) ([]meetings.CalendarInfo, error) {
	var out []meetings.CalendarInfo
	for _, p := range meetingProviderIDs {
		cs, err := c.conns.List(ctx, org, p, connections.StatusActive)
		if err != nil {
			return nil, err
		}
		for _, cn := range cs {
			out = append(out, meetings.CalendarInfo{ConnectionID: cn.ID, Provider: cn.Provider, Label: cn.Label, Account: cn.AccountLabel,
				Class: cn.Class, Status: cn.Status, Simulated: cn.Mode == connections.ModeSimulated})
		}
	}
	return out, nil
}

func (c *meetingCalendars) Client(ctx context.Context, org, id string) (calendar.Client, error) {
	cn, err := c.conns.Get(ctx, org, id)
	if err != nil {
		return nil, calendar.ErrNotConfigured
	}
	if (cn.Provider != meetingcal.GoogleID && cn.Provider != meetingcal.MicrosoftID) || cn.Status != connections.StatusActive {
		return nil, calendar.ErrNotConfigured
	}
	var inner calendar.Client
	if cn.Mode == connections.ModeSimulated {
		c.mu.Lock()
		f, ok := c.fakes[cn.ID]
		if !ok {
			kind := calendar.ProviderGoogle
			if cn.Provider == meetingcal.MicrosoftID {
				kind = calendar.ProviderMicrosoft
			}
			f = calendar.NewFake(kind)
			c.fakes[cn.ID] = f
		}
		c.mu.Unlock()
		inner = f
	} else {
		h := c.conns.Handle(cn)
		if cn.Provider == meetingcal.GoogleID {
			inner = &calendar.Google{HTTP: h.HTTP}
		} else {
			inner = &calendar.Microsoft{HTTP: h.HTTP}
		}
	}
	return &guardedCalendar{inner: inner, ctl: c.ctl, conns: c.conns, org: org, id: cn.ID, canWrite: cn.Class == "write" && !cn.ReadOnly}, nil
}

// guardedCalendar applies the platform controls to the person-driven calendar calls: the kill switch
// stops every call, read-only mode and read-class connections stop the writes, and a rejected token
// flags the connection for re-authorization.
type guardedCalendar struct {
	inner    calendar.Client
	ctl      *controls.Service
	conns    *connections.Service
	org, id  string
	canWrite bool
}

func (g *guardedCalendar) Provider() string { return g.inner.Provider() }

func (g *guardedCalendar) check(ctx context.Context, write bool) error {
	if write && !g.canWrite {
		return calendar.ErrReadOnly
	}
	if g.ctl != nil && !g.ctl.Check(ctx, g.org, "", write).Allowed {
		return calendar.ErrBlocked
	}
	return nil
}

func (g *guardedCalendar) after(ctx context.Context, err error) error {
	if errors.Is(err, calendar.ErrReauth) {
		g.conns.MarkNeedsReauth(context.WithoutCancel(ctx), g.org, g.id)
	}
	return err
}

func (g *guardedCalendar) Events(ctx context.Context, from, to time.Time) ([]calendar.Event, error) {
	if err := g.check(ctx, false); err != nil {
		return nil, err
	}
	ev, err := g.inner.Events(ctx, from, to)
	return ev, g.after(ctx, err)
}

func (g *guardedCalendar) Create(ctx context.Context, in calendar.Input) (calendar.Event, error) {
	if err := g.check(ctx, true); err != nil {
		return calendar.Event{}, err
	}
	ev, err := g.inner.Create(ctx, in)
	return ev, g.after(ctx, err)
}

func (g *guardedCalendar) Update(ctx context.Context, id string, in calendar.Input) (calendar.Event, error) {
	if err := g.check(ctx, true); err != nil {
		return calendar.Event{}, err
	}
	ev, err := g.inner.Update(ctx, id, in)
	return ev, g.after(ctx, err)
}

func (g *guardedCalendar) Delete(ctx context.Context, id string) error {
	if err := g.check(ctx, true); err != nil {
		return err
	}
	return g.after(ctx, g.inner.Delete(ctx, id))
}

func (g *guardedCalendar) FreeBusy(ctx context.Context, from, to time.Time, who []string) (map[string][]calendar.Busy, error) {
	if err := g.check(ctx, false); err != nil {
		return nil, err
	}
	fb, err := g.inner.FreeBusy(ctx, from, to, who)
	return fb, g.after(ctx, err)
}
