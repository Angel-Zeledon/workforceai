package memory

import (
	"context"
	"sort"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var _ application.LLMStore = (*Store)(nil)

// llmState holds the organizations' model credentials (sealed) and active provider.
type llmState struct {
	creds  map[string]map[string]application.LLMCredentialRecord // org -> provider
	active map[string]string                                     // org -> provider
}

func (s *Store) ListLLMCredentials(_ context.Context, orgID string) ([]application.LLMCredentialRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []application.LLMCredentialRecord{}
	for _, r := range s.llm.creds[orgID] {
		r.Sealed = append([]byte(nil), r.Sealed...)
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out, nil
}

func (s *Store) PutLLMCredential(_ context.Context, orgID string, r application.LLMCredentialRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.llm.creds == nil {
		s.llm.creds = map[string]map[string]application.LLMCredentialRecord{}
	}
	if s.llm.creds[orgID] == nil {
		s.llm.creds[orgID] = map[string]application.LLMCredentialRecord{}
	}
	r.Sealed = append([]byte(nil), r.Sealed...)
	s.llm.creds[orgID][r.Provider] = r
	return nil
}

func (s *Store) DeleteLLMCredential(_ context.Context, orgID, provider string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.llm.creds[orgID][provider]; !ok {
		return domain.ErrNotFound
	}
	delete(s.llm.creds[orgID], provider)
	return nil
}

func (s *Store) RecordLLMTest(_ context.Context, orgID, provider, code string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.llm.creds[orgID][provider]
	if !ok {
		return nil
	}
	r.LastTestCode, r.LastTestAt = code, &at
	s.llm.creds[orgID][provider] = r
	return nil
}

func (s *Store) GetLLMActive(_ context.Context, orgID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.llm.active[orgID], nil
}

func (s *Store) PutLLMActive(_ context.Context, orgID, provider, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.llm.active == nil {
		s.llm.active = map[string]string{}
	}
	s.llm.active[orgID] = provider
	return nil
}
