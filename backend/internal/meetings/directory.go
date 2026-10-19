package meetings

import (
	"context"
	"sort"
	"strings"
)

// PersonInfo is a member of the organization who can be invited.
type PersonInfo struct{ ID, Name, Role string }

// People lists the members of the organization (the same directory the people chat uses).
type People interface {
	People(ctx context.Context, org string) ([]PersonInfo, error)
}

// DirectoryEntry is one invitable attendee: a person or an agent of the organization.
type DirectoryEntry struct {
	Kind string `json:"kind"` // person | agent
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

// Directory returns the people and agents that can be picked as attendees, people first, each group by name.
// A missing port just yields an empty group (the UI then falls back to typing a guest's e-mail).
func (s *Service) Directory(ctx context.Context, org string) ([]DirectoryEntry, error) {
	var out []DirectoryEntry
	if s.People != nil {
		list, err := s.People.People(ctx, org)
		if err != nil {
			return nil, err
		}
		for _, p := range list {
			if p.ID != "" {
				out = append(out, DirectoryEntry{Kind: KindPerson, ID: p.ID, Name: oneLine(p.Name, 80), Role: p.Role})
			}
		}
	}
	if s.Agents != nil {
		list, err := s.Agents.Agents(ctx, org)
		if err != nil {
			return nil, err
		}
		for _, a := range list {
			if a.ID != "" {
				out = append(out, DirectoryEntry{Kind: KindAgent, ID: a.ID, Name: oneLine(a.Name, 80), Role: a.Role})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == KindPerson
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}
