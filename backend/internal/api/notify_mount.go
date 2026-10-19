package api

import (
	"context"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/notify"
)

// Approvals from outside the app, notification channels, the free-plan cap and
// the daily goal (docs/architecture/approvals-notify.md).
//
//	GET  /notify/a?t=...           public  confirmation page of a signed link (changes nothing)
//	POST /notify/a                 public  confirms and decides (single-use token)
//	GET  /notify/settings          approvals:decide
//	PUT  /notify/settings          approvals:decide
//	POST /notify/test              approvals:decide
//	GET  /plan/usage               metrics:read    spent / estimated / cap of the plan
//	GET  /rewards/daily-goal       shop:read
func (s *server) mountNotifyPublic(r chi.Router) {
	if s.Notify == nil {
		return
	}
	r.Handle("/notify/a", s.Notify.Landing())
}

func (s *server) notifyAPI() notify.API {
	return notify.API{
		Svc:   s.Notify,
		Org:   s.org,
		Actor: s.actor,
		Fail:  s.fail,
		Write: writeJSON,
		Usage: func(ctx context.Context, org string) (float64, float64, float64, error) {
			st, err := s.Orch.Budget().Status(ctx)
			if err != nil {
				return 0, 0, 0, err
			}
			return s.Cfg.BudgetUSD, st.Org.UsedUSD, st.Org.ReservedUSD, nil
		},
	}
}

func (s *server) mountNotify(r chi.Router) {
	if s.Notify != nil {
		a := s.notifyAPI()
		decide := s.can(auth.PermApprovalsDecide)
		r.With(decide).Get("/notify/settings", a.GetSettings)
		r.With(decide).Put("/notify/settings", a.PutSettings)
		r.With(decide).Post("/notify/test", a.Test)
		if s.Notify.Plans != nil {
			r.With(s.can(auth.PermMetricsRead)).Get("/plan/usage", a.PlanUsage)
		}
	}
	if s.Rewards != nil {
		r.With(s.can(auth.PermShopRead)).Get("/rewards/daily-goal", s.Rewards.DailyGoalHandler(s.org, writeJSON, s.fail))
	}
}
