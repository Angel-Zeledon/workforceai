package auth

import (
	"context"
	"sort"
	"time"
)

func (s *MemoryStore) CreateOrg(_ context.Context, o Org, m Membership) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.slugs[o.Slug]; ok {
		return ErrSlugTaken
	}
	if _, ok := s.users[m.UserID]; !ok {
		return ErrNotFound
	}
	s.orgs[o.ID] = o
	s.slugs[o.Slug] = o.ID
	m.OrgID = o.ID
	s.memberships[[2]string{o.ID, m.UserID}] = m
	return nil
}

func (s *MemoryStore) PutOrgSetup(_ context.Context, st OrgSetup) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.orgs[st.OrgID]; !ok {
		return ErrNotFound
	}
	s.setups[st.OrgID] = st
	return nil
}

func (s *MemoryStore) GetOrgSetup(_ context.Context, orgID string) (OrgSetup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.setups[orgID]
	if !ok {
		return OrgSetup{}, ErrNotFound
	}
	return st, nil
}

func (s *MemoryStore) MarkOrgSetupDone(_ context.Context, orgID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.setups[orgID]
	if !ok {
		return ErrNotFound
	}
	st.SetupDoneAt = at
	s.setups[orgID] = st
	return nil
}

func (s *MemoryStore) PutMemberProfile(_ context.Context, p MemberProfile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.orgs[p.OrgID]; !ok {
		return ErrNotFound
	}
	k := [2]string{p.OrgID, p.UserID}
	if cur, ok := s.profiles[k]; ok && !cur.CreatedAt.IsZero() {
		p.CreatedAt = cur.CreatedAt
	}
	s.profiles[k] = p
	return nil
}

func (s *MemoryStore) GetMemberProfile(_ context.Context, orgID, userID string) (MemberProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.profiles[[2]string{orgID, userID}]
	if !ok {
		return MemberProfile{}, ErrNotFound
	}
	return p, nil
}

func (s *MemoryStore) ListMemberProfiles(_ context.Context, orgID string) ([]MemberProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []MemberProfile
	for k, p := range s.profiles {
		if k[0] == orgID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt) || (out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].UserID < out[j].UserID)
	})
	return out, nil
}

func (s *MemoryStore) PutSignupIntent(_ context.Context, in SignupIntent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[in.UserID]; !ok {
		return ErrNotFound
	}
	s.intents[in.UserID] = in
	return nil
}

func (s *MemoryStore) TakeSignupIntent(_ context.Context, userID string) (SignupIntent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.intents[userID]
	if !ok {
		return SignupIntent{}, ErrNotFound
	}
	delete(s.intents, userID)
	return in, nil
}
