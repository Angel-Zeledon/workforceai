package officeaudience_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

type stubDebater struct{ fail bool }

func (s stubDebater) Debate(_ context.Context, in application.DebateRequest) (application.DebateResponse, error) {
	if s.fail {
		return application.DebateResponse{}, errors.New("boom")
	}
	turns := []application.DebateTurnOut{{AgentID: in.Participants[0].ID, Kind: domain.DebateTurnPosition, Text: "a"},
		{AgentID: in.Participants[1].ID, Kind: domain.DebateTurnPosition, Text: "b"},
		{AgentID: "assistant", Kind: domain.DebateTurnSynthesis, Text: "s"}}
	return application.DebateResponse{Options: []string{"x", "y"}, Turns: turns, Synthesis: domain.DebateSynthesis{Choice: "x", Rationale: "r"}}, nil
}

// A debate with no request (a person asked for it) is about the work of its participants: debate.started,
// debate.completed and debate.failed carry the topic and the participants, so they reach the members of the private
// office of a participant and whoever opened the debate, not the rest of the organization.
func TestDebateEventsAreSealedToTheirParticipants(t *testing.T) {
	f := newFix(t)
	f.install(nil)
	for _, fail := range []bool{false, true} {
		d := &application.Debates{Store: f.store, Agents: f.store, Runtime: stubDebater{fail: fail}, Rec: f.rec, Cfg: application.DefaultConfig()}
		ctx := application.WithActor(f.ctx, "u-boss")
		f.got()
		_, _ = d.Run(ctx, application.DebateInput{Topic: "SECRETO-tema", AgentIDs: []string{"legal", "operations"}})
		g := f.got()
		for u, want := range map[string]bool{"u-pri": true, "u-boss": true, "u-ana": false, "u-eve": false} {
			started := slices.Contains(g[u], application.EvDebateStarted)
			ended := slices.Contains(g[u], application.EvDebateCompleted) || slices.Contains(g[u], application.EvDebateFailed)
			if started != want || ended != want {
				t.Fatalf("fail=%v: %s got started=%v ended=%v, want %v (%v)", fail, u, started, ended, want, g)
			}
		}
		// the same debate between agents of General and an open office stays visible to everybody
		_, _ = d.Run(ctx, application.DebateInput{Topic: "abierto", AgentIDs: []string{"sales", "operations"}})
		g = f.got()
		for _, u := range []string{"u-ana", "u-eve", "u-boss", "u-pri"} {
			if !slices.Contains(g[u], application.EvDebateStarted) {
				t.Fatalf("fail=%v: the open debate did not reach %s: %v", fail, u, g)
			}
		}
	}
}
