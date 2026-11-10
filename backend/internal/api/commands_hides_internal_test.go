package api

import (
	"context"
	"errors"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

type agentsDown struct{ application.Store }

func (agentsDown) ListAgents(context.Context, string) ([]domain.Agent, error) {
	return nil, errors.New("store down")
}

// D3: when the agents cannot be read, /compact cannot tell whether the person sees them all, so it fails closed: it
// treats some agent as hidden (the shared summary is not carried over).
func TestHidesAnyAgentFailsClosed(t *testing.T) {
	c := &cmdCall{s: &server{Deps: Deps{Store: agentsDown{}}}, ctx: context.Background(), org: "org1"}
	if !c.hidesAnyAgent() {
		t.Fatal("an unreadable agent list must count as hiding some agent")
	}
}
