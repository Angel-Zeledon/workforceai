package rewards

import (
	"context"
	"sync/atomic"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Observer decorates the event publisher (like push.Notifier): every event
// goes through unchanged and first; task.completed and project.status_changed
// additionally trigger the earning rules in a background goroutine, so
// earning never blocks or slows the execution of tasks. Until Bind is called
// (or with a nil service) it is a plain pass-through.
type Observer struct {
	Inner application.Publisher
	svc   atomic.Pointer[Service]
}

// Bind installs the service (the Recorder that the service emits through
// publishes through this very observer, hence the late binding).
func (o *Observer) Bind(s *Service) { o.svc.Store(s) }

// Publish implements application.Publisher.
func (o *Observer) Publish(ctx context.Context, e domain.Event) error {
	err := o.Inner.Publish(ctx, e)
	svc := o.svc.Load()
	if svc == nil || !svc.Rules.Enabled || (e.Type != domain.EvTaskCompleted && e.Type != "project.status_changed") {
		return err
	}
	switch p := e.Payload.(type) {
	case map[string]any:
		switch e.Type {
		case domain.EvTaskCompleted:
			if t, ok := p["task"].(domain.Task); ok {
				go svc.background(ctx, func(c context.Context) { svc.OnTaskCompleted(c, e.OrgID, t) })
			}
		default:
			if proj, ok := projectOf(p["project"]); ok && proj.status == "done" {
				go svc.background(ctx, func(c context.Context) { svc.OnProjectDone(c, e.OrgID, proj.id, proj.name, proj.done) })
			}
		}
	}
	return err
}

func (s *Service) background(ctx context.Context, fn func(context.Context)) {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			s.log().Error("rewards: panic while earning", "panic", r)
		}
	}()
	fn(c)
}

type projectView struct {
	id, name, status string
	done             int
}

// ProjectSummary is implemented by the project Summary type through a small
// adapter (kept as an interface so this package does not import projects).
type ProjectSummary interface {
	RewardView() (id, name, status string, tasksDone int)
}

func projectOf(v any) (projectView, bool) {
	ps, ok := v.(ProjectSummary)
	if !ok {
		return projectView{}, false
	}
	id, name, status, done := ps.RewardView()
	return projectView{id, name, status, done}, id != ""
}
