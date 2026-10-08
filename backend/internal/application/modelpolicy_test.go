package application_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
)

func policySvc(ceiling string) (*application.ModelPolicyService, *memory.Store) {
	st := memory.New()
	rec := &application.Recorder{OrgID: domain.DemoOrgID, Store: st}
	return &application.ModelPolicyService{Store: st, Rec: rec, Cfg: application.Config{OrgID: domain.DemoOrgID},
		Ceiling: application.ParseProviderList(ceiling), TTL: time.Hour}, st
}

func TestModelPolicyDefaultSendsNothing(t *testing.T) {
	s, _ := policySvc("")
	if p := s.Runtime(context.Background()); !p.Empty() {
		t.Fatalf("no policy and no ceiling must send nothing, got %+v", p)
	}
}

func TestModelPolicyCeilingAlwaysSent(t *testing.T) {
	s, _ := policySvc("anthropic, custom")
	p := s.Runtime(context.Background())
	if !slices.Equal(p.AllowedProviders, []string{"anthropic", "custom"}) {
		t.Fatalf("ceiling not applied: %+v", p)
	}
}

func TestModelPolicyPutValidatesAndAudits(t *testing.T) {
	s, st := policySvc("deepseek,anthropic")
	ctx := context.Background()
	bad := []application.ModelPolicy{
		{AllowedProviders: []string{"openai"}},
		{AllowedProviders: []string{"custom"}},                                              // outside the server ceiling
		{AllowedProviders: []string{"anthropic"}, PreferredProviders: []string{"deepseek"}}, // preferred outside allowed
		{RoleModels: map[string]string{"legal": "claude-opus-5-5"}},                         // no provider prefix
		{RoleModels: map[string]string{"legal": "deepseek/claude-opus-5-5"}},                // wrong provider for a known model
		{RoleModels: map[string]string{"Le gal!": "anthropic/claude-opus-5-5"}},             // invalid role key
		{RoleProviders: map[string][]string{"legal": {"custom"}}},
	}
	for i, p := range bad {
		if _, err := s.Put(ctx, p); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("case %d: want ErrInvalid, got %v", i, err)
		}
	}
	v, err := s.Put(ctx, application.ModelPolicy{
		AllowedProviders:   []string{" Anthropic ", "deepseek"},
		PreferredProviders: []string{"anthropic"},
		RoleProviders:      map[string][]string{"legal": {"anthropic"}},
		RoleModels:         map[string]string{"legal": "anthropic/claude-opus-5-5", "sales": "anthropic/claude-new-model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(v.Policy.AllowedProviders, []string{"anthropic", "deepseek"}) || v.Policy.UpdatedAt == nil {
		t.Fatalf("normalized policy: %+v", v.Policy)
	}
	if len(v.Warnings) != 1 {
		t.Fatalf("an unpriced model must produce one warning: %v", v.Warnings)
	}
	found := false
	for _, a := range st.Audit() {
		found = found || a.Action == "settings.model_policy_updated"
	}
	if !found {
		t.Fatal("policy change not audited")
	}
	p := s.Runtime(ctx)
	if !slices.Equal(p.AllowedProviders, []string{"anthropic", "deepseek"}) || p.RoleModels["legal"] != "anthropic/claude-opus-5-5" ||
		!slices.Equal(p.RoleProviders["legal"], []string{"anthropic"}) || !slices.Equal(p.PreferredProviders, []string{"anthropic"}) {
		t.Fatalf("runtime payload: %+v", p)
	}
}

// If the operator narrows the ceiling after the policy was saved, the
// intersection is sent; an empty intersection blocks the call, never widens it.
func TestModelPolicyNarrowedCeilingNeverWidens(t *testing.T) {
	s, st := policySvc("")
	ctx := context.Background()
	if _, err := s.Put(ctx, application.ModelPolicy{AllowedProviders: []string{"deepseek"}}); err != nil {
		t.Fatal(err)
	}
	narrowed := &application.ModelPolicyService{Store: st, Cfg: application.Config{OrgID: domain.DemoOrgID}, Ceiling: []string{"anthropic"}}
	if p := narrowed.Runtime(ctx); !slices.Equal(p.AllowedProviders, []string{"none"}) {
		t.Fatalf("empty intersection must send none, got %+v", p)
	}
}
