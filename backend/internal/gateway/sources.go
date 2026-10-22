package gateway

import (
	"context"
	"slices"
	"sort"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/connections"
)

// MaxSources bounds the sources listed in one run (the prompt stays small).
const MaxSources = 20

var _ application.SourceLister = (*Gateway)(nil)

// SourcesFor implements application.SourceLister: every active connection the
// agent holds an active grant on, with the READ tools both the grant and the
// connection allow. Writes are not listed: they are actions, not sources.
func (g *Gateway) SourcesFor(ctx context.Context, org, agentID string) []application.SourceRef {
	gs, err := g.Conns.Store.ListGrantsByAgent(ctx, org, agentID)
	if err != nil {
		return nil
	}
	now := g.now()
	var out []application.SourceRef
	for _, gr := range gs {
		if gr.Status != connections.GrantActive || now.Before(gr.ValidFrom) || (gr.ValidUntil != nil && now.After(*gr.ValidUntil)) {
			continue
		}
		cn, err := g.Conns.Store.GetConnection(ctx, org, gr.ConnectionID)
		if err != nil || cn.Status != connections.StatusActive {
			continue
		}
		m, ok := g.Conns.Manifest(cn.Provider)
		if !ok || (cn.Provider == connections.SourceProvider && cn.Source == nil) {
			continue // a generic source without its configuration has nothing to query yet
		}
		var tools []string
		for _, cp := range gr.Capabilities {
			spec, ok := m.Capability(cp)
			if !ok || spec.SideEffects || !slices.Contains(cn.GrantedCapabilities, cp) {
				continue
			}
			tools = append(tools, spec.Tools...)
		}
		if len(tools) == 0 {
			continue
		}
		sort.Strings(tools)
		ref := application.SourceRef{Alias: gr.Alias, Label: cn.Label, Provider: cn.Provider, Tools: slices.Compact(tools),
			Simulated: cn.Mode == connections.ModeSimulated}
		if src := cn.Source; src != nil {
			ref.Description = src.Description
			for _, e := range src.Endpoints {
				ref.Endpoints = append(ref.Endpoints, application.SourceEndpoint{Name: e.Name, Description: e.Description, Params: e.Params})
			}
		}
		out = append(out, ref)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	if len(out) > MaxSources {
		out = out[:MaxSources]
	}
	return out
}
